package preview

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	_ "image/gif"  // decoders for image.Decode
	_ "image/jpeg" // decoders for image.Decode
	_ "image/png"  // decoders for image.Decode
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp" // decoders for image.Decode
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // decoders for image.Decode
	_ "golang.org/x/image/webp" // decoders for image.Decode
)

// The bounds of a picture. A small file can hold a huge picture, so the
// decode is judged by its pixels, not by the file's size.
const (
	// MaxPx is the longest side a picture keeps: enough to zoom in on a
	// picture in a viewer a third of a 5K screen wide.
	MaxPx = 4096
	// imageFileMax bounds the file read whole for a picture.
	imageFileMax = 256 << 20
	// decodeMax bounds the memory one decode takes, its buffers counted.
	decodeMax = 512 << 20
)

// directFormats are those the viewer's own decoder reads (MyGo's
// DecodeBitmap, which turns a photo upright by its EXIF), so a picture of
// one, no larger than MaxPx, goes to it as its bytes: the viewer can then
// drop its pixels once they are on the GPU, and decode them again from the
// bytes if it needs them.
var directFormats = map[string]bool{"png": true, "jpeg": true, "gif": true, "webp": true, "bmp": true}

// Picture is a picture read for the viewer: its bytes, for the viewer's
// decoder, or the picture decoded and scaled to at most MaxPx.
type Picture struct {
	Data   []byte
	Img    image.Image
	W, H   int    // the picture's own size, upright
	Format string // as image.Decode names it, or the tool that read it
}

// nativeImage decodes a picture with the platform's decoder, at most maxPx
// on its long side, upright. Set on macOS (ImageIO), where it reads HEIC,
// AVIF, RAW and the rest. It returns the picture's own size too.
var nativeImage func(ctx context.Context, path string, maxPx int) (img image.Image, w, h int, err error)


