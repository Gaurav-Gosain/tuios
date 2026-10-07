package app

import (
	"image"
	"image/color"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/mosaic"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Images on a host that draws no graphics.
//
// The Linux console under kmscon, and any terminal without sixel and kitty
// graphics, can show a pane's image only as text. In that case the sixel
// passthrough runs in sixelSymbols mode: each image is drawn as block glyphs
// (internal/mosaic), one glyph with two colours per image cell, and a pass
// over the composed canvas puts those glyphs where the image's marker cells
// are. Everything the marker model gives a picture holds: the glyphs move
// with the pane, scroll into the scrollback, are cut by popups and other
// panes, and go when the cells are cleared.
//
// The pass runs before the shading passes: the modal scrim, the spotlight and
// the pane's dim_unfocused. The scrim and the spotlight then fade the glyphs
// like text. dim_unfocused is applied where the pane is rendered, to the
// marker cells, so the pass gives each glyph the pane's dim itself (see
// drawImageSymbols).
//
// What is drawn, and when. A picture is drawn a part at a time: the cells the
// pane shows when the image arrives are drawn on the PTY reader, and any other
// cell is drawn by the frame pass when it first comes into view (scrolled back
// to, or uncovered by a resize). The dither is ordered, so the parts meet
// without a seam. A pane that sends pictures faster than they can be drawn,
// a video played as sixel, spends a per-pane budget of drawing time
// (symbolBudget); past it a new picture shows the image box until the budget
// is back, and the frame pass then draws the part on screen.
//
// A pane is told it can draw sixel in this mode, since what it draws is
// shown. Programs that pick between sixel and their own text output, such as
// chafa, timg and yazi, then send sixel, and a client that attaches later with
// real graphics shows the same image as a picture.

// symbolCellBytes is what one drawn cell costs for the per-pane image budget:
// the cell and its flag in symbolImage.have.
const symbolCellBytes = int(unsafe.Sizeof(mosaic.Cell{})) + 1

// symbolImage is one picture drawn as glyphs of one set in one colour set.
//
// After the image is registered it is read and written only by the frame
// pass, on the UI goroutine. The pointer itself is changed under the
// passthrough's lock, when the glyph set or the colour depth changes.
type symbolImage struct {
	kind   mosaic.Kind
	colors mosaic.Colors
	// cells is the whole picture, row by row, nil until a part is drawn.
	// have says which of its cells are drawn.
	cells []mosaic.Cell
	have  []bool
	// measured says the 16-colour check has run, and poor that the picture
	// failed it (mosaic.MinFidelity): it shows the image box. See
	// fidelityRegion for what is measured.
	measured, poor bool
	// waiting says the pane's budget was spent when the image came: the
	// image box is shown until the budget is back.
	waiting bool
}

// bytes is what the picture's cells cost the pane's image budget.
func (s *symbolImage) bytes() int {
	if s == nil {
		return 0
	}
	return len(s.cells) * symbolCellBytes
}

// draw draws the cells of region that are not drawn yet. It reports whether
// it had any to draw.
//
// At sixteen colours the first draw also draws fidelityRegion, which the
// 16-colour check measures, so the check judges the picture and not whatever
// part of it was on screen first.
func (s *symbolImage) draw(e *sixelEntry, region image.Rectangle) bool {
	bounds := image.Rect(0, 0, e.cols, e.rows)
	region = region.Intersect(bounds)
	if region.Empty() || e.img == nil {
		return false
	}
	var measure image.Rectangle
	if !s.measured {
		s.measured = true
		if s.colors == mosaic.ANSI16 {
			measure = fidelityRegion(e.rows, e.cols)
			region = region.Union(measure)
		}
	}
	if s.cells == nil {
		s.cells = make([]mosaic.Cell, e.rows*e.cols)
		s.have = make([]bool, e.rows*e.cols)
	}
	img := mosaic.Indexed{Width: e.img.Width, Height: e.img.Height, Pix: e.img.Pix, Palette: e.img.Palette}
	pic := mosaic.Prepare(img)
	drew := false
	for r := region.Min.Y; r < region.Max.Y; r++ {
		// Each run of cells this row still lacks, drawn as one region.
		for c := region.Min.X; c < region.Max.X; {
			if s.have[r*e.cols+c] {
				c++
				continue
			}
			end := c + 1
			for end < region.Max.X && !s.have[r*e.cols+end] {
				end++
			}
			pic.EncodeRegion(s.cells, e.cellW, e.cellH, e.cols, image.Rect(c, r, end, r+1), s.kind, s.colors)
			for i := c; i < end; i++ {
				s.have[r*e.cols+i] = true
			}
			drew = true
			c = end
		}
	}
	if !measure.Empty() {
		s.poor = mosaic.Fidelity(img, e.cellW, e.cellH, e.cols, measure, s.cells) < mosaic.MinFidelity
	}
	if d := symbolDrawCost(); drew && d > 0 {
		time.Sleep(d)
	}
	return drew
}

// fidelityRegion is the part of a picture of rows by cols cells that the
// 16-colour check measures: the whole picture up to fidelityCells, and past
// that a window of about that many cells in its middle, which is enough to
// judge whether the shapes survive.
func fidelityRegion(rows, cols int) image.Rectangle {
	if rows*cols <= fidelityCells {
		return image.Rect(0, 0, cols, rows)
	}
	w := min(cols, 100)
	h := min(rows, max(2, fidelityCells/w))
	x0, y0 := (cols-w)/2, (rows-h)/2
	return image.Rect(x0, y0, x0+w, y0+h)
}

const fidelityCells = 5000

// symbolDrawCost is time added to every glyph draw, read once from
// TUIOS_E2E_SYMBOL_DRAW_COST (a Go duration). It exists for the end-to-end
// tests alone: a draw on a fast machine costs too little to spend the drawing
// budget the way a slow one does, and the test of the budget needs it spent.
// Ordinary runs never set it.
var symbolDrawCost = sync.OnceValue(func() time.Duration {
	d, _ := time.ParseDuration(os.Getenv("TUIOS_E2E_SYMBOL_DRAW_COST"))
	return max(d, 0)
})

// symbolView is the part of an image a pane shows when the image arrives: the
// image's columns up to the pane's right edge, and its last rows, as many as
// the pane is high. An image taller than the pane scrolls its top rows off
// as it is drawn.
func symbolView(rows, cols, cursorX, paneW, paneH int) image.Rectangle {
	w := cols
	if paneW > 0 {
		w = min(cols, max(1, paneW-cursorX))
	}
	top := 0
	if paneH > 0 {
		top = max(0, rows-paneH)
	}
	return image.Rect(0, top, w, rows)
}

// The budget for drawing glyphs, per pane. It refills at symbolBudgetRate of
// wall time up to symbolBudgetBurst, and every draw spends what it took. An
// image that arrives while the budget is spent is not drawn: the pane sends
// pictures faster than they can be drawn, and drawing them would put its
// reader further behind the program. Its cells show the image box until the
// budget is back, and then the frame pass draws the part on screen.
//
// The frame pass runs on the UI goroutine for every pane, so its draws also
// spend one budget shared by all panes (SixelPassthrough.frameBudget). Without
// it, N panes that flood would take N quarters of the UI goroutine.
const (
	symbolBudgetRate  = 0.25
	symbolBudgetBurst = 100 * time.Millisecond
)

// symbolBudget is one pane's budget. Guarded by SixelPassthrough.mu.
type symbolBudget struct {
	tokens time.Duration
	at     time.Time
	// timer is set while a frame is asked for at the time the budget is
	// back.
	timer *time.Timer
}

func (b *symbolBudget) refill(now time.Time) {
	if b.at.IsZero() {
		b.tokens = symbolBudgetBurst
	} else {
		b.tokens = min(symbolBudgetBurst, b.tokens+time.Duration(float64(now.Sub(b.at))*symbolBudgetRate))
	}
	b.at = now
}

// wait is how long the budget takes to be positive again.
func (b *symbolBudget) wait() time.Duration {
	if b.tokens > 0 {
		return 0
	}
	return time.Duration(float64(-b.tokens) / symbolBudgetRate)
}

// symbolBudgetLocked is a pane's budget, refilled to now. Only a pane the
// passthrough knows (setupSixelPassthrough, until ClearWindow) keeps its
// budget: an image a pane registers after it closed gets a full one that is
// not stored, so nothing is left behind for a pane that is gone.
func (sp *SixelPassthrough) symbolBudgetLocked(windowID string, now time.Time) *symbolBudget {
	b := sp.budgets[windowID]
	if b == nil {
		b = &symbolBudget{}
		if sp.wakers[windowID] != nil {
			if sp.budgets == nil {
				sp.budgets = make(map[string]*symbolBudget)
			}
			sp.budgets[windowID] = b
		}
	}
	b.refill(now)
	return b
}

// canDrawLocked says a pane may draw glyphs now: its budget is positive, and
// for a draw on the frame pass (onFrame) the shared frame budget is too.
func (sp *SixelPassthrough) canDrawLocked(windowID string, onFrame bool) bool {
	now := time.Now()
	if sp.symbolBudgetLocked(windowID, now).tokens <= 0 {
		return false
	}
	if onFrame {
		sp.frameBudget.refill(now)
		return sp.frameBudget.tokens > 0
	}
	return true
}

// spendSymbolsLocked charges a pane's budget for a draw, and the shared frame
// budget for a draw on the frame pass.
func (sp *SixelPassthrough) spendSymbolsLocked(windowID string, d time.Duration, onFrame bool) {
	now := time.Now()
	sp.symbolBudgetLocked(windowID, now).tokens -= d
	if onFrame {
		sp.frameBudget.refill(now)
		sp.frameBudget.tokens -= d
	}
}

// wakeWhenBudgetLocked asks the pane for a frame once its budget and the
// frame budget are back, so the images that wait for them are drawn even when
// the pane has gone quiet.
func (sp *SixelPassthrough) wakeWhenBudgetLocked(windowID string) {
	now := time.Now()
	b := sp.symbolBudgetLocked(windowID, now)
	if b.timer != nil {
		return
	}
	sp.frameBudget.refill(now)
	wait := time.Millisecond + max(b.wait(), sp.frameBudget.wait())
	b.timer = time.AfterFunc(wait, func() {
		sp.mu.Lock()
		b.timer = nil
		wake := sp.wakers[windowID]
		sp.mu.Unlock()
		sixelPassthroughLog("symbols: win=%s drawing budget is back", windowID[:min(8, len(windowID))])
		if wake != nil {
			wake()
		}
	})
}

// drawSymbolsOnReader draws the part of e its pane shows as it arrives, on
// the PTY reader, when the pane's budget allows; otherwise e waits. Called
// before e is registered, so nothing else can see it yet.
func (sp *SixelPassthrough) drawSymbolsOnReader(e *sixelEntry, k mosaic.Kind, colors mosaic.Colors, view image.Rectangle) {
	s := &symbolImage{kind: k, colors: colors}
	e.sym = s
	sp.mu.Lock()
	ok := sp.canDrawLocked(e.windowID, false)
	sp.mu.Unlock()
	if !ok {
		s.waiting = true
		sixelPassthroughLog("symbols: win=%s over its drawing budget, the image waits", e.windowID[:min(8, len(e.windowID))])
		return
	}
	start := time.Now()
	s.draw(e, view)
	spent := time.Since(start)
	sp.mu.Lock()
	sp.spendSymbolsLocked(e.windowID, spent, false)
	sp.mu.Unlock()
	e.bytes += s.bytes()
}

// symbolsOfLocked is image e's glyphs for the glyph set and colours of this
// frame, started over when the set or the colours changed. An image that
// waits for its pane's budget stops waiting once the budget is back.
func (sp *SixelPassthrough) symbolsOfLocked(e *sixelEntry, colors mosaic.Colors) *symbolImage {
	if e.sym == nil || e.sym.kind != sp.symbols || e.sym.colors != colors {
		d := -e.sym.bytes()
		e.bytes += d
		sp.byWindow[e.windowID] += d
		e.sym = &symbolImage{kind: sp.symbols, colors: colors}
	}
	if e.sym.waiting {
		if sp.canDrawLocked(e.windowID, true) {
			e.sym.waiting = false
		} else {
			sp.wakeWhenBudgetLocked(e.windowID)
		}
	}
	return e.sym
}

// symbolColors is the colour set the frame is drawn for, and false when the
// frame carries no colour at all: NO_COLOR, an ASCII profile, or output that
// is not a terminal. Glyphs without their colours are no picture, so those
// show the image box.
func symbolColors() (mosaic.Colors, bool) {
	switch theme.ColorProfile() {
	case colorprofile.ASCII, colorprofile.NoTTY:
		return mosaic.TrueColor, false
	}
	switch overlay.CurrentDepth() {
	case overlay.Depth256:
		return mosaic.XTerm256, true
	case overlay.Depth16:
		return mosaic.ANSI16, true
	}
	return mosaic.TrueColor, true
}

// colorBoxes hands out each colour of a picture as a color.Color, boxed once.
// A cell's style holds an interface, and boxing a color.RGBA allocates, so
// without this every glyph on every frame would. Direct-mapped: a picture's
// colours that collide just box again. Used by the frame pass only.
type colorBoxes struct {
	key [colorBoxSlots]uint32
	val [colorBoxSlots]color.Color
}

const colorBoxSlots = 4096

func (b *colorBoxes) box(c color.RGBA) color.Color {
	// The tag bit keeps black apart from an empty slot.
	k := 1<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	i := (k * 2654435761) >> 20 % colorBoxSlots
	if b.key[i] == k {
		return b.val[i]
	}
	v := color.Color(c)
	b.key[i], b.val[i] = k, v
	return v
}

// symbolCell draws one image cell as its glyph. A transparent cell, or the
// transparent part of one, keeps the background the pane put there.
func symbolCell(c *uv.Cell, mc *mosaic.Cell, colors mosaic.Colors, boxes *colorBoxes) {
	bg := c.Style.Bg
	*c = uv.Cell{Content: " ", Width: 1}
	c.Style.Bg = bg
	if mc.Clear {
		return
	}
	c.Content = mc.Glyph
	if colors == mosaic.ANSI16 {
		// The terminal's own colours, by index. A uint8 boxes without
		// allocating.
		if mc.Fg.A != 0 {
			c.Style.Fg = ansi.BasicColor(mc.FgIndex)
		}
		if mc.Bg.A != 0 {
			c.Style.Bg = ansi.BasicColor(mc.BgIndex)
		}
		return
	}
	if mc.Fg.A != 0 {
		c.Style.Fg = boxes.box(mc.Fg)
	}
	if mc.Bg.A != 0 {
		c.Style.Bg = boxes.box(mc.Bg)
	}
}

// drawImageSymbols replaces the marker cells of every image on the canvas with
// its glyphs, in symbols mode. It runs before the shading passes (see the top
// of this file), and scanSixelFrame runs it again for a marker drawn after
// them. A marker of an image this mode cannot draw is left for the scan.
//
// A pane's dim_unfocused was applied to the marker cells, which the glyphs
// replace, so each glyph is dimmed here with the dim its pane was rendered at
// and toward the same ground (see dimGlyphCell).
func (m *OS) drawImageSymbols(canvas *frameCanvas) {
	sp := m.SixelPassthrough
	if sp == nil {
		return
	}
	colors, colored := symbolColors()
	sp.mu.Lock()
	if sp.mode != sixelSymbols || len(sp.images) == 0 {
		sp.mu.Unlock()
		return
	}
	k := sp.symbols
	sp.mu.Unlock()
	if !colored {
		return
	}
	if sp.boxes == nil {
		sp.boxes = new(colorBoxes)
	}

	type paneImage struct {
		e   *sixelEntry
		sym *symbolImage
		// dim is the pane's dim, 0 to 1, and dimBg the pane ground both
		// colours of a glyph carry toward.
		dim            float64
		dimBg          color.Color
		dimResolved    bool
		missing        image.Rectangle
		missingPresent bool
		// held says the budget stopped this frame from drawing the
		// cells it lacks: the image box is shown for now.
		held bool
	}
	var images map[uint32]*paneImage
	look := func(id uint32) *paneImage {
		if pi, ok := images[id]; ok {
			return pi
		}
		if images == nil {
			images = make(map[uint32]*paneImage)
		}
		var pi *paneImage
		sp.mu.Lock()
		if e := sp.images[id]; e != nil && e.img != nil && k != mosaic.Off {
			pi = &paneImage{e: e, sym: sp.symbolsOfLocked(e, colors)}
		}
		sp.mu.Unlock()
		images[id] = pi
		return pi
	}

	// First the cells this frame shows that are not drawn yet, so each
	// image is drawn once for the frame rather than once per run.
	for _, line := range canvas.Lines {
		for x := range line {
			if !vt.IsSixelMarker(line[x].Content) {
				continue
			}
			id, row, col, ok := vt.ParseSixelMarker(line[x].Content)
			if !ok {
				continue
			}
			pi := look(id)
			if pi == nil || pi.sym.waiting || row < 0 || col < 0 || row >= pi.e.rows || col >= pi.e.cols {
				continue
			}
			if pi.sym.have != nil && pi.sym.have[row*pi.e.cols+col] {
				continue
			}
			cell := image.Rect(col, row, col+1, row+1)
			if pi.missingPresent {
				pi.missing = pi.missing.Union(cell)
			} else {
				pi.missing, pi.missingPresent = cell, true
			}
		}
	}
	// Every image met is on screen this frame, drawn or boxed, and must not
	// be evicted. The scan sees no marker of these, so it is told here.
	sp.mu.Lock()
	if sp.frame.drawn == nil {
		sp.frame.drawn = make(map[uint32]bool)
	}
	for id, pi := range images {
		if pi != nil {
			sp.frame.drawn[id] = true
		}
	}
	sp.mu.Unlock()
	for _, pi := range images {
		if pi == nil || !pi.missingPresent {
			continue
		}
		sp.mu.Lock()
		ok := sp.canDrawLocked(pi.e.windowID, true)
		if !ok {
			sp.wakeWhenBudgetLocked(pi.e.windowID)
		}
		sp.mu.Unlock()
		if !ok {
			pi.held = true
			continue
		}
		before := pi.sym.bytes()
		start := time.Now()
		pi.sym.draw(pi.e, pi.missing)
		spent := time.Since(start)
		sp.mu.Lock()
		sp.spendSymbolsLocked(pi.e.windowID, spent, true)
		if grown := pi.sym.bytes() - before; grown > 0 && pi.e.sym == pi.sym {
			pi.e.bytes += grown
			sp.byWindow[pi.e.windowID] += grown
			sp.evictLocked(pi.e.windowID)
		}
		sp.mu.Unlock()
	}

	var (
		dim     color.Color
		scratch uv.Cell
		memo    blendMemo
	)
	for _, line := range canvas.Lines {
		for x := range line {
			c := &line[x]
			if !vt.IsSixelMarker(c.Content) {
				continue
			}
			id, row, col, ok := vt.ParseSixelMarker(c.Content)
			if !ok {
				continue
			}
			pi := images[id]
			if pi == nil {
				continue
			}
			if pi.sym.waiting || pi.sym.poor || pi.held {
				if dim == nil {
					dim = theme.UI().FgDim
				}
				placeholderCell(c, row, col, pi.e.rows, pi.e.cols, dim)
				continue
			}
			i := row*pi.e.cols + col
			if row < 0 || col < 0 || col >= pi.e.cols || i >= len(pi.sym.cells) || !pi.sym.have[i] {
				blankCellKeepGround(c)
				continue
			}
			symbolCell(c, &pi.sym.cells[i], pi.sym.colors, sp.boxes)
			if !pi.dimResolved {
				pi.dimResolved = true
				if w := m.windowByID(pi.e.windowID); w != nil {
					if d := w.CachedContentDim(); d > 0 {
						if _, pi.dimBg = m.paneDimGround(); pi.dimBg != nil {
							pi.dim = float64(d) / 100
						}
					}
				}
			}
			if pi.dim > 0 {
				*c = *dimGlyphCell(&scratch, c, pi.dimBg, pi.dim, &memo)
			}
		}
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
// mosaic.Encode. With no colour at all it is off: see symbolKind.
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

// symbolKind is imageSymbolKind for a frame written through this process's
// colour profile: off when the profile carries no colour (symbolColors).
func symbolKind(setting, term string) mosaic.Kind {
	if _, colored := symbolColors(); !colored {
		return mosaic.Off
	}
	return imageSymbolKind(setting, term)
}

// drawsSymbols says a client draws images as glyphs: its setting is not off,
// its frame carries colour, and its host has no image protocol. A host that
// has one gets the picture in it, never glyphs, and its panes are told sixel
// only for that reason.
func drawsSymbols(setting string, caps *HostCapabilities) bool {
	if caps == nil || caps.SixelGraphics || caps.KittyGraphics {
		return false
	}
	return symbolKind(setting, caps.Term) != mosaic.Off
}

// DrawsSymbols is drawsSymbols for a client whose frame is written through
// profile, for the SSH server, which builds a client's capabilities from its
// TERM and environment before any OS exists.
func DrawsSymbols(setting string, caps *HostCapabilities, profile colorprofile.Profile) bool {
	if caps == nil || caps.SixelGraphics || caps.KittyGraphics {
		return false
	}
	if profile == colorprofile.ASCII || profile == colorprofile.NoTTY {
		return false
	}
	return imageSymbolKind(setting, caps.Term) != mosaic.Off
}

// refreshImageSymbols applies a change of appearance.image_symbols, or of the
// colour profile, to the passthrough and to what the daemon tells the panes.
func (m *OS) refreshImageSymbols() {
	sp := m.SixelPassthrough
	if sp == nil {
		return
	}
	caps := m.hostCaps()
	k := symbolKind(m.Settings.ImageSymbols, caps.Term)
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
