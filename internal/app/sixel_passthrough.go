package app

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func sixelPassthroughLog(format string, args ...any) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}
	f, err := debuglog.Open(debuglog.Path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] SIXEL-PASSTHROUGH: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

// sixelMode is how this connection's terminal is shown a pane's sixel image.
type sixelMode int

const (
	// sixelPlaceholder draws a dim box where the image is. The host has
	// neither sixel nor kitty graphics, and a sixel sent to it would at best
	// be dropped and at worst print as text.
	sixelPlaceholder sixelMode = iota
	// sixelNative sends the image as sixel, cropped to what is visible.
	sixelNative
	// sixelViaKitty sends the decoded image as a kitty graphics image, for a
	// host that has kitty graphics and not sixel.
	sixelViaKitty
)

func (m sixelMode) String() string {
	switch m {
	case sixelNative:
		return "sixel"
	case sixelViaKitty:
		return "kitty"
	default:
		return "placeholder"
	}
}

// Memory bounds. A pane keeps the images whose cells may still be on screen
// or in its scrollback; these cap what that can cost.
const (
	// sixelWindowBudget is the most decoded image data one pane holds. Past
	// it the pane's oldest images are dropped, and their cells, if they are
	// ever shown again, show the placeholder. Images whose cells are all gone
	// are freed long before this, by the sweep (sixel_sweep.go); the budget
	// is for a pane that keeps many pictures in its scrollback.
	sixelWindowBudget = 16 << 20
	// sixelMaxImages bounds the number of images across all panes, which an
	// animation of tiny frames could otherwise grow without limit.
	sixelMaxImages = 4096
)

// SixelPassthrough shows the sixel images panes draw on this connection's
// terminal.
//
// The emulator marks the cells an image covers (see internal/vt's
// sixel_marker.go) and hands the image here, where it is decoded and kept
// under an id. The compositor finds the marked cells on each finished frame
// (sixel_frame.go) and says which parts of which images are visible where;
// this type turns that into bytes for the host, after the frame's text, from
// the renderer's writer (FrameBytes).
type SixelPassthrough struct {
	mu   sync.Mutex
	mode sixelMode
	caps *HostCapabilities

	nextID   uint32
	images   map[uint32]*sixelEntry
	byWindow map[string]int // decoded bytes held per window

	frame sixelFrameState
	// direct writes to the host outside the renderer's writes, for output a
	// background job made ready. See ConnectFrameWriter.
	direct func([]byte)
	// lastSweep is when the last sweep started. See sixel_sweep.go.
	lastSweep time.Time
}

// sixelEntry is one image a pane drew.
type sixelEntry struct {
	windowID string
	img      *vt.SixelImage // nil in placeholder mode
	// raw is the guest's own DCS body, sent unchanged when the whole image
	// is visible at the size it was drawn for.
	raw []byte
	// cellW and cellH are the cell size the pane measured the image with,
	// and rows and cols the cells it covers at that size.
	cellW, cellH int
	rows, cols   int
	// kittyID is the id the image was sent to a kitty host under, 0 until
	// it is sent, and kittyPayload the transmission once it is built.
	kittyID      uint32
	kittyPayload []byte
	bytes        int
	seq          uint64 // registration order, for eviction
	// born is when the image was registered. The sweep leaves a young image
	// alone: its cells are written just after it is registered.
	born time.Time
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
}

// NewSixelPassthroughWithOptions creates the passthrough for one connection.
func NewSixelPassthroughWithOptions(opts SixelPassthroughOptions) *SixelPassthrough {
	caps := opts.Caps
	if caps == nil {
		caps = GetHostCapabilities()
	}
	sp := &SixelPassthrough{
		caps:     caps,
		images:   make(map[uint32]*sixelEntry),
		byWindow: make(map[string]int),
	}
	sp.mode = chooseSixelMode(caps.SixelGraphics || opts.ForceEnable, caps.KittyGraphics)
	sixelPassthroughLog("NewSixelPassthrough: sixel=%v kitty=%v force=%v term=%s mode=%s",
		caps.SixelGraphics, caps.KittyGraphics, opts.ForceEnable, caps.TerminalName, sp.mode)
	return sp
}