// DecodeImage reads the picture at path. maxPx bounds its long side, 0 for
// MaxPx.
func DecodeImage(ctx context.Context, path string, maxPx int) (*Picture, error) {
	if maxPx <= 0 || maxPx > MaxPx {
		maxPx = MaxPx
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > imageFileMax {
		return nil, ErrTooLarge
	}
	ext := strings.ToLower(filepath.Ext(path))
	// The platform's decoder reads what Go's do not, and a large JPEG at a
	// fraction of its size without decoding all of it.
	if nativeImage != nil && (!goReads[ext] || fi.Size() > 4<<20) {
		if img, w, h, err := nativeImage(ctx, path, maxPx); err == nil {
			return &Picture{Img: img, W: w, H: h, Format: "native"}, nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// HEIC, AVIF, JPEG XL and icons: a tool reads them, where one is.
		return toolImage(ctx, path, maxPx)
	}
	orient := 1
	if format == "jpeg" {
		orient = exifOrientation(data)
	}
	w, h := cfg.Width, cfg.Height
	if orient >= 5 {
		w, h = h, w
	}
	if directFormats[format] && cfg.Width <= maxPx && cfg.Height <= maxPx {
		return &Picture{Data: data, W: w, H: h, Format: format}, nil
	}
	if int64(cfg.Width)*int64(cfg.Height)*int64(bytesPerPixel(cfg.ColorModel)) > decodeMax {
		return nil, ErrTooLarge
	}
	// A large JPEG decodes in Go at about 30 megapixels a second; libjpeg's
	// djpeg scales it down as it decodes, five to ten times faster.
	if format == "jpeg" && (cfg.Width > 2*maxPx || cfg.Height > 2*maxPx || cfg.Width*cfg.Height > 12_000_000) {
		if img, err := djpeg(ctx, path, cfg.Width, cfg.Height, maxPx); err == nil {
			img = Fit(img, maxPx)
			if orient > 1 {
				img = Orient(img, orient)
			}
			return &Picture{Img: img, W: w, H: h, Format: format}, nil
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnreadable
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	img = Fit(img, maxPx)
	if orient > 1 {
		img = Orient(img, orient)
	}
	return &Picture{Img: img, W: w, H: h, Format: format}, nil
}

// goReads are the extensions Go's decoders here read.
var goReads = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".tif": true, ".tiff": true}

// djpeg decodes a JPEG with libjpeg's djpeg, scaled by the eighths that
// keep it at least maxPx on its long side.
func djpeg(ctx context.Context, path string, w, h, maxPx int) (image.Image, error) {
	long := max(w, h)
	m := min(max((8*maxPx+long-1)/long, 1), 8)
	out, err := run(ctx, 512<<20, "djpeg", "-scale", itoa(m)+"/8", "-ppm", path)
	if err != nil {
		return nil, err
	}
	return decodePPM(out)
}

// bytesPerPixel is what a pixel of the color model takes, decoded, at most.
func bytesPerPixel(m color.Model) int {
	switch m {
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model:
		return 2
	}
	return 4
}

// toolImage reads a picture Go cannot through a tool: ImageMagick, then
// ffmpeg. Each writes a PNG of at most maxPx.
func toolImage(ctx context.Context, path string, maxPx int) (*Picture, error) {
	size := itoa(maxPx) + "x" + itoa(maxPx) + ">"
	tries := []struct {
		tool string
		args []string
	}{
		{"magick", []string{path + "[0]", "-auto-orient", "-thumbnail", size, "png:-"}},
		{"ffmpeg", []string{"-v", "error", "-nostdin", "-i", path, "-frames:v", "1", "-vf",
			"scale='min(" + itoa(maxPx) + ",iw)':'min(" + itoa(maxPx) + ",ih)':force_original_aspect_ratio=decrease", "-f", "image2pipe", "-vcodec", "png", "-"}},
	}
	var missing []string
	for _, t := range tries {
		out, err := run(ctx, 256<<20, t.tool, t.args...)
		var nt ErrNoTool
		if errors.As(err, &nt) {
			missing = append(missing, t.tool)
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || len(out) == 0 {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			continue
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		// The tool scaled it: its own size comes from the tool's header
		// when it says, else the size it sent.
		if ow, oh, ok := headerSize(path); ok {
			w, h = ow, oh
		}
		return &Picture{Img: img, W: w, H: h, Format: t.tool}, nil
	}
	if len(missing) == len(tries) {
		return nil, ErrNoDecoder
	}
	return nil, ErrUnreadable
}

// headerSize reads a HEIF's size from its ispe box, without decoding it.
func headerSize(path string) (w, h int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 64<<10)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	// The largest ispe is the primary picture's; the small ones are its
	// thumbnail and the grid's tiles.
	for i := bytes.Index(buf, []byte("ispe")); i >= 0 && i+16 <= len(buf); {
		bw := int(be32(buf[i+8:]))
		bh := int(be32(buf[i+12:]))
		if bw*bh > w*h {
			w, h = bw, bh
		}
		j := bytes.Index(buf[i+4:], []byte("ispe"))
		if j < 0 {
			break
		}
		i += 4 + j
	}
	return w, h, w > 0 && h > 0
}

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// Fit scales img down to at most maxPx on its long side: bilinear down to
// twice that, then halves by averaging, so a large reduction keeps every
// pixel's part rather than shimmering.
func Fit(img image.Image, maxPx int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxPx && h <= maxPx {
		return img
	}
	tw, th := fitSize(w, h, maxPx)
	if w >= 2*tw && h >= 2*th {
		// Bilinear to twice the target, then one halving.
		dst := image.NewRGBA(image.Rect(0, 0, 2*tw, 2*th))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		return halve(dst)
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// fitSize is w by h scaled to at most maxPx on its long side.
func fitSize(w, h, maxPx int) (int, int) {
	if w >= h {
		return maxPx, max(h*maxPx/w, 1)
	}
	return max(w*maxPx/h, 1), maxPx
}

// halve averages each 2 by 2 block of src into a pixel.
func halve(src *image.RGBA) *image.RGBA {
	w, h := src.Rect.Dx()/2, src.Rect.Dy()/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		r0 := src.Pix[(2*y)*src.Stride:]
		r1 := src.Pix[(2*y+1)*src.Stride:]
		d := dst.Pix[y*dst.Stride:]
		for x := 0; x < w; x++ {
			i := 8 * x
			for c := 0; c < 4; c++ {
				d[4*x+c] = uint8((uint16(r0[i+c]) + uint16(r0[i+4+c]) + uint16(r1[i+c]) + uint16(r1[i+4+c]) + 2) / 4)
			}
		}
	}
	return dst
}

// exifOrientation reads a JPEG's EXIF orientation, 1 (upright) when it has
// none.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) && b[i] == 0xff {
		marker := b[i+1]
		n := int(b[i+2])<<8 | int(b[i+3])
		if marker == 0xda || n < 2 || i+2+n > len(b) {
			return 1 // the scan starts: no EXIF before it
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

// tiffOrientation finds tag 0x0112 in the first IFD of a TIFF header.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var u16 func([]byte) int
	var u32 func([]byte) int
	switch string(t[:2]) {
	case "II":
		u16 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 }
		u32 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24 }
	case "MM":
		u16 = func(b []byte) int { return int(b[1]) | int(b[0])<<8 }
		u32 = func(b []byte) int { return int(b[3]) | int(b[2])<<8 | int(b[1])<<16 | int(b[0])<<24 }
	default:
		return 1
	}
	off := u32(t[4:])
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := u16(t[off:])
	for k := 0; k < n; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if u16(t[e:]) == 0x0112 {
			if o := u16(t[e+8:]); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// Orient turns a picture upright by its EXIF orientation (2 to 8).
func Orient(img image.Image, o int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src, ok := img.(*image.RGBA)
	if !ok {
		src = image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	}
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			default:
				dx, dy = x, y
			}
			copy(dst.Pix[dy*dst.Stride+4*dx:dy*dst.Stride+4*dx+4], src.Pix[y*src.Stride+4*x:y*src.Stride+4*x+4])
		}
	}
	return dst
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
