package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	tfx "github.com/Gaurav-Gosain/tuiffects"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The effect picker is the theme picker's third sibling. screensaver.effect
// used to accept five values and a cycler stepped through them; it accepts
// thirty-six now, and a control that costs one keypress per stop is the wrong
// shape for a set that size. The next largest closed list in the panel is ten.
//
// Names alone would not fix it. orbittingvolley, synthgrid, binarypath and
// errorcorrect say nothing about what lands on the screen, so this picker does
// what the other two do: it previews. The theme picker recolours the whole
// screen, the glyph set picker redraws the chrome, and this one runs the effect
// over the screen you opened it from.
//
// The capture is the real composed screen, taken once when the picker opens and
// reused for every selection. Nothing else would be honest: the effects resolve
// each character back to the colour it was captured with, and several of them
// behave differently over a cell that carries its own background, so an effect
// previewed over invented sample text is a preview of a screen nobody has.
//
// It costs nothing when it is shut. The preview drives its own frames the way
// the saver does, so the maintenance tick never learns it exists.

// effectPreviewFrameMsg asks the running preview for its next frame. gen is the
// generation it was scheduled for: a message from a preview that has already
// been closed and reopened is dropped rather than starting a second chain of
// frames alongside the live one.
type effectPreviewFrameMsg struct{ gen int }

// effectPreview is the animation running behind the picker panel. The zero
// value is a preview that is not running.
type effectPreview struct {
	// capture is the composed screen the picker opened over, converted once.
	capture      [][]tfx.InputCell
	canvasWidth  int
	canvasHeight int
	// width and height are the render size the capture was taken at. A resize
	// invalidates the capture, and this is what it is compared against.
	width  int
	height int

	engine *tfx.Engine
	effect tfx.Effect
	// running is the effect actually on screen. It differs from the selected
	// row only for "random", which resolves to a real effect each time it is
	// built, and naming it is most of what the random row has to say.
	running string
	frame   string

	// gen counts openings. See effectPreviewFrameMsg.
	gen int
	// ticking is true while a frame message is in flight, so a rebuild does not
	// start a second chain.
	ticking bool
	// resized records that the capture went stale under a resize. The picker
	// stays up and says so rather than animating a screen that is gone.
	resized bool
	// capturing is true only inside captureEffectPreview, where it tells
	// renderOverlays to draw no panels. See captureEffectPreview.
	capturing bool
}

// effectPickerItems returns the effect names on offer, filtered by the current
// query. "random" is always first, as it is in the registry's accepted list.
func (m *OS) effectPickerItems() []string {
	all := config.ScreensaverEffects
	q := strings.ToLower(strings.TrimSpace(m.EffectPickerQuery))
	if q == "" {
		return all
	}
	hits := fuzzy.Filter(q, all)
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Text
	}
	return out
}

// OpenEffectPicker shows the searchable effect picker and starts its preview.
func (m *OS) OpenEffectPicker() tea.Cmd {
	m.EffectPickerQuery = ""
	m.EffectPickerScroll = 0
	m.EffectPickerOriginal = m.screensaverConfig().EffectName()

	m.EffectPickerSelected = 0
	for i, id := range m.effectPickerItems() {
		if id == m.EffectPickerOriginal {
			m.EffectPickerSelected = i
			break
		}
	}

	// Captured before the panel is on screen, so the preview animates the
	// screen the user was looking at rather than the picker sitting over it.
	m.captureEffectPreview()
	m.ShowEffectPicker = true

	// Cascade down-right of the settings panel it was opened from, so both are
	// visible and can be dragged as separate panels.
	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("effectpicker", so[0]+10, so[1]+3)
	}

	m.effectPreview.gen++
	m.effectPreview.ticking = false
	m.buildEffectPreview()
	return m.effectPreviewTick()
}

// captureEffectPreview converts the composed screen into engine input.
//
// The panels are left out of it. The saver animates panes, chrome, the sidebar
// and the dock, and the settings page is on screen only because someone is in
// the middle of choosing an effect. Captured with it, the preview carries a
// frozen copy of the settings panel that the live one then sits on top of,
// half a panel out of register, which reads as a fault rather than a preview.
func (m *OS) captureEffectPreview() {
	p := &m.effectPreview
	p.capture, p.canvasWidth, p.canvasHeight = nil, 0, 0
	p.resized = false
	width, height := m.GetRenderWidth(), m.GetRenderHeight()
	if width < 8 || height < 4 {
		return
	}
	p.capturing = true
	grid := m.composedGrid(0, 0, width, height)
	p.capturing = false
	if grid == nil {
		return
	}
	p.capture = screensaverCells(grid)
	p.canvasWidth, p.canvasHeight = grid.Cols, grid.Rows
	p.width, p.height = width, height
}

