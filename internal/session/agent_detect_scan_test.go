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
