package app

import (
	"errors"
	"image"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// The image view: one picture of the draft at the largest size the screen
// holds, over the whole agents view (DESIGN.md 3.10, frame f). It names the
// image, its size in pixels and bytes and where it came from. o opens it in
// the desktop's viewer, c saves a copy and copies its path, s saves a copy,
// left and right step through the draft's images, and esc or a click outside
// the picture closes it.
//
// On a kitty host the picture zooms as quick look's does (quicklook_zoom.go):
// + and - zoom, the wheel zooms around the pointer, 0 fits and 1 shows 100%.
// A zoomed picture moves with a drag and with the arrows and h, j, k and l. A
// zoom or a move step sends no picture: a placement shows the part in view
// with a source rect, and placeholder cells show it by the cells they name,
// of one virtual placement the size of the whole zoomed picture.
//
// The picture goes through the reply previews' pipeline: kitty graphics
// placed after the frame where the host has them, block glyphs from
// shot/mosaic where it does not, and a line of words at 16 colours. It is
// prepared once per image and size, off the update loop.

// ckImageView is the open image view.
type ckImageView struct {
	imgs []*composer.Image
	idx  int
	who  string
	// from says where an image of the conversation came from, in place of
	// who for a draft's.
	from string
	// saved is where each image was saved, by index, so c and s write
	// one copy.
	saved map[int]string
	// box is the picture's cell box in the last frame, on the screen.
	box          overlay.Rect
	cols, rows   int
	mode         composer.PreviewMode
	placeX, plcY int
	// zoom is the picture's zoom, and fitCols and fitRows the box at the
	// fit, which its pictures are prepared for. key is the picture placed.
	zoom             picZoom
	fitCols, fitRows int
	key              previewKey
	// crop is the part of the picture in the box, for a placement. ph says
	// the picture is drawn as placeholder cells: phCols and phRows are the
	// whole picture's cell box at the zoom, and phCol and phRow the first
	// cell in view.
	crop                         image.Rectangle
	ph                           bool
	phCols, phRows, phCol, phRow int
}

// CockpitImageOpen reports whether the image view is up.
func (m *OS) CockpitImageOpen() bool { return m.ShowInbox && m.Inbox.ck.img != nil }

// cockpitOpenImage opens the view on the draft's image i.
func (m *OS) cockpitOpenImage(i int) tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	imgs := ckDraftImages(ed.comp)
	if i < 0 || i >= len(imgs) {
		return nil
	}
	m.Inbox.ck.img = &ckImageView{imgs: imgs, idx: i, who: ed.who, saved: map[int]string{}}
	m.cockpitTouch()
	// A click opens the view, and the mouse path asks for no picture.
	return m.CockpitImagePicture()
}

// CockpitImageKey handles a key while the image view is up.
func (m *OS) CockpitImageKey(key string) tea.Cmd {
	v := m.Inbox.ck.img
	if v == nil {
		return nil
	}
	m.cockpitTouch()
	switch key {
	case "esc", "q":
		m.cockpitCloseImage()
	case "left", "h":
		// A zoomed picture moves. At the fit the key shows the image before.
		if !m.cockpitImageZoom("column_left") {
			v.idx, v.zoom = (v.idx-1+len(v.imgs))%len(v.imgs), picZoom{}
		}
	case "right", "l":
		if !m.cockpitImageZoom("column_right") {
			v.idx, v.zoom = (v.idx+1)%len(v.imgs), picZoom{}
		}
	case "up", "k":
		m.cockpitImageZoom("line_up")
	case "down", "j":
		m.cockpitImageZoom("line_down")
	case "+", "=":
		m.cockpitImageZoom("zoom_in")
	case "-", "_":
		m.cockpitImageZoom("zoom_out")
	case "0":
		m.cockpitImageZoom("zoom_fit")
	case "1":
		m.cockpitImageZoom("zoom_actual")
	case "o":
		return m.replyOpenImage(v.imgs[v.idx])
	case "s":
		if path := m.cockpitSaveImage(); path != "" {
			m.ShowNotification("Saved to "+path, "success", m.Settings.NotificationDuration)
		}
	case "c":
		if path := m.cockpitSaveImage(); path != "" {
			cmd := m.CopyToClipboard(path)
			m.ShowNotification("Saved to "+path+". The path is copied.", "success", m.Settings.NotificationDuration)
			return cmd
		}
	}
	return nil
}

