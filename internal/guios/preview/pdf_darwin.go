//go:build darwin

package preview

import (
	"context"
	"image"
	"sync"
)

// Quartz draws PDF pages on macOS: the renderer Preview uses, built into
// the system, with no tool to install.

func init() { nativePDF = openQuartzPDF }

// quartzDoc is a PDF open in Quartz. Drawing pages of one document from two
// goroutines at once is not safe, so its calls take turns.
type quartzDoc struct {
	mu    sync.Mutex
	doc   uintptr
	path  string
	pages int
	sizes [][2]float64 // by page, rotated as shown
}

func openQuartzPDF(path string) (Doc, error) {
	if err := loadCG(); err != nil {
		return nil, err
	}
	u, err := cfURL(path)
	if err != nil {
		return nil, err
	}
	doc := cg.CGPDFDocumentCreateWithURL(u)
	cg.CFRelease(u)
	if doc == 0 {
		return nil, ErrUnreadable
	}
	if !cg.CGPDFDocumentIsUnlocked(doc) {
		cg.CGPDFDocumentRelease(doc)
		return nil, ErrLocked
	}
	n := int(cg.CGPDFDocumentGetNumberOfPages(doc))
	if n <= 0 {
		cg.CGPDFDocumentRelease(doc)
		return nil, ErrNoPages
	}
	d := &quartzDoc{doc: doc, path: path, pages: n}
	// Every page's size, as the viewer lays the pages out before it
	// draws them. A box read is cheap: 5,000 pages take a few ms.
	m := min(n, maxSizedPages)
	d.sizes = make([][2]float64, 0, m)
	for i := 1; i <= m; i++ {
		page := cg.CGPDFDocumentGetPage(doc, uintptr(i))
		if page == 0 {
			d.sizes = append(d.sizes, [2]float64{612, 792})
			continue
		}
		d.sizes = append(d.sizes, pageSize(page))
	}
	return d, nil
}

// pageSize is a page's crop box, turned by its rotation.
func pageSize(page uintptr) [2]float64 {
	r := cg.CGPDFPageGetBoxRect(page, cgPDFCropBox)
	w, h := r.W, r.H
	if w <= 0 || h <= 0 {
		w, h = 612, 792
	}
	if rot := ((cg.CGPDFPageGetRotationAngle(page) % 360) + 360) % 360; rot == 90 || rot == 270 {
		w, h = h, w
	}
	return [2]float64{w, h}
}

func (d *quartzDoc) Pages() int     { return d.pages }
func (d *quartzDoc) Engine() string { return "Quartz" }

func (d *quartzDoc) PageSize(i int) (w, h float64) {
	s := d.sizes[min(max(i, 0), len(d.sizes)-1)]
	return s[0], s[1]
}

func (d *quartzDoc) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc != 0 {
		cg.CGPDFDocumentRelease(d.doc)
		d.doc = 0
	}
}

func (d *quartzDoc) Render(ctx context.Context, i, widthPx int) (image.Image, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == 0 {
		return nil, ErrUnreadable
	}
	page := cg.CGPDFDocumentGetPage(d.doc, uintptr(i+1))
	if page == 0 {
		return nil, ErrPageFailed
	}
	size := pageSize(page)
	widthPx = min(max(widthPx, 16), MaxPagePx)
	scale := float64(widthPx) / size[0]
	heightPx := max(int(size[1]*scale+0.5), 1)
	if heightPx > 4*MaxPagePx {
		return nil, ErrPageFailed
	}
	bm, err := newBitmap(widthPx, heightPx)
	if err != nil {
		return nil, ErrPageFailed
	}
	defer bm.release()
	cg.CGContextSetRGBFillColor(bm.ctx, 1, 1, 1, 1)
	cg.CGContextFillRect(bm.ctx, cgRect{0, 0, float64(widthPx), float64(heightPx)})
	cg.CGContextSetInterpolationQuality(bm.ctx, 3) // kCGInterpolationHigh, for pictures in the page
	// The drawing transform never scales up, so the scale comes first and
	// the transform only moves and turns the page into a box of its size.
	cg.CGContextScaleCTM(bm.ctx, scale, scale)
	t := cg.CGPDFPageGetDrawingTransform(page, cgPDFCropBox, cgRect{0, 0, size[0], size[1]}, 0, true)
	cg.CGContextConcatCTM(bm.ctx, t)
	cg.CGContextDrawPDFPage(bm.ctx, page)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return bm.image(), nil
}

// Words reads a page's text with poppler's pdftotext where this Mac has it.
// Quartz has no text call; PDFKit's is Objective-C and not worth the bridge
// for a view the Mac's pictures already serve.
func (d *quartzDoc) Words(ctx context.Context, i int) (string, error) {
	return (&popplerDoc{path: d.path}).Words(ctx, i)
}
