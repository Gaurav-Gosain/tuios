package session

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// A prompt gate and an ask wait both listen to every agent-state event of the
// session, and a fan or a fleet of asks puts many of them on one session at
// once. An event about another pane says nothing about the pane a gate
// watches, so a gate that looks at its pane's state and screen on every such
// event pays that look once per gate per event, for nothing. These tests hold
// the cost of a foreign event per gate to a few allocations, and their
// positive halves hold that a gate still sees its own pane: its own report, and
// a screen that shows the prompt taken, found by the poll.
//
// The ways this could fail, written down before the code:
//
//  1. A gate or wait still runs its check on a foreign event: hundreds of
//     allocations per gate per event.
//  2. A gate stops looking after an event about its own pane too, so its own
//     working report no longer ends it.
//  3. The poll stops looking after a foreign event, so a screen that shows the
//     prompt taken is never read while other panes report.
//  4. A gate ends on a foreign event, which would make the budget look met on
//     a bus with no gates left on it. The budget tests check every waiter is
//     still waiting and still subscribed after each window.
//  5. The bus drops events, so fewer reach the gates than were counted. The
//     budget tests check no subscriber dropped one.

// foreignBurst is how many foreign events one measured window publishes. It
// stays under defaultEventQueue, so a waiter's queue holds the whole burst.
const foreignBurst = 200

// gateFixture is one daemon with a session of claude-code panes on the idle
// screen, each waited on by a gate or an ask, and one more pane whose events are
// the foreign ones.
type gateFixture struct {
	d       *Daemon
	sess    *Session
	panes   []string
	ptys    []*PTY
	foreign string
	// asks is set once ask waits run, which also read the panes' output.
	asks bool
}

// newGateFixture builds a session with n waited panes and one foreign pane.
// The foreground detector is off, so nothing but the test reports on a pane.
func newGateFixture(t *testing.T, n int) *gateFixture {
	t.Helper()
	off := false
	d, _ := startTestDaemonWith(t, &DaemonConfig{AgentAutoDetect: &off})
	sess := makeSessionWithWindow(t, d, "gates")
	for range n {
		if _, err := sess.AddDaemonWindow("Waited", nil); err != nil {
			t.Fatalf("AddDaemonWindow: %v", err)
		}
	}
	st := sess.GetState()
	if len(st.Windows) != n+1 {
		t.Fatalf("the session has %d windows, want %d", len(st.Windows), n+1)
	}
	f := &gateFixture{d: d, sess: sess, foreign: st.Windows[0].ID}
	for _, w := range st.Windows {
		pty, err := d.resolvePTYForTarget(sess, w.ID)
		if err != nil {
			t.Fatalf("resolvePTYForTarget: %v", err)
		}
		waitForQuiet(t, pty, 300*time.Millisecond, 10*time.Second)
		if w.ID == f.foreign {
			continue
		}
		paintPane(t, pty, claudeIdleScreen)
		if _, _, err := sess.ApplyAgentReport(w.ID, AgentReport{State: AgentStateIdle, Harness: "claude-code"}); err != nil {
			t.Fatalf("ApplyAgentReport: %v", err)
		}
		f.panes = append(f.panes, w.ID)
		f.ptys = append(f.ptys, pty)
	}
	if subs := f.subs(); len(subs) != 0 {
		t.Fatalf("the session has %d subscribers before any waiter", len(subs))
	}
	return f
}

// gateResult is what one gate returned.
type gateResult struct{ taken, gone, stopped bool }

// startGates starts a gate on every waited pane, looking every poll, with a one
// hour deadline. They stop at cleanup.
func (f *gateFixture) startGates(t *testing.T, poll time.Duration) chan gateResult {
	t.Helper()
	stop := make(chan struct{})
	results := make(chan gateResult, len(f.panes))
	var wg sync.WaitGroup
	for i, id := range f.panes {
		g := f.d.newPromptGate(f.sess, id)
		g.markSubmitted(time.Now())
		pty := f.ptys[i]
		wg.Go(func() {
			taken, gone, stopped := f.d.watchPromptGate(f.sess, pty, g, time.Hour, poll, stop)
			results <- gateResult{taken, gone, stopped}
		})
	}
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})
	return results
}

// settleResult is what one ask wait returned.
type settleResult struct{ settledBy, state string }

