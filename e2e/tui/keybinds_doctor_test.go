package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestKeybindsDoctorNamesACommandNameClash: two [[keybindings.command]]
// entries with no name, whose commands agree in the first 40 characters, get
// the same name, and tuios keeps only the first. Nothing in the file shows the
// name, so the doctor has to say which entry lost and why.
//
// The positive half is the same two entries with a name each: both are kept
// and the doctor says nothing about them.
//
// Negative control (NEGATIVE_CONTROLS.md): with the CommandProblems field cut
// from the report, the doctor prints no entry and --json has none.
func TestKeybindsDoctorNamesACommandNameClash(t *testing.T) {
	const prefix = "touch /tmp/a-long-directory-name/markers/"
	entry := func(name, key, file string) string {
		body := "\n[[keybindings.command]]\n"
		if name != "" {
			body += `name = "` + name + `"` + "\n"
		}
		return body + `key = "prefix+alt+` + key + `"` + "\ntype = \"shell\"\ncommand = \"" + prefix + file + "\"\n"
	}
	type problem struct {
		Entry   int    `json:"entry"`
		Key     string `json:"key"`
		Name    string `json:"name"`
		Ignored bool   `json:"ignored"`
		Problem string `json:"problem"`
	}
	doctor := func(t *testing.T, base string) ([]problem, string) {
		t.Helper()
		out, err := tuiosCLI(t, base, "keybinds", "doctor", "--json")
		if err != nil {
			t.Fatalf("keybinds doctor --json: %v\n%s", err, out)
		}
		var rep struct {
			CommandProblems []problem `json:"command_problems"`
		}
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &rep); err != nil {
			t.Fatalf("ASSERTION: keybinds doctor --json is not JSON: %v\n%s", err, out)
		}
		text, err := tuiosCLI(t, base, "keybinds", "doctor")
		if err != nil {
			t.Fatalf("keybinds doctor: %v\n%s", err, text)
		}
		return rep.CommandProblems, text
	}

	t.Run("nameless entries clash", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, entry("", "y", "late.fired")+entry("", "h", "added.fired"))
		probs, text := doctor(t, base)
		if len(probs) != 1 {
			t.Fatalf("ASSERTION: the doctor reports %d command entries, want 1 (the second entry)\n%+v\n%s", len(probs), probs, text)
		}
		p := probs[0]
		if p.Entry != 2 || p.Key != "prefix+alt+h" || !p.Ignored {
			t.Errorf("ASSERTION: the report names entry %d on %q, ignored=%v; want entry 2 on prefix+alt+h, ignored", p.Entry, p.Key, p.Ignored)
		}
		for _, want := range []string{"no name", "first 40 characters", "Entry 1", "Add a name"} {
			if !strings.Contains(p.Problem, want) {
				t.Errorf("ASSERTION: the problem does not say %q:\n%s", want, p.Problem)
			}
		}
		if !strings.Contains(text, "1 command entry tuios ignores") || !strings.Contains(text, "entry 2") {
			t.Errorf("ASSERTION: the text report does not show the ignored entry:\n%s", text)
		}
	})

	t.Run("named entries are both kept", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, entry("late", "y", "late.fired")+entry("added", "h", "added.fired"))
		probs, text := doctor(t, base)
		if len(probs) != 0 {
			t.Errorf("ASSERTION: the doctor reports entries with a name each: %+v", probs)
		}
		if strings.Contains(text, "COMMAND ENTRIES") {
			t.Errorf("ASSERTION: the text report has a command section for a clean file:\n%s", text)
		}
		list, err := tuiosCLI(t, base, "keybinds", "list")
		if err != nil {
			t.Fatalf("keybinds list: %v\n%s", err, list)
		}
		for _, name := range []string{"command:late", "command:added"} {
			if !strings.Contains(list, name) {
				t.Errorf("ASSERTION: keybinds list has no %s:\n%s", name, list)
			}
		}
	})
}
