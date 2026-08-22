//go:build ghostty

package vt

import (
	"image/color"
	"io"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"
)

// GhosttyTerminal implements Terminal on top of libghostty-vt. libghostty
// owns parsing and grid state; this type owns what tuios needs beyond that:
// a uv-typed shadow of the viewport kept fresh from the render state's dirty
// rows, the graphics passthrough pipeline (kitty, sixel), and the handful of
// states the library keeps but does not expose (charsets, scroll region,
// kitty keyboard stack).
//
// Locking: mu serializes every libghostty call and all shadow state. Getters
// that callers use without external serialization read atomic caches, the
// same contract the pure emulator offers. vt.Callbacks are invoked after mu
// is released, so a handler may call back into the terminal.
type GhosttyTerminal struct {
	mu   sync.Mutex
	term *gh.Terminal
	rs   *gh.RenderState
	ri   *gh.RenderStateRowIterator
	rc   *gh.RenderStateRowCells

	scanner *ghosttyScanner
	dec     ghosttyCellDecoder

	// Cursor cache, refreshed by syncLocked.
	curX, curY int
	curHidden  bool

	// bufs shadow the two screens in uv cells; bufs[0] is main. active
	// mirrors which one libghostty is drawing to.
	bufs   [2]*uv.Buffer
	active int
	// gridStale is set by Write and cleared by syncLocked.
	gridStale bool

	width, height    int
	cellW, cellH     int
	scrollbackMax    int
	scrollGeneration uint64 // bumped per write; invalidates scrollback cache
	scrollCache      map[int]uv.Line
	scrollCacheGen   uint64

	// styleCache maps a libghostty style ID to its uv conversion. Reset
	// when the theme changes, since conversion depends on the theme.
	styleCache map[uint16]uv.Style

	// Theme state, mirroring the pure emulator's resolution rules.
	defaultFg, defaultBg, defaultCur color.Color
	guestFg, guestBg, guestCur       color.Color
	themePal                         [16]color.Color
	paletteClaimed                   bool
	// colors holds guest OSC 4 palette overrides, exactly as the pure
	// emulator keeps them.
	colors [256]color.Color

	pipe *bufPipe

	cb Callbacks
	// cbq holds callback invocations queued while mu was held.
	cbq []func()

	// Shadow state libghostty does not expose.
	charsetIDs       [4]byte
	gl, gr           int
	savedCharsets    [4]byte
	savedGL, savedGR int
	scrollRegion     uv.Rectangle
	kittyKbd         *kittyKeyboardState

	// Lock-free getter caches, refreshed after every write.
	cachedHasMouse   atomic.Bool
	cachedAllMotion  atomic.Bool
	cachedCellMotion atomic.Bool
	cachedMouseSGR   atomic.Bool
	cachedMousePx    atomic.Bool
	cachedAltScreen  atomic.Bool
	cachedSyncOutput atomic.Bool
	syncSetAtNanos   atomic.Int64
	cachedKittyFlags atomic.Int32
	closed           atomic.Bool
	// restorePending makes the lock-free getters flush a buffered restore
	// before answering, so state queried right after ApplyTerminalState is
	// the restored state.
	restorePending atomic.Bool

	// tuios graphics state, owned here exactly as the pure emulator owns it.
	kittyMain, kittyAlt  *KittyState
	sixelMain, sixelAlt  *SixelState
	semanticMarkers      *SemanticMarkerList
	kittyPassthroughFunc func(cmd *KittyCommand, rawData []byte)
	sixelPassthroughFunc func(cmd *SixelCommand, cursorX, cursorY, absLine int)
	textSizingFunc       func(rawOSC []byte, cursorX, cursorY, scale, textLen int)

	restore *ghosttyRestore
}

var _ Terminal = (*GhosttyTerminal)(nil)

