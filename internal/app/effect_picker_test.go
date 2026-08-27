package app

import (
	"regexp"
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

// TestEffectRowOpensThePickerRatherThanCycling is the reason the row stopped
// being derived. screensaver.effect accepts thirty-six values; a cycler asks
// for up to thirty-five keypresses to reach one, and the picker is what Enter
// has to reach instead.
//
// Negative control: dropping the activate hook, or putting opt("screensaver.
// effect") back in the category, leaves Enter cycling and fails this.
func TestEffectRowOpensThePickerRatherThanCycling(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)

	if n := len(config.ScreensaverEffects); n < 30 {
		t.Fatalf("setup: the effect list is %d long, so this row is not the outlier the picker was built for", n)
	}

	items := m.settingsCurrentItems()
	idx := slices.IndexFunc(items, func(it settingItem) bool { return it.Path == "screensaver.effect" })
	if idx < 0 {
		t.Fatal("the Saver section has no screensaver.effect row")
	}
	m.SettingsSelected = idx
	if items[idx].activate == nil {
		t.Fatal("the effect row has no activate hook, so Enter cycles instead of opening the picker")
	}

	cmd := m.SettingsActivate()
	if !m.ShowEffectPicker {
		t.Fatal("Enter on the effect row did not open the picker")
	}
	if cmd == nil {
		t.Error("opening the picker scheduled no first frame, so the preview never animates")
	}
	// And it opens on the effect that is set, not at the top of the list.
	if got := m.effectPickerItems()[m.EffectPickerSelected]; got != m.screensaverConfig().EffectName() {
		t.Errorf("the picker opened on %q, want the configured %q", got, m.screensaverConfig().EffectName())
	}
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

// TestEffectPreviewDrawsUnderThePanels: the animation covers the screen, so it
// has to sit above the panes and the dock and below every panel, or the picker
// choosing the effect is itself invisible.
//
// Negative control: giving the preview layer the panel's own z, or placing it
// after the panels at a higher one, fails this.
func TestEffectPreviewDrawsUnderThePanels(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 30)
	_ = m.OpenEffectPicker()

	var previewZ, pickerZ, settingsZ int
	var found bool
	for _, l := range m.renderOverlays() {
		switch l.GetID() {
		case "effectpreview":
			previewZ, found = l.GetZ(), true
		case "effectpicker":
			pickerZ = l.GetZ()
		case "settings":
			settingsZ = l.GetZ()
		}
	}
	if !found {
		t.Fatal("no preview layer was placed while the picker was open")
	}
	if pickerZ == 0 || settingsZ == 0 {
		t.Fatal("setup: the panels were not placed, so there is nothing to be under")
	}
	if previewZ >= pickerZ {
		t.Errorf("the preview draws at z %d and the picker at %d, so the picker is buried", previewZ, pickerZ)
	}
	if previewZ >= settingsZ {
		t.Errorf("the preview draws at z %d and the settings panel at %d", previewZ, settingsZ)
	}
	if previewZ <= config.ZIndexDock {
		t.Errorf("the preview draws at z %d, at or under the dock's %d, so the dock shows through it",
			previewZ, config.ZIndexDock)
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

// TestEffectPickerShowsWhatEachEffectDoesAndHowLongItHides: the names are the
// problem the picker exists for. orbittingvolley and errorcorrect say nothing,
// and neither does a list of them.
//
// Negative control: dropping the description line, or the opening column, fails
// this.
func TestEffectPickerShowsWhatEachEffectDoesAndHowLongItHides(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 40)
	_ = m.OpenEffectPicker()

	// A slow opener: the picker has to say so before it is chosen, not after.
	idx := effectRowIndex(m, "print")
	if idx < 0 {
		t.Fatal("setup: print is not on offer")
	}
	_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
	body := pickerBody(t, m)

	d, _ := tfx.Lookup("print")
	head := d.Description[:20]
	if !strings.Contains(body, head) {
		t.Errorf("the panel does not describe print; body:\n%s", body)
	}
	// Off print's own row, not off the panel. The band words are also in the
	// sentence under the list, so a search of the whole body passes with the
	// column gone.
	if got := pickerRowColumn(t, m, "print"); got != "long" {
		t.Errorf("print's row carries %q in the opening column, want \"long\"; body:\n%s", got, body)
	}
	if !strings.Contains(body, "The wait is long. The time depends on your screen.") {
		t.Errorf("the panel does not say how long print hides the screen; body:\n%s", body)
	}

	// A fast one reads differently, or the column says the same thing for
	// everything and carries no information.
	idx = effectRowIndex(m, "wipe")
	_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
	body = pickerBody(t, m)
	if got := pickerRowColumn(t, m, "wipe"); got != "short" {
		t.Errorf("wipe's row carries %q in the opening column, want \"short\"; body:\n%s", got, body)
	}
	if strings.Contains(body, "The wait is long") {
		t.Error("the detail line did not follow the selection")
	}
}

// pickerRowColumn is the last word on one effect's row, which is the opening
// column when the row has one. It fails the test when the row is not on screen,
// so a caller cannot pass by asking about a row that scrolled away.
func pickerRowColumn(t *testing.T, m *OS, name string) string {
	t.Helper()
	content, _, rows := m.renderEffectPicker()
	lines := strings.Split(stripANSIForTrace(content), "\n")
	items := m.effectPickerItems()
	for _, r := range rows {
		if items[r.Idx] != name || r.Rect.Y0 < 0 || r.Rect.Y0 >= len(lines) {
			continue
		}
		fields := strings.Fields(lines[r.Rect.Y0])
		if len(fields) == 0 {
			t.Fatalf("%s's row is blank", name)
		}
		if last := fields[len(fields)-1]; last != name {
			return last
		}
		return ""
	}
	t.Fatalf("%s is not on screen, so its row cannot be read", name)
	return ""
}

// TestEffectPickerClaimsNoTimeItCannotKnow is the truthfulness fix.
//
// The table behind the column is one measurement of one screen at 80x24. The
// effects that do work per character take longer on a bigger screen with more
// text on it, and the spread is not small: pour is 3.1 seconds over a bare
// prompt and 97.8 seconds at 200x50 over a full one. So the picker may not put
// a time on the panel. It used to put two: "35s" on the row and "The screen
// comes back after about 35 seconds." under the list.
//
// A band is what survives. This holds the panel to one.
//
// Negative control: returning strconv.Itoa(int(seconds+0.5))+"s" from
// effectOpeningWord, or the old sentence from effectOpeningSentence, fails
// this.
func TestEffectPickerClaimsNoTimeItCannotKnow(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 40)
	_ = m.OpenEffectPicker()

	digits := regexp.MustCompile(`[0-9]`)
	clock := regexp.MustCompile(`(?i)second|minute|\bsec\b`)

	checked := 0
	for i, name := range m.effectPickerItems() {
		m.EffectPickerSelected = i
		m.buildEffectPreview()
		_, status := m.effectDetailText(name)
		for _, s := range []string{status, effectOpeningWord(effectOpeningBandOf(name))} {
			if s == "" {
				continue
			}
			checked++
			if digits.MatchString(s) {
				t.Errorf("%s is given a figure the picker cannot know: %q", name, s)
			}
			if clock.MatchString(s) {
				t.Errorf("%s is given a time the picker cannot know: %q", name, s)
			}
		}
	}
	if checked < 60 {
		t.Fatalf("only %d strings were checked; the gather is not reaching them", checked)
	}
}

