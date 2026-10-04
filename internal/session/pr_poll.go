package session

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
)

// Pull request state: what the rail and the listings say about a worktree's
// pull request, kept fresh by a poll of gh.
//
// A worktree session's record carries the pull request of its branch
// (WorktreeInfo.PR) once ship-pr opened it or ship-status found it. While at
// least one such pull request is open and at least one client is attached,
// one goroutine asks gh about each of them every prPollInterval, one call at
// a time, and writes a record only when what gh says changed. A merged or
// closed pull request is final and is not asked about again.
//
// The cost at idle is the point. With no open pull request recorded, or with
// no client attached to show one, there is no goroutine and no timer: the
// loop exits at its next tick and only an attach or a new record starts it
// again. With gh not installed nothing starts at all.

// prPollInterval is how often an open pull request is read again.
const prPollInterval = 60 * time.Second

// prViewGap is the least time between two gh calls, over the whole daemon.
const prViewGap = time.Second

// prPollMax bounds the pull requests one round reads.
const prPollMax = 32

// prPollEvery is the poll interval: TUIOS_PR_POLL_SECONDS when set, for
// tests, else prPollInterval.
func prPollEvery() time.Duration {
	if s := os.Getenv("TUIOS_PR_POLL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return prPollInterval
}

// prPoller is the daemon's pull request poll. The zero value is idle.
type prPoller struct {
	mu      sync.Mutex
	running bool

	// callMu runs one gh call at a time, and last is when the last one
	// started, so the calls keep prViewGap apart.
	callMu sync.Mutex
	last   time.Time
}

// view reads the pull request of branch through gh, one call at a time and
// at most one per prViewGap.
func (p *prPoller) view(ctx context.Context, root, branch string) (ghpr.PR, error) {
	p.callMu.Lock()
	defer p.callMu.Unlock()
	if wait := prViewGap - time.Since(p.last); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ghpr.PR{}, ctx.Err()
		case <-t.C:
		}
	}
	p.last = time.Now()
	pr, err := ghpr.View(ctx, root, branch)
	if err == nil {
		pr.CheckedAt = time.Now().UnixNano()
	}
	return pr, err
}

// prTarget is one open pull request the poll reads.
type prTarget struct {
	sess   *Session
	root   string
	branch string
}

// trackedPRs lists the open pull requests recorded on worktree sessions.
func (d *Daemon) trackedPRs() []prTarget {
	var out []prTarget
	for _, s := range d.manager.AllSessions() {
		wt := s.Worktree()
		if wt == nil || wt.PR == nil || wt.PR.Final() || wt.PR.Branch == "" {
			continue
		}
		out = append(out, prTarget{sess: s, root: canonRoot(wt.Path), branch: wt.PR.Branch})
		if len(out) == prPollMax {
			break
		}
	}
	return out
}

// anyTUIClient reports whether a client is attached to a session: somebody
// who could see a pull request's state.
func (d *Daemon) anyTUIClient() bool {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		attached := cs.isTUIClient && cs.sessionID != ""
		cs.mu.Unlock()
		if attached {
			return true
		}
	}
	return false
}

// prPollWanted reports whether the poll has anything to do: gh is there, a
// client is attached, and an open pull request is recorded.
func (d *Daemon) prPollWanted() bool {
	if _, err := ghpr.Path(); err != nil {
		return false
	}
	return d.anyTUIClient() && len(d.trackedPRs()) > 0
}

// kickPRPoll starts the poll when it has something to do and is not
// running. It is called on an attach and when an open pull request is
// recorded, and costs a scan of the sessions and nothing else.
func (d *Daemon) kickPRPoll() {
	p := &d.prs
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	if !d.prPollWanted() {
		return
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()
	if !d.goTracked(d.runPRPoll) {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}
}

// runPRPoll reads every open pull request once per interval, until none is
// open or no client is attached.
func (d *Daemon) runPRPoll() {
	every := prPollEvery()
	timer := time.NewTimer(every)
	defer timer.Stop()
	stop := func() {
		d.prs.mu.Lock()
		d.prs.running = false
		d.prs.mu.Unlock()
	}
	for {
		select {
		case <-d.ctx.Done():
			stop()
			return
		case <-timer.C:
		}
		if !d.prPollWanted() {
			stop()
			// A record or an attach that came while this loop was deciding
			// to stop saw it running and did not start another.
			d.kickPRPoll()
			return
		}
		for _, t := range d.trackedPRs() {
			if d.ctx.Err() != nil {
				break
			}
			ctx, cancel := context.WithTimeout(d.ctx, shipGhTimeout)
			pr, err := d.prs.view(ctx, t.root, t.branch)
			cancel()
			switch {
			case errors.Is(err, ghpr.ErrNoPR):
				t.sess.setWorktreePR(t.root, nil)
			case err != nil:
				LogBasic("Reading the pull request of %s in %s failed: %v", t.branch, t.root, err)
			default:
				t.sess.setWorktreePR(t.root, &pr)
			}
		}
		timer.Reset(every)
	}
}

// recordPR keeps pr on the session's worktree record when the record is the
// work tree at root, and starts the poll for an open one.
func (d *Daemon) recordPR(sess *Session, root string, pr *ghpr.PR) {
	if sess.setWorktreePR(root, pr) && pr != nil && !pr.Final() {
		d.kickPRPoll()
	}
}

// errPRSame stops a record write that would change nothing.
var errPRSame = errors.New("pull request unchanged")

// setWorktreePR replaces the pull request on the session's worktree record
// when the record is the work tree at root. It reports whether it changed,
// and writes nothing when it did not, so a poll that reads the same state
// costs no state version and no broadcast.
func (s *Session) setWorktreePR(root string, pr *ghpr.PR) bool {
	// The path is resolved outside the state lock: canonRoot reads the
	// file system.
	wt := s.Worktree()
	if wt == nil || canonRoot(wt.Path) != root || wt.PR.Same(pr) {
		return false
	}
	path := wt.Path
	changed := false
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree == nil || st.Worktree.Path != path || st.Worktree.PR.Same(pr) {
			return errPRSame
		}
		if pr != nil {
			cp := *pr
			pr = &cp
		}
		st.Worktree.PR = pr
		changed = true
		return nil
	})
	return changed
}
