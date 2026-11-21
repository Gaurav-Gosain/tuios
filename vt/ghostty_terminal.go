//go:build ghostty

package vt

import (
	"image/color"
	"io"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
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

	curX, curY int
	curHidden  bool

	cursorStyle  CursorStyle
	cursorSteady bool

	bufs      [2]*grid
	active    int
	gridStale bool

	width, height      int
	cellW, cellH       int
	scrollbackMax      int
	scrollGeneration   uint64
	scrollCache        map[int]uv.Line
	scrollCacheGen     uint64
	mainSbLen          int
	modesDirty         bool
	altHistory         *gh.Terminal
	altHistoryGen      uint64
	pendingMainSbClear bool

	styleCache map[uint16]uv.Style

	defaultFg, defaultBg, defaultCur color.Color
	guestFg, guestBg, guestCur       color.Color
	reportFg, reportBg               color.Color
	reportPal                        [16]color.Color
	themePal                         [16]color.Color
	paletteClaimed                   bool
	colors                           [256]color.Color

	pipe *bufPipe

	cb  Callbacks
	cbq []func()

	charsetIDs      [4]byte
	gl, gr          int
	scrollRegion    uv.Rectangle
	savedCur        [2]SavedCursor
	penProtected    bool
	savedLRMM       bool
	kittyKbd        *kittyKeyboardState
	kkpUnavailable  atomic.Bool
	tabstops        *uv.TabStops
	iconName        string
	explicitIcon    string
	titleStack      []savedTitle
	modifyOtherKeys atomic.Int32

	cachedHasMouse   atomic.Bool
	cachedAllMotion  atomic.Bool
	cachedCellMotion atomic.Bool
	cachedMouseSGR   atomic.Bool
	cachedMousePx    atomic.Bool
	cachedMouseUTF8  atomic.Bool
	cachedMouseURXVT atomic.Bool
	cachedMouseX10   atomic.Bool
	cachedAltScreen  atomic.Bool
	cachedSyncOutput atomic.Bool
	syncSetAtNanos   atomic.Int64
	syncOpens        atomic.Uint64
	cachedKittyFlags atomic.Int32
	closed           atomic.Bool
	restorePending   atomic.Bool

	kittyMain, kittyAlt    *KittyState
	semanticMarkers        *SemanticMarkerList
	kittyPassthroughFunc   func(cmd *KittyCommand, rawData []byte)
	kittyHeaderOnly        bool
	kittyImages            KittyGuestImages
	kittyImageIDTranslator KittyImageIDTranslator
	kittyPlaceholderMode   KittyPlaceholderMode
	sixelPassthroughFunc   SixelPassthroughFunc
	sixelAdvertised        func() bool
	textSizingFunc         func(rawOSC []byte, cursorX, cursorY, scale, textLen int)

	restore *ghosttyRestore
}

func (t *GhosttyTerminal) bufAt(idx int) *grid {
	if t.bufs[idx] == nil {
		t.bufs[idx] = newGrid(t.width, t.height)
	}
	return t.bufs[idx]
}

var _ Terminal = (*GhosttyTerminal)(nil)

const ghosttyScrollbackRowBudget = 4096

// NewGhosttyTerminal creates a libghostty-backed terminal with the default
// scrollback depth.
func NewGhosttyTerminal(w, h int) *GhosttyTerminal {
	return newGhosttyTerminal(w, h, DefaultScrollbackSize)
}

