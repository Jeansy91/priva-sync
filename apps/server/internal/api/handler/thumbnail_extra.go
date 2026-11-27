package handler

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// decodeHEIC ruft heif-convert (libheif), um aus einer HEIC-Datei ein JPEG
// zu erzeugen, das image.Decode anschließend dekodiert. Pattern wie bei
// pdftoppm/ffmpeg — fehlt das Binary, wird der Aufruf vorher abgefangen.
func decodeHEIC(s storageReader, userID uuid.UUID, rootID, filePath string) (image.Image, error) {
	bin, err := exec.LookPath("heif-convert")
	if err != nil {
		return nil, err
	}
	srcTmp, err := os.CreateTemp("", "priva-heic-*"+filepath.Ext(filePath))
	if err != nil {
		return nil, err
	}
	defer os.Remove(srcTmp.Name())
	if err := s.ServeFile(userID, rootID, filePath, srcTmp); err != nil {
		_ = srcTmp.Close()
		return nil, err
	}
	_ = srcTmp.Close()

	outFile, err := os.CreateTemp("", "priva-heic-out-*.jpg")
	if err != nil {
		return nil, err
	}
	outPath := outFile.Name()
	_ = outFile.Close()
	defer os.Remove(outPath)

	//#nosec G702 -- args are server-generated os.CreateTemp paths, never user input
	cmd := exec.Command(bin, "-q", "85", srcTmp.Name(), outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("heif-convert: " + stderr.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// extractFirstComicImage liefert das erste Bild eines Comic-Archivs
// (CBZ via stdlib, CBR via externem bsdtar/libarchive). Sortierung "natural" wie im
// Client (page1 vor page10), damit Cover stabil bleibt.
func extractFirstComicImage(s storageReader, userID uuid.UUID, rootID, filePath, ext string) (image.Image, error) {
	tmp, err := os.CreateTemp("", "priva-comic-*"+ext)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := s.ServeFile(userID, rootID, filePath, tmp); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	_ = tmp.Close()

	if ext == ".cbz" {
		return firstZipImage(tmp.Name())
	}
	return firstRarImage(tmp.Name())
}

func firstZipImage(path string) (image.Image, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	names := make([]string, 0, len(zr.File))
	idx := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isComicImage(f.Name) {
			continue
		}
		names = append(names, f.Name)
		idx[f.Name] = f
	}
	if len(names) == 0 {
		return nil, errors.New("no image in cbz")
	}
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	rc, err := idx[names[0]].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	img, _, err := image.Decode(rc)
	return img, err
}

func firstRarImage(path string) (image.Image, error) {
	// bsdtar (libarchive) reads RAR and, unlike unrar, is licence-clean and
	// packaged in Alpine (libarchive-tools). It also streams a single entry to
	// stdout, so no temp-dir round-trip is needed.
	bin, err := exec.LookPath("bsdtar")
	if err != nil {
		return nil, err
	}
	// List contents, pick the natural-first image. `-tf` prints one entry per
	// line; non-image lines fail isComicImage.
	//#nosec G702 -- `path` is a server-generated os.CreateTemp name, not user input
	list := exec.Command(bin, "-tf", path)
	var listOut bytes.Buffer
	list.Stdout = &listOut
	if err := list.Run(); err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(listOut.String(), "\n") {
		n := strings.TrimSpace(line)
		if n != "" && isComicImage(n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("no image in cbr")
	}
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })

	// Extract the chosen entry straight to stdout (`-xOf`). The `--` separator
	// stops libarchive from treating an archive entry named like `-x` as an
	// option flag; names[0] is attacker-controlled (from the uploaded archive).
	//#nosec G702 -- entry name is guarded by the `--` option terminator; no shell, exec.Command with a fixed binary
	extract := exec.Command(bin, "-xOf", path, "--", names[0])
	var out, stderr bytes.Buffer
	extract.Stdout = &out
	extract.Stderr = &stderr
	if err := extract.Run(); err != nil {
		return nil, errors.New("bsdtar: " + stderr.String())
	}
	img, _, err := image.Decode(bytes.NewReader(out.Bytes()))
	return img, err
}

func isComicImage(name string) bool {
	n := strings.ToLower(name)
	for _, suf := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp"} {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}

// naturalLess vergleicht alphanumerisch mit Zifferngruppen — "page2.jpg"
// kommt vor "page10.jpg".
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, bi := a[i], b[j]
		if isDigit(ai) && isDigit(bi) {
			ae := i
			for ae < len(a) && isDigit(a[ae]) {
				ae++
			}
			be := j
			for be < len(b) && isDigit(b[be]) {
				be++
			}
			an, bn := a[i:ae], b[j:be]
			if len(an) != len(bn) {
				return len(an) < len(bn)
			}
			if an != bn {
				return an < bn
			}
			i, j = ae, be
			continue
		}
		if ai != bi {
			return ai < bi
		}
		i++
		j++
	}
	return len(a) < len(b)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
