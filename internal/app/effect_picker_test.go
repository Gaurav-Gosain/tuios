package app

import (
	"slices"
	"strings"
	"testing"

	tfx "github.com/Gaurav-Gosain/tuiffects"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// effectPickerOS is a screen worth animating: one pane with recognisable text
// in it, a dock, and the settings page open on the Saver section, which is
// where the picker is reached from.
func effectPickerOS(t *testing.T, w, h int) (*OS, *terminal.Window) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	win := newTestWindow(t, "effect-0001", w-4, h-6)
	win.X, win.Y = 1, 2
	win.Width, win.Height = w-4, h-6
	win.Workspace = 1
	m.CurrentWorkspace = 1
	m.Windows = append(m.Windows, win)
	m.FocusedWindow = len(m.Windows) - 1

	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[32mPANETEXTMARKER\x1b[0m\r\n$ "))
	win.UnlockIO()
	win.MarkContentDirty()

	m.ShowSettings = true
	for i, c := range m.settingsCategories() {
		if c.Name == "Saver" {
			m.SettingsCategory = i
		}
	}
	return m, win
}

// captureText is the picker's capture read back as plain rows.
func captureText(m *OS) string {
	var b strings.Builder
	for _, row := range m.effectPreview.capture {
		for _, c := range row {
			if c.Symbol == "" {
				b.WriteString(" ")
				continue
			}
			b.WriteString(c.Symbol)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// effectRowIndex is where a name sits in the picker's current list.
func effectRowIndex(m *OS, name string) int {
	return slices.Index(m.effectPickerItems(), name)
}

// TestEffectPreviewAnimatesTheRealScreen is the point of the whole feature.
//
// The names say nothing, so the picker previews; a preview over invented sample
// text would misrepresent it, because these effects resolve every character
// back to the colour it was captured with and several behave differently over a
// cell that carries its own background. So the capture has to be the screen.
//
// Negative control: building the preview over a grid of dummy text, or over the
// picker's own panel, loses PANETEXTMARKER and fails this.
func TestEffectPreviewAnimatesTheRealScreen(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()

	if m.effectPreview.capture == nil {
		t.Fatal("the picker opened with no capture, so there is nothing to animate")
	}
	if !strings.Contains(captureText(m), "PANETEXTMARKER") {
		t.Error("the capture does not carry the pane's own text, so the preview is not of this screen")
	}
	if m.effectPreview.canvasWidth != m.GetRenderWidth() || m.effectPreview.canvasHeight != m.GetRenderHeight() {
		t.Errorf("the effect was built on a %dx%d canvas, want the screen's %dx%d",
			m.effectPreview.canvasWidth, m.effectPreview.canvasHeight,
			m.GetRenderWidth(), m.GetRenderHeight())
	}

	// Colour survives the capture. It is what makes the screen reassemble as
	// itself rather than in the effect's palette, and a capture that dropped it
	// would still pass a symbols-only check.
	coloured := false
	for _, row := range m.effectPreview.capture {
		for _, c := range row {
			if c.HasFg {
				coloured = true
			}
		}
	}
	if !coloured {
		t.Error("no captured cell carries a foreground colour")
	}

	// The frame covers the screen. A short one would let panes show through the
	// bottom of the animation.
	if lines := strings.Split(m.effectPreview.frame, "\n"); len(lines) != m.GetRenderHeight() {
		t.Errorf("the preview frame is %d rows, want %d", len(lines), m.GetRenderHeight())
	}
}

// TestEffectCaptureLeavesOutThePanels: the settings page is on screen only
// because someone is choosing an effect. Captured with it, the preview carries
// a frozen copy of the panel that the live one then sits on top of, out of
// register, which reads as a fault rather than as a preview.
//
// Negative control: dropping the capturing guard in renderOverlays puts
// "Screen saver" back into the capture and fails this.
func TestEffectCaptureLeavesOutThePanels(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)

	// Setup: the panel really is on screen before the capture, or the check
	// below proves nothing.
	if !strings.Contains(stripANSIForTrace(m.composeFrame()), "Settings") {
		t.Fatal("setup: the settings panel is not on screen, so leaving it out is not being tested")
	}

	_ = m.OpenEffectPicker()
	text := captureText(m)
	if strings.Contains(text, "Settings") {
		t.Error("the capture carries the settings panel, so the preview animates a panel over itself")
	}
	// What must survive is everything under the panels.
	if !strings.Contains(text, "PANETEXTMARKER") {
		t.Error("leaving the panels out took the pane with them")
	}
}

// TestEffectPickerMovePreviewsTheRowUnderTheCursor: moving is what previews.
// A picker where the animation stayed on whatever was selected when it opened
// would list thirty-six names and show one of them.
//
// Negative control: dropping the buildEffectPreview call from EffectPickerMove
// leaves the running effect on the first one and fails this.
func TestEffectPickerMovePreviewsTheRowUnderTheCursor(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()

	for _, name := range []string{"wipe", "expand", "middleout"} {
		idx := effectRowIndex(m, name)
		if idx < 0 {
			t.Fatalf("setup: %s is not on offer", name)
		}
		_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
		if m.effectPreview.running != name {
			t.Errorf("moved to %s but the preview is running %q", name, m.effectPreview.running)
		}
		if m.effectPreview.frame == "" {
			t.Errorf("%s: the preview has no frame", name)
		}
	}
}

// TestRandomPreviewRunsARealEffect: random is the default and has to stay
// selectable, and previewing it as a blank screen would say the default does
// nothing.
//
// Negative control: making buildEffectPreview skip the random row leaves
// running empty and fails this.
func TestRandomPreviewRunsARealEffect(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()

	idx := effectRowIndex(m, config.ScreensaverRandomEffect)
	if idx < 0 {
		t.Fatal("random is not on offer, and it is the default")
	}
	_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
	running := m.effectPreview.running
	if running == "" || running == config.ScreensaverRandomEffect {
		t.Fatalf("the random row is previewing %q rather than a real effect", running)
	}
	if _, ok := tfx.Lookup(running); !ok {
		t.Errorf("the random row resolved to %q, which is not an effect", running)
	}
	// And the panel names what it resolved to, which is the only thing the row
	// has to say beyond its own name.
	body := pickerBody(t, m)
	if !strings.Contains(body, running) {
		t.Errorf("the panel does not name the effect random is showing (%s)", running)
	}
}

// pickerBody renders the effect picker and strips its styling.
func pickerBody(t *testing.T, m *OS) string {
	t.Helper()
	content, _, _ := m.renderEffectPicker()
	return stripANSIForTrace(content)
}

// TestEffectsWithNoOpeningNeverHideTheScreen is the one claim on this panel
// that is not a band, so it is the one that has to be structural.
//
// "The screen stays visible from the start" is stronger than anything else the
// picker says. It carries no "it depends on your screen", so it has to hold on
// every screen, and a measurement of one screen cannot earn it. That is how
// rings, vhstape, waves and thunderstorm came to carry it: all four measured
// zero under a metric that asked when the screen first read well, and the first
// three then took the screen away for twenty-three, twelve and seven seconds.
//
// So this runs every effect flagged keepsScreen to its end and holds it to the
// whole claim on every frame: every captured glyph that is legible on the
// screen itself stays legible, in its own place, no share of it missing. Six
// screen shapes, from a phone-sized terminal to a wide one, because the effects
// that failed this failed it by size.
//
// "Legible on the screen itself" is the capture's own contrast, and it matters
// once the capture has colour. The dock's hairline and the rounded caps of its
// pills are drawn below the readability floor on purpose, so no effect can make
// them readable and a claim that counted them failed on the untouched screen.
// It passed for as long as it did only because test frames were stripped of
// colour, which made every glyph the default foreground.
//
// highlight passes. It sweeps a band of brighter colour over text that never
// moves.
//
// Negative control: setting keepsScreen on rings, vhstape, waves, thunderstorm
// or burn fails this, naming the frame the screen goes away on.
func TestEffectsWithNoOpeningNeverHideTheScreen(t *testing.T) {
	var none []string
	for name := range effectOpenings {
		if effectOpeningBandOf(name, &config.Global) == effectOpeningNone {
			none = append(none, name)
		}
	}
	slices.Sort(none)
	if len(none) == 0 {
		t.Fatal("no effect claims to keep the screen visible, so this proves nothing")
	}

	for _, screen := range []struct{ w, h int }{
		{40, 12}, {60, 20}, {80, 24}, {100, 30}, {160, 45}, {220, 60},
	} {
		m, _ := effectPickerOS(t, screen.w, screen.h)
		_ = m.OpenEffectPicker()
		p := &m.effectPreview
		if p.capture == nil {
			t.Fatalf("setup: no capture at %dx%d", screen.w, screen.h)
		}
		for _, name := range none {
			// The screen the effect ends on, read off the effect itself. The
			// engine anchors the captured text to the top left of the canvas,
			// so a capture whose content starts below the first row is drawn a
			// row or two higher than it was taken; comparing against the
			// capture's own coordinates would measure that shift rather than
			// the effect. The end state is what the claim is about anyway.
			settled := effectSettledScreen(t, p, name)
			want := legibleGlyphs(settled)
			if len(want) < 20 || len(want) < len(settled)/2 {
				t.Fatalf("setup: %s settles on %d legible glyph cells of %d at %dx%d, not a screen worth hiding",
					name, len(want), len(settled), screen.w, screen.h)
			}

			d, _ := tfx.Lookup(name)
			effect := d.New()
			engine, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, effect, d.NeedsFillCharacters, defaultSaverSettings())
			if !ok {
				t.Fatalf("%s will not build at %dx%d", name, screen.w, screen.h)
			}
			worst, worstFrame, frame := len(want), 0, 0
			for frame < 8000 {
				if readable := measureReadable(engine, want); readable < worst {
					worst, worstFrame = readable, frame
				}
				if !effect.Advance(engine) {
					break
				}
				frame++
			}
			if frame < 30 {
				t.Errorf("%s ran %d frames at %dx%d, too few to have hidden anything",
					name, frame, screen.w, screen.h)
			}
			// The whole screen, not most of it. An effect that dims a
			// corner of a small terminal for a second is hiding part of the
			// screen, and the claim leaves no room for a part.
			if worst < len(want) {
				t.Errorf("%s is told to say the screen stays visible, but at frame %d of %d at %dx%d "+
					"only %d of %d legible captured glyphs are readable",
					name, worstFrame, frame, screen.w, screen.h, worst, len(want))
			}
		}
	}
}

// TestEffectOpeningTableCoversEveryEffect keeps the baked measurements honest.
// A tuiffects bump that adds an effect must not leave a row with no number and
// no way to know it is missing.
//
// Negative control: deleting any entry, or adding one for an effect that does
// not exist, fails this.
func TestEffectOpeningTableCoversEveryEffect(t *testing.T) {
	names := tfx.Names()
	for _, name := range names {
		if _, ok := effectOpenings[name]; !ok {
			t.Errorf("%s has no measured opening; re-run the measurement and add it", name)
		}
	}
	for name := range effectOpenings {
		if !slices.Contains(names, name) {
			t.Errorf("%s is measured but is not an effect any more", name)
		}
	}
}

// effectSettledScreen runs an effect to its end and returns the screen it
// leaves behind, cell by cell.
func effectSettledScreen(t *testing.T, p *effectPreview, name string) map[measureCoord]measureLook {
	t.Helper()
	d, _ := tfx.Lookup(name)
	effect := d.New()
	engine, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, effect, d.NeedsFillCharacters, defaultSaverSettings())
	if !ok {
		t.Fatalf("%s will not build", name)
	}
	for range 8000 {
		if !effect.Advance(engine) {
			break
		}
	}
	settled := map[measureCoord]measureLook{}
	for y, line := range engine.FrameRows() {
		for x, cell := range line {
			if cell == nil || cell.Symbol == "" || cell.Symbol == " " {
				continue
			}
			look := measureLook{symbol: cell.Symbol}
			if cell.Colors.HasFg {
				look.fg = cell.Colors.Fg
			}
			if cell.Colors.HasBg {
				look.bg, look.hasBg = cell.Colors.Bg, true
			}
			settled[measureCoord{x, y}] = look
		}
	}
	return settled
}

// legibleGlyphs keeps the cells of a screen that are readable as drawn, by the
// same contrast rule measureReadable applies to a frame. A cell the screen
// itself draws below that rule, such as a dim rule line, is not something an
// effect can hide.
func legibleGlyphs(screen map[measureCoord]measureLook) map[measureCoord]measureLook {
	legible := make(map[measureCoord]measureLook, len(screen))
	for at, look := range screen {
		bg := tfx.Color{}
		if look.hasBg {
			bg = look.bg
		}
		if measureContrast(look.fg, bg) >= measureReadableContrast {
			legible[at] = look
		}
	}
	return legible
}

// TestEveryEffectBuildsOverACapturedScreen is the fill-character fix.
//
// A capture only covers the cells that carry a glyph or a background, so a
// screen with plain empty space leaves holes in the canvas. burn and laseretch
// declare NeedsFillCharacters because their build picks a starting cell that
// can land in one of those holes; without it they returned an error and the
// saver silently refused to start, which from outside looks like a screen saver
// that is switched on and never appears.
//
// Negative control: passing false for fill makes burn and laseretch fail here.
func TestEveryEffectBuildsOverACapturedScreen(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()
	p := &m.effectPreview
	if p.capture == nil {
		t.Fatal("setup: no capture")
	}

	// Setup: the capture really does have holes in it, or the fill flag is not
	// being tested.
	holes := 0
	for _, row := range p.capture {
		for _, c := range row {
			if (c.Symbol == "" || c.Symbol == " ") && !c.HasBg {
				holes++
			}
		}
	}
	if holes == 0 {
		t.Fatal("setup: the capture has no empty cells, so fill characters change nothing here")
	}

	for _, name := range tfx.Names() {
		d, _ := tfx.Lookup(name)
		if _, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, d.New(), d.NeedsFillCharacters, defaultSaverSettings()); !ok {
			t.Errorf("%s will not build over a captured screen, so choosing it stops the saver working", name)
		}
	}
}

// TestEffectPreviewFrameChainDoesNotDouble: the preview drives its own frames,
// so exactly one chain may be in flight. A second one steps the animation twice
// per frame and pays for it twice.
//
// Negative control: dropping the ticking guard makes the second tick return a
// command; dropping the generation check makes the stale message return one.
func TestEffectPreviewFrameChainDoesNotDouble(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	if cmd := m.OpenEffectPicker(); cmd == nil {
		t.Fatal("setup: opening scheduled no frame")
	}
	gen := m.effectPreview.gen

	// A move while a frame is in flight rides the chain that exists.
	if cmd := m.EffectPickerMove(1); cmd != nil {
		t.Error("a move started a second chain of frames alongside the live one")
	}
	// The frame in flight arrives and schedules the next one.
	if cmd := m.handleEffectPreviewFrame(effectPreviewFrameMsg{gen: gen}); cmd == nil {
		t.Error("a live frame did not schedule the next one, so the preview freezes")
	}
	// A message from an earlier opening is dropped.
	if cmd := m.handleEffectPreviewFrame(effectPreviewFrameMsg{gen: gen - 1}); cmd != nil {
		t.Error("a stale frame message started a chain of its own")
	}

	// Reopening bumps the generation, so the old chain cannot come back.
	m.CloseEffectPicker()
	_ = m.OpenEffectPicker()
	if m.effectPreview.gen == gen {
		t.Error("reopening reused the generation, so a frame from the last opening is still accepted")
	}
}

// TestClosedEffectPickerRunsNothing is the constraint the whole feature is
// under: a picker that runs an animation must cost nothing when it is shut.
//
// Negative control: re-arming the tick regardless of ShowEffectPicker, or
// leaving the engine in place on close, fails this.
func TestClosedEffectPickerRunsNothing(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()
	gen := m.effectPreview.gen
	m.CloseEffectPicker()

	if cmd := m.handleEffectPreviewFrame(effectPreviewFrameMsg{gen: gen}); cmd != nil {
		t.Error("a frame message for a closed picker scheduled another frame")
	}
	// And with the engine still in hand, which is the case the flag is the only
	// guard for: nothing may run once the panel is gone.
	_ = m.OpenEffectPicker()
	gen = m.effectPreview.gen
	m.ShowEffectPicker = false
	if m.effectPreview.engine == nil {
		t.Fatal("setup: the engine is already gone, so the flag is not what is being tested")
	}
	if cmd := m.handleEffectPreviewFrame(effectPreviewFrameMsg{gen: gen}); cmd != nil {
		t.Error("the preview kept running after the picker was taken off screen")
	}
	m.CloseEffectPicker()
	if m.effectPreview.engine != nil || m.effectPreview.capture != nil {
		t.Error("closing the picker left the engine and the capture in memory")
	}
	if m.effectPreview.frame != "" {
		t.Error("closing the picker left the last animation frame on screen")
	}
	// And nothing is drawn for it.
	for _, l := range m.renderOverlays() {
		if l.GetID() == "effectpreview" {
			t.Error("a preview layer was placed with the picker shut")
		}
	}
}

// TestEffectPreviewStopsOnResize: the capture is the screen at one size, and
// the screen it was taken from is underneath the picker now, so there is
// nothing to re-capture. The saver stops for this and so does the preview.
//
// Negative control: dropping the size check animates the old capture into the
// new screen, anchored into a corner, and this fails.
func TestEffectPreviewStopsOnResize(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()
	gen := m.effectPreview.gen

	m.Width, m.EffectiveWidth = 80, 80
	cmd := m.handleEffectPreviewFrame(effectPreviewFrameMsg{gen: gen})
	if cmd != nil {
		t.Error("the preview kept animating a capture of a screen that is no longer that size")
	}
	if m.effectPreview.frame != "" {
		t.Error("a stale frame was left on screen after the resize")
	}
	if !m.ShowEffectPicker {
		t.Error("the resize took the picker away as well; the list is still worth using")
	}
	if !strings.Contains(pickerBody(t, m), "The screen size changed") {
		t.Error("the picker says nothing about why the preview stopped")
	}
}

// TestEffectPickerCommitsOnEnterAndLeavesItAloneOnEsc.
//
// Negative control: making apply skip setOption, or making cancel write the
// selection, fails this.
func TestEffectPickerCommitsOnEnterAndLeavesItAloneOnEsc(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	m.ConfigReadOnly = true // nothing here may touch the real config file
	before := m.screensaverConfig().EffectName()

	_ = m.OpenEffectPicker()
	idx := effectRowIndex(m, "expand")
	_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
	_ = m.EffectPickerApplySelection()

	if m.ShowEffectPicker {
		t.Error("Enter did not close the picker")
	}
	if got := m.screensaverConfig().EffectName(); got != "expand" {
		t.Errorf("Enter left the setting on %q, want expand", got)
	}

	// Esc changes nothing, however far the selection moved.
	_ = m.OpenEffectPicker()
	idx = effectRowIndex(m, "matrix")
	_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
	m.CancelEffectPicker()
	if m.ShowEffectPicker {
		t.Error("Esc did not close the picker")
	}
	if got := m.screensaverConfig().EffectName(); got != "expand" {
		t.Errorf("Esc left the setting on %q, want the applied expand", got)
	}
	if before == "expand" {
		t.Fatal("setup: the setting already was expand, so neither half of this proves anything")
	}
}

// TestEffectPickerFiltersAndRefusesAnEmptyApply: the search is what makes
// thirty-six reachable, and an apply with nothing under the cursor must not
// close the panel and strand the query.
//
// Negative control: making ApplySelection close on an empty list fails this.
func TestEffectPickerFiltersAndRefusesAnEmptyApply(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()

	_ = m.EffectPickerType("matr")
	items := m.effectPickerItems()
	if len(items) == 0 || !slices.Contains(items, "matrix") {
		t.Fatalf("the query matr found %v, want matrix among them", items)
	}
	if len(items) == len(config.ScreensaverEffects) {
		t.Error("the query filtered nothing out")
	}

	_ = m.EffectPickerClearQuery()
	if len(m.effectPickerItems()) != len(config.ScreensaverEffects) {
		t.Error("clearing the query did not put the whole list back")
	}

	_ = m.EffectPickerType("zzzznotaneffect")
	if len(m.effectPickerItems()) != 0 {
		t.Fatal("setup: the nonsense query still matched something")
	}
	if cmd := m.EffectPickerApplySelection(); cmd != nil {
		t.Error("an apply with nothing selected returned a save command")
	}
	if !m.ShowEffectPicker {
		t.Error("an apply with nothing selected closed the picker and stranded the query")
	}
}

// TestEffectPickerPanelFitsEveryScreen: thirty-six rows and a two-line detail
// block on a panel that has to fit a phone-sized terminal.
func TestEffectPickerPanelFitsEveryScreen(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)
			_ = m.OpenEffectPicker()
			out, _, _ := m.renderEffectPicker()
			assertFitsScreen(t, "effectpicker", out, sc.w, sc.h)
			_ = m.EffectPickerType("zzz")
			out, _, _ = m.renderEffectPicker()
			assertFitsScreen(t, "effectpicker empty", out, sc.w, sc.h)
			m.CancelEffectPicker()
		})
	}
}

// TestScreensaverEffectListIsWhatThePickerOffers ties the two together: the
// picker must offer the accepted set and nothing else, or a value it commits
// fails the option validator.
//
// Negative control: filtering any effect out of effectPickerItems fails this.
func TestScreensaverEffectListIsWhatThePickerOffers(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	o, ok := config.LookupOption("screensaver.effect")
	if !ok {
		t.Fatal("screensaver.effect is not in the registry")
	}
	got := m.effectPickerItems()
	if len(got) != len(o.Accepted) {
		t.Fatalf("the picker offers %d values and the option accepts %d", len(got), len(o.Accepted))
	}
	for i := range got {
		if got[i] != o.Accepted[i] {
			t.Errorf("row %d is %q, the option accepts %q", i, got[i], o.Accepted[i])
		}
	}
	if got[0] != config.ScreensaverRandomEffect {
		t.Errorf("the first row is %q, want random, which is the default", got[0])
	}
}
