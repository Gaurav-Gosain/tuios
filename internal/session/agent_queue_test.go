package session

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// queueFixture is a daemon whose queue looks at a pane 50 ms after it comes
// to rest, with a session work of two windows, a verb connection from outside
// every pane, and the second window's id.
func queueFixture(t *testing.T) (*Daemon, string, *Session, *verbConn, string, string) {
	t.Helper()
	d, sp := startTestDaemon(t)
	d.queue.rest = 50 * time.Millisecond
	sess, a, b := twoWindowSession(t, d, "work")
	return d, sp, sess, dialVerb(t, sp), a, b
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within * time.Duration(testDeadlineScale))
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, within)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// paneShows reports whether the window's screen or history holds s.
func paneShows(t *testing.T, d *Daemon, sess *Session, window, s string) bool {
	t.Helper()
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	return strings.Contains(pty.CaptureContent(true, false), s)
}

// queuedEntries is list-queued's entries for window.
func queuedEntries(t *testing.T, c *verbConn, window string) []map[string]any {
	t.Helper()
	res := result(t, callP(c, t, "list-queued", map[string]any{"session": "work", "window": window}))
	raw, _ := res["entries"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		out = append(out, e.(map[string]any))
	}
	return out
}

// TestQueuedPaneEntryIsCheckedAgainWhenTyped: an entry a pane queued is typed
// only if the pane may still type into the target, by its grants as they are
// when the agent comes to rest. A pane whose grants shrank loses its entry,
// and nothing is typed.
func TestQueuedPaneEntryIsCheckedAgainWhenTyped(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.queue.rest = 50 * time.Millisecond
	sess := d.manager.GetSession("a")
	person := dialVerb(t, sp)
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read", "write"}}))
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a2, "grants": []string{"read"}}))
	setAgentState(t, person, "a", a2, "working", "", "")

	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	pane := dialVerb(t, sp)
	result(t, callP(pane, t, "queue-prompt", map[string]any{"window": a2, "text": "echo from-a1"}))
	d.approvalPeer = nil

	// The person takes write away from a1 before the agent rests.
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read"}}))
	setAgentState(t, person, "a", a2, "idle", "", "")
	eventually(t, "the entry is dropped at delivery", 3*time.Second, func() bool { return d.queue.count(a2) == 0 })
	time.Sleep(200 * time.Millisecond)
	if paneShows(t, d, sess, a2, "from-a1") {
		t.Fatal("an entry was typed for a pane that no longer holds write")
	}

	// With its grants intact, the same entry is typed.
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read", "write"}}))
	setAgentState(t, person, "a", a2, "working", "", "")
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	pane2 := dialVerb(t, sp)
	result(t, callP(pane2, t, "queue-prompt", map[string]any{"window": a2, "text": "echo again-a1"}))
	d.approvalPeer = nil
	setAgentState(t, person, "a", a2, "idle", "", "")
	eventually(t, "the entry is typed", 5*time.Second, func() bool { return paneShows(t, d, sess, a2, "again-a1") })
}

// TestQueueOriginRefusalForALink: an entry a linked machine queued is typed
// only while that machine's policy still allows write.
func TestQueueOriginRefusalForALink(t *testing.T) {
	d, _, sess, _, _, b := queueFixture(t)
	target, _ := findWindowState(sess.GetState(), b)
	e := &queueEntry{origin: queueOrigin{kind: "link", peer: "laptop"}}
	if why := d.queueOriginRefusal(e, sess, target); why != "" {
		t.Fatalf("the default policy, which allows write, refused: %s", why)
	}
	d.SetLinkPolicies(map[string]config.HostConfig{"laptop": {Allow: []string{"list"}}})
	if why := d.queueOriginRefusal(e, sess, target); why == "" || !strings.Contains(why, "laptop") {
		t.Fatalf("a link that may only list was not refused: %q", why)
	}
}

