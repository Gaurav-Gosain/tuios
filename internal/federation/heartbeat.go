package federation

import (
	"context"
	"errors"
	"sync"
	"time"
)

// The link's own heartbeat.
//
// ssh's ServerAlive probes find a dead path in about KeepaliveWindow, and they
// do not run at all when the link rides a shared master: the master holds the
// TCP connection, and its keepalives are whatever the person's ssh config
// says. A laptop that wakes from sleep has a link that looks up and carries
// nothing, and a session row on the rail that opens nothing until ssh gives
// up.
//
// So the link checks itself. While the link is up, it asks the far daemon for
// a hello on a stream of its own every heartbeatInterval, but only when no
// frame came off the pipe in that time: a link that carries traffic is alive,
// and an idle one costs one small round trip per interval. A reply of any
// kind, an error included, proves the far side is there. A frame of any kind
// read while the ping waits proves the same, so a ping stuck behind a busy
// pipe is not a miss.
//
// The same timer finds a wake from sleep. The monotonic clock does not run
// while the machine sleeps, and the wall clock does, so a gap between the two
// across one tick is a sleep. After a sleep, and after a person asks for the
// host (retry-host), one missed ping ends the link at once. Otherwise
// heartbeatMisses in a row end it.

const (
	// heartbeatInterval is how long a link may carry no frame before the
	// heartbeat pings it. It is also how often a link that waits to dial
	// again checks for a wake from sleep.
	heartbeatInterval = 15 * time.Second
	// heartbeatTimeout is how long one ping waits for its reply.
	heartbeatTimeout = 5 * time.Second
	// heartbeatRetry is the wait before the next ping after a miss.
	heartbeatRetry = time.Second
	// heartbeatMisses is how many pings in a row may miss before the link
	// is ended.
	heartbeatMisses = 2
	// sleepJump is how far the wall clock must run ahead of the monotonic
	// clock across one tick for the tick to count as a wake from sleep.
	sleepJump = 5 * time.Second
)

// sleepWatch finds a wake from sleep between two calls of slept.
type sleepWatch struct{ last time.Time }

func newSleepWatch() sleepWatch { return sleepWatch{last: time.Now()} }

// slept reports whether the machine slept since the last call. time.Now
// carries both clocks, Sub uses the monotonic one, and Round(0) strips it so
// Sub uses the wall clock. Linux and macOS stop the monotonic clock during a
// sleep. Windows does not, so there only the ping finds a dead link.
func (w *sleepWatch) slept() bool {
	now := time.Now()
	mono := now.Sub(w.last)
	wall := now.Round(0).Sub(w.last.Round(0))
	w.last = now
	return wall-mono > sleepJump
}

// readSince reports whether the read loop took a frame off the pipe after
// start, or is parked handing one to a slow stream.
func (m *mux) readSince(start int64) bool {
	return m.blocked.Load() > 0 || m.lastRead.Load() > start
}

// quietCheckIn is how long until the pipe has been quiet for interval, or 0
// when it already has. A read loop parked on a slow stream counts as a read
// now.
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
	// A floor, so a frame that lands just before the check does not turn
	// the timer into a spin.
	return max(left, time.Second)
}

// heartbeat watches a link that is up until the link ends. It calls
// tearDown when the far side stops answering.
func (l *link) heartbeat(ctx context.Context, m *mux, closed <-chan struct{}, tearDown func()) {
	var p pinger
	defer p.close()
	clock := newSleepWatch()
	t := time.NewTimer(heartbeatInterval)
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
			// Since Go 1.23 a stopped timer sends nothing stale, so a
			// Reset below needs no drain.
			t.Stop()
		}
		woke := clock.slept()
		if !asked && !woke && misses == 0 {
			// A frame in the last interval proves the pipe. The next
			// check is one interval after that frame, so a pipe that goes
			// quiet is pinged heartbeatInterval later, not up to twice
			// that.
			if next := m.quietCheckIn(heartbeatInterval); next > 0 {
				t.Reset(next)
				continue
			}
		}
		start := time.Now().UnixNano()
		if p.ping(ctx, m) || m.readSince(start) {
			misses = 0
			t.Reset(heartbeatInterval)
			continue
		}
		misses++
		if !woke && !asked && misses < heartbeatMisses {
			t.Reset(heartbeatRetry)
			continue
		}
		// The link can end for another reason while the ping waits: the
		// manager stopped, ssh exited, or a call tore it down. That drop is
		// not the heartbeat's, so it leaves no note and no fresh dial.
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

// pinger owns the heartbeat's stream. One ping runs at a time. A ping whose
// write is stuck on a dead pipe is left to the teardown, which closes the
// pipe under it.
type pinger struct {
	mu sync.Mutex
	c  *caller
}

// ping sends one hello on the heartbeat's stream and reports whether an
// answer came within heartbeatTimeout. The hello names no client, so the far
// daemon does not log it.
func (p *pinger) ping(ctx context.Context, m *mux) bool {
	done := make(chan bool, 1)
	go func() {
		if !p.mu.TryLock() {
			// The last ping is still stuck. That is a miss too.
			done <- false
			return
		}
		defer p.mu.Unlock()
		if p.c == nil {
			s, err := m.Open()
			if err != nil {
				done <- false
				return
			}
			p.c = newCaller(s)
		}
		callCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
		defer cancel()
		_, err := p.c.call(callCtx, "hello", nil)
		var remote *RemoteError
		if err == nil || errors.As(err, &remote) {
			done <- true
			return
		}
		// A stream that missed an answer cannot be used again: the late
		// answer would be read as the next one.
		_ = p.c.rw.Close()
		p.c = nil
		done <- false
	}()
	t := time.NewTimer(heartbeatTimeout + time.Second)
	defer t.Stop()
	select {
	case ok := <-done:
		return ok
	case <-t.C:
		return false
	}
}

// close ends the heartbeat's stream, unless a stuck ping holds it.
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
