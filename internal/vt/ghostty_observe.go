//go:build ghostty

package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"
)

// This file holds the scanner hooks: the sequences tuios observes or owns on
// top of libghostty. Hooks run with mu held, at a point where libghostty has
// consumed every byte preceding the sequence, so grid and cursor queries are
// consistent with the guest's view at that moment.

// callUnlocked releases mu around a passthrough invocation. The passthrough
// handlers in internal/app call straight back into the terminal (cursor
// position, scrollback length, ReserveImageSpace), which the pure emulator
// tolerates because it has no internal lock. Callers of Write serialize it,
// so dropping the lock mid-scan does not admit a second writer.
//
// Queued callbacks drain first, in order: the pure emulator fires callbacks
// inline as sequences are handled, so a callback queued earlier in this
// chunk logically precedes this hook. The alt-screen callback is the one
// that bit: it feeds the window flag the kitty pipeline stamps placements
// with, and a placement stamped with the pre-switch flag is suppressed as
// belonging to the wrong screen on every later frame.
func (t *GhosttyTerminal) callUnlocked(f func()) {
	q := t.takeQueue()
	t.mu.Unlock()
	defer t.mu.Lock()
	t.drain(q)
	f()
}

func (t *GhosttyTerminal) observeCtrl(b byte) {
	switch b {
	case 0x0e: // SO: G1 into GL
		t.gl = 1
	case 0x0f: // SI: G0 into GL
		t.gl = 0
	}
}

func (t *GhosttyTerminal) observeESC(inter, final byte) {
	switch inter {
	case '(':
		t.charsetIDs[0] = final
	case ')':
		t.charsetIDs[1] = final
	case '*':
		t.charsetIDs[2] = final
	case '+':
		t.charsetIDs[3] = final
	case 0:
		switch final {
		case 'c': // RIS resets to the main screen among everything else.
			t.cachedAltScreen.Store(false)
			t.resetShadowState()
		case '7': // DECSC saves the charset selection with the cursor
			t.savedCharsets = t.charsetIDs
			t.savedGL, t.savedGR = t.gl, t.gr
		case '8': // DECRC
			t.charsetIDs = t.savedCharsets
			t.gl, t.gr = t.savedGL, t.savedGR
		case 'n': // LS2
			t.gl = 2
		case 'o': // LS3
			t.gl = 3
		case '~': // LS1R
			t.gr = 1
		case '}': // LS2R
			t.gr = 2
		case '|': // LS3R
			t.gr = 3
		}
	}
}

// resetShadowState puts the shadow state where a hard reset puts the
// emulator. libghostty resets its own side; this covers what it does not
// expose.
func (t *GhosttyTerminal) resetShadowState() {
	t.charsetIDs = defaultCharsetIDs
	t.savedCharsets = defaultCharsetIDs
	t.gl, t.gr = 0, 0
	t.savedGL, t.savedGR = 0, 0
	t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	t.kittyKbd.Reset()
	t.modifyOtherKeys.Store(0)
	t.semanticMarkers.Clear()
}

