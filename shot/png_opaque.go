package shot

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"math"
)

// encodeOpaquePNG writes an opaque, non-empty raster as an 8-bit RGB PNG whose
// rows all carry filter byte 0. image/png tries all five filters on every row
// of an RGBA image at any compression level, and that search is most of what
// an encode costs; a capture is long runs of one colour, which deflate well
// without it, at a few percent more bytes. Rows are read through Stride and
// Rect, so a sub-image encodes the pixels it shows.
func encodeOpaquePNG(img *image.RGBA) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	var idat bytes.Buffer
	zw, err := zlib.NewWriterLevel(&idat, zlib.BestSpeed)
	if err != nil {
		return nil, err
	}
	row := make([]byte, 1+3*w)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		src := img.Pix[img.PixOffset(b.Min.X, y):]
		dst := row[1:]
		for x := 0; x < w; x++ {
			dst[3*x], dst[3*x+1], dst[3*x+2] = src[4*x], src[4*x+1], src[4*x+2]
		}
		if _, err := zw.Write(row); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if idat.Len() > math.MaxInt32 {
		return nil, fmt.Errorf("png data too large: %d bytes", idat.Len())
	}

	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 8, 2 // bit depth 8, colour type RGB; the rest stay 0

	out := bytes.NewBuffer(make([]byte, 0, idat.Len()+64))
	out.WriteString("\x89PNG\r\n\x1a\n")
	writePNGChunk(out, "IHDR", ihdr[:])
	writePNGChunk(out, "IDAT", idat.Bytes())
	writePNGChunk(out, "IEND", nil)
	return out.Bytes(), nil
}

// writePNGChunk appends a chunk: length, type, data, then the CRC-32 of the
// type and the data.
func writePNGChunk(out *bytes.Buffer, typ string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	out.Write(n[:])
	out.WriteString(typ)
	out.Write(data)
	crc := crc32.Update(crc32.ChecksumIEEE([]byte(typ)), crc32.IEEETable, data)
	binary.BigEndian.PutUint32(n[:], crc)
	out.Write(n[:])
}