// NewGhosttyTerminal creates a libghostty-backed terminal.
func NewGhosttyTerminal(w, h int) *GhosttyTerminal {
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	t := &GhosttyTerminal{
		width:           w,
		height:          h,
		cellW:           defaultCellWidth,
		cellH:           defaultCellHeight,
		scrollbackMax:   DefaultScrollbackSize,
		styleCache:      make(map[uint16]uv.Style),
		scrollCache:     make(map[int]uv.Line),
		charsetIDs:      defaultCharsetIDs,
		savedCharsets:   defaultCharsetIDs,
		pipe:            newBufPipe(),
		kittyKbd:        newKittyKeyboardState(),
		kittyMain:       NewKittyState(),
		kittyAlt:        NewKittyState(),
		sixelMain:       NewSixelState(),
		sixelAlt:        NewSixelState(),
		semanticMarkers: NewSemanticMarkerList(maxSemanticMarkers),
	}
	t.bufs[0] = uv.NewBuffer(w, h)
	t.bufs[1] = uv.NewBuffer(w, h)
	t.scrollRegion = uv.Rect(0, 0, w, h)
	t.dec = newGhosttyCellDecoder()

	term, err := gh.NewTerminal(
		gh.WithSize(clampU16(w), clampU16(h)),
		gh.WithMaxScrollbackLines(uint(t.scrollbackMax)),
		gh.WithWritePty(func(_ *gh.Terminal, data []byte) {
			// Query responses; the pipe write never blocks.
			_, _ = t.pipe.Write(data)
		}),
		gh.WithBell(func(_ *gh.Terminal) {
			t.queue(func(cb Callbacks) {
				if cb.Bell != nil {
					cb.Bell()
				}
			})
		}),
		gh.WithTitleChanged(func(term *gh.Terminal) {
			title, err := term.Title()
			if err != nil {
				return
			}
			t.queue(func(cb Callbacks) {
				if cb.Title != nil {
					cb.Title(title)
				}
			})
		}),
		gh.WithPwdChanged(func(term *gh.Terminal) {
			pwd, err := term.Pwd()
			if err != nil {
				return
			}
			t.queue(func(cb Callbacks) {
				if cb.WorkingDirectory != nil {
					cb.WorkingDirectory(pwd)
				}
			})
		}),
		gh.WithClipboardWrite(func(_ *gh.Terminal, w gh.ClipboardWrite) gh.ClipboardWriteResult {
			selection := "c"
			if w.Location == gh.ClipboardLocationPrimary {
				selection = "p"
			}
			for _, c := range w.Contents {
				data := string(c.Data)
				t.queue(func(cb Callbacks) {
					if cb.ClipboardSet != nil {
						cb.ClipboardSet(selection, data)
					}
				})
				break // tuios's callback carries one payload per selection
			}
			return gh.ClipboardWriteSuccess
		}),
		gh.WithDesktopNotification(func(_ *gh.Terminal, n gh.TerminalDesktopNotification) {
			t.queue(func(cb Callbacks) {
				if cb.Notify != nil {
					cb.Notify(n.Title, n.Body)
				}
			})
		}),
		gh.WithProgressReport(func(_ *gh.Terminal, r gh.TerminalProgressReport) {
			state, ok := ghosttyProgressState(r.State)
			if !ok {
				return
			}
			progress := int(r.Progress)
			if progress < 0 {
				progress = 0
			}
			t.queue(func(cb Callbacks) {
				if cb.Progress != nil {
					cb.Progress(state, progress)
				}
			})
		}),
		gh.WithDeviceAttributes(func(_ *gh.Terminal) (gh.DeviceAttributes, bool) {
			return ghosttyDeviceAttributes(), true
		}),
	)
	if err != nil {
		// Construction fails only on allocation failure inside the C
		// library; there is no meaningful degraded mode for the build that
		// chose this backend.
		panic("vt: libghostty terminal construction failed: " + err.Error())
	}
	t.term = term

	// tuios owns kitty graphics end to end; storing decoded images in the C
	// library would only duplicate memory.
	var zero uint64
	_ = t.term.SetKittyImageStorageLimit(&zero)

	rs, err := gh.NewRenderState()
	if err != nil {
		term.Close()
		panic("vt: libghostty render state construction failed: " + err.Error())
	}
	ri, err := gh.NewRenderStateRowIterator()
	if err != nil {
		rs.Close()
		term.Close()
		panic("vt: libghostty row iterator construction failed: " + err.Error())
	}
	rc, err := gh.NewRenderStateRowCells()
	if err != nil {
		ri.Close()
		rs.Close()
		term.Close()
		panic("vt: libghostty cells iterator construction failed: " + err.Error())
	}
	t.rs, t.ri, t.rc = rs, ri, rc

	t.scanner = newGhosttyScanner(ghosttyScanHooks{
		Forward:  t.forward,
		KittyAPC: t.handleKittyAPC,
		SixelDCS: t.handleSixelDCS,
		OSC:      t.handleOSC,
		CSI:      t.observeCSI,
		ESC:      t.observeESC,
		Ctrl:     t.observeCtrl,
	})
	return t
}

const (
	// defaultCellWidth/Height match the pure emulator's assumptions until
	// the host reports real pixel metrics via SetCellSize.
	defaultCellWidth  = 10
	defaultCellHeight = 20
	// maxSemanticMarkers matches the pure emulator's marker list bound.
	maxSemanticMarkers = 1000
)

