package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func TestKeybindsDoctorNamesACopyModeKeyUsedTwice(t *testing.T) {
	type problem struct {
		Action  string `json:"action"`
		Key     string `json:"key"`
		Problem string `json:"problem"`
	}
	doctor := func(t *testing.T, base string) ([]problem, string) {
		t.Helper()
		out, err := tuiosCLI(t, base, "keybinds", "doctor", "--json")
		if err != nil {
			t.Fatalf("keybinds doctor --json: %v\n%s", err, out)
		}
		var rep struct {
			CopyModeProblems []problem `json:"copy_mode_problems"`
		}
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &rep); err != nil {
			t.Fatalf("keybinds doctor --json is not JSON: %v\n%s", err, out)
		}
		text, err := tuiosCLI(t, base, "keybinds", "doctor")
		if err != nil {
			t.Fatalf("keybinds doctor: %v\n%s", err, text)
		}
		return rep.CopyModeProblems, text
	}

	t.Run("clashes", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, "[[keybindings.copy_pipe]]\nkey = \"p\"\ncommand = \"cat\"\n\n"+
			"[keybindings.copy_mode]\ncopy_mode_line_start = [\"home\", \"v\"]\ncopy_mode_line_end = [\"end\", \"p\"]\n")
		probs, text := doctor(t, base)
		if len(probs) != 2 {
			t.Fatalf("the doctor reports %d copy mode keys, want 2 (p and v)\n%+v\n%s", len(probs), probs, text)
		}
		if p := probs[0]; p.Action != "copy_mode_line_end" || p.Key != "p" || !strings.Contains(p.Problem, "copy_pipe") {
			t.Errorf("the first problem is %+v, want copy_mode_line_end on p, named as a copy pipe key", p)
		}
		if p := probs[1]; p.Action != "copy_mode_line_start" || p.Key != "v" || !strings.Contains(p.Problem, "Copy mode uses the key v") {
			t.Errorf("the second problem is %+v, want copy_mode_line_start on v, named as a copy mode key", p)
		}
		for _, want := range []string{"2 copy mode keys used twice", "COPY MODE KEYS", "[copy_mode.copy_mode_line_start]"} {
			if !strings.Contains(text, want) {
				t.Errorf("the text report does not show %q:\n%s", want, text)
			}
		}
	})

	t.Run("startup", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, "[[keybindings.copy_pipe]]\nkey = \"p\"\ncommand = \"cat\"\n\n"+
			"[keybindings.copy_mode]\ncopy_mode_line_start = [\"w\"]\ncopy_mode_line_end = [\"b\", \"p\"]\n\n"+
			"[keybindings.mode_control]\nenter_window_mode = []\n\n"+
			"[keybindings.prefix_mode]\nprefix_exit_mode = [\"ctrl+e\", \"esc\"]\n")
		probs, text := doctor(t, base)
		if len(probs) != 3 {
			t.Errorf("the doctor reports %d copy mode keys, want 3 (w, b and p)\n%+v\n%s", len(probs), probs, text)
		}

		term := startIn(t, base, startOpts{cols: 160, rows: 40})
		waitBoot(t, term)
		if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
			t.Fatalf("open the log viewer: %v", err)
		}
		if err := term.WaitForText("copy_mode_line_end] p", uiTimeout); err != nil {
			t.Fatalf("the log does not warn about the copy pipe key: %v\n%s", err, term.Snapshot())
		}
		saveFrame(t, term, "copy-mode-startup-log")
		screen := term.Screen().Text()
		for _, unwanted := range []string{"copy_mode_line_start] w", "copy_mode_line_end] b", "enter_window_mode", "config problems"} {
			if strings.Contains(screen, unwanted) {
				t.Errorf("the startup log warns %q about a binding that works:\n%s", unwanted, term.Snapshot())
			}
		}
	})

	t.Run("defaults", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, copyCursorConfig)
		probs, text := doctor(t, base)
		if len(probs) != 0 || strings.Contains(text, "COPY MODE KEYS") {
			t.Errorf("the doctor reports copy mode keys for the defaults: %+v\n%s", probs, text)
		}
	})
}

// TestHelpListsHomeAndEndWithTheLineKeys opens the help overlay on its copy
// mode section and requires the Home and End row right after the 0, ^, $ row.
func TestHelpListsHomeAndEndWithTheLineKeys(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	term := startIn(t, base, startOpts{cols: 140, rows: 60})
	waitBoot(t, term)
	openHelp(t, term)

	lineRow := func(s tuitest.Screen) int {
		for i, line := range strings.Split(s.Text(), "\n") {
			if strings.Contains(line, "Line start/first/end") {
				return i
			}
		}
		return -1
	}
	for range 30 {
		if lineRow(term.Screen()) >= 0 {
			break
		}
		if err := term.SendKeys(tuitest.Right); err != nil {
			t.Fatalf("send right: %v", err)
		}
		_ = term.WaitFor(func(s tuitest.Screen) bool { return lineRow(s) >= 0 }, 300e6)
	}
	s := term.Screen()
	r := lineRow(s)
	if r < 0 {
		t.Fatalf("no category of the help overlay lists the 0, ^, $ row\n%s", term.Snapshot())
	}
	var next string
	for _, line := range strings.Split(s.Text(), "\n")[r+1:] {
		if strings.TrimSpace(strings.Trim(line, "│| ")) != "" {
			next = line
			break
		}
	}
	if !strings.Contains(next, "home") || !strings.Contains(next, "end") || !strings.Contains(next, "Line start/end") {
		t.Fatalf("the row after the 0, ^, $ row is %q, want the home and end row\n%s", next, term.Snapshot())
	}
	alive(t, term, "after the help overlay")
}
