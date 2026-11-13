package config

import (
	"strings"
	"testing"
)

// A [[keybindings.command]] entry can name one of the leader's submenus with
// scope, so a user command can run on ctrl+b m g. Before this a command key was
// only ever a single key after the leader (prefix+) or a global key, and every
// multi-key spelling the reporter of #1723 tried loaded nothing.
//
// How these tests could pass wrongly, written down first:
//   - An entry wired into the leader's own menu instead of the submenu would
//     answer on two presses instead of three. Every lookup is checked in its
//     own table, and the other tables must stay clear of the command.
//   - An entry that reaches the report but not the key map would show in
//     tuios keybinds list and do nothing when pressed, so Action, Bindings and
//     the which-key rows are all checked.
//   - Adding scope support could break the two forms that already work. Both
//     are asserted alongside the new assertions: the positive half of every
//     negative control.
//   - A hardcoded one-off for minimize would leave the other submenus out, so
//     every submenu is checked with its own table.

const submenuCommandConfig = `
[[keybindings.command]]
scope = "prefix.minimize"
key = "g"
type = "shell"
command = "echo submenu"
description = "Submenu command"
name = "submenu"

[[keybindings.command]]
key = "prefix+alt+g"
command = "lazygit"
name = "lazygit"

[[keybindings.command]]
key = "alt+t"
command = "htop"
name = "htop"
`

func parseSubmenuConfig(t *testing.T, toml string) (*UserConfig, *KeybindRegistry) {
	t.Helper()
	cfg, err := ParseUserConfig([]byte(toml))
	if err != nil {
		t.Fatal(err)
	}
	if res := ValidateConfig(cfg); len(res.Errors) != 0 {
		t.Fatalf("errors = %+v", res.Errors)
	}
	return cfg, NewKeybindRegistry(cfg)
}

// The submenu is either named by the scope tuios keybinds list shows or by the
// config section it comes from. Both must land on the same table.
func TestCommandEntryInSubmenuScope(t *testing.T) {
	for _, scope := range []string{"prefix.minimize", "minimize_prefix"} {
		t.Run(scope, func(t *testing.T) {
			toml := strings.Replace(submenuCommandConfig, `scope = "prefix.minimize"`, `scope = "`+scope+`"`, 1)
			_, r := parseSubmenuConfig(t, toml)

			// The positive half: the command runs on the third press.
			if got := r.Action(TableMinimizePrefix, "g"); got != "command:submenu" {
				t.Errorf("ctrl+b m g = %q, want command:submenu", got)
			}
			// The negative half: three presses, not two, and not a global key.
			if got := r.Action(TablePrefixMode, "g"); got == "command:submenu" {
				t.Error("the submenu command answers on the leader's own menu")
			}
			if got := r.Action(TableGlobal, "g"); got == "command:submenu" {
				t.Error("the submenu command answers globally")
			}

			// Positive controls: the two forms shipped before this still work.
			if got := r.Action(TablePrefixMode, "alt+g"); got != "command:lazygit" {
				t.Errorf("prefix alt+g = %q, want command:lazygit", got)
			}
			if got := r.Action(TableGlobal, "alt+t"); got != "command:htop" {
				t.Errorf("global alt+t = %q, want command:htop", got)
			}
		})
	}
}

// Every leader submenu is a target, not just minimize. alt+j is free in all of
// the shipped tables, so a built-in cannot be the reason a lookup answers.
func TestCommandEntryTargetsOtherSubmenus(t *testing.T) {
	cases := []struct {
		scope string
		table Table
	}{
		{"prefix.window", TableWindowPrefix},
		{"prefix.minimize", TableMinimizePrefix},
		{"prefix.workspace", TableWorkspacePrefix},
		{"prefix.debug", TableDebugPrefix},
		{"prefix.tape", TableTapePrefix},
		{"prefix.layout", TableLayoutPrefix},
		{"prefix.tuios", TableTuiosPrefix},
		{"prefix", TablePrefixMode},
	}
	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			toml := "[[keybindings.command]]\nscope = \"" + tc.scope + "\"\nkey = \"alt+j\"\ncommand = \"x\"\nname = \"sub\"\n"
			_, r := parseSubmenuConfig(t, toml)
			if got := r.Action(tc.table, "alt+j"); got != "command:sub" {
				t.Errorf("%s alt+j = %q, want command:sub", tc.scope, got)
			}
			if got := r.Action(TableGlobal, "alt+j"); got == "command:sub" {
				t.Error("a scoped entry also answers globally")
			}
		})
	}
}