func clampU16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 0xffff {
		return 0xffff
	}
	return uint16(v)
}

// queue schedules a vt callback to run once mu is released.
func (t *GhosttyTerminal) queue(f func(cb Callbacks)) {
	cb := t.cb
	t.cbq = append(t.cbq, func() { f(cb) })
}

// drain runs queued callbacks. Call with mu released.
func (t *GhosttyTerminal) drain(q []func()) {
	for _, f := range q {
		f()
	}
}

// takeQueue detaches the queued callbacks; call with mu held.
func (t *GhosttyTerminal) takeQueue() []func() {
	q := t.cbq
	t.cbq = nil
	return q
}

// forward hands scanner output to libghostty. The closed check covers a
// Close that slipped in while a passthrough callback held the lock open.
func (t *GhosttyTerminal) forward(p []byte) {
	if t.closed.Load() {
		return
	}
	t.term.VTWrite(p)
}

// Write feeds raw PTY bytes. The scanner forwards them to libghostty and
// surfaces the sequences tuios handles itself.
func (t *GhosttyTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	if t.closed.Load() {
		t.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	t.flushRestoreLocked()
	t.scanner.Scan(p)
	t.gridStale = true
	t.scrollGeneration++
	t.refreshCachesLocked()
	q := t.takeQueue()
	t.mu.Unlock()
	t.drain(q)
	return len(p), nil
}

// Read drains query responses destined for the guest.
func (t *GhosttyTerminal) Read(p []byte) (int, error) {
	if t.closed.Load() {
		return 0, io.EOF
	}
	return t.pipe.Read(p) //nolint:wrapcheck
}

// WriteResponse injects a response produced outside the emulator.
func (t *GhosttyTerminal) WriteResponse(data []byte) {
	_, _ = t.pipe.Write(data)
}

// Close releases the C resources. The pipe closes first so a blocked reader
// unblocks with EOF.
func (t *GhosttyTerminal) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	if t.pipe != nil {
		_ = t.pipe.Close()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rc.Close()
	t.ri.Close()
	t.rs.Close()
	t.term.Close()
	return nil
}

// Resize changes the terminal dimensions.
func (t *GhosttyTerminal) Resize(width, height int) {
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	t.mu.Lock()
	if t.closed.Load() {
		t.mu.Unlock()
		return
	}
	t.flushRestoreLocked()
	t.width, t.height = width, height
	_ = t.term.Resize(clampU16(width), clampU16(height), uint32(t.cellW), uint32(t.cellH))
	t.bufs[0].Resize(width, height)
	t.bufs[1].Resize(width, height)
	// DECSTBM margins reset on resize, as on the pure emulator's screens.
	t.scrollRegion = uv.Rect(0, 0, width, height)
	t.gridStale = true
	t.scrollGeneration++
	// No cache refresh: a resize cannot flip a mode, and shared-border
	// drags resize every crossing pane per motion event, so this path must
	// not pay the query round-trips.
	q := t.takeQueue()
	t.mu.Unlock()
	t.drain(q)
}

// SetCellSize records the host cell size in pixels and forwards it so
// XTWINOPS pixel reports are right.
func (t *GhosttyTerminal) SetCellSize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return
	}
	t.cellW, t.cellH = width, height
	_ = t.term.Resize(clampU16(t.width), clampU16(t.height), uint32(width), uint32(height))
}

func (t *GhosttyTerminal) Width() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.width
}

func (t *GhosttyTerminal) Height() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.height
}

func (t *GhosttyTerminal) SetCallbacks(cb Callbacks) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cb = cb
}

func (t *GhosttyTerminal) GetCallbacks() Callbacks {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cb
}

func (t *GhosttyTerminal) SetScreenClearFunc(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cb.ScreenClear = f
}

// refreshCachesLocked refreshes the atomic caches lock-free getters read.
// Runs after every write; each query is one cheap library call.
func (t *GhosttyTerminal) refreshCachesLocked() {
	if t.closed.Load() {
		return
	}
	x10, _ := t.term.Mode(gh.ModeX10Mouse)
	normal, _ := t.term.Mode(gh.ModeNormalMouse)
	button, _ := t.term.Mode(gh.ModeButtonMouse)
	any, _ := t.term.Mode(gh.ModeAnyMouse)
	t.cachedHasMouse.Store(x10 || normal || button || any)
	t.cachedAllMotion.Store(any)
	t.cachedCellMotion.Store(button)
	sgr, _ := t.term.Mode(gh.ModeSGRMouse)
	px, _ := t.term.Mode(gh.ModeSGRPixelsMouse)
	t.cachedMouseSGR.Store(sgr)
	t.cachedMousePx.Store(px)

	alt1047, _ := t.term.Mode(gh.ModeAltScreen)
	alt1049, _ := t.term.Mode(gh.ModeAltScreenSave)
	t.cachedAltScreen.Store(alt1047 || alt1049)

	sync, _ := t.term.Mode(gh.ModeSyncOutput)
	if sync && !t.cachedSyncOutput.Load() {
		t.syncSetAtNanos.Store(time.Now().UnixNano())
	}
	t.cachedSyncOutput.Store(sync)

	t.cachedKittyFlags.Store(int32(t.kittyKbd.CurrentFlags()))
}