// buildEffectPreview puts the selected effect over the capture. It is called on
// every move, so the row under the cursor is the one animating.
func (m *OS) buildEffectPreview() {
	p := &m.effectPreview
	p.engine, p.effect, p.running, p.frame = nil, nil, "", ""
	if p.capture == nil {
		return
	}
	items := m.effectPickerItems()
	if m.EffectPickerSelected < 0 || m.EffectPickerSelected >= len(items) {
		return
	}
	// Through the saver's own resolver and builder, not a copy of them: an
	// effect that previews differently from the way it runs is a preview of
	// nothing. random resolves here exactly as it does at start-up.
	name, effect, fill := screensaverEffect(items[m.EffectPickerSelected])
	if effect == nil {
		return
	}
	engine, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, effect, fill, &m.Settings)
	if !ok {
		return
	}
	p.engine, p.effect, p.running = engine, effect, name
	p.frame = engine.Frame()
	m.renderSkipped = false
}

// effectPreviewTick schedules the next preview frame, unless one is already in
// flight or there is nothing to advance.
func (m *OS) effectPreviewTick() tea.Cmd {
	p := &m.effectPreview
	if p.engine == nil || p.ticking {
		return nil
	}
	p.ticking = true
	return effectPreviewFrameCmd(p.gen, &m.Settings)
}

// effectPreviewFrameCmd waits one frame and asks for the next one.
func effectPreviewFrameCmd(gen int, s *config.Settings) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(s.NormalFPS), func(time.Time) tea.Msg {
		return effectPreviewFrameMsg{gen: gen}
	})
}

// handleEffectPreviewFrame advances the preview by one frame. An effect that
// finishes is rebuilt, so the preview loops for as long as the row is selected
// and "random" shows a different effect each time round.
func (m *OS) handleEffectPreviewFrame(msg effectPreviewFrameMsg) tea.Cmd {
	p := &m.effectPreview
	if msg.gen != p.gen {
		// A frame left over from an earlier opening. Dropping it is the whole
		// point of the generation: without it the old chain and the new one
		// both run and the animation steps twice per frame.
		return nil
	}
	p.ticking = false
	if !m.ShowEffectPicker {
		return nil
	}
	if p.engine == nil {
		return nil
	}
	// A resize invalidates the capture, and the screen it was taken from is
	// underneath the picker now. The saver stops for this; so does the preview,
	// and the panel says so instead of animating a screen that has moved.
	if m.GetRenderWidth() != p.width || m.GetRenderHeight() != p.height {
		m.stopEffectPreview()
		p.resized = true
		m.MarkAllDirty()
		m.renderSkipped = false
		return nil
	}
	if !p.effect.Advance(p.engine) {
		m.buildEffectPreview()
	} else {
		p.frame = p.engine.Frame()
	}
	m.renderSkipped = false
	return m.effectPreviewTick()
}

// stopEffectPreview drops the animation and everything it held.
func (m *OS) stopEffectPreview() {
	p := &m.effectPreview
	p.engine, p.effect = nil, nil
	p.frame, p.running = "", ""
	p.capture = nil
	p.canvasWidth, p.canvasHeight = 0, 0
}

// EffectPickerMove moves the selection by delta and previews the new row.
func (m *OS) EffectPickerMove(delta int) tea.Cmd {
	items := m.effectPickerItems()
	if len(items) == 0 {
		return nil
	}
	next := clampInt(m.EffectPickerSelected+delta, 0, len(items)-1)
	if next == m.EffectPickerSelected {
		return nil
	}
	m.EffectPickerSelected = next
	_, visible, _ := m.effectPickerLayout()
	m.EffectPickerScroll = scrollWindow(m.EffectPickerScroll, m.EffectPickerSelected, len(items), visible)
	m.buildEffectPreview()
	return m.effectPreviewTick()
}

