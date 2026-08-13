package app

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The fifteen ANSI slots the picker used to offer. They are no longer a way in:
// the picker reaches the whole colour space now. They stay because a stored
// accent index means one of them, and an accents file written before the picker
// grew must keep meaning what it meant. Eight bright slots first, then seven
// normal ones; black is skipped, since an accent nobody can see is not a choice.
const accentSwatchCount = 15

// accentBrightCount is how many of the slots are the bright half.
const accentBrightCount = 8

// accentColor resolves a legacy accent index against the live theme: the first
// eight are ANSI 8-15, the rest ANSI 1-7.
func accentColor(idx int) color.Color {
	pal := theme.GetANSIPalette()
	idx = clampInt(idx, 0, accentSwatchCount-1)
	if idx < accentBrightCount {
		return pal[accentBrightCount+idx]
	}
	return pal[idx-accentBrightCount+1]
}

// accentMark is the one-cell chip an accented row wears in its glyph column.
func accentMark() string {
	if overlay.UseASCII() {
		return "|"
	}
	return "▌"
}

// WindowAccent returns the accent a window carries, and whether it has one.
func (m *OS) WindowAccent(windowID string) (Accent, bool) {
	a, ok := m.SidebarAccents[windowID]
	return a, ok
}

// SetWindowAccent gives a window an accent and persists it with the rest of the
// sidebar's state.
func (m *OS) SetWindowAccent(windowID string, a Accent) {
	if windowID == "" {
		return
	}
	if m.SidebarAccents == nil {
		m.SidebarAccents = make(map[string]Accent, 1)
	}
	m.SidebarAccents[windowID] = a
	m.saveSidebarState()
}

// ClearWindowAccent takes a window's accent away.
func (m *OS) ClearWindowAccent(windowID string) {
	if windowID == "" {
		return
	}
	delete(m.SidebarAccents, windowID)
	m.saveSidebarState()
}

// accentFocus names the part of the picker the keyboard is driving. Tab walks
// them in this order, which is the order they are drawn in.
type accentFocus uint8

const (
	accentFocusHue accentFocus = iota
	accentFocusGrid
	accentFocusHex
	accentFocusHarmony
	accentFocusCount
)

// accentGridMaxRows caps the shades grid so the dialog stays a dialog on a tall
// screen. Lightness is the axis with the least to say per row.
const accentGridMaxRows = 8

// accentPickerState is the picker's whole model.
//
// Cur is what would be applied and what the rail previews. Base is what the
// harmony chips are computed from, and only the grid, the strip and the hex
// field move it: walking the chips has to leave the chips where they are, or
// the row slides out from under the cursor.
type accentPickerState struct {
	Hue      float64 // 0..360, the hue the shades grid holds
	Col, Row int     // cursor in the grid: saturation across, lightness down
	Cur      color.RGBA
	Base     color.RGBA
	Hex      string // the hex field's buffer
	Harmony  int    // which chip the harmony cursor is on
	Focus    accentFocus
	Prev     Accent // the colour the target was wearing when the picker opened
	HadPrev  bool
	// Src says where Prev came from. A colour the target was given and a colour
	// it derives look the same on the rail and behave differently: a pane with
	// no accent follows its session wherever that goes, a session with no accent
	// follows the arbitration, and a pinned one does neither. The picker has to
	// keep them apart and say which it is showing.
	Src accentSource
}

// accentGridSize is the shades grid's dimensions for the current screen. One
// function, read by the renderer as it draws and by the keyboard as it moves,
// so a cursor position always names a cell that exists.
func (m *OS) accentGridSize() (cols, rows int) {
	inner := overlay.DialogFitWidth(accentPickerInnerWidth, m.GetRenderWidth())
	// Body furniture around the grid: the hue strip, a rule, the now line, the
	// hex line and the harmony line, plus the dialog's two border rows.
	const furniture = 7
	return max(inner-2, 1), clampInt(m.GetRenderHeight()-furniture, 1, accentGridMaxRows)
}

// accentGridLightRange is the lightness the grid's top and bottom rows carry.
// It stops short of white and black: those are one colour each at every
// saturation, so a row of either would be a row that says nothing.
const (
	accentLightTop    = 0.95
	accentLightBottom = 0.10
)

// accentCellColor is the colour of shades-grid cell (col, row) at the held hue:
// saturation runs left to right, lightness top to bottom.
func accentCellColor(hue float64, col, row, cols, rows int) color.RGBA {
	s := 1.0
	if cols > 1 {
		s = float64(col) / float64(cols-1)
	}
	l := accentLightTop
	if rows > 1 {
		l = accentLightTop - float64(row)*(accentLightTop-accentLightBottom)/float64(rows-1)
	}
	return hslToRGB(hue, s, l)
}

