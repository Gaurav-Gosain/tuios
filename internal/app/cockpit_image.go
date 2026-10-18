package app

import (
	"errors"
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
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The image view: one picture of the draft at the largest size the screen
// holds, over the whole agents view (DESIGN.md 3.10, frame f). It names the
// image, its size in pixels and bytes and where it came from. o opens it in
// the desktop's viewer, c saves a copy and copies its path, s saves a copy,
// left and right step through the draft's images, + and - zoom, and esc or a
// click outside the picture closes it.
//
// The picture goes through the reply previews' pipeline: kitty graphics
// placed after the frame where the host has them, block glyphs from
// shot/mosaic where it does not, and a line of words at 16 colours. It is
// prepared once per image and size, off the update loop.

// ckZoomSteps are the zoom levels, as shares of the largest fit.
var ckZoomSteps = [...]float64{1, 0.75, 0.5, 0.35}

// ckImageView is the open image view.
type ckImageView struct {
	imgs []*composer.Image
	idx  int
	zoom int
	who  string
	// saved is where each image was saved, by index, so c and s write
	// one copy.
	saved map[int]string
	// box is the picture's cell box in the last frame, on the screen.
	box          overlay.Rect
	cols, rows   int
	mode         composer.PreviewMode
	placeX, plcY int
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
		v.idx = (v.idx - 1 + len(v.imgs)) % len(v.imgs)
	case "right", "l":
		v.idx = (v.idx + 1) % len(v.imgs)
	case "+", "=":
		v.zoom = max(v.zoom-1, 0)
	case "-", "_":
		v.zoom = min(v.zoom+1, len(ckZoomSteps)-1)
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
// picture closes it.
func (m *OS) cockpitImageClick(x, y int) {
	v := m.Inbox.ck.img
	if !v.box.Contains(x, y) {
		m.cockpitCloseImage()
	}
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
	margin := 2
	if c.w >= 120 {
		margin = 8
	}
	x0, x1 := margin, c.w-margin
	y := 1
	head := []ckSeg{{t: l.g(ckGlyphImage) + " ", fg: pal.FgMute}, {t: ckImageName(img, v.idx), fg: pal.Fg, attrs: ckBold},
		{t: "   " + ckImageSize(img), fg: pal.FgDim}, {t: "   " + ckBytes(len(img.Data)), fg: pal.FgDim}}
	if v.who != "" {
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
	if len(v.imgs) > 1 {
		key(overlay.ArrowLeft()+" "+overlay.ArrowRight(), "other images")
	}
	key("+ -", "zoom")
	rx = c.textR(x1, fy, ckSeg{t: "Click outside the picture to close it.", fg: pal.FgMute})
	c.text(x0, fy, rx-2, hints...)
}

// cockpitImageLayout places the view's picture for a canvas of w by h cells
// whose first row is screen row top: the largest box that holds its shape,
// zoomed, centred in the area under the header.
func (m *OS) cockpitImageLayout(w, h, top int) {
	v := m.Inbox.ck.img
	margin := 2
	if w >= 120 {
		margin = 8
	}
	ay, ah := 3, max(h-7, 3)
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	img := v.imgs[v.idx]
	cols, rows := ckFitBox(img, w-2*margin-4, ah-2, float64(ch)/float64(cw), ckZoomSteps[v.zoom])
	px, py := margin+(w-2*margin-cols)/2, ay+(ah-rows)/2
	v.box = overlay.Rect{X0: px, Y0: top + py, X1: px + cols, Y1: top + py + rows}
	v.cols, v.rows, v.mode = cols, rows, m.replyPreviewer().Mode()
	v.placeX, v.plcY = px, top+py
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
	return m.cockpitImagePicture()
}

// ckBytes is a byte count in words: "79 KB".
func ckBytes(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + " KB"
	}
	return strconv.Itoa(n) + " B"
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
	k := pixelsKey(img, v.cols, v.rows)
	if v.mode == composer.PreviewCells {
		k = p.cellsKey(img, v.cols, v.rows)
	}
	p.mu.Lock()
	if p.asked[k] {
		p.mu.Unlock()
		return nil
	}
	p.asked[k] = true
	p.mu.Unlock()
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	ground := theme.UI().Panel
	return func() tea.Msg {
		return replyPreviewMsg{pics: map[previewKey]*previewPicture{k: preparePicture(img, k, cw, ch, ground)}}
	}
}

// cockpitImagePlacement is the view's picture as a kitty placement, when the
// view is up in pixel mode.
func (m *OS) cockpitImagePlacement() (previewPlacement, bool) {
	v := m.Inbox.ck.img
	if !m.CockpitImageOpen() || v.mode != composer.PreviewPixels || v.cols <= 0 {
		return previewPlacement{}, false
	}
	return previewPlacement{key: pixelsKey(v.imgs[v.idx], v.cols, v.rows), x: v.placeX, y: v.plcY, cols: v.cols, rows: v.rows}, true
}