// startAsks starts an ask wait on every waited pane with the given settle time
// and a one hour stall and timeout. The caller leaves at cleanup.
func (f *gateFixture) startAsks(t *testing.T, settle time.Duration) chan settleResult {
	t.Helper()
	gone := make(chan struct{})
	results := make(chan settleResult, len(f.panes))
	f.asks = true
	var wg sync.WaitGroup
	for i, id := range f.panes {
		g := f.d.newPromptGate(f.sess, id)
		g.markSubmitted(time.Now())
		pty := f.ptys[i]
		wg.Go(func() {
			by, state := f.d.waitAgentSettled(f.sess, id, pty, g, settle, time.Hour, time.Hour, gone)
			results <- settleResult{by, state}
		})
	}
	t.Cleanup(func() {
		close(gone)
		wg.Wait()
	})
	return results
}

// subs lists the bus's subscriptions to the session.
func (f *gateFixture) subs() []*eventSub {
	h := f.d.events
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*eventSub
	for s := range h.subs {
		if s.filter.sess == f.sess || s.filter.session == f.sess.Name() {
			out = append(out, s)
		}
	}
	return out
}

// publishForeign puts k agent-state events about the foreign pane on the bus.
func (f *gateFixture) publishForeign(k int) {
	for range k {
		f.d.events.publish(streamEvent{Type: EventAgentState, Session: f.sess.Name(), Window: f.foreign, State: AgentStateWorking.Name(), Time: time.Now().UnixNano()})
	}
}

// pollUntil polls cond until it holds, and fails the test after limit.
func pollUntil(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s within %v", what, limit)
		}
		time.Sleep(time.Millisecond)
	}
}

// drained reports whether every subscriber of the session has an empty queue.
func (f *gateFixture) drained() bool {
	for _, s := range f.subs() {
		if len(s.ch) > 0 {
			return false
		}
	}
	return true
}

// ready waits until want subscriptions are on the session and each waiter has
// taken one foreign event, which means each has made its first look and sits in
// its select.
//
// A drained queue says a waiter took the event, not that it is done with it.
// An ask wait that still looks at its pane for that event after the test paints
// a working screen would end there instead of on the path the test is about.
// So ask waits also get one output event for their own pane: output only
// restarts the settle clock, and a waiter that took it has finished with the
// foreign event before it.
func (f *gateFixture) ready(t *testing.T, want int) {
	t.Helper()
	pollUntil(t, 10*time.Second, "the waiters did not subscribe", func() bool { return len(f.subs()) == want })
	f.publishForeign(1)
	pollUntil(t, 10*time.Second, "the waiters did not take the first event", f.drained)
	if !f.asks {
		return
	}
	for i, id := range f.panes {
		f.d.events.publish(streamEvent{Type: EventOutput, Session: f.sess.Name(), Window: id, PTYID: f.ptys[i].ID, Time: time.Now().UnixNano()})
	}
	pollUntil(t, 10*time.Second, "the ask waits did not take their output event", f.drained)
}

// stillWaiting fails the test when a waiter has returned, left the bus, or
// lost an event.
func (f *gateFixture) stillWaiting(t *testing.T, returned, want int) {
	t.Helper()
	if returned != 0 {
		t.Fatalf("%d waiters returned on foreign events", returned)
	}
	subs := f.subs()
	if len(subs) != want {
		t.Fatalf("the session has %d subscribers, want %d", len(subs), want)
	}
	for _, s := range subs {
		if n := s.dropped.Load(); n != 0 {
			t.Fatalf("a subscriber dropped %d events", n)
		}
	}
}

// perForeignEvent measures the allocations one foreign event costs each of
// waiters, the least of three runs. A run publishes a burst and waits for every
// queue to drain, then counts a quiet window of the same length with nothing
// published, and takes the difference: the quiet window holds what the daemon
// and the waiters' own polls allocate anyway. The quiet window is the one timed
// span here, and it is a length to measure over, not a wait for a condition.
func (f *gateFixture) perForeignEvent(t *testing.T, waiters, subs int, returned func() int) float64 {
	t.Helper()
	least := -1.0
	for range 3 {
		var m0, m1, m2, m3 runtime.MemStats
		runtime.ReadMemStats(&m0)
		start := time.Now()
		f.publishForeign(foreignBurst)
		pollUntil(t, time.Minute, "the waiters did not drain a burst", f.drained)
		span := time.Since(start)
		runtime.ReadMemStats(&m1)
		f.stillWaiting(t, returned(), subs)

		runtime.ReadMemStats(&m2)
		time.Sleep(span)
		runtime.ReadMemStats(&m3)
		f.stillWaiting(t, returned(), subs)

		burst := float64(m1.Mallocs - m0.Mallocs)
		quiet := float64(m3.Mallocs - m2.Mallocs)
		per := (burst - quiet) / float64(foreignBurst*waiters)
		t.Logf("burst %.0f, quiet %.0f over %v: %.1f allocations per foreign event per waiter", burst, quiet, span, per)
		if least < 0 || per < least {
			least = per
		}
	}
	return least
}

