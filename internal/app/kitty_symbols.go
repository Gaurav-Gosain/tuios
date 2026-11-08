package app

import (
	"image"
	"image/color"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

// Kitty images as block glyphs, on a host that draws no graphics.
//
// A sixel picture is drawn there as glyphs (sixel_symbols.go): its cells are
// marked in the pane's grid, so it scrolls, clips and clears with the text,
// and each frame draws the marked cells as glyphs. A kitty picture had no such
// path: with the kitty passthrough off its commands were dropped, and chafa
// -f kitty showed nothing (issue #586). Here a kitty upload is put together
// from its chunks, decoded, made into the palette image the glyph path draws,
// registered with the sixel passthrough, and its cells marked the same way.
// A delete (a=d) of all images or of one id blanks the cells it drew.
//
// Not covered: Unicode placeholders (U=1), and a host with sixel but no
// kitty graphics, whose kitty images stay off.

// Bounds on what one pane may hold for kitty glyphs. A guest picks every
// number here, so each is bounded.
const (
	// kittySymbolsMaxStored is how many uploaded (a=t) images a pane keeps
	// for a later placement.
	kittySymbolsMaxStored = 16
	// kittySymbolsMaxStoredBytes bounds the pixels those images hold.
	kittySymbolsMaxStoredBytes = 64 << 20
	// kittySymbolsMaxDrawn is how many drawn pictures a pane remembers, for
	// a delete by id.
	kittySymbolsMaxDrawn = 256
)

// kittySymbols holds every pane's kitty glyph state.
type kittySymbols struct {
	mu   sync.Mutex
	wins map[string]*kittySymbolWindow
}

// kittySymbolWindow is one pane's: the upload in progress, the images kept
// for a placement, and the marker ids each image's pictures were drawn with.
type kittySymbolWindow struct {
	pending *kittySymbolUpload
	stored  map[uint32]*vt.SixelImage
	order   []uint32
	bytes   int                 // what stored holds, two bytes a pixel
	drawn   map[uint32][]uint32 // guest image id -> marker ids
	nDrawn  int
}

// kittySymbolUpload is an upload being put together from its chunks: the
// first chunk's keys, and the data of every chunk.
type kittySymbolUpload struct {
	cmd  vt.KittyCommand
	data []byte
}

// kittySymbolPlace is what a command asks the pane to draw, once the lock is
// released: an image, the cells it covers, and whether the cursor moves.
type kittySymbolPlace struct {
	imageID    uint32
	img        *vt.SixelImage
	rows, cols int
	move       bool
}

func (ks *kittySymbols) window(id string) *kittySymbolWindow {
	if ks.wins == nil {
		ks.wins = map[string]*kittySymbolWindow{}
	}
	w := ks.wins[id]
	if w == nil {
		w = &kittySymbolWindow{stored: map[uint32]*vt.SixelImage{}, drawn: map[uint32][]uint32{}}
		ks.wins[id] = w
	}
	return w
}

// forget drops a closed pane's state.
func (ks *kittySymbols) forget(windowID string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	delete(ks.wins, windowID)
}

// take reads one command. It returns the picture to draw, or the marker ids
// a delete takes down. cells turns a command into the cells it covers.
func (ks *kittySymbols) take(windowID string, cmd *vt.KittyCommand, cells func(*vt.KittyCommand) (rows, cols int)) (place *kittySymbolPlace, erase []uint32) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	w := ks.window(windowID)
	switch cmd.Action {
	case vt.KittyActionTransmit, vt.KittyActionTransmitPlace:
		if cmd.Medium != vt.KittyMediumDirect && cmd.Medium != 0 {
			return nil, nil // a file names a file of the guest's machine
		}
		if w.pending == nil {
			w.pending = &kittySymbolUpload{cmd: *cmd}
			w.pending.cmd.Data, w.pending.cmd.RawPayload = nil, ""
		}
		if len(w.pending.data)+len(cmd.Data) > maxPassthroughTransmitBytes {
			w.pending = nil
			return nil, nil
		}
		w.pending.data = append(w.pending.data, cmd.Data...)
		if cmd.More {
			return nil, nil
		}
		up := w.pending
		w.pending = nil
		img := kittySymbolImage(&up.cmd, up.data)
		if img == nil {
			return nil, nil
		}
		if up.cmd.Action == vt.KittyActionTransmit {
			w.store(up.cmd.ImageID, img)
			return nil, nil
		}
		box := up.cmd
		box.Width, box.Height = img.Width, img.Height
		box.SourceX, box.SourceY, box.SourceWidth, box.SourceHeight = 0, 0, 0, 0
		rows, cols := cells(&box)
		return &kittySymbolPlace{imageID: up.cmd.ImageID, img: img, rows: rows, cols: cols, move: up.cmd.CursorMove == 0}, nil
	case vt.KittyActionPlace:
		img := w.stored[cmd.ImageID]
		if img == nil {
			return nil, nil
		}
		src := cropKittySymbol(img, cmd)
		box := *cmd
		box.Width, box.Height = src.Width, src.Height
		box.SourceX, box.SourceY, box.SourceWidth, box.SourceHeight = 0, 0, 0, 0
		rows, cols := cells(&box)
		return &kittySymbolPlace{imageID: cmd.ImageID, img: src, rows: rows, cols: cols, move: cmd.CursorMove == 0}, nil
	case vt.KittyActionDelete:
		switch cmd.Delete {
		case 0, vt.KittyDeleteAll, vt.KittyDeleteAllFree:
			for id, ids := range w.drawn {
				erase = append(erase, ids...)
				delete(w.drawn, id)
			}
			w.nDrawn = 0
			if cmd.Delete == vt.KittyDeleteAllFree {
				w.stored, w.order, w.bytes = map[uint32]*vt.SixelImage{}, nil, 0
			}
			return nil, erase
		case vt.KittyDeleteByID, vt.KittyDeleteByIDFree:
			erase = w.drawn[cmd.ImageID]
			w.nDrawn -= len(erase)
			delete(w.drawn, cmd.ImageID)
			if cmd.Delete == vt.KittyDeleteByIDFree {
				w.unstore(cmd.ImageID)
			}
			return nil, erase
		}
	}
	return nil, nil
}

