//go:build ghostty

package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"

	"tuios.dev/progstatus"
)

func (t *GhosttyTerminal) callUnlocked(f func()) {
	q := t.takeQueue()
	t.mu.Unlock()
	defer t.mu.Lock()
	t.drain(q)
	f()
}

func (t *GhosttyTerminal) observeCtrl(b byte) {
	switch b {
	case 0x0e:
		t.gl = 1
	case 0x0f:
		t.gl = 0
	}
}

func (t *GhosttyTerminal) observeESC(inter, final byte) {
	if final == 'c' && inter == 0 {
		t.modesDirty = true
	}
	switch inter {
	case '(', ')', '*', '+':
		if final != 'B' && final != 'A' && final != '0' {
			return
		}
	}
	switch inter {
	case '(':
		t.charsetIDs[0] = final
	case ')':
		t.charsetIDs[1] = final
	case '*':
		t.charsetIDs[2] = final
	case '+':
		t.charsetIDs[3] = final
	case '#':
		if final == '8' {
			t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
		}
	case 0:
		switch final {
		case 'c':
			t.cachedAltScreen.Store(false)
			t.resetShadowState()
			t.queue(func(cb Callbacks) {
				if cb.ProgramStatus != nil {
					cb.ProgramStatus(progstatus.Event{Reset: true})
				}
			})
		case 'H':
			t.setTabStopLocked()
		case '7':
			t.saveCursorShadowLocked()
		case '8':
			t.restoreCursorShadowLocked(t.liveScreenLocked())
		case 'V':
			t.penProtected = true
		case 'W':
			t.penProtected = false
		case 'n':
			t.gl = 2
		case 'o':
			t.gl = 3
		case '~':
			t.gr = 1
		case '}':
			t.gr = 2
		case '|':
			t.gr = 3
		}
	}
}

func (t *GhosttyTerminal) resetShadowState() {
	t.charsetIDs = defaultCharsetIDs
	t.gl, t.gr = 0, 0
	t.savedCur = [2]SavedCursor{{Charsets: defaultCharsetIDs}, {Charsets: defaultCharsetIDs}}
	t.penProtected = false
	t.scanner.lastPrint = 0
	t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	t.savedLRMM = false
	t.kittyKbd.Reset()
	t.modifyOtherKeys.Store(0)
	t.semanticMarkers.Clear()
	t.tabstops = uv.DefaultTabStops(t.width)
	t.iconName = ""
	t.titleStack = nil
}

func (t *GhosttyTerminal) softResetShadowLocked() {
	t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	t.charsetIDs = defaultCharsetIDs
	t.gl, t.gr = 0, 0
	t.penProtected = false
	t.savedCur[t.liveScreenLocked()] = SavedCursor{Charsets: defaultCharsetIDs}
	t.modifyOtherKeys.Store(0)
}

