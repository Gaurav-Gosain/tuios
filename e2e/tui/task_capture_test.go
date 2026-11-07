package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTaskCaptureFromAnywhere writes task cards with the capture sheet and
// reads them back from the daemon. It holds:
//
//   - ctrl+b + opens the sheet in window management mode and in terminal
//     mode, and "New task" opens it from the palette;
//   - esc closes it and keeps the draft for the next open;
//   - enter files the card: the first line is the title, the rest and the
//     pasted image (as its stored path) the prompt, in the session picked;
//   - ctrl+enter on a Plan card files it and starts the agent in its plan
//     mode; the agent holds the card, and its first prompt says to write a
//     plan.
//
// How this could pass wrongly, written down first:
//   - The toast could say "Added" while the daemon kept nothing. Each card
//     is read back with list-tasks and get-task.
//   - A key could reach the pane under the sheet. The pane's shell would
//     run it; the test checks the card text is whole and the pane quiet.
//   - The stored image could be another file. Its bytes are compared.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on the build before
// the sheet, ctrl+b + opens nothing.
func TestTaskCaptureFromAnywhere(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	bin := t.TempDir()
	argvFile, promptFile := filepath.Join(bin, "argv"), filepath.Join(bin, "prompt")
	script := strings.NewReplacer("__MARK__", filepath.Join(bin, "signed-in"), "__ARGV__", argvFile, "__PROMPT__", promptFile, "__TUIOS__", tuiosBin).Replace(fakeSheetClaude)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 40, shippedLooks: true,
		env: []string{"PATH=" + bin + ":/usr/bin:/bin", "TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	open := func(what string, keys ...any) {
		t.Helper()
		sendKeys(t, term, keys...)
		waitText(t, term, "the capture sheet from "+what, "New task", "the first line is its title", "Session", "Stage")
	}
	closed := func() {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "the first line is its title") }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the sheet did not close: %v\n%s", err, term.Snapshot())
		}
	}
	tasks := func(sess string) []map[string]any {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "list-tasks", `{"session":"`+sess+`"}`)
		if err != nil {
			t.Fatalf("list-tasks: %v\n%s", err, out)
		}
		var res struct{ Tasks []map[string]any }
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res); err != nil {
			t.Fatalf("list-tasks answer: %v\n%s", err, out)
		}
		return res.Tasks
	}
	prompt := func(sess, id string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "get-task", `{"session":"`+sess+`","id":"`+id+`"}`)
		if err != nil {
			t.Fatalf("get-task: %v\n%s", err, out)
		}
		var res struct{ Prompt string }
		_ = json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res)
		return res.Prompt
	}

	// Terminal mode: the leader works from the pane.
	open("terminal mode", tuitest.Ctrl('b'), "+")
	sendKeys(t, term, "Fix the flaky retry test")
	waitText(t, term, "the typed title", "Fix the flaky retry test")
	sendKeys(t, term, tuitest.Esc)
	closed()

	// Window management mode: the draft is kept.
	windowManagementMode(t, term)
	open("window management mode", tuitest.Ctrl('b'), "+")
	waitText(t, term, "the kept draft", "Fix the flaky retry test")
	sendKeys(t, term, tuitest.Ctrl('j'), "It fails one run in ten. ")
	pic := filepath.Join(t.TempDir(), "retry-log.png")
	img := gradientPNG(t, 48, 24)
	if err := os.WriteFile(pic, img, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := term.Paste(pic); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the image in the task", "[Image #1")
	// The session pill: home, then api.
	sendKeys(t, term, tuitest.Tab, tuitest.Right)
	waitText(t, term, "the api session picked", "‹ api ›")
	_ = term.WaitStable(uiTimeout)
	saveArtifact(t, term, dir, "capture-filled")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "capture-filled")
	sendKeys(t, term, tuitest.Enter)
	closed()
	waitText(t, term, "the toast", "Added t1 to api.")
	got := tasks("api")
	if len(got) != 1 || got[0]["title"] != "Fix the flaky retry test" || got[0]["stage"] != "todo" {
		t.Fatalf("ASSERTION: api holds %v, want one todo card titled \"Fix the flaky retry test\"", got)
	}
	p := prompt("api", "t1")
	stored := ""
	for f := range strings.FieldsSeq(p) {
		if strings.HasSuffix(f, ".png") && filepath.IsAbs(f) {
			stored = f
		}
	}
	if !strings.HasPrefix(p, "It fails one run in ten.") || stored == "" {
		t.Errorf("ASSERTION: the card's prompt %q is not the second line and the stored image", p)
	} else if b, err := os.ReadFile(stored); err != nil || !bytes.Equal(b, img) {
		t.Errorf("ASSERTION: the stored image %s does not hold the pasted image (err %v)", stored, err)
	}

	// The palette, then a Plan card started at once with ctrl+enter.
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitText(t, term, "the palette", paletteTitle)
	sendKeys(t, term, "New task")
	waitText(t, term, "the palette entry", "New task")
	sendKeys(t, term, tuitest.Enter)
	waitText(t, term, "the sheet from the palette", "the first line is its title")
	sendKeys(t, term, "Write the release notes", tuitest.Tab, tuitest.Tab, tuitest.Right)
	waitText(t, term, "the plan stage", "‹ Plan first ›", "‹ claude ›")
	_ = term.WaitStable(uiTimeout)
	saveArtifact(t, term, dir, "capture-plan")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "capture-plan")
	sendKeys(t, term, tuitest.Key("\x1b[13;5u"))
	closed()
	// start-agent answers once the agent took its first prompt, so the
	// toast comes after the prompt file.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Added t1 to home.") && strings.Contains(s.Text(), "claude works on it.")
	}, 60*time.Second); err != nil {
		t.Errorf("ASSERTION: no toast says the card was added and claude works on it: %v\n%s", err, term.Snapshot())
	}
	got = tasks("home")
	// The claim moves the card on to doing; the agent's plan approval
	// moves it back to plan. Claude starts in its plan mode.
	if len(got) != 1 || got[0]["title"] != "Write the release notes" || got[0]["holder"] == nil {
		t.Errorf("ASSERTION: home holds %v, want one card held by the new pane", got)
	}
	if argv, _ := os.ReadFile(argvFile); string(argv) != "--permission-mode\nplan\n" {
		t.Errorf("ASSERTION: claude started with argv %q, want its plan mode", argv)
	}
	deadline := time.Now().Add(30 * time.Second)
	var first []byte
	for time.Now().Before(deadline) {
		if first, _ = os.ReadFile(promptFile); len(first) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(string(first), "Take task t1: tuios task get t1.") || !strings.Contains(string(first), "Write a plan first") {
		t.Errorf("ASSERTION: the agent's first prompt is %q", first)
	}
	_ = os.WriteFile(filepath.Join(dir, "capture-cards.txt"), []byte("api t1 prompt:\n"+p+"\n\nhome agent first prompt:\n"+string(first)+"\n"), 0o644)
	t.Logf("frames in %s", dir)
}
