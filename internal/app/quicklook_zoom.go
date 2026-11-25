package app

import (
	"context"
	"encoding/json"
	"image"
	"math"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Zoom and pan in quick look's picture: an image, a PDF page or a film frame,
// on a host that draws kitty graphics. Other hosts get no zoom.
//
// The host holds the picture, and a zoom or a pan step changes only its
// placement: the source rectangle (x, y, w, h) and the cell box (c, r). The
// placement goes out after the frame, so a burst of wheel notches between two
// frames costs one placement, and so does a frame of a drag.
//
// The scale is screen pixels per pixel of the file's own size, its base, so
// 100% shows the file's pixels at the host's. A PDF page has no pixels of its
// own: its base is the page as first read, at the size of the panes' area.
//
// The fit picture is on the host first. When the zoom wants more pixels than
// it has, a sharper copy is prepared off the loop, at a power of two times the
// fit size, up to the read's own pixels, and the placement moves to it when it
// is ready. When the zoom wants more pixels than the read holds, an image or a
// PDF page is read again at the size it needs, up to quickLookZoomReadPx. Until
// a sharper picture lands, the blurry one stays.

const (
	// quickLookZoomMax is the largest scale, 800%.
	quickLookZoomMax = 8.0
	// quickLookZoomWheel is the step of one wheel notch, quickLookZoomStep the
	// step of + and -.
	quickLookZoomWheel = 1.25
	quickLookZoomStep  = 1.5
	// quickLookCropUnit is the whole picture's side in a crop's units.
	quickLookCropUnit = 1 << 20
	// quickLookZoomReadPx is the long side of the sharpest read: the most a
	// machine sends (previewMaxPxLimit).
	quickLookZoomReadPx = 4096
	// quickLookZoomUploadPx bounds the pixels of a sharper copy on the host.
	quickLookZoomUploadPx = 16 << 20
)

// quickLookZoom is the zoom of quick look's picture. A zero scale is the fit.
type quickLookZoom struct {
	// scale is screen pixels per base pixel, and ox, oy the base pixel at the
	// top left of the view.
	scale  float64
	ox, oy float64
	// drag says a press on the zoomed picture is held: dragX and dragY are
	// where on the screen, and dragOX and dragOY the view's corner then.
	drag           bool
	dragX, dragY   int
	dragOX, dragOY float64
	// held is the last picture placed for the zoom, kept on screen until a
	// sharper one is ready.
	held   previewKey
	heldOK bool
	// sharpOut says a sharper read is out, sharpPx the long side it asks
	// for, and sharpCancel stops it.
	sharpOut    bool
	sharpPx     int
	sharpCancel context.CancelFunc
}

// quickLookSharpMsg carries a sharper read of the picture back to the loop.
type quickLookSharpMsg struct {
	target quickLookTarget
	page   int
	px     int
	img    *composer.Image
}

// quickLookZoomReset goes back to the fit and stops a sharper read.
func (ql *quickLookState) quickLookZoomReset() {
	if ql.zoom.sharpCancel != nil {
		ql.zoom.sharpCancel()
	}
	ql.zoom = quickLookZoom{}
}

// QuickLookZoomed reports whether the picture is zoomed in.
func (m *OS) QuickLookZoomed() bool { return m.quickLook.open && m.quickLook.zoom.scale > 0 }

// quickLookZoomable reports whether the picture on the panel can zoom: a
// picture the host draws with kitty graphics.
func (m *OS) quickLookZoomable() bool {
	ql := &m.quickLook
	r := ql.res
	return ql.open && r != nil && r.img != nil && r.target == ql.target && ql.copy == nil &&
		(r.kind == "image" || r.kind == "pdf" || r.kind == "video") &&
		m.replyPreviewer().Mode() == composer.PreviewPixels
}

// quickLookBase is the picture's size at 100%.
func quickLookBase(r *quickLookResult) (w, h float64) {
	if r.baseW > 0 && r.baseH > 0 {
		return float64(r.baseW), float64(r.baseH)
	}
	return float64(r.img.Width), float64(r.img.Height)
}

// quickLookSetBase sets the size at 100% of a fresh read: the file's own size
// when the machine said it and it has the picture's shape, else the picture's.
func quickLookSetBase(res *quickLookResult) {
	if res.img == nil {
		return
	}
	res.baseW, res.baseH = res.img.Width, res.img.Height
	if res.kind == "pdf" || res.width <= 0 || res.height <= 0 {
		return
	}
	own := float64(res.width) / float64(res.height)
	read := float64(res.img.Width) / float64(res.img.Height)
	if math.Abs(own-read) <= read/50 {
		res.baseW, res.baseH = res.width, res.height
	}
}

// quickLookCropRect is crop c in the pixels of a picture w by h, never empty.
func quickLookCropRect(c image.Rectangle, w, h int) image.Rectangle {
	u := int64(quickLookCropUnit)
	x0, y0 := int(int64(c.Min.X)*int64(w)/u), int(int64(c.Min.Y)*int64(h)/u)
	x1, y1 := int((int64(c.Max.X)*int64(w)+u-1)/u), int((int64(c.Max.Y)*int64(h)+u-1)/u)
	x0, y0 = max(min(x0, w-1), 0), max(min(y0, h-1), 0)
	return image.Rect(x0, y0, max(min(x1, w), x0+1), max(min(y1, h), y0+1))
}

// quickLookCell is the host's cell size in pixels, 8 by 16 when unknown.
func (m *OS) quickLookCell() (cw, ch int) {
	cw, ch = m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		return 8, 16
	}
	return cw, ch
}

