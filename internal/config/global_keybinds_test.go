package config_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestGlobalBindsAreInTheRegistry is the whole complaint in one assertion: the
// palette and the launcher were literals in the input path, so nothing could
// read them and nothing could change them.
func TestGlobalBindsAreInTheRegistry(t *testing.T) {
	r := config.NewKeybindRegistry(config.DefaultConfig())
	for key, want := range map[string]string{
		"ctrl+p":    "command_palette",
		"alt+space": "launcher",
	} {
		if got := r.GetGlobalAction(key); got != want {
			t.Errorf("GetGlobalAction(%q) = %q, want %q", key, got, want)
		}
	}
	if got := r.GetGlobalAction("ctrl+q"); got != "" {
		t.Errorf("an unbound key resolved to %q", got)
	}
}

// TestGlobalBindsAppearInTheReport pins that a registered bind is visible to
// `tuios keybinds doctor`. A hardcoded key is invisible to it, which is most of
// why hardcoding it was the bug.
func TestGlobalBindsAppearInTheReport(t *testing.T) {
	r := config.NewKeybindRegistry(config.DefaultConfig())
	want := map[string]bool{"ctrl+p": false, "alt+space": false}
	for _, b := range r.Bindings() {
		if b.Scope != config.ScopeGlobal {
			continue
		}
		if _, ok := want[b.Key]; ok {
			want[b.Key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("%s is not in the global scope's bindings", key)
		}
	}
}

// TestGlobalBindCanBeUnbound pins that an empty list turns an action off.
// "Hackable" includes turning something off, and a user who wants fish's
// history-back on ctrl+p has no other way to get it.
func TestGlobalBindCanBeUnbound(t *testing.T) {
	cfg := writeConfig(t, strings.Join([]string{
		"[keybindings.global]",
		"command_palette = []",
		"",
	}, "\n"))
	r := config.NewKeybindRegistry(cfg)
	if got := r.GetGlobalAction("ctrl+p"); got != "" {
		t.Errorf("command_palette = [] still resolves ctrl+p to %q", got)
	}
	// The rest of the section still fills from defaults, so unbinding one action
	// does not silently take the other with it.
	if got := r.GetGlobalAction("alt+space"); got != "launcher" {
		t.Errorf("launcher lost its default when the palette was unbound: %q", got)
	}
}

// TestGlobalBindCanBeRebound is the on-screen check in test form.
func TestGlobalBindCanBeRebound(t *testing.T) {
	cfg := writeConfig(t, strings.Join([]string{
		"[keybindings.global]",
		`command_palette = ["ctrl+g"]`,
		"",
	}, "\n"))
	r := config.NewKeybindRegistry(cfg)
	if got := r.GetGlobalAction("ctrl+g"); got != "command_palette" {
		t.Errorf("ctrl+g = %q, want command_palette", got)
	}
	if got := r.GetGlobalAction("ctrl+p"); got != "" {
		t.Errorf("the old key still opens the palette: ctrl+p = %q", got)
	}
}

// TestSettingsCommaMigratesOffTheDeadResize covers the config an existing user
// already has on disk. "," has been open_settings in practice since the literal
// landed; the stale layout claim would now be reported as a conflict for a key
// the user never chose.
func TestSettingsCommaMigratesOffTheDeadResize(t *testing.T) {
	cfg := writeConfig(t, strings.Join([]string{
		"[keybindings.layout]",
		`resize_master_shrink_left = [","]`,
		"",
	}, "\n"))
	if keys, ok := cfg.Keybindings.Layout["resize_master_shrink_left"]; ok {
		t.Errorf("the stale claim survived: %q", keys)
	}
	for _, w := range config.ValidateConfig(cfg).Warnings {
		if strings.Contains(w.Message, "','") {
			t.Errorf("an existing config warns about \",\": %s", w.Message)
		}
	}
}

// TestOwnResizeCommaIsLeftAlone guards the other half: a user who deliberately
// put "," on the resize action keeps it, warning and all. Quietly deleting
// someone's binding to tidy a report would be the worse bug.
func TestOwnResizeCommaIsLeftAlone(t *testing.T) {
	cfg := writeConfig(t, strings.Join([]string{
		"[keybindings.layout]",
		`resize_master_shrink_left = [",", "ctrl+alt+left"]`,
		"",
	}, "\n"))
	keys := cfg.Keybindings.Layout["resize_master_shrink_left"]
	if len(keys) != 2 || keys[0] != "," {
		t.Errorf("a deliberate binding was rewritten: %q", keys)
	}
}

// TestScriptScopeIsSeparate pins that script playback is its own keyboard
// context. It shares ctrl+p with the palette by default, and that is not a
// conflict because only one of the two contexts is ever active.
func TestScriptScopeIsSeparate(t *testing.T) {
	r := config.NewKeybindRegistry(config.DefaultConfig())
	if got := r.GetScriptAction("ctrl+p"); got != "script_pause" {
		t.Errorf("GetScriptAction(ctrl+p) = %q, want script_pause", got)
	}
	for _, b := range r.Bindings() {
		if b.Scope == config.ScopeScript && b.Shadowed {
			t.Errorf("script_pause is reported as shadowed by %q; the scopes are separate", b.ShadowedBy)
		}
	}
}