// ensureRestored flushes a buffered restore so an atomic cache answers with
// the restored state. The check is one atomic load on the common path.
func (t *GhosttyTerminal) ensureRestored() {
	if !t.restorePending.Load() {
		return
	}
	t.mu.Lock()
	t.flushRestoreLocked()
	t.mu.Unlock()
}

// HasMouseMode reports whether any mouse tracking mode is enabled.
// Thread-safe via an atomic cache, like the pure emulator.
func (t *GhosttyTerminal) HasMouseMode() bool {
	t.ensureRestored()
	return t.cachedHasMouse.Load()
}

// HasAllMotionMode reports whether mode 1003 is enabled.
func (t *GhosttyTerminal) HasAllMotionMode() bool {
	t.ensureRestored()
	return t.cachedAllMotion.Load()
}

// HasCellMotionMode reports whether mode 1002 is enabled.
func (t *GhosttyTerminal) HasCellMotionMode() bool {
	t.ensureRestored()
	return t.cachedCellMotion.Load()
}

// IsAltScreen reports the alt-screen mode bits, like the pure emulator's
// reading of modes 1047/1049.
func (t *GhosttyTerminal) IsAltScreen() bool {
	t.ensureRestored()
	return t.cachedAltScreen.Load()
}

// IsSyncActive reports an open synchronized update, bounded by the same
// syncMaxHold the pure emulator applies.
func (t *GhosttyTerminal) IsSyncActive() bool {
	if !t.cachedSyncOutput.Load() {
		return false
	}
	return time.Now().UnixNano()-t.syncSetAtNanos.Load() < int64(syncMaxHold)
}

// KittyKeyboardFlags returns the current kitty keyboard flags.
func (t *GhosttyTerminal) KittyKeyboardFlags() int {
	t.ensureRestored()
	return int(t.cachedKittyFlags.Load())
}

func (t *GhosttyTerminal) KittyKeyboardStack() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int(nil), t.kittyKbd.stack...)
}

func (t *GhosttyTerminal) ApplicationCursorKeys() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return false
	}
	v, _ := t.term.Mode(gh.ModeDECCKM)
	return v
}

func (t *GhosttyTerminal) BracketedPasteEnabled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return false
	}
	v, _ := t.term.Mode(gh.ModeBracketedPaste)
	return v
}

// Paste feeds text to the guest, bracketed when mode 2004 is set.
func (t *GhosttyTerminal) Paste(text string) {
	if t.BracketedPasteEnabled() {
		_, _ = io.WriteString(t.pipe, ansi.BracketedPasteStart)
		_, _ = io.WriteString(t.pipe, text)
		_, _ = io.WriteString(t.pipe, ansi.BracketedPasteEnd)
		return
	}
	_, _ = io.WriteString(t.pipe, text)
}

// ghosttyProgressState maps libghostty progress states onto the pure
// emulator's OSC 9;4 states.
func ghosttyProgressState(s gh.TerminalProgressState) (ProgressState, bool) {
	switch s {
	case gh.TerminalProgressStateRemove:
		return ProgressClear, true
	case gh.TerminalProgressStateSet:
		return ProgressNormal, true
	case gh.TerminalProgressStateError:
		return ProgressError, true
	case gh.TerminalProgressStateIndeterminate:
		return ProgressIndeterminate, true
	case gh.TerminalProgressStatePause:
		return ProgressWarning, true
	default:
		return ProgressClear, false
	}
}

// ghosttyDeviceAttributes answers DA1 exactly as the pure emulator does:
// VT220 with 132 columns, sixel, selective erase, NRC, technical characters,
// windowing and ANSI color.
func ghosttyDeviceAttributes() gh.DeviceAttributes {
	var da gh.DeviceAttributes
	da.Primary.ConformanceLevel = 62
	features := []uint16{1, 4, 6, 9, 15, 18, 22}
	copy(da.Primary.Features[:], features)
	da.Primary.NumFeatures = len(features)
	return da
}
