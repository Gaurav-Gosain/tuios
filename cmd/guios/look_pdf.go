package main

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/preview"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// A PDF scrolls as one column of pages, as Preview and Quick Look show one.
// The page sizes are known when it opens, so every page has its place before
// it is drawn, and only the pages in view (and one each side) are drawn: by
// Quartz on macOS, by pdftoppm elsewhere, at the pixels the viewer shows
// them at. A page shows as soon as it is drawn, and pages far from the view
// are let go.
//
// J and K go a page, the wheel and the bar scroll, + - 0 and a pinch zoom
// about the pointer, z goes between fit to width and fit to page, and :
// (or a click on the page count) asks for a page.

const (
	pagePad = 16 // around the column of pages
	pageGap = 12 // between pages
	// pageAsksMax is the pages drawn at once.
	pageAsksMax = 2
	// pagesKept bounds the pages held drawn.
	pagesKept = 8
)

// lookPDF is a PDF's state.
type lookPDF struct {
	doc   preview.Doc
	pages int
	err   string
	// sizes are the pages' sizes in points, tops their places from the
	// first page's top, in points, without the gaps, and widest the widest.
	sizes  [][2]float64
	tops   []float64
	widest float64

	scroll       ui.ScrollState
	viewW, viewH float32
	// zoomK multiplies the fit; fitPage fits a whole page in place of the
	// width; shownZoom is the DIPs a point the last frame showed.
	zoomK     float32
	fitPage   bool
	shownZoom float32
	devScale  float32

	page int // the page at the top third of the view
	keep int // the page to show once it opens, from 1; 0 for none
	pics map[int]*pagePic
	asks map[int]*pageAsk

	goTo     bool
	goToText string
	// layoutTries counts the frames built before the view had a size.
	layoutTries int
}

// pagePic is a page drawn at px pixels wide. Its pixels stay on the CPU
// too: packing pages as PNGs, so they could leave it, cost a hitch of 30 to
// 50 ms each time a packed page came back into view (its upload and its
// decode on the UI thread), so pagesKept bounds them instead.
type pagePic struct {
	bm *ui.Bitmap
	px int
}

// pageAsk is a page being drawn.
type pageAsk struct {
	px     int
	cancel context.CancelFunc
}

// startPDF opens the document on a goroutine.
func (a *App) startPDF(l *fileLook) {
	if l.pdf != nil && (l.pdf.doc != nil || l.pdf.err != "") {
		return
	}
	pd := l.pdf
	if pd == nil {
		pd = &lookPDF{zoomK: 1}
		l.pdf = pd
	}
	if pd.zoomK == 0 {
		pd.zoomK = 1
	}
	ctx, path := l.ctx, l.path
	go func() {
		doc, err := preview.OpenPDF(ctx, path)
		if ctx.Err() != nil {
			if doc != nil {
				doc.Close()
			}
			return
		}
		var sizes [][2]float64
		if err == nil {
			n := doc.Pages()
			sizes = make([][2]float64, n)
			for i := range n {
				w, h := doc.PageSize(i)
				if w <= 0 || h <= 0 {
					w, h = 612, 792
				}
				sizes[i] = [2]float64{w, h}
			}
		}
		a.update(func() {
			if ctx.Err() != nil {
				if doc != nil {
					doc.Close()
				}
				return
			}
			if err != nil {
				pd.err = pdfMessage(err)
				return
			}
			pd.doc, pd.pages, pd.sizes = doc, len(sizes), sizes
			pd.tops = make([]float64, len(sizes)+1)
			for i, s := range sizes {
				pd.tops[i+1] = pd.tops[i] + s[1]
				pd.widest = max(pd.widest, s[0])
			}
			pd.pics, pd.asks = map[int]*pagePic{}, map[int]*pageAsk{}
			l.mark("opened " + itoa(pd.pages) + " pages")
			if pd.keep > 0 {
				a.pdfGo(l, pd.keep-1)
				pd.keep = 0
			}
		})
	}()
}

// pdfMessage is what the viewer says of a PDF it cannot open.
func pdfMessage(err error) string {
	if nt, ok := err.(preview.ErrNoTool); ok {
		return locales.LookNoPoppler(string(nt))
	}
	return lookErrText(err)
}

