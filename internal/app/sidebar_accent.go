package app

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const accentSwatchCount = 15

const accentBrightCount = 8

var accentSlotNames = [accentSwatchCount]string{
	"bright black", "bright red", "bright green", "bright yellow",
	"bright blue", "bright purple", "bright cyan", "bright white",
	"red", "green", "yellow", "blue", "purple", "cyan", "white",
}

func accentColor(idx int) color.Color {
	pal := theme.GetANSIPalette()
	idx = clampInt(idx, 0, accentSwatchCount-1)
	if idx < accentBrightCount {
		return pal[accentBrightCount+idx]
	}
	return pal[idx-accentBrightCount+1]
}

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

type accentFocus uint8

const (
	accentFocusNamed accentFocus = iota
	accentFocusANSI
	accentFocusHue
	accentFocusGrid
	accentFocusHex
	accentFocusR
	accentFocusG
	accentFocusB
	accentFocusS
	accentFocusL
	accentFocusHarmony
	accentFocusCount
)

const accentGridMaxRows = 8

type accentPickerState struct {
	Hue        float64
	Sat, Light float64
	Col, Row   int
	Cur        color.RGBA
	Base       color.RGBA
	Hex        string
	Harmony    int
	Slot       int
	Focus      accentFocus
	Prev       Accent
	HadPrev    bool
	Named      string
	NamedOpts  []string
	SrcWord    string
	Src        accentSource
}

type accentLayout uint8

const (
	accentLayoutCompact accentLayout = iota
	accentLayoutStacked
	accentLayoutWide
)

const (
	accentWideLeft          = 38
	accentWideRight         = 32
	accentWideInner         = accentWideLeft + 1 + accentWideRight
	accentStackedInnerWidth = accentWideLeft
)

const accentSlotRows = 2

const accentSliderRows = int(accentChanCount) + 1

const accentSliderMinHeight = 22

const accentWideSlotsMinHeight = 16

func accentWideRightRows(blanks bool, chipRows int) int {
	n := 1 + accentSliderRows + 1 + chipRows
	if blanks {
		n += 3
	}
	return n
}

const (
	accentWideChipCols     = 8
	accentStackedChipCols  = 12
	accentChipWidth        = 3
	accentCompactChipWidth = 4
)

type accentLayoutPlan struct {
	Mode         accentLayout
	Inner        int
	ColInner     int
	Slots        bool
	Sliders      bool
	Blanks       bool
	HueCells     int
	GridCols     int
	GridRows     int
	CellWidth    int
	HarmonyCols  int
	HarmonyRows  int
	HarmonyLabel bool
	ChipWidth    int
	ChipGap      int
}

func (p accentLayoutPlan) HarmonyCount() int { return p.HarmonyCols * p.HarmonyRows }

const accentGridChunkyCols = 12

const accentGridChunkyCell = 3

func (m *OS) accentPlan() accentLayoutPlan {
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	avail := h - 2

	if w >= accentWideInner+2 && avail >= accentWideRightRows(false, 1) {
		p := accentLayoutPlan{
			Mode: accentLayoutWide, Inner: accentWideInner, ColInner: accentWideLeft,
			Sliders:     true,
			Slots:       h >= accentWideSlotsMinHeight,
			HarmonyCols: accentWideChipCols, HarmonyRows: 2, HarmonyLabel: true,
			ChipWidth: accentChipWidth, ChipGap: 1,
		}
		switch {
		case avail >= accentWideRightRows(true, 2):
			p.Blanks = true
		case avail >= accentWideRightRows(false, 2):
		default:
			p.HarmonyRows = 1
		}
		fixed := 3
		if p.Slots {
			fixed += accentSlotRows
			if p.Blanks {
				fixed++
			}
		}
		p.HueCells = max(p.ColInner-2, 1)
		p.GridCols, p.CellWidth = accentGridChunkyCols, accentGridChunkyCell
		p.GridRows = clampInt(avail-fixed, 1, accentGridMaxRows)
		return p
	}

	p := accentLayoutPlan{Mode: accentLayoutStacked, Inner: accentStackedInnerWidth}
	if w < accentStackedInnerWidth+2 {
		p.Mode = accentLayoutCompact
		p.Inner = overlay.DialogFitWidth(accentPickerInnerWidth, w)
	}
	p.ColInner = p.Inner
	p.HueCells = max(p.Inner-2, 1)
	p.GridCols, p.CellWidth = accentGridChunkyCols, accentGridChunkyCell
	p.HarmonyCols, p.HarmonyRows = accentStackedChipCols, 1
	p.ChipWidth, p.ChipGap = accentChipWidth, 0
	if p.Mode == accentLayoutCompact {
		p.GridCols, p.CellWidth = p.HueCells, 1
		p.HarmonyCols = accentHarmonyCompactCount
		p.ChipWidth = accentCompactChipWidth
	}

	furniture := 7
	p.Slots = h >= 1+7+accentSlotRows
	if p.Slots {
		furniture += accentSlotRows
	}
	p.Sliders = p.Mode == accentLayoutStacked && h >= accentSliderMinHeight
	if p.Sliders {
		furniture += accentSliderRows
	}
	p.GridRows = clampInt(h-furniture, 1, accentGridMaxRows)
	return p
}

