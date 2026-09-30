package config

import (
	"strings"
	"testing"
)

const copyPipeConfig = `
[appearance.selection]
copy_command = "  tmux-copy-it  "

[[keybindings.copy_pipe]]
key = "p"
command = "tr '\n' ' '"
cancel = true
description = "flatten"

[[keybindings.copy_pipe]]
key = "alt+y"
command = "nc -U /tmp/sock"

[[keybindings.copy_pipe]]
key = "p"
command = "cat"
`

func TestCopyPipeEntriesParse(t *testing.T) {
	cfg, err := ParseUserConfig([]byte(copyPipeConfig))
	if err != nil {
		t.Fatal(err)
	}
	pipes := cfg.Keybindings.CopyPipes()
	if len(pipes) != 2 {
		t.Fatalf("entries = %+v, want 2 (the second p is left out)", pipes)
	}
	p, ok := cfg.Keybindings.CopyPipeFor("p")
	if !ok || !p.Cancel || p.Command != "tr '\n' ' '" || p.Label() != "flatten" {
		t.Fatalf("p = %+v, %v", p, ok)
	}
	y, ok := cfg.Keybindings.CopyPipeFor("Alt+Y")
	if !ok || y.Cancel || y.Label() != "nc -U /tmp/sock" {
		t.Fatalf("alt+y = %+v, %v", y, ok)
	}
	if _, ok := cfg.Keybindings.CopyPipeFor("y"); ok {
		t.Fatal("y has no entry, but CopyPipeFor found one")
	}
	s := DefaultSettings()
	ApplyAppearanceConfig(cfg, &s)
	if s.CopyCommand != "tmux-copy-it" {
		t.Fatalf("copy command = %q", s.CopyCommand)
	}

	res := ValidateConfig(cfg)
	var msgs []string
	for _, w := range res.Warnings {
		msgs = append(msgs, w.Field+": "+w.Message)
	}
	if !strings.Contains(strings.Join(msgs, " | "), "keybindings.copy_pipe[3]: An earlier entry has the key p") {
		t.Fatalf("warnings = %q", msgs)
	}
}

func TestCopyPipeEntryValidation(t *testing.T) {
	cases := []struct{ name, toml, want string }{
		{"no key", `command = "x"`, "has no key"},
		{"no command", `key = "p"`, "has no command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseUserConfig([]byte("[[keybindings.copy_pipe]]\n" + tc.toml + "\n"))
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
			if len(cfg.Keybindings.CopyPipes()) != 0 {
				t.Fatal("tuios kept a bad entry")
			}
		})
	}
}

// An entry on a key copy mode needs is used, with a warning. y is allowed
// without one.
func TestCopyPipeEntryOnACopyModeKeyWarns(t *testing.T) {
	for _, key := range []string{"q", "esc", "v", "V", "/", "?", "0", "5"} {
		cfg, err := ParseUserConfig([]byte("[[keybindings.copy_pipe]]\nkey = \"" + key + "\"\ncommand = \"cat\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		res := ValidateConfig(cfg)
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0].Message, "Copy mode uses the key") {
			t.Errorf("key %s: warnings = %+v", key, res.Warnings)
		}
	}
	cfg, err := ParseUserConfig([]byte("[[keybindings.copy_pipe]]\nkey = \"y\"\ncommand = \"cat\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res := ValidateConfig(cfg); len(res.Warnings) != 0 {
		t.Errorf("an entry on y warned: %+v", res.Warnings)
	}
}