// ptScale is the DIPs a point at the zoom: the widest page fits the width,
// or the tallest fits the height with fitPage.
func (pd *lookPDF) ptScale() float32 {
	if pd.widest <= 0 || pd.viewW <= 0 {
		return 1
	}
	s := (pd.viewW - 2*pagePad) / float32(pd.widest)
	if pd.fitPage && pd.viewH > 0 {
		tallest := 0.0
		for _, sz := range pd.sizes[:min(len(pd.sizes), 200)] {
			tallest = max(tallest, sz[1])
		}
		s = min(s, (pd.viewH-2*pagePad)/float32(tallest))
	}
	return max(s*pd.zoomK, 0.05)
}

// pageTop is page i's top in the content, in DIPs at scale s.
func (pd *lookPDF) pageTop(i int, s float32) float32 {
	return pagePad + float32(pd.tops[i])*s + float32(i)*pageGap
}

// pageAt is the page at y in the content.
func (pd *lookPDF) pageAt(y, s float32) int {
	i := sort.Search(pd.pages, func(i int) bool { return pd.pageTop(i+1, s) > y })
	return min(max(i, 0), pd.pages-1)
}

// pdfGo scrolls to page i.
func (a *App) pdfGo(l *fileLook, i int) {
	pd := l.pdf
	if pd == nil || pd.doc == nil {
		return
	}
	i = min(max(i, 0), pd.pages-1)
	pd.page = i
	pd.scroll.ScrollTo(pd.scroll.X, pd.pageTop(i, pd.ptScale())-pagePad/2, ui.Smooth)
}

// pdfZoom zooms by k about the view point (px, py), the centre for -1; 0
// goes back to fit.
func (a *App) pdfZoom(l *fileLook, k, px, py float32) {
	pd := l.pdf
	if pd == nil || pd.doc == nil {
		return
	}
	if px < 0 {
		px, py = pd.viewW/2, pd.viewH/2
	}
	old := pd.ptScale()
	if k == 0 {
		pd.zoomK = 1
	} else {
		pd.zoomK = min(max(pd.zoomK*k, 0.25), 8)
	}
	ns := pd.ptScale()
	// The point under (px, py) stays there: its place in the content, in
	// points, is the same at both scales, but for the pad and the gaps.
	cy := pd.scroll.Y + py
	page := pd.pageAt(cy, old)
	within := (cy - pd.pageTop(page, old)) / old
	cx := pd.scroll.X + px
	jumpTo(&pd.scroll, max((cx-pagePad)*ns/old+pagePad-px, 0), max(pd.pageTop(page, ns)+within*ns-py, 0))
}

// jumpTo scrolls to x, y at once, ending a glide on its way.
func jumpTo(st *ui.ScrollState, x, y float32) {
	st.X, st.Y = x, y
	st.ScrollTo(x, y, ui.Animation{Duration: time.Nanosecond})
}

// pdfView is the column of pages and its bar.
func (a *App) pdfView(c *ui.Context, pal *palette, l *fileLook) {
	pd := l.pdf
	switch {
	case pd == nil:
		lookWaiting(c, pal, l)
		return
	case pd.err != "":
		lookFailed(c, pal, pd.err)
		return
	case pd.doc == nil:
		lookWaiting(c, pal, l)
		return
	}
	ui.Column(c.Key("pdf-view")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		sc := ui.ScrollBoth(c.Key("pages")).TrackScroll(&pd.scroll).Grow(1).MinHeight(0).Background(pal.sidebar.Alpha(0.35))
		if b := sc.Bounds(); b.W > 0 && (b.W != pd.viewW || b.H != pd.viewH) {
			// The view changed size: the page at the top stays there, at
			// the new scale, and the frame builds again at it.
			old := pd.ptScale()
			top := pd.pageAt(pd.scroll.Y, old)
			within := (pd.scroll.Y - pd.pageTop(top, old)) / old
			first := pd.viewW == 0
			pd.viewW, pd.viewH = b.W, b.H
			if !first {
				ns := pd.ptScale()
				jumpTo(&pd.scroll, pd.scroll.X, max(pd.pageTop(top, ns)+within*ns, 0))
			}
			c.Invalidate()
		}
		if pd.viewW == 0 && pd.layoutTries < 30 {
			// Not laid out yet: the next frame knows the size, and asks
			// for the pages at it.
			pd.layoutTries++
			c.Invalidate()
		}
		sc.HandleInput(func(ev ui.InputEvent) bool {
			switch {
			case ev.Kind == ui.InputZoom:
				a.pdfZoom(l, ev.Scale, ev.X, ev.Y)
			case ev.Kind == ui.InputScroll && ev.Mods&(ui.Ctrl|ui.Super) != 0:
				k := float32(1.15)
				if ev.DY > 0 {
					k = 1 / 1.15
				}
				if ev.Precise {
					k = 1 - ev.DY/300
				}
				a.pdfZoom(l, k, ev.X, ev.Y)
			default:
				return false
			}
			return true
		})
		s := pd.ptScale()
		pd.shownZoom = s
		contentH := pd.pageTop(pd.pages, s) - pageGap + pagePad
		contentW := max(pd.viewW, float32(pd.widest)*s+2*pagePad)
		// The pages in view, and one screen each side.
		top, bottom := pd.scroll.Y-pd.viewH, pd.scroll.Y+2*pd.viewH
		first, last := pd.pageAt(max(top, 0), s), pd.pageAt(bottom, s)
		if pd.viewH > 0 {
			pd.page = pd.pageAt(pd.scroll.Y+pd.viewH/3, s)
		}
		sc.Children(func() {
			ui.Box(c.Key("content")).Width(contentW).Height(contentH).Shrink(0).Children(func() {
				for i := first; i <= last; i++ {
					a.pdfPage(c, pal, pd, i, s, contentW)
				}
			})
		})
		a.pdfWant(l, first, last, s)
		a.pdfBar(c, pal, l)
	})
}

