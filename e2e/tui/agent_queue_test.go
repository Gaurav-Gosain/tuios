package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestQueuedMessageIsTypedWhenTheAgentRests drives the delivery queue against
// a real daemon and client. A pane reports a working agent, and tuios queue
// leaves it a message: it is listed as waiting, get-agent-state counts it, and
// nothing reaches the pane. When the pane reports idle the message is typed
// into it, where the attached client shows it, and the queue is empty again.
//
// Then the same pane runs a stand-in for Claude Code's input box, and the
// person types half a prompt into it while the agent rests. A message queued
// then waits: the agents view says "Waits: your draft is in the prompt box",
// and once the draft has held it long enough the row moves to Needs you. The
// person clears the box, and the message is typed only after three seconds
// with no key from the person. Frames of each step are saved under
// artifactDir.
//
// Negative control: with the noteQueueEvent call taken out of the session
// event sink, the message is never typed and the wait for it times out. With
// holdLocked in deliverQueued always reporting nothing held, the second
// message is typed into the draft and the test fails on that.
func TestQueuedMessageIsTypedWhenTheAgentRests(t *testing.T) {
	// The draft's move to Needs you takes ten minutes in use.
	t.Setenv("TUIOS_E2E_DRAFT_SURFACE_MS", "9000")
	term, base := attachClientBase(t)
	const marker = "queued-reply-7f3a"

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "working"); err != nil {
		t.Fatalf("set-agent-state working: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "queue", "-s", "e2e-ctrlp", "echo", marker); err != nil {
		t.Fatalf("tuios queue: %v\n%s", err, out)
	} else if !strings.Contains(out, "typed when the agent comes to rest") {
		t.Errorf("tuios queue said %q, want it to wait for the agent", out)
	}
	out, err := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-ctrlp")
	if err != nil || !strings.Contains(out, "waiting") || !strings.Contains(out, "echo "+marker) {
		t.Fatalf("tuios queue ls = %q (%v), want the waiting entry", out, err)
	}
	if out, err := tuiosCLI(t, base, "get-agent-state", "-s", "e2e-ctrlp", "--json"); err != nil || !strings.Contains(strings.ReplaceAll(out, " ", ""), `"queued":1`) {
		t.Errorf("get-agent-state = %q (%v), want queued 1", out, err)
	}
	time.Sleep(time.Second)
	if strings.Contains(term.Screen().Text(), marker) {
		t.Fatalf("the message was typed into a working agent\n%s", term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	if err := term.WaitForText(marker, uiTimeout); err != nil {
		t.Fatalf("the queued message was never typed at rest: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "agent-queue-typed")
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-ctrlp")
		if err == nil && strings.Contains(out, "Nothing is queued") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the typed message stayed queued: %q (%v)", out, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after the queued message was typed")

	queueWaitsForTheDraft(t, term, base)
}

// draftBoxScript is the stand-in agent: a box drawn the way Claude Code draws
// its input box, between two rules, that shows what is typed into it. Enter
// moves the line above the box as "sent: ...", and Ctrl+U clears the box.
const draftBoxScript = `stty -icanon -echo min 1 2>/dev/null
draft=""
sent=""
rule=$(printf '\342\224\200%.0s' $(seq 1 40))
draw() {
	printf '\033[H\033[2J'
	printf 'fakebox ready\n%s\n%s\n\342\235\257 %s\n%s\n  ? for shortcuts\n' "$sent" "$rule" "$draft" "$rule"
}
draw
while IFS= read -r -n1 -d '' c; do
	case "$c" in
	$'\r' | $'\n') sent="sent: $draft"; draft="" ;;
	$'\x15') draft="" ;;
	$'\x7f') draft="${draft%?}" ;;
	*) draft="$draft$c" ;;
	esac
	draw
done
`

// queueWaitsForTheDraft is the draft half of
// TestQueuedMessageIsTypedWhenTheAgentRests.
func queueWaitsForTheDraft(t *testing.T, term *tuitest.Terminal, base string) {
	const marker, draft = "queued-after-draft-5c1e", "half a thought"
	cli := func(what string, args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return out
	}
	script := filepath.Join(t.TempDir(), "box.sh")
	if err := os.WriteFile(script, []byte(draftBoxScript), 0o644); err != nil {
		t.Fatal(err)
	}
	cli("start the stand-in agent", "send-text", "-s", "e2e-ctrlp", "bash "+script+"\n")
	if err := term.WaitForText("fakebox ready", uiTimeout); err != nil {
		t.Fatalf("the stand-in agent never drew its box: %v\n%s", err, term.Snapshot())
	}
	// A turn ends: the agent is at rest, with its box empty.
	cli("set working", "set-agent-state", "-s", "e2e-ctrlp", "working", "--harness", "claude-code")
	cli("set idle", "set-agent-state", "-s", "e2e-ctrlp", "idle", "--harness", "claude-code")

	enterTerminalMode(t, term)
	if err := term.Type(draft); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("❯ "+draft, uiTimeout); err != nil {
		t.Fatalf("the draft never reached the box: %v\n%s", err, term.Snapshot())
	}
	cli("queue behind the draft", "queue", "-s", "e2e-ctrlp", "echo", marker)

	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(strings.ReplaceAll(cli("get-agent-state", "get-agent-state", "-s", "e2e-ctrlp", "--json"), " ", ""), `"draft_held":true`) {
		if strings.Contains(term.Screen().Text(), marker) {
			t.Fatalf("ASSERTION: the queued message was typed into the person's draft\n%s", term.Snapshot())
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never marked the queue as held by the draft\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	// The row reads "Waits:" and the reason, on one line or two by the layout.
	const waits = "your draft is in the prompt box"
	showsWait := func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Waits:") && strings.Contains(text, waits)
	}
	if err := term.WaitFor(showsWait, uiTimeout); err != nil {
		t.Fatalf("the agents view never said the draft holds the queue: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "agent-queue-draft-waits")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "agent-queue-draft-waits")
	if strings.Contains(term.Screen().Text(), marker) {
		t.Fatalf("ASSERTION: the queued message was typed into the person's draft\n%s", term.Snapshot())
	}

	// Held long enough, the row moves to Needs you.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return showsWait(s) && strings.Contains(s.Text(), "1 needs you")
	}, 20*time.Second); err != nil {
		t.Fatalf("the held draft never moved to Needs you: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "agent-queue-draft-needs-you")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "agent-queue-draft-needs-you")
	if strings.Contains(term.Screen().Text(), marker) {
		t.Fatalf("ASSERTION: the queued message was typed into the person's draft\n%s", term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), waits) }, uiTimeout); err != nil {
		t.Fatalf("the agents view never closed: %v\n%s", err, term.Snapshot())
	}
	// The person clears the box. The message waits for three quiet seconds.
	cleared := time.Now()
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("sent: echo "+marker, uiTimeout); err != nil {
		t.Fatalf("the queued message was never typed after the draft was cleared: %v\n%s", err, term.Snapshot())
	}
	if took := time.Since(cleared); took < 2500*time.Millisecond {
		t.Errorf("the message was typed %s after the person's last key, want at least the 3 s quiet", took)
	}
	if strings.Contains(term.Screen().Text(), draft) {
		t.Errorf("the draft is still on the screen after the person cleared it\n%s", term.Snapshot())
	}
	saveArtifact(t, term, dir, "agent-queue-draft-typed")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "agent-queue-draft-typed")
	// The agent takes it, which is what the prompt gate waits for.
	cli("set working", "set-agent-state", "-s", "e2e-ctrlp", "working", "--harness", "claude-code")
	deadline = time.Now().Add(uiTimeout)
	for {
		out := cli("queue ls", "queue", "ls", "-s", "e2e-ctrlp")
		if strings.Contains(out, "Nothing is queued") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the typed message stayed queued: %q", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after the message was typed behind the draft")
}

// TestNarrowRailKeepsTheAgentNameBesideAQueue: on the rail an 80 column
// screen gets, "1 queued" used to leave "a…" of an agent called agent. The
// name wins: the figure shortens or goes before the name is cut. The frame
// is saved under artifactDir.
//
// How this could pass wrongly, written down first: "agent" is also the
// session's name, so the row read is the one under the agents section's
// header and inside the rail's columns; and the row might be drawn before the
// queue reaches it, so the test waits for its queue figure first.
func TestNarrowRailKeepsTheAgentNameBesideAQueue(t *testing.T) {
	const cols, rows = 80, 24
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "agent", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "agent", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "agent", "--name", "agent"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "queue", "-s", "agent", "also add a CHANGELOG entry"); err != nil {
		t.Fatalf("queue: %v\n%s", err, out)
	}
	term := attachIn(t, base, "agent", startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - narrowRailWidth
	railText := func(s tuitest.Screen, y int) string {
		var b strings.Builder
		for x := railCol; x < cols; x++ {
			b.WriteString(s.Cell(x, y).Content)
		}
		return b.String()
	}
	// The row under the agents section's header, which is the agent's.
	agentRow := func(s tuitest.Screen) string {
		for y := range rows - 1 {
			if strings.HasPrefix(strings.TrimSpace(strings.Trim(railText(s, y), "│ ")), "agents") {
				return railText(s, y+1)
			}
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(agentRow(s), "q") }, uiTimeout); err != nil {
		t.Fatalf("the rail never listed the agent: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-80x24")
	if row := agentRow(term.Screen()); !strings.Contains(row, "agent") {
		t.Errorf("the rail cut the agent's name for its queue: %q\n%s", row, term.Snapshot())
	}
}
