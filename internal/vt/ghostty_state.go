//go:build ghostty

package vt

import (
	"bytes"
	"image/color"
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"
)

// ghosttyModeNumbers maps every DEC mode the library tracks to its number,
// for GetModes. The pure emulator serializes every DEC mode it has seen set
// or reset; the library cannot distinguish "explicitly reset" from "still at
// its default", so the snapshot carries current values for this fixed set.
// RestoreModes applies them all, which lands on the same state either way.
var ghosttyModeNumbers = []struct {
	num  int
	mode gh.Mode
}{
	{1, gh.ModeDECCKM},
	{3, gh.Mode132Column},
	{4, gh.ModeSlowScroll},
	{5, gh.ModeReverseColors},
	{6, gh.ModeOrigin},
	{7, gh.ModeWraparound},
	{8, gh.ModeAutorepeat},
	{9, gh.ModeX10Mouse},
	{12, gh.ModeCursorBlinking},
	{25, gh.ModeCursorVisible},
	{45, gh.ModeReverseWrap},
	{47, gh.ModeAltScreenLegacy},
	{66, gh.ModeKeypadKeys},
	{69, gh.ModeLeftRightMargin},
	{1000, gh.ModeNormalMouse},
	{1002, gh.ModeButtonMouse},
	{1003, gh.ModeAnyMouse},
	{1004, gh.ModeFocusEvent},
	{1005, gh.ModeUTF8Mouse},
	{1006, gh.ModeSGRMouse},
	{1007, gh.ModeAltScroll},
	{1015, gh.ModeURxvtMouse},
	{1016, gh.ModeSGRPixelsMouse},
	{1047, gh.ModeAltScreen},
	{1048, gh.ModeSaveCursor},
	{1049, gh.ModeAltScreenSave},
	{2004, gh.ModeBracketedPaste},
	{2027, gh.ModeGraphemeCluster},
	{2031, gh.ModeColorSchemeReport},
	{2048, gh.ModeInBandResize},
}

// GetModes serializes DEC mode state for a wire snapshot. Synchronized
// output (2026) is omitted exactly as in the pure emulator: restoring the
// transient frame gate would hold the first frame after attach.
func (t *GhosttyTerminal) GetModes() map[int]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	modes := make(map[int]bool, len(ghosttyModeNumbers))
	for _, m := range ghosttyModeNumbers {
		if v, err := t.term.Mode(m.mode); err == nil {
			modes[m.num] = v
		}
	}
	return modes
}

func (t *GhosttyTerminal) RestoreModes(modes map[int]bool) {
	if modes == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	if r.modes == nil {
		r.modes = make(map[int]bool, len(modes))
	}
	for k, v := range modes {
		r.modes[k] = v
	}
}

func (t *GhosttyTerminal) ScrollRegion() uv.Rectangle {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	return t.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
}

func (t *GhosttyTerminal) RestoreScrollRegion(r uv.Rectangle) {
	if r.Empty() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rr := t.pendingRestore()
	rr.scrollRegion = r
	rr.hasScrollRegion = true
}

func (t *GhosttyTerminal) ResetScrollRegion() {
	t.mu.Lock()
	defer t.mu.Unlock()
	rr := t.pendingRestore()
	rr.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	rr.hasScrollRegion = true
}

func (t *GhosttyTerminal) Charsets() (ids [4]byte, gl, gr int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	return t.charsetIDs, t.gl, t.gr
}

func (t *GhosttyTerminal) RestoreCharsets(ids [4]byte, gl, gr int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.charsets = ids
	r.gl, r.gr = gl, gr
	r.hasCharsets = true
}

func (t *GhosttyTerminal) ActiveScreenIsAlt() bool {
	// The library has no separate screen pointer to diverge from the mode
	// bits; RestoreAltScreenMode synthesizes the mode change itself.
	return t.IsAltScreen()
}

func (t *GhosttyTerminal) RestoreAltScreenMode(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.altScreen = enabled
	r.hasAltScreen = true
}

func (t *GhosttyTerminal) RestoreCursorPosition(x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.cursorX, r.cursorY = x, y
	r.hasCursor = true
}

func (t *GhosttyTerminal) RestoreCursorPen(pen uv.Style, link uv.Link) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.pen, r.penLink = pen, link
	r.hasPen = true
}

