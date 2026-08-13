package app

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The theme's own accent slots, and the quick-pick row's whole vocabulary. They
// are also what a stored accent index means, so an accents file written before
// the picker reached the full colour space keeps meaning what it meant. Eight
// bright slots first, then seven normal ones; ANSI black is skipped, since an
// accent nobody can see is not a choice, which is why this is fifteen and not
// sixteen.
const accentSwatchCount = 15

// accentBrightCount is how many of the slots are the bright half.
const accentBrightCount = 8

// accentSlotNames names the slots, in the words set-session-accent already
// takes: the picker prints them and the daemon is sent them, so the label on
// screen and the value stored are the same string. ParseAccent ignores the
// space, so "bright cyan" reads back as the slot it was drawn for.
var accentSlotNames = [accentSwatchCount]string{
	"bright black", "bright red", "bright green", "bright yellow",
	"bright blue", "bright purple", "bright cyan", "bright white",
	"red", "green", "yellow", "blue", "purple", "cyan", "white",
}

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
	// The theme's own colours come first, drawn first and reached first, because
	// picking one by name is the easy answer and the whole colour space below it
	// is the expert one.
	accentFocusANSI accentFocus = iota
	accentFocusHue
	accentFocusGrid
	accentFocusHex
	// The sliders, in channel order, so accentChannel and the focus stop it owns
	// are one addition apart.
	accentFocusR
	accentFocusG
	accentFocusB
	accentFocusS
	accentFocusL
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
	Hue float64 // 0..360, the hue the shades grid holds
	// Sat and Light are the continuous 0..1 model the S and L sliders edit. The
	// grid is a coarse projection of the same two numbers (9.1 % of saturation a
	// column, 12.1 % of lightness a row), so the cursor shows the nearest cell
	// while the sliders keep the value between cells. One model, two views,
	// which is why they cannot disagree.
	Sat, Light float64
	Col, Row   int // cursor in the grid: saturation across, lightness down
	Cur        color.RGBA
	Base       color.RGBA
	Hex        string // the hex field's buffer
	Harmony    int    // which chip the harmony cursor is on
	// Slot is the theme slot the current colour was picked as, or -1 when it is
	// a literal. Cur is the same colour either way; this is what says whether it
	// will follow the next theme. Set to -1 by every control that produces a
	// colour of its own, so a slot cannot outlive the pick that chose it.
	Slot    int
	Focus   accentFocus
	Prev    Accent // the colour the target was wearing when the picker opened
	HadPrev bool
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
	// hex line and the harmony line, plus the dialog's two border rows, plus the
	// slot rows where they are drawn.
	furniture := 7
	if m.accentSlotsShown() {
		furniture += accentSlotRows
	}
	if m.accentSlidersShown() {
		furniture += accentSliderRows
	}
	return max(inner-2, 1), clampInt(m.GetRenderHeight()-furniture, 1, accentGridMaxRows)
}

// accentSliderRows is how many rows the slider block occupies: one per channel
// plus the blank that separates the bytes from the two that move the whole
// colour.
const accentSliderRows = int(accentChanCount) + 1

// accentSliderMinHeight is the screen height the slider block needs. It is the
// first of the picker's controls to go on a short screen: it is the tallest
// thing in the dialog, and the grid and the hex field still reach every colour
// without it.
const accentSliderMinHeight = 22

// accentSlidersShown reports whether the screen has room for the slider block.
func (m *OS) accentSlidersShown() bool { return m.GetRenderHeight() >= accentSliderMinHeight }

// accentSlotRows is how many rows the theme's colours are drawn on.
const accentSlotRows = 2

// accentSlotsShown reports whether the screen has room for the quick-pick rows.
// They are the first thing dropped on a screen too short for everything: the
// readout, the hex field and one row of the grid are what the picker cannot work
// without, and the same colours are still reachable by name through the hex
// field and by eye through the grid.
func (m *OS) accentSlotsShown() bool {
	// One grid row, the rest of the furniture, and the slot rows on top.
	const need = 1 + 7 + accentSlotRows
	return m.GetRenderHeight() >= need
}

