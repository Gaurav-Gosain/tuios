//go:build darwin

package preview

import (
	"errors"
	"image"
	"sync"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/ebitengine/purego"
)

// The macOS frameworks the native decoders call, through purego: no cgo, so
// the app still builds with CGO_ENABLED=0. They load on the first preview
// that needs them, not at start.

// cgRect is a CGRect, flat: four doubles, which purego passes and returns
// in float registers (a homogeneous float aggregate) as the C ABI does.
type cgRect struct{ X, Y, W, H float64 }

// cgAffine is a CGAffineTransform: six doubles, returned through memory.
type cgAffine struct{ A, B, C, D, Tx, Ty float64 }

// CoreFoundation's number types and the bitmap layout the decoders draw in.
const (
	cfNumberSInt64Type = 4
	// kCGImageAlphaPremultipliedLast with the default byte order: R, G,
	// B, A in memory, alpha premultiplied, as image.RGBA holds them.
	cgBitmapRGBA = 1
	cgPDFCropBox = 1
)

var cg struct {
	once sync.Once
	err  error

	// CoreFoundation
	CFRelease                                    func(ref uintptr)
	CFURLCreateFromFileSystemRepresentation      func(alloc uintptr, buf *byte, n int, dir bool) uintptr
	CFDictionaryCreate                           func(alloc uintptr, keys, values *uintptr, n int, kcb, vcb uintptr) uintptr
	CFDictionaryGetValue                         func(dict, key uintptr) uintptr
	CFNumberCreate                               func(alloc uintptr, typ int, value unsafe.Pointer) uintptr
	CFNumberGetValue                             func(num uintptr, typ int, value unsafe.Pointer) bool
	keyCallBacks, valueCallBacks, kCFBooleanTrue uintptr

	// CoreGraphics
	CGColorSpaceCreateDeviceRGB      func() uintptr
	CGColorSpaceRelease              func(cs uintptr)
	CGBitmapContextCreate            func(data unsafe.Pointer, w, h, bpc, bpr uintptr, cs uintptr, info uint32) uintptr
	CGBitmapContextGetData           func(ctx uintptr) unsafe.Pointer
	CGContextRelease                 func(ctx uintptr)
	CGContextSetRGBFillColor         func(ctx uintptr, r, g, b, a float64)
	CGContextFillRect                func(ctx uintptr, r cgRect)
	CGContextScaleCTM                func(ctx uintptr, sx, sy float64)
	CGContextConcatCTM               func(ctx uintptr, t cgAffine)
	CGContextSetInterpolationQuality func(ctx uintptr, q int32)
	CGContextDrawImage               func(ctx uintptr, r cgRect, img uintptr)
	CGImageGetWidth                  func(img uintptr) uintptr
	CGImageGetHeight                 func(img uintptr) uintptr
	CGImageRelease                   func(img uintptr)

	CGPDFDocumentCreateWithURL    func(url uintptr) uintptr
	CGPDFDocumentRelease          func(doc uintptr)
	CGPDFDocumentGetNumberOfPages func(doc uintptr) uintptr
	CGPDFDocumentIsUnlocked       func(doc uintptr) bool
	CGPDFDocumentGetPage          func(doc uintptr, n uintptr) uintptr
	CGPDFPageGetBoxRect           func(page uintptr, box int32) cgRect
	CGPDFPageGetRotationAngle     func(page uintptr) int32
	CGPDFPageGetDrawingTransform  func(page uintptr, box int32, r cgRect, rotate int32, keepAspect bool) cgAffine
	CGContextDrawPDFPage          func(ctx, page uintptr)

	// ImageIO
	CGImageSourceCreateWithURL                                            func(url, opts uintptr) uintptr
	CGImageSourceGetCount                                                 func(src uintptr) uintptr
	CGImageSourceCopyPropertiesAtIndex                                    func(src uintptr, i uintptr, opts uintptr) uintptr
	CGImageSourceCreateThumbnailAtIndex                                   func(src uintptr, i uintptr, opts uintptr) uintptr
	kPixelWidth, kPixelHeight, kOrientation                               uintptr
	kThumbAlways, kThumbTransform, kThumbMaxSize, kShouldCacheImmediately uintptr
}