// accentCellFor is the grid cell nearest to a colour, which is how a hex the
// user typed puts the cursor somewhere sensible. A grey carries no hue, so the
// hue the picker is already holding stands.
func accentCellFor(c color.RGBA, held float64, cols, rows int) (hue float64, col, row int) {
	h, s, l := rgbToHSL(c)
	hue = h
	if s == 0 {
		hue = held
	}
	if cols > 1 {
		col = clampInt(int(s*float64(cols-1)+0.5), 0, cols-1)
	}
	if rows > 1 {
		step := (accentLightTop - accentLightBottom) / float64(rows-1)
		row = clampInt(int((accentLightTop-l)/step+0.5), 0, rows-1)
	}
	return hue, col, row
}

// accentHueAt is the hue the hue strip's cell i stands for.
func accentHueAt(i, cols int) float64 {
	if cols <= 1 {
		return 0
	}
	return float64(i) * 360 / float64(cols)
}

// accentHueCell is the strip cell holding a hue, the inverse of accentHueAt.
func accentHueCell(hue float64, cols int) int {
	if cols <= 1 {
		return 0
	}
	return clampInt(int(hue*float64(cols)/360+0.5)%cols, 0, cols-1)
}

// accentHarmonyCount is how many chips the harmony row carries: the complement,
// then the two analogous neighbours.
const accentHarmonyCount = 3

// accentHarmonyRotations are the hue turns the chips apply to the base colour.
var accentHarmonyRotations = [accentHarmonyCount]float64{180, -30, 30}

// accentHarmonyColor is the harmony chip at index i for the picker's base
// colour.
func (s *accentPickerState) harmonyColor(i int) color.RGBA {
	return rotateHue(s.Base, accentHarmonyRotations[clampInt(i, 0, accentHarmonyCount-1)])
}

// setCur moves the colour the picker would apply, and with it the base the
// harmony chips hang off and the text in the hex field.
func (s *accentPickerState) setCur(c color.RGBA) {
	s.Cur, s.Base = c, c
	s.Hex = hexString(c)
}

// AccentTarget names what the picker is pointed at. There is one picker for
// both, the way there is one rename editor for a window, a session and a
// workspace: a user who has coloured a pane already knows how to colour a
// session.
type AccentTarget int

const (
	// AccentTargetWindow is a pane's own accent, held by this client.
	AccentTargetWindow AccentTarget = iota
	// AccentTargetSession is a session's accent, held by the daemon and shared
	// by every client attached to it.
	AccentTargetSession
)

// OpenAccentPicker opens the colour picker for a window, landing on the colour
// the pane is wearing on screen: its own accent, or its session's when it has
// none of its own. Seeding from the effective colour is what makes "change this
// colour" start from the colour being changed; the chrome's accent is left as
// the seed only when the pane is wearing nothing at all.
//
// Seeding from an inherited colour does not pin the pane to it. Prev and
// Inherited record where the seed came from, and nothing is written unless the
// user picks something else.
func (m *OS) OpenAccentPicker(windowID string) {
	if windowID == "" {
		return
	}
	prev, src := m.effectiveAccent(windowID, m.SessionName)
	m.openAccentPicker(AccentTargetWindow, windowID, prev, src)
}

// OpenSessionAccentPicker opens the same picker on a session, seeded the same
// way: the accent the session was given, or the colour it was assigned when it
// has none.
func (m *OS) OpenSessionAccentPicker(name string) {
	if name == "" {
		return
	}
	prev, src := m.sessionEffectiveAccent(name)
	m.openAccentPicker(AccentTargetSession, name, prev, src)
}

// openAccentPicker installs the picker on a target already resolved to a colour
// and a source. The seed is resolved before the picker is opened because an open
// picker previews over the very thing it was seeded from.
func (m *OS) openAccentPicker(target AccentTarget, id string, prev Accent, src accentSource) {
	start := toRGBA(theme.UI().Accent)
	if src != accentSourceNone {
		start = prev.RGB()
	}
	cols, rows := m.accentGridSize()
	hue, col, row := accentCellFor(start, 0, cols, rows)

	m.ShowAccentPicker = true
	m.AccentPickerTarget, m.AccentPickerTargetID = target, id
	m.AccentPicker = accentPickerState{
		Hue: hue, Col: col, Row: row, Focus: accentFocusGrid,
		Prev: prev, HadPrev: src != accentSourceNone, Src: src,
	}
	m.AccentPicker.setCur(start)
}