// drew notes that image imageID's picture was drawn under marker id.
func (ks *kittySymbols) drew(windowID string, imageID, id uint32) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	w := ks.window(windowID)
	if w.nDrawn >= kittySymbolsMaxDrawn {
		// The oldest are forgotten: their cells have long scrolled off in
		// practice, and a delete by id of them then leaves them.
		for k := range w.drawn {
			w.nDrawn -= len(w.drawn[k])
			delete(w.drawn, k)
			if w.nDrawn < kittySymbolsMaxDrawn/2 {
				break
			}
		}
	}
	w.drawn[imageID] = append(w.drawn[imageID], id)
	w.nDrawn++
}

func (w *kittySymbolWindow) store(id uint32, img *vt.SixelImage) {
	w.unstore(id)
	w.order = append(w.order, id)
	w.stored[id] = img
	w.bytes += len(img.Pix) * 2
	for len(w.order) > kittySymbolsMaxStored || w.bytes > kittySymbolsMaxStoredBytes && len(w.order) > 1 {
		w.unstore(w.order[0])
	}
}

func (w *kittySymbolWindow) unstore(id uint32) {
	if img := w.stored[id]; img != nil {
		w.bytes -= len(img.Pix) * 2
	}
	delete(w.stored, id)
	for i, o := range w.order {
		if o == id {
			w.order = append(w.order[:i], w.order[i+1:]...)
			break
		}
	}
}

// kittySymbolImage decodes an upload into the palette image the glyph path
// draws, cropped to the command's source rectangle, or nil when it does not
// decode or is larger than a sixel may be.
func kittySymbolImage(cmd *vt.KittyCommand, data []byte) *vt.SixelImage {
	k := keptFromCommand(cmd, data)
	if k.format == vt.KittyFormatPNG && (k.width <= 0 || k.height <= 0) {
		if w, h, ok := pngSize(data, cmd.Compression == vt.KittyCompressionZlib); ok {
			k.width, k.height = w, h
		}
	}
	if k.width > vt.SixelMaxDimension || k.height > vt.SixelMaxDimension {
		return nil
	}
	src, err := k.decode(vt.SixelMaxPixels)
	if err != nil || src == nil {
		return nil
	}
	img := quantizeKittySymbol(src)
	if img == nil {
		return nil
	}
	return cropKittySymbol(img, cmd)
}

// quantizeKittySymbol turns pixels into a palette image of 5 bits a channel,
// which the glyph path draws (shot/mosaic). A pixel less than half opaque is
// transparent, as an unpainted sixel pixel is.
func quantizeKittySymbol(src image.Image) *vt.SixelImage {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > vt.SixelMaxDimension || h > vt.SixelMaxDimension || w*h > vt.SixelMaxPixels {
		return nil
	}
	img := &vt.SixelImage{Width: w, Height: h, Pix: make([]uint16, w*h), Palette: kittySymbolPalette()}
	for y := range h {
		for x := range w {
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if c.A < 128 {
				continue
			}
			img.Pix[y*w+x] = uint16(int(c.R>>3)<<10|int(c.G>>3)<<5|int(c.B>>3)) + 1
		}
	}
	return img
}

