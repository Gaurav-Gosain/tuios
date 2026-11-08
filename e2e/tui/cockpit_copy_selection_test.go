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
)

// TestCockpitCopyModeEndsWithItsAgent: copy mode over agent A's thread, then
// a click on agent B's row. It holds:
//
//   - ctrl+b [ on B starts copy mode over B's thread;
//   - back on A, copy mode is off: A's thread takes keys as the view does.
//
// How this could pass wrongly, written down first:
//   - The notice "Copy mode." could be left from the first start. The test
//     waits for it to go before the second ctrl+b [.
//   - The second copy mode could run over A's rows drawn under B's name.
//     The search in it finds B's words, which A's thread does not have.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the copy mode
// pointer stays after the click, so ctrl+b [ on B does nothing, and back on
// A copy mode is on at once.
func TestCockpitCopyModeEndsWithItsAgent(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	mustCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "retry-fix")
	mustCLI(t, base, "new-window", "-s", "api", "docs-fix", "--no-focus")
	agent := func(window, id string, transcript []byte) {
		t.Helper()
		path := filepath.Join(atProjects(base), "work", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, transcript, 0o600); err != nil {
			t.Fatal(err)
		}
		hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", window)
		hook.Dir = workDirIn(t, base)
		hook.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
		}
		hook.Stdin = bytes.NewReader(hookPayload(path, id))
		if out, err := hook.CombinedOutput(); err != nil {
			t.Fatalf("agent-hook SessionStart %s: %v\n%s", window, err, out)
		}
		mustCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", window, "--harness", "claude-code")
	}
	agent("retry-fix", "0c0ffee1", copyTranscript(t))
	agent("docs-fix", "0c0ffee2", docsTranscript(t))

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 50, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "retry-fix", "docs-fix")
	show := func(name, words string) {
		t.Helper()
		for i := 0; !strings.Contains(term.Screen().Text(), words); i++ {
			if i == 3 {
				t.Fatalf("clicking %s never showed its thread (%q)\n%s", name, words, term.Snapshot())
			}
			clickAgentsRow(t, term, name)
			_ = term.WaitStable(2 * time.Second)
		}
		_ = term.WaitStable(uiTimeout)
	}
	const notice = "Copy mode. v selects"
	// The copy mode legend in the dock, there only while copy mode runs.
	const copyLegendQuit = "q/esc quit"
	noticeGone := func() {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), notice) }, 30*time.Second); err != nil {
			t.Fatalf("the copy mode notice never went: %v", err)
		}
	}
	dir := artifactDir(t)

	show("retry-fix", "Why does the retry loop stop")
	sendKeys(t, term, tuitest.Ctrl('b'), "[")
	waitText(t, term, "copy mode over A", notice)
	noticeGone()

	// A click on B's row, as a person does while copy mode runs.
	show("docs-fix", "Write the release notes")
	sendKeys(t, term, tuitest.Ctrl('b'), "[")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), notice) }, uiTimeout); err != nil {
		saveArtifact(t, term, dir, "b-no-copy")
		t.Errorf("ASSERTION: ctrl+b [ on B did not start copy mode after copy mode on A\n%s", term.Snapshot())
	} else {
		// Copy mode runs over B's rows: a search finds B's words.
		sendKeys(t, term, "/")
		if err := term.Type("quarter"); err != nil {
			t.Fatal(err)
		}
		sendKeys(t, term, tuitest.Enter)
		_ = term.WaitStable(uiTimeout)
		saveArtifact(t, term, dir, "b-copy")
		if !strings.Contains(term.Screen().Text(), "Write the release notes") {
			t.Errorf("ASSERTION: copy mode on B does not show B's thread\n%s", term.Snapshot())
		}
		ckPress(t, term, tuitest.Esc)
		noticeGone()
	}

	// Back on A: copy mode is off. The dock shows copy mode's legend while
	// it runs ("q/esc quit").
	show("retry-fix", "Why does the retry loop stop")
	saveArtifact(t, term, dir, "back-on-a")
	if strings.Contains(term.Screen().Text(), copyLegendQuit) {
		t.Errorf("ASSERTION: back on A, copy mode is on again\n%s", term.Snapshot())
	}
	t.Logf("frames in %s", dir)
}

// docsTranscript is a short thread for a second agent, with words the first
// one does not have.
func docsTranscript(t *testing.T) []byte {
	t.Helper()
	recs := []map[string]any{
		{"type": "user", "timestamp": "2026-10-20T11:00:00.000Z", "message": map[string]any{"role": "user",
			"content": "Write the release notes for the retry change."}},
		{"type": "assistant", "timestamp": "2026-10-20T11:00:20.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "The notes say the backoff now stops at a quarter of the deadline."},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("d%d", i)
		r["isSidechain"] = false
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
