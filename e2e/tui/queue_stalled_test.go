package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A queued message is typed into a pane, and the daemon then waits five
// seconds for the agent to show it took it. With Claude Code that sign is the
// hook's working report, and on a loaded machine Claude Code killed the hook
// at its five second timeout: the report never came, the message read as not
// taken, and the Inbox asked the person to look at a pane that was busy on
// it. Nothing closed that question, since the hook that would have said done
// was killed too and the pane's state never changed.
//
// What could go wrong, written down before the fix:
//   - the gate reads only the pane's state, which a lost or late report never
//     moves, so a taken message reads as stalled;
//   - the agent is busy when the message lands and takes it at the end of its
//     turn, after the five seconds;
//   - the pane's state changes after the gate gave up and before the question
//     opens, so the close runs before the open and the question stays;
//   - the question closes only on a state change, so with every report lost it
//     stays open for good;
//   - the other way: a message left in the input box, which is the stall the
//     gate is for, reads as taken because the screen shows the agent at work
//     on something else;
//   - closing by the pane's screen closes a different question the pane
//     raised on the same key.
//
// The tests run the real hook for the reports that do arrive, against a pane
// drawing Claude Code's input box, and leave the pane's state log and frames
// under TUIOS_E2E_FRAMES.

// stallBoxScript is the stand-in: Claude Code's input box between two rules.
// Enter does what the file named by $1 says. "take" moves the box's text above
// it and shows a turn running, as Claude Code does when it takes a prompt.
// "busy" leaves the text in the box, a submit key the agent did not read as
// one, while the screen shows a turn running on something else. Ctrl+U clears
// the box and Ctrl+R ends the turn on the screen.
const stallBoxScript = `stty -icanon -echo min 1 2>/dev/null
mode="$1"
draft=""
sent=""
spin=""
foot="  ? for shortcuts"
rule=$(printf '\342\224\200%.0s' $(seq 1 40))
draw() {
	printf '\033[H\033[2J'
	printf 'fakeclaude ready\n%s\n%s\n%s\n\342\235\257 %s\n%s\n%s\n' "$sent" "$spin" "$rule" "$draft" "$rule" "$foot"
}
draw
while IFS= read -r -n1 -d '' c; do
	case "$c" in
	$'\r' | $'\n')
		if [ "$(cat "$mode")" = take ]; then
			sent="> $draft"
			draft=""
		fi
		spin="✻ Brewing… (1s · ↑ 1.2k tokens)"
		foot="  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt"
		;;
	$'\x15') draft="" ;;
	$'\x12')
		spin=""
		foot="  ? for shortcuts"
		;;
	*) draft="$draft$c" ;;
	esac
	draw
done
`

// stallStage is one pane in a throwaway daemon running stallBoxScript, with
// Claude Code's real hook run for it.
type stallStage struct {
	t               *testing.T
	base, sess, win string
	mode            string
	log             *stateLog
}

func newStallStage(t *testing.T, logName string) *stallStage {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-stall"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "claude", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	dir := t.TempDir()
	s := &stallStage{t: t, base: base, sess: session, win: windowID(t, base, session, "claude"), mode: filepath.Join(dir, "mode"), log: &stateLog{name: logName}}
	t.Cleanup(func() { s.log.save(t) })
	script := filepath.Join(dir, "box.sh")
	if err := os.WriteFile(script, []byte(stallBoxScript), 0o644); err != nil {
		t.Fatal(err)
	}
	s.setMode("take")
	s.cli("start the stand-in", "send-text", "-s", session, "-w", s.win, "bash "+script+" "+s.mode+"\n")
	s.waitScreen("the stand-in drew its box", func(text string) bool { return strings.Contains(text, "fakeclaude ready") })
	return s
}

func (s *stallStage) cli(what string, args ...string) string {
	s.t.Helper()
	out, err := tuiosCLI(s.t, s.base, args...)
	if err != nil {
		s.t.Fatalf("%s: %v\n%s", what, err, out)
	}
	return out
}

