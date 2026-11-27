package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/png"

	_ "image/jpeg"

	"golang.org/x/image/draw"
)

const maxDimension = 256

var (
	ErrTooLarge     = errors.New("avatar: too large")
	ErrInvalidImage = errors.New("avatar: invalid image")
)

func Process(data []byte, maxBytes int) ([]byte, error) {
	if len(data) > maxBytes {
		return nil, ErrTooLarge
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidImage
	}
	img = downscale(img)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func downscale(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDimension && h <= maxDimension {
		return src
	}
	var nw, nh int
	if w >= h {
		nw = maxDimension
		nh = h * maxDimension / w
	} else {
		nh = maxDimension
		nw = w * maxDimension / h
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
