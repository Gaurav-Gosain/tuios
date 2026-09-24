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

// windowQueued is the window's agent_queued as the synced state carries it.
func windowQueued(sess *Session, window string) int {
	w, _ := findWindowState(sess.GetState(), window)
	return w.AgentQueued
}

// TestQueueTypesWhenTheAgentRests: a message queued for a working agent waits,
// is not typed while the agent is working or on a prompt, and is typed once
// the agent comes to rest. The pane's agent_queued and get-agent-state's
// queued follow the queue.
func TestQueueTypesWhenTheAgentRests(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")

	res := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo queued-one"}))
	if res["type"] != "prompt_queued" || res["position"] != float64(1) || res["queued"] != float64(1) || res["delivering"] != false {
		t.Fatalf("queue-prompt = %v, want the first entry, not delivering to a working agent", res)
	}
	id, _ := res["id"].(string)
	if id == "" {
		t.Fatal("queue-prompt returned no id")
	}
	eventually(t, "agent_queued reaches the window state", 2*time.Second, func() bool { return windowQueued(sess, b) == 1 })
	st := result(t, callP(c, t, "get-agent-state", map[string]any{"session": "work", "window": b}))
	if st["queued"] != float64(1) {
		t.Errorf("get-agent-state queued = %v, want 1", st["queued"])
	}
	entries := queuedEntries(t, c, b)
	if len(entries) != 1 || entries[0]["id"] != id || entries[0]["by"] != queueByShell || entries[0]["state"] != queueWaiting || entries[0]["preview"] != "echo queued-one" {
		t.Fatalf("list-queued = %v, want the one waiting entry, by shell", entries)
	}

	time.Sleep(300 * time.Millisecond)
	if paneShows(t, d, sess, b, "queued-one") {
		t.Fatal("the message was typed into a working agent")
	}
	setAgentState(t, c, "work", b, "needs_input", "question", "pick one")
	time.Sleep(300 * time.Millisecond)
	if paneShows(t, d, sess, b, "queued-one") {
		t.Fatal("the message was typed into an agent waiting on a prompt")
	}

	setAgentState(t, c, "work", b, "idle", "", "")
	eventually(t, "the message is typed at rest", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "queued-one") })
	eventually(t, "the entry leaves the queue", 5*time.Second, func() bool { return len(queuedEntries(t, c, b)) == 0 && windowQueued(sess, b) == 0 })
}

// TestQueueTypesOnePerRest: two entries queued for an agent at rest are typed
// one at a time. The second waits for a rest the agent reaches after the first
// was typed, rather than following it into the same moment.
func TestQueueTypesOnePerRest(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "idle", "", "")
	first := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo first-entry"}))
	if first["delivering"] != true {
		t.Errorf("an entry queued for an agent at rest reports delivering %v, want true", first["delivering"])
	}
	second := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo second-entry"}))
	if second["position"] != float64(2) || second["delivering"] != false {
		t.Errorf("second entry = %v, want position 2, not delivering", second)
	}
	eventually(t, "the first entry is typed", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "first-entry") })
	eventually(t, "the first entry leaves the queue", 5*time.Second, func() bool { return len(queuedEntries(t, c, b)) == 1 })
	time.Sleep(400 * time.Millisecond)
	if paneShows(t, d, sess, b, "second-entry") {
		t.Fatal("the second entry was typed without the agent coming to rest again")
	}
	// The agent works on the first and comes back to rest.
	setAgentState(t, c, "work", b, "working", "", "")
	setAgentState(t, c, "work", b, "idle", "", "")
	eventually(t, "the second entry is typed at the next rest", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "second-entry") })
	eventually(t, "the queue empties", 5*time.Second, func() bool { return len(queuedEntries(t, c, b)) == 0 })
}

// TestQueueWaitsOnUnknownOnlyWhereIdleCanBeShown: unknown is rest for a
// harness whose rules can never show idle, and not for one whose can.
func TestQueueWaitsOnUnknownOnlyWhereIdleCanBeShown(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	if reg := d.agentMatcher.registry; reg == nil || !reg.CanProveIdle("claude-code") {
		t.Skip("the registry has no claude-code rules that show idle")
	}
	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateUnknown, Harness: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo unknown-entry"}))
	time.Sleep(400 * time.Millisecond)
	if paneShows(t, d, sess, b, "unknown-entry") {
		t.Fatal("the message was typed into a claude-code pane on unknown")
	}
	if got := queuedEntries(t, c, b); len(got) != 1 || got[0]["state"] != queueWaiting {
		t.Fatalf("list-queued = %v, want the entry still waiting", got)
	}
}

