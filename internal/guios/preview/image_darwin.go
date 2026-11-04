//go:build darwin

package preview

import (
	"context"
	"errors"
	"image"
)

// ImageIO decodes pictures on macOS: HEIC, AVIF, RAW and the formats Go
// reads too. Its thumbnail call scales while it decodes (a JPEG by its DCT),
// so a 48 megapixel photo comes back at the size asked for without the
// whole picture in memory, and upright by its EXIF.

func init() { nativeImage = imageIO }

func imageIO(ctx context.Context, path string, maxPx int) (image.Image, int, int, error) {
	if err := loadCG(); err != nil {
		return nil, 0, 0, err
	}
	if ctx.Err() != nil {
		return nil, 0, 0, ctx.Err()
	}
	u, err := cfURL(path)
	if err != nil {
		return nil, 0, 0, err
	}
	src := cg.CGImageSourceCreateWithURL(u, 0)
	cg.CFRelease(u)
	if src == 0 {
		return nil, 0, 0, errUnreadable
	}
	defer cg.CFRelease(src)
	if cg.CGImageSourceGetCount(src) == 0 {
		return nil, 0, 0, errUnreadable
	}
	props := cg.CGImageSourceCopyPropertiesAtIndex(src, 0, 0)
	if props == 0 {
		return nil, 0, 0, errUnreadable
	}
	w, h := int(cfInt(props, cg.kPixelWidth)), int(cfInt(props, cg.kPixelHeight))
	orient := cfInt(props, cg.kOrientation)
	cg.CFRelease(props)
	if w <= 0 || h <= 0 {
		return nil, 0, 0, errUnreadable
	}
	if orient >= 5 && orient <= 8 {
		w, h = h, w
	}
	if int64(w)*int64(h) > 1<<30 {
		// ImageIO would read it, scaled; a picture past a gigapixel is
		// still refused, as the Go path refuses it.
		return nil, 0, 0, errTooLarge
	}
	side := min(maxPx, max(w, h))
	num := cfNumber(int64(side))
	opts := cfDict(
		[]uintptr{cg.kThumbAlways, cg.kThumbTransform, cg.kThumbMaxSize, cg.kShouldCacheImmediately},
		[]uintptr{cg.kCFBooleanTrue, cg.kCFBooleanTrue, num, cg.kCFBooleanTrue},
	)
	cg.CFRelease(num)
	img := cg.CGImageSourceCreateThumbnailAtIndex(src, 0, opts)
	cg.CFRelease(opts)
	if img == 0 {
		return nil, 0, 0, errUnreadable
	}
	defer cg.CGImageRelease(img)
	iw, ih := int(cg.CGImageGetWidth(img)), int(cg.CGImageGetHeight(img))
	if iw <= 0 || ih <= 0 {
		return nil, 0, 0, errUnreadable
	}
	if ctx.Err() != nil {
		return nil, 0, 0, ctx.Err()
	}
	bm, err := newBitmap(iw, ih)
	if err != nil {
		return nil, 0, 0, errors.New("The picture could not be drawn.")
	}
	defer bm.release()
	cg.CGContextDrawImage(bm.ctx, cgRect{0, 0, float64(iw), float64(ih)}, img)
	return bm.image(), w, h, nil
}
