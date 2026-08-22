package config_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// colorOptionPaths are the options that hold a colour. The registry is the
// source of truth for which those are, so this reads them back rather than
// listing them again: an option marked Color later is covered by these tests
// the moment it is marked.
func colorOptionPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, opt := range config.Options() {
		if opt.Color {
			paths = append(paths, opt.Path)
		}
	}
	if len(paths) == 0 {
		t.Fatal("the registry marks no option as holding a colour")
	}
	return paths
}

// TestColorOptionsAreMarked pins the three colour options, because the settings
// panel offers a picker for exactly the ones the registry marks and a colour
// that lost its mark would quietly go back to being a text field.
func TestColorOptionsAreMarked(t *testing.T) {
	want := map[string]bool{
		"appearance.border_focused_color":   true,
		"appearance.border_unfocused_color": true,
		"appearance.scrollbar.tint":         true,
	}
	got := map[string]bool{}
	for _, p := range colorOptionPaths(t) {
		got[p] = true
	}
	for p := range want {
		if !got[p] {
			t.Errorf("%s is a colour but the registry does not say so", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("%s is marked as a colour and this test did not expect it; add it here if that is right", p)
		}
	}
}

// TestSetOptionRefusesANonColour is the bug this closes: the two border colours
// carried no accepted set, so set-option took anything and the border was drawn
// in nothing afterwards.
func TestSetOptionRefusesANonColour(t *testing.T) {
	for _, path := range colorOptionPaths(t) {
		cfg := config.DefaultConfig()
		err := config.SetOptionValue(cfg, path, "notacolour")
		if err == nil {
			t.Errorf("%s: set-option took a value that is not a colour", path)
			continue
		}
		if !strings.Contains(err.Error(), "#RRGGBB") {
			t.Errorf("%s: the error does not say what a colour looks like: %v", path, err)
		}
	}
}

// TestSetOptionTakesAColourAndClearsIt covers the three states a colour option
// has: a literal, its keywords where it has them, and empty for unset. Clearing
// has to keep working or the picker could set a colour and never take it back.
func TestSetOptionTakesAColourAndClearsIt(t *testing.T) {
	for _, path := range colorOptionPaths(t) {
		opt, ok := config.LookupOption(path)
		if !ok {
			t.Fatalf("%s vanished from the registry", path)
		}
		cfg := config.DefaultConfig()
		if err := config.SetOptionValue(cfg, path, "#89b4fa"); err != nil {
			t.Errorf("%s: refused a colour literal: %v", path, err)
		}
		if got, _ := config.GetOptionValue(cfg, path); got != "#89b4fa" {
			t.Errorf("%s: stored %q, want the literal back", path, got)
		}
		if err := config.SetOptionValue(cfg, path, ""); err != nil {
			t.Errorf("%s: refused to clear: %v", path, err)
		}
		if got, _ := config.GetOptionValue(cfg, path); got != "" {
			t.Errorf("%s: clearing left %q behind", path, got)
		}
		for _, kw := range opt.Accepted {
			if err := config.SetOptionValue(cfg, path, kw); err != nil {
				t.Errorf("%s: refused its own keyword %q: %v", path, kw, err)
			}
		}
	}
}

// TestBorderColorWarnsAtLoad proves the file path is covered too. A config
// edited by hand never goes through SetOptionValue, so the validator is the only
// thing that can say the border colour is broken.
func TestBorderColorWarnsAtLoad(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Appearance.BorderFocusedColor = "blue"
	cfg.Appearance.BorderUnfocusedColor = "#585b70"

	result := config.ValidateConfig(cfg)
	var keys []string
	for _, w := range result.Warnings {
		keys = append(keys, w.Key)
	}
	if !contains(keys, "border_focused_color") {
		t.Errorf("a border colour of \"blue\" drew no warning; got %v", keys)
	}
	if contains(keys, "border_unfocused_color") {
		t.Errorf("a valid #RRGGBB border colour was warned about; got %v", keys)
	}
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
