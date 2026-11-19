package tuie2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tuios prompt shows a field or a list in the command palette of the
// attached clients (internal/session/prompt.go, internal/app/prompt.go). The
// person's keys answer it, and the waiting command prints the answer. Each
// test saves the client's screens with saveFrame.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Prompts in the palette").

const (
	promptKeyDown  = "\x1b[B"
	promptKeyEnter = "\r"
	promptKeyEsc   = "\x1b"
	promptKeyTab   = "\t"
)

// waitPrompt waits for the palette to show the prompt and every marker, and
// saves the frame.
func waitPrompt(t *testing.T, term *tuitest.Terminal, frame string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, markers...) }, uiTimeout); err != nil {
		t.Fatalf("the prompt never showed %q: %v\n%s", markers, err, term.Snapshot())
	}
	saveFrame(t, term, frame)
}

// pressAfterSettle waits out the moment a new prompt ignores Enter, then
// presses keys.
func pressAfterSettle(t *testing.T, term *tuitest.Terminal, keys ...string) {
	t.Helper()
	pause(t, "a new prompt ignores Enter for its first moment", answerSettle)
	pressKeys(t, term, keys...)
}

// promptRun waits for a background tuios prompt to end.
func promptRun(t *testing.T, term *tuitest.Terminal, run <-chan askRun) askRun {
	t.Helper()
	select {
	case r := <-run:
		return r
	case <-time.After(uiTimeout):
		t.Fatalf("tuios prompt never returned\n%s", term.Snapshot())
	}
	return askRun{}
}

