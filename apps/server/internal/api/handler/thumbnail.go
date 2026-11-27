package handler

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

const (
	thumbDefaultSize = 256
	thumbMaxSize     = 512
	thumbQuality     = 82
)

var imageExts = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {},
	".tif": {}, ".tiff": {}, ".webp": {},
}

// heicExts brauchen einen externen Decoder (libheif) — landen nicht in
// imageExts, weil image.Decode sie nicht öffnet.
var heicExts = map[string]struct{}{
	".heic": {}, ".heif": {},
}

var comicExts = map[string]struct{}{
	".cbz": {}, ".cbr": {},
}

var videoExts = map[string]struct{}{
	".mp4": {}, ".webm": {}, ".mov": {}, ".m4v": {}, ".ogv": {},
}

// GET /api/v1/sync-roots/{rootID}/files/thumbnail?path=/foo.png&size=128
// Liefert ein quadratisches, mittig zugeschnittenes JPEG-Thumbnail.
// Cache liegt unter <data>/thumbs/<userID>/<rootID>/<hash>_<size>.jpg —
// pro Content-Hash, sodass identische Inhalte denselben Thumb teilen.
func (h *FileHandler) Thumbnail(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}
	h.serveThumbnail(w, r, claims.UserID, root.ID, r.URL.Query().Get("path"))
}

// GET /api/v1/shared-mounts/{shareID}/thumbnail?path=/foo.png&size=128
// Recipient-side analogue of Thumbnail. Resolves the share, validates that
// the caller is the target, then renders the thumb using the giver's
// storage scope so the cache is shared across both surfaces.
func (h *FileHandler) MountThumbnail(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMountForThumb(w, r)
	if share == nil {
		return
	}
	target, ok := mountSubPath(w, r, share, false)
	if !ok {
		return
	}
	h.serveThumbnail(w, r, share.UserID, *share.SyncRootID, target)
}

// resolveMountForThumb mirrors ShareHandler.resolveMount; duplicated so
// the thumbnail endpoint can live on FileHandler (which already owns
// the heavy thumb rendering) without dragging in ShareHandler.
func (h *FileHandler) resolveMountForThumb(w http.ResponseWriter, r *http.Request) *model.Share {
	claims := middleware.ClaimsFromContext(r.Context())
	// Mount id is the recipient's grant id (ADR-0015); resolve scoped to the
	// caller and overlay the grant's can_write/expires_at.
	grantID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidShareID, "invalid share id")
		return nil
	}
	share, err := h.shares.FindMountByGrant(r.Context(), grantID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return nil
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return nil
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		writeError(w, http.StatusGone, ErrShareExpired, "share expired")
		return nil
	}
	return share
}

