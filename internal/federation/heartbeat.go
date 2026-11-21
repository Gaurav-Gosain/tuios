package federation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const (
	// DefaultHeartbeat is how long a link may carry no frame before the
	// heartbeat pings it (Options.Heartbeat). It is also how often a link
	// that waits to dial again checks for a wake from sleep.
	DefaultHeartbeat = 15 * time.Second
	// DefaultHeartbeatTimeout is how long one ping waits for its reply
	// (Options.HeartbeatTimeout).
	DefaultHeartbeatTimeout = 5 * time.Second
	heartbeatRetry          = time.Second
	heartbeatMisses         = 2
	sleepJump               = 5 * time.Second
	loadMaxAgeBeats         = 4
)

type sleepWatch struct{ last time.Time }

func newSleepWatch() sleepWatch { return sleepWatch{last: time.Now()} }

func (w *sleepWatch) slept() bool {
	now := time.Now()
	mono := now.Sub(w.last)
	wall := now.Round(0).Sub(w.last.Round(0))
	w.last = now
	return wall-mono > sleepJump
}

func (m *mux) readSince(start int64) bool {
	return m.blocked.Load() > 0 || m.lastRead.Load() > start
}

func (m *mux) quietCheckIn(interval time.Duration) time.Duration {
	if m.blocked.Load() > 0 {
		return interval
	}
	last := m.lastRead.Load()
	if last == 0 {
		return 0
	}
	left := time.Until(time.Unix(0, last).Add(interval))
	if left <= 0 {
		return 0
	}
	return max(left, time.Second)
}

func (l *link) heartbeat(ctx context.Context, m *mux, closed <-chan struct{}, tearDown func()) {
	var p pinger
	defer p.close()
	clock := newSleepWatch()
	t := time.NewTimer(l.opts.Heartbeat)
	defer t.Stop()
	misses := 0
	for {
		asked := false
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case <-m.Done():
			return
		case <-t.C:
		case <-l.probe:
			asked = true
			t.Stop()
		}
		woke := clock.slept()
		if !asked && !woke && misses == 0 && !l.loadStale() {
			if next := m.quietCheckIn(l.opts.Heartbeat); next > 0 {
				t.Reset(next)
				continue
			}
		}
		start := time.Now().UnixNano()
		ok, load, rtt, held := p.ping(ctx, m, l.opts.HeartbeatTimeout)
		l.noteHello(load, rtt)
		if ok {
			l.noteMailHeld(held)
		}
		if ok || m.readSince(start) {
			misses = 0
			t.Reset(l.opts.Heartbeat)
			continue
		}
		misses++
		if !woke && !asked && misses < heartbeatMisses {
			t.Reset(heartbeatRetry)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case <-m.Done():
			return
		default:
		}
		reason := "The host stopped answering. tuios is connecting again."
		if woke {
			reason = "The computer woke from sleep and the host did not answer. tuios is connecting again."
		}
		l.mu.Lock()
		l.lossNote = reason
		l.fresh = true
		l.mu.Unlock()
		l.logf("host %s: %d pings in a row got no answer, so the link is dialed again (after a sleep: %v, asked for: %v).", l.host.Name, misses, woke, asked)
		tearDown()
		return
	}
}

type pinger struct {
	mu sync.Mutex
	c  *caller
}

func (p *pinger) ping(ctx context.Context, m *mux, timeout time.Duration) (bool, *Load, time.Duration, MailHeld) {
	type answer struct {
		ok   bool
		load *Load
		rtt  time.Duration
		held MailHeld
	}
	done := make(chan answer, 1)
	go func() {
		if !p.mu.TryLock() {
			done <- answer{}
			return
		}
		defer p.mu.Unlock()
		if p.c == nil {
			s, err := m.Open()
			if err != nil {
				done <- answer{}
				return
			}
			p.c = newCaller(s)
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		at := time.Now()
		raw, err := p.c.call(callCtx, "hello", nil)
		var remote *RemoteError
		if err == nil || errors.As(err, &remote) {
			a := answer{ok: true, rtt: time.Since(at)}
			var shake struct {
				Load *Load    `json:"load"`
				Held MailHeld `json:"mail_waiting"`
			}
			if err == nil && jsonx.Unmarshal(raw, &shake) == nil {
				a.load = shake.Load
				a.held = shake.Held.bounded()
			}
			done <- a
			return
		}
		_ = p.c.rw.Close()
		p.c = nil
		done <- answer{}
	}()
	t := time.NewTimer(timeout + time.Second)
	defer t.Stop()
	select {
	case a := <-done:
		return a.ok, a.load, a.rtt, a.held
	case <-t.C:
		return false, nil, 0, MailHeld{}
	}
}

func (p *pinger) close() {
	if !p.mu.TryLock() {
		return
	}
	defer p.mu.Unlock()
	if p.c != nil {
		_ = p.c.rw.Close()
		p.c = nil
	}
}
