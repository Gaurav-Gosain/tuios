package config

import (
	"slices"
	"strings"
	"testing"
)

const nestedMenusTOML = `
[keybindings.window_prefix]
"open_menu:split" = ["s"]

[keybindings.menus.split]
title = "Split"

[keybindings.menus.split.keys]
prefix_split_vertical = ["y"]
"open_menu:resize" = ["R"]

[keybindings.menus.resize]
sticky = true

[keybindings.menus.resize.keys]
resize_pane_left_5 = ["h"]
`

func TestDeclaredMenusReadFromConfig(t *testing.T) {
	cfg := loadTOML(t, nestedMenusTOML)
	r := NewKeybindRegistry(cfg)

	if got := r.GetMenuAction("split", "y"); got != "prefix_split_vertical" {
		t.Errorf("split y = %q, want prefix_split_vertical", got)
	}
	if got := r.GetMenuAction("resize", "h"); got != "resize_pane_left_5" {
		t.Errorf("resize h = %q, want resize_pane_left_5", got)
	}
	// A key lives in its own menu alone.
	if got := r.GetMenuAction("resize", "y"); got != "" {
		t.Errorf("resize y = %q, want unbound", got)
	}
	if got := r.GetWindowPrefixAction("s"); got != "open_menu:split" {
		t.Errorf("window prefix s = %q, want open_menu:split", got)
	}
	if !cfg.Keybindings.Menus["resize"].Sticky {
		t.Error("sticky = true did not load")
	}
}

// The press the report gives for a key in a menu is the whole path to it,
// through however many menus it sits under.
func TestMenuScopeChordFollowsTheOpeningKeys(t *testing.T) {
	cfg := loadTOML(t, nestedMenusTOML)
	var chords = map[string]string{}
	for _, b := range NewKeybindRegistry(cfg).Bindings() {
		if b.Section == MenuSectionPrefix+"resize" {
			chords[b.Action] = b.Press
		}
	}
	want := cfg.Keybindings.LeaderKey
	if want == "" {
		want = DefaultLeaderKey
	}
	want += " t s R h"
	if got := chords["resize_pane_left_5"]; got != want {
		t.Errorf("press for resize h = %q, want %q", got, want)
	}
}

func TestMenuKeyProblemsAreWarnedAbout(t *testing.T) {
	cfg := loadTOML(t, `
[keybindings.prefix_mode]
"open_menu:ghost" = ["Z"]

[keybindings.menus.unopened.keys]
prefix_split_vertical = ["y"]

[keybindings.menus."Bad Name".keys]
prefix_split_vertical = ["y"]
`)
	var msgs []string
	for _, w := range ValidateConfig(cfg).Warnings {
		if strings.Contains(w.Field, "menus") || strings.HasPrefix(w.Key, "open_menu:") {
			msgs = append(msgs, w.Key+": "+w.Message)
		}
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{
		"open_menu:ghost: open_menu:ghost opens a menu that is not declared",
		"unopened: Nothing opens the menu unopened",
		"Bad Name: The menu name",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning containing %q in:\n%s", want, joined)
		}
	}
}

func TestMenuPanelListsMenusFirstAndCancelLast(t *testing.T) {
	cfg := loadTOML(t, nestedMenusTOML)
	rows := MenuKeybindingGroups(&cfg.Keybindings, "split")[0].Bindings
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	if want := []string{"R", "y", "esc"}; !slices.Equal(keys, want) {
		t.Fatalf("rows = %v, want %v", keys, want)
	}
	if !rows[0].Submenu || rows[0].Description != "resize" {
		t.Errorf("R row = %+v, want a submenu titled by the menu it opens", rows[0])
	}

	cfg.Keybindings.Menus["split"].Keys["menu_cancel"] = []string{"esc"}
	rows = MenuKeybindingGroups(&cfg.Keybindings, "split")[0].Bindings
	if n := len(rows); rows[n-1].Key == "esc" && rows[n-1].Description == "Cancel" {
		t.Errorf("esc is bound in the menu, but the panel still adds its own cancel row: %+v", rows)
	}
}

// unbind and free act on the keys of declared menus too, or a key could not be
// taken back from one.
func TestFreeKeyReachesDeclaredMenus(t *testing.T) {
	cfg := loadTOML(t, nestedMenusTOML)
	cfg.Keybindings.FreeKey("y")
	if keys := cfg.Keybindings.Menus["split"].Keys["prefix_split_vertical"]; slices.Contains(keys, "y") {
		t.Errorf("y still bound in the split menu: %v", keys)
	}
}
