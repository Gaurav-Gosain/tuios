package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// TestQueuedMessageWithAClientIDIsQueuedOnce is the phone's send again after a
// lost answer: two queue-prompt calls with one client_id leave one entry, the
// second answers as the first did, and the message is typed once.
//
// The id belongs to its sender: the same id from a linked machine or from
// another one is a message of its own, and a machine that sends again on a new
// connection with a nonce it did not have is still the same sender. It lasts
// through an outage of hours, and past 7 days it is forgotten. An id sent again
// past that takes one place in the bound, so the ids that follow do not push
// out the new entry early. A file moves the daemon's clock for the dedupe.
//
// Negative controls: each is recorded in NEGATIVE_CONTROLS.md. With the dedupe
// in verbQueuePrompt replaced by a plain queuePrompt, the second call makes a
// second entry. With queueDedupAge at an hour, the send two hours later queues
// again. With the sender out of the key, the link's first send is called a
// duplicate. With the entry's by in the key, the send with a nonce queues
// again. With the old slot left in order, the last send queues again.
func TestQueuedMessageWithAClientIDIsQueuedOnce(t *testing.T) {
	const session = "e2e-client-id"
	base := t.TempDir()
	skew := filepath.Join(base, "dedup-skew-hours")
	if err := os.WriteFile(skew, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_E2E_QUEUE_DEDUP_SKEW", skew)
	moveClock := func(hours int) {
		t.Helper()
		if err := os.WriteFile(skew, []byte(strconv.Itoa(hours)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[hosts.phone]\nallow = [\"list\", \"write\", \"respond\"]\n\n[hosts.viewer]\nallow = [\"list\", \"write\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	working := func() {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "working"); err != nil {
			t.Fatalf("set-agent-state working: %v\n%s", err, out)
		}
	}
	working()

	// From this machine's shell.
	const marker = "client-id-once-c41d"
	const id = "phone-msg-1"
	shellSend := func() map[string]any {
		t.Helper()
		params := fmt.Sprintf(`{"session":%q,"text":"echo %s","client_id":%q}`, session, marker, id)
		out, err := tuiosCLI(t, base, "call", "queue-prompt", params)
		if err != nil {
			t.Fatalf("queue-prompt: %v\n%s", err, out)
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("queue-prompt answer %q: %v", out, err)
		}
		return res
	}
	first := shellSend()
	if _, dup := first["duplicate"]; dup {
		t.Fatalf("the first call says duplicate: %v", first)
	}
	second := shellSend()
	if second["duplicate"] != true || second["id"] != first["id"] {
		t.Fatalf("the second call = %v, want duplicate with the id of %v", second, first)
	}
	out, err := tuiosCLI(t, base, "queue", "ls", "-s", session)
	if err != nil {
		t.Fatalf("queue ls: %v\n%s", err, out)
	}
	if n := strings.Count(out, "echo "+marker); n != 1 {
		t.Fatalf("queue ls shows the message %d times, want once:\n%s", n, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
		if strings.Contains(out, "echo "+marker) {
			time.Sleep(2 * time.Second)
			out, _ = tuiosCLI(t, base, "capture-pane", "-s", session)
			if n := strings.Count(out, "echo "+marker); n != 1 {
				t.Fatalf("the message was typed %d times:\n%s", n, out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message was never typed:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	working()

	// The same id from other senders is another message.
	linkSend := func(c *linkStream, text, msgID, nonce string) map[string]any {
		t.Helper()
		params := map[string]any{"session": session, "text": text, "client_id": msgID}
		if nonce != "" {
			params["human_nonce"] = nonce
		}
		res, verr := c.call(t, "queue-prompt", params)
		if verr != nil {
			t.Fatalf("queue-prompt over the link: %v", verr)
		}
		return res
	}
	phone := startLinkAs(t, base, "phone").open(t, true)
	fromPhone := linkSend(phone, "from the phone", id, "")
	if fromPhone["duplicate"] == true {
		t.Fatalf("the same id from a linked machine is called a duplicate: %v", fromPhone)
	}
	viewer := startLinkAs(t, base, "viewer").open(t, true)
	if res := linkSend(viewer, "from the viewer", id, ""); res["duplicate"] == true {
		t.Fatalf("the same id from a second linked machine is called a duplicate: %v", res)
	}

	// The phone again, on a new connection, now with a nonce: the same sender.
	again := startLinkAs(t, base, "phone").open(t, true)
	pres, verr := again.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence: %v", verr)
	}
	nonce := pres["human_nonce"].(string)
	if res := linkSend(again, "from the phone", id, nonce); res["duplicate"] != true || res["id"] != fromPhone["id"] {
		t.Fatalf("the phone's send with a nonce = %v, want a duplicate of %v", res, fromPhone)
	}

	// Through an outage of hours the id still holds, and past 7 days it is gone.
	moveClock(2)
	if res := shellSend(); res["duplicate"] != true {
		t.Fatalf("two hours later the send queued again: %v", res)
	}
	moveClock(6 * 24)
	if res := shellSend(); res["duplicate"] != true {
		t.Fatalf("six days later the send queued again: %v", res)
	}
	moveClock(8 * 24)
	if res := shellSend(); res["duplicate"] == true {
		t.Fatalf("eight days later the send is still a duplicate: %v", res)
	}

	// That send took one place in the bound. The bound holds 256 and these
	// three ids come first: the phone's, the viewer's and the shell's. With
	// 254 more, 257 ids push out one, the phone's. An old slot left behind for
	// the shell's id would be first in line, and pushing it out would delete
	// the entry just made.
	for i := range 254 {
		res := linkSend(again, "another message", fmt.Sprintf("other-%d", i), nonce)
		if _, verr := again.call(t, "cancel-queued", map[string]any{"session": session, "id": res["id"], "human_nonce": nonce}); verr != nil {
			t.Fatalf("cancel-queued: %v", verr)
		}
	}
	if res := shellSend(); res["duplicate"] != true {
		t.Fatalf("the id sent again past its age lost its place early: %v", res)
	}
}
