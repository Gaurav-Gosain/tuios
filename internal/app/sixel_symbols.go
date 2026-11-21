package app

import (
	"image"
	"image/color"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/shot/mosaic"
	"tuios.dev/vt"
)

const symbolCellBytes = int(unsafe.Sizeof(mosaic.Cell{})) + 1

type symbolImage struct {
	kind           mosaic.Kind
	colors         mosaic.Colors
	cells          []mosaic.Cell
	have           []bool
	measured, poor bool
	waiting        bool
}

func (s *symbolImage) bytes() int {
	if s == nil {
		return 0
	}
	return len(s.cells) * symbolCellBytes
}

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

var symbolDrawCost = sync.OnceValue(func() time.Duration {
	return testknob.Millis("symbol_draw_cost_ms")
})

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

const (
	symbolBudgetRate  = 0.25
	symbolBudgetBurst = 100 * time.Millisecond
)

type symbolBudget struct {
	tokens time.Duration
	at     time.Time
	timer  *time.Timer
}

func (b *symbolBudget) refill(now time.Time) {
	if b.at.IsZero() {
		b.tokens = symbolBudgetBurst
	} else {
		b.tokens = min(symbolBudgetBurst, b.tokens+time.Duration(float64(now.Sub(b.at))*symbolBudgetRate))
	}
	b.at = now
}

func (b *symbolBudget) wait() time.Duration {
	if b.tokens > 0 {
		return 0
	}
	return time.Duration(float64(-b.tokens) / symbolBudgetRate)
}

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

func (sp *SixelPassthrough) spendSymbolsLocked(windowID string, d time.Duration, onFrame bool) {
	now := time.Now()
	sp.symbolBudgetLocked(windowID, now).tokens -= d
	if onFrame {
		sp.frameBudget.refill(now)
		sp.frameBudget.tokens -= d
	}
}

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

type colorBoxes struct {
	key [colorBoxSlots]uint32
	val [colorBoxSlots]color.Color
}

const colorBoxSlots = 4096

func (b *colorBoxes) box(c color.RGBA) color.Color {
	k := 1<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	i := (k * 2654435761) >> 20 % colorBoxSlots
	if b.key[i] == k {
		return b.val[i]
	}
	v := color.Color(c)
	b.key[i], b.val[i] = k, v
	return v
}

func symbolCell(c *uv.Cell, mc *mosaic.Cell, colors mosaic.Colors, boxes *colorBoxes) {
	bg := c.Style.Bg
	*c = uv.Cell{Content: " ", Width: 1}
	c.Style.Bg = bg
	if mc.Clear {
		return
	}
	c.Content = mc.Glyph
	if colors == mosaic.ANSI16 {
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
		e              *sixelEntry
		sym            *symbolImage
		dim            float64
		dimBg          color.Color
		dimResolved    bool
		missing        image.Rectangle
		missingPresent bool
		held           bool
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
						if _, pi.dimBg, _ = m.paneDimGround(); pi.dimBg != nil {
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

func symbolKind(setting, term string) mosaic.Kind {
	if _, colored := symbolColors(); !colored {
		return mosaic.Off
	}
	return imageSymbolKind(setting, term)
}

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
