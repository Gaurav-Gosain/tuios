package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// screenFixture is one pane screen and what the harness's rules must make of
// it. Each fixture says in its header how the screen was obtained, so a
// derived screen is never passed off as a measured one.
//
// A file holds one fixture, or several separated by a line of five equals
// signs, each with its own header. The per-harness files under
// testdata/screens/rules keep one fixture for every rule of a manifest
// together, which is where someone changing that manifest will look.
type screenFixture struct {
	file    string
	harness string
	want    string // a state, or "none" for no rule matching
	screen  []string
}

// fixtureSeparator splits the fixtures of one file.
const fixtureSeparator = "\n=====\n"

func loadScreenFixtures(t *testing.T) []screenFixture {
	t.Helper()
	var paths []string
	for _, pattern := range []string{"*.txt", filepath.Join("rules", "*.txt")} {
		found, err := filepath.Glob(filepath.Join("testdata", "screens", pattern))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, found...)
	}
	if len(paths) == 0 {
		t.Fatal("no screen fixtures")
	}
	var out []screenFixture
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for n, chunk := range strings.Split(string(data), fixtureSeparator) {
			name := filepath.Base(p)
			if strings.Contains(string(data), fixtureSeparator) {
				name = fmt.Sprintf("%s#%d", name, n+1)
			}
			out = append(out, parseScreenFixture(t, name, chunk))
		}
	}
	return out
}

func parseScreenFixture(t *testing.T, name, chunk string) screenFixture {
	t.Helper()
	head, body, ok := strings.Cut(chunk, "\n---\n")
	if !ok {
		t.Fatalf("%s: no --- line between header and screen", name)
	}
	fx := screenFixture{file: name, screen: strings.Split(body, "\n")}
	for _, line := range strings.Split(head, "\n") {
		key, val, _ := strings.Cut(strings.TrimPrefix(line, "# "), ":")
		switch key {
		case "harness":
			fx.harness = strings.TrimSpace(val)
		case "want":
			fx.want = strings.TrimSpace(val)
		case "how":
			if !strings.HasPrefix(strings.TrimSpace(val), "measured") && !strings.HasPrefix(strings.TrimSpace(val), "derived") {
				t.Errorf("%s: how must start with measured or derived", name)
			}
		}
	}
	if fx.harness == "" || fx.want == "" {
		t.Fatalf("%s: header must name a harness and a want", name)
	}
	return fx
}