func (h *FileHandler) serveThumbnail(w http.ResponseWriter, r *http.Request, userID uuid.UUID, rootID uuid.UUID, filePath string) {
	cleanPath, err := sanitizePath(filePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}

	dbFile, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if !isLiveFile(dbFile, err) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	ext := strings.ToLower(filepath.Ext(cleanPath))
	_, isImage := imageExts[ext]
	_, isVideo := videoExts[ext]
	_, isHEIC := heicExts[ext]
	_, isComic := comicExts[ext]
	isSVG := ext == ".svg"
	isPDF := ext == ".pdf"
	is3MF := ext == ".3mf"
	isMesh := ext == ".stl" || ext == ".obj" || ext == ".ply"
	if !isImage && !isVideo && !isSVG && !isPDF && !is3MF && !isMesh && !isHEIC && !isComic {
		writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
			"format not supported for thumbnails")
		return
	}

	// SVG ist Vektorformat, klein -> 1:1 ausliefern statt rasterisieren.
	if isSVG {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		if err := h.storage.ServeFile(userID, rootID.String(), cleanPath, w); err != nil {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		}
		return
	}
	// Video braucht externes ffmpeg. Wenn nicht installiert -> 415, Client
	// fällt auf das Datei-Icon zurück.
	if isVideo {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"video thumbnails require ffmpeg")
			return
		}
	}
	// PDF nutzt pdftoppm (poppler-utils) — fehlt das Binary, fällt der
	// Client auf das Datei-Icon zurück (gleicher Vertrag wie bei Video).
	if isPDF {
		if _, err := exec.LookPath("pdftoppm"); err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"pdf thumbnails require pdftoppm")
			return
		}
	}
	// HEIC braucht libheif (heif-convert) — kein pure-Go-Decoder verfügbar.
	if isHEIC {
		if _, err := exec.LookPath("heif-convert"); err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"heic thumbnails require heif-convert")
			return
		}
	}
	// CBR braucht externes bsdtar (libarchive). CBZ läuft via stdlib archive/zip.
	if isComic && ext == ".cbr" {
		if _, err := exec.LookPath("bsdtar"); err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"cbr thumbnails require bsdtar")
			return
		}
	}

	size := thumbDefaultSize
	if s := r.URL.Query().Get("size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > thumbMaxSize {
				n = thumbMaxSize
			}
			size = n
		}
	}
	// fit=contain skaliert ohne Crop und behält die Original-Aspect-Ratio —
	// die UI-Vorschau soll das Bild nicht zentriert beschneiden.
	fit := "square"
	if f := strings.ToLower(r.URL.Query().Get("fit")); f == "contain" {
		fit = "contain"
	}
	// Source dekodieren — output-Format hängt vom dekodierten Bild ab
	// (echte Alpha-Pixel -> PNG, sonst JPEG), nicht von der Datei-Endung.
	var src image.Image
	if isVideo {
		frame, err := extractVideoFrame(h.storage, userID, rootID.String(), cleanPath)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"video decode failed")
			return
		}
		src = frame
	} else if isPDF {
		frame, err := extractPDFFirstPage(h.storage, userID, rootID.String(), cleanPath)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"pdf decode failed")
			return
		}
		src = frame
	} else if is3MF {
		// 3MF ist ZIP — Slicer-Projektbild bevorzugen, sonst Icon-Fallback.
		// (Mesh-Render aus 3D/3dmodel.model übersteigt das Budget hier.)
		img, err := extractZipThumb(h.storage, userID, rootID.String(), cleanPath)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"3mf has no embedded thumbnail")
			return
		}
		src = img
	} else if isMesh {
		img, err := renderMesh3D(h.storage, userID, rootID.String(), cleanPath, thumbMaxSize)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"mesh render failed")
			return
		}
		src = img
	} else if isHEIC {
		img, err := decodeHEIC(h.storage, userID, rootID.String(), cleanPath)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"heic decode failed")
			return
		}
		src = img
	} else if isComic {
		img, err := extractFirstComicImage(h.storage, userID, rootID.String(), cleanPath, ext)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"comic extract failed")
			return
		}
		src = img
	} else {
		var buf bytes.Buffer
		if err := h.storage.ServeFile(userID, rootID.String(), cleanPath, &buf); err != nil {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
			return
		}
		decoded, _, err := image.Decode(&buf)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_THUMB_FORMAT",
				"image decode failed")
			return
		}
		src = decoded
	}

	var thumb image.Image
	if fit == "contain" {
		thumb = containThumbnail(src, size)
	} else {
		thumb = squareThumbnail(src, size)
	}
	useAlpha := imageHasTransparency(thumb)
	outExt := "jpg"
	outMime := "image/jpeg"
	if useAlpha {
		outExt = "png"
		outMime = "image/png"
	}

	hashHex := hex.EncodeToString(dbFile.ContentHash)
	cacheDir := filepath.Join(h.storage.DataPath(), "thumbs", "v2", userID.String(), rootID.String())
	cachePath := filepath.Join(cacheDir, hashHex+"_"+strconv.Itoa(size)+"_"+fit+"."+outExt)

	// ETag bindet content-hash + size + fit + mime — Browser revalidieren so
	// bei jedem Parameter-Wechsel statt eine veraltete Antwort aus dem disk
	// cache zu liefern.
	etag := `"` + hashHex + "-" + strconv.Itoa(size) + "-" + fit + "-" + outExt + `"`
	w.Header().Set("Content-Type", outMime)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// cachePath/cacheDir are built only from hex(content-hash), the caller's
	// uuid, an int size and a whitelisted fit/ext — no user-supplied path
	// segment reaches them, so the G703 taint below is a false positive.
	if f, err := os.Open(cachePath); err == nil { //#nosec G703
		var hdr [8]byte
		n, _ := f.Read(hdr[:])
		if matchesMagic(outMime, hdr[:n]) {
			if _, err := f.Seek(0, io.SeekStart); err == nil {
				defer f.Close()
				_, _ = io.Copy(w, f)
				return
			}
		}
		_ = f.Close()
		_ = os.Remove(cachePath) //#nosec G703 -- see cachePath note above
	}

	if err := os.MkdirAll(cacheDir, 0o755); err == nil { //#nosec G703 -- see cachePath note above
		if cf, err := os.Create(cachePath); err == nil { //#nosec G703 -- see cachePath note above
			_ = encodeThumb(cf, thumb, useAlpha)
			_ = cf.Close()
		}
	}

	_ = encodeThumb(w, thumb, useAlpha)
}

// imageHasTransparency scannt alle Pixel. Bei einem 256er-Thumb sind das
// 65k Lookups — < 1 ms. Stichproben verfehlen Transparenz, die nicht an den
// Ecken liegt (z.B. Logos mit opaken Rändern und transparentem Innenraum).
func imageHasTransparency(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xFFFF {
				return true
			}
		}
	}
	return false
}