func (t *GhosttyTerminal) RestoreKittyKeyboardState(stack []int) {
	if len(stack) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.kittyKbdStack = append([]int(nil), stack...)
}

// SetThemeColors mirrors the pure emulator: default fg/bg/cursor and the
// sixteen ANSI slots, with nil fg and bg dropping the theme entirely.
func (t *GhosttyTerminal) SetThemeColors(fg, bg, cur color.Color, ansiPalette [16]color.Color) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.defaultFg, t.defaultBg, t.defaultCur = fg, bg, cur
	if fg == nil && bg == nil {
		t.themePal = [16]color.Color{}
	} else {
		t.themePal = ansiPalette
	}
	t.refreshPaletteClaimsLocked()
	// Converted styles depend on the theme.
	t.styleCache = make(map[uint16]uv.Style)
	t.scrollCache = make(map[int]uv.Line)
	t.scrollCacheGen = 0
	// Repaint everything on the next read: cells already on screen were
	// converted under the old theme.
	t.markAllDirtyLocked()
}

func (t *GhosttyTerminal) refreshPaletteClaimsLocked() {
	t.paletteClaimed = false
	for i := range 16 {
		if t.colors[i] != nil || t.themePal[i] != nil {
			t.paletteClaimed = true
			return
		}
	}
}

// markAllDirtyLocked forces a full shadow refresh on the next sync.
func (t *GhosttyTerminal) markAllDirtyLocked() {
	t.gridStale = true
	_ = t.rs.SetDirty(gh.RenderStateDirtyFull)
}

func (t *GhosttyTerminal) PaletteColor(i int) color.Color {
	if i < 0 || i > 15 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.paletteEntryLocked(i); c != nil {
		return c
	}
	return ansi.BasicColor(uint8(i)) //nolint:gosec // bounded above
}

func (t *GhosttyTerminal) IndexedColor(i int) color.Color {
	if i < 0 || i > 255 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.paletteEntryLocked(i); c != nil {
		return c
	}
	return ansi.IndexedColor(uint8(i)) //nolint:gosec // bounded above
}

// handleColorOSC owns OSC 4/104 (palette) and 10/11/12/110/111/112 (default
// colors), mirroring the pure emulator. These are never forwarded: the
// library would answer color queries a second time.
func (t *GhosttyTerminal) handleColorOSC(number int, payload []byte) {
	parts := bytes.Split(payload, []byte{';'})
	switch number {
	case 4:
		if len(parts) < 3 {
			return
		}
		idx, ok := parsePaletteIndex(parts[1])
		if !ok {
			return
		}
		arg := string(parts[2])
		if arg == "?" {
			c := t.paletteEntryLocked(idx)
			if c == nil {
				c = ansi.IndexedColor(uint8(idx)) //nolint:gosec // parsePaletteIndex bounds it
			}
			var xrgb ansi.XRGBColor
			xrgb.Color = c
			_, _ = t.pipe.Write([]byte("\x1b]4;" + string(parts[1]) + ";" + xrgb.String() + "\x1b\\"))
			return
		}
		if c := ansi.XParseColor(arg); c != nil {
			t.colors[idx] = c
			t.refreshPaletteClaimsLocked()
			t.styleCache = make(map[uint16]uv.Style)
		}
	case 104:
		if len(parts) < 2 {
			t.colors = [256]color.Color{}
		} else {
			for _, p := range parts[1:] {
				if idx, ok := parsePaletteIndex(p); ok {
					t.colors[idx] = nil
				}
			}
		}
		t.refreshPaletteClaimsLocked()
		t.styleCache = make(map[uint16]uv.Style)
	case 10, 11, 12, 110, 111, 112:
		t.handleDefaultColorOSC(number, parts)
	}
}