// A key bound in a submenu must be visible where a person looks: the
// keybinds list, with the whole chord, and the which-key menu of the submenu.
func TestCommandEntryInSubmenuIsReported(t *testing.T) {
	_, r := parseSubmenuConfig(t, submenuCommandConfig)

	found := false
	for _, b := range r.Bindings() {
		if b.Action != "command:submenu" {
			continue
		}
		found = true
		if b.Scope != ScopePrefixMinimize || b.Section != SectionCommand ||
			b.Key != "g" || b.Press != DefaultLeaderKey+" m g" || b.Shadowed {
			t.Errorf("binding = %+v, want a live g in prefix.minimize pressed as %q", b, DefaultLeaderKey+" m g")
		}
	}
	if !found {
		t.Fatal("tuios keybinds list has no row for the submenu command")
	}

	var rows []Keybinding
	for _, g := range PrefixMenuGroups(r, "minimize", MenuState{}) {
		rows = append(rows, g.Bindings...)
	}
	found = false
	for _, k := range rows {
		if k.Action == "command:submenu" {
			found = true
			if k.Key != "g" || k.Description != "Submenu command" {
				t.Errorf("which-key row = %+v, want g / Submenu command", k)
			}
		}
	}
	if !found {
		t.Fatal("the ctrl+b m which-key menu has no row for the command")
	}
}

// f in the minimize submenu is the built-in focus-only action (#1769). A
// command must not take a built-in's key in a submenu any more than it can on
// the leader's own menu.
func TestCommandEntrySubmenuLosesABuiltinKey(t *testing.T) {
	toml := "[[keybindings.command]]\nscope = \"prefix.minimize\"\nkey = \"f\"\ncommand = \"x\"\nname = \"focused\"\n"
	_, r := parseSubmenuConfig(t, toml)
	if got := r.Action(TableMinimizePrefix, "f"); got != "minimize_prefix_focus_only" {
		t.Errorf("ctrl+b m f = %q, want minimize_prefix_focus_only", got)
	}
	found := false
	for _, b := range r.Bindings() {
		if b.Action != "command:focused" {
			continue
		}
		found = true
		if !b.Shadowed || b.ShadowedBy != "minimize_prefix_focus_only" {
			t.Errorf("binding = %+v, want dead behind minimize_prefix_focus_only", b)
		}
	}
	if !found {
		t.Fatal("Bindings has no row for the entry")
	}
}

// A scope tuios does not know, or a scope combined with the old prefix+ form,
// is a warning and an ignored entry, never a silently global command.
func TestCommandEntrySubmenuScopeValidation(t *testing.T) {
	// The positive half lives in the same fixture as each bad entry. If
	// validation ever started rejecting every scoped entry, or warning about
	// a good one, a test that only counted the survivors would still pass.
	const good = `
[[keybindings.command]]
scope = "prefix.minimize"
key = "alt+k"
type = "shell"
command = "echo good"
name = "good"
`
	cases := []struct {
		name, scope, key, want string
	}{
		{"unknown scope", "prefix.nope", "g", "scope"},
		{"a mode is not a submenu", "window", "g", "scope"},
		{"prefix+ with a scope", "prefix.minimize", "prefix+g", "prefix+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toml := "[[keybindings.command]]\nscope = \"" + tc.scope + "\"\nkey = \"" + tc.key + "\"\ncommand = \"x\"\nname = \"bad\"\n" + good
			cfg, err := ParseUserConfig([]byte(toml))
			if err != nil {
				t.Fatal(err)
			}
			res := ValidateConfig(cfg)
			if len(res.Errors) != 0 {
				t.Fatalf("errors = %+v, want warnings only", res.Errors)
			}
			var msgs []string
			for _, w := range res.Warnings {
				if w.Key == "alt+k" {
					t.Errorf("the valid scoped entry was warned about: %+v", w)
				}
				msgs = append(msgs, w.Message)
			}
			if !strings.Contains(strings.Join(msgs, " | "), tc.want) {
				t.Fatalf("warnings = %q, want %q", msgs, tc.want)
			}
			cmds := cfg.Keybindings.Commands()
			if len(cmds) != 1 || cmds[0].ResolvedName() != "good" {
				t.Fatalf("kept %+v, want only the valid scoped entry", cmds)
			}
			if got := NewKeybindRegistry(cfg).Action(TableMinimizePrefix, "alt+k"); got != "command:good" {
				t.Errorf("the valid scoped entry does not dispatch: %q", got)
			}
		})
	}
}
