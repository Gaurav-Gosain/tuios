package app

import (
	"fmt"
	"image"
	"io"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/shot/mosaic"
	"tuios.dev/vt"
)

func sixelPassthroughLog(format string, args ...any) {
	if !debuglog.Enabled() {
		return
	}
	f, err := debuglog.Open(debuglog.Path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] SIXEL-PASSTHROUGH: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type sixelMode int

const (
	sixelPlaceholder sixelMode = iota
	sixelNative
	sixelViaKitty
	sixelSymbols
)

func (m sixelMode) String() string {
	switch m {
	case sixelNative:
		return "sixel"
	case sixelViaKitty:
		return "kitty"
	case sixelSymbols:
		return "symbols"
	default:
		return "placeholder"
	}
}

const (
	sixelWindowBudget = 16 << 20
	sixelMaxImages    = 4096
)

// SixelPassthrough shows the sixel images panes draw on this connection's
// terminal.
//
// The emulator marks the cells an image covers (see vt's
// sixel_marker.go) and hands the image here, where it is decoded and kept
// under an id. The compositor finds the marked cells on each finished frame
// (sixel_frame.go) and says which parts of which images are visible where;
// this type turns that into bytes for the host, after the frame's text, from
// the renderer's writer (FrameBytes).
type SixelPassthrough struct {
	mu      sync.Mutex
	mode    sixelMode
	caps    *HostCapabilities
	symbols mosaic.Kind

	nextID   uint32
	images   map[uint32]*sixelEntry
	byWindow map[string]int

	frame     sixelFrameState
	direct    func([]byte)
	lastSweep time.Time

	budgets     map[string]*symbolBudget
	wakers      map[string]func()
	frameBudget symbolBudget
	boxes       *colorBoxes
}

type sixelEntry struct {
	windowID     string
	img          *vt.SixelImage
	raw          []byte
	cellW, cellH int
	rows, cols   int
	kittyID      uint32
	kittyPayload []byte
	sym          *symbolImage
	bytes        int
	seq          uint64
	born         time.Time
	glyphOnly    bool
}

// SixelPassthroughOptions configures a SixelPassthrough instance.
type SixelPassthroughOptions struct {
	// ForceEnable says the terminal draws sixel without asking it (web mode).
	ForceEnable bool
	// Output is unused: sixel output goes out through the renderer's writer
	// (FrameBytes), so it lands after the frame it belongs to. Kept so callers
	// that pass their graphics writer to every passthrough need not special
	// case this one.
	Output io.Writer
	// Caps is the terminal at the far end of this session. Nil falls back to
	// this process's own terminal.
	Caps *HostCapabilities
	// Symbols is the glyph set images are drawn with when the terminal draws
	// no graphics. mosaic.Off shows the placeholder box.
	Symbols mosaic.Kind
}

// NewSixelPassthroughWithOptions creates the passthrough for one connection.
func NewSixelPassthroughWithOptions(opts SixelPassthroughOptions) *SixelPassthrough {
	caps := opts.Caps
	if caps == nil {
		caps = GetHostCapabilities()
	}
	sp := &SixelPassthrough{
		caps:     caps,
		symbols:  opts.Symbols,
		images:   make(map[uint32]*sixelEntry),
		byWindow: make(map[string]int),
	}
	sp.mode = chooseSixelMode(caps.SixelGraphics || opts.ForceEnable, caps.KittyGraphics, opts.Symbols)
	sixelPassthroughLog("NewSixelPassthrough: sixel=%v kitty=%v force=%v term=%s mode=%s symbols=%s",
		caps.SixelGraphics, caps.KittyGraphics, opts.ForceEnable, caps.TerminalName, sp.mode, opts.Symbols)
	return sp
}

func chooseSixelMode(sixel, kitty bool, symbols mosaic.Kind) sixelMode {
	switch {
	case sixel:
		return sixelNative
	case kitty:
		return sixelViaKitty
	case symbols != mosaic.Off:
		return sixelSymbols
	default:
		return sixelPlaceholder
	}
}

// SetHostSixel updates whether the host draws sixel, from an answer that came
// after the passthrough was built: an SSH client's DA1 reply, or a local
// terminal's that came after the startup probe gave up. Images already
// shown in the old mode are taken down on the next frame.
func (sp *SixelPassthrough) SetHostSixel(sixel bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.setModeLocked(chooseSixelMode(sixel, sp.caps.KittyGraphics, sp.symbols))
}

// SetSymbols changes the glyph set images are drawn with on a host without
// graphics, for a settings change. Images registered while the box was shown
// were never decoded and keep showing it.
func (sp *SixelPassthrough) SetSymbols(k mosaic.Kind) {
	if sp == nil {
		return
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.symbols = k
	sp.setModeLocked(chooseSixelMode(sp.mode == sixelNative, sp.caps.KittyGraphics, k))
}

func (sp *SixelPassthrough) setModeLocked(mode sixelMode) {
	if mode == sp.mode {
		return
	}
	sixelPassthroughLog("sixel mode: %s -> %s", sp.mode, mode)
	sp.frame.pending = append(sp.frame.pending, sp.takeDownLocked()...)
	sp.mode = mode
	if mode != sixelSymbols {
		for id, e := range sp.images {
			if e.glyphOnly {
				sp.dropLocked(id)
			}
		}
	}
}

// IsEnabled reports whether images are shown as pictures, in either format.
func (sp *SixelPassthrough) IsEnabled() bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.mode != sixelPlaceholder
}

// Advertised reports whether panes are told they can draw sixel. It is true
// when the picture will be shown, in sixel or through kitty: a program that
// is told sixel and draws it gets its image, and one that is not told falls
// back to text rather than to the placeholder box.
func (sp *SixelPassthrough) Advertised() bool {
	return sp.IsEnabled()
}

// Register takes an image a pane drew and returns the id its cells are
// marked with. It runs on the pane's PTY reader, so the decode costs the
// reader and not the UI. view is the part of the image, in cells, the pane
// shows as it is drawn: the part drawn as glyphs now, on a host without
// graphics (see symbolView).
func (sp *SixelPassthrough) Register(windowID string, cmd *vt.SixelCommand, view image.Rectangle) uint32 {
	if cmd == nil {
		return 0
	}
	cw, ch := cmd.CellWidth, cmd.CellHeight
	if cw <= 0 || ch <= 0 {
		return 0
	}
	rows, cols := vt.SixelCells(cmd, cw, ch)
	if rows <= 0 || cols <= 0 {
		return 0
	}
	sp.mu.Lock()
	mode, symbols := sp.mode, sp.symbols
	sp.mu.Unlock()

	e := &sixelEntry{windowID: windowID, cellW: cw, cellH: ch, rows: rows, cols: cols}
	if mode != sixelPlaceholder {
		e.img = vt.DecodeSixel(cmd)
		if e.img != nil {
			e.bytes = e.img.Bytes()
			if mode == sixelNative && e.img.Exact {
				e.raw = append([]byte(nil), cmd.RawSequence...)
				e.bytes += len(e.raw)
			}
		}
		if colors, colored := symbolColors(); mode == sixelSymbols && e.img != nil && colored {
			sp.drawSymbolsOnReader(e, symbols, colors, view)
		}
	}

	sp.mu.Lock()
	defer sp.mu.Unlock()
	id := sp.allocateIDLocked()
	if id == 0 {
		return 0
	}
	sp.frame.seq++
	e.seq = sp.frame.seq
	e.born = time.Now()
	sp.images[id] = e
	sp.byWindow[windowID] += e.bytes
	sp.evictLocked(windowID)
	if e.sym != nil && e.sym.waiting {
		sp.wakeWhenBudgetLocked(windowID)
	}
	sixelPassthroughLog("Register: win=%s id=%d %dx%d px, %dx%d cells at %dx%d, %d bytes, mode=%s",
		windowID[:min(8, len(windowID))], id, cmd.Width, cmd.Height, cols, rows, cw, ch, e.bytes, mode)
	return id
}

// DrawsSymbols reports whether images are drawn as glyphs: the host draws
// no graphics, and a glyph set is chosen.
func (sp *SixelPassthrough) DrawsSymbols() bool {
	if sp == nil {
		return false
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.mode == sixelSymbols
}

// RegisterImage is Register for a picture already decoded, img, which is
// cols cells of cellW pixels wide and rows of cellH high: a kitty image drawn
// as glyphs (kitty_symbols.go). It returns the id its cells are marked with,
// 0 when images are not drawn as glyphs.
func (sp *SixelPassthrough) RegisterImage(windowID string, img *vt.SixelImage, cellW, cellH, rows, cols int, view image.Rectangle) uint32 {
	if img == nil || cellW <= 0 || cellH <= 0 || rows <= 0 || cols <= 0 {
		return 0
	}
	sp.mu.Lock()
	mode, symbols := sp.mode, sp.symbols
	sp.mu.Unlock()
	if mode != sixelSymbols {
		return 0
	}
	e := &sixelEntry{windowID: windowID, img: img, cellW: cellW, cellH: cellH, rows: rows, cols: cols, glyphOnly: true}
	e.bytes = img.Width * img.Height * 2
	if colors, colored := symbolColors(); colored {
		sp.drawSymbolsOnReader(e, symbols, colors, view)
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	id := sp.allocateIDLocked()
	if id == 0 {
		return 0
	}
	sp.frame.seq++
	e.seq = sp.frame.seq
	e.born = time.Now()
	sp.images[id] = e
	sp.byWindow[windowID] += e.bytes
	sp.evictLocked(windowID)
	if e.sym != nil && e.sym.waiting {
		sp.wakeWhenBudgetLocked(windowID)
	}
	sixelPassthroughLog("RegisterImage: win=%s id=%d %dx%d px, %dx%d cells at %dx%d", windowID[:min(8, len(windowID))], id, img.Width, img.Height, cols, rows, cellW, cellH)
	return id
}

// Forget drops the images ids names, once their cells are gone: a kitty
// delete of pictures drawn as glyphs.
func (sp *SixelPassthrough) Forget(ids []uint32) {
	if sp == nil || len(ids) == 0 {
		return
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for _, id := range ids {
		sp.dropLocked(id)
	}
}

func (sp *SixelPassthrough) allocateIDLocked() uint32 {
	for range vt.SixelMaxID {
		sp.nextID++
		if sp.nextID > vt.SixelMaxID {
			sp.nextID = 1
		}
		if _, used := sp.images[sp.nextID]; !used {
			return sp.nextID
		}
	}
	return 0
}

func (sp *SixelPassthrough) evictLocked(windowID string) {
	for sp.byWindow[windowID] > sixelWindowBudget || len(sp.images) > sixelMaxImages {
		overCount := len(sp.images) > sixelMaxImages
		var victim uint32
		var oldest uint64
		for id, e := range sp.images {
			if !overCount && e.windowID != windowID {
				continue
			}
			if sp.frame.visible[id] || sp.frame.drawn[id] {
				continue
			}
			if victim == 0 || e.seq < oldest {
				victim, oldest = id, e.seq
			}
		}
		if victim == 0 {
			return
		}
		sp.dropLocked(victim)
	}
}

func (sp *SixelPassthrough) dropLocked(id uint32) {
	e := sp.images[id]
	if e == nil {
		return
	}
	delete(sp.images, id)
	sp.byWindow[e.windowID] -= e.bytes
	if sp.byWindow[e.windowID] <= 0 {
		delete(sp.byWindow, e.windowID)
	}
	sp.frame.dropCache(id)
	if e.kittyID != 0 {
		sp.frame.pending = append(sp.frame.pending, kittyFreeImage(e.kittyID)...)
	}
}

// ClearWindow forgets every image a window drew. Called when the window
// closes; the images' cells went with it, so nothing is left to show them.
func (sp *SixelPassthrough) ClearWindow(windowID string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for id, e := range sp.images {
		if e.windowID == windowID {
			sp.dropLocked(id)
		}
	}
	if b := sp.budgets[windowID]; b != nil && b.timer != nil {
		b.timer.Stop()
	}
	delete(sp.budgets, windowID)
	delete(sp.wakers, windowID)
	sixelPassthroughLog("ClearWindow: windowID=%s", windowID[:min(8, len(windowID))])
}

// ImageCount is the number of images held, for tests and the debug log.
func (sp *SixelPassthrough) ImageCount() int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return len(sp.images)
}

func (m *OS) imagesOnScreen() bool {
	sp := m.SixelPassthrough
	if sp == nil {
		return false
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return len(sp.images) > 0 || len(sp.byWindow) > 0
}

// WindowHasImages reports whether a window has drawn an image this
// passthrough still holds. The fullscreen fast path does not look for image
// cells, so such a window takes the compositor.
func (sp *SixelPassthrough) WindowHasImages(windowID string) bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	_, ok := sp.byWindow[windowID]
	if ok {
		return true
	}
	for _, e := range sp.images {
		if e.windowID == windowID {
			return true
		}
	}
	return false
}

func (m *OS) setupSixelPassthrough(window *terminal.Window) {
	if m.SixelPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}
	sp := m.SixelPassthrough
	id := window.ID
	term := window.Terminal
	sp.mu.Lock()
	if sp.wakers == nil {
		sp.wakers = make(map[string]func())
	}
	sp.wakers[id] = window.NoteRedraw
	sp.mu.Unlock()
	window.Terminal.SetSixelPassthroughFunc(func(cmd *vt.SixelCommand, cursorX, _ int) uint32 {
		rows, cols := vt.SixelCells(cmd, cmd.CellWidth, cmd.CellHeight)
		return sp.Register(id, cmd, symbolView(rows, cols, cursorX, term.Width(), term.Height()))
	})
	window.Terminal.SetSixelAdvertised(sp.Advertised)
}
