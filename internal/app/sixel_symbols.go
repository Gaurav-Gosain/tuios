package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/mosaic"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	uv "github.com/charmbracelet/ultraviolet"
)

// Images on a host that draws no graphics.
//
// The Linux console under kmscon, and any terminal without sixel and kitty
// graphics, can show a pane's image only as text. In that case the sixel
// passthrough runs in sixelSymbols mode: each image is drawn once as block
// glyphs (internal/mosaic), one glyph with two colours per image cell, and the
// frame scan puts those glyphs where the image's marker cells are. Everything
// the marker model gives a picture holds: the glyphs move with the pane,
// scroll into the scrollback, are cut by popups and other panes, and go when
// the cells are cleared. They are ordinary cells, so a modal's dim fades them
// like text, and nothing is written after the frame.
//
// A pane is told it can draw sixel in this mode, since what it draws is
// shown. Programs that pick between sixel and their own text output, such as
// chafa, timg and yazi, then send sixel, and a client that attaches later with
// real graphics shows the same image as a picture.

// symbolCellBytes is what one drawn cell costs, roughly, for the per-pane
// image budget: the struct and its two boxed colours.
const symbolCellBytes = 64

// symbolCells draws e's image with the glyphs of kind k in the colours the
// host shows. At sixteen colours a picture that keeps too little of its
// shape (mosaic.MinFidelity) comes back as an empty, non-nil slice, which
// the frame scan draws as the image box.
func symbolCells(e *sixelEntry, k mosaic.Kind, colors mosaic.Colors) []mosaic.Cell {
	if e == nil || e.img == nil || k == mosaic.Off {
		return nil
	}
	img := mosaic.Indexed{Width: e.img.Width, Height: e.img.Height, Pix: e.img.Pix, Palette: e.img.Palette}
	cells := mosaic.Encode(img, e.cellW, e.cellH, e.rows, e.cols, k, colors)
	if colors == mosaic.ANSI16 && mosaic.Fidelity(img, e.cellW, e.cellH, e.rows, e.cols, cells) < mosaic.MinFidelity {
		return []mosaic.Cell{}
	}
	return cells
}

// symbolColors is the colour set the frame is drawn for.
func symbolColors() mosaic.Colors {
	switch overlay.CurrentDepth() {
	case overlay.Depth256:
		return mosaic.XTerm256
	case overlay.Depth16:
		return mosaic.ANSI16
	}
	return mosaic.TrueColor
}

// symbolCellsOf returns the glyphs of image id for the current glyph set,
// drawing them first when the image was registered before this mode or with
// another set. The drawing happens outside the lock.
func (sp *SixelPassthrough) symbolCellsOf(id uint32) []mosaic.Cell {
	sp.mu.Lock()
	e := sp.images[id]
	k := sp.symbols
	if e == nil || e.img == nil {
		sp.mu.Unlock()
		return nil
	}
	colors := symbolColors()
	if e.cells != nil && e.symbolKind == k && e.symbolColors == colors {
		cells := e.cells
		sp.mu.Unlock()
		return cells
	}
	sp.mu.Unlock()
	cells := symbolCells(e, k, colors)
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.images[id] == e && sp.symbols == k {
		sp.byWindow[e.windowID] += (len(cells) - len(e.cells)) * symbolCellBytes
		e.bytes += (len(cells) - len(e.cells)) * symbolCellBytes
		e.cells, e.symbolKind, e.symbolColors = cells, k, colors
	}
	return cells
}

// symbolCell draws one image cell as its glyph. A transparent cell, or the
// transparent part of one, keeps the background the pane put there.
func symbolCell(c *uv.Cell, mc *mosaic.Cell) {
	bg := c.Style.Bg
	*c = uv.Cell{Content: " ", Width: 1}
	c.Style.Bg = bg
	if mc.Clear {
		return
	}
	c.Content = mc.Glyph
	c.Style.Fg = mc.Fg
	if mc.Bg != nil {
		c.Style.Bg = mc.Bg
	}
}

// imageSymbolKind resolves appearance.image_symbols to a glyph set for a
// host whose TERM is term. A glyph set's name is taken as it is. auto picks
// by the terminal, since a terminal cannot be asked which glyphs its font
// has:
//
//   - kmscon: octants. Its built-in Unifont has them (kmscon 10).
//   - linux, the kernel console: half blocks. Console fonts hold 256 or 512
//     glyphs, the CP437 block set and nothing finer.
//   - anything else: quadrants. They are in the Basic Multilingual Plane,
//     and every font with block elements has them.
//
// At sixteen colours the cells are half blocks whatever this says; see
// mosaic.Encode.
func imageSymbolKind(setting, term string) mosaic.Kind {
	if k, ok := mosaic.ParseKind(setting); ok {
		return k
	}
	switch {
	case term == "kmscon" || strings.HasPrefix(term, "kmscon-"):
		return mosaic.Octant
	case term == "linux" || strings.HasPrefix(term, "linux-"):
		return mosaic.Half
	}
	return mosaic.Quadrant
}

// drawsSymbols says a client draws images as glyphs: its setting is not off
// and its host has no image protocol. A host that has one gets the picture
// in it, never glyphs, and its panes are told sixel only for that reason.
func drawsSymbols(setting string, caps *HostCapabilities) bool {
	if caps == nil || caps.SixelGraphics || caps.KittyGraphics {
		return false
	}
	return imageSymbolKind(setting, caps.Term) != mosaic.Off
}

// refreshImageSymbols applies a change of appearance.image_symbols to the
// passthrough and to what the daemon tells the panes.
func (m *OS) refreshImageSymbols() {
	sp := m.SixelPassthrough
	if sp == nil {
		return
	}
	caps := m.hostCaps()
	k := imageSymbolKind(m.Settings.ImageSymbols, caps.Term)
	sp.mu.Lock()
	same := sp.symbols == k
	sp.mu.Unlock()
	if same {
		return
	}
	sp.SetSymbols(k)
	if client := m.DaemonClient; client != nil {
		sixel, kitty, symbols := caps.SixelGraphics, caps.KittyGraphics, drawsSymbols(m.Settings.ImageSymbols, caps)
		go func() { _ = client.ReportGraphics(sixel, kitty, symbols) }()
	}
}
