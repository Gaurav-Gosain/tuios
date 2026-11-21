package app

import (
	"fmt"
	"image"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	pastePreviewMaxCols = 44
	pastePreviewMaxRows = 12
)

type pastePreviewState struct {
	img    *composer.Image
	window string
	path   string
	gen    uint64
	pic    quickLookPic
}

type pastePreviewExpireMsg struct{ gen uint64 }

func (m *OS) pastePreviewOn() bool { return m.pastePreview.img != nil }

func (m *OS) showPastePreview(window, path string, data []byte) tea.Cmd {
	if !m.Settings.PastePreview || len(data) == 0 {
		return nil
	}
	img, err := composer.NewImage(data)
	if err != nil || img == nil {
		return nil
	}
	gen := m.pastePreview.gen + 1
	m.pastePreview = pastePreviewState{img: img, window: window, path: path, gen: gen}
	m.renderSkipped = false
	return tea.Batch(m.pastePreviewPicture(), tea.Tick(m.Settings.PastePreviewDuration(), func(time.Time) tea.Msg {
		return pastePreviewExpireMsg{gen: gen}
	}))
}

func (m *OS) hidePastePreview() {
	if !m.pastePreviewOn() {
		return
	}
	m.pastePreview = pastePreviewState{gen: m.pastePreview.gen}
	m.renderSkipped = false
}

func (m *OS) applyPastePreviewExpire(msg pastePreviewExpireMsg) {
	if msg.gen == m.pastePreview.gen {
		m.hidePastePreview()
	}
}

func (m *OS) pastePreviewRoom() (room image.Rectangle, anchorX, anchorY int, onScreen bool) {
	w := m.windowByID(m.pastePreview.window)
	if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized {
		return image.Rectangle{}, 0, 0, false
	}
	room, _ = m.pathPickerRoom(w)
	if room.Dx() < overlay.MinDialogWidth+2 || room.Dy() < 4 {
		room = image.Rect(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
	}
	anchorX, anchorY = m.pathPickerAnchor(w)
	return room, anchorX, anchorY, true
}

func (m *OS) pastePreviewBox(room image.Rectangle) (cols, rows int) {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	img := m.pastePreview.img
	availW := max(min(pastePreviewMaxCols, room.Dx()-2), 1)
	availH := max(min(pastePreviewMaxRows, room.Dy()-3), 1)
	cols, rows = ckFitBox(img, availW, availH, float64(ch)/float64(cw), 1)
	if m.replyPreviewer().Mode() == composer.PreviewPixels && cols*cw > img.Width {
		f := float64(img.Width) / float64(cols*cw)
		cols, rows = max(int(float64(cols)*f+0.5), 1), max(int(float64(rows)*f+0.5), 1)
	}
	return cols, rows
}

func (m *OS) pastePreviewPicture() tea.Cmd {
	if !m.pastePreviewOn() || m.previewResizing() {
		return nil
	}
	if m.replyPreviewer().Mode() == composer.PreviewText {
		return nil
	}
	room, _, _, onScreen := m.pastePreviewRoom()
	if !onScreen {
		return nil
	}
	cols, rows := m.pastePreviewBox(room)
	return m.quickLookPrepare(m.pastePreview.img, cols, rows)
}

func (m *OS) pastePreviewPlacement() (previewPlacement, bool) {
	pic := m.pastePreview.pic
	if !m.pastePreviewOn() || !pic.ok || pic.key.mode != composer.PreviewPixels ||
		m.AnyOverlayOpen() || m.ContextMenu != nil {
		return previewPlacement{}, false
	}
	return previewPlacement{key: pic.key, x: pic.x, y: pic.y, cols: pic.cols, rows: pic.rows}, true
}

func (m *OS) placePastePreview(layers []*lipgloss.Layer) []*lipgloss.Layer {
	pp := &m.pastePreview
	pp.pic = quickLookPic{}
	if !m.pastePreviewOn() {
		return layers
	}
	room, ax, ay, onScreen := m.pastePreviewRoom()
	if !onScreen || m.AnyOverlayOpen() || m.ContextMenu != nil {
		m.hidePastePreview()
		return layers
	}
	if room.Dx() <= 0 || room.Dy() <= 0 {
		return layers
	}

	pal := m.overlayUI()
	p := m.replyPreviewer()
	mode := p.Mode()
	cols, rows := m.pastePreviewBox(room)

	meta := fmt.Sprintf("%d×%d %s · %s", pp.img.Width, pp.img.Height, pp.img.Format, filepath.Base(pp.path))
	width := cols
	if mode == composer.PreviewText {
		rows = 0
		width = ansi.StringWidth(meta)
	}
	width = max(min(max(width, 20), room.Dx()-2), overlay.MinDialogWidth)
	picX := max((width-cols)/2, 0)

	bodyH := rows + 1
	c := (*ckCanvas)(nil).reset(width, bodyH, ckColor(pal.Canvas))
	pic := quickLookPic{}
	switch mode {
	case composer.PreviewCells:
		k := quickLookCellsKey(p, pp.img, cols, rows)
		p.mu.Lock()
		cells := p.pics[k]
		p.mu.Unlock()
		if cells != nil {
			for i, s := range cells.lines {
				c.draw(picX, i, cols, s)
			}
		}
	case composer.PreviewPixels:
		pic = quickLookPic{ok: true, key: pixelsKey(pp.img, cols, rows), x: picX, y: 0, cols: cols, rows: rows}
	}
	c.text(0, rows, width, ckSeg{t: shortenMiddle(meta, width), fg: pal.FgMute})

	content, geo := overlay.Dialog{Title: "pasted image", Width: width, Body: c.render()}.Render(pal)

	x, y, _ := overlay.Place(overlay.PointRect(ax-1, ay), geo.Width, geo.Height, overlayRect(room), overlay.Placement{Side: overlay.SideAbove})

	if pic.ok {
		pic.x += x + 1
		pic.y += y + 1
		pp.pic = pic
	}
	return append(layers, lipgloss.NewLayer(content).
		X(x).Y(y).Z(config.ZIndexPathPicker).ID("paste-preview"))
}

func shortenMiddle(s string, width int) string {
	full := ansi.StringWidth(s)
	ellipsis := overlay.Ellipsis()
	keep := width - ansi.StringWidth(ellipsis)
	if full <= width || keep < 4 {
		return s
	}
	head := keep / 2
	return ansi.Truncate(s, head, "") + ellipsis + ansi.TruncateLeft(s, full-(keep-head), "")
}