// handleDefaultColorOSC mirrors the pure emulator's OSC 10/11/12 family:
// guest-set colors override the theme defaults, "?" queries answer with
// whichever is in force.
func (t *GhosttyTerminal) handleDefaultColorOSC(number int, parts [][]byte) {
	set := func(c color.Color) {
		switch number {
		case 10, 110:
			t.guestFg = c
		case 11, 111:
			t.guestBg = c
		case 12, 112:
			t.guestCur = c
		}
	}
	switch len(parts) {
	case 1:
		set(nil)
	case 2:
		arg := string(parts[1])
		if arg == "?" {
			var c color.Color
			switch number {
			case 10:
				c = firstColor(t.guestFg, t.defaultFg, color.White)
			case 11:
				c = firstColor(t.guestBg, t.defaultBg, color.Black)
			case 12:
				c = firstColor(t.guestCur, t.defaultCur, color.White)
			default:
				return
			}
			var xrgb ansi.XRGBColor
			xrgb.Color = c
			_, _ = t.pipe.Write([]byte("\x1b]" + itoa(number) + ";" + xrgb.String() + "\x1b\\"))
			return
		}
		if c := ansi.XParseColor(arg); c != nil {
			set(c)
		}
	}
}

func firstColor(cs ...color.Color) color.Color {
	for _, c := range cs {
		if c != nil {
			return c
		}
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// SendMouse encodes and delivers a mouse event to the guest, with the same
// mode selection and motion gating as the pure emulator. DEC 1001 highlight
// tracking is not tracked by the library and is treated as absent.
func (t *GhosttyTerminal) SendMouse(m Mouse) {
	s := t.EncodeMouseEvent(m)
	if s == "" {
		return
	}
	if _, isMotion := m.(MouseMotion); isMotion {
		if !t.HasAllMotionMode() {
			if !t.HasCellMotionMode() {
				return
			}
			if m.Mouse().Button == MouseNone {
				return
			}
		}
	}
	_, _ = io.WriteString(t.pipe, s)
}

// EncodeMouseEvent encodes a mouse event for the guest, or returns "" when
// no mouse mode is on.
func (t *GhosttyTerminal) EncodeMouseEvent(m Mouse) string {
	t.mu.Lock()
	modeOn := false
	for _, gm := range []gh.Mode{gh.ModeX10Mouse, gh.ModeNormalMouse, gh.ModeButtonMouse, gh.ModeAnyMouse} {
		if v, err := t.term.Mode(gm); err == nil && v {
			modeOn = true
		}
	}
	sgr := false
	if v, err := t.term.Mode(gh.ModeSGRMouse); err == nil {
		sgr = v
	}
	pixels := false
	if v, err := t.term.Mode(gh.ModeSGRPixelsMouse); err == nil {
		pixels = v
	}
	cw, ch := t.cellW, t.cellH
	t.mu.Unlock()

	if !modeOn {
		return ""
	}
	mouse := m.Mouse()
	_, isMotion := m.(MouseMotion)
	_, isRelease := m.(MouseRelease)
	b := ansi.EncodeMouseButton(mouse.Button, isMotion,
		mouse.Mod.Contains(ModShift),
		mouse.Mod.Contains(ModAlt),
		mouse.Mod.Contains(ModCtrl))
	if pixels {
		return ansi.MouseSgr(b, mouse.X*cw+cw/2, mouse.Y*ch+ch/2, isRelease)
	}
	if sgr {
		return ansi.MouseSgr(b, mouse.X, mouse.Y, isRelease)
	}
	return ansi.MouseX10(b, mouse.X, mouse.Y)
}

func (t *GhosttyTerminal) SetKittyPassthroughFunc(fn func(cmd *KittyCommand, rawData []byte)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.kittyPassthroughFunc = fn
}

func (t *GhosttyTerminal) SetSixelPassthroughFunc(fn func(cmd *SixelCommand, cursorX, cursorY, absLine int)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sixelPassthroughFunc = fn
}

func (t *GhosttyTerminal) SetTextSizingFunc(fn func(rawOSC []byte, cursorX, cursorY, scale, textLen int)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.textSizingFunc = fn
}

func (t *GhosttyTerminal) KittyMainState() *KittyState { return t.kittyMain }
func (t *GhosttyTerminal) KittyAltState() *KittyState  { return t.kittyAlt }

func (t *GhosttyTerminal) SemanticMarkers() *SemanticMarkerList {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.semanticMarkers
}

// penStyleSequence renders a uv style as the SGR sequence that reproduces
// it. uv.Style.String is exactly that encoding.
func penStyleSequence(s *uv.Style) string {
	if s.IsZero() {
		return ""
	}
	return s.String()
}