func (t *GhosttyTerminal) observeCSI(prefix, inter, final byte, params []byte) {
	if final == 'h' || final == 'l' || (final == 'p' && inter == '!') || (prefix == '?' && (final == 'r' || final == 's')) {
		t.modesDirty = true
	}
	switch {
	case final == 'r' && prefix == 0 && inter == 0:
		if csiParamCount(params) > 2 {
			return
		}
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
		if t.closed.Load() {
			return
		}
		t.scanner.flushOut()
		on, _ := t.term.Mode(gh.ModeEnableLeftAndRightMargin)
		if !on {
			t.saveCursorShadowLocked()
		}
		if on && csiParamCount(params) <= 2 {
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
	case final == 's' && prefix == '?' && inter == 0:
		if csiHasParam(params, 69) && !t.closed.Load() {
			t.scanner.flushOut()
			t.savedLRMM, _ = t.term.Mode(gh.ModeEnableLeftAndRightMargin)
		}
	case final == 'r' && prefix == '?' && inter == 0:
		if csiHasParam(params, 69) && !t.savedLRMM {
			t.scrollRegion = uv.Rect(0, t.scrollRegion.Min.Y, t.width, t.scrollRegion.Dy())
		}
	case final == 'q' && inter == ' ' && prefix == 0:
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
	case final == 'u' && prefix == 0 && inter == 0 && len(params) == 0:
		t.restoreCursorShadowLocked(t.liveScreenLocked())
	case final == 'q' && inter == '"' && prefix == 0:
		switch v, _ := csiFirstParam(params); v {
		case 1:
			t.penProtected = true
		case 0, 2:
			t.penProtected = false
		}
	case final == 'u' && prefix == '>':
		if t.kkpUnavailable.Load() {
			break
		}
		flags := 0
		if v, ok := csiFirstParam(params); ok {
			flags = v
		}
		t.kittyKbd.Push(flags)
	case final == 'u' && prefix == '<':
		if t.kkpUnavailable.Load() {
			break
		}
		n := 1
		if v, ok := csiFirstParam(params); ok && v > 0 {
			n = v
		}
		t.kittyKbd.Pop(n)
	case final == 'u' && prefix == '=':
		if t.kkpUnavailable.Load() {
			break
		}
		flags, mode := csiTwoParams(params, 0, 1)
		t.kittyKbd.Set(flags, mode)
	case (final == 'm' || final == 'n') && prefix == '>' && inter == 0:
		t.observeModifyOtherKeys(final, params)
	case final == 'p' && inter == '!' && prefix == 0:
		t.softResetShadowLocked()
	case final == 'J' && prefix == 0 && inter == 0:
		t.observeEraseDisplay(params)
	case (final == 'g' || final == 'W') && inter == 0:
		t.observeTabControl(prefix, final, params)
	case final == 't' && prefix == 0 && inter == 0:
		t.observeTitleStack(params)
	case final == 'h' && prefix == '?', final == 'l' && prefix == '?':
		t.observeDecMode(params, final == 'h')
	case final == 'S' && prefix == '?' && inter == 0:
		t.answerSixelGraphics(params)
	case final == 'n' && prefix == '?' && inter == 0:
		t.answerDecStatusReport(params)
	}
}

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

func (t *GhosttyTerminal) answerDecStatusReport(params []byte) {
	n, ok := csiFirstParam(params)
	if !ok || t.closed.Load() {
		return
	}
	switch n {
	case 5:
		t.scanner.flushOut()
		_, _ = t.pipe.Write([]byte(ansi.DeviceStatusReport(ansi.DECStatusReport(0))))
	case 996:
		t.scanner.flushOut()
		_, _ = t.pipe.Write([]byte(colorSchemeNotice(t.reportedBackgroundLocked())))
	}
}

func (t *GhosttyTerminal) observeDecMode(params []byte, set bool) {
	mode40 := 0
	for _, part := range bytes.Split(params, []byte{';'}) {
		n, ok := atoiBytes(part)
		if !ok {
			continue
		}
		switch n {
		case 47, 1047, 1049:
			if n == 1049 {
				if set {
					t.saveCursorShadowLocked()
				} else {
					t.restoreCursorShadowLocked(0)
				}
			}
			if set && !t.cachedAltScreen.Load() {
				t.scanner.flushOut()
				if !t.activeAltLiveLocked() {
					if rows, err := t.term.ScrollbackRows(); err == nil {
						t.mainSbLen = int(rows)
					}
					t.gridStale = true
					t.syncLocked()
				}
			}
			t.cachedAltScreen.Store(set)
			t.kittyKbd.SetAltScreen(set)
			t.queue(func(cb Callbacks) {
				if cb.AltScreen != nil {
					cb.AltScreen(set)
				}
			})
		case 3:
			allowed := mode40 > 0
			if mode40 == 0 {
				t.scanner.flushOut()
				on, err := t.term.Mode(gh.ModeEnableMode3)
				allowed = err == nil && on
			}
			if allowed {
				t.tabstops = uv.DefaultTabStops(t.width)
			}
		case 40:
			mode40 = -1
			if set {
				mode40 = 1
			}
		case 1048:
			if set {
				t.saveCursorShadowLocked()
			} else {
				t.restoreCursorShadowLocked(t.liveScreenLocked())
			}
		case 69:
			if !set {
				t.scrollRegion = uv.Rect(0, t.scrollRegion.Min.Y, t.width, t.scrollRegion.Dy())
			}
		case 2026:
			t.noteSyncOutput(set)
		}
	}
}

func (t *GhosttyTerminal) observeEraseDisplay(params []byte) {
	t.scanner.flushOut()
	n, _ := csiFirstParam(params)
	switch n {
	case 0:
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

func (t *GhosttyTerminal) handleOSC(number int, payload []byte) bool {
	switch number {
	case 52:
		t.handleClipboardOSC(payload)
		return false
	case 66:
		t.handleTextSizingOSC(payload)
		return false
	case 99:
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
	case progstatus.Command:
		bel := t.scanner.oscBEL
		r, query, ok := parseProgramStatusOSC(payload, bel)
		switch {
		case query:
			_, _ = t.pipe.Write([]byte(programStatusReply(bel)))
		case ok:
			t.queue(func(cb Callbacks) {
				if cb.ProgramStatus != nil {
					cb.ProgramStatus(progstatus.Event{Report: r})
				}
			})
		}
		return false
	case 0, 1:
		if _, raw, ok := bytes.Cut(payload, []byte{';'}); ok {
			name := sanitiseTitle(raw)
			t.iconName = name
			explicit := number == 1
			if explicit {
				t.explicitIcon = name
			}
			t.queue(func(cb Callbacks) {
				if cb.IconName != nil {
					cb.IconName(name)
				}
				if explicit && cb.ExplicitIconName != nil {
					cb.ExplicitIconName(name)
				}
			})
		}
		return true
	case 4, 104, 10, 11, 12, 110, 111, 112:
		t.handleColorOSC(number, payload)
		return false
	default:
		if t.cb.PrivateOSC != nil && privateOSCNumber(number, payload) {
			t.privateOSC(number, payload)
		}
		return true
	}
}

func (t *GhosttyTerminal) privateOSC(number int, payload []byte) {
	data := bytes.Clone(payload)
	t.queue(func(cb Callbacks) {
		if cb.PrivateOSC != nil {
			cb.PrivateOSC(number, data)
		}
	})
}

func (t *GhosttyTerminal) handleClipboardOSC(payload []byte) {
	parts := bytes.Split(payload, []byte{';'})
	if len(parts) < 3 {
		return
	}
	selection := clipboardSelection(parts[1])
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
	cx, _ := t.term.CursorX()
	cy, _ := t.term.CursorY()
	curX, curY := int(cx), int(cy)
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
		marker.CapturedText = captureCommandText(t.semanticMarkers, ghosttyMarkReader{t}, absLine)
	}
	t.semanticMarkers.Add(marker)
	t.queue(func(cb Callbacks) {
		if cb.SemanticMark != nil {
			cb.SemanticMark(marker)
		}
	})
}

func (t *GhosttyTerminal) handleKittyAPC(payload []byte) {
	cmd, rawData, err := parseKittyAPC(payload, t.kittyHeaderOnly)
	if err != nil || cmd == nil {
		return
	}

	if cmd.PayloadErr != nil {
		if resp := KittyPayloadErrorResponse(cmd); resp != nil {
			_, _ = t.pipe.Write(resp)
		}
		if cmd.Action == KittyActionQuery {
			return
		}
	}

	if !t.kittyImages.Handle(cmd, func(b []byte) { _, _ = t.pipe.Write(b) }) {
		return
	}

	if fn := t.kittyPassthroughFunc; fn != nil {
		t.callUnlocked(func() { fn(cmd, rawData) })
		return
	}
	if cmd.Action == KittyActionQuery {
		_, _ = t.pipe.Write(BuildKittyResponse(true, cmd.ImageID, ""))
	}
}

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

func csiFirstParam(params []byte) (int, bool) {
	end := bytes.IndexAny(params, ";:")
	if end < 0 {
		end = len(params)
	}
	return atoiBytes(params[:end])
}

func csiParamCount(params []byte) int {
	if len(params) == 0 {
		return 0
	}
	return bytes.Count(params, []byte{';'}) + 1
}

func csiHasParam(params []byte, n int) bool {
	for _, part := range bytes.Split(params, []byte{';'}) {
		if v, ok := atoiBytes(part); ok && v == n {
			return true
		}
	}
	return false
}

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
