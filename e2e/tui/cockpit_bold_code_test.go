package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestCockpitThreadBoldAroundCode reads an agent's answer whose list items
// open with bold around a code span, the way Claude Code writes a summary,
// with more code spans after it in the same item: "**`main.go`:** moved the
// logic into a `run(args, out)` function". The thread draws the bold and the
// code, and no "**".
//
// Negative control: on main the closer search skips the code span's opening
// backtick, pairs its closing one with the next code span, and the item
// shows "**main.go:**".
func TestCockpitThreadBoldAroundCode(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	answer := "Done.\n\n- **`main.go`:** moved the logic into a `run(args, out)` function so the test can call it. It uses `flag.NewFlagSet`.\n- **`main_test.go`:** covers plain output, the `--json` and `-json` forms.\n- **Behavior change:** a missing N used to panic."
	now := time.Now()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	recs := []map[string]any{
		{"type": "user", "timestamp": at(2 * time.Minute), "message": map[string]any{"role": "user", "content": "Add a --json flag."}},
		{"type": "assistant", "timestamp": at(time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": answer},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("u%d", i)
		r["isSidechain"] = false
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	path := filepath.Join(atProjects(base), "work", "b0a1d2c3.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "json-flag"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "json-flag")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "b0a1d2c3"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 40, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Behavior change:") && strings.Contains(s.Text(), "flag.NewFlagSet")
	}, 20*time.Second); err != nil {
		t.Fatalf("the thread never showed the answer: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveFrame(t, term, "bold-code")
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "bold-code")
	text := term.Screen().Text()
	if strings.Contains(text, "**") {
		t.Errorf("ASSERTION: the thread shows the stars of a bold around a code span:\n%s", term.Snapshot())
	}
	if !strings.Contains(text, "main.go: moved the logic") || !strings.Contains(text, "main_test.go: covers plain output") {
		t.Errorf("ASSERTION: the bold items are not drawn as their words:\n%s", term.Snapshot())
	}
	alive(t, term, "after a thread with bold around code")
}