// pdfPage is one page in its place: the page's ground, its picture once
// drawn, and its number until then.
func (a *App) pdfPage(c *ui.Context, pal *palette, pd *lookPDF, i int, s, contentW float32) {
	w, h := float32(pd.sizes[i][0])*s, float32(pd.sizes[i][1])*s
	x := (contentW - w) / 2
	pic := pd.pics[i]
	box := ui.Box(c.Key(i)).Absolute().Left(x).Top(pd.pageTop(i, s)).Width(w).Height(h).Radius(2).Background(ui.RGB(255, 255, 255)).
		Border(1, pal.divider)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		pd.devScale = p.Scale()
		if pic != nil {
			p.Image(pic.bm, r, ui.FillBox)
		}
	})
	if pic == nil {
		box.Children(func() {
			ui.Text(c, itoa(i+1)).FontSize(13).TextColor(ui.RGBA(0, 0, 0, 0.3)).Absolute().Left(0).Right(0).Top(h/2 - 9).TextAlign(ui.Center)
		})
	}
}

// pdfWant asks for the pages near the view, first to last, drawn at the
// pixels they show at, nearest first, and lets go of the pages far from the
// view.
func (a *App) pdfWant(l *fileLook, first, last int, s float32) {
	pd := l.pdf
	if pd.viewW == 0 {
		return // the scale is known from the first laid out frame
	}
	scale := pd.devScale
	if scale == 0 {
		scale = 2
	}
	// Steps of 128 px, so a small resize draws nothing again.
	want := func(i int) int {
		px := int(math.Ceil(float64(float32(pd.sizes[i][0])*s*scale)/128)) * 128
		return min(max(px, 256), preview.MaxPagePx)
	}
	// Asks for pages that left the view stop.
	for i, ask := range pd.asks {
		if i < first-1 || i > last+1 {
			ask.cancel()
			delete(pd.asks, i)
		}
	}
	// Pages far away go.
	if len(pd.pics) > pagesKept {
		for i := range pd.pics {
			if i < first-2 || i > last+2 {
				delete(pd.pics, i)
			}
		}
	}
	mid := pd.page
	order := make([]int, 0, last-first+1)
	for i := first; i <= last; i++ {
		order = append(order, i)
	}
	sort.Slice(order, func(x, y int) bool { return absInt(order[x]-mid) < absInt(order[y]-mid) })
	for _, i := range order {
		if len(pd.asks) >= pageAsksMax {
			return
		}
		px := want(i)
		if pic := pd.pics[i]; pic != nil && pic.px >= px*7/8 {
			continue
		}
		if ask := pd.asks[i]; ask != nil {
			if ask.px >= px*7/8 {
				continue
			}
			ask.cancel()
		}
		a.pdfRender(l, i, px)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// pdfRender draws page i at px pixels wide on a goroutine, shows it, then
// packs it.
func (a *App) pdfRender(l *fileLook, i, px int) {
	pd := l.pdf
	ctx, cancel := context.WithCancel(l.ctx)
	ask := &pageAsk{px: px, cancel: cancel}
	pd.asks[i] = ask
	doc := pd.doc
	go func() {
		img, err := doc.Render(ctx, i, px)
		var bm *ui.Bitmap
		if err == nil {
			bm = ui.NewBitmap(img)
		}
		a.update(func() {
			if pd.asks[i] == ask {
				delete(pd.asks, i)
			}
			if ctx.Err() != nil || err != nil {
				return
			}
			pd.pics[i] = &pagePic{bm: bm, px: px}
			l.mark("page " + itoa(i+1) + " drawn at " + itoa(px))
		})
	}()
}

// pdfBar is the bar under the pages: the page and its count, which a click
// turns into a field for a page, the zoom, and the fit.
func (a *App) pdfBar(c *ui.Context, pal *palette, l *fileLook) {
	pd := l.pdf
	ui.Row(c.Key("pdf-bar")).Height(36).Shrink(0).Padding(0, 10).Gap(4).AlignItems(ui.Center).Children(func() {
		iconButton(c, "pdf-prev", chevronUpIcon, locales.LookPrevPage(), pal, func() { a.pdfGo(l, pd.page-1) })
		iconButton(c, "pdf-next", chevronDownIcon, locales.LookNextPage(), pal, func() { a.pdfGo(l, pd.page+1) })
		if pd.goTo {
			in := ui.TextInput(c.Key("pdf-goto"), &pd.goToText).Width(64).Height(24).FontSize(12).AutoFocus().Placeholder(itoa(pd.page + 1))
			if in.Submitted() {
				if n, err := strconv.Atoi(strings.TrimSpace(pd.goToText)); err == nil {
					a.pdfGo(l, n-1)
				}
				pd.goTo, pd.goToText = false, ""
				a.focusViewer()
			}
			if in.Shortcut(0, ui.KeyEscape) {
				pd.goTo, pd.goToText = false, ""
				a.focusViewer()
			}
			ui.Text(c, locales.LookOfPages(itoa(pd.pages))).FontSize(12).FontFeatures("tnum").TextColor(pal.textMuted).SingleLine()
		} else {
			t := ui.ButtonBase(c.Key("pdf-page")).Height(24).Padding(0, 6).Radius(6).Center().Tooltip(locales.LookGoToPage()).Label(locales.LookGoToPage())
			if t.Hovered() {
				t.Background(pal.rowHover)
			}
			t.Children(func() {
				ui.Text(c, locales.LookPageOf(itoa(pd.page+1), itoa(pd.pages))).FontSize(12).FontFeatures("tnum").TextColor(pal.textMuted).SingleLine()
			})
			if t.Clicked() {
				pd.goTo = true
			}
		}
		ui.Spacer(c)
		iconButton(c, "pdf-out", minusIcon, locales.LookZoomOut(), pal, func() { a.pdfZoom(l, 0.8, -1, -1) })
		z := ui.ButtonBase(c.Key("pdf-zoom")).Height(24).Width(52).Radius(6).Center().Tooltip(locales.LookZoomFit()).Label(locales.LookZoomFit())
		if z.Hovered() {
			z.Background(pal.rowHover)
		}
		z.Children(func() {
			ui.Text(c, itoa(int(pd.shownZoom*100+0.5))+"%").FontSize(12).FontFeatures("tnum").TextColor(pal.textMuted).SingleLine()
		})
		if z.Clicked() {
			a.pdfZoom(l, 0, -1, -1)
		}
		iconButton(c, "pdf-in", plusIcon, locales.LookZoomIn(), pal, func() { a.pdfZoom(l, 1.25, -1, -1) })
		fit := locales.LookFitPage()
		fitIcon := expandIcon
		if pd.fitPage {
			fit, fitIcon = locales.LookFitWidth(), collapseIcon
		}
		iconButton(c, "pdf-fit", fitIcon, fit, pal, func() { a.pdfFit(l) })
	})
}

// pdfFit goes between fit to the width and fit to the page, the page in
// view staying in view.
func (a *App) pdfFit(l *fileLook) {
	pd := l.pdf
	page := pd.page
	pd.fitPage, pd.zoomK = !pd.fitPage, 1
	jumpTo(&pd.scroll, 0, pd.pageTop(page, pd.ptScale())-pagePad/2)
}

// lookGoTo opens the field for a page, or a line of a large text.
func (a *App) lookGoTo(l *fileLook) {
	switch {
	case l.current() == viewOwn && l.pdf != nil && l.pdf.doc != nil:
		l.pdf.goTo = true
	case l.current() == viewText && l.big != nil:
		l.big.prompt, l.big.query = promptGoTo, ""
	}
}

var minusIcon = icon(`<path d="M3.5 8h9"/>`)
