package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
)

const prPollInterval = 60 * time.Second

const prViewGap = time.Second

const prPollMax = 32

type prPoller struct {
	mu      sync.Mutex
	running bool

	callMu sync.Mutex
	last   time.Time
}

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

type prTarget struct {
	sess   *Session
	root   string
	branch string
}

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

func (d *Daemon) prPollWanted() bool {
	if _, err := ghpr.Path(); err != nil {
		return false
	}
	return d.anyTUIClient() && len(d.trackedPRs()) > 0
}

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

func (d *Daemon) runPRPoll() {
	every := d.prPollEvery
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
				d.notePR(t.sess, t.root, nil)
			case err != nil:
				LogBasic("Reading the pull request of %s in %s failed: %v", t.branch, t.root, err)
			default:
				d.notePR(t.sess, t.root, &pr)
			}
		}
		timer.Reset(every)
	}
}

func (d *Daemon) recordPR(sess *Session, root string, pr *ghpr.PR) {
	if d.notePR(sess, root, pr) && pr != nil && !pr.Final() {
		d.kickPRPoll()
	}
}

var errPRSame = errors.New("pull request unchanged")

func (s *Session) setWorktreePR(root string, pr *ghpr.PR) (changed bool, was string) {
	wt := s.Worktree()
	if wt == nil || canonRoot(wt.Path) != root || wt.PR.Same(pr) {
		return false, ""
	}
	path := wt.Path
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree == nil || st.Worktree.Path != path || st.Worktree.PR.Same(pr) {
			return errPRSame
		}
		if old := st.Worktree.PR; old != nil {
			was = old.State
		}
		if pr != nil {
			cp := *pr
			pr = &cp
		}
		st.Worktree.PR = pr
		changed = true
		return nil
	})
	return changed, was
}