// TestQueueStalledEntryIsNeverTypedAgain: a pane that shows no sign of taking
// the message leaves the entry stalled, opens an Inbox question, and holds
// the entries behind it. The pane working settles it.
func TestQueueStalledEntryIsNeverTypedAgain(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	d.promptStallOverride = 600 * time.Millisecond
	silenceWindow(t, d, sess, b)
	setAgentState(t, c, "work", b, "idle", "", "")
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "first"}))
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "second"}))
	eventually(t, "the entry stalls", 5*time.Second, func() bool {
		got := queuedEntries(t, c, b)
		return len(got) == 2 && got[0]["state"] == queueStalled
	})
	items, _ := listAttention(t, c, "")
	found := false
	for _, it := range items {
		if it["window"] == b && it["kind"] == AttentionQuestion && strings.Contains(it["summary"].(string), "queued message was typed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Inbox question for the stalled entry: %v", items)
	}
	// Another rest types nothing: the stalled entry holds the queue.
	setAgentState(t, c, "work", b, "done", "", "")
	time.Sleep(400 * time.Millisecond)
	if got := queuedEntries(t, c, b); len(got) != 2 || got[0]["state"] != queueStalled || got[1]["state"] != queueWaiting {
		t.Fatalf("list-queued = %v, want the stalled entry still holding the second", got)
	}
	// The pane working settles the stalled entry, and only that one goes.
	setAgentState(t, c, "work", b, "working", "", "")
	eventually(t, "the stalled entry is dropped", 2*time.Second, func() bool {
		got := queuedEntries(t, c, b)
		return len(got) == 1 && got[0]["preview"] == "second"
	})
}

// TestQueueFull: a queue at [agents.queue] max refuses the next entry.
func TestQueueFull(t *testing.T) {
	d, _, _, c, _, b := queueFixture(t)
	d.SetQueueMax(2)
	setAgentState(t, c, "work", b, "working", "", "")
	for i := range 2 {
		result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "entry " + string(rune('a'+i))}))
	}
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "one too many"}), ErrVerbQueueFull, "a third entry with max 2")
	if got := queuedEntries(t, c, b); len(got) != 2 {
		t.Fatalf("list-queued = %v, want the two entries", got)
	}
}

// TestQueueMaxFollowsTheConfig: [agents.queue] max is read at start by every
// starter and again when the file changes.
func TestQueueMaxFollowsTheConfig(t *testing.T) {
	uc := &config.UserConfig{Agents: config.AgentsConfig{Queue: config.QueueConfig{Max: 3}}}
	if cfg := DaemonConfigFromUser(uc); cfg.QueueMax != 3 {
		t.Fatalf("DaemonConfigFromUser carried QueueMax %d, want 3", cfg.QueueMax)
	}
	d, _ := startTestDaemon(t)
	if got := d.queue.maxEntries(); got != config.DefaultQueueMax {
		t.Fatalf("a daemon with no table holds %d per pane, want %d", got, config.DefaultQueueMax)
	}
	d.onConfigReload(uc, nil)
	if got := d.queue.maxEntries(); got != 3 {
		t.Errorf("after the reload a queue holds %d, want 3", got)
	}
	d.onConfigReload(&config.UserConfig{}, nil)
	if got := d.queue.maxEntries(); got != config.DefaultQueueMax {
		t.Errorf("after the table was removed a queue holds %d, want %d", got, config.DefaultQueueMax)
	}
}

// TestQueueRefusesWhatHasNoAgent: human has no keyboard, and a pane with no
// agent has nobody to take a prompt.
func TestQueueRefusesWhatHasNoAgent(t *testing.T) {
	_, _, _, c, a, _ := queueFixture(t)
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": AgentInboxHuman, "text": "hi"}), ErrVerbNoKeyboard, "queueing for human")
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": a, "text": "hi"}), ErrVerbInvalidParams, "queueing for a plain shell")
}