func promptExit(err error) int {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// TestPromptPickInThePalette: a pick opens in the palette with its rows and
// who asks, typing filters the rows, and Enter prints the id of the row.
func TestPromptPickInThePalette(t *testing.T) {
	term, base := attachClientBase(t)
	run := startCLIBackground(t, base, "prompt", "pick", "Deploy to?", "--timeout", "60000",
		"--item", "stg=Staging", "--item", "prod=Production")
	waitPrompt(t, term, "prompt-pick", "Prompt from a command", "Deploy to?", "Staging", "Production", "choose", "cancel")
	pause(t, "a new prompt ignores Enter for its first moment", answerSettle)

	pressKeys(t, term, "p", "r", "o", "d")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Production") && !strings.Contains(s.Text(), "Staging")
	}, uiTimeout); err != nil {
		t.Fatalf("typing did not filter the rows: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "prompt-pick-filtered")
	pressKeys(t, term, promptKeyEnter)
	if r := promptRun(t, term, run); r.err != nil || strings.TrimSpace(r.out) != "prod" {
		t.Fatalf("tuios prompt pick printed %q and ended with %v, want prod and exit 0", r.out, r.err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Deploy to?") }, uiTimeout); err != nil {
		t.Fatalf("the palette stayed open after the answer: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after answering a prompt")
}

// TestPromptPickMarksMoreThanOne: with --multiple, Tab marks rows and Enter
// prints every marked id in list order.
func TestPromptPickMarksMoreThanOne(t *testing.T) {
	term, base := attachClientBase(t)
	run := startCLIBackground(t, base, "prompt", "pick", "Which hosts?", "--multiple", "--timeout", "60000",
		"--item", "a=Alpha", "--item", "b=Beta", "--item", "c=Gamma")
	waitPrompt(t, term, "prompt-multiple", "Which hosts?", "Alpha", "Gamma", "mark")
	pause(t, "a new prompt ignores Enter for its first moment", answerSettle)

	pressKeys(t, term, promptKeyTab, promptKeyDown, promptKeyDown, promptKeyTab)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Count(s.Text(), "✓") >= 2 }, uiTimeout); err != nil {
		t.Fatalf("Tab did not mark two rows: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "prompt-multiple-marked")
	pressKeys(t, term, promptKeyEnter)
	if r := promptRun(t, term, run); r.err != nil || strings.Fields(r.out)[0] != "a" || strings.TrimSpace(r.out) != "a\nc" {
		t.Fatalf("tuios prompt pick --multiple printed %q and ended with %v, want a and c", r.out, r.err)
	}
}

// TestPromptInputTypesALine: an input opens with its placeholder, the typed
// line is printed, and Esc on the next one exits 1.
func TestPromptInputTypesALine(t *testing.T) {
	term, base := attachClientBase(t)
	run := startCLIBackground(t, base, "prompt", "input", "Branch name?", "--placeholder", "feature/x", "--timeout", "60000")
	waitPrompt(t, term, "prompt-input", "Branch name?", "feature/x", "send", "cancel")
	pause(t, "a new prompt ignores Enter for its first moment", answerSettle)
	pressKeys(t, term, "f", "i", "x", "/", "m", "e", "n", "u")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "fix/menu") }, uiTimeout); err != nil {
		t.Fatalf("the typed text never showed: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "prompt-input-typed")
	pressKeys(t, term, promptKeyEnter)
	if r := promptRun(t, term, run); r.err != nil || strings.TrimSpace(r.out) != "fix/menu" {
		t.Fatalf("tuios prompt input printed %q and ended with %v, want fix/menu", r.out, r.err)
	}

	run = startCLIBackground(t, base, "prompt", "input", "Tag?", "--timeout", "60000")
	waitPrompt(t, term, "prompt-input-cancel", "Tag?")
	pause(t, "a new prompt ignores Enter for its first moment", answerSettle)
	pressKeys(t, term, promptKeyEsc)
	if r := promptRun(t, term, run); promptExit(r.err) != 1 || r.out != "" {
		t.Fatalf("Esc: tuios prompt printed %q and exited %d, want nothing and 1", r.out, promptExit(r.err))
	}
}

// TestPromptWithNoClientSaysSo: with no client attached nothing is shown,
// and the command exits 3 at once.
func TestPromptWithNoClientSaysSo(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-prompt", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	start := time.Now()
	out, err := tuiosCLI(t, base, "prompt", "input", "Name?")
	if promptExit(err) != 3 || !strings.Contains(out, "No client is attached") || time.Since(start) > 30*time.Second {
		t.Fatalf("tuios prompt with no client: %q, exit %d, want exit 3 and the reason", out, promptExit(err))
	}
}

const promptPluginManifest = `api = 1
id = "e2e.prompt"
name = "Prompt probe"
version = "1.0.0"

[permissions]
grants = ["ui"]

[[process]]
id = "ask"
run = "action"
title = "Ask"
command = ["sh", "ask.sh"]
`

const promptPluginAsk = `D="$TUIOS_PLUGIN_STATE_DIR"
"$TUIOS_BIN" prompt pick "Run which job?" --item lint=Lint --item test=Test --timeout 60000 >"$D/answer.out" 2>"$D/answer.err"
echo $? >"$D/answer.code"
`

// TestPluginPromptsThePerson: a plugin with the ui grant calls the same
// verb, the palette says the plugin asks, and the plugin reads the answer. A
// plugin without the grant is refused.
func TestPluginPromptsThePerson(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"tuios-plugin.toml": promptPluginManifest, "ask.sh": promptPluginAsk})
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", dir, "--yes"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	run := startCLIBackground(t, base, "plugins", "run", "e2e.prompt", "ask", "--wait")
	waitPrompt(t, term, "prompt-plugin", "Prompt from plugin e2e.prompt", "Run which job?", "Lint", "Test")
	pause(t, "a new prompt ignores Enter for its first moment", answerSettle)
	pressKeys(t, term, promptKeyDown, promptKeyEnter)
	if r := promptRun(t, term, run); r.err != nil {
		t.Fatalf("plugins run: %v\n%s", r.err, r.out)
	}
	state := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", "e2e.prompt")
	got, _ := os.ReadFile(filepath.Join(state, "answer.out"))
	code, _ := os.ReadFile(filepath.Join(state, "answer.code"))
	if strings.TrimSpace(string(got)) != "test" || strings.TrimSpace(string(code)) != "0" {
		errOut, _ := os.ReadFile(filepath.Join(state, "answer.err"))
		t.Fatalf("the plugin read %q, exit %q (%s), want test and 0", got, code, errOut)
	}

	plain := t.TempDir()
	writeFiles(t, plain, map[string]string{
		"tuios-plugin.toml": strings.NewReplacer(`"e2e.prompt"`, `"e2e.noprompt"`, `grants = ["ui"]`, `grants = ["read"]`).Replace(promptPluginManifest),
		"ask.sh":            promptPluginAsk,
	})
	if out, err := tuiosCLI(t, base, "plugins", "install", plain, "--yes"); err != nil {
		t.Fatalf("install of the plain plugin: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "run", "e2e.noprompt", "ask", "--wait"); err != nil {
		t.Fatalf("plugins run: %v\n%s", err, out)
	}
	noui := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", "e2e.noprompt")
	if b, _ := os.ReadFile(filepath.Join(noui, "answer.err")); !strings.Contains(string(b), "needs the ui grant") {
		t.Errorf("prompt without the ui grant: %s", b)
	}
	if strings.Contains(term.Snapshot(), "Prompt from plugin e2e.noprompt") {
		t.Errorf("a plugin without the grant showed a prompt:\n%s", term.Snapshot())
	}
}