// CloseAccentPicker dismisses the picker, changing nothing. Cancelling needs no
// restore step: nothing is written until the picker is applied, and the rail's
// preview is derived from this state, so dropping the state is the revert.
func (m *OS) CloseAccentPicker() {
	m.ShowAccentPicker = false
	m.AccentPickerTarget, m.AccentPickerTargetID = AccentTargetWindow, ""
	m.AccentPicker = accentPickerState{}
	m.accentHits = m.accentHits[:0]
}

// accentPreview is the accent the rail draws the picker's target in while the
// picker is open, so the colour under the cursor shows on the thing being
// accented before it is applied. Derived from the picker's own state rather
// than stored beside it: one fewer thing that can disagree with what is on
// screen, and the fields it reads are in the rail's signature, so the preview
// repaints on the keystrokes that change it and on nothing else.
func (m *OS) accentPreview(target AccentTarget, id string) (Accent, bool) {
	if !m.ShowAccentPicker || id == "" {
		return Accent{}, false
	}
	if target != m.AccentPickerTarget || id != m.AccentPickerTargetID {
		return Accent{}, false
	}
	return RGBAccent(m.AccentPicker.Cur), true
}

// AccentPickerApply commits the colour under the cursor and closes the picker.
// A window's accent is this client's and is written here; a session's belongs to
// the daemon and comes back as a command, because reaching it is a blocking
// round trip that must not run on the Update goroutine.
//
// Applying the colour the target already wears writes nothing, which is what
// the picker opening on the effective colour costs: a user who opens it and
// presses enter has changed their mind about nothing, and writing the seed
// through would pin an inheriting pane to a literal colour, take a session out
// of the automatic arbitration, or freeze a theme slot to whatever hex it
// resolves to today. All three are losses the user was never told about. Moving
// anywhere first stores the colour landed on, as it always has.
func (m *OS) AccentPickerApply() tea.Cmd {
	if !m.ShowAccentPicker {
		return nil
	}
	s := &m.AccentPicker
	target, id := m.AccentPickerTarget, m.AccentPickerTargetID
	unchanged := s.HadPrev && s.Cur == s.Prev.RGB()
	defer m.CloseAccentPicker()

	if unchanged {
		return nil
	}
	if target == AccentTargetSession {
		return m.setSessionAccentCmd(id, hexString(s.Cur))
	}
	m.SetWindowAccent(id, RGBAccent(s.Cur))
	return nil
}

// AccentPickerClear takes the target's own accent away and closes the picker,
// which returns it to whatever it falls back to rather than to no colour at
// all: a pane goes back to following its session, and a session back to the
// colour it is assigned automatically. Clearing is how a pinned thing rejoins
// the scheme.
func (m *OS) AccentPickerClear() tea.Cmd {
	if !m.ShowAccentPicker {
		return nil
	}
	target, id := m.AccentPickerTarget, m.AccentPickerTargetID
	m.CloseAccentPicker()

	if target == AccentTargetSession {
		return m.setSessionAccentCmd(id, "")
	}
	m.ClearWindowAccent(id)
	return nil
}

// AccentPickerFocus moves the keyboard between the picker's four controls,
// wrapping in both directions. Landing on the harmony row takes its chip as the
// current colour; leaving it hands the colour back to the grid cursor, so the
// preview always shows the thing the focused control is pointing at.
func (m *OS) AccentPickerFocus(delta int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	n := int(accentFocusCount)
	s.Focus = accentFocus(((int(s.Focus)+delta)%n + n) % n)
	switch s.Focus {
	case accentFocusHarmony:
		s.Cur = s.harmonyColor(s.Harmony)
		s.Hex = hexString(s.Cur)
	case accentFocusGrid, accentFocusHue:
		cols, rows := m.accentGridSize()
		s.setCur(accentCellColor(s.Hue, s.Col, s.Row, cols, rows))
	}
}

// AccentPickerMove takes one step in the focused control. The keyboard sends a
// direction and the picker decides what it means: along the hue circle, across
// the shades grid, or between the harmony chips. The hex field has no caret to
// move (typing appends, backspace deletes), so a step there drives the grid and
// rewrites the field from the cell it lands on.
func (m *OS) AccentPickerMove(dx, dy int) {
	if !m.ShowAccentPicker {
		return
	}
	switch m.AccentPicker.Focus {
	case accentFocusHue:
		m.AccentPickerMoveHue(dx + dy)
	case accentFocusHarmony:
		m.AccentPickerMoveHarmony(dx + dy)
	default:
		m.AccentPickerMoveCell(dx, dy)
	}
}

// AccentPickerClearKey is the clear key. It does nothing while the hex field
// has the keyboard, where the same keystroke was meant for the buffer.
func (m *OS) AccentPickerClearKey() tea.Cmd {
	if m.ShowAccentPicker && m.AccentPicker.Focus != accentFocusHex {
		return m.AccentPickerClear()
	}
	return nil
}