// TestCancelQueuedOwnership: the person drops anything, a pane only what it
// queued, a shell anything but the person's.
func TestCancelQueuedOwnership(t *testing.T) {
	d, sp, _, c, a, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	tui := attachTUI(t, sp, "work")

	byPerson := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "from the person", "human_nonce": tui.HumanNonce()}))
	byShell := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "from a shell"}))
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	pane := dialVerb(t, sp)
	byPane := result(t, callP(pane, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "from pane a"}))

	entries := queuedEntries(t, c, b)
	if len(entries) != 3 || entries[0]["by"] != queueByHuman || entries[1]["by"] != queueByShell || entries[2]["by"] != a {
		t.Fatalf("list-queued = %v, want by human, shell and pane a", entries)
	}
	// A bad nonce is refused, not taken as the shell.
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "x", "human_nonce": "0123456789abcdef0123456789abcdef"}), ErrVerbNotHuman, "a nonce that does not verify")

	// The pane may not drop the person's or the shell's, and drops its own.
	wantForbidden(t, "a pane dropping the person's entry", callP(pane, t, "cancel-queued", map[string]any{"session": "work", "id": byPerson["id"]}))
	wantForbidden(t, "a pane dropping the shell's entry", callP(pane, t, "cancel-queued", map[string]any{"session": "work", "id": byShell["id"]}))
	got := result(t, callP(pane, t, "cancel-queued", map[string]any{"session": "work", "window": b, "all": true}))
	if ids, _ := got["cancelled"].([]any); len(ids) != 1 || ids[0] != byPane["id"] || got["queued"] != float64(2) {
		t.Fatalf("a pane's cancel all = %v, want only its own entry dropped", got)
	}
	// Nor may it name itself as another sender.
	wantForbidden(t, "a pane queueing as another window", callP(pane, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "x", "from": b}))
	d.approvalPeer = nil

	// A shell may not drop the person's entry, and may drop its own.
	wantForbidden(t, "a shell dropping the person's entry", callP(c, t, "cancel-queued", map[string]any{"session": "work", "id": byPerson["id"]}))
	result(t, callP(c, t, "cancel-queued", map[string]any{"session": "work", "id": byShell["id"]}))
	// The person drops theirs.
	got = result(t, callP(c, t, "cancel-queued", map[string]any{"session": "work", "window": b, "all": true, "human_nonce": tui.HumanNonce()}))
	if ids, _ := got["cancelled"].([]any); len(ids) != 1 || ids[0] != byPerson["id"] || got["queued"] != float64(0) {
		t.Fatalf("the person's cancel all = %v, want their entry dropped and the queue empty", got)
	}
	mustRefuse(t, callP(c, t, "cancel-queued", map[string]any{"session": "work", "id": "q999"}), ErrVerbInvalidParams, "an id that is not queued")
}

// TestQueueStampOutlivesTheQueue: an entry queued just after the queue
// emptied still waits for a rest reached after the last entry was typed,
// rather than being typed at once into an agent still on that entry.
func TestQueueStampOutlivesTheQueue(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	d.queue.quiet = 1500 * time.Millisecond
	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateUnknown}); err != nil {
		t.Fatal(err)
	}
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo stamp-$((10+1))"}))
	eventually(t, "the first entry is typed and the queue empties", 5*time.Second, func() bool {
		return paneShows(t, d, sess, b, "stamp-11") && d.queue.count(b) == 0
	})
	second := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo stamp-$((10+2))"}))
	if second["delivering"] != false {
		t.Errorf("an entry queued while the agent is still on the last one reports delivering %v, want false", second["delivering"])
	}
	time.Sleep(300 * time.Millisecond)
	if paneShows(t, d, sess, b, "stamp-$((10+2))") {
		t.Fatal("the entry was typed without a new rest after the last one")
	}
	eventually(t, "the entry is typed at the next rest", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "stamp-12") })

	// The stamp goes with the agent, and with the pane.
	d.queue.mu.Lock()
	_, stamped := d.queue.typed[b]
	d.queue.mu.Unlock()
	if !stamped {
		t.Fatal("no stamp was kept for the pane")
	}
	setAgentState(t, c, "work", b, "none", "", "")
	eventually(t, "the stamp goes when the agent leaves", 2*time.Second, func() bool {
		d.queue.mu.Lock()
		defer d.queue.mu.Unlock()
		_, ok := d.queue.typed[b]
		return !ok && d.queue.stamped.Load() == 0
	})
}