// quickLookZoomGeom is what a zoomed view is laid out from: the room for the
// picture in cells, the cell size, the base size and the fit's scale.
type quickLookZoomGeom struct {
	availW, availH, cw, ch int
	bw, bh, fit            float64
}

// quickLookZoomView is a zoomed view: its cell box in the body, and the part
// of the picture it shows, in quickLookCropUnit of each side.
type quickLookZoomView struct {
	x, y, cols, rows int
	crop             image.Rectangle
}

// quickLookZoomGeomAt is the layout of the zoom in a body innerW by bodyH.
func (m *OS) quickLookZoomGeomAt(innerW, bodyH int) (quickLookZoomGeom, bool) {
	if !m.quickLookZoomable() {
		return quickLookZoomGeom{}, false
	}
	r := m.quickLook.res
	g := quickLookZoomGeom{}
	g.availW, g.availH = m.quickLookPicArea(innerW, bodyH)
	g.cw, g.ch = m.quickLookCell()
	g.bw, g.bh = quickLookBase(r)
	_, _, cols, rows := m.quickLookPicBox(r.img, innerW, bodyH)
	g.fit = min(float64(cols*g.cw)/g.bw, float64(rows*g.ch)/g.bh)
	return g, g.fit > 0
}

// quickLookZoomGeomNow is the layout of the zoom in the panel as it is now.
func (m *OS) quickLookZoomGeomNow() (quickLookZoomGeom, bool) {
	innerW, bodyH := m.quickLookSize()
	return m.quickLookZoomGeomAt(innerW, bodyH)
}

// quickLookZoomSpan is one side of a view at scale s: the cells it takes of
// the room, and the base pixels it shows. A picture narrower than the room
// shows whole, in the cells it fills.
func quickLookZoomSpan(base float64, room, cell int, s float64) (cells int, span float64) {
	if px := base * s; px <= float64(room*cell) {
		return max(min(int(px/float64(cell)+0.5), room), 1), base
	}
	return room, float64(room*cell) / s
}

// view lays out the view at scale s with its corner at ox, oy, and returns
// the corner held inside the picture.
func (g quickLookZoomGeom) view(s, ox, oy float64) (v quickLookZoomView, cx, cy float64) {
	cols, sw := quickLookZoomSpan(g.bw, g.availW, g.cw, s)
	rows, sh := quickLookZoomSpan(g.bh, g.availH, g.ch, s)
	cx = max(min(ox, g.bw-sw), 0)
	cy = max(min(oy, g.bh-sh), 0)
	v.cols, v.rows = cols, rows
	v.x, v.y = 1+(g.availW-cols)/2, 1+(g.availH-rows)/2
	u := float64(quickLookCropUnit)
	v.crop = image.Rect(int(cx/g.bw*u+0.5), int(cy/g.bh*u+0.5), int((cx+sw)/g.bw*u+0.5), int((cy+sh)/g.bh*u+0.5))
	return v, cx, cy
}

