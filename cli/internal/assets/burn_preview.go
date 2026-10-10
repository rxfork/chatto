package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	"github.com/disintegration/imageorient"
	xdraw "golang.org/x/image/draw"
)

// TransformBurnPreview irreversibly discards fine detail before encoding a
// static JPEG. Callers cannot choose dimensions, blur strength, or frame.
func TransformBurnPreview(data []byte) ([]byte, error) {
	if int64(len(data)) > DefaultMaxUploadSize {
		return nil, fmt.Errorf("image exceeds preview limit")
	}
	if err := validateDecodedImageSize(data); err != nil {
		return nil, err
	}
	src, _, err := imageorient.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w >= h {
		h = max(1, h*48/w)
		w = 48
	} else {
		w = max(1, w*48/h)
		h = 48
	}
	tiny := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(tiny, tiny.Bounds(), &image.Uniform{C: color.RGBA{128, 128, 128, 255}}, image.Point{}, draw.Src)
	xdraw.BiLinear.Scale(tiny, tiny.Bounds(), src, bounds, draw.Over, nil)
	blurred := image.NewRGBA(tiny.Bounds())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, n uint32
			for yy := max(0, y-2); yy <= min(h-1, y+2); yy++ {
				for xx := max(0, x-2); xx <= min(w-1, x+2); xx++ {
					c := tiny.RGBAAt(xx, yy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
					n++
				}
			}
			blurred.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255})
		}
	}
	outW, outH := 160, max(1, bounds.Dy()*160/bounds.Dx())
	if bounds.Dy() > bounds.Dx() {
		outW, outH = max(1, bounds.Dx()*160/bounds.Dy()), 160
	}
	output := image.NewRGBA(image.Rect(0, 0, outW, outH))
	xdraw.BiLinear.Scale(output, output.Bounds(), blurred, blurred.Bounds(), draw.Src, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: 70}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