func (t *GhosttyTerminal) observeCSI(prefix, inter, final byte, params []byte) {
	switch {
	case final == 'r' && prefix == 0 && inter == 0:
		// DECSTBM. Empty params reset to the full screen.
		top, bottom := csiTwoParams(params, 1, t.height)
		if top < 1 {
			top = 1
		}
		if bottom > t.height || bottom < 1 {
			bottom = t.height
		}
		if top < bottom {
			t.scrollRegion = uv.Rect(t.scrollRegion.Min.X, top-1, t.scrollRegion.Dx(), bottom-top+1)
		}
	case final == 's' && prefix == 0 && inter == 0:
		// DECSLRM when left/right margin mode is on; SCOSC otherwise.
		if t.closed.Load() {
			return
		}
		t.scanner.flushOut()
		if on, _ := t.term.Mode(gh.ModeLeftRightMargin); on {
			left, right := csiTwoParams(params, 1, t.width)
			if left < 1 {
				left = 1
			}
			if right > t.width || right < 1 {
				right = t.width
			}
			if left < right {
				t.scrollRegion = uv.Rect(left-1, t.scrollRegion.Min.Y, right-left+1, t.scrollRegion.Dy())
			}
		}
	case final == 'q' && inter == ' ' && prefix == 0:
		// DECSCUSR, mapped exactly as the pure emulator maps it.
		n := 1
		if v, ok := csiFirstParam(params); ok && v > 1 {
			n = v
		}
		blink := n == 0 || n%2 == 1
		style := n / 2
		if !blink {
			style--
		}
		t.cursorStyle, t.cursorSteady = CursorStyle(style), !blink
	case final == 'u' && prefix == '>':
		flags := 0
		if v, ok := csiFirstParam(params); ok {
			flags = v
		}
		t.kittyKbd.Push(flags)
	case final == 'u' && prefix == '<':
		n := 1
		if v, ok := csiFirstParam(params); ok && v > 0 {
			n = v
		}
		t.kittyKbd.Pop(n)
	case final == 'u' && prefix == '=':
		flags, mode := csiTwoParams(params, 0, 1)
		t.kittyKbd.Set(flags, mode)
	case (final == 'm' || final == 'n') && prefix == '>' && inter == 0:
		t.observeModifyOtherKeys(final, params)
	case final == 'p' && inter == '!':
		// DECSTR resets margins and charsets among its soft-reset set.
		t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
		t.charsetIDs = defaultCharsetIDs
		t.gl, t.gr = 0, 0
	case final == 'J' && prefix == 0 && inter == 0:
		t.observeEraseDisplay(params)
	case final == 'h' && prefix == '?', final == 'l' && prefix == '?':
		t.observeDecMode(params, final == 'h')
	case final == 'S' && prefix == '?' && inter == 0:
		t.answerSixelGraphics(params)
	case final == 'n' && prefix == '?' && inter == 0:
		t.answerDecStatusReport(params)
	}
}

// observeModifyOtherKeys follows XTMODKEYS for the input path, as the pure
// emulator reads it: CSI > 4 ; n m sets the level, CSI > 4 m and CSI > 4 n
// turn it off, and CSI > m resets every resource. The library sees the same
// bytes and keeps its own copy.
func (t *GhosttyTerminal) observeModifyOtherKeys(final byte, params []byte) {
	if len(params) == 0 {
		if final == 'm' {
			t.modifyOtherKeys.Store(0)
		}
		return
	}
	res, level := csiTwoParams(params, -1, 0)
	if res != modifyOtherKeysResource {
		return
	}
	if final == 'n' {
		level = 0
	}
	if level >= 0 && level <= 2 {
		t.modifyOtherKeys.Store(int32(level)) //nolint:gosec // bounded above
	}
}

// answerDecStatusReport answers the DEC private DSR queries that libghostty
// drops: CSI ? 5 n (operating status) and CSI ? 6 n (DECXCPR). The pure
// emulator answers both, and xterm does too, so a guest that probes with the
// private form would otherwise wait out its timeout on this backend only.
// libghostty still answers the ANSI forms CSI 5 n and CSI 6 n itself.
//
// Earlier bytes of the chunk flush first. That settles the cursor, and it
// puts libghostty's replies to earlier queries in the pipe ahead of this one.
func (t *GhosttyTerminal) answerDecStatusReport(params []byte) {
	n, ok := csiFirstParam(params)
	if !ok || t.closed.Load() {
		return
	}
	switch n {
	case 5:
		t.scanner.flushOut()
		_, _ = t.pipe.Write([]byte(ansi.DeviceStatusReport(ansi.DECStatusReport(0))))
	case 6:
		t.scanner.flushOut()
		x, errX := t.term.CursorX()
		y, errY := t.term.CursorY()
		if errX != nil || errY != nil {
			return
		}
		col, line := int(x), int(y)
		// Under DECOM the report is relative to the margins, the way
		// libghostty answers CSI 6 n and the pure emulator answers both.
		if on, _ := t.term.Mode(gh.ModeOrigin); on {
			col -= t.scrollRegion.Min.X
			line -= t.scrollRegion.Min.Y
		}
		// No page number, as xterm at the VT220 level this pane claims in DA1.
		_, _ = t.pipe.Write([]byte(ansi.ExtendedCursorPositionReport(line+1, col+1, 0)))
	}
}

