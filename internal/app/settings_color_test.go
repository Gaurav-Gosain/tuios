package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestEveryColourOptionGetsThePicker is the coverage guard. The registry says
// which options hold a colour; this says every one of them has a row that opens
// the picker. An option marked Color later and left out of colorSettings would
// stay a text field, which is the bug this whole change is about.
func TestEveryColourOptionGetsThePicker(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	rows := map[string]settingItem{}
	for _, cat := range m.settingsCategories() {
		for _, it := range cat.Items {
			rows[it.Label] = it
		}
	}

	for _, opt := range config.Options() {
		if !opt.Color {
			continue
		}
		cs, ok := lookupColorSetting(opt.Path)
		if !ok {
			t.Errorf("%s holds a colour and has no colour setting; it would still be a text field", opt.Path)
			continue
		}
		row, ok := rows[cs.Label]
		if !ok {
			t.Errorf("%s has a colour setting but no row labelled %q in the panel", opt.Path, cs.Label)
			continue
		}
		if row.Control != controlColor {
			t.Errorf("%s: row %q is control %d, want controlColor", opt.Path, cs.Label, row.Control)
		}
		if row.activate == nil {
			t.Errorf("%s: row %q has no activate hook, so Enter opens nothing", opt.Path, cs.Label)
		}
		if row.swatch == nil {
			t.Errorf("%s: row %q shows no swatch, which makes it a list of strings", opt.Path, cs.Label)
		}
	}
}

// TestColourRowHasNoStepper pins that a colour row records no stepper arrows.
// Arrows on a colour row would step to a next colour that does not exist, and
// the rects for them would swallow the clicks meant for the picker.
func TestColourRowHasNoStepper(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	item := focusSetting(t, m, "Appearance", "Focused border color")
	if item.adjust != nil {
		t.Error("a colour row carries an adjust hook")
	}
	if cmd := m.SettingsAdjust(1); cmd != nil {
		t.Error("stepping a colour row queued a save, so it changed something")
	}
	_, _, stepless := m.settingsRow(item, true, theme.UI(), 60)
	if !stepless {
		t.Error("the colour row asked for stepper rects")
	}
}

// openPickerOn selects a colour row and opens its picker.
func openPickerOn(t *testing.T, m *OS, label string) {
	t.Helper()
	item := focusSetting(t, m, "Appearance", label)
	if item.activate == nil {
		t.Fatalf("%s has no activate hook", label)
	}
	if cmd := m.SettingsActivate(); cmd != nil {
		t.Fatalf("%s: opening the picker queued a save; it must write nothing until applied", label)
	}
	if !m.ShowAccentPicker {
		t.Fatalf("%s: the picker did not open", label)
	}
	if m.AccentPickerTarget != AccentTargetSetting {
		t.Fatalf("%s: the picker opened on target %d, want the setting", label, m.AccentPickerTarget)
	}
}

// TestPickerSetsAndClearsABorderColour walks the three states a border colour
// has: unset, a colour, and back to unset. The last is the one a picker that
// could only set a value would have taken away.
func TestPickerSetsAndClearsABorderColour(t *testing.T) {
	useTempConfig(t)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	// Unset to begin with, so the row shows what it inherits rather than a value.
	item := focusSetting(t, m, "Appearance", "Focused border color")
	if got := item.value(m); got != "(theme)" {
		t.Fatalf("an unset border colour reads %q, want (theme)", got)
	}

	// Opening and applying without moving must write nothing: the picker opens on
	// the colour in force, and storing that would pin the border to whatever the
	// theme resolves to today.
	openPickerOn(t, m, "Focused border color")
	runSave(t, m.AccentPickerApply())
	if got := m.UserConfig.Appearance.BorderFocusedColor; got != "" {
		t.Errorf("applying an untouched picker pinned the border to %q", got)
	}

	// Pick a colour through the hex field, the way a user who knows the value
	// they want reaches it.
	openPickerOn(t, m, "Focused border color")
	m.AccentPickerFocusHex()
	for _, r := range "89b4fa" {
		m.AccentPickerHexKey(r)
	}
	runSave(t, m.AccentPickerApply())
	if got := m.UserConfig.Appearance.BorderFocusedColor; got != "#89b4fa" {
		t.Fatalf("the picked colour stored as %q, want #89b4fa", got)
	}
	if item := focusSetting(t, m, "Appearance", "Focused border color"); item.value(m) != "#89b4fa" {
		t.Errorf("the row still reads %q after the pick", item.value(m))
	}

	// And back to inheriting. This is the state the text field expressed only by
	// being empty and the picker has to reach.
	openPickerOn(t, m, "Focused border color")
	runSave(t, m.AccentPickerClear())
	if got := m.UserConfig.Appearance.BorderFocusedColor; got != "" {
		t.Errorf("clearing left %q behind", got)
	}
	if item := focusSetting(t, m, "Appearance", "Focused border color"); item.value(m) != "(theme)" {
		t.Errorf("after clearing the row reads %q, want (theme)", item.value(m))
	}
}