func (m *OS) accentGridSize() (cols, rows int) {
	p := m.accentPlan()
	return p.GridCols, p.GridRows
}

func (m *OS) accentHueCells() int { return m.accentPlan().HueCells }

func (m *OS) accentSlidersShown() bool { return m.accentPlan().Sliders }

func (m *OS) accentSlotsShown() bool { return m.accentPlan().Slots }

const (
	accentLightTop    = 0.95
	accentLightBottom = 0.10
)

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

func accentCellColor(hue float64, col, row, cols, rows int) color.RGBA {
	sat, light := accentCellSL(col, row, cols, rows)
	return hslToRGB(hue, sat, light)
}

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

func accentHueAt(i, cols int) float64 {
	if cols <= 1 {
		return 0
	}
	return float64(i) * 360 / float64(cols)
}

func accentHueCell(hue float64, cols int) int {
	if cols <= 1 {
		return 0
	}
	return clampInt(int(hue*float64(cols)/360+0.5)%cols, 0, cols-1)
}

const accentHarmonyCompactCount = 3

var accentHarmonyCompactRotations = [accentHarmonyCompactCount]float64{180, -30, 30}

func (s *accentPickerState) baseHue() float64 {
	h, sat, _ := rgbToHSL(s.Base)
	if sat == 0 {
		return s.Hue
	}
	return h
}

func (s *accentPickerState) harmonyColor(i, count int) color.RGBA {
	count = max(count, 1)
	i = clampInt(i, 0, count-1)
	turn := 180 + float64(i)*360/float64(count)
	if count == accentHarmonyCompactCount {
		turn = accentHarmonyCompactRotations[i]
	}
	return hslToRGB(s.baseHue()+turn, s.Sat, s.Light)
}

func (s *accentPickerState) setCur(c color.RGBA) {
	s.takeColor(c)
	_, s.Sat, s.Light = rgbToHSL(c)
}

func (s *accentPickerState) takeColor(c color.RGBA) {
	s.Cur, s.Base = c, c
	s.Hex = overlay.Hex(c)
	s.Slot, s.Named = -1, ""
}

func (m *OS) takeHarmony(i int) {
	s := &m.AccentPicker
	count := m.accentPlan().HarmonyCount()
	s.Harmony = clampInt(i, 0, max(count-1, 0))
	c := s.harmonyColor(s.Harmony, count)
	s.Cur = c
	s.Hex = overlay.Hex(c)
	s.Slot, s.Named = -1, ""
}

func (m *OS) accentPickerSetHSL(sat, light float64) {
	s := &m.AccentPicker
	s.Sat, s.Light = clamp01(sat), clamp01(light)
	c := hslToRGB(s.Hue, s.Sat, s.Light)
	s.takeColor(c)
	cols, rows := m.accentGridSize()
	s.Col, s.Row = accentCellForSL(s.Sat, s.Light, cols, rows)
}