// cockpitCloseImage closes the image view.
func (m *OS) cockpitCloseImage() {
	m.Inbox.ck.img = nil
	m.cockpitTouch()
}

// cockpitImageClick handles a click while the view is up: one outside the
// picture closes it, and one on the zoomed picture starts a drag.
func (m *OS) cockpitImageClick(x, y int) {
	v := m.Inbox.ck.img
	switch {
	case !v.box.Contains(x, y):
		m.cockpitCloseImage()
	case v.zoom.scale > 0:
		v.zoom.press(x, y)
	}
}

// cockpitImageGeom is the layout of the zoom in a canvas w by h cells, from
// the last layout. ok is false where the picture does not zoom: a host with
// no kitty graphics, or a picture tuios cannot decode.
func (m *OS) cockpitImageGeom(w, h int) (quickLookZoomGeom, bool) {
	v := m.Inbox.ck.img
	img := v.imgs[v.idx]
	if v.mode != composer.PreviewPixels || img.Width <= 0 || img.Height <= 0 || v.fitCols <= 0 {
		return quickLookZoomGeom{}, false
	}
	margin := ckImageMargin(w)
	g := quickLookZoomGeom{availW: max(w-2*margin-4, 1), availH: max(h-7, 3) - 2}
	g.cw, g.ch = m.quickLookCell()
	// The base is the image's own pixels: the view has all of them.
	g.bw, g.bh = float64(img.Width), float64(img.Height)
	g.fit = min(float64(v.fitCols*g.cw)/g.bw, float64(v.fitRows*g.ch)/g.bh)
	// A small image fits above 100%: it still zooms a few steps.
	g.top = max(quickLookZoomMax, g.fit*quickLookZoomStep*quickLookZoomStep)
	if v.ph {
		// Placeholder cells name at most placeholderMaxCells rows and
		// columns of the zoomed picture.
		g.top = min(g.top, float64(placeholderMaxCells*g.cw)/g.bw, float64(placeholderMaxCells*g.ch)/g.bh)
	}
	return g, g.fit > 0 && g.top > g.fit*1.001
}

// cockpitImageGeomNow is the layout of the zoom on the screen as it is now.
func (m *OS) cockpitImageGeomNow() (quickLookZoomGeom, bool) {
	res := m.viewReserve()
	return m.cockpitImageGeom(m.GetRenderWidth(), m.GetRenderHeight()-res.Top-res.Bottom)
}

// cockpitImageZoom runs a zoom key, or a line or column key that moves the
// zoomed picture (see quickLookZoomGeom.act). It reports false where the
// key does nothing, so a column key steps through the images.
func (m *OS) cockpitImageZoom(act string) bool {
	v := m.Inbox.ck.img
	g, ok := m.cockpitImageGeomNow()
	if !ok {
		return false
	}
	ok, fit := g.act(&v.zoom, act)
	if fit {
		v.zoom = picZoom{}
	}
	return ok
}

// cockpitImageWheel zooms one notch around the pointer while it is over the
// picture. The view shows one picture and has nothing to scroll, so the
// wheel zooms there as it does over quick look's picture.
func (m *OS) cockpitImageWheel(x, y int, up bool) {
	v := m.Inbox.ck.img
	g, ok := m.cockpitImageGeomNow()
	if !ok || !v.box.Contains(x, y) {
		return
	}
	ax, ay := (float64(x-v.placeX)+0.5)*float64(g.cw), (float64(y-v.plcY)+0.5)*float64(g.ch)
	if !g.wheel(&v.zoom, ax, ay, up) {
		v.zoom = picZoom{}
	}
	m.cockpitTouch()
}

// CockpitImageDrag moves the zoomed picture with the pointer while a press
// on it is held. It reports whether one is.
func (m *OS) CockpitImageDrag(x, y int) bool {
	if !m.CockpitImageOpen() || !m.Inbox.ck.img.zoom.drag {
		return false
	}
	v := m.Inbox.ck.img
	g, ok := m.cockpitImageGeomNow()
	if !ok || v.zoom.scale <= 0 {
		v.zoom.drag = false
		return false
	}
	g.dragTo(&v.zoom, x, y)
	m.cockpitTouch()
	return true
}