// AccentPickerMoveCell moves the shades-grid cursor. The grid is clamped rather
// than wrapped: the corners are meaningful colours, and a cursor that jumped
// from the palest to the darkest row would lose the user's place.
func (m *OS) AccentPickerMoveCell(dx, dy int) {
	if !m.ShowAccentPicker {
		return
	}
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	m.AccentPickerCell(clampInt(s.Col+dx, 0, cols-1), clampInt(s.Row+dy, 0, rows-1))
}

// AccentPickerCell puts the shades-grid cursor on a cell and takes its colour.
func (m *OS) AccentPickerCell(col, row int) {
	if !m.ShowAccentPicker {
		return
	}
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	s.Focus = accentFocusGrid
	s.Col, s.Row = clampInt(col, 0, cols-1), clampInt(row, 0, rows-1)
	s.setCur(accentCellColor(s.Hue, s.Col, s.Row, cols, rows))
}

// AccentPickerMoveHue turns the held hue by whole strip cells, wrapping: the
// strip is a circle, so running off one end and coming back on the other is
// what the colour actually does.
func (m *OS) AccentPickerMoveHue(delta int) {
	if !m.ShowAccentPicker {
		return
	}
	cols, _ := m.accentGridSize()
	at := (accentHueCell(m.AccentPicker.Hue, cols) + delta%cols + cols) % cols
	m.AccentPickerHueCell(at)
}

// AccentPickerHueCell holds a new hue, keeping the grid cursor where it is so
// the same saturation and lightness carry across the change.
func (m *OS) AccentPickerHueCell(i int) {
	if !m.ShowAccentPicker {
		return
	}
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	s.Focus = accentFocusHue
	s.Hue = accentHueAt(clampInt(i, 0, cols-1), cols)
	s.setCur(accentCellColor(s.Hue, s.Col, s.Row, cols, rows))
}

// AccentPickerHarmonyAt puts the harmony cursor on a chip and takes its colour.
func (m *OS) AccentPickerHarmonyAt(i int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	s.Focus = accentFocusHarmony
	s.Harmony = clampInt(i, 0, accentHarmonyCount-1)
	s.Cur = s.harmonyColor(s.Harmony)
	s.Hex = hexString(s.Cur)
}

// AccentPickerMoveHarmony walks the harmony chips.
func (m *OS) AccentPickerMoveHarmony(delta int) {
	if !m.ShowAccentPicker {
		return
	}
	m.AccentPickerHarmonyAt(m.AccentPicker.Harmony + delta)
}

// AccentPickerFocusHex puts the keyboard in the hex field.
func (m *OS) AccentPickerFocusHex() {
	if m.ShowAccentPicker {
		m.AccentPicker.Focus = accentFocusHex
	}
}

// AccentPickerHexKey appends a character to the hex field and reports whether
// the field took it. A buffer that parses is adopted at once, which is what
// makes typing a hex converge on the same colour walking the grid reaches: the
// grid cursor and the held hue both move to the cell nearest what was typed.
func (m *OS) AccentPickerHexKey(r rune) bool {
	if !m.ShowAccentPicker {
		return false
	}
	if r == '#' {
		m.accentPickerSetHex("#")
		return true
	}
	if !isHexDigit(r) {
		return false
	}
	digits := hexDigitsOf(m.AccentPicker.Hex)
	if len(digits) >= 6 {
		digits = "" // a seventh digit starts the next colour rather than being dropped
	}
	m.accentPickerSetHex("#" + digits + string(r))
	return true
}

// AccentPickerHexBackspace deletes the last hex digit.
func (m *OS) AccentPickerHexBackspace() {
	if !m.ShowAccentPicker {
		return
	}
	digits := hexDigitsOf(m.AccentPicker.Hex)
	if digits == "" {
		return
	}
	m.accentPickerSetHex("#" + digits[:len(digits)-1])
}

// accentPickerSetHex installs a hex buffer and, when it names a colour, takes
// it: the grid cursor and the held hue move to the nearest cell so every part
// of the dialog agrees on what is selected.
func (m *OS) accentPickerSetHex(buf string) {
	s := &m.AccentPicker
	s.Focus = accentFocusHex
	s.Hex = buf
	c, ok := parseHexColor(buf)
	if !ok {
		return
	}
	cols, rows := m.accentGridSize()
	s.Cur, s.Base = c, c
	s.Hue, s.Col, s.Row = accentCellFor(c, s.Hue, cols, rows)
}

// hexDigitsOf strips the leading hash from a hex buffer.
func hexDigitsOf(buf string) string {
	if len(buf) > 0 && buf[0] == '#' {
		return buf[1:]
	}
	return buf
}
