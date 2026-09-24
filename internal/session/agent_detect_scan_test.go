package session

import (
	"sync/atomic"
	"testing"
	"time"
)

// shellAt is a pane sitting at its own shell prompt.
func shellAt(pid int) fakeProc {
	return fakeProc{foregroundInfo{comm: "zsh", argv: []string{"zsh"}, pid: pid, shellPID: pid}, true}
}

// agentIn is a pane whose shell runs an agent in the foreground.
func agentIn(shellPID int) fakeProc {
	return fakeProc{foregroundInfo{comm: "claude", argv: []string{"claude"}, pid: shellPID + 1, shellPID: shellPID}, true}
}

// TestDetectionReadsWithNoSessionLock is the regression test for the poll
// holding the session's write lock across its foreground reads. Those reads are
// three sysctls a pane on darwin, so every writer in the session waited behind
// them on every tick. The resolver here checks, while it runs, that a writer
// could take the lock.
func TestDetectionReadsWithNoSessionLock(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	matcher := newAgentMatcher(nil)

	var reads, blocked atomic.Int32
	resolve := func(p string) (foregroundInfo, bool) {
		reads.Add(1)
		if sess.stateMu.TryLock() {
			sess.stateMu.Unlock()
		} else {
			blocked.Add(1)
		}
		return fakeResolver(map[string]fakeProc{ptyID: agentIn(100)})(p)
	}
	if n := sess.applyAgentDetection(resolve, matcher.identifyDetail); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	if reads.Load() == 0 {
		t.Fatal("the resolver was never called")
	}
	if b := blocked.Load(); b != 0 {
		t.Fatalf("%d of %d foreground reads ran with the session lock held", b, reads.Load())
	}
}

// TestIdleDetectionTakesNoWriteLock checks that a tick which changes nothing
// never asks for the write lock. A reader holds the lock for the whole scan:
// any attempt to take the write lock would wait for it, and the scan would not
// finish.
func TestIdleDetectionTakesNoWriteLock(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	matcher := newAgentMatcher(nil)
	resolve := fakeResolver(map[string]fakeProc{ptyID: shellAt(100)})

	// The first scan records the shell pid, which is a change. The second has
	// nothing to say.
	sess.applyAgentDetection(resolve, matcher.identifyDetail)
	version := sess.GetState().Version

	sess.stateMu.RLock()
	done := make(chan struct{})
	go func() {
		sess.applyAgentDetection(resolve, matcher.identifyDetail)
		close(done)
	}()
	select {
	case <-done:
		sess.stateMu.RUnlock()
	case <-time.After(2 * time.Second):
		sess.stateMu.RUnlock()
		<-done
		t.Fatal("an idle detection tick waited for the write lock")
	}
	if got := sess.GetState().Version; got != version {
		t.Fatalf("an idle tick moved the version from %d to %d", version, got)
	}
}