// CockpitImageDragging reports whether a press on the zoomed picture is held.
func (m *OS) CockpitImageDragging() bool {
	return m.CockpitImageOpen() && m.Inbox.ck.img.zoom.drag
}

// CockpitImageRelease ends a drag of the picture. It reports whether one was
// going on.
func (m *OS) CockpitImageRelease() bool {
	if !m.CockpitImageDragging() {
		return false
	}
	m.Inbox.ck.img.zoom.drag = false
	return true
}

// cockpitSaveImage writes the image to the screenshot folder, once, and
// returns its path, "" when it could not be written.
func (m *OS) cockpitSaveImage() string {
	v := m.Inbox.ck.img
	if path, ok := v.saved[v.idx]; ok {
		return path
	}
	cfg := config.ScreenshotConfig{}
	if m.UserConfig != nil {
		cfg = m.UserConfig.Screenshot
	}
	dir := cfg.ResolveDirectory()
	img := v.imgs[v.idx]
	name := img.Name
	if name == "" {
		name = "tuios-image-" + time.Now().Format("20060102-150405") + "." + img.Format
	}
	path, err := ckWriteNew(dir, filepath.Base(name), img.Data)
	if err != nil {
		m.ShowNotification("tuios cannot save the image: "+err.Error(), "error", m.Settings.NotificationDuration)
		return ""
	}
	v.saved[v.idx] = path
	return path
}

