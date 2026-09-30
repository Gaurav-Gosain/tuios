package config

import (
	"strings"
	"testing"
)

const commandConfig = `
[[keybindings.command]]
key = "prefix+alt+g"
type = "scratch"
command = "lazygit"
description = "Lazygit"

[[keybindings.command]]
key = "alt+t"
command = "htop | cat"
width = "60"

[[keybindings.command]]
key = "prefix+alt+p"
type = "pane"
command = "make test"
name = "tests"

[[keybindings.command]]
key = "prefix+alt+s"
type = "shell"
command = "touch /tmp/x"
description = "Touch x"
`

func TestCommandEntriesParse(t *testing.T) {
	cfg, err := ParseUserConfig([]byte(commandConfig))
	if err != nil {
		t.Fatal(err)
	}
	cmds := cfg.Keybindings.Commands()
	if len(cmds) != 4 {
		t.Fatalf("commands = %d, want 4", len(cmds))
	}
	want := []struct{ name, typ, section, key string }{
		{"lazygit", CommandTypeScratch, SectionPrefixMode, "alt+g"},
		{"htop-cat", CommandTypePopup, SectionGlobal, "alt+t"},
		{"tests", CommandTypePane, SectionPrefixMode, "alt+p"},
		{"touch-x", CommandTypeShell, SectionPrefixMode, "alt+s"},
	}
	for i, w := range want {
		c := cmds[i]
		if c.ResolvedName() != w.name || c.ResolvedType() != w.typ || c.Section() != w.section || c.BareKey() != w.key {
			t.Errorf("entry %d = %s %s %s %s, want %+v", i, c.ResolvedName(), c.ResolvedType(), c.Section(), c.BareKey(), w)
		}
	}
	if cmds[1].WidthSpec() != "60" || cmds[1].HeightSpec() != "80%" {
		t.Errorf("popup size = %s x %s", cmds[1].WidthSpec(), cmds[1].HeightSpec())
	}
	if cmds[1].Label() != "Run htop | cat" || cmds[0].Label() != "Lazygit" {
		t.Errorf("labels = %q, %q", cmds[0].Label(), cmds[1].Label())
	}
	if res := ValidateConfig(cfg); len(res.Errors) != 0 {
		t.Fatalf("errors = %+v", res.Errors)
	}
}

// The registry resolves an entry's key in its section: prefix+ after the
// leader, any other key globally.
func TestCommandEntriesReachTheKeyMap(t *testing.T) {
	cfg, err := ParseUserConfig([]byte(commandConfig))
	if err != nil {
		t.Fatal(err)
	}
	r := NewKeybindRegistry(cfg)
	if got := r.GetPrefixAction("alt+g"); got != "command:lazygit" {
		t.Errorf("prefix alt+g = %q", got)
	}
	if got := r.GetGlobalAction("alt+t"); got != "command:htop-cat" {
		t.Errorf("global alt+t = %q", got)
	}
	if got := r.GetGlobalAction("alt+g"); got == "command:lazygit" {
		t.Error("a prefix entry answers without the leader")
	}
	if _, ok := cfg.Keybindings.CommandFor("command:tests"); !ok {
		t.Error("CommandFor does not find the pane entry")
	}
}

// A command entry never takes a key a built-in action has. The doctor sees
// the entry as shadowed.
func TestCommandEntryLosesABuiltinKey(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\nkey = \"prefix+g\"\ncommand = \"lazygit\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewKeybindRegistry(cfg)
	if got := r.GetPrefixAction("g"); got != "toggle_scratch" {
		t.Fatalf("prefix g = %q, want toggle_scratch", got)
	}
	found := false
	for _, b := range r.Bindings() {
		if b.Action == "command:lazygit" {
			found = true
			if !b.Shadowed || b.ShadowedBy != "toggle_scratch" || b.Section != SectionCommand {
				t.Errorf("binding = %+v, want shadowed by toggle_scratch", b)
			}
		}
	}
	if !found {
		t.Fatal("Bindings has no row for the entry")
	}
}

// A bad entry is a warning, never an error, and tuios leaves it out.
func TestCommandEntryValidation(t *testing.T) {
	cases := []struct {
		name, toml, want string
	}{
		{"no key", `command = "x"`, "has no key"},
		{"bad type", "key = \"alt+x\"\ntype = \"window\"\ncommand = \"x\"", "is not known"},
		{"no command", "key = \"alt+x\"\ntype = \"popup\"", "has no command"},
		{"reserved name", "key = \"alt+x\"\ncommand = \"x\"\nname = \"scratch\"", "belongs to the built-in"},
		{"bad size", "key = \"alt+x\"\ncommand = \"x\"\nwidth = \"wide\"", "width is not valid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\n" + tc.toml + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			res := ValidateConfig(cfg)
			if len(res.Errors) != 0 {
				t.Fatalf("errors = %+v, want warnings only", res.Errors)
			}
			var msgs []string
			for _, w := range res.Warnings {
				msgs = append(msgs, w.Message)
			}
			if !strings.Contains(strings.Join(msgs, " | "), tc.want) {
				t.Fatalf("warnings = %q, want %q", msgs, tc.want)
			}
			if len(cfg.Keybindings.Commands()) != 0 {
				t.Fatal("tuios kept a bad entry")
			}
		})
	}
}