// TestDetectionSkipsAWindowRepointedDuringTheRead checks the re-check the
// apply step makes. The reads run with no lock, so the window can change under
// them; a reading taken for a PTY the window no longer has must not be applied
// to it.
func TestDetectionSkipsAWindowRepointedDuringTheRead(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	matcher := newAgentMatcher(nil)

	resolve := func(p string) (foregroundInfo, bool) {
		// The window moves to another PTY while its old one is being read.
		_ = sess.mutateState(func(st *SessionState) error {
			for i := range st.Windows {
				if st.Windows[i].ID == id {
					st.Windows[i].PTYID = "elsewhere"
				}
			}
			return nil
		})
		return fakeResolver(map[string]fakeProc{ptyID: agentIn(100)})(p)
	}
	if n := sess.applyAgentDetection(resolve, matcher.identifyDetail); n != 0 {
		t.Fatalf("a reading of the old PTY changed %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("a reading of the old PTY set %q on the window, want none", got)
	}
}

// detectTicker drives scanAgentDetection tick by tick with the daemon's
// backoff, against one fake PTY per window, and counts the reads per PTY.
type detectTicker struct {
	sess   *Session
	ptys   map[string]*PTY
	reads  map[string]int
	table  map[string]fakeProc
	now    int64
	quiet  int32
	ident  func(foregroundInfo) (detection, bool)
	period time.Duration
}

func newDetectTicker(sess *Session) *detectTicker {
	return &detectTicker{
		sess:   sess,
		ptys:   map[string]*PTY{},
		reads:  map[string]int{},
		table:  map[string]fakeProc{},
		now:    time.Now().UnixNano(),
		quiet:  detectQuietTicks(defaultAgentDetectInterval),
		ident:  newAgentMatcher(nil).identifyDetail,
		period: defaultAgentDetectInterval,
	}
}

func (d *detectTicker) pty(ptyID string) *PTY {
	p := d.ptys[ptyID]
	if p == nil {
		p = &PTY{}
		d.ptys[ptyID] = p
	}
	return p
}

// output records output on a pane at the current time.
func (d *detectTicker) output(ptyID string) {
	d.pty(ptyID).lastOutput.Store(d.now + 1)
}

func (d *detectTicker) tick() {
	d.now += int64(d.period)
	now := d.now
	resolve := func(p string) (foregroundInfo, bool) {
		d.reads[p]++
		return fakeResolver(d.table)(p)
	}
	due := func(p string) bool { return d.pty(p).detectScanDue(now, d.quiet) }
	d.sess.scanAgentDetection(resolve, d.ident, due)
}

// TestDetectionBacksOffAQuietPane checks the poll reads a new pane at once, a
// silent pane only every few ticks after that, a pane on the next tick once it
// prints, and a pane that holds an agent on every tick.
func TestDetectionBacksOffAQuietPane(t *testing.T) {
	sess, quietID := bareSessionWithWindow(t)
	agentWin, err := sess.AddDaemonWindow("Agent", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	quiet := ptyIDOfWindow(t, sess, quietID)
	agent := ptyIDOfWindow(t, sess, agentWin.ID)
	if err := sess.SetDaemonWindowAgentState(agentWin.ID, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	d := newDetectTicker(sess)
	d.table[quiet] = shellAt(100)
	d.table[agent] = agentIn(200)
	if d.quiet != 5 {
		t.Fatalf("quiet ticks at the default interval = %d, want 5", d.quiet)
	}

	// A pane never read before is read on the first tick, silent or not: a
	// pane opened on a silent program has no output to flag it.
	d.tick()
	if got := d.reads[quiet]; got != 1 {
		t.Fatalf("a new quiet pane was read %d times on its first tick, want 1", got)
	}

	const ticks = 20
	for range ticks - 1 {
		d.tick()
	}
	if got := d.reads[agent]; got != ticks {
		t.Errorf("the agent pane was read %d times in %d ticks, want every tick", got, ticks)
	}
	if got := d.reads[quiet]; got != ticks/int(d.quiet) {
		t.Errorf("the quiet pane was read %d times in %d ticks, want %d", got, ticks, ticks/int(d.quiet))
	}

	// Output makes the next tick read it, however recently it was skipped.
	before := d.reads[quiet]
	d.tick()
	d.output(quiet)
	d.tick()
	if got := d.reads[quiet] - before; got < 1 {
		t.Errorf("the quiet pane was not read on the tick after it printed")
	}
}

// TestDetectionFindsAnAgentStartedSilently pins the bound the backoff keeps.
// An agent started in a quiet pane with no output at all, which is the one
// case output cannot flag, is still found within agentDetectQuietBound: five
// ticks at the default two-second interval, so ten seconds. With output, which
// is the ordinary case since typing the command echoes it, it is found on the
// next tick as before.
func TestDetectionFindsAnAgentStartedSilently(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	d := newDetectTicker(sess)
	d.table[ptyID] = shellAt(100)

	// Settle into the backoff: read, then quiet.
	for range 2 * d.quiet {
		d.tick()
	}
	// Start the agent right after a read, which is the worst case.
	for d.pty(ptyID).detectSkips.Load() != 0 {
		d.tick()
	}
	d.table[ptyID] = agentIn(100)
	ticks := 0
	for agentStateOf(t, sess, id) != AgentStateWorking {
		ticks++
		if ticks > int(d.quiet) {
			t.Fatalf("a silently started agent was not found in %d ticks", ticks-1)
		}
		d.tick()
	}
	if bound := time.Duration(ticks) * d.period; bound > agentDetectQuietBound {
		t.Fatalf("a silently started agent took %v to find, bound is %v", bound, agentDetectQuietBound)
	}

	// With output it is the next tick.
	sess2, id2 := bareSessionWithWindow(t)
	ptyID2 := ptyIDOfWindow(t, sess2, id2)
	d2 := newDetectTicker(sess2)
	d2.table[ptyID2] = shellAt(100)
	for range 2 * d2.quiet {
		d2.tick()
	}
	for d2.pty(ptyID2).detectSkips.Load() != 0 {
		d2.tick()
	}
	d2.table[ptyID2] = agentIn(100)
	d2.output(ptyID2)
	d2.tick()
	if got := agentStateOf(t, sess2, id2); got != AgentStateWorking {
		t.Fatalf("an agent started with output was not found on the next tick: %q", got)
	}
}

// TestDetectQuietTicks pins the tick count for the bound at a few intervals.
func TestDetectQuietTicks(t *testing.T) {
	for _, tc := range []struct {
		interval time.Duration
		want     int32
	}{
		{2 * time.Second, 5},
		{time.Second, 10},
		{3 * time.Second, 3},
		{10 * time.Second, 1},
		{30 * time.Second, 1},
		{0, 1},
	} {
		if got := detectQuietTicks(tc.interval); got != tc.want {
			t.Errorf("detectQuietTicks(%v) = %d, want %d", tc.interval, got, tc.want)
		}
	}
}
