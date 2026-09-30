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
