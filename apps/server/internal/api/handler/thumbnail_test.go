package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

func lookFFmpeg() (string, error) { return exec.LookPath("ffmpeg") }

func runFFmpegMake(out string) error {
	bin, err := lookFFmpeg()
	if err != nil {
		return err
	}
	return exec.Command(bin, "-y",
		"-f", "lavfi", "-i", "color=c=red:s=64x64:d=0.1",
		"-pix_fmt", "yuv420p",
		"-t", "0.1",
		out).Run()
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestThumbnail_ReturnsResizedSquare(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "thumbroot")

	// Synthetisches 200x100 PNG hochladen.
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y * 2), 80, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/pic.png",
		bytes.NewReader(pngBuf.Bytes()), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	// Thumbnail anfragen.
	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/pic.png&size=64",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)

	if w2.Code != 200 {
		t.Fatalf("thumbnail status %d body=%s", w2.Code, w2.Body.String())
	}
	// Opakes Quell-PNG -> JPEG-Output (kompakter, kein Alpha nötig).
	if ct := w2.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type %q, want image/jpeg (opaque src)", ct)
	}

	decoded, err := jpeg.Decode(bytes.NewReader(w2.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode thumb: %v", err)
	}
	b := decoded.Bounds()
	if b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("thumb size %dx%d, want 64x64", b.Dx(), b.Dy())
	}
}

func TestThumbnail_DefaultSizeWhenMissing(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "thumbroot2")

	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/x.png",
		bytes.NewReader(pngBuf.Bytes()), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/x.png",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("status %d", w2.Code)
	}
	decoded, err := png.Decode(bytes.NewReader(w2.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Bounds().Dx() != 256 {
		t.Fatalf("default size %d, want 256", decoded.Bounds().Dx())
	}
}

func TestThumbnail_PreservesTransparencyForPNG(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "alpha")

	// PNG mit transparentem Hintergrund + opakem roten Quadrat.
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 30; y < 70; y++ {
		for x := 30; x < 70; x++ {
			src.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, src); err != nil {
		t.Fatal(err)
	}
	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/transp.png",
		bytes.NewReader(pngBuf.Bytes()), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/transp.png&size=32",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("status %d", w2.Code)
	}
	if ct := w2.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type %q, want image/png (alpha-preserving)", ct)
	}
	decoded, err := png.Decode(bytes.NewReader(w2.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode png thumb: %v", err)
	}
	// Ecke muss transparent sein.
	_, _, _, a := decoded.At(1, 1).RGBA()
	if a != 0 {
		t.Fatalf("corner alpha %d, want 0 (transparent preserved)", a)
	}
}

func TestThumbnail_VideoUsesFFmpegWhenAvailable(t *testing.T) {
	if _, err := lookFFmpeg(); err != nil {
		t.Skip("ffmpeg not available")
	}

	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "vid")

	// Erzeuge winziges Test-MP4 mit ffmpeg selbst (color source, 1 frame).
	tmp := t.TempDir()
	mp4Path := tmp + "/in.mp4"
	if err := runFFmpegMake(mp4Path); err != nil {
		t.Skipf("cannot synthesize mp4 with ffmpeg: %v", err)
	}
	mp4Bytes := mustReadFile(t, mp4Path)

	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/clip.mp4",
		bytes.NewReader(mp4Bytes), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/clip.mp4&size=64",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("thumb status %d body=%s", w2.Code, w2.Body.String())
	}
	if ct := w2.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("video thumb content-type %q, want image/jpeg", ct)
	}
}

func TestThumbnail_SVGReturnedVerbatim(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "svgroot")

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="42" height="42"><rect width="42" height="42" fill="red"/></svg>`)
	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/icon.svg",
		bytes.NewReader(svg), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/icon.svg",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("status %d body=%s", w2.Code, w2.Body.String())
	}
	if ct := w2.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("content-type %q, want image/svg+xml", ct)
	}
	if !bytes.Equal(w2.Body.Bytes(), svg) {
		t.Fatalf("svg body modified — want 1:1 passthrough")
	}
}

func TestThumbnail_PNGFullyTransparentBackgroundStaysTransparent(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "tbg")

	// 100x100 PNG: weißes Quadrat in Mitte, Rest komplett transparent.
	src := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for y := 40; y < 60; y++ {
		for x := 40; x < 60; x++ {
			src.Set(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, src); err != nil {
		t.Fatal(err)
	}
	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/bg.png",
		bytes.NewReader(pngBuf.Bytes()), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/bg.png&size=64",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("status %d", w2.Code)
	}
	decoded, err := png.Decode(bytes.NewReader(w2.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Mehrere Hintergrund-Pixel müssen alpha=0 sein.
	for _, p := range [][2]int{{2, 2}, {5, 5}, {10, 10}, {61, 61}, {2, 30}} {
		c := color.NRGBAModel.Convert(decoded.At(p[0], p[1])).(color.NRGBA)
		if c.A != 0 {
			t.Errorf("alpha at %v = %d, want 0 (fully transparent bg)", p, c.A)
		}
	}
	// Mitte muss opak weiß sein.
	c := color.NRGBAModel.Convert(decoded.At(32, 32)).(color.NRGBA)
	if c.A < 240 || c.R < 240 || c.G < 240 || c.B < 240 {
		t.Errorf("center = %+v, want opaque white", c)
	}
}

func TestThumbnail_PNGAlphaPreservedAcrossScale(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "alphafull")

	// PNG vollflächig halb-transparent rot.
	src := image.NewNRGBA(image.Rect(0, 0, 80, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			src.Set(x, y, color.NRGBA{255, 0, 0, 128})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, src); err != nil {
		t.Fatal(err)
	}
	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/half.png",
		bytes.NewReader(pngBuf.Bytes()), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/half.png&size=32",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("status %d", w2.Code)
	}
	decoded, err := png.Decode(bytes.NewReader(w2.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Pixel im Inneren muss halb-transparentes Rot bleiben (NRGBA-Werte).
	c := color.NRGBAModel.Convert(decoded.At(16, 16)).(color.NRGBA)
	if c.A < 100 || c.A > 160 {
		t.Fatalf("alpha %d, want ~128", c.A)
	}
	if c.R < 200 {
		t.Fatalf("red %d, want ~255 (non-premultiplied)", c.R)
	}
}

func TestThumbnail_UnsupportedFormatReturns415(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "thumbroot3")

	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/files?path=/data.bin",
		bytes.NewReader([]byte("not an image")), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	r2 := reqWithClaims("GET",
		"/sync-roots/"+root.ID.String()+"/files/thumbnail?path=/data.bin",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.Thumbnail(w2, r2)
	if w2.Code != 415 {
		t.Fatalf("status %d, want 415", w2.Code)
	}
}

// Bob requests the thumbnail of a file-mount: the share points straight at an
// image, so the row icon and the detail-panel preview both ask for path="/".
// The mount thumbnail endpoint must resolve "/" to the shared file and render
// its thumbnail — this is the exact path the icons and detail preview take for
// a single-file mount. See ADR-0010 / 0012.
func TestMountThumbnail_FileMount_ResolvesRootPath(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())

	// Seed a real 200x100 PNG so the thumbnail pipeline has something to decode.
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y * 2), 80, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", pngBuf.Bytes())
	// Share the FILE itself, not its parent directory → file-mount.
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024/cat.png", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/thumbnail?path=/&size=64",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.handler.MountThumbnail(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("mount thumbnail status %d body=%s", w.Code, w.Body.String())
	}
	if _, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatalf("decode mount thumb: %v", err)
	}
}
