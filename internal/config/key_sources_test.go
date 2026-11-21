package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func layeredFrom(t *testing.T, text string) *UserConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := LoadLayered(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := lc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	DropUnreadableKeys(cfg, lc)
	return cfg
}

// TestKeyConflictsSayWhereEachBindingIs: a key bound twice, a command entry
// and an action that take the same key, and a key that is also the leader
// each warn with the file and line of every binding.
func TestKeyConflictsSayWhereEachBindingIs(t *testing.T) {
	cfg := layeredFrom(t, `[keybindings]
leader_key = "ctrl+a"

[keybindings.window_management]
new_window = ["n"]
close_window = ["n"]

[keybindings.terminal_mode]
toggle_zoom = ["ctrl+a"]

[[actions]]
name = "deploy"
run = ["deploy"]
key = "prefix+alt+d"

[[keybindings.command]]
name = "build"
key = "prefix+alt+d"
type = "shell"
command = "make"
`)
	res := ValidateConfig(cfg)
	var got []string
	for _, w := range res.Warnings {
		if w.Field == "keybindings" {
			got = append(got, w.Message)
		}
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"config.toml:5", "config.toml:6",
		"config.toml:14", "config.toml:18",
		"toggle_zoom in terminal mode (config.toml:9), and it is also the leader key (config.toml:2)",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning names %q:\n%s", want, all)
		}
	}

	rep := NewKeybindRegistry(cfg).Report(PaneFacts{})
	if len(rep.LeaderClashes) != 1 || rep.LeaderClashes[0].Action != "toggle_zoom" {
		t.Errorf("leader clashes = %+v, want toggle_zoom", rep.LeaderClashes)
	}
	if !strings.Contains(rep.Summary(), "also the leader") {
		t.Errorf("summary %q does not count the leader clash", rep.Summary())
	}
}

// TestDefaultKeysHaveNoLeaderClash: the shipped keymap gives the leader to
// nothing else.
func TestDefaultKeysHaveNoLeaderClash(t *testing.T) {
	if c := NewKeybindRegistry(DefaultConfig()).LeaderClashes(); len(c) != 0 {
		t.Errorf("the defaults have leader clashes: %+v", c)
	}
}

func TestFindKeyLine(t *testing.T) {
	data := []byte("# top\n[keybindings.window_management]\n\"new_window\" = [\"n\"] # x\n\n[[actions]]\nname = \"a\"\n\n[[actions]]\nkey = \"x\"\nname = \"b\"\n")
	if n := findKeyLine(data, "[keybindings.window_management]", "", "new_window"); n != 3 {
		t.Errorf("table line = %d, want 3", n)
	}
	if n := findKeyLine(data, "[[actions]]", "a", ""); n != 5 {
		t.Errorf("entry with no key: line %d, want its header 5", n)
	}
	if n := findKeyLine(data, "[[actions]]", "b", ""); n != 9 {
		t.Errorf("entry key line = %d, want 9", n)
	}
	if n := findKeyLine(data, "[[actions]]", "c", ""); n != 0 {
		t.Errorf("missing entry = %d, want 0", n)
	}
}