// TestQueueStampIsTheSubmitTime: an agent quick enough to go working and back
// to done before the prompt gate returns has reached a rest after the entry
// was typed, and the next entry is typed at it.
func TestQueueStampIsTheSubmitTime(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	if reg := d.agentMatcher.registry; reg == nil || !reg.CanProveWorking("claude-code") {
		t.Skip("the registry has no claude-code rules that show working")
	}
	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateIdle, Harness: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	// The fast turn: once the first command has run, which is after its
	// Enter, the agent reports working and done back to back.
	pty, err := d.resolvePTYForTarget(sess, b)
	if err != nil {
		t.Fatal(err)
	}
	turned := make(chan struct{})
	var workingAt int64
	go func() {
		defer close(turned)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(pty.CaptureContent(true, false), "fast-7") {
				_, _, _ = sess.ApplyAgentReport(b, AgentReport{State: AgentStateWorking, Harness: "claude-code"})
				w, _ := findWindowState(sess.GetState(), b)
				workingAt = w.AgentStateAt
				_, _, _ = sess.ApplyAgentReport(b, AgentReport{State: AgentStateDone, Harness: "claude-code"})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo fast-$((3+4))"}))
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo fast-$((4+4))"}))
	<-turned
	if workingAt == 0 {
		t.Fatal("the first entry never ran")
	}
	// The gate can return only after it saw working, so a stamp taken when
	// it returned would be later than working and hide the rest after it.
	eventually(t, "the first entry is stamped", 5*time.Second, func() bool {
		d.queue.mu.Lock()
		defer d.queue.mu.Unlock()
		return d.queue.typed[b].at != 0
	})
	d.queue.mu.Lock()
	stamp := d.queue.typed[b].at
	d.queue.mu.Unlock()
	if stamp >= workingAt {
		t.Fatalf("the stamp %d is not before the working report at %d: it is not the submit time", stamp, workingAt)
	}
	eventually(t, "the second entry is typed at the rest the fast turn reached", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "fast-8") })
}

// TestQueueRechecksNeedsInputRightBeforeTyping: a pane that reaches
// needs_input after the look found it at rest, and before the entry is typed,
// is not typed into, and the entry waits.
func TestQueueRechecksNeedsInputRightBeforeTyping(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "idle", "", "")
	var once sync.Once
	d.queue.beforeType = func(window string) {
		once.Do(func() {
			if _, _, err := sess.ApplyAgentReport(window, AgentReport{State: AgentStateNeedsInput, Kind: "question", Message: "pick one"}); err != nil {
				t.Error(err)
			}
		})
	}
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo blocked-entry"}))
	eventually(t, "the look reaches the pane", 3*time.Second, func() bool {
		w, _ := findWindowState(sess.GetState(), b)
		return w.AgentState == AgentStateNeedsInput
	})
	time.Sleep(400 * time.Millisecond)
	if paneShows(t, d, sess, b, "blocked-entry") {
		t.Fatal("a queued entry was typed into a pane on needs_input")
	}
	if got := queuedEntries(t, c, b); len(got) != 1 || got[0]["state"] != queueWaiting {
		t.Fatalf("list-queued = %v, want the entry waiting again", got)
	}
}