// chooseSixelMode prefers the host's own sixel, then kitty, then the box.
// Sixel first because it is the format the image arrived in: the whole image
// goes out byte for byte, and only a crop is re-encoded.
func chooseSixelMode(sixel, kitty bool) sixelMode {
	switch {
	case sixel:
		return sixelNative
	case kitty:
		return sixelViaKitty
	default:
		return sixelPlaceholder
	}
}

// SetHostSixel updates whether the host draws sixel, from an answer that came
// after the passthrough was built: an SSH client's DA1 reply. Images already
// shown in the old mode are taken down on the next frame.
func (sp *SixelPassthrough) SetHostSixel(sixel bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	mode := chooseSixelMode(sixel, sp.caps.KittyGraphics)
	if mode == sp.mode {
		return
	}
	sixelPassthroughLog("SetHostSixel: %s -> %s", sp.mode, mode)
	sp.frame.pending = append(sp.frame.pending, sp.takeDownLocked()...)
	sp.mode = mode
	// Images decoded for the old mode stay valid; ones registered in
	// placeholder mode were never decoded and keep showing the box.
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
// reader and not the UI.
func (sp *SixelPassthrough) Register(windowID string, cmd *vt.SixelCommand) uint32 {
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
	mode := sp.mode
	sp.mu.Unlock()

	e := &sixelEntry{windowID: windowID, cellW: cw, cellH: ch, rows: rows, cols: cols}
	if mode != sixelPlaceholder {
		// Decoded outside the lock: it is the expensive part, and nothing
		// else touches the image until it is registered below.
		e.img = vt.DecodeSixel(cmd)
		if e.img != nil {
			e.bytes = e.img.Bytes()
			if mode == sixelNative && e.img.Exact {
				e.raw = append([]byte(nil), cmd.RawSequence...)
				e.bytes += len(e.raw)
			}
		}
		// An image too large to decode still gets an id, so its cells show
		// the placeholder rather than nothing.
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
	sixelPassthroughLog("Register: win=%s id=%d %dx%d px, %dx%d cells at %dx%d, %d bytes, mode=%s",
		windowID[:min(8, len(windowID))], id, cmd.Width, cmd.Height, cols, rows, cw, ch, e.bytes, mode)
	return id
}

// allocateIDLocked returns a free id in 1..vt.SixelMaxID, or 0 when every id
// is taken, which the image cap makes impossible in practice.
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

// evictLocked drops the oldest images of windowID until the window is within
// budget, and the oldest overall past the image count cap. An image on screen
// in the last frame is kept while there is anything older to drop.
func (sp *SixelPassthrough) evictLocked(windowID string) {
	for sp.byWindow[windowID] > sixelWindowBudget || len(sp.images) > sixelMaxImages {
		overCount := len(sp.images) > sixelMaxImages
		var victim uint32
		var oldest uint64
		for id, e := range sp.images {
			if !overCount && e.windowID != windowID {
				continue
			}
			if sp.frame.visible[id] {
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

// dropLocked forgets one image, and frees it on a kitty host.
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
	sixelPassthroughLog("ClearWindow: windowID=%s", windowID[:min(8, len(windowID))])
}

// ImageCount is the number of images held, for tests and the debug log.
func (sp *SixelPassthrough) ImageCount() int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return len(sp.images)
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

// setupSixelPassthrough connects a window's emulator to the passthrough.
func (m *OS) setupSixelPassthrough(window *terminal.Window) {
	if m.SixelPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}
	sp := m.SixelPassthrough
	id := window.ID
	window.Terminal.SetSixelPassthroughFunc(func(cmd *vt.SixelCommand, _, _ int) uint32 {
		return sp.Register(id, cmd)
	})
	window.Terminal.SetSixelAdvertised(sp.Advertised)
}
