package app

import (
	"bytes"
	"image"
	"image/draw"

	"github.com/charmbracelet/x/ansi/sixel"
)

// CropSixel decodes a raw sixel sequence, crops it to the given pixel
// rectangle, and re-encodes it. The input is the raw DCS body (everything
// between ESC P and ST), including the parameters and 'q' introducer.
// Returns the cropped sixel body (same format) or nil on error.
func CropSixel(raw []byte, cropRect image.Rectangle) []byte {
	if len(raw) == 0 {
		return nil
	}

	// Find the 'q' introducer - data after it is the sixel raster data
	qIdx := bytes.IndexByte(raw, 'q')
	if qIdx == -1 {
		return nil
	}

	sixelData := raw[qIdx+1:]
	if len(sixelData) == 0 {
		return nil
	}

	// Decode sixel data to image
	dec := &sixel.Decoder{}
	img, err := dec.Decode(bytes.NewReader(sixelData))
	if err != nil {
		sixelPassthroughLog("CropSixel: decode error: %v", err)
		return nil
	}

	imgBounds := img.Bounds()

	// Clamp crop rectangle to image bounds
	cropRect = cropRect.Intersect(imgBounds)
	if cropRect.Empty() {
		return nil
	}

	// If no cropping needed, return original
	if cropRect.Eq(imgBounds) {
		return raw
	}

	// Create cropped image
	cropped := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
	draw.Draw(cropped, cropped.Bounds(), img, cropRect.Min, draw.Src)

	// Re-encode to sixel
	enc := &sixel.Encoder{}
	var buf bytes.Buffer
	if err := enc.Encode(&buf, cropped); err != nil {
		sixelPassthroughLog("CropSixel: encode error: %v", err)
		return nil
	}

	// Build new raw sequence: parameters + 'q' + new sixel data
	var result bytes.Buffer
	result.Write(raw[:qIdx+1]) // params + 'q'
	result.Write(buf.Bytes())
	return result.Bytes()
}
