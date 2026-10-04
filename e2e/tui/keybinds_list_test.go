package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestKeybindsListShowsEveryScope runs `tuios keybinds list` the way a person
// and a script read it, and checks the rows the old hand-picked list left
// out: a rail files key, a prefix sub-menu key with its chord and a real
// description, an action with no key, and a fixed copy-mode key. The text
// list and --json print the same actions.
//
// Negative controls (NEGATIVE_CONTROLS.md): origin/main has no --json and no
// rail or no-key rows. With the sidebar.files scope skipped in keybindRows,
// the Copy path row is gone. With the loop over actions with no key cut,
// close_workspace is gone.
func TestKeybindsListShowsEveryScope(t *testing.T) {
	base := t.TempDir()

	out, err := tuiosCLI(t, base, "keybinds", "list", "--json")
	if err != nil {
		t.Fatalf("keybinds list --json: %v\n%s", err, out)
	}
	var rows []struct {
		Scope       string   `json:"scope"`
		Chord       string   `json:"chord"`
		Action      string   `json:"action"`
		Keys        []string `json:"keys"`
		Description string   `json:"description"`
		Fixed       bool     `json:"fixed"`
		Unbound     bool     `json:"unbound"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ASSERTION: keybinds list --json is not JSON: %v\n%s", err, out)
	}

	has := func(what string, ok func(i int) bool) {
		t.Helper()
		for i := range rows {
			if ok(i) {
				return
			}
		}
		t.Errorf("ASSERTION: keybinds list --json has no row for %s", what)
	}
	has("file_copy_path on Y in sidebar.files", func(i int) bool {
		r := rows[i]
		return r.Scope == "sidebar.files" && r.Action == "file_copy_path" && len(r.Keys) == 1 && r.Keys[0] == "Y"
	})
	has("window_prefix_new on ctrl+b t n with a description", func(i int) bool {
		r := rows[i]
		return r.Scope == "prefix.window" && r.Chord == "ctrl+b t" && r.Action == "window_prefix_new" &&
			len(r.Keys) == 1 && r.Keys[0] == "ctrl+b t n" && r.Description == "New window"
	})
	has("close_workspace with no key", func(i int) bool {
		r := rows[i]
		return r.Action == "close_workspace" && r.Unbound && len(r.Keys) == 0 && r.Description != ""
	})
	has("a fixed copy_mode key", func(i int) bool {
		return rows[i].Scope == "copy_mode" && rows[i].Fixed && len(rows[i].Keys) > 0
	})

	text, err := tuiosCLI(t, base, "keybinds", "list")
	if err != nil {
		t.Fatalf("keybinds list: %v\n%s", err, text)
	}
	missing := 0
	for _, r := range rows {
		if r.Action != "" && !strings.Contains(text, r.Action) {
			missing++
			if missing <= 5 {
				t.Errorf("ASSERTION: the text list has no %s, which --json lists in %s", r.Action, r.Scope)
			}
		}
	}
	if missing > 5 {
		t.Errorf("ASSERTION: %d actions in --json are not in the text list", missing)
	}
}
