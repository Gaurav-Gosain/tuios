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

// The paste preview: a small card at the pane's cursor that shows the image an
// image paste just turned into a path.
//
// An image paste types a path, which says nothing about what was pasted. The
// card shows the picture itself for a few seconds, through the reply previews'
// pipeline: kitty graphics where the host has them, block glyphs where it does
// not, and only the line of words at 16 colours. It takes no key and no click:
// the next key or press takes it down and still goes where it was going, and
// so does the timer, a closed pane, or a pane that is no longer on screen.

// The picture's largest box, in cells. The card is a glance at what was
// pasted, not a viewer.
const (
	pastePreviewMaxCols = 44
	pastePreviewMaxRows = 12
)

// pastePreviewState is the card on screen, if any.
type pastePreviewState struct {
	img    *composer.Image
	window string
	path   string
	gen    uint64
	// pic is where the last frame put the picture, for the host's graphics.
	pic quickLookPic
}

// pastePreviewExpireMsg ends the card it was armed for.
type pastePreviewExpireMsg struct{ gen uint64 }

// pastePreviewOn reports whether a card is up.
func (m *OS) pastePreviewOn() bool { return m.pastePreview.img != nil }

// showPastePreview puts the card up for an image just pasted into window, and
// arms the timer that takes it down. Bytes that are no image this build reads
// show no card.
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

// hidePastePreview takes the card down. The next frame takes the host's
// picture down with it.
func (m *OS) hidePastePreview() {
	if !m.pastePreviewOn() {
		return
	}
	m.pastePreview = pastePreviewState{gen: m.pastePreview.gen}
	m.renderSkipped = false
}

// applyPastePreviewExpire ends the card the timer was armed for, and not a
// later one.
func (m *OS) applyPastePreviewExpire(msg pastePreviewExpireMsg) {
	if msg.gen == m.pastePreview.gen {
		m.hidePastePreview()
	}
}

// pastePreviewRoom is where the card goes: the part of the screen it must
// stay in and the pane's cursor, which it is anchored at. onScreen is false
// when the pane closed, is minimized or is on another workspace.
//
// A pane too small to hold the card gives it the whole screen. Both the
// picture's preparation and the draw read the room here, so the size the
// picture is prepared at is the size it is drawn at.
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

// pastePreviewBox is the picture's size in cells inside room.
func (m *OS) pastePreviewBox(room image.Rectangle) (cols, rows int) {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	img := m.pastePreview.img
	// The border takes two columns and two rows, the line under the picture
	// one more.
	availW := max(min(pastePreviewMaxCols, room.Dx()-2), 1)
	availH := max(min(pastePreviewMaxRows, room.Dy()-3), 1)
	cols, rows = ckFitBox(img, availW, availH, float64(ch)/float64(cw), 1)
	// Pixels are drawn at their own size at most, as quick look does: a small
	// picture stretched is a blur.
	if m.replyPreviewer().Mode() == composer.PreviewPixels && cols*cw > img.Width {
		f := float64(img.Width) / float64(cols*cw)
		cols, rows = max(int(float64(cols)*f+0.5), 1), max(int(float64(rows)*f+0.5), 1)
	}
	return cols, rows
}

// pastePreviewPicture prepares the picture at the size the card shows it,
// when it is not ready. Update asks after every message, as it does for quick
// look, because a resize or a moved pane changes the box.
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

// pastePreviewPlacement is the picture as a kitty placement, when the host
// draws it and nothing is open over the card.
func (m *OS) pastePreviewPlacement() (previewPlacement, bool) {
	pic := m.pastePreview.pic
	if !m.pastePreviewOn() || !pic.ok || pic.key.mode != composer.PreviewPixels ||
		m.AnyOverlayOpen() || m.ContextMenu != nil {
		return previewPlacement{}, false
	}
	return previewPlacement{key: pic.key, x: pic.x, y: pic.y, cols: pic.cols, rows: pic.rows}, true
}

// placePastePreview draws the card as a layer at the pane's cursor: above the
// line being typed when it fits there, below it when it does not.
func (m *OS) placePastePreview(layers []*lipgloss.Layer) []*lipgloss.Layer {
	pp := &m.pastePreview
	pp.pic = quickLookPic{}
	if !m.pastePreviewOn() {
		return layers
	}
	// A pane that closed or left the screen takes its card with it, and so
	// does a panel or menu opened over it: the person has moved on, and a
	// card under a panel would show no picture, which the host only draws
	// with nothing over it.
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

	// Above the cursor, as the path picker opens, and below it when it does
	// not fit there. The box starts a column left of the cursor, so the
	// picture inside its border starts at the cursor's column.
	x, y, _ := overlay.Place(overlay.PointRect(ax-1, ay), geo.Width, geo.Height, overlayRect(room), overlay.Placement{Side: overlay.SideAbove})

	if pic.ok {
		pic.x += x + 1
		pic.y += y + 1
		pp.pic = pic
	}
	return append(layers, lipgloss.NewLayer(content).
		X(x).Y(y).Z(config.ZIndexPathPicker).ID("paste-preview"))
}

// shortenMiddle cuts s to width cells with the chrome's ellipsis in the
// middle, so the start (the size) and the end (the unique part of the file
// name) both stay.
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
