package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// startHeldHookIn is startHeldHook for the pane of another session.
func startHeldHookIn(t *testing.T, base, sessionName, payload string) *heldHook {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", sessionName, "--window", "0")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(payload)
	h := &heldHook{exited: make(chan error, 1)}
	cmd.Stdout = &h.stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	finished := make(chan struct{})
	go func() {
		h.exited <- cmd.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-finished
	})
	return h
}

// waitHookAllow waits for h to print Claude Code's allow.
func waitHookAllow(t *testing.T, term *tuitest.Terminal, h *heldHook, what string) {
	t.Helper()
	select {
	case err := <-h.exited:
		if err != nil {
			t.Fatalf("%s: the hook failed: %v", what, err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("%s: the hook never returned\n%s", what, term.Snapshot())
	}
	if out := h.stdout.String(); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("%s: the hook printed %q, want Claude Code's allow", what, out)
	}
}

// TestCockpitAnswerAdvances holds a Bash call on each of two agents and
// answers them from the agents view. After the first answer the cursor is on
// the other agent's approval with no move key, the note says one more needs
// you, and the same keys answer it. With [agents] answer_advance = false the
// cursor stays on the agent answered.
//
// Negative control: on main the cursor stays on the first agent, whose row
// moves to Working, so the second approval's card never shows and the second
// a answers nothing.
func TestCockpitAnswerAdvances(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extra   string
		advance bool
	}{
		{"on", "", true},
		{"off", "[agents]\nanswer_advance = false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, term := approvalsClientWith(t, tc.extra)
			if out, err := tuiosCLI(t, base, "new", "e2e-agent2", "--detach"); err != nil {
				t.Fatalf("create the second agent's session: %v\n%s", err, out)
			}
			hooks := map[string]*heldHook{
				"go test": startHeldHookIn(t, base, "e2e-agent", `{"hook_event_name":"PermissionRequest","session_id":"e2e-adv-a","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`),
				"go vet":  startHeldHookIn(t, base, "e2e-agent2", `{"hook_event_name":"PermissionRequest","session_id":"e2e-adv-b","tool_name":"Bash","tool_input":{"command":"go vet ./..."}}`),
			}
			if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
				t.Fatalf("open the agents view: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "Needs you  2", "go test ./...", "go vet ./...", "Allow Bash?") && (strings.Contains(s.Text(), "$ go test") || strings.Contains(s.Text(), "$ go vet"))
			}, uiTimeout); err != nil {
				t.Fatalf("the agents view never showed both approvals: %v\n%s", err, term.Snapshot())
			}
			first, second := "go test", "go vet"
			if !strings.Contains(term.Screen().Text(), "$ go test") {
				first, second = second, first
			}
			dir := artifactDir(t)
			saveFrame(t, term, "answer-advance-"+tc.name+"-first")
			savePNG(t, term.Screen(), shot.XTermPalette(), dir, "first")
			pause(t, "the view takes no answer key in the moment after a card is shown", answerSettle)

			if err := term.SendKeys("a", tuitest.Enter); err != nil {
				t.Fatalf("answer the first: %v", err)
			}
			waitHookAllow(t, term, hooks[first], "the first answer")

			if !tc.advance {
				stayTrue(t, "with answer_advance off, the cursor stays on the agent answered", 1500*time.Millisecond, func() (bool, string) {
					s := term.Snapshot()
					return !strings.Contains(s, "$ "+second), s
				})
				saveFrame(t, term, "answer-advance-off-after")
				savePNG(t, term.Screen(), shot.XTermPalette(), dir, "after")
				return
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "Allow Bash?", "$ "+second, "1 more needs you")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: after the answer the cursor did not move to the other approval: %v\n%s", err, term.Snapshot())
			}
			saveFrame(t, term, "answer-advance-on-next")
			savePNG(t, term.Screen(), shot.XTermPalette(), dir, "next")
			pause(t, "the view takes no answer key in the moment after a card is shown", answerSettle)
			if err := term.SendKeys("a", tuitest.Enter); err != nil {
				t.Fatalf("answer the second: %v", err)
			}
			waitHookAllow(t, term, hooks[second], "the second answer")
			alive(t, term, "after answering two approvals in a row")
		})
	}
}