// observeDecMode watches DEC mode flips the shadow layer acts on: the
// alt-screen callback and the kitty/sixel state pairs follow modes
// 47/1047/1049, exactly where the pure emulator fires cb.AltScreen, and the
// synchronized-output cache follows 2026.
func (t *GhosttyTerminal) observeDecMode(params []byte, set bool) {
	for _, part := range bytes.Split(params, []byte{';'}) {
		n, ok := atoiBytes(part)
		if !ok {
			continue
		}
		switch n {
		case 47, 1047, 1049:
			// The cache must flip here, mid-write: a guest that enters the
			// alternate screen and draws in the same chunk (yazi's image
			// preview) has its kitty placement computed through
			// IsAltScreen() before this Write returns, and end-of-write
			// refresh is too late. The refresh still runs afterwards and
			// stays authoritative.
			if set && !t.cachedAltScreen.Load() {
				// Entering: bank the main screen's history length while
				// the library still shows the main screen. The hook runs
				// before the switch is forwarded, so a flush here settles
				// the sink on everything that preceded this sequence.
				t.scanner.flushOut()
				if !t.activeAltLiveLocked() {
					if rows, err := t.term.ScrollbackRows(); err == nil {
						t.mainSbLen = int(rows)
					}
					// Bring the main screen's shadow up to date too.
					// It is only synced when something reads the
					// screen, and the library shows only the active
					// screen, so output that arrived with no read
					// before this switch was never in bufs[0]: the
					// shell's screen under vim read as blank to
					// MainCellAt, in a snapshot and in saved history.
					// The cache still says main here, so the sync
					// targets bufs[0].
					t.gridStale = true
					t.syncLocked()
				}
			}
			t.cachedAltScreen.Store(set)
			// Each screen has its own kitty keyboard stack, as in the
			// pure emulator and in libghostty itself.
			t.kittyKbd.SetAltScreen(set)
			t.queue(func(cb Callbacks) {
				if cb.AltScreen != nil {
					cb.AltScreen(set)
				}
			})
		case 2026:
			// Flipped mid-write for the same reason: the kitty passthrough
			// asks whether a command belongs to an open update while this
			// Write is still running, and a guest writes a whole frame,
			// 2026h through 2026l, in one chunk. The end-of-write refresh
			// reads the library's mode and agrees.
			t.noteSyncOutput(set)
		}
	}
}

// observeEraseDisplay mirrors the pure emulator's ScreenClear callback and
// marker bookkeeping around ED. The grid itself is libghostty's job.
func (t *GhosttyTerminal) observeEraseDisplay(params []byte) {
	// The queries below need the library caught up to the byte before this
	// CSI; CSI hooks do not flush by default because SGRs dominate them.
	t.scanner.flushOut()
	n, _ := csiFirstParam(params)
	switch n {
	case 0:
		// ctrl-l pattern: CUP(1,1) + ED 0 clears from the origin.
		x, y := t.cursorLocked()
		if x == 0 && y == 0 {
			t.queue(func(cb Callbacks) {
				if cb.ScreenClear != nil {
					cb.ScreenClear()
				}
			})
		}
	case 2:
		t.activeKittyState().ClearPlacements()
		if t.semanticMarkers != nil {
			t.semanticMarkers.RemoveOnScreen(t.scrollbackLenLocked())
		}
		t.queue(func(cb Callbacks) {
			if cb.ScreenClear != nil {
				cb.ScreenClear()
			}
		})
	case 3:
		// Scrollback clear: the pure emulator's ring fires a trim callback
		// that shifts markers; the library's ring cannot, so shift by the
		// history length being dropped. The library has not consumed the
		// ED 3 yet, so the pre-clear length is still readable. On the
		// alternate screen ED 3 is a no-op on both implementations: the
		// alternate screen keeps no history.
		if !t.activeAltLiveLocked() {
			if t.semanticMarkers != nil {
				t.semanticMarkers.AdjustForScrollbackTrim(t.scrollbackLenLocked())
			}
			t.mainSbLen = 0
			t.scrollGeneration++
		}
	}
}

func (t *GhosttyTerminal) activeKittyState() *KittyState {
	if t.IsAltScreen() {
		return t.kittyAlt
	}
	return t.kittyMain
}

// handleOSC routes the OSC families tuios owns. Returning true forwards the
// sequence to libghostty.
func (t *GhosttyTerminal) handleOSC(number int, payload []byte) bool {
	switch number {
	case 52:
		t.handleClipboardOSC(payload)
		// Never forwarded: libghostty's own clipboard callback would
		// otherwise fire a second ClipboardSet.
		return false
	case 66:
		t.handleTextSizingOSC(payload)
		return false
	case 99:
		// libghostty reports OSC 9 and OSC 777 through its desktop
		// notification callback but has no OSC 99, so the kitty form is
		// parsed here the way the pure emulator parses it.
		if title, body, ok := parseNotify99(payload); ok {
			t.queue(func(cb Callbacks) {
				if cb.Notify != nil {
					cb.Notify(title, body)
				}
			})
		}
		return false
	case 133:
		t.handleSemanticZoneOSC(payload)
		return true
	case 7777:
		if direction, ok := parseTuiosNavigation(payload); ok {
			t.queue(func(cb Callbacks) {
				if cb.TuiosNavigation != nil {
					cb.TuiosNavigation(direction)
				}
			})
		}
		if active, ok := parseTuiosNavigatorState(payload); ok {
			t.queue(func(cb Callbacks) {
				if cb.NvimNavigatorState != nil {
					cb.NvimNavigatorState(active)
				}
			})
		}
		return false
	case 4, 104, 10, 11, 12, 110, 111, 112:
		// Color set/query is owned here so the library does not answer
		// queries a second time.
		t.handleColorOSC(number, payload)
		return false
	default:
		return true
	}
}

