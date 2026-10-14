package config

import "testing"

// The e2e tests in e2e/tui/split_prefix_test.go see the default panel on
// screen; this one stays for what a screen test cannot pin down cheaply: a
// rebind, a key bound twice, the sort order and the cancel row being last.
//
// TestSubPrefixPanelShowsTheConfig: a nested menu's which-key panel lists
// what the config binds, rebinds included, marks a key that opens another
// menu, and puts the cancel last.
func TestSubPrefixPanelShowsTheConfig(t *testing.T) {
	cfg := DefaultConfig()
	k := &cfg.Keybindings
	k.SplitPrefix["split_prefix_vertical"] = []string{"v", "|"}
	k.SplitPrefix["window_prefix_move"] = []string{"g"}

	rows := SubPrefixKeybindingGroups(k, SectionSplitPrefix)[0].Bindings
	byKey := map[string]Keybinding{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	for action, keys := range k.SplitPrefix {
		if len(keys) == 0 {
			continue
		}
		key := keys[0]
		for _, more := range keys[1:] {
			key += "/" + more
		}
		if _, ok := byKey[key]; !ok {
			t.Errorf("the split panel has no row %q for %s", key, action)
		}
	}
	if r := byKey["g"]; !r.Submenu || r.Description != "Move" {
		t.Errorf("g opens the move menu, and its row is %+v", r)
	}
	if !rows[0].Submenu {
		t.Errorf("a menu row should come first, got %+v", rows[0])
	}
	if last := rows[len(rows)-1]; last.Key != "esc" {
		t.Errorf("the last row is %+v, want the cancel", last)
	}
}

// TestMenuDefaultsYieldToAUserBinding: the keys the nested menus added to the
// leader's menu, the window prefix and the workspace prefix must not take a key
// the user's own table already binds. The duplicate cleanup hands a contested
// key to the action that owns it by default, so without yielding the user's
// binding would lose it on the next load.
func TestMenuDefaultsYieldToAUserBinding(t *testing.T) {
	cfg, err := ParseUserConfig([]byte(`[keybindings.prefix_mode]
prefix_help = ["G"]

[keybindings.window_prefix]
window_prefix_rename = ["s"]

[keybindings.workspace_prefix]
workspace_prefix_rename = ["n"]
`))
	if err != nil {
		t.Fatal(err)
	}
	k := cfg.Keybindings
	if got := k.PrefixMode["prefix_help"]; len(got) != 1 || got[0] != "G" {
		t.Errorf("prefix_help = %q, want the user's [G]", got)
	}
	if _, ok := k.PrefixMode["prefix_session"]; ok {
		t.Errorf("prefix_session took G from prefix_help: %q", k.PrefixMode["prefix_session"])
	}
	if got := k.WindowPrefix["window_prefix_rename"]; len(got) != 1 || got[0] != "s" {
		t.Errorf("window_prefix_rename = %q, want the user's [s]", got)
	}
	if _, ok := k.WindowPrefix["window_prefix_split"]; ok {
		t.Errorf("window_prefix_split took s from window_prefix_rename")
	}
	if got := k.WorkspacePrefix["workspace_prefix_rename"]; len(got) != 1 || got[0] != "n" {
		t.Errorf("workspace_prefix_rename = %q, want the user's [n]", got)
	}
	if _, ok := k.WorkspacePrefix["next_workspace"]; ok {
		t.Errorf("next_workspace took n from workspace_prefix_rename")
	}
	// The positive half: a key the user left free gets its default.
	if got := k.WindowPrefix["window_prefix_resize"]; len(got) != 1 || got[0] != "R" {
		t.Errorf("window_prefix_resize = %q, want the default [R]", got)
	}
	if got := k.WorkspacePrefix["prev_workspace"]; len(got) != 1 || got[0] != "p" {
		t.Errorf("prev_workspace = %q, want the default [p]", got)
	}
	if got := k.PrefixMode["prefix_tools"]; len(got) != 1 || got[0] != "U" {
		t.Errorf("prefix_tools = %q, want the default [U]", got)
	}
}