// fixtureTail reads a screen the way the emulator's TailText does: the bottom n
// lines that carry anything, with trailing space trimmed.
func fixtureTail(screen []string, n int) []string {
	var out []string
	for i := len(screen) - 1; i >= 0 && len(out) < n; i-- {
		if line := strings.TrimRight(screen[i], " \t"); line != "" {
			out = append(out, line)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// TestScreenFixtures holds every bundled screen rule to the captured and
// derived screens under testdata/screens.
func TestScreenFixtures(t *testing.T) {
	r := testRegistry(t)
	for _, fx := range loadScreenFixtures(t) {
		t.Run(fx.file, func(t *testing.T) {
			if r.Lookup(fx.harness) == nil {
				t.Fatalf("no manifest %q", fx.harness)
			}
			tail := fixtureTail(fx.screen, r.ScreenLines(fx.harness))
			state, rule, ok := r.Classify(fx.harness, tail)
			got := "none"
			if ok {
				got = state
			}
			if got != fx.want {
				t.Errorf("classified as %s (rule %d), want %s\ntail:\n%s", got, rule, fx.want, strings.Join(tail, "\n"))
			}
		})
	}
}

// TestEveryScreenRuleHasAFixture holds every bundled screen rule to at least
// one fixture it decides. A rule nothing exercises is a rule nobody can tell is
// still right, and a rule that some louder rule always shadows is dead weight
// that a reader would still have to reason about.
func TestEveryScreenRuleHasAFixture(t *testing.T) {
	r := testRegistry(t)
	decided := map[string]map[int]bool{}
	for _, fx := range loadScreenFixtures(t) {
		if r.Lookup(fx.harness) == nil {
			continue
		}
		tail := fixtureTail(fx.screen, r.ScreenLines(fx.harness))
		if state, rule, ok := r.Classify(fx.harness, tail); ok && state == fx.want {
			if decided[fx.harness] == nil {
				decided[fx.harness] = map[int]bool{}
			}
			decided[fx.harness][rule] = true
		}
	}
	for _, id := range r.IDs() {
		m := r.Lookup(id)
		if !m.Screen.Enabled {
			continue
		}
		for i, rule := range m.Screen.Rule {
			if !decided[id][i] {
				t.Errorf("%s screen rule %d (%s, priority %d) decides no fixture under testdata/screens",
					id, i, rule.State, rule.Priority)
			}
		}
	}
}

// TestPromptBoxRegion checks the box region on its own: the body is what sits
// between the last two borders, corners are allowed, and a screen with fewer
// than two borders has no box.
func TestPromptBoxRegion(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail []string
		want []string
	}{
		{"bare rules", []string{"x", "─────", "❯ hi", "─────", "footer"}, []string{"❯ hi"}},
		{"rounded box", []string{"╭───╮", "│ > │", "╰───╯"}, []string{"│ > │"}},
		{"last box wins", []string{"───", "old", "───", "text", "───", "new", "───"}, []string{"new"}},
		{"one border", []string{"❯ hi", "─────"}, nil},
		{"two dashes are not a border", []string{"──", "❯", "──"}, nil},
		{"text is not a border", []string{"- - -", "❯", "=== x"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := regionLines(tc.tail, RegionPromptBox)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("box body %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIdleRuleNeedsProof is the loader's half of the idle policy: an idle
// screen rule that reads the whole tail with only substrings is refused, and
// one that reads the box, or pins the box with a regex, loads.
func TestIdleRuleNeedsProof(t *testing.T) {
	base := "schema_version = 1\nid = \"x-agent\"\n[detect]\ncomm = [\"x-agent\"]\n[screen]\nenabled = true\n"
	for _, tc := range []struct {
		name string
		rule string
		ok   bool
	}{
		{"substring alone", "[[screen.rule]]\nstate = \"idle\"\nall = [\"ready\"]\n", false},
		{"prompt box", "[[screen.rule]]\nstate = \"idle\"\nregion = \"prompt_box\"\nall = [\">\"]\n", true},
		{"regex", "[[screen.rule]]\nstate = \"idle\"\nregex = ['^> $']\n", true},
		{"working needs no proof", "[[screen.rule]]\nstate = \"working\"\nall = [\"busy\"]\n", true},
		{"unknown region", "[[screen.rule]]\nstate = \"working\"\nregion = \"middle\"\nall = [\"busy\"]\n", false},
		{"region on a title rule", "[title]\nenabled = true\n[[title.rule]]\nstate = \"working\"\nregion = \"prompt_box\"\nall = [\"busy\"]\n", false},
		{"done in a notify rule", "[notify]\nenabled = true\n[[notify.rule]]\nstate = \"done\"\nall = [\"finished\"]\n", true},
		{"done in a screen rule", "[[screen.rule]]\nstate = \"done\"\nall = [\"finished\"]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest("x.toml", []byte(base+tc.rule))
			if (err == nil) != tc.ok {
				t.Errorf("load error %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestNotifyRules checks the bundled notification rules against the words the
// harnesses send.
func TestNotifyRules(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct {
		harness, title, body, want, message string
	}{
		{"claude-code", "Claude Code", "Claude needs your permission to use Bash", "needs_input", "approval: Claude Code: Claude needs your permission to use Bash"},
		{"claude-code", "", "Claude is waiting for your input", "idle", "Claude is waiting for your input"},
		{"claude-code", "", "Build finished", "none", ""},
		{"codex", "", "Approval requested: go test ./...", "needs_input", "approval: Approval requested: go test ./..."},
		{"codex", "", "Codex wants to edit main.go", "needs_input", "approval: Codex wants to edit main.go"},
		{"codex", "", "All tests pass.", "done", "All tests pass."},
		{"gemini-cli", "", "anything", "none", ""},
	} {
		text := NotifyText(tc.title, tc.body)
		state, rule, ok := r.ClassifyNotify(tc.harness, text)
		got := "none"
		if ok {
			got = state
		}
		if got != tc.want {
			t.Errorf("%s %q: got %s, want %s", tc.harness, text, got, tc.want)
			continue
		}
		if ok {
			if msg := r.NotifyRuleMessage(tc.harness, rule, text); msg != tc.message {
				t.Errorf("%s %q: message %q, want %q", tc.harness, text, msg, tc.message)
			}
		}
	}
}

// TestNewTitleRules checks the title rules added for idle and working.
func TestNewTitleRules(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct {
		harness, title, want string
	}{
		{"claude-code", "✳ Claude Code", "idle"},
		{"claude-code", "⠂ Fix the flaky test", "working"},
		{"claude-code", "◐ Fix the flaky test", "working"},
		{"claude-code", "Claude Code", "none"},
		{"claude-code", "~/src/claude", "none"},
		{"codex", "⠋ codex", "working"},
		{"codex", "codex", "none"},
		{"gemini-cli", "◇  Ready (tuios)", "idle"},
		{"gemini-cli", "✦  Working… (tuios)", "working"},
		{"gemini-cli", "✋  Action Required (tuios)", "needs_input"},
		{"gemini-cli", "~/src/ready-player-one", "none"},
		{"gemini-cli", "Gemini - tuios", "none"},
	} {
		state, _, ok := r.ClassifyTitle(tc.harness, tc.title)
		got := "none"
		if ok {
			got = state
		}
		if got != tc.want {
			t.Errorf("%s title %q: got %s, want %s", tc.harness, tc.title, got, tc.want)
		}
	}
}
