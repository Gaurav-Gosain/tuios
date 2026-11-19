package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestKeyConflictsNameBothBindings: a key two entries claim, one in
// config.toml and one in a config.d file, and a key that is also the leader.
// tuios keybinds doctor and the warnings at load name each binding with the
// file and line it is set in.
//
// How it could pass wrongly: a line number from the wrong entry would still
// look like a line, so each one is checked against the line that holds the
// key in its file.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Key conflicts say where").
func TestKeyConflictsNameBothBindings(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, `[keybindings]
leader_key = "ctrl+a"

[keybindings.terminal_mode]
toggle_zoom = ["ctrl+a"]

[[actions]]
name = "deploy"
run = ["true"]
key = "prefix+alt+d"
`)
	dropIn := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.d")
	if err := os.MkdirAll(dropIn, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropIn, "keys.toml"), []byte(`[[keybindings.command]]
name = "build"
key = "prefix+alt+d"
type = "shell"
command = "true"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	art := artifactDir(t)

	out, err := tuiosCLI(t, base, "keybinds", "doctor")
	_ = os.WriteFile(filepath.Join(art, "doctor.txt"), []byte(out), 0o644)
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	for _, want := range []string{
		"config.d/keys.toml:3", "config.toml:10", // the two claims on ctrl+a alt+d
		"KEYS THAT ARE ALSO THE LEADER",
		"runs toggle_zoom (config.toml:5). The leader is set in config.toml:2.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: keybinds doctor does not say %q:\n%s", want, out)
		}
	}

	// At load: the warnings go to the log viewer, and a notification counts
	// them.
	term := startIn(t, base, startOpts{cols: 220, rows: 40})
	if err := term.WaitForText("config problem", bootTimeout); err != nil {
		t.Fatalf("ASSERTION: no config problem was reported at load: %v\n%s", err, term.Snapshot())
	}
	waitBoot(t, term)
	sendKeys(t, term, tuitest.Ctrl('a'), "D", "l")
	if err := term.WaitForText("config problems", uiTimeout); err != nil {
		t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
	}
	// The viewer cuts each line. The line under the cursor shows whole under
	// the list: up twice is the leader warning, once more the claimed key.
	for i, want := range []string{"(config.toml:2)", "config.toml:10"} {
		sendKeys(t, term, keyUp)
		if i == 0 {
			sendKeys(t, term, keyUp)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, want) }, uiTimeout); err != nil {
			t.Errorf("ASSERTION: the warning at load does not say %q: %v\n%s", want, err, term.Snapshot())
		}
		saveArtifact(t, term, art, "load-warning-"+string(rune('a'+i)))
	}
}