// quickLookZoomNow is the scale and the corner now, the fit's when not zoomed.
func (ql *quickLookState) quickLookZoomNow(g quickLookZoomGeom) (s, ox, oy float64) {
	if ql.zoom.scale <= 0 {
		return g.fit, 0, 0
	}
	return ql.zoom.scale, ql.zoom.ox, ql.zoom.oy
}

// quickLookZoomTo zooms to scale s and keeps the picture's point at ax, ay
// where it is. ax and ay are pixels from the view's top left corner. A scale
// at the fit or below it goes back to the fit.
func (m *OS) quickLookZoomTo(g quickLookZoomGeom, s, ax, ay float64) {
	ql := &m.quickLook
	s = min(s, quickLookZoomMax)
	if s <= g.fit*1.001 {
		m.quickLookZoomFit()
		return
	}
	old, ox, oy := ql.quickLookZoomNow(g)
	was, ox, oy := g.view(old, ox, oy)
	bx, by := ox+ax/old, oy+ay/old
	next, _, _ := g.view(s, 0, 0)
	// The view's box can move as it grows: the point stays on the same
	// screen pixel.
	ax += float64((was.x - next.x) * g.cw)
	ay += float64((was.y - next.y) * g.ch)
	z := &ql.zoom
	z.scale = s
	_, z.ox, z.oy = g.view(s, bx-ax/s, by-ay/s)
	// The keys move the view now, so they act inside the file.
	ql.inside = true
}

// quickLookZoomFit goes back to the fit. A picture with nothing else to move
// through gives the keyboard back to the list.
func (m *OS) quickLookZoomFit() {
	ql := &m.quickLook
	ql.quickLookZoomReset()
	if !ql.free && !quickLookMoves(ql.res) {
		ql.inside = false
	}
}

// QuickLookZoom runs a zoom key: zoom_in, zoom_out, zoom_fit or zoom_actual,
// around the middle of the view. It reports false where the picture cannot
// zoom, so the key goes on.
func (m *OS) QuickLookZoom(act string) bool {
	g, ok := m.quickLookZoomGeomNow()
	if !ok {
		return false
	}
	ql := &m.quickLook
	s, ox, oy := ql.quickLookZoomNow(g)
	v, _, _ := g.view(s, ox, oy)
	ax, ay := float64(v.cols*g.cw)/2, float64(v.rows*g.ch)/2
	switch act {
	case "zoom_in":
		m.quickLookZoomTo(g, s*quickLookZoomStep, ax, ay)
	case "zoom_out":
		m.quickLookZoomTo(g, s/quickLookZoomStep, ax, ay)
	case "zoom_fit":
		m.quickLookZoomFit()
	case "zoom_actual":
		m.quickLookZoomTo(g, 1, ax, ay)
	default:
		return false
	}
	return true
}

// QuickLookPan moves the zoomed view by an eighth of it for a key that moves
// a line or a column. It reports false when the picture is not zoomed.
func (m *OS) QuickLookPan(act string) bool {
	ql := &m.quickLook
	g, ok := m.quickLookZoomGeomNow()
	if !ok || ql.zoom.scale <= 0 {
		return false
	}
	v, _, _ := g.view(ql.zoom.scale, ql.zoom.ox, ql.zoom.oy)
	dx, dy := float64(v.cols*g.cw)/8, float64(v.rows*g.ch)/8
	switch act {
	case "line_down":
		dx = 0
	case "line_up":
		dx, dy = 0, -dy
	case "column_left":
		dx, dy = -dx, 0
	case "column_right":
		dy = 0
	default:
		return false
	}
	z := &ql.zoom
	_, z.ox, z.oy = g.view(z.scale, z.ox+dx/z.scale, z.oy+dy/z.scale)
	return true
}