func clamp01(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

func (m *OS) accentPickerAdopt(c color.RGBA) {
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	s.setCur(c)
	s.Hue, s.Col, s.Row = accentCellFor(c, s.Hue, cols, rows)
}

func (s *accentPickerState) selection() Accent {
	if s.Slot >= 0 {
		return SlotAccent(s.Slot)
	}
	return RGBAccent(s.Cur)
}

func accentPayload(a Accent) string {
	if a.IsSlot() {
		return accentSlotNames[clampInt(a.Slot, 0, accentSwatchCount-1)]
	}
	return a.Hex()
}

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
	// AccentTargetSetting is a colour-valued config option, identified by its
	// registry path. The picker spares the user from knowing a hex, and from
	// knowing that empty means the theme's.
	AccentTargetSetting
	// AccentTargetThemeRole is a row of the theme editor: a chrome role by its
	// name, or a pane colour as pane: and its key. See theme_editor.go.
	AccentTargetThemeRole
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

	if unchanged && s.Named == "" {
		return nil
	}
	if target == AccentTargetSetting {
		return m.applySettingColor(id, s.settingSelection())
	}
	if target == AccentTargetThemeRole {
		m.applyThemeRoleColor(id, sel)
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

	switch target {
	case AccentTargetSession:
		return m.setSessionAccentCmd(id, "")
	case AccentTargetThemeRole:
		if row, ok := m.themeEditorRowByID(id); ok {
			m.themeEditorSet(row, "")
		}
		return nil
	case AccentTargetSetting:
		return m.applySettingColor(id, "")
	}
	m.ClearWindowAccent(id)
	return nil
}

func (m *OS) accentFocusShown(f accentFocus) bool {
	if f == accentFocusNamed {
		return len(m.AccentPicker.NamedOpts) > 0
	}
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
	for range n {
		s.Focus = accentFocus(((int(s.Focus)+delta)%n + n) % n)
		if m.accentFocusShown(s.Focus) {
			break
		}
	}
	switch s.Focus {
	case accentFocusHarmony:
		m.takeHarmony(s.Harmony)
	case accentFocusNamed:
		m.AccentPickerNamed(m.accentNamedIndex())
	case accentFocusANSI:
		slot := s.Slot
		if slot < 0 {
			slot = accentNearestSlot(s.Cur)
		}
		m.AccentPickerSlot(slot)
	case accentFocusGrid, accentFocusHue:
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
		m.AccentPickerSliderStep(ch, dx-dy)
		return
	}
	switch m.AccentPicker.Focus {
	case accentFocusHue:
		m.AccentPickerMoveHue(dx + dy)
	case accentFocusHarmony:
		m.AccentPickerMoveHarmony(dx + dy)
	case accentFocusNamed:
		m.AccentPickerNamed(m.accentNamedIndex() + dx + dy)
	case accentFocusANSI:
		m.AccentPickerMoveSlot(dx, dy)
	default:
		m.AccentPickerMoveCell(dx, dy)
	}
}

func (m *OS) accentNamedIndex() int {
	for i, name := range m.AccentPicker.NamedOpts {
		if name == m.AccentPicker.Named {
			return i
		}
	}
	return 0
}

// AccentPickerMoveShift is the shifted arrow: the same direction at the other
// granularity. On a slider it is the eyeballing jump the one-unit step is too
// fine for; on the hue strip it is the opposite, a single degree where a plain
// arrow is a whole cell of ten.
func (m *OS) AccentPickerMoveShift(dx, dy int) {
	if !m.ShowAccentPicker {
		return
	}
	if ch, ok := m.AccentPicker.Focus.sliderChannel(); ok {
		m.AccentPickerSliderStep(ch, (dx-dy)*ch.coarse())
		return
	}
	if m.AccentPicker.Focus == accentFocusHue {
		m.AccentPickerNudgeHue(dx + dy)
	}
}

// AccentPickerNudgeHue turns the held hue by whole degrees, wrapping. The strip
// is a cell every ten degrees, so this reaches the nine hues between two cells;
// the cursor stays on the nearest cell and the hex says which one is held.
func (m *OS) AccentPickerNudgeHue(deg int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	s.Focus = accentFocusHue
	s.Hue = math.Mod(math.Mod(s.Hue+float64(deg), 360)+360, 360)
	m.accentPickerSetHSL(s.Sat, s.Light)
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
	cells := m.accentHueCells()
	at := (accentHueCell(m.AccentPicker.Hue, cells) + delta%cells + cells) % cells
	m.AccentPickerHueCell(at)
}

// AccentPickerHueCell holds a new hue, carrying the saturation and lightness
// across it. They come from the continuous model rather than from the cell the
// grid cursor is on, so turning the hue does not round a slider's work away.
func (m *OS) AccentPickerHueCell(i int) {
	if !m.ShowAccentPicker {
		return
	}
	cells := m.accentHueCells()
	s := &m.AccentPicker
	s.Focus = accentFocusHue
	s.Hue = accentHueAt(clampInt(i, 0, cells-1), cells)
	m.accentPickerSetHSL(s.Sat, s.Light)
}

// AccentPickerHarmonyAt puts the harmony cursor on a chip and takes its colour.
func (m *OS) AccentPickerHarmonyAt(i int) {
	if !m.ShowAccentPicker {
		return
	}
	m.AccentPicker.Focus = accentFocusHarmony
	m.takeHarmony(i)
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
		digits = ""
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

func (m *OS) accentPickerSetHex(buf string) {
	s := &m.AccentPicker
	s.Focus = accentFocusHex
	s.Hex = buf
	s.Slot, s.Named = -1, ""
	c, ok := parseHexColor(buf)
	if !ok {
		return
	}
	cols, rows := m.accentGridSize()
	s.Cur, s.Base = c, c
	s.Hue, s.Col, s.Row = accentCellFor(c, s.Hue, cols, rows)
	_, s.Sat, s.Light = rgbToHSL(c)
}

func hexDigitsOf(buf string) string {
	if len(buf) > 0 && buf[0] == '#' {
		return buf[1:]
	}
	return buf
}
