package assets

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestBurnPreviewDiscardsDetailAndAnimation(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 320; x++ {
			v := uint8(0)
			if (x+y)%2 == 0 {
				v = 255
			}
			source.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var input bytes.Buffer
	require.NoError(t, png.Encode(&input, source))
	output, err := TransformBurnPreview(input.Bytes())
	require.NoError(t, err)
	decoded, err := jpeg.Decode(bytes.NewReader(output))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 160, 80), decoded.Bounds())
	lo, hi := uint32(65535), uint32(0)
	for y := 0; y < 80; y++ {
		for x := 0; x < 160; x++ {
			r, _, _, _ := decoded.At(x, y).RGBA()
			lo = min(lo, r)
			hi = max(hi, r)
		}
	}
	require.Less(t, hi-lo, uint32(4000), "high-frequency detail must disappear")
	palette := color.Palette{color.Black, color.White}
	a := image.NewPaletted(image.Rect(0, 0, 32, 32), palette)
	b := image.NewPaletted(a.Bounds(), palette)
	for i := range b.Pix {
		b.Pix[i] = 1
	}
	input.Reset()
	require.NoError(t, gif.EncodeAll(&input, &gif.GIF{Image: []*image.Paletted{a, b}, Delay: []int{1, 1}}))
	output, err = TransformBurnPreview(input.Bytes())
	require.NoError(t, err)
	_, err = jpeg.Decode(bytes.NewReader(output))
	require.NoError(t, err, "animation becomes a static JPEG")
	_, err = TransformBurnPreview(createPNGHeader(MaxDecodedImageDimension+1, 1))
	require.Error(t, err, "decoded allocation must be bounded")
	_, err = TransformBurnPreview([]byte("not an image"))
	require.Error(t, err)
	_, err = TransformBurnPreview(make([]byte, DefaultMaxUploadSize+1))
	require.Error(t, err)
}

func TestBurnPreviewPreservesLargeShapes(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 320; x++ {
			v := uint8(255)
			if x >= 80 && x < 240 && y >= 40 && y < 120 {
				v = 0
			}
			source.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var input bytes.Buffer
	require.NoError(t, png.Encode(&input, source))
	output, err := TransformBurnPreview(input.Bytes())
	require.NoError(t, err)
	decoded, err := jpeg.Decode(bytes.NewReader(output))
	require.NoError(t, err)
	corner, _, _, _ := decoded.At(10, 10).RGBA()
	center, _, _, _ := decoded.At(80, 40).RGBA()
	edge, _, _, _ := decoded.At(40, 40).RGBA()
	require.Greater(t, corner-center, uint32(50000), "large shapes remain distinct instead of becoming a color swatch")
	require.Greater(t, edge, center+5000, "shape edges are still blurred")
	require.Less(t, edge, corner-5000)
}
