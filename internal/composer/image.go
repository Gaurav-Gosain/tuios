package composer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"image"
	// The decoders read only an image's header here, for its size. They are
	// in the binary already, for the launcher's icons and file previews.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
)

// Image bounds. They match the daemon's: one pasted image is at most
// MaxImageBytes, and a message carries at most MaxImages.
const (
	MaxImageBytes = 8 << 20
	MaxImages     = 8
)

// Image is one image attached to a message. The bytes are held in memory
// only, and go when the block that holds them is deleted and its undo step
// falls off the stack.
type Image struct {
	// Data is the image file as it was read.
	Data []byte
	// Format is the type read from the bytes: png, jpeg, gif, webp, bmp or
	// tiff.
	Format string
	// Width and Height are in pixels, 0 when the header could not be read.
	Width, Height int
	// Sum is the bytes' SHA-256, which tells the same image pasted twice.
	Sum [sha256.Size]byte
	// Name is the name of the file it was read from, empty for an image
	// from the clipboard.
	Name string
}

// Errors of NewImage.
var (
	ErrNotImage      = errors.New("the content is not a PNG, JPEG, GIF, WebP, BMP or TIFF image")
	ErrImageTooLarge = errors.New("the image is larger than 8 MB")
)

// NewImage reads an image's type from its bytes, and its size from its
// header. It does not decode the pixels.
func NewImage(data []byte) (*Image, error) {
	if len(data) > MaxImageBytes {
		return nil, ErrImageTooLarge
	}
	format := SniffImage(data)
	if format == "" {
		return nil, ErrNotImage
	}
	img := &Image{Data: data, Format: format, Sum: sha256.Sum256(data)}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		img.Width, img.Height = cfg.Width, cfg.Height
	}
	return img, nil
}

// SniffImage is the type of an image's bytes, from their magic number, or ""
// when they are not an image tuios takes. The daemon reads the bytes again.
func SniffImage(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "webp"
	case bytes.HasPrefix(data, []byte("BM")) && len(data) >= 26:
		return "bmp"
	case bytes.HasPrefix(data, []byte("II*\x00")), bytes.HasPrefix(data, []byte("MM\x00*")):
		return "tiff"
	}
	return ""
}

// measureWords is the image's measure as its label says it: its size and
// type, or the type alone when the size is not known.
func (img *Image) measureWords() string {
	if img.Width > 0 && img.Height > 0 {
		return strconv.Itoa(img.Width) + "×" + strconv.Itoa(img.Height) + " " + img.Format
	}
	return img.Format
}

// newImageBlock makes block n of img.
func newImageBlock(n int, img *Image) *block {
	b := &block{n: n, img: img}
	b.label = "[Image #" + strconv.Itoa(n) + " · " + img.measureWords() + "]"
	return b
}

// renumbered is a copy of b numbered n.
func renumbered(b *block, n int) *block {
	if b.img != nil {
		return newImageBlock(n, b.img)
	}
	return newBlock(n, b.text)
}

// Images is how many images the draft holds, and their bytes.
func (m *Model) Images() (n, bytes int) {
	for _, c := range m.cells {
		if c.block != nil && c.block.img != nil {
			n++
			bytes += len(c.block.img.Data)
		}
	}
	return n, bytes
}

// AddImage attaches img at the cursor as one block, as one undo step. It
// refuses, with a notice, when the draft takes no images, holds the same image
// already, or holds as many as a message takes.
func (m *Model) AddImage(img *Image, remote bool) bool {
	if remote {
		m.automated = true
	}
	if img == nil {
		return false
	}
	if !m.opt.Images {
		m.notice = "This agent cannot take images."
		return false
	}
	if len(img.Data) > MaxImageBytes {
		m.notice = "The image is larger than 8 MB. Make it smaller and paste it again."
		return false
	}
	n := 0
	for _, c := range m.cells {
		if c.block == nil || c.block.img == nil {
			continue
		}
		n++
		if c.block.img.Sum == img.Sum {
			m.notice = "Image #" + strconv.Itoa(c.block.n) + " is already in the message."
			return false
		}
	}
	if n >= MaxImages {
		m.notice = "A message takes " + strconv.Itoa(MaxImages) + " images. Remove one to add another."
		return false
	}
	m.edited()
	b := newImageBlock(m.nextNumber(true), img)
	m.change(m.cur, 0, []cell{blockCell(b)}, m.cur+1)
	return true
}

// TakesImages reports whether the draft takes images.
func (m *Model) TakesImages() bool { return m.opt.Images }