// handleClipboardOSC mirrors the pure emulator's OSC 52: set fires the
// callback, query answers through the response pipe.
func (t *GhosttyTerminal) handleClipboardOSC(payload []byte) {
	parts := bytes.Split(payload, []byte{';'})
	if len(parts) < 3 {
		return
	}
	selection := string(parts[1])
	data := string(parts[2])
	if data == "?" {
		t.callUnlocked(func() {
			content := ""
			if q := t.GetCallbacks().ClipboardQuery; q != nil {
				content = q(selection)
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(content))
			_, _ = t.pipe.Write([]byte("\x1b]52;" + selection + ";" + encoded + oscReplyEnd(t.scanner.oscBEL)))
		})
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return
	}
	t.queue(func(cb Callbacks) {
		if cb.ClipboardSet != nil {
			cb.ClipboardSet(selection, string(decoded))
		}
	})
}

// handleTextSizingOSC mirrors the pure emulator's OSC 66: forward to the
// host through the callback, then blank the rows the scaled text occupies so
// the passthrough rendering is not overdrawn. The blanking is synthesized as
// erase sequences because the cells live in libghostty's grid.
func (t *GhosttyTerminal) handleTextSizingOSC(payload []byte) {
	parts := bytes.SplitN(payload, []byte{';'}, 3)
	if len(parts) < 3 || len(parts[2]) == 0 {
		return
	}
	text := parts[2]
	scale := 1
	for kv := range bytes.SplitSeq(parts[1], []byte{':'}) {
		if bytes.HasPrefix(kv, []byte("s=")) && len(kv) > 2 {
			if s := kv[2] - '0'; s >= 1 && s <= 7 {
				scale = int(s)
			}
		}
	}
	textRunes := len([]rune(string(text)))
	curX, curY := t.cursorLocked()

	if t.textSizingFunc != nil {
		var rawOSC []byte
		rawOSC = append(rawOSC, "\x1b]"...)
		rawOSC = append(rawOSC, payload...)
		rawOSC = append(rawOSC, '\a')
		fn := t.textSizingFunc
		t.callUnlocked(func() { fn(rawOSC, curX, curY, scale, textRunes) })
	}

	// Erase the rows the scaled text covers, and the wrapped command text
	// beyond it on the row above, then put the cursor back.
	var seq bytes.Buffer
	h := t.height
	scaledCols := textRunes * scale
	for row := 0; row < scale; row++ {
		y := curY + row
		if y >= h {
			break
		}
		fmt.Fprintf(&seq, "\x1b[%d;1H\x1b[2K", y+1)
	}
	if curY > 0 && scaledCols < t.width {
		fmt.Fprintf(&seq, "\x1b[%d;%dH\x1b[0K", curY, scaledCols+1)
	}
	fmt.Fprintf(&seq, "\x1b[%d;%dH", curY+1, curX+1)
	if t.closed.Load() {
		return
	}
	t.term.VTWrite(seq.Bytes())
	t.gridStale = true
}