// loadCG opens the frameworks once and reports whether they are there.
func loadCG() error {
	cg.once.Do(func() {
		defer func() {
			// RegisterLibFunc panics on a symbol it cannot find.
			if r := recover(); r != nil {
				cg.err = errfmt.Errorf("macOS graphics frameworks: %v", r)
			}
		}()
		open := func(name string) uintptr {
			h, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				panic(err)
			}
			return h
		}
		cf, cgl, iio := open("CoreFoundation"), open("CoreGraphics"), open("ImageIO")
		// sym is the address of a global; ref is the CFTypeRef a global
		// constant holds.
		sym := func(lib uintptr, name string) uintptr {
			p, err := purego.Dlsym(lib, name)
			if err != nil {
				panic(err)
			}
			return p
		}
		ref := func(lib uintptr, name string) uintptr {
			p := sym(lib, name)
			// The address of the global, read as a pointer to the
			// CFTypeRef it holds, without turning a uintptr into an
			// unsafe.Pointer that vet would flag.
			return **(**uintptr)(unsafe.Pointer(&p))
		}

		purego.RegisterLibFunc(&cg.CFRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cg.CFURLCreateFromFileSystemRepresentation, cf, "CFURLCreateFromFileSystemRepresentation")
		purego.RegisterLibFunc(&cg.CFDictionaryCreate, cf, "CFDictionaryCreate")
		purego.RegisterLibFunc(&cg.CFDictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&cg.CFNumberCreate, cf, "CFNumberCreate")
		purego.RegisterLibFunc(&cg.CFNumberGetValue, cf, "CFNumberGetValue")
		cg.keyCallBacks = sym(cf, "kCFTypeDictionaryKeyCallBacks")
		cg.valueCallBacks = sym(cf, "kCFTypeDictionaryValueCallBacks")
		cg.kCFBooleanTrue = ref(cf, "kCFBooleanTrue")

		purego.RegisterLibFunc(&cg.CGColorSpaceCreateDeviceRGB, cgl, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&cg.CGColorSpaceRelease, cgl, "CGColorSpaceRelease")
		purego.RegisterLibFunc(&cg.CGBitmapContextCreate, cgl, "CGBitmapContextCreate")
		purego.RegisterLibFunc(&cg.CGBitmapContextGetData, cgl, "CGBitmapContextGetData")
		purego.RegisterLibFunc(&cg.CGContextRelease, cgl, "CGContextRelease")
		purego.RegisterLibFunc(&cg.CGContextSetRGBFillColor, cgl, "CGContextSetRGBFillColor")
		purego.RegisterLibFunc(&cg.CGContextFillRect, cgl, "CGContextFillRect")
		purego.RegisterLibFunc(&cg.CGContextScaleCTM, cgl, "CGContextScaleCTM")
		purego.RegisterLibFunc(&cg.CGContextConcatCTM, cgl, "CGContextConcatCTM")
		purego.RegisterLibFunc(&cg.CGContextSetInterpolationQuality, cgl, "CGContextSetInterpolationQuality")
		purego.RegisterLibFunc(&cg.CGContextDrawImage, cgl, "CGContextDrawImage")
		purego.RegisterLibFunc(&cg.CGImageGetWidth, cgl, "CGImageGetWidth")
		purego.RegisterLibFunc(&cg.CGImageGetHeight, cgl, "CGImageGetHeight")
		purego.RegisterLibFunc(&cg.CGImageRelease, cgl, "CGImageRelease")
		purego.RegisterLibFunc(&cg.CGPDFDocumentCreateWithURL, cgl, "CGPDFDocumentCreateWithURL")
		purego.RegisterLibFunc(&cg.CGPDFDocumentRelease, cgl, "CGPDFDocumentRelease")
		purego.RegisterLibFunc(&cg.CGPDFDocumentGetNumberOfPages, cgl, "CGPDFDocumentGetNumberOfPages")
		purego.RegisterLibFunc(&cg.CGPDFDocumentIsUnlocked, cgl, "CGPDFDocumentIsUnlocked")
		purego.RegisterLibFunc(&cg.CGPDFDocumentGetPage, cgl, "CGPDFDocumentGetPage")
		purego.RegisterLibFunc(&cg.CGPDFPageGetBoxRect, cgl, "CGPDFPageGetBoxRect")
		purego.RegisterLibFunc(&cg.CGPDFPageGetRotationAngle, cgl, "CGPDFPageGetRotationAngle")
		purego.RegisterLibFunc(&cg.CGPDFPageGetDrawingTransform, cgl, "CGPDFPageGetDrawingTransform")
		purego.RegisterLibFunc(&cg.CGContextDrawPDFPage, cgl, "CGContextDrawPDFPage")

		purego.RegisterLibFunc(&cg.CGImageSourceCreateWithURL, iio, "CGImageSourceCreateWithURL")
		purego.RegisterLibFunc(&cg.CGImageSourceGetCount, iio, "CGImageSourceGetCount")
		purego.RegisterLibFunc(&cg.CGImageSourceCopyPropertiesAtIndex, iio, "CGImageSourceCopyPropertiesAtIndex")
		purego.RegisterLibFunc(&cg.CGImageSourceCreateThumbnailAtIndex, iio, "CGImageSourceCreateThumbnailAtIndex")
		cg.kPixelWidth = ref(iio, "kCGImagePropertyPixelWidth")
		cg.kPixelHeight = ref(iio, "kCGImagePropertyPixelHeight")
		cg.kOrientation = ref(iio, "kCGImagePropertyOrientation")
		cg.kThumbAlways = ref(iio, "kCGImageSourceCreateThumbnailFromImageAlways")
		cg.kThumbTransform = ref(iio, "kCGImageSourceCreateThumbnailWithTransform")
		cg.kThumbMaxSize = ref(iio, "kCGImageSourceThumbnailMaxPixelSize")
		cg.kShouldCacheImmediately = ref(iio, "kCGImageSourceShouldCacheImmediately")
	})
	return cg.err
}

