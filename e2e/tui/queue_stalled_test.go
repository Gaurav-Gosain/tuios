package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func (s *stallStage) hook(event, state string) {
	s.t.Helper()
	s.hookWith(event, state, "")
}

func (s *stallStage) hookWith(event, state, extra string) {
	s.t.Helper()
	payload := `{"hook_event_name":"` + event + `","session_id":"stall-e2e","prompt":"the queued message","last_assistant_message":"done"` + extra + `}`
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

func (s *stallStage) queue(text string) {
	s.t.Helper()
	s.cli("queue "+text, "queue", "-s", s.sess, "-w", s.win, text)
	s.waitScreen("the queued message was typed", func(screen string) bool { return strings.Contains(screen, text) })
	s.log.add("%-44s typed", "queue "+text)
}

func (s *stallStage) queueState() string {
	s.t.Helper()
	return s.cli("queue ls", "queue", "ls", "-s", s.sess, "-w", s.win)
}

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
	s.hook("UserPromptSubmit", "working")
	s.waitQuestion("a hook report of working", false, uiTimeout)
	if out := s.queueState(); !strings.Contains(out, "Nothing is queued") {
		t.Errorf("ASSERTION: the stalled message stayed queued after the pane worked: %q", out)
	}
	s.cli("clear the box", "send-keys", "-s", s.sess, "-w", s.win, "C-u")
	s.waitScreen("the box was cleared", func(text string) bool { return !strings.Contains(text, first) })
	s.rest()

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

// TestQueuedMessageLeftInTheBoxStaysStalledThroughALateStop: a message the
// agent left in its input box stays stalled when a finished turn or a
// needs_input comes late. Neither says the message was taken: the agent can
// end the turn it was already on, or ask something, with the text still in
// the box, and dropping the entry then would lose text that was never sent.
// The watch goes on after them, and the person pressing Enter, which the
// screen shows taken, still settles it.
//
// Negative control: with the late gate counting done and needs_input as the
// five second gate does, the late Stop drops the stalled message and the test
// fails on it.
func TestQueuedMessageLeftInTheBoxStaysStalledThroughALateStop(t *testing.T) {
	s := newStallStage(t, "queue-stalled-late-stop")
	s.hook("SessionStart", "idle")

	s.setMode("busy")
	const marker = "queued-left-then-stop-5c7e"
	s.queue(marker)
	s.waitQuestion("Enter not read as submit", true, 3*promptStallE2E)

	stillStalled := func(step string) {
		t.Helper()
		time.Sleep(3 * time.Second)
		out := s.queueState()
		s.log.add("%-44s question open=%v queue=%q", step, len(s.stalledQuestions()) > 0, strings.TrimSpace(out))
		if !strings.Contains(out, "stalled") {
			t.Fatalf("ASSERTION: %s dropped the message still in the box: %q\nscreen:\n%s", step, out, s.screen())
		}
		if screen := s.screen(); !strings.Contains(screen, marker) || strings.Contains(screen, "> "+marker) {
			t.Fatalf("the stand-in moved the message out of the box\n%s", screen)
		}
	}

	s.cli("end the turn on the screen", "send-keys", "-s", s.sess, "-w", s.win, "C-r")
	s.hook("Stop", "done")
	stillStalled("a late Stop")
	s.hookWith("Notification", "needs_input", `,"notification_type":"agent_needs_input","message":"Claude needs your input"`)
	stillStalled("a late agent_needs_input")

	s.setMode("take")
	s.cli("the person presses Enter", "send-keys", "-s", s.sess, "-w", s.win, "Enter")
	s.waitScreen("the stand-in took it", func(text string) bool { return strings.Contains(text, "> "+marker) })
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(s.queueState(), "Nothing is queued") {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the message the screen shows taken stayed queued: %q", s.queueState())
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.log.add("%-44s queue=%q", "taken on Enter", strings.TrimSpace(s.queueState()))
	s.rest()
}

// TestDroppingAStalledQueuedMessageEndsItsWatch: a stalled message dropped
// with tuios queue rm is no longer watched. The watch would otherwise look at
// the pane every second for ten minutes for a message nobody is waiting on.
// The daemon log says when a watch ends that way.
//
// Negative control: with removeLocked not closing the entry's unwatch, the
// log never says so and the test fails on it.
func TestDroppingAStalledQueuedMessageEndsItsWatch(t *testing.T) {
	s := newStallStage(t, "queue-stalled-rm")
	s.hook("UserPromptSubmit", "working")
	s.hook("Stop", "done")

	s.setMode("busy")
	s.queue("queued-then-dropped-2f6a")
	s.waitQuestion("Enter not read as submit", true, 3*promptStallE2E)
	s.cli("queue rm", "queue", "rm", "--all", "-s", s.sess, "-w", s.win)
	s.waitQuestion("dropped with queue rm", false, uiTimeout)

	const ended = "Stopped watching window "
	deadline := time.Now().Add(uiTimeout)
	for {
		data, err := os.ReadFile(daemonLogOf(t, s.base))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), ended) {
			s.log.add("%-44s watch ended", "dropped with queue rm")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the watch on the dropped message did not end within %s\n%s", uiTimeout, daemonLogTail(s.base, 20))
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.cli("clear the box", "send-keys", "-s", s.sess, "-w", s.win, "C-u")
	s.rest()
}

const promptStallE2E = 5 * time.Second
