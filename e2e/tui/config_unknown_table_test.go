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

// TestUnknownKeybindActionIsNamed: a misspelled action name inside a real
// keybinding table, such as copy_mode_wrod for copy_mode_word_forward, was
// skipped without a word, so its keys did nothing and the binding looked
// broken. The client must list it in its config problems with the action
// that was meant, and tuios config apply must too.
//
// How this could pass wrongly, written down first:
//   - The name could be reported for another reason. The table is a real
//     one, so only the action name can be wrong.
//   - A retired action (nav_up, nav_down) is in the file and must stay silent.
//   - A warning for a correct name would pass the first check. The file also
//     binds copy_mode_word_end, a real action, and the test fails if that one
//     is named.
func TestUnknownKeybindActionIsNamed(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.copy_mode]\ncopy_mode_wrod = ['w']\ncopy_mode_word_end = ['e']\n[keybindings.navigation]\nnav_up = ['up']\nnav_down = ['down']\n")
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", "e2e-unknown-action"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("copy errors", uiTimeout); err != nil {
		t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("copy_mode_wrod", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client does not say that copy_mode_wrod is not an action: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "unknown-action-log")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the log viewer: %v", err)
	}

	out, err := tuiosCLI(t, base, "config", "apply")
	if err != nil {
		t.Fatalf("tuios config apply: %v\n%s", err, out)
	}
	for _, want := range []string{
		"config.toml: [keybindings.copy_mode] copy_mode_wrod is not an action, so its keys do nothing.",
		"Did you mean copy_mode_word_",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASSERTION: tuios config apply does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "copy_mode_word_end is not an action") {
		t.Fatalf("ASSERTION: tuios config apply names copy_mode_word_end, a real action:\n%s", out)
	}
	// nav_up and nav_down are retired actions an older tuios wrote into the
	// config it generated. They must not warn, and must not count.
	if strings.Contains(out, "nav_up") || strings.Contains(out, "nav_down") {
		t.Fatalf("ASSERTION: tuios config apply names a retired action:\n%s", out)
	}
	alive(t, term, "after config apply with an unknown action")
}