// accentGridLightRange is the lightness the grid's top and bottom rows carry.
// It stops short of white and black: those are one colour each at every
// saturation, so a row of either would be a row that says nothing.
const (
	accentLightTop    = 0.95
	accentLightBottom = 0.10
)

// accentCellSL is the saturation and lightness shades-grid cell (col, row)
// stands for: saturation runs left to right, lightness top to bottom. The cell
// is those two numbers, and the colour below is what they make at a hue, which
// is why picking a cell sets the model rather than reading it back off the
// eight-bit colour the cell was painted with.
func accentCellSL(col, row, cols, rows int) (sat, light float64) {
	sat = 1.0
	if cols > 1 {
		sat = float64(col) / float64(cols-1)
	}
	light = accentLightTop
	if rows > 1 {
		light = accentLightTop - float64(row)*(accentLightTop-accentLightBottom)/float64(rows-1)
	}
	return sat, light
}

// accentCellColor is the colour of shades-grid cell (col, row) at the held hue.
func accentCellColor(hue float64, col, row, cols, rows int) color.RGBA {
	sat, light := accentCellSL(col, row, cols, rows)
	return hslToRGB(hue, sat, light)
}

// accentCellForSL is the grid cell nearest to a saturation and lightness, the
// inverse of accentCellSL. The sliders use this rather than accentCellFor
// because they hold the two numbers exactly: going by way of the colour would
// put the cursor a cell off near white, where eight bits a channel no longer
// tell saturation and its neighbours apart.
func accentCellForSL(sat, light float64, cols, rows int) (col, row int) {
	if cols > 1 {
		col = clampInt(int(sat*float64(cols-1)+0.5), 0, cols-1)
	}
	if rows > 1 {
		step := (accentLightTop - accentLightBottom) / float64(rows-1)
		row = clampInt(int((accentLightTop-light)/step+0.5), 0, rows-1)
	}
	return col, row
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
// harmony chips hang off, the text in the hex field, and the saturation and
// lightness the sliders show. The colour is a literal: every control but the
// slot row produces one.
func (s *accentPickerState) setCur(c color.RGBA) {
	s.takeColor(c)
	// A grey reports no hue, so the held one stands; saturation and lightness it
	// does report, and zero saturation is the true answer for a grey.
	_, s.Sat, s.Light = rgbToHSL(c)
}

// takeColor installs the colour without touching the HSL model, for the callers
// that already hold the exact saturation and lightness it was built from. Going
// through rgbToHSL there would be a round trip through eight bits a channel for
// numbers that were never rounded.
func (s *accentPickerState) takeColor(c color.RGBA) {
	s.Cur, s.Base = c, c
	s.Hex = hexString(c)
	s.Slot = -1
}

// takeHarmony selects a harmony chip. It moves the colour without moving Base,
// so the chips stay where they are while the cursor walks them.
func (s *accentPickerState) takeHarmony(i int) {
	s.Harmony = clampInt(i, 0, accentHarmonyCount-1)
	c := s.harmonyColor(s.Harmony)
	s.Cur = c
	s.Hex = hexString(c)
	s.Slot = -1
	_, s.Sat, s.Light = rgbToHSL(c)
}

// accentPickerSetHSL moves the picker's saturation and lightness and rebuilds
// the colour from them at the held hue. The sliders write the model directly
// rather than editing the colour and reading the model back off it, so a step
// out and back is not a round trip through eight bits a channel.
func (m *OS) accentPickerSetHSL(sat, light float64) {
	s := &m.AccentPicker
	s.Sat, s.Light = clamp01(sat), clamp01(light)
	c := hslToRGB(s.Hue, s.Sat, s.Light)
	s.takeColor(c)
	// The grid cursor is the coarse view of the same two numbers.
	cols, rows := m.accentGridSize()
	s.Col, s.Row = accentCellForSL(s.Sat, s.Light, cols, rows)
}

// clamp01 holds a fraction in range.
func clamp01(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

// accentPickerAdopt takes a literal colour from a control that names one
// outright rather than by cell, and walks the grid cursor and the held hue to
// the nearest cell so every part of the dialog is pointing at the same colour.
func (m *OS) accentPickerAdopt(c color.RGBA) {
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	s.setCur(c)
	s.Hue, s.Col, s.Row = accentCellFor(c, s.Hue, cols, rows)
}

// selection is the accent the picker would store: the slot when the user picked
// one by name, and the colour itself otherwise. Keeping the slot is what lets a
// pane pinned to "cyan" follow the user to another theme.
func (s *accentPickerState) selection() Accent {
	if s.Slot >= 0 {
		return SlotAccent(s.Slot)
	}
	return RGBAccent(s.Cur)
}

// accentPayload is how an accent is written down for the daemon, which records
// the string verbatim: a slot goes over as its name so every client resolves it
// against its own theme, and a literal as its hex.
func accentPayload(a Accent) string {
	if a.IsSlot() {
		return accentSlotNames[clampInt(a.Slot, 0, accentSwatchCount-1)]
	}
	return a.Hex()
}

// accentNearestSlot is the slot closest to a colour, which is where the ANSI
// cursor lands when the keyboard arrives from a colour no slot names. The grid
// does the same thing with a typed hex.
func accentNearestSlot(c color.RGBA) int {
	best, bestDist := 0, math.MaxFloat64
	for i := range accentSwatchCount {
		s := SlotAccent(i).RGB()
		dr, dg, db := float64(s.R)-float64(c.R), float64(s.G)-float64(c.G), float64(s.B)-float64(c.B)
		if d := dr*dr + dg*dg + db*db; d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
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

	// A colour that is a slot opens on that slot, cursor and keyboard both: the
	// target is wearing the name, not the hex, and the picker showing a grid
	// position instead would hide the one thing it could say about it.
	if src != accentSourceNone && prev.IsSlot() && m.accentSlotsShown() {
		m.AccentPickerSlot(prev.Slot)
	}
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
	sel := s.selection()
	unchanged := s.HadPrev && sel == s.Prev
	defer m.CloseAccentPicker()

	if unchanged {
		return nil
	}
	if target == AccentTargetSession {
		return m.setSessionAccentCmd(id, accentPayload(sel))
	}
	m.SetWindowAccent(id, sel)
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

// accentFocusShown reports whether a focus stop is drawn on this screen. The
// sliders and the slot rows both come and go with the screen's height, and a
// keyboard stop on a control nobody can see is a control the user has lost.
func (m *OS) accentFocusShown(f accentFocus) bool {
	if f == accentFocusANSI {
		return m.accentSlotsShown()
	}
	if _, ok := f.sliderChannel(); ok {
		return m.accentSlidersShown()
	}
	return true
}

// AccentPickerFocus moves the keyboard between the picker's controls, wrapping
// in both directions. Landing on the harmony row takes its chip as the current
// colour; leaving it hands the colour back to the grid cursor, so the preview
// always shows the thing the focused control is pointing at. A slider is
// already showing the held colour, so landing on one changes nothing.
func (m *OS) AccentPickerFocus(delta int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	n := int(accentFocusCount)
	// Tab must never land on a control that is not on screen, and a short screen
	// can have dropped a run of them.
	for range n {
		s.Focus = accentFocus(((int(s.Focus)+delta)%n + n) % n)
		if m.accentFocusShown(s.Focus) {
			break
		}
	}
	switch s.Focus {
	case accentFocusHarmony:
		s.takeHarmony(s.Harmony)
	case accentFocusANSI:
		slot := s.Slot
		if slot < 0 {
			slot = accentNearestSlot(s.Cur)
		}
		m.AccentPickerSlot(slot)
	case accentFocusGrid, accentFocusHue:
		// Focus names the selection, so arriving at the grid takes the cell the
		// cursor is on, at that cell's own saturation and lightness.
		cols, rows := m.accentGridSize()
		s.Sat, s.Light = accentCellSL(s.Col, s.Row, cols, rows)
		s.takeColor(hslToRGB(s.Hue, s.Sat, s.Light))
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
	if ch, ok := m.AccentPicker.Focus.sliderChannel(); ok {
		// Right and up raise the channel, left and down lower it, so the number
		// moves the way the thumb does and the way a column of them reads.
		m.AccentPickerSliderStep(ch, dx-dy)
		return
	}
	switch m.AccentPicker.Focus {
	case accentFocusHue:
		m.AccentPickerMoveHue(dx + dy)
	case accentFocusHarmony:
		m.AccentPickerMoveHarmony(dx + dy)
	case accentFocusANSI:
		m.AccentPickerMoveSlot(dx, dy)
	default:
		m.AccentPickerMoveCell(dx, dy)
	}
}

// AccentPickerMoveShift is the shifted arrow: the same direction at the other
// granularity. On a slider it is the eyeballing jump the one-unit step is too
// fine for; nothing else has a second granularity to offer yet.
func (m *OS) AccentPickerMoveShift(dx, dy int) {
	if !m.ShowAccentPicker {
		return
	}
	if ch, ok := m.AccentPicker.Focus.sliderChannel(); ok {
		m.AccentPickerSliderStep(ch, (dx-dy)*ch.coarse())
	}
}

// AccentPickerMoveSlot walks the theme's colours. Left and right stay in the row
// they started in, because the two rows are two rows on screen; up and down step
// between a bright colour and the normal one drawn under it, which is the pairing
// the layout is for. Bright black has nothing under it and stays where it is.
func (m *OS) AccentPickerMoveSlot(dx, dy int) {
	if !m.ShowAccentPicker {
		return
	}
	i := m.AccentPicker.Slot
	if i < 0 {
		i = accentNearestSlot(m.AccentPicker.Cur)
	}
	switch {
	case dy > 0 && i >= 1 && i < accentBrightCount:
		i += accentBrightCount - 1
	case dy < 0 && i >= accentBrightCount:
		i -= accentBrightCount - 1
	case i < accentBrightCount:
		i = clampInt(i+dx, 0, accentBrightCount-1)
	default:
		i = clampInt(i+dx, accentBrightCount, accentSwatchCount-1)
	}
	m.AccentPickerSlot(i)
}

// AccentPickerSlot puts the cursor on one of the theme's colours and takes it as
// the slot it is, not as the colour that slot resolves to today.
func (m *OS) AccentPickerSlot(i int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	s.Focus = accentFocusANSI
	i = clampInt(i, 0, accentSwatchCount-1)
	s.setCur(SlotAccent(i).RGB())
	s.Slot = i
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
// The cell is a saturation and a lightness, so it is those two the picker takes
// and the colour that follows from them, which is what lets the S and L sliders
// step off a cell and back onto exactly it.
func (m *OS) AccentPickerCell(col, row int) {
	if !m.ShowAccentPicker {
		return
	}
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	s.Focus = accentFocusGrid
	s.Col, s.Row = clampInt(col, 0, cols-1), clampInt(row, 0, rows-1)
	s.Sat, s.Light = accentCellSL(s.Col, s.Row, cols, rows)
	s.takeColor(hslToRGB(s.Hue, s.Sat, s.Light))
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

// AccentPickerHueCell holds a new hue, carrying the saturation and lightness
// across it. They come from the continuous model rather than from the cell the
// grid cursor is on, so turning the hue does not round a slider's work away.
func (m *OS) AccentPickerHueCell(i int) {
	if !m.ShowAccentPicker {
		return
	}
	cols, _ := m.accentGridSize()
	s := &m.AccentPicker
	s.Focus = accentFocusHue
	s.Hue = accentHueAt(clampInt(i, 0, cols-1), cols)
	m.accentPickerSetHSL(s.Sat, s.Light)
}

// AccentPickerHarmonyAt puts the harmony cursor on a chip and takes its colour.
func (m *OS) AccentPickerHarmonyAt(i int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	s.Focus = accentFocusHarmony
	s.takeHarmony(i)
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
	// Typing a hex is asking for that exact colour, so the slot goes even before
	// the buffer names one: a half-typed hex is no longer "cyan".
	s.Slot = -1
	c, ok := parseHexColor(buf)
	if !ok {
		return
	}
	cols, rows := m.accentGridSize()
	s.Cur, s.Base = c, c
	s.Hue, s.Col, s.Row = accentCellFor(c, s.Hue, cols, rows)
	_, s.Sat, s.Light = rgbToHSL(c)
}

// hexDigitsOf strips the leading hash from a hex buffer.
func hexDigitsOf(buf string) string {
	if len(buf) > 0 && buf[0] == '#' {
		return buf[1:]
	}
	return buf
}