// quickLookOnPicture reports whether screen cell x, y is on the picture, and
// where in it, in pixels from its top left corner, to the cell's middle.
func (m *OS) quickLookOnPicture(g quickLookZoomGeom, x, y int) (ax, ay float64, ok bool) {
	pic := m.quickLook.pic
	if !pic.ok || x < pic.x || x >= pic.x+pic.cols || y < pic.y || y >= pic.y+pic.rows {
		return 0, 0, false
	}
	return (float64(x-pic.x) + 0.5) * float64(g.cw), (float64(y-pic.y) + 0.5) * float64(g.ch), true
}

// quickLookZoomWheel zooms around the pointer for a wheel notch over the
// picture. It reports false off the picture, where the wheel scrolls.
func (m *OS) quickLookZoomWheel(x, y int, up bool) bool {
	g, ok := m.quickLookZoomGeomNow()
	if !ok {
		return false
	}
	ax, ay, on := m.quickLookOnPicture(g, x, y)
	if !on {
		return false
	}
	s, _, _ := m.quickLook.quickLookZoomNow(g)
	if up {
		s *= quickLookZoomWheel
	} else {
		s /= quickLookZoomWheel
	}
	m.quickLookZoomTo(g, s, ax, ay)
	return true
}

// quickLookPanPress takes a press on the zoomed picture: a drag from it pans.
func (m *OS) quickLookPanPress(x, y int) bool {
	ql := &m.quickLook
	g, ok := m.quickLookZoomGeomNow()
	if !ok || ql.zoom.scale <= 0 {
		return false
	}
	if _, _, on := m.quickLookOnPicture(g, x, y); !on {
		return false
	}
	z := &ql.zoom
	z.drag, z.dragX, z.dragY, z.dragOX, z.dragOY = true, x, y, z.ox, z.oy
	return true
}

// quickLookPanDrag moves the view with the pointer while a press on the
// picture is held. It reports whether one is.
func (m *OS) quickLookPanDrag(x, y int) bool {
	ql := &m.quickLook
	z := &ql.zoom
	if !ql.open || !z.drag {
		return false
	}
	g, ok := m.quickLookZoomGeomNow()
	if !ok || z.scale <= 0 {
		z.drag = false
		return false
	}
	dx, dy := float64((x-z.dragX)*g.cw)/z.scale, float64((y-z.dragY)*g.ch)/z.scale
	_, z.ox, z.oy = g.view(z.scale, z.dragOX-dx, z.dragOY-dy)
	return true
}

// quickLookZoomLabel is the zoom in the footer: "zoom" at the fit, else the
// scale, as "zoom 250%".
func (m *OS) quickLookZoomLabel() string {
	s := m.quickLook.zoom.scale
	if s <= 0 {
		return "zoom"
	}
	return "zoom " + strconv.Itoa(int(math.Round(s*100))) + "%"
}

// quickLookZoomKey is the picture the zoom shows best: the fit picture at a
// power of two of its size, up to the read's own pixels.
func quickLookZoomKey(g quickLookZoomGeom, s float64, img *composer.Image, cols, rows int) previewKey {
	k := pixelsKey(img, cols, rows)
	// The view spans s*bw screen pixels across the whole picture, and the
	// copy needs no more pixels than the read has.
	fitW := float64(cols * g.cw)
	need := min(s*g.bw, float64(img.Width))
	z := 1
	for fitW*float64(z) < need*0.99 && fitW*float64(z) < float64(img.Width) {
		z *= 2
	}
	if z > 1 {
		k.zoom = z
	}
	return k
}

