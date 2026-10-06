package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// symbolCells draws e's image with the glyphs of kind k, in the xterm
// palette when the host shows 256 colours.
func symbolCells(e *sixelEntry, k mosaic.Kind, xterm256 bool) []mosaic.Cell {
	if e == nil || e.img == nil || k == mosaic.Off {
		return nil
	}
	img := mosaic.Indexed{Width: e.img.Width, Height: e.img.Height, Pix: e.img.Pix, Palette: e.img.Palette}
	return mosaic.Encode(img, e.cellW, e.cellH, e.rows, e.cols, k, xterm256)
}

// hostIs256 says the frame is drawn for a host with the xterm 256 colours.
func hostIs256() bool { return overlay.CurrentDepth() == overlay.Depth256 }

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
	p256 := hostIs256()
	if e.cells != nil && e.symbolKind == k && e.symbol256 == p256 {
		cells := e.cells
		sp.mu.Unlock()
		return cells
	}
	sp.mu.Unlock()
	cells := symbolCells(e, k, p256)
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.images[id] == e && sp.symbols == k {
		sp.byWindow[e.windowID] += (len(cells) - len(e.cells)) * symbolCellBytes
		e.bytes += (len(cells) - len(e.cells)) * symbolCellBytes
		e.cells, e.symbolKind, e.symbol256 = cells, k, p256
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

// imageSymbolKind resolves appearance.image_symbols to a glyph set. auto is
// quadrants: they are in the Basic Multilingual Plane, so every font with
// block elements has them, kmscon's built-in Unifont included. Sextants and
// octants draw finer shapes but need a font that has them, and a terminal
// cannot be asked whether its font does.
func imageSymbolKind(setting string) mosaic.Kind {
	if k, ok := mosaic.ParseKind(setting); ok {
		return k
	}
	return mosaic.Quadrant
}

// refreshImageSymbols applies a change of appearance.image_symbols to the
// passthrough and to what the daemon tells the panes.
func (m *OS) refreshImageSymbols() {
	sp := m.SixelPassthrough
	if sp == nil {
		return
	}
	k := imageSymbolKind(m.Settings.ImageSymbols)
	sp.mu.Lock()
	same := sp.symbols == k
	sp.mu.Unlock()
	if same {
		return
	}
	sp.SetSymbols(k)
	if client := m.DaemonClient; client != nil {
		caps := m.hostCaps()
		sixel, kitty, symbols := caps.SixelGraphics, caps.KittyGraphics, k != mosaic.Off
		go func() { _ = client.ReportGraphics(sixel, kitty, symbols) }()
	}
}

// symbolsFromGlobal is the glyph set for a client whose settings are the
// process's own, read before any OS exists: the daemon's hello is built from
// it.
func symbolsFromGlobal() mosaic.Kind {
	return imageSymbolKind(config.Global.ImageSymbols)
}
