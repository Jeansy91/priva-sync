package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestProcess_RejectsOversized(t *testing.T) {
	data := make([]byte, 1024+1)
	_, err := Process(data, 1024)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestProcess_RejectsNonImage(t *testing.T) {
	_, err := Process([]byte("hello, not an image"), 1<<20)
	if !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("want ErrInvalidImage, got %v", err)
	}
}

func TestProcess_ConvertsJPEGToPNG(t *testing.T) {
	out, err := Process(encodeJPEG(t, 100, 100), 1<<20)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output is not PNG: %v", err)
	}
}

func TestProcess_DoesNotUpscaleSmallImage(t *testing.T) {
	out, err := Process(encodePNG(t, 64, 32), 1<<20)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 64 || b.Dy() != 32 {
		t.Fatalf("want 64x32 (no upscale), got %dx%d", b.Dx(), b.Dy())
	}
}

func TestProcess_DownscalesLargeImageToMax256(t *testing.T) {
	out, err := Process(encodePNG(t, 1024, 512), 1<<20)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 256 || b.Dy() != 128 {
		t.Fatalf("want 256x128, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestProcess_AcceptsPNGAndReturnsPNG(t *testing.T) {
	out, err := Process(encodePNG(t, 100, 100), 1<<20)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output is not a valid PNG: %v", err)
	}
}