// TestPromptGateSkipsForeignEvents holds what an event about another pane costs
// a prompt gate. Twenty gates wait on panes that show the prompt untaken, with
// an hour's poll and deadline, so only events wake them.
func TestPromptGateSkipsForeignEvents(t *testing.T) {
	const gates = 20
	const budget = 2
	f := newGateFixture(t, gates)
	results := f.startGates(t, time.Hour)
	f.ready(t, gates)
	per := f.perForeignEvent(t, gates, gates, func() int { return len(results) })
	if per >= budget {
		t.Fatalf("a foreign event costs each gate %.1f allocations, the budget is under %d", per, budget)
	}
}

// TestAskWaitSkipsForeignEvents holds the same cost for the ask wait. Its poll
// is fixed at promptGatePoll, so its own looks fall in both windows and the
// difference leaves the foreign events.
func TestAskWaitSkipsForeignEvents(t *testing.T) {
	const asks = 20
	const budget = 20
	f := newGateFixture(t, asks)
	results := f.startAsks(t, time.Hour)
	f.ready(t, 2*asks)
	per := f.perForeignEvent(t, asks, 2*asks, func() int { return len(results) })
	if per >= budget {
		t.Fatalf("a foreign event costs each ask wait %.1f allocations, the budget is under %d", per, budget)
	}
}

// TestPromptGateStillSeesItsPane is the positive half of the gate's budget: a
// gate that skips foreign events still ends on its own pane's report, and its
// poll still reads a screen that shows the prompt taken while other panes
// report. Each fixture leaves one way for the gate to end.
func TestPromptGateStillSeesItsPane(t *testing.T) {
	t.Run("own report", func(t *testing.T) {
		f := newGateFixture(t, 1)
		// An hour's poll and deadline: only the report's event can end it.
		results := f.startGates(t, time.Hour)
		f.ready(t, 1)
		if _, _, err := f.sess.ApplyAgentReport(f.panes[0], AgentReport{State: AgentStateWorking, Harness: "claude-code"}); err != nil {
			t.Fatalf("ApplyAgentReport: %v", err)
		}
		select {
		case r := <-results:
			if !r.taken || r.gone || r.stopped {
				t.Fatalf("the gate ended %+v on its pane's working report, want taken", r)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the gate did not end on its pane's working report within 10s")
		}
	})
	t.Run("screen under foreign events", func(t *testing.T) {
		f := newGateFixture(t, 1)
		// The gate's own poll and an hour's deadline: only the poll can
		// read the screen once foreign events have woken the gate.
		results := f.startGates(t, promptGatePoll)
		f.ready(t, 1)
		done := make(chan struct{})
		streamed := make(chan struct{})
		go func() {
			defer close(streamed)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					f.publishForeign(1)
				}
			}
		}()
		defer func() { close(done); <-streamed }()
		paintPane(t, f.ptys[0], claudeWorkingScreen)
		select {
		case r := <-results:
			if !r.taken || r.gone || r.stopped {
				t.Fatalf("the gate ended %+v on a working screen, want taken", r)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the gate did not take a working screen within 10s while other panes reported")
		}
	})
}

// TestAskWaitStillSeesItsPane is the positive half of the ask wait's budget:
// its poll still takes the question from the screen, and its pane's own rest
// report still ends it. Each ask leaves one way to end.
func TestAskWaitStillSeesItsPane(t *testing.T) {
	t.Run("screen then quiet", func(t *testing.T) {
		f := newGateFixture(t, 1)
		// The poll takes the question from the screen and starts the settle
		// clock; with no output after, the clock ends the wait.
		results := f.startAsks(t, 300*time.Millisecond)
		f.ready(t, 2)
		paintPane(t, f.ptys[0], claudeWorkingScreen)
		select {
		case r := <-results:
			if r.settledBy != "idle" {
				t.Fatalf("the ask settled by %q, want idle", r.settledBy)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the ask did not settle within 10s after its pane showed the question taken")
		}
	})
	t.Run("own rest report", func(t *testing.T) {
		f := newGateFixture(t, 1)
		// An hour's settle, stall and timeout: only the report can end it.
		results := f.startAsks(t, time.Hour)
		f.ready(t, 2)
		if _, _, err := f.sess.ApplyAgentReport(f.panes[0], AgentReport{State: AgentStateDone, Harness: "claude-code"}); err != nil {
			t.Fatalf("ApplyAgentReport: %v", err)
		}
		select {
		case r := <-results:
			if r.settledBy != "agent-state" {
				t.Fatalf("the ask settled by %q, want agent-state", r.settledBy)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the ask did not end on its pane's done report within 10s")
		}
	})
}