// TestEffectPickerSaysTheTimeDependsOnTheScreen: dropping the number is half
// the fix. Somebody who reads "long" and comes back to a screen that took twice
// as long has still been told something that was not true of their screen, so
// the panel has to say what moves it.
//
// The none band is exempt and says something stronger instead. See
// TestEffectsWithNoOpeningNeverHideTheScreen.
//
// Negative control: cutting the second sentence out of effectOpeningSentence
// fails this.
func TestEffectPickerSaysTheTimeDependsOnTheScreen(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 40)
	_ = m.OpenEffectPicker()

	const caveat = "The time depends on your screen."
	hiding, quoted := 0, 0
	for i, name := range m.effectPickerItems() {
		if name == config.ScreensaverRandomEffect {
			continue
		}
		band := effectOpeningBandOf(name)
		if band == effectOpeningNone || band == effectOpeningUnknown {
			continue
		}
		hiding++
		m.EffectPickerSelected = i
		m.buildEffectPreview()
		if _, status := m.effectDetailText(name); strings.Contains(status, caveat) {
			quoted++
		} else {
			t.Errorf("%s hides the screen and the panel does not say what sets the time: %q", name, status)
		}
	}
	if hiding < 30 {
		t.Fatalf("only %d effects hide the screen; the gather is not reaching them", hiding)
	}
	if quoted != hiding {
		t.Errorf("%d of %d hiding effects carry the caveat", quoted, hiding)
	}

	// And it is on the panel, not only in the string.
	idx := effectRowIndex(m, "swarm")
	_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
	if body := pickerBody(t, m); !strings.Contains(body, caveat) {
		t.Errorf("the caveat is not on the panel; body:\n%s", body)
	}
}