// TestPickedBorderColourAppliesLive is the other half: the config field is not
// the border. A value written and never pushed into the theme package is the
// failure mode the settings audit found in 82 of 88 options.
func TestPickedBorderColourAppliesLive(t *testing.T) {
	useTempConfig(t)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	t.Cleanup(func() { theme.SetBorderOverrides("", "") })

	before := theme.BorderFocusedWindow()

	openPickerOn(t, m, "Focused border color")
	m.AccentPickerFocusHex()
	for _, r := range "ff00aa" {
		m.AccentPickerHexKey(r)
	}
	runSave(t, m.AccentPickerApply())

	after := theme.BorderFocusedWindow()
	if theme.ContrastRatio(before, after) == 1 && sameColor(before, after) {
		t.Fatal("the border colour did not change; the pick was written and never applied")
	}
	if !sameColor(after, lipgloss.Color("#ff00aa")) {
		t.Errorf("the focused border is %v, want the picked #ff00aa", after)
	}

	// Clearing must put the theme's colour back, live, not only in the file.
	openPickerOn(t, m, "Focused border color")
	runSave(t, m.AccentPickerClear())
	if !sameColor(theme.BorderFocusedWindow(), before) {
		t.Error("clearing did not restore the theme's border colour on screen")
	}
}

// TestTintKeywordsReachableAndHexToo covers the option with all three kinds of
// value. The panel used to cycle its keywords and offer no way at all to reach
// the hex form the config accepts.
func TestTintKeywordsReachableAndHexToo(t *testing.T) {
	useTempConfig(t)
	orig := config.Global.ScrollbarTint
	t.Cleanup(func() { config.Global.ScrollbarTint = orig })
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	openPickerOn(t, m, "Scrollbar tint")
	if len(m.AccentPicker.NamedOpts) != len(config.ScrollbarTints) {
		t.Fatalf("the picker offers %d keywords, want %d", len(m.AccentPicker.NamedOpts), len(config.ScrollbarTints))
	}
	m.AccentPickerNamedValue(config.ScrollbarTintMuted)
	runSave(t, m.AccentPickerApply())
	if got := m.UserConfig.Appearance.Scrollbar.Tint; got != config.ScrollbarTintMuted {
		t.Errorf("the keyword stored as %q, want muted", got)
	}
	if m.Settings.ScrollbarTint != config.ScrollbarTintMuted {
		t.Errorf("the live tint is %q; the keyword was written and never applied", m.Settings.ScrollbarTint)
	}

	// The literal the enum cycler could never produce.
	openPickerOn(t, m, "Scrollbar tint")
	m.AccentPickerFocusHex()
	for _, r := range "00ff88" {
		m.AccentPickerHexKey(r)
	}
	runSave(t, m.AccentPickerApply())
	if got := m.UserConfig.Appearance.Scrollbar.Tint; got != "#00ff88" {
		t.Errorf("the tint stored as %q, want the literal #00ff88", got)
	}

	// And unset, which the renderer must read as the documented default rather
	// than falling through to the border rule.
	openPickerOn(t, m, "Scrollbar tint")
	runSave(t, m.AccentPickerClear())
	if got := m.UserConfig.Appearance.Scrollbar.Tint; got != "" {
		t.Errorf("clearing the tint left %q behind", got)
	}
	if got := m.Settings.ScrollbarTintResolved(); got != config.ScrollbarTintQuiet {
		t.Errorf("an unset tint resolves to %q, want quiet", got)
	}
}

// TestColourRowShowsTheColourInForce is the swatch's whole reason to exist: an
// unset row still paints the colour it is inheriting, so the panel says what the
// border looks like instead of only that it is the theme's.
func TestColourRowShowsTheColourInForce(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	item := focusSetting(t, m, "Appearance", "Unfocused border color")
	if item.swatch == nil {
		t.Fatal("the row has no swatch")
	}
	pal := theme.UI()
	if !sameColor(item.swatch(pal.Surface, &m.Settings), theme.BorderUnfocused()) {
		t.Error("the swatch is not the colour the border is actually drawn in")
	}

	row, _, _ := m.settingsRow(item, true, pal, 60)
	if !strings.Contains(row, "(theme)") {
		t.Errorf("the unset row does not say where its colour comes from:\n%q", row)
	}
}
