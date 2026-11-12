package tuie2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAnOlderActionNameStillBindsTheAction: R2-3 gives every action one
// name. prefix_settings is now an alias of open_settings, and a config.toml
// written with the older name must go on working: the key it binds opens the
// settings page, keybinds list names the action by its own name and the old
// one beside it, and the old name does not come back as a second action.
//
// The file also unbinds terminal_focus_up, an older name of focus_up, with an
// empty list; the default alt+up must not come back under focus_up.
//
// How this could pass wrongly, written down first:
//   - Y could open the settings page some other way. Y has no default
//     binding in the prefix table, so only the line in the file binds it.
//   - The list could show the key under both names. The test wants exactly
//     one row for the action with Y, and no row named prefix_settings.
//   - The default key could hide a lost binding. The test checks ctrl+b Y,
//     the key only the file gives.
func TestAnOlderActionNameStillBindsTheAction(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.prefix_mode]\nprefix_settings = [\"Y\"]\n\n"+
		"[keybindings.terminal_mode]\nterminal_focus_up = []\n")

	out, err := tuiosCLI(t, base, "keybinds", "list", "--json")
	if err != nil {
		t.Fatalf("keybinds list --json: %v\n%s", err, out)
	}
	var rows []struct {
		Scope  string   `json:"scope"`
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
		Also   []string `json:"also"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "["):]), &rows); err != nil {
		t.Fatalf("keybinds list --json is not JSON: %v", err)
	}
	found := 0
	for _, r := range rows {
		if r.Action == "prefix_settings" {
			t.Errorf("ASSERTION: keybinds list still has a row named prefix_settings: %+v", r)
		}
		if r.Action == "open_settings" && slices.ContainsFunc(r.Keys, func(k string) bool { return strings.HasSuffix(k, " Y") }) {
			found++
			if !slices.Contains(r.Also, "prefix_settings") {
				t.Errorf("ASSERTION: the open_settings row does not name prefix_settings as an older name: %+v", r)
			}
		}
	}
	if found != 1 {
		t.Errorf("ASSERTION: %d rows give open_settings the key Y from the file, want 1", found)
	}
	// An empty list under the older name unbinds the action: the default
	// alt+up must not come back under the action's own name.
	for _, r := range rows {
		// The GUI app's own table binds alt+up too; only terminal mode is the
		// file's.
		if r.Scope == "terminal" && r.Action == "focus_up" && slices.ContainsFunc(r.Keys, func(k string) bool { return strings.HasSuffix(k, "alt+up") }) {
			t.Errorf("ASSERTION: terminal_focus_up = [] in the file left focus_up on alt+up: %+v", r)
		}
	}

	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "Y"); err != nil {
		t.Fatalf("send prefix+Y: %v", err)
	}
	if err := term.WaitForText("tab section", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b Y, bound under the older name prefix_settings, did not open the settings page: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "settings-by-older-name")
	alive(t, term, "after the settings page from an older action name")
}