// handleSemanticZoneOSC mirrors the pure emulator's OSC 133 marker capture.
func (t *GhosttyTerminal) handleSemanticZoneOSC(payload []byte) {
	parts := bytes.Split(payload, []byte{';'})
	if len(parts) < 2 || len(parts[1]) == 0 {
		return
	}
	subCmd := parts[1][0]
	switch subCmd {
	case 'A', 'B', 'C', 'D':
	default:
		return
	}
	curX, curY := t.cursorLocked()
	absLine := t.scrollbackLenLocked() + curY

	exitCode := -1
	if subCmd == 'D' && len(parts) >= 3 && len(parts[2]) > 0 {
		code := 0
		for _, b := range parts[2] {
			if b >= '0' && b <= '9' {
				code = code*10 + int(b-'0')
			}
		}
		exitCode = code
	}

	marker := SemanticMarker{
		Type:     SemanticMarkerType(subCmd),
		AbsLine:  absLine,
		Col:      curX,
		ExitCode: exitCode,
	}
	if subCmd == 'C' {
		if bMarker := t.semanticMarkers.Last(MarkerCommandStart); bMarker != nil {
			marker.CapturedText = extractCommandTextFrom(t.readerNoLock(), bMarker.AbsLine, bMarker.Col, absLine)
		}
	}
	t.semanticMarkers.Add(marker)
	// Queued like every other callback of this backend, so it runs in order
	// with them and never with the lock the scan holds.
	t.queue(func(cb Callbacks) {
		if cb.SemanticMark != nil {
			cb.SemanticMark(marker)
		}
	})
}

// handleKittyAPC runs tuios's kitty pipeline on an intercepted APC. The
// sequence never reaches libghostty: the passthrough pipeline is its only
// consumer, exactly as in the pure emulator when a passthrough func is set.
func (t *GhosttyTerminal) handleKittyAPC(payload []byte) {
	cmd, rawData, err := parseKittyAPC(payload, t.kittyHeaderOnly)
	if err != nil || cmd == nil {
		return
	}

	// The same rule as the pure emulator: an undecodable payload is answered
	// here, and a query is finished by that answer.
	if cmd.PayloadErr != nil {
		if resp := KittyPayloadErrorResponse(cmd); resp != nil {
			_, _ = t.pipe.Write(resp)
		}
		if cmd.Action == KittyActionQuery {
			return
		}
	}

	if fn := t.kittyPassthroughFunc; fn != nil {
		t.callUnlocked(func() { fn(cmd, rawData) })
		return
	}
	// No passthrough is a test-only situation in this backend: the daemon
	// and the app both install one before any guest runs. Queries still
	// deserve an answer so a probing guest does not hang.
	if cmd.Action == KittyActionQuery {
		_, _ = t.pipe.Write(BuildKittyResponse(true, cmd.ImageID, ""))
	}
}

// handleSixelDCS mirrors the pure emulator's sixel DCS handler: the image
// is handed to the passthrough, and its cells are marked in the grid.
func (t *GhosttyTerminal) handleSixelDCS(params, payload []byte) {
	fullData := make([]byte, 0, len(params)+1+len(payload))
	fullData = append(fullData, params...)
	fullData = append(fullData, 'q')
	fullData = append(fullData, payload...)
	cmd := ParseSixelCommand(fullData)
	if cmd == nil {
		return
	}
	curX, curY := t.cursorLocked()
	cmd.CellWidth, cmd.CellHeight = t.cellW, t.cellH
	rows, cols := SixelCells(cmd, t.cellW, t.cellH)
	var id uint32
	if fn := t.sixelPassthroughFunc; fn != nil {
		t.callUnlocked(func() { id = fn(cmd, curX, curY) })
	}
	t.placeSixelLocked(rows, cols, id)
}

// answerSixelGraphics answers XTSMGRAPHICS, which libghostty does not.
func (t *GhosttyTerminal) answerSixelGraphics(params []byte) {
	if t.closed.Load() {
		return
	}
	item, action := csiTwoParams(params, 0, 0)
	t.scanner.flushOut()
	_, _ = t.pipe.Write([]byte(sixelGraphicsReply(item, action, t.sixelOn(), t.width*t.cellW, t.height*t.cellH)))
}

func (t *GhosttyTerminal) sixelOn() bool {
	fn := t.sixelAdvertised
	return fn != nil && fn()
}

// csiFirstParam parses the first numeric CSI parameter.
func csiFirstParam(params []byte) (int, bool) {
	end := bytes.IndexAny(params, ";:")
	if end < 0 {
		end = len(params)
	}
	return atoiBytes(params[:end])
}

// csiTwoParams parses the first two numeric CSI parameters with defaults.
func csiTwoParams(params []byte, def1, def2 int) (int, int) {
	a, b := def1, def2
	parts := bytes.SplitN(params, []byte{';'}, 3)
	if len(parts) > 0 {
		if v, ok := atoiBytes(parts[0]); ok {
			a = v
		}
	}
	if len(parts) > 1 {
		if v, ok := atoiBytes(parts[1]); ok {
			b = v
		}
	}
	return a, b
}

func atoiBytes(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<24 {
			return 0, false
		}
	}
	return n, true
}
