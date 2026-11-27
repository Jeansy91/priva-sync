package handler

import (
	"image"
	"testing"
)

func TestIsDigit(t *testing.T) {
	for b := byte('0'); b <= '9'; b++ {
		if !isDigit(b) {
			t.Errorf("isDigit(%q) = false, want true", b)
		}
	}
	for _, b := range []byte{'a', 'Z', ' ', '.', '-'} {
		if isDigit(b) {
			t.Errorf("isDigit(%q) = true, want false", b)
		}
	}
}

func TestIsComicImage(t *testing.T) {
	cases := map[string]bool{
		"page1.jpg":  true,
		"page1.JPEG": true,
		"cover.png":  true,
		"a.webp":     true,
		"a.gif":      true,
		"a.bmp":      true,
		"readme.txt": false,
		"page1.tiff": false,
	}
	for name, want := range cases {
		if got := isComicImage(name); got != want {
			t.Errorf("isComicImage(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNaturalLess_OrdersNumericGroupsNumerically(t *testing.T) {
	if !naturalLess("page2.jpg", "page10.jpg") {
		t.Error("naturalLess(page2.jpg, page10.jpg) = false, want true")
	}
	if naturalLess("page10.jpg", "page2.jpg") {
		t.Error("naturalLess(page10.jpg, page2.jpg) = true, want false")
	}
	if naturalLess("a.jpg", "a.jpg") {
		t.Error("naturalLess(a.jpg, a.jpg) = true, want false (equal)")
	}
	if !naturalLess("a.jpg", "b.jpg") {
		t.Error("naturalLess(a.jpg, b.jpg) = false, want true")
	}
	if !naturalLess("page1.jpg", "page1_extra.jpg") {
		t.Error("naturalLess(page1.jpg, page1_extra.jpg) = false, want true (shorter prefix)")
	}
}

func TestMatchesMagic(t *testing.T) {
	pngHdr := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegHdr := []byte{0xFF, 0xD8, 0xFF, 0xE0}

	if !matchesMagic("image/png", pngHdr) {
		t.Error("matchesMagic(image/png, valid PNG header) = false, want true")
	}
	if matchesMagic("image/png", jpegHdr) {
		t.Error("matchesMagic(image/png, JPEG header) = true, want false")
	}
	if !matchesMagic("image/jpeg", jpegHdr) {
		t.Error("matchesMagic(image/jpeg, valid JPEG header) = false, want true")
	}
	if matchesMagic("image/jpeg", []byte{0x00, 0x01}) {
		t.Error("matchesMagic(image/jpeg, too-short header) = true, want false")
	}
	if matchesMagic("application/pdf", pngHdr) {
		t.Error("matchesMagic(unknown mime) = true, want false")
	}
}

func TestContainThumbnail_PreservesAspectRatio(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))
	dst := containThumbnail(src, 50)
	b := dst.Bounds()
	if b.Dx() != 50 {
		t.Errorf("wide image: Dx() = %d, want 50 (long side)", b.Dx())
	}
	if b.Dy() != 25 {
		t.Errorf("wide image: Dy() = %d, want 25 (aspect-preserved)", b.Dy())
	}

	src = image.NewRGBA(image.Rect(0, 0, 100, 200))
	dst = containThumbnail(src, 50)
	b = dst.Bounds()
	if b.Dy() != 50 {
		t.Errorf("tall image: Dy() = %d, want 50 (long side)", b.Dy())
	}
	if b.Dx() != 25 {
		t.Errorf("tall image: Dx() = %d, want 25 (aspect-preserved)", b.Dx())
	}
}