// cfURL makes a file URL of a path. Release it with CFRelease.
func cfURL(path string) (uintptr, error) {
	if path == "" {
		return 0, errors.New("no path")
	}
	b := []byte(path)
	u := cg.CFURLCreateFromFileSystemRepresentation(0, &b[0], len(b), false)
	if u == 0 {
		return 0, errors.New("bad path")
	}
	return u, nil
}

// cfNumber makes a CFNumber of n. Release it with CFRelease.
func cfNumber(n int64) uintptr { return cg.CFNumberCreate(0, cfNumberSInt64Type, unsafe.Pointer(&n)) }

// cfInt reads a CFNumber in dict under key, 0 when there is none.
func cfInt(dict, key uintptr) int64 {
	v := cg.CFDictionaryGetValue(dict, key)
	if v == 0 {
		return 0
	}
	var n int64
	if !cg.CFNumberGetValue(v, cfNumberSInt64Type, unsafe.Pointer(&n)) {
		return 0
	}
	return n
}

// cfDict makes a CFDictionary of CF keys and values, which it retains.
// Release it with CFRelease.
func cfDict(keys, values []uintptr) uintptr {
	if len(keys) == 0 {
		return 0
	}
	return cg.CFDictionaryCreate(0, &keys[0], &values[0], len(keys), cg.keyCallBacks, cg.valueCallBacks)
}

// bitmap is a CGBitmapContext of w by h pixels, RGBA premultiplied, in
// memory CoreGraphics holds. Its y axis goes up, so a page or picture drawn
// in it the usual way comes out upright in its rows.
type bitmap struct {
	ctx  uintptr
	w, h int
}

func newBitmap(w, h int) (*bitmap, error) {
	cs := cg.CGColorSpaceCreateDeviceRGB()
	if cs == 0 {
		return nil, errors.New("no RGB color space")
	}
	defer cg.CGColorSpaceRelease(cs)
	ctx := cg.CGBitmapContextCreate(nil, uintptr(w), uintptr(h), 8, uintptr(4*w), cs, cgBitmapRGBA)
	if ctx == 0 {
		return nil, errors.New("no bitmap context")
	}
	return &bitmap{ctx: ctx, w: w, h: h}, nil
}

// image copies the bitmap into an image.RGBA, whose layout it shares.
func (b *bitmap) image() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, b.w, b.h))
	src := cg.CGBitmapContextGetData(b.ctx)
	if src != nil {
		copy(img.Pix, unsafe.Slice((*byte)(src), len(img.Pix)))
	}
	return img
}

func (b *bitmap) release() { cg.CGContextRelease(b.ctx) }