// TestEffectOpeningBandsSeparateFastFromSlow: a band that says the same thing
// for every effect is a column of nothing. The order it puts them in is the
// part of the old measurement that survives a change of screen, so it has to be
// visible.
//
// Negative control: returning one word from effectOpeningWord, or dropping the
// boundaries so every effect lands in one band, fails this.
func TestEffectOpeningBandsSeparateFastFromSlow(t *testing.T) {
	want := []struct {
		name string
		band effectOpeningBand
		word string
	}{
		{"highlight", effectOpeningNone, "none"},
		{"wipe", effectOpeningShort, "short"},
		{"rain", effectOpeningMedium, "medium"},
		{"swarm", effectOpeningLong, "long"},
	}
	seen := map[string]bool{}
	for _, w := range want {
		got := effectOpeningBandOf(w.name)
		if got != w.band {
			t.Errorf("%s is in band %d, want %d", w.name, got, w.band)
		}
		word := effectOpeningWord(got)
		if word != w.word {
			t.Errorf("%s reads %q in the column, want %q", w.name, word, w.word)
		}
		if seen[word] {
			t.Errorf("%s repeats the column word %q, so the column carries no order", w.name, word)
		}
		seen[word] = true
		if sentence := effectOpeningSentence(got); sentence == "" {
			t.Errorf("%s gets no sentence under the list", w.name)
		}
	}

	// Every band word fits the column it is drawn in, or the name beside it is
	// cut to make room.
	for _, band := range []effectOpeningBand{
		effectOpeningNone, effectOpeningShort, effectOpeningMedium, effectOpeningLong,
	} {
		if n := len(effectOpeningWord(band)); n > effectOpeningColumn {
			t.Errorf("band %d reads %q, %d cells in a %d-cell column",
				band, effectOpeningWord(band), n, effectOpeningColumn)
		}
	}

	// random has no one opening, so it claims none.
	if got := effectOpeningBandOf(config.ScreensaverRandomEffect); got != effectOpeningUnknown {
		t.Errorf("random is in band %d, want no band at all", got)
	}
	if word := effectOpeningWord(effectOpeningUnknown); word != "" {
		t.Errorf("random carries %q in the column", word)
	}
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
// whole claim on every frame: every captured glyph legible, in its own place,
// no share of it missing. Six screen shapes, from a phone-sized terminal to a
// wide one, because the effects that failed this failed it by size.
//
// highlight passes. It sweeps a band of brighter colour over text that never
// moves.
//
// Negative control: setting keepsScreen on rings, vhstape, waves, thunderstorm
// or burn fails this, naming the frame the screen goes away on.
func TestEffectsWithNoOpeningNeverHideTheScreen(t *testing.T) {
	var none []string
	for name := range effectOpenings {
		if effectOpeningBandOf(name) == effectOpeningNone {
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
			want := effectSettledScreen(t, p, name)
			if len(want) < 20 {
				t.Fatalf("setup: %s settles on %d glyph cells at %dx%d, not a screen worth hiding",
					name, len(want), screen.w, screen.h)
			}

			d, _ := tfx.Lookup(name)
			effect := d.New()
			engine, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, effect, d.NeedsFillCharacters)
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
					"only %d of %d captured glyphs are readable",
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
	engine, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, effect, d.NeedsFillCharacters)
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
		if _, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, d.New(), d.NeedsFillCharacters); !ok {
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

// TestEffectPickerRowHitsComeFromTheRenderer: hit rectangles are recorded by
// the renderer as it draws and never recomputed in a handler, so a click lands
// where the user is pointing whatever the panel reflowed to.
//
// Negative control: returning nil rows from renderEffectPicker, or shifting the
// row rects by the search line's two rows, fails this.
func TestEffectPickerRowHitsComeFromTheRenderer(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 40)
	_ = m.OpenEffectPicker()

	content, geo, rows := m.renderEffectPicker()
	if len(rows) == 0 {
		t.Fatal("the renderer recorded no row rects, so the picker cannot be clicked")
	}
	lines := strings.Split(stripANSIForTrace(content), "\n")
	items := m.effectPickerItems()
	for _, r := range rows {
		if r.Rect.Y0 < 0 || r.Rect.Y0 >= len(lines) {
			t.Fatalf("row %d recorded at y %d, outside the %d-line panel", r.Idx, r.Rect.Y0, len(lines))
		}
		name := items[r.Idx]
		if !strings.Contains(lines[r.Rect.Y0], name) {
			t.Errorf("row %d claims y %d, but that line is %q and the row is %q",
				r.Idx, r.Rect.Y0, strings.TrimSpace(lines[r.Rect.Y0]), name)
		}
		if r.Rect.X1 != geo.Width {
			t.Errorf("row %d is %d wide, want the panel's %d", r.Idx, r.Rect.X1, geo.Width)
		}
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

// TestEffectPickerStringsArePlain keeps the runtime text this feature adds
// inside the house style: one idea a sentence, under twenty words, sentence
// case, and no dash standing in for a verb.
//
// It reads the strings before they are laid out, not off the panel. Rendered
// text is truncated to the panel width, so a sentence in the wrong voice comes
// back inside the word count with a mark on the end and the check passes on a
// string nobody would want to ship.
//
// The engine's own descriptions are not in scope: they belong to tuiffects and
// rewriting them here would only make the two drift.
//
// Negative control: putting any of these in the commit-message voice fails it.
func TestEffectPickerStringsArePlain(t *testing.T) {
	m, _ := effectPickerOS(t, 100, 40)
	_ = m.OpenEffectPicker()

	var strs []string
	for _, item := range m.settingsCurrentItems() {
		if item.Path == "screensaver.effect" {
			strs = append(strs, item.Label, item.Desc)
		}
	}
	for i, name := range m.effectPickerItems() {
		m.EffectPickerSelected = i
		m.buildEffectPreview()
		description, status := m.effectDetailText(name)
		strs = append(strs, status)
		if name == config.ScreensaverRandomEffect {
			strs = append(strs, description)
		}
	}
	// The resize note, which only shows after a resize.
	m.effectPreview.resized = true
	_, status := m.effectDetailText("wipe")
	strs = append(strs, status, "Screen saver effect", "No matching effects",
		"No effect matches ")
	for _, h := range effectPickerHints {
		strs = append(strs, h.Label)
	}

	checked := 0
	for _, s := range strs {
		if s == "" {
			continue
		}
		checked++
		if strings.ContainsAny(s, "—–") {
			t.Errorf("a dash is doing a verb's work: %q", s)
		}
		if n := len(strings.Fields(s)); n > 20 {
			t.Errorf("%d words in one string: %q", n, s)
		}
		for _, sentence := range strings.Split(s, ". ") {
			if n := len(strings.Fields(sentence)); n > 20 {
				t.Errorf("%d words in one sentence: %q", n, sentence)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d strings were checked; the gather is not reaching them", checked)
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