// EffectPickerRefilter resets the selection after the query changes and
// previews the new top result.
func (m *OS) EffectPickerRefilter() tea.Cmd {
	m.EffectPickerSelected = 0
	m.EffectPickerScroll = 0
	m.buildEffectPreview()
	return m.effectPreviewTick()
}

// EffectPickerType adds text to the query and refilters.
func (m *OS) EffectPickerType(s string) tea.Cmd {
	m.EffectPickerQuery += s
	return m.EffectPickerRefilter()
}

// EffectPickerBackspace removes the last character of the query.
func (m *OS) EffectPickerBackspace() tea.Cmd {
	if m.EffectPickerQuery == "" {
		return nil
	}
	q := []rune(m.EffectPickerQuery)
	m.EffectPickerQuery = string(q[:len(q)-1])
	return m.EffectPickerRefilter()
}

// EffectPickerClearQuery empties the query.
func (m *OS) EffectPickerClearQuery() tea.Cmd {
	m.EffectPickerQuery = ""
	return m.EffectPickerRefilter()
}

// CloseEffectPicker hides the picker and takes the animation off the screen.
func (m *OS) CloseEffectPicker() {
	m.ShowEffectPicker = false
	m.EffectPickerQuery = ""
	m.stopEffectPreview()
	m.effectPreview.resized = false
	// The panes under the animation have not drawn for as long as it was up.
	m.MarkAllDirty()
	m.renderSkipped = false
}

// CancelEffectPicker closes without changing the setting. Used for Esc and for
// a click outside the panel.
//
// Unlike the theme and glyph pickers it has nothing to restore. Those two
// preview by applying the value, so cancel has to put the old one back; this
// one previews by running the animation, and the setting is not written until
// Enter. The contract a user sees is the same either way: Esc leaves the row
// where it was.
func (m *OS) CancelEffectPicker() {
	m.CloseEffectPicker()
}

