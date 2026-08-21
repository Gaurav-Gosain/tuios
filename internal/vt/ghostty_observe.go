//go:build ghostty

package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
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
func (t *GhosttyTerminal) callUnlocked(f func()) {
	t.mu.Unlock()
	defer t.mu.Lock()
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
		case 'c': // RIS
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
		t.queue(func(cb Callbacks) {
			if cb.CursorStyle != nil {
				cb.CursorStyle(CursorStyle(style), blink)
			}
		})
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
	case final == 'p' && inter == '!':
		// DECSTR resets margins and charsets among its soft-reset set.
		t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
		t.charsetIDs = defaultCharsetIDs
		t.gl, t.gr = 0, 0
	case final == 'J' && prefix == 0 && inter == 0:
		t.observeEraseDisplay(params)
	case final == 'h' && prefix == '?', final == 'l' && prefix == '?':
		t.observeDecMode(params, final == 'h')
	}
}

// observeDecMode watches DEC mode flips for the two the shadow layer acts
// on: the alt-screen callback and the kitty/sixel state pairs follow modes
// 47/1047/1049, exactly where the pure emulator fires cb.AltScreen.
func (t *GhosttyTerminal) observeDecMode(params []byte, set bool) {
	for _, part := range bytes.Split(params, []byte{';'}) {
		n, ok := atoiBytes(part)
		if !ok {
			continue
		}
		switch n {
		case 47, 1047, 1049:
			t.queue(func(cb Callbacks) {
				if cb.AltScreen != nil {
					cb.AltScreen(set)
				}
			})
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
		// ED 3 yet, so the pre-clear length is still readable.
		if t.semanticMarkers != nil {
			t.semanticMarkers.AdjustForScrollbackTrim(t.scrollbackLenLocked())
		}
		t.scrollGeneration++
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
	case 133:
		t.handleSemanticZoneOSC(payload)
		return true
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
			_, _ = t.pipe.Write([]byte("\x1b]52;" + selection + ";" + encoded + "\x1b\\"))
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
}

// handleKittyAPC runs tuios's kitty pipeline on an intercepted APC. The
// sequence never reaches libghostty: the passthrough pipeline is its only
// consumer, exactly as in the pure emulator when a passthrough func is set.
func (t *GhosttyTerminal) handleKittyAPC(payload []byte) {
	cmd, err := ParseKittyCommand(payload[1:])
	if err != nil || cmd == nil {
		return
	}
	rawData := make([]byte, len(payload)+4)
	rawData[0] = '\x1b'
	rawData[1] = '_'
	copy(rawData[2:], payload)
	rawData[len(rawData)-2] = '\x1b'
	rawData[len(rawData)-1] = '\\'

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

// handleSixelDCS mirrors the pure emulator's sixel DCS handler.
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
	absLine := t.scrollbackLenLocked() + curY
	if t.IsAltScreen() {
		absLine = curY
	}

	cellW, cellH := t.cellW, t.cellH
	rows := cmd.RowsForHeight(cellH)
	cols := cmd.ColsForWidth(cellW)

	if fn := t.sixelPassthroughFunc; fn != nil {
		t.callUnlocked(func() { fn(cmd, curX, curY, absLine) })
		if rows > 0 {
			t.reserveImageSpaceLocked(rows, cols)
		}
		return
	}

	state := t.sixelMain
	if t.IsAltScreen() {
		state = t.sixelAlt
	}
	state.AddPlacement(&SixelPlacement{
		AbsoluteLine:   absLine,
		ScreenX:        curX,
		Width:          cmd.Width,
		Height:         cmd.Height,
		Rows:           rows,
		Cols:           cols,
		Data:           cmd.Data,
		RawSequence:    cmd.RawSequence,
		AspectRatio:    cmd.AspectRatio,
		BackgroundMode: cmd.BackgroundMode,
	})
	if rows > 0 {
		t.reserveImageSpaceLocked(rows, cols)
	}
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