// quickLookZoomPic is the picture placed for the zoomed view v: the one the
// zoom wants when it is ready, else the last one placed, else the fit
// picture.
func (m *OS) quickLookZoomPic(g quickLookZoomGeom, img *composer.Image, cols, rows int, v quickLookZoomView) quickLookPic {
	ql := &m.quickLook
	z := &ql.zoom
	p := m.replyPreviewer()
	want := quickLookZoomKey(g, z.scale, img, cols, rows)
	ready := func(k previewKey) bool {
		pic := p.pics[k]
		return pic != nil && len(pic.png) > 0
	}
	p.mu.Lock()
	k := want
	switch {
	case ready(want):
	case z.heldOK && ready(z.held):
		k = z.held
	default:
		k = pixelsKey(img, cols, rows)
	}
	if ready(k) {
		z.held, z.heldOK = k, true
	}
	p.mu.Unlock()
	return quickLookPic{ok: true, key: k, x: v.x, y: v.y, cols: v.cols, rows: v.rows, crop: v.crop}
}

// quickLookZoomPrepare prepares the picture the zoom wants, and asks for a
// sharper read when the zoom wants more pixels than the read holds. It is nil
// at the fit.
func (m *OS) quickLookZoomPrepare(innerW, bodyH int) tea.Cmd {
	ql := &m.quickLook
	if ql.zoom.scale <= 0 {
		return nil
	}
	g, ok := m.quickLookZoomGeomAt(innerW, bodyH)
	if !ok {
		return nil
	}
	r := ql.res
	_, _, cols, rows := m.quickLookPicBox(r.img, innerW, bodyH)
	cmd := m.quickLookPrepareKey(r.img, quickLookZoomKey(g, ql.zoom.scale, r.img, cols, rows))
	return tea.Batch(cmd, m.quickLookSharpRead(g))
}

// quickLookSharpRead reads an image or a PDF page again at the size the zoom
// wants, when the read holds fewer pixels. One read is out at a time, and a
// size is asked for once.
func (m *OS) quickLookSharpRead(g quickLookZoomGeom) tea.Cmd {
	ql := &m.quickLook
	r, z := ql.res, &ql.zoom
	if z.sharpOut || (r.kind != "image" && r.kind != "pdf") {
		return nil
	}
	long := max(g.bw, g.bh)
	have := max(r.img.Width, r.img.Height)
	need := int(math.Ceil(z.scale * long))
	// Steps of 512 pixels, so a burst of notches asks once.
	need = min((need+511)/512*512, quickLookZoomReadPx)
	if r.kind == "image" {
		// An image has no more pixels than its own.
		need = min(need, int(long))
	}
	if need <= have*9/8 || need <= z.sharpPx {
		return nil
	}
	t, page, paged := ql.target, r.page, quickLookPaged(r)
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if t.host != "" {
		call = m.verbOnHostCaller()
	}
	want := session.PreviewWant{MaxPx: need}
	if paged {
		want.Page, want.PagesKnown = page, true
	}
	ascii := overlay.UseASCII()
	ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
	z.sharpOut, z.sharpPx, z.sharpCancel = true, need, cancel
	return func() tea.Msg {
		defer cancel()
		raw, err := quickLookWantReply(ctx, call, t, want)
		msg := quickLookSharpMsg{target: t, page: page, px: need}
		if err == nil {
			msg.img = quickLookDecode(t, raw, nil, ascii).img
		}
		return msg
	}
}

// applyQuickLookSharp puts a sharper read in place of the picture, when the
// zoom still wants it and it has the picture's shape.
func (m *OS) applyQuickLookSharp(msg quickLookSharpMsg) {
	ql := &m.quickLook
	z := &ql.zoom
	if !z.sharpOut || msg.px != z.sharpPx {
		return
	}
	z.sharpOut, z.sharpCancel = false, nil
	r := ql.res
	if !ql.open || z.scale <= 0 || r == nil || r.img == nil || msg.img == nil || msg.target != ql.target ||
		(quickLookPaged(r) && r.page != msg.page) || msg.img.Width <= r.img.Width {
		return
	}
	read := float64(r.img.Width) / float64(r.img.Height)
	if math.Abs(float64(msg.img.Width)/float64(msg.img.Height)-read) > read/50 {
		return
	}
	msg.img.Name = r.img.Name
	r.img = msg.img
}