var (
	kittyPaletteOnce sync.Once
	kittyPalette     []color.RGBA
)

// kittySymbolPalette is the 32768 colours of 5 bits a channel, entry n the
// colour of index n+1. It is shared and never written.
func kittySymbolPalette() []color.RGBA {
	kittyPaletteOnce.Do(func() {
		kittyPalette = make([]color.RGBA, 1<<15)
		for i := range kittyPalette {
			r, g, b := (i>>10)&31, (i>>5)&31, i&31
			kittyPalette[i] = color.RGBA{uint8(r<<3 | r>>2), uint8(g<<3 | g>>2), uint8(b<<3 | b>>2), 255}
		}
	})
	return kittyPalette
}

// cropKittySymbol is img cut to the command's source rectangle (x, y, w, h).
func cropKittySymbol(img *vt.SixelImage, cmd *vt.KittyCommand) *vt.SixelImage {
	r := image.Rect(0, 0, img.Width, img.Height)
	if cmd.SourceX > 0 || cmd.SourceY > 0 || cmd.SourceWidth > 0 || cmd.SourceHeight > 0 {
		x0, y0 := max(cmd.SourceX, 0), max(cmd.SourceY, 0)
		x1, y1 := img.Width, img.Height
		if cmd.SourceWidth > 0 {
			x1 = x0 + cmd.SourceWidth
		}
		if cmd.SourceHeight > 0 {
			y1 = y0 + cmd.SourceHeight
		}
		r = r.Intersect(image.Rect(x0, y0, x1, y1))
	}
	if r.Empty() {
		return img
	}
	if r == image.Rect(0, 0, img.Width, img.Height) {
		return img
	}
	out := &vt.SixelImage{Width: r.Dx(), Height: r.Dy(), Pix: make([]uint16, r.Dx()*r.Dy()), Palette: img.Palette}
	for y := range r.Dy() {
		copy(out.Pix[y*r.Dx():(y+1)*r.Dx()], img.Pix[(r.Min.Y+y)*img.Width+r.Min.X:(r.Min.Y+y)*img.Width+r.Max.X])
	}
	return out
}

// resampleKittySymbol is img scaled to w by h pixels, nearest pixel, the size
// the glyph path reads: cols cells of cellW pixels by rows of cellH.
func resampleKittySymbol(img *vt.SixelImage, w, h int) *vt.SixelImage {
	if w == img.Width && h == img.Height {
		return img
	}
	out := &vt.SixelImage{Width: w, Height: h, Pix: make([]uint16, w*h), Palette: img.Palette}
	for y := range h {
		sy := y * img.Height / h
		row := img.Pix[sy*img.Width : (sy+1)*img.Width]
		for x := range w {
			out.Pix[y*w+x] = row[x*img.Width/w]
		}
	}
	return out
}

// drawKittySymbols draws one kitty command of window win as glyphs: a
// picture's cells marked at the cursor, or a deleted one's blanked.
func (m *OS) drawKittySymbols(win *terminal.Window, kp *KittyPassthrough, cmd *vt.KittyCommand) {
	sp, term := m.SixelPassthrough, win.Terminal
	if sp == nil || term == nil || cmd.Virtual {
		return
	}
	place, erase := m.kittySymbols.take(win.ID, cmd, kp.calculateImageCells)
	if len(erase) > 0 {
		gone := make(map[uint32]bool, len(erase))
		for _, id := range erase {
			gone[id] = true
		}
		term.EraseImageMarkers(func(id uint32) bool { return gone[id] })
		sp.Forget(erase)
	}
	if place == nil || place.rows <= 0 || place.cols <= 0 {
		return
	}
	caps := kp.hostCaps()
	cw, ch := caps.CellWidth, caps.CellHeight
	if cw <= 0 || ch <= 0 {
		cw, ch = vt.FallbackCellWidth, vt.FallbackCellHeight
	}
	rows, cols := min(place.rows, vt.SixelMaxDimension/ch), min(place.cols, vt.SixelMaxDimension/cw)
	if rows <= 0 || cols <= 0 || rows*ch*cols*cw > vt.SixelMaxPixels {
		return
	}
	img := resampleKittySymbol(place.img, cols*cw, rows*ch)
	cur := term.CursorPosition()
	id := sp.RegisterImage(win.ID, img, cw, ch, rows, cols, symbolView(rows, cols, cur.X, term.Width(), term.Height()))
	if id == 0 {
		return
	}
	term.PlaceImageMarkers(rows, cols, id, place.move)
	m.kittySymbols.drew(win.ID, place.imageID, id)
	win.NoteRedraw()
}