// A scratch entry needs no command: it runs the user's shell.
func TestScratchEntryNeedsNoCommand(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\nkey = \"alt+x\"\ntype = \"scratch\"\nname = \"notes\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Keybindings.Commands()) != 1 {
		t.Fatal("a scratch entry with no command was left out")
	}
}

// A second entry of the same name is left out, with a warning.
func TestCommandEntryDuplicateName(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\nkey = \"alt+x\"\ncommand = \"a\"\nname = \"n\"\n[[keybindings.command]]\nkey = \"alt+y\"\ncommand = \"b\"\nname = \"n\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Keybindings.Commands(); len(got) != 1 || got[0].Command != "a" {
		t.Fatalf("commands = %+v", got)
	}
	res := ValidateConfig(cfg)
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[len(res.Warnings)-1].Message, "An earlier entry") {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
}

// Of two entries on one key, the first in the file runs, and the report
// agrees. The key map used to sort by name, so alpha ran while the doctor
// named zeta.
func TestCommandEntriesOnOneKeyFollowTheFile(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\nkey = \"alt+x\"\ncommand = \"z\"\nname = \"zeta\"\n[[keybindings.command]]\nkey = \"alt+x\"\ncommand = \"a\"\nname = \"alpha\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewKeybindRegistry(cfg)
	if got := r.GetGlobalAction("alt+x"); got != "command:zeta" {
		t.Fatalf("alt+x runs %q, want command:zeta", got)
	}
	for _, b := range r.Bindings() {
		switch b.Action {
		case "command:zeta":
			if b.Shadowed {
				t.Error("the report says zeta is dead")
			}
		case "command:alpha":
			if !b.Shadowed || b.ShadowedBy != "command:zeta" {
				t.Errorf("alpha = %+v, want dead behind zeta", b)
			}
		}
	}
	var msg string
	for _, w := range ValidateConfig(cfg).Warnings {
		if strings.Contains(w.Message, "command:alpha") {
			msg = w.Message
		}
	}
	if !strings.Contains(msg, "runs command:zeta") || !strings.Contains(msg, "[[keybindings.command]]") || strings.Contains(msg, "unbind command:") {
		t.Fatalf("warning = %q, want zeta the winner and a config.toml hint", msg)
	}
}

// An entry on the leader key never runs, and the report says so.
func TestCommandEntryOnTheLeaderIsDead(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\nkey = \"ctrl+b\"\ncommand = \"x\"\nname = \"lead\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewKeybindRegistry(cfg)
	if got := r.GetGlobalAction("ctrl+b"); got == "command:lead" {
		t.Fatal("the leader runs the entry")
	}
	for _, b := range r.Bindings() {
		if b.Action == "command:lead" && (!b.Shadowed || b.ShadowedBy != LeaderAction) {
			t.Fatalf("binding = %+v, want dead behind the leader", b)
		}
	}
}

// A global key with no modifier takes the letter from every pane: warned.
func TestBareGlobalKeyWarns(t *testing.T) {
	for key, want := range map[string]bool{"g": true, "G": true, "space": true, "alt+g": false, "prefix+g": false, "f5": false} {
		cfg, err := ParseUserConfig([]byte("[[keybindings.command]]\nkey = \"" + key + "\"\ncommand = \"x\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		got := false
		for _, w := range ValidateConfig(cfg).Warnings {
			got = got || strings.Contains(w.Message, "takes it from every pane")
		}
		if got != want {
			t.Errorf("key %q warned=%v, want %v", key, got, want)
		}
	}
}

// A name that no slug can make falls back to the key, then to a hash, and
// is the same on every read.
func TestCommandNameFallback(t *testing.T) {
	c := CommandBinding{Key: "prefix+alt+g", Description: "Привет", Command: "эхо"}
	if got := c.ResolvedName(); got != "prefix-alt-g" {
		t.Fatalf("name = %q, want prefix-alt-g", got)
	}
	c = CommandBinding{Key: "ж", Description: "Привет", Command: "эхо"}
	a, b := c.ResolvedName(), c.ResolvedName()
	if a == "" || a != b || !strings.HasPrefix(a, "cmd-") {
		t.Fatalf("names = %q, %q, want one stable cmd- name", a, b)
	}
}