func newGhosttyTerminal(w, h, maxLines int) *GhosttyTerminal {
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	t := &GhosttyTerminal{
		modesDirty:      true,
		width:           w,
		height:          h,
		cellW:           defaultCellWidth,
		cellH:           defaultCellHeight,
		scrollbackMax:   maxLines,
		styleCache:      make(map[uint16]uv.Style),
		scrollCache:     make(map[int]uv.Line),
		charsetIDs:      defaultCharsetIDs,
		pipe:            newBufPipe(),
		kittyKbd:        newKittyKeyboardState(),
		kittyMain:       NewKittyState(),
		kittyAlt:        NewKittyState(),
		semanticMarkers: NewSemanticMarkerList(maxSemanticMarkers),
		cursorStyle:     defaultCursorStyle,
		cursorSteady:    defaultCursorSteady,
	}
	t.savedCur = [2]SavedCursor{{Charsets: defaultCharsetIDs}, {Charsets: defaultCharsetIDs}}
	t.bufs[0] = newGrid(w, h)
	t.scrollRegion = uv.Rect(0, 0, w, h)
	t.tabstops = uv.DefaultTabStops(w)
	t.dec = newGhosttyCellDecoder()

	term, err := gh.NewTerminal(
		gh.WithSize(clampU16(w), clampU16(h)),
		gh.WithMaxScrollbackLines(uint(t.scrollbackMax)),
		gh.WithMaxScrollbackBytes(uint(t.scrollbackMax)*ghosttyScrollbackRowBudget),
		gh.WithModeDefault(gh.ModeGraphemeCluster, true),
		gh.WithContinuationMaxBytes(uint(MaxPendingInput)),
		gh.WithWritePty(func(_ *gh.Terminal, data []byte) {
			if t.kkpUnavailable.Load() {
				data = dropKittyKeyboardReplies(data)
				if len(data) == 0 {
					return
				}
			}
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
		gh.WithClipboardWrite(func(_ *gh.Terminal, w gh.ClipboardWrite) gh.ClipboardWriteReply {
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
				break
			}
			return gh.ClipboardWriteReply{Result: gh.ClipboardWriteSuccess}
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
		gh.WithXtversion(func(_ *gh.Terminal) string { return XTVersionName() }),
		gh.WithSizeReport(func(_ *gh.Terminal) (gh.SizeReportSize, bool) {
			cw, ch := cellSizeOr(t.cellW, t.cellH)
			return gh.SizeReportSize{
				Rows:       clampU16(t.height),
				Columns:    clampU16(t.width),
				CellWidth:  uint32(cw),
				CellHeight: uint32(ch),
			}, true
		}),
		gh.WithDeviceAttributes(func(_ *gh.Terminal) (gh.DeviceAttributes, bool) {
			return ghosttyDeviceAttributes(t.sixelOn()), true
		}),
	)
	if err != nil {
		panic("vt: libghostty terminal construction failed: " + err.Error())
	}
	t.term = term

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
	defaultCellWidth   = FallbackCellWidth
	defaultCellHeight  = FallbackCellHeight
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

func (t *GhosttyTerminal) queue(f func(cb Callbacks)) {
	cb := t.cb
	t.cbq = append(t.cbq, func() { f(cb) })
}

func (t *GhosttyTerminal) drain(q []func()) {
	for _, f := range q {
		f()
	}
}

func (t *GhosttyTerminal) takeQueue() []func() {
	q := t.cbq
	t.cbq = nil
	return q
}

func (t *GhosttyTerminal) forward(p []byte) {
	if t.closed.Load() {
		return
	}
	t.term.VTWrite(p)
	t.gridStale = true
	t.scrollGeneration++
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

// TakeResponses removes and returns the queued query responses without
// blocking, or nil when there are none or the terminal is closed.
func (t *GhosttyTerminal) TakeResponses() []byte {
	if t.closed.Load() {
		return nil
	}
	return t.pipe.take()
}

// OnResponse sets a function called each time a response is queued, on the
// goroutine that queued it. It must not block. nil clears it.
func (t *GhosttyTerminal) OnResponse(fn func()) { t.pipe.setNotify(fn) }

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
	t.dropAltHistoryLocked()
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
	if width == t.width && height == t.height {
		t.mu.Unlock()
		return
	}
	t.flushRestoreLocked()
	if width != t.width {
		t.tabstops = uv.DefaultTabStops(width)
	}
	t.width, t.height = width, height
	_ = t.term.Resize(clampU16(width), clampU16(height), uint32(t.cellW), uint32(t.cellH))
	t.bufs[0].Resize(width, height)
	if t.bufs[1] != nil {
		t.bufs[1].Resize(width, height)
	}
	t.scrollRegion = uv.Rect(0, 0, width, height)
	t.gridStale = true
	t.scrollGeneration++
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
	if t.closed.Load() || (width == t.cellW && height == t.cellH) {
		return
	}
	t.cellW, t.cellH = width, height
	_ = t.term.Resize(clampU16(t.width), clampU16(t.height), uint32(width), uint32(height))
}

// Width implements [Terminal.Width].
func (t *GhosttyTerminal) Width() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.width
}

// Height implements [Terminal.Height].
func (t *GhosttyTerminal) Height() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.height
}

// SetCallbacks implements [Terminal.SetCallbacks].
func (t *GhosttyTerminal) SetCallbacks(cb Callbacks) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cb = cb
}

// GetCallbacks implements [Terminal.GetCallbacks].
func (t *GhosttyTerminal) GetCallbacks() Callbacks {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cb
}

// SetScreenClearFunc implements [Terminal.SetScreenClearFunc].
func (t *GhosttyTerminal) SetScreenClearFunc(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cb.ScreenClear = f
}

func (t *GhosttyTerminal) refreshCachesLocked() {
	if t.closed.Load() {
		return
	}
	isAlt := t.cachedAltScreen.Load()
	if t.modesDirty {
		t.modesDirty = false
		x10, _ := t.term.Mode(gh.ModeMouseEventX10)
		normal, _ := t.term.Mode(gh.ModeMouseEventNormal)
		button, _ := t.term.Mode(gh.ModeMouseEventButton)
		any, _ := t.term.Mode(gh.ModeMouseEventAny)
		t.cachedHasMouse.Store(x10 || normal || button || any)
		t.cachedAllMotion.Store(any)
		t.cachedCellMotion.Store(button)
		sgr, _ := t.term.Mode(gh.ModeMouseFormatSGR)
		px, _ := t.term.Mode(gh.ModeMouseFormatSGRPixels)
		t.cachedMouseSGR.Store(sgr)
		t.cachedMousePx.Store(px)
		utf8Mouse, _ := t.term.Mode(gh.ModeMouseFormatUTF8)
		urxvt, _ := t.term.Mode(gh.ModeMouseFormatURxvt)
		t.cachedMouseUTF8.Store(utf8Mouse)
		t.cachedMouseURXVT.Store(urxvt)
		t.cachedMouseX10.Store(x10 && !normal && !button && !any)

		alt47, _ := t.term.Mode(gh.ModeAltScreenLegacy)
		alt1047, _ := t.term.Mode(gh.ModeAltScreen)
		alt1049, _ := t.term.Mode(gh.ModeAltScreenSaveCursorClearEnter)
		isAlt = alt47 || alt1047 || alt1049
		t.cachedAltScreen.Store(isAlt)
		sync, _ := t.term.Mode(gh.ModeSynchronizedOutput)
		t.noteSyncOutput(sync)
	}
	if !isAlt {
		if t.pendingMainSbClear {
			t.pendingMainSbClear = false
			t.term.VTWrite([]byte("\x1b[3J"))
			t.scrollGeneration++
			t.dropAltHistoryLocked()
		}
		if n, err := t.term.ScrollbackRows(); err == nil {
			t.mainSbLen = int(n)
		}
	}

	t.cachedKittyFlags.Store(int32(t.kittyKbd.CurrentFlags()))
}

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

// HasPixelMouseMode reports whether the guest tracks the mouse in SGR-pixel
// mode (1016).
func (t *GhosttyTerminal) HasPixelMouseMode() bool {
	t.ensureRestored()
	return t.cachedHasMouse.Load() && t.cachedMousePx.Load()
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

// SyncUpdate reports an open synchronized update and the serial naming it,
// with the pure emulator's meaning.
func (t *GhosttyTerminal) SyncUpdate() (open bool, serial uint64) {
	return t.IsSyncActive(), t.syncOpens.Load()
}

func (t *GhosttyTerminal) noteSyncOutput(sync bool) {
	if sync && !t.cachedSyncOutput.Load() {
		t.syncSetAtNanos.Store(time.Now().UnixNano())
		t.syncOpens.Add(1)
	}
	t.cachedSyncOutput.Store(sync)
}

// KittyKeyboardFlags returns the current kitty keyboard flags.
func (t *GhosttyTerminal) KittyKeyboardFlags() int {
	t.ensureRestored()
	return int(t.cachedKittyFlags.Load())
}

// ModifyOtherKeys returns the modifyOtherKeys level the guest set.
func (t *GhosttyTerminal) ModifyOtherKeys() int {
	t.ensureRestored()
	return int(t.modifyOtherKeys.Load())
}

// RestoreModifyOtherKeys puts back a level saved from another emulator. The
// library is told too, so it answers a query with the same level.
func (t *GhosttyTerminal) RestoreModifyOtherKeys(level int) {
	if level < 0 || level > 2 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.modifyOtherKeys = level
	r.hasModifyOtherKeys = true
}

// DisableKittyKeyboardProtocol implements
// [Terminal.DisableKittyKeyboardProtocol].
func (t *GhosttyTerminal) DisableKittyKeyboardProtocol() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.kkpUnavailable.Store(true)
	t.kittyKbd.Reset()
	t.cachedKittyFlags.Store(0)
}

// KittyKeyboardUnavailable implements [Terminal.KittyKeyboardUnavailable].
func (t *GhosttyTerminal) KittyKeyboardUnavailable() bool { return t.kkpUnavailable.Load() }

func dropKittyKeyboardReplies(data []byte) []byte {
	out := data[:0:0]
	for i := 0; i < len(data); {
		if j := kittyKeyboardReplyEnd(data, i); j > 0 {
			i = j
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out
}

func kittyKeyboardReplyEnd(data []byte, i int) int {
	if i+3 >= len(data) || data[i] != 0x1b || data[i+1] != '[' || data[i+2] != '?' {
		return 0
	}
	j := i + 3
	for j < len(data) && data[j] >= '0' && data[j] <= '9' {
		j++
	}
	if j == i+3 || j >= len(data) || data[j] != 'u' {
		return 0
	}
	return j + 1
}

// KittyKeyboardStack implements [Terminal.KittyKeyboardStack].
func (t *GhosttyTerminal) KittyKeyboardStack() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int(nil), t.kittyKbd.Stack()...)
}

// KittyKeyboardMainStack implements [Terminal.KittyKeyboardMainStack].
func (t *GhosttyTerminal) KittyKeyboardMainStack() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int(nil), t.kittyKbd.MainStack()...)
}

// ApplicationCursorKeys implements [Terminal.ApplicationCursorKeys].
func (t *GhosttyTerminal) ApplicationCursorKeys() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return false
	}
	v, _ := t.term.Mode(gh.ModeCursorKeys)
	return v
}

// BracketedPasteEnabled implements [Terminal.BracketedPasteEnabled].
func (t *GhosttyTerminal) BracketedPasteEnabled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return false
	}
	v, _ := t.term.Mode(gh.ModeBracketedPaste)
	return v
}

// FocusReportingEnabled reports whether the guest has focus event reporting
// (DECSET 1004) on.
func (t *GhosttyTerminal) FocusReportingEnabled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return false
	}
	v, _ := t.term.Mode(gh.ModeFocusEvent)
	return v
}

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

func ghosttyDeviceAttributes(sixel bool) gh.DeviceAttributes {
	var da gh.DeviceAttributes
	features := DeviceAttributes(sixel)
	da.Primary.ConformanceLevel = uint16(features[0])
	for i, f := range features[1:] {
		da.Primary.Features[i] = uint16(f)
	}
	da.Primary.NumFeatures = len(features) - 1
	return da
}