// TestQueueDropsWithItsPaneAndAgent: a queue goes when its pane closes, when
// its agent leaves the pane, and an entry a pane queued goes when that pane
// closes.
func TestQueueDropsWithItsPaneAndAgent(t *testing.T) {
	d, sp, sess, c, a, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "for the agent"}))
	setAgentState(t, c, "work", b, "none", "", "")
	eventually(t, "the queue goes with the agent", 2*time.Second, func() bool { return d.queue.count(b) == 0 && windowQueued(sess, b) == 0 })

	// An entry queued by pane a for pane b goes when a closes.
	setAgentState(t, c, "work", b, "working", "", "")
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	pane := dialVerb(t, sp)
	res := result(t, callP(pane, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "from pane a"}))
	d.approvalPeer = nil
	if got := queuedEntries(t, c, b); len(got) != 1 || got[0]["by"] != a || got[0]["from"] != a {
		t.Fatalf("list-queued = %v, want the entry by and from pane a", got)
	}
	_ = res
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "from the shell"}))
	result(t, callP(c, t, "close-window", map[string]any{"session": "work", "window": a}))
	eventually(t, "pane a's entry goes with pane a", 2*time.Second, func() bool {
		got := queuedEntries(t, c, b)
		return len(got) == 1 && got[0]["by"] == queueByShell
	})
	eventually(t, "agent_queued follows", 2*time.Second, func() bool { return windowQueued(sess, b) == 1 })

	// The pane's own queue goes when it closes.
	result(t, callP(c, t, "close-window", map[string]any{"session": "work", "window": b}))
	eventually(t, "the queue goes with its pane", 2*time.Second, func() bool { return d.queue.count(b) == 0 && d.queue.total.Load() == 0 })
}

// TestQueueDropsWithItsSession: a session that ends takes its queues.
func TestQueueDropsWithItsSession(t *testing.T) {
	d, _, _, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "for the agent"}))
	result(t, callP(c, t, "kill-session", map[string]any{"session": "work"}))
	eventually(t, "the queue goes with the session", 2*time.Second, func() bool { return d.queue.total.Load() == 0 })
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

// TestQueuePreview: the first line, control characters left out, cut at 80.
func TestQueuePreview(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"make the backoff configurable", "make the backoff configurable"},
		{"  first line\nsecond line", "first line..."},
		{"bell\x07 and \x1b[31mred", "bell and [31mred"},
		{strings.Repeat("x", 100), strings.Repeat("x", 80) + "..."},
	} {
		if got := queuePreview(tc.in); got != tc.want {
			t.Errorf("queuePreview(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestQueueQuietRestOnAPaneThatCannotShowWorking: a pane whose harness shows
// neither idle nor working rests on unknown and never changes state after it
// takes a prompt. The entry after the first is typed once the pane has printed
// something since the first and then been silent for the quiet window, and
// not before.
func TestQueueQuietRestOnAPaneThatCannotShowWorking(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	d.queue.quiet = time.Second
	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateUnknown}); err != nil {
		t.Fatal(err)
	}
	w, _ := findWindowState(sess.GetState(), b)
	if !d.agentReady(w, fanReadyStates) || !d.outputShowsTaking(sess, w) {
		t.Fatal("a pane with no harness on unknown should be at rest, with output as its evidence")
	}
	stateAt := w.AgentStateAt
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo quiet-$((40+1))"}))
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "echo quiet-$((40+2))"}))
	eventually(t, "the first entry is typed", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "quiet-41") })
	time.Sleep(300 * time.Millisecond)
	if paneShows(t, d, sess, b, "quiet-$((40+2))") {
		t.Fatal("the second entry followed the first before the pane was quiet")
	}
	eventually(t, "the second entry is typed after the quiet", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "quiet-42") })
	if w, _ := findWindowState(sess.GetState(), b); w.AgentStateAt != stateAt {
		t.Fatalf("the pane changed state (%s), so this did not test the quiet rest", w.AgentState.Name())
	}
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

// TestQueueCancelOfAStalledEntryWaitsForTheNextRest: dropping a stalled entry
// does not type the next one into the same rest, since the stalled text may
// still sit in the input box. The next rest types it.
func TestQueueCancelOfAStalledEntryWaitsForTheNextRest(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	d.promptStallOverride = 600 * time.Millisecond
	d.queue.quiet = 300 * time.Millisecond
	silenceWindow(t, d, sess, b)
	setAgentState(t, c, "work", b, "idle", "", "")
	first := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "first"}))
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "text": "second"}))
	eventually(t, "the entry stalls", 5*time.Second, func() bool {
		got := queuedEntries(t, c, b)
		return len(got) == 2 && got[0]["state"] == queueStalled
	})
	result(t, callP(c, t, "cancel-queued", map[string]any{"session": "work", "id": first["id"]}))
	time.Sleep(600 * time.Millisecond)
	if got := queuedEntries(t, c, b); len(got) != 1 || got[0]["preview"] != "second" || got[0]["state"] != queueWaiting {
		t.Fatalf("list-queued = %v, want the second entry still waiting", got)
	}
	setAgentState(t, c, "work", b, "working", "", "")
	setAgentState(t, c, "work", b, "idle", "", "")
	eventually(t, "the second entry is typed at the next rest", 5*time.Second, func() bool {
		got := queuedEntries(t, c, b)
		return len(got) == 0 || got[0]["state"] != queueWaiting
	})
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