// TestCancelQueuedUnnamedLinks: two linked machines that gave no name share
// the empty peer, and neither may drop what the other queued. Each drops its
// own.
func TestCancelQueuedUnnamedLinks(t *testing.T) {
	d, _, _, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	one := &connState{clientID: "link-one", viaLink: true, done: make(chan struct{})}
	two := &connState{clientID: "link-two", viaLink: true, done: make(chan struct{})}
	queue := func(cs *connState, text string) string {
		res, verr := d.verbQueuePrompt(cs, mustJSON(map[string]any{"session": "work", "window": b, "text": text}))
		if verr != nil {
			t.Fatalf("queue-prompt over a link: %v", verr.Message)
		}
		return res.(map[string]any)["id"].(string)
	}
	idOne := queue(one, "from link one")
	idTwo := queue(two, "from link two")
	if _, verr := d.verbCancelQueued(two, mustJSON(map[string]any{"session": "work", "id": idOne})); verr == nil || verr.Code != ErrVerbForbidden {
		t.Fatalf("an unnamed link dropping another unnamed link's entry = %v, want forbidden", verr)
	}
	res, verr := d.verbCancelQueued(two, mustJSON(map[string]any{"session": "work", "window": b, "all": true}))
	if verr != nil {
		t.Fatal(verr.Message)
	}
	if ids := res.(map[string]any)["cancelled"].([]string); len(ids) != 1 || ids[0] != idTwo {
		t.Fatalf("an unnamed link's cancel all = %v, want only its own entry", ids)
	}
	if _, verr := d.verbCancelQueued(one, mustJSON(map[string]any{"session": "work", "id": idOne})); verr != nil {
		t.Fatalf("an unnamed link dropping its own entry: %v", verr.Message)
	}

	// A named machine drops what it queued, on any connection.
	named := &connState{clientID: "link-three", viaLink: true, linkPeer: "laptop", done: make(chan struct{})}
	again := &connState{clientID: "link-four", viaLink: true, linkPeer: "laptop", done: make(chan struct{})}
	id := queue(named, "from laptop")
	if _, verr := d.verbCancelQueued(again, mustJSON(map[string]any{"session": "work", "id": id})); verr != nil {
		t.Fatalf("laptop dropping its own entry after reconnecting: %v", verr.Message)
	}
}

// TestQueueRefusesAForwardedPane: a call a pane on another machine forwards
// through its report channel is refused by both writing verbs, rather than
// passing for the person's shell.
func TestQueueRefusesAForwardedPane(t *testing.T) {
	d, _, _, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	byShell := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "from a shell"}))
	hosted := &connState{clientID: "hosted:box", paneOnly: true, done: make(chan struct{})}
	if _, verr := d.verbQueuePrompt(hosted, mustJSON(map[string]any{"session": "work", "window": b, "text": "x"})); verr == nil || verr.Code != ErrVerbForbidden {
		t.Fatalf("queue-prompt from a forwarded pane = %v, want forbidden", verr)
	}
	if _, verr := d.verbCancelQueued(hosted, mustJSON(map[string]any{"session": "work", "id": byShell["id"]})); verr == nil || verr.Code != ErrVerbForbidden {
		t.Fatalf("cancel-queued from a forwarded pane = %v, want forbidden", verr)
	}
	if got := queuedEntries(t, c, b); len(got) != 1 {
		t.Fatalf("list-queued = %v, want the shell's entry still there", got)
	}
}

// TestQueueAgentLeavingMidTypeLeavesNoStamp: an agent that leaves the pane
// while an entry is being typed drops the queue, and the delivery that
// finishes afterwards records no stamp, as for a pane that closes or a
// session that ends while the gate waits.
func TestQueueAgentLeavingMidTypeLeavesNoStamp(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "idle", "", "")
	var once sync.Once
	d.queue.beforeType = func(window string) {
		once.Do(func() {
			if _, _, err := sess.ApplyAgentReport(window, AgentReport{State: AgentStateNone}); err != nil {
				t.Error(err)
			}
		})
	}
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo left-$((20+1))"}))
	eventually(t, "the entry is typed", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "left-21") })
	eventually(t, "the delivery finishes", 5*time.Second, func() bool {
		d.queue.mu.Lock()
		defer d.queue.mu.Unlock()
		pq := d.queue.panes[b]
		return pq == nil || !pq.delivering
	})
	d.queue.mu.Lock()
	_, stamped := d.queue.typed[b]
	d.queue.mu.Unlock()
	if stamped || d.queue.stamped.Load() != 0 {
		t.Fatal("a delivery that finished after the agent left stamped the pane")
	}
	if n := d.queue.count(b); n != 0 {
		t.Fatalf("the queue holds %d after the agent left", n)
	}
}