// extractVideoFrame ruft ffmpeg, um einen Frame des Videos als PNG zu lesen.
// ffmpeg liest aus einer Temp-Datei (Streaming-Decode aus stdin scheitert
// häufig an mp4 mit hinten liegender moov-Atom) und schreibt das PNG auf
// stdout, das wir direkt decodieren.
func extractVideoFrame(s storageReader, userID uuid.UUID, rootID, filePath string) (image.Image, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "priva-thumb-*.bin")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := s.ServeFile(userID, rootID, filePath, tmp); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	_ = tmp.Close()

	cmd := exec.Command(bin,
		"-loglevel", "error",
		"-ss", "00:00:00.5",
		"-i", tmp.Name(),
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "png",
		"-",
	)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	// A video shorter than 0.5s yields no frame at the seek point; depending
	// on the ffmpeg version that is either a non-zero exit or exit 0 with
	// empty output. Both cases retry from the start.
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		cmd2 := exec.Command(bin,
			"-loglevel", "error",
			"-i", tmp.Name(),
			"-frames:v", "1",
			"-f", "image2pipe",
			"-vcodec", "png",
			"-",
		)
		out.Reset()
		cmd2.Stdout = &out
		cmd2.Stderr = &stderr
		if err := cmd2.Run(); err != nil {
			return nil, errors.New("ffmpeg: " + stderr.String())
		}
	}
	return png.Decode(&out)
}

// extractPDFFirstPage rendert die erste Seite eines PDF via pdftoppm
// (poppler-utils). Schreibt die Quelle in eine Temp-Datei und liest das
// erzeugte JPEG zurück. Fehlt pdftoppm, wird der Aufruf vorher abgefangen.
func extractPDFFirstPage(s storageReader, userID uuid.UUID, rootID, filePath string) (image.Image, error) {
	bin, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, err
	}
	srcTmp, err := os.CreateTemp("", "priva-pdf-*.pdf")
	if err != nil {
		return nil, err
	}
	defer os.Remove(srcTmp.Name())
	if err := s.ServeFile(userID, rootID, filePath, srcTmp); err != nil {
		_ = srcTmp.Close()
		return nil, err
	}
	_ = srcTmp.Close()

	outRoot, err := os.MkdirTemp("", "priva-pdf-out-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outRoot)
	outPrefix := filepath.Join(outRoot, "p")

	cmd := exec.Command(bin,
		"-jpeg",
		"-singlefile",
		"-r", "96",
		"-f", "1", "-l", "1",
		srcTmp.Name(),
		outPrefix,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("pdftoppm: " + stderr.String())
	}
	data, err := os.ReadFile(outPrefix + ".jpg")
	if err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(data))
}

type storageReader interface {
	ServeFile(userID uuid.UUID, rootID, filePath string, w io.Writer) error
}

func matchesMagic(mime string, hdr []byte) bool {
	switch mime {
	case "image/png":
		return len(hdr) >= 8 &&
			hdr[0] == 0x89 && hdr[1] == 0x50 && hdr[2] == 0x4E && hdr[3] == 0x47 &&
			hdr[4] == 0x0D && hdr[5] == 0x0A && hdr[6] == 0x1A && hdr[7] == 0x0A
	case "image/jpeg":
		return len(hdr) >= 3 && hdr[0] == 0xFF && hdr[1] == 0xD8 && hdr[2] == 0xFF
	}
	return false
}

func encodeThumb(w io.Writer, img image.Image, useAlpha bool) error {
	if useAlpha {
		return png.Encode(w, img)
	}
	return jpeg.Encode(w, img, &jpeg.Options{Quality: thumbQuality})
}

// containThumbnail skaliert das Bild aspect-ratio-erhaltend so, dass die
// längere Seite genau `size` Pixel misst. Es wird kein Hintergrund hinzugefügt
// und nicht gecroppt — der Caller erhält ein rechteckiges Bild und kann es
// in seiner Layout-Box per CSS positionieren.
func containThumbnail(src image.Image, size int) image.Image {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return src
	}
	dstW, dstH := size, size
	if srcW >= srcH {
		dstH = int(float64(size) * float64(srcH) / float64(srcW))
		if dstH < 1 {
			dstH = 1
		}
	} else {
		dstW = int(float64(size) * float64(srcW) / float64(srcH))
		if dstW < 1 {
			dstW = 1
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// squareThumbnail croppt das Bild mittig auf ein Quadrat und skaliert es per
// BiLinear auf size x size. Schnell genug für On-Demand-Generierung und gut
// genug für Thumbnails — vermeidet eine schwere Resize-Lib.
func squareThumbnail(src image.Image, size int) image.Image {
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	cropRect := image.Rect(x0, y0, x0+side, y0+side)

	// NRGBA (non-premultiplied) + draw.Src statt RGBA+Over — erhält die
	// originalen RGB-Werte unter alpha=0, sodass Encoder/Browser den
	// Hintergrund nicht als Schwarz fehlinterpretieren.
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, cropRect, draw.Src, nil)
	return dst
}