// ckWriteNew writes data to a new file in dir named name, or name with a
// number in front when one is there already. It never replaces a file and
// never follows a link: the file is created with O_EXCL.
func ckWriteNew(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for i := 0; ; i++ {
		path := filepath.Join(dir, name)
		if i > 0 {
			path = filepath.Join(dir, strconv.Itoa(i)+"-"+name)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) && i < 1000 {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return path, err
	}
}

// drawImageView draws the image view over the whole canvas.
func (m *OS) drawImageView(c *ckCanvas, l ckLook, top int) {
	v := m.Inbox.ck.img
	pal := l.pal
	img := v.imgs[v.idx]
	margin := ckImageMargin(c.w)
	x0, x1 := margin, c.w-margin
	y := 1
	head := []ckSeg{{t: l.g(ckGlyphImage) + " ", fg: pal.FgMute}, {t: ckImageName(img, v.idx), fg: pal.Fg, attrs: ckBold},
		{t: "   " + ckImageSize(img), fg: pal.FgDim}, {t: "   " + units.Bytes(int64(len(img.Data))), fg: pal.FgDim}}
	if v.from != "" {
		head = append(head, ckSeg{t: "   " + v.from, fg: pal.FgMute})
	} else if v.who != "" {
		head = append(head, ckSeg{t: "   pasted by you for " + v.who, fg: pal.FgMute})
	}
	rx := c.textR(x1, y, ckSeg{t: strconv.Itoa(v.idx+1) + " of " + strconv.Itoa(len(v.imgs)) + "   ", fg: pal.FgMute},
		ckSeg{t: "esc", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " close", fg: pal.FgMute})
	c.text(x0, y, rx-2, head...)

	ay, ah := 3, max(c.h-7, 3)
	c.fill(x0, ay, x1-x0, ah, l.panel)
	p := m.replyPreviewer()
	m.cockpitImageLayout(c.w, c.h, top)
	px, py, cols, rows := v.placeX, v.plcY-top, v.cols, v.rows
	switch {
	case img.Width <= 0 || img.Height <= 0:
		// A format tuios cannot decode, such as WebP: no picture comes.
		msg := "tuios cannot draw this image."
		if p.CanOpen() {
			msg += " o opens it in the viewer."
		}
		c.text(max(x0+2, (c.w-len(msg))/2), ay+ah/2, x1-2, ckSeg{t: msg, fg: pal.FgDim})
	case v.ph:
		id := p.placeholderID(v.key)
		for i := range rows {
			c.draw(px, py+i, cols, placeholderLineAt(id, v.phRow+i, v.phCol, cols))
		}
	case v.mode == composer.PreviewCells:
		k := p.cellsKey(img, cols, rows)
		p.mu.Lock()
		pic := p.pics[k]
		p.mu.Unlock()
		if pic != nil {
			for i, s := range pic.lines {
				c.draw(px, py+i, cols, s)
			}
		}
	case v.mode == composer.PreviewText:
		msg := "This terminal cannot draw the picture."
		if p.CanOpen() {
			msg += " o opens it in the viewer."
		}
		c.text(max(x0+2, (c.w-len(msg))/2), ay+ah/2, x1-2, ckSeg{t: msg, fg: pal.FgDim})
	}

	fy := c.h - 2
	var hints []ckSeg
	key := func(k, words string) {
		hints = append(hints, ckSeg{t: k, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " " + words + "   ", fg: pal.FgMute})
	}
	if p.CanOpen() {
		key("o", "open in viewer")
	}
	key("c", "copy path")
	key("s", "save")
	zoomed := v.zoom.scale > 0
	if len(v.imgs) > 1 && !zoomed {
		key(overlay.ArrowLeft()+" "+overlay.ArrowRight(), "other images")
	}
	if _, ok := m.cockpitImageGeom(c.w, c.h); ok {
		key("+ -", zoomLabel(v.zoom.scale))
	}
	if zoomed {
		key("0", "fit")
		key("h j k l", "move")
	}
	rx = c.textR(x1, fy, ckSeg{t: "Click outside the picture to close it.", fg: pal.FgMute})
	c.text(x0, fy, rx-2, hints...)
}

// cockpitImageLayout places the view's picture for a canvas of w by h cells
// whose first row is screen row top: the largest box that holds its shape,
// zoomed, centred in the area under the header.
//
// A zoomed picture takes the box its zoom gives, and shows the part in view:
// a source rect of the picture for a placement, or the cells of that part of
// the whole zoomed picture for placeholder cells.
func (m *OS) cockpitImageLayout(w, h, top int) {
	v := m.Inbox.ck.img
	margin := ckImageMargin(w)
	ay, ah := 3, max(h-7, 3)
	cw, ch := m.quickLookCell()
	img := v.imgs[v.idx]
	v.mode = m.replyPreviewer().Mode()
	v.ph = v.mode == composer.PreviewPixels && m.threadPlaceholders()
	maxCols, maxRows := w-2*margin-4, ah-2
	if v.ph {
		// Placeholder cells name at most placeholderMaxCells rows and
		// columns, on a very wide screen too.
		maxCols, maxRows = min(maxCols, placeholderMaxCells), min(maxRows, placeholderMaxCells)
	}
	cols, rows := ckFitBox(img, maxCols, maxRows, float64(ch)/float64(cw), 1)
	px, py := margin+(w-2*margin-cols)/2, ay+(ah-rows)/2
	v.fitCols, v.fitRows = cols, rows
	v.key, v.crop = pixelsKey(img, cols, rows), image.Rectangle{}
	v.phCols, v.phRows, v.phCol, v.phRow = cols, rows, 0, 0
	if g, ok := m.cockpitImageGeom(w, h); ok && v.zoom.scale > 0 {
		s := v.zoom.scale
		zv, cx, cy := g.view(s, v.zoom.ox, v.zoom.oy)
		px, py, cols, rows = margin+1+zv.x, ay+zv.y, zv.cols, zv.rows
		if v.zoom.heldOK && v.zoom.held.sum == v.key.sum {
			v.key = v.zoom.held
		}
		if v.ph {
			v.phCols, v.phRows = max(int(g.bw*s/float64(cw)+0.5), cols), max(int(g.bh*s/float64(ch)+0.5), rows)
			v.phCol = max(min(int(cx*s/float64(cw)+0.5), v.phCols-cols), 0)
			v.phRow = max(min(int(cy*s/float64(ch)+0.5), v.phRows-rows), 0)
		} else {
			v.crop = zv.crop
		}
	} else if v.zoom.scale > 0 {
		// The host lost its graphics, or the screen changed: the fit.
		v.zoom = picZoom{}
	}
	v.box = overlay.Rect{X0: px, Y0: top + py, X1: px + cols, Y1: top + py + rows}
	v.cols, v.rows = cols, rows
	v.placeX, v.plcY = px, top+py
}

// ckImageMargin is the image view's margin on each side of a canvas w wide.
func ckImageMargin(w int) int {
	if w >= 120 {
		return 8
	}
	return 2
}

// CockpitImagePicture lays the view out for the screen and prepares its
// picture when it is not ready. The input path calls it after every key and
// click while the agents view is up; it is nil when nothing is to be done.
func (m *OS) CockpitImagePicture() tea.Cmd {
	if !m.CockpitImageOpen() {
		return nil
	}
	res := m.viewReserve()
	m.cockpitImageLayout(m.GetRenderWidth(), m.GetRenderHeight()-res.Top-res.Bottom, res.Top)
	return tea.Batch(m.cockpitImagePicture(), m.cockpitImageSharp())
}

// cockpitImageSharp prepares the sharper copy the zoom wants, and places it
// once it is ready: a copy at a power of quickLookZoomTier of the fit's
// size, up to the image's own pixels (see quickLookZoomKey). Until then the
// last picture placed stays. It is nil at the fit.
func (m *OS) cockpitImageSharp() tea.Cmd {
	v := m.Inbox.ck.img
	if v == nil || v.zoom.scale <= 0 {
		return nil
	}
	g, ok := m.cockpitImageGeomNow()
	if !ok {
		return nil
	}
	img := v.imgs[v.idx]
	want := quickLookZoomKey(g, v.zoom.scale, img, v.fitCols, v.fitRows)
	if want == v.zoom.held && v.zoom.heldOK {
		return nil
	}
	p := m.replyPreviewer()
	p.mu.Lock()
	pic := p.pics[want]
	p.mu.Unlock()
	if pic != nil && len(pic.png) > 0 {
		v.zoom.held, v.zoom.heldOK = want, true
		m.cockpitTouch()
		return nil
	}
	return m.prepareKey(img, want, m.cockpitPalette().Panel)
}

// ckFitBox is the largest box of at most maxCols by maxRows cells that holds
// img's shape, scaled by zoom. aspect is a cell's height over its width.
func ckFitBox(img *composer.Image, maxCols, maxRows int, aspect, zoom float64) (int, int) {
	if img.Width <= 0 || img.Height <= 0 {
		return max(int(float64(maxCols)*zoom), 1), max(int(float64(maxRows)*zoom), 1)
	}
	ratio := float64(img.Width) / float64(img.Height) * aspect // cols per row
	rows := float64(maxRows)
	cols := rows * ratio
	if cols > float64(maxCols) {
		cols = float64(maxCols)
		rows = cols / ratio
	}
	return max(int(math.Round(cols*zoom)), 1), max(int(math.Round(rows*zoom)), 1)
}

// cockpitImagePicture prepares the view's picture when it is not ready.
func (m *OS) cockpitImagePicture() tea.Cmd {
	v := m.Inbox.ck.img
	if v == nil || v.cols <= 0 || v.mode == composer.PreviewText {
		return nil
	}
	p := m.replyPreviewer()
	img := v.imgs[v.idx]
	k := pixelsKey(img, v.fitCols, v.fitRows)
	if v.mode == composer.PreviewCells {
		k = p.cellsKey(img, v.cols, v.rows)
	}
	return m.prepareKey(img, k, m.cockpitPalette().Panel)
}

// cockpitImagePlacement is the view's picture as a kitty placement, when the
// view is up in pixel mode: the part in view, or for placeholder cells the
// virtual placement of the whole zoomed picture.
func (m *OS) cockpitImagePlacement() (previewPlacement, bool) {
	v := m.Inbox.ck.img
	if !m.CockpitImageOpen() || v.mode != composer.PreviewPixels || v.cols <= 0 {
		return previewPlacement{}, false
	}
	if v.ph {
		return previewPlacement{key: v.key, cols: v.phCols, rows: v.phRows, virtual: true, box: image.Pt(v.phCols, v.phRows)}, true
	}
	return previewPlacement{key: v.key, x: v.placeX, y: v.plcY, cols: v.cols, rows: v.rows, crop: v.crop}, true
}