func (s *stallStage) setMode(mode string) {
	s.t.Helper()
	if err := os.WriteFile(s.mode, []byte(mode), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// hook runs Claude Code's hook for event and waits for the pane to be on
// state.
func (s *stallStage) hook(event, state string) {
	s.t.Helper()
	payload := `{"hook_event_name":"` + event + `","session_id":"stall-e2e","prompt":"the queued message","last_assistant_message":"done"}`
	if out := runHook(s.t, s.base, "claude-code", s.sess, s.win, payload); out != "" {
		s.t.Fatalf("%s: the hook printed %q, want nothing", event, out)
	}
	waitAgentState(s.t, s.base, s.sess, s.win, state, "claude-code", s.log, event)
}

func (s *stallStage) screen() string {
	s.t.Helper()
	return s.cli("capture-pane", "capture-pane", "-s", s.sess, "-w", s.win)
}

func (s *stallStage) waitScreen(step string, ok func(string) bool) string {
	s.t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		text := s.screen()
		if ok(text) {
			return text
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%s: the pane never showed it:\n%s", step, text)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stalledQuestions lists the open Inbox questions saying the pane did not take
// a queued message.
func (s *stallStage) stalledQuestions() []string {
	s.t.Helper()
	out := s.cli("list-attention", "list-attention", "--json")
	var res struct {
		Items []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Window  string `json:"window"`
			Summary string `json:"summary"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		s.t.Fatalf("list-attention: %v\n%s", err, out)
	}
	var ids []string
	for _, it := range res.Items {
		if it.Window == s.win && it.Kind == "question" && strings.Contains(it.Summary, "did not take it") {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// queue queues text for the pane and waits for it to be typed: the box holds
// it, or the stand-in moved it above the box.
func (s *stallStage) queue(text string) {
	s.t.Helper()
	s.cli("queue "+text, "queue", "-s", s.sess, "-w", s.win, text)
	s.waitScreen("the queued message was typed", func(screen string) bool { return strings.Contains(screen, text) })
	s.log.add("%-44s typed", "queue "+text)
}

// queueState is what tuios queue ls says about the pane.
func (s *stallStage) queueState() string {
	s.t.Helper()
	return s.cli("queue ls", "queue", "ls", "-s", s.sess, "-w", s.win)
}

// waitQuestion waits for the stalled question to be open (want true) or
// closed, and fails the test with step when it does not come to that.
func (s *stallStage) waitQuestion(step string, want bool, within time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(within)
	for {
		open := len(s.stalledQuestions()) > 0
		if open == want {
			s.log.add("%-44s question open=%v queue=%q", step, open, strings.TrimSpace(s.queueState()))
			return
		}
		if time.Now().After(deadline) {
			s.log.add("%-44s question open=%v queue=%q", step, open, strings.TrimSpace(s.queueState()))
			s.t.Fatalf("ASSERTION: %s: the stalled question is open=%v after %s, want %v\nqueue: %s\nscreen:\n%s", step, open, within, want, s.queueState(), s.screen())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// rest ends a turn: the screen's turn is over, Ctrl+R, and the Stop hook says
// done.
func (s *stallStage) rest() {
	s.t.Helper()
	s.cli("end the turn on the screen", "send-keys", "-s", s.sess, "-w", s.win, "C-r")
	s.waitScreen("the turn ended on the screen", func(text string) bool { return !strings.Contains(text, "esc to interrupt") })
	s.hook("Stop", "done")
}

// TestQueuedMessageTakenWhileItsHookIsLostRaisesNoQuestion: the pane takes the
// queued message, clears its box and shows a turn running, and the hook that
// would say working does not come within the five seconds, as when Claude
// Code killed it on a loaded machine. No question opens, and the message
// leaves the queue. The hook coming late, after the gate, changes nothing.
//
// Negative control: with the screen evidence taken out of promptGate.check,
// the question opens five seconds after the message is typed and the test
// fails on it.
func TestQueuedMessageTakenWhileItsHookIsLostRaisesNoQuestion(t *testing.T) {
	s := newStallStage(t, "queue-taken-hook-lost")
	s.hook("UserPromptSubmit", "working")
	s.hook("Stop", "done")

	const marker = "queued-and-taken-4d2b"
	s.queue(marker)
	s.waitScreen("the stand-in took it", func(text string) bool { return strings.Contains(text, "> "+marker) })
	// Past the five second gate, with no report from the hook.
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if ids := s.stalledQuestions(); len(ids) > 0 {
			s.log.add("%-44s question open=%v", "no hook for 7s", true)
			t.Fatalf("ASSERTION: the message the pane took opened a not-taken question %v\nqueue: %s\nscreen:\n%s", ids, s.queueState(), s.screen())
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.log.add("%-44s question open=false queue=%q", "no hook for 7s", strings.TrimSpace(s.queueState()))
	if out := s.queueState(); !strings.Contains(out, "Nothing is queued") {
		t.Errorf("ASSERTION: the taken message is still queued: %q", out)
	}
	if st := readAgentState(t, s.base, s.sess, s.win); st.State != "done" {
		t.Errorf("the pane is %s, want it still on the done its last report said", st.State)
	}

	// The hook comes late.
	s.hook("UserPromptSubmit", "working")
	s.waitQuestion("the late hook", false, time.Second)
	s.rest()
}

// TestQueuedMessageLeftInTheBoxRaisesAQuestionThatClosesOnceTaken: a message
// the agent leaves in its input box still opens the question, and the
// question closes once the pane is past it, with or without a report.
//
// First the pane's next state change closes it: the Enter is not read as
// submit, the message sits in the box past the gate while the screen shows
// the agent at work on something else, and the question opens,
// and a hook report of working closes it and drops the message. Then the
// message sits in the box again, the person presses Enter in the pane, the
// agent takes it, and no hook reports it. The question closes and the message
// leaves the queue.
//
// Negative control: with watchStalledQueue returning at once, the question
// opened for the second message stays open and the test fails on that. With
// screenShowsTaken not reading the input box, the question never opens for
// the first message.
func TestQueuedMessageLeftInTheBoxRaisesAQuestionThatClosesOnceTaken(t *testing.T) {
	s := newStallStage(t, "queue-left-in-box")
	s.hook("UserPromptSubmit", "working")
	s.hook("Stop", "done")

	s.setMode("busy")
	const first = "queued-left-in-box-8a1c"
	s.queue(first)
	s.waitQuestion("Enter not read as submit", true, 3*promptStallE2E)
	if out := s.queueState(); !strings.Contains(out, "stalled") {
		t.Errorf("ASSERTION: the message left in the box is not marked stalled: %q", out)
	}
	if strings.Contains(s.screen(), "> "+first) {
		t.Fatalf("the stand-in took a message it was meant to leave in the box\n%s", s.screen())
	}
	// The pane's next state change closes it.
	s.hook("UserPromptSubmit", "working")
	s.waitQuestion("a hook report of working", false, uiTimeout)
	if out := s.queueState(); !strings.Contains(out, "Nothing is queued") {
		t.Errorf("ASSERTION: the stalled message stayed queued after the pane worked: %q", out)
	}
	s.cli("clear the box", "send-keys", "-s", s.sess, "-w", s.win, "C-u")
	s.waitScreen("the box was cleared", func(text string) bool { return !strings.Contains(text, first) })
	s.rest()

	// Taken late, and nothing reports it.
	const second = "queued-taken-late-3e9f"
	s.queue(second)
	s.waitQuestion("left in the box again", true, 3*promptStallE2E)
	s.setMode("take")
	s.cli("the person presses Enter", "send-keys", "-s", s.sess, "-w", s.win, "Enter")
	s.waitScreen("the stand-in took it", func(text string) bool { return strings.Contains(text, "> "+second) })
	s.waitQuestion("taken late with no report", false, uiTimeout)
	if out := s.queueState(); !strings.Contains(out, "Nothing is queued") {
		t.Errorf("ASSERTION: the message taken late stayed queued: %q", out)
	}
	if st := readAgentState(t, s.base, s.sess, s.win); st.State != "done" {
		t.Errorf("the pane is %s, want it still on done: nothing reported", st.State)
	}
	s.rest()
}

// promptStallE2E is the daemon's stall window, five seconds.
const promptStallE2E = 5 * time.Second