// EffectPickerApplySelection commits the selected effect, persists it, and
// closes.
func (m *OS) EffectPickerApplySelection() tea.Cmd {
	items := m.effectPickerItems()
	if m.EffectPickerSelected < 0 || m.EffectPickerSelected >= len(items) {
		// Nothing to commit, so nothing is closed. Closing here would take away
		// the query that found nothing along with the only key that gets back
		// to a list, which is the trap the other two pickers name.
		m.ShowNotification("No effect matches "+m.EffectPickerQuery,
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.setOption("screensaver.effect", items[m.EffectPickerSelected])
	save := m.persistSettings()
	m.CloseEffectPicker()
	return save
}

// effectOpenings is how long each effect hides the screen, over one reference
// screen at 80x24, and whether it hides it at all.
//
// frames is not a time the picker can show anyone. Read the block below before
// putting a number back on the panel.
//
// The metric is the last frame at which fewer than nine cells in ten of the
// capture's glyph cells are readable, plus one. A cell is readable when it
// shows its captured symbol, in its captured place, in a colour that stands off
// the background it is drawn on. TestMeasureEffectOpenings carries the argument
// for both halves and re-measures the table.
//
// What frames is not is a promise about anyone's screen. Several of these
// effects do work per character, so the wait scales with how much text is on
// screen and how big the screen is. Measured over four screens, pour ran from
// 3.1 seconds on a bare prompt to 97.8 seconds at 200x50 on a full one, rain
// from 2.6 to 65.2, and laseretch from 6.4 to 133.3. Others hardly move:
// fireworks stayed inside 18.9 to 20.3 and matrix inside 25.1 to 37.6. So no
// single scale factor fits them, and a number taken from here and shown to a
// user is a measurement of a screen that is not theirs.
//
// What survives is the order and the size of the wait. wipe is at the top on
// every screen measured and swarm at the bottom, and an effect that hides the
// screen for most of a minute does that on any screen. So the picker takes a
// band from this table and never a time, and the panel says the time depends on
// the screen. effectOpeningBandOf is the only reader.
//
// Measuring the real screen instead was considered and does not fit: it means
// running each effect to its end, and at 200x50 four of them are still going
// after two minutes of animation. The picker would have to sit there.
//
// TestEffectOpeningTableCoversEveryEffect fails the build when the engine gains
// or loses an effect, so a version bump cannot leave a row with no band.
var effectOpenings = map[string]effectOpening{
	"binarypath":      {frames: 801},
	"blackhole":       {frames: 848},
	"bouncyballs":     {frames: 1043},
	"bubbles":         {frames: 1411},
	"burn":            {frames: 754},
	"crumble":         {frames: 544},
	"decrypt":         {frames: 1014},
	"errorcorrect":    {frames: 454},
	"expand":          {frames: 88},
	"fireworks":       {frames: 1164},
	"highlight":       {frames: 0, keepsScreen: true},
	"laseretch":       {frames: 1649},
	"matrix":          {frames: 1548},
	"middleout":       {frames: 99},
	"orbittingvolley": {frames: 1043},
	"overflow":        {frames: 152},
	"pour":            {frames: 807},
	"print":           {frames: 2087},
	"rain":            {frames: 539},
	"randomsequence":  {frames: 156},
	"rings":           {frames: 1373},
	"scattered":       {frames: 136},
	"slice":           {frames: 121},
	"slide":           {frames: 113},
	"smoke":           {frames: 154},
	"spotlights":      {frames: 641},
	"spray":           {frames: 366},
	"swarm":           {frames: 2778},
	"sweep":           {frames: 177},
	"synthgrid":       {frames: 530},
	"thunderstorm":    {frames: 0},
	"tuffbaby":        {frames: 505},
	"unstable":        {frames: 291},
	"vhstape":         {frames: 702},
	"waves":           {frames: 410},
	"wipe":            {frames: 54},
}

// effectOpening is one row of the table.
type effectOpening struct {
	// frames is the opening over the reference screen, in engine frames. It
	// feeds a band and nothing else.
	frames int
	// keepsScreen is the one thing here that is not a measurement of one
	// screen. It says the effect never takes a character off the screen, never
	// moves one, and never draws one in a colour you cannot read it in: every
	// captured glyph is legible in its own place on every frame of the run.
	//
	// highlight is the only effect that does this. It sweeps a band of brighter
	// colour over text that never moves.
	//
	// A zero in frames does not imply it, and reading it that way is what put
	// "The screen stays visible from the start." under rings, vhstape, waves
	// and thunderstorm. The first three run from the untouched screen and take
	// it away afterwards, for twenty-three, twelve and seven seconds; the
	// fourth dims a fifth of a small screen at a time. All four measure zero
	// under a metric that asks when the screen first reads well.
	//
	// TestEffectsWithNoOpeningNeverHideTheScreen holds every effect flagged
	// here to the claim, on every frame, over six screen shapes.
	keepsScreen bool
}

// effectOpeningBand is how long an effect hides the screen, coarse enough to
// stay true on a screen that is not the one the table was measured over.
type effectOpeningBand int

const (
	// effectOpeningUnknown is an effect with no measurement, and the picker
	// shows nothing for it. random lands here: it has no one opening.
	effectOpeningUnknown effectOpeningBand = iota
	// effectOpeningNone never hides the screen at all. See keepsScreen.
	effectOpeningNone
	effectOpeningShort
	effectOpeningMedium
	// effectOpeningLong is the band worth flagging. It is drawn in the warning
	// colour on the row and under the list.
	effectOpeningLong
)

// The band boundaries, in seconds over the reference screen.
//
// Both sit in a gap in the measurements rather than on a round number, so a
// re-measurement that moves an effect by a few percent does not move it a band.
// The nearest neighbours are unstable at 4.8 and spray at 6.1, then crumble at
// 9.1 and spotlights at 10.7.
const (
	effectMediumOpening = 5.5
	// effectSlowOpening is the point past which an opening is worth flagging.
	// Ten seconds of a screen you cannot read is long enough to look like a
	// fault.
	effectSlowOpening = 10.0
)

// effectOpeningBandOf is the band an effect falls in over the reference screen.
//
// It takes seconds from the table at the current frame rate and throws them
// away again. Nothing else may read effectOpenings: a caller that wants the
// number wants to show it, and the number is not true of the screen the user is
// looking at.
func effectOpeningBandOf(name string, s *config.Settings) effectOpeningBand {
	opening, ok := effectOpenings[name]
	if !ok {
		return effectOpeningUnknown
	}
	if opening.keepsScreen {
		return effectOpeningNone
	}
	fps := s.NormalFPS
	if fps <= 0 {
		fps = 60
	}
	switch seconds := float64(opening.frames) / float64(fps); {
	case seconds < effectMediumOpening:
		return effectOpeningShort
	case seconds < effectSlowOpening:
		return effectOpeningMedium
	default:
		return effectOpeningLong
	}
}
