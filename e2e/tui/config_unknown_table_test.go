package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestUnknownKeybindTableIsNamed is issue #1734. A person bound ctrl+v to the
// host paste under [keybindings.terminal], a table tuios does not have (the
// table is terminal_mode). tuios dropped the whole table without a word, and
// ctrl+v stayed free, so the binding looked broken. The client must say which
// table it ignores and name the table that was meant, and tuios config apply
// must list it.
func TestUnknownKeybindTableIsNamed(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.terminal]\nterminal_paste_host = ['ctrl+v', 'ctrl+shift+v', 'middle']\n")
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", "e2e-unknown-table"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	// The log viewer holds every config problem tuios found at start.
	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("copy errors", uiTimeout); err != nil {
		t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("[keybindings.terminal]", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client does not say that it ignores [keybindings.terminal]: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "unknown-table-log")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the log viewer: %v", err)
	}

	out, err := tuiosCLI(t, base, "config", "apply")
	if err != nil {
		t.Fatalf("tuios config apply: %v\n%s", err, out)
	}
	for _, want := range []string{
		"config.toml: [keybindings.terminal] is not a keybinding table, so tuios ignores it.",
		"Did you mean [keybindings.terminal_mode]?",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASSERTION: tuios config apply does not say %q:\n%s", want, out)
		}
	}
	alive(t, term, "after config apply with an unknown table")
}
