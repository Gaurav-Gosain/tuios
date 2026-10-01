package session

import (
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The window_size policy: which client's size a session with several clients
// takes. It is tmux's window-size option, with the same names.
//
//   - smallest: the smallest client in each dimension. Every client sees the
//     whole session. This is the default, and what every build did before
//     the option existed.
//   - largest: the largest client in each dimension. A smaller client shows
//     the part of the session around the focused pane's cursor.
//   - latest: the client that last had input. A smaller client shows a part,
//     as for largest, and a larger one shows the rest as unused space.
//
// What counts as input is the client's to say, because the client is the only
// side that sees it: a key or a click that tuios itself handles (window
// management, the rail, the dock, a wheel over scrollback) never reaches the
// daemon as pane input. A client reports it with MsgClientActivity. A resize
// is not input, and neither is a focus report or a reply the host terminal
// sends on its own (DA, OSC colours, kitty graphics answers): bubbletea parses
// those into messages of their own, which the client does not report. tmux
// differs in two places, both on purpose: it counts a resize, and a focus-in,
// as activity, so a phone that only attached, or a laptop window that only
// gained focus, takes the session from the person typing on the other one.
//
// The gate. A client too old to say WindowSize in its hello sends no activity,
// and it lays its panes out in min(its terminal, the session), resizing the
// panes' PTYs to fit. Sized larger than it, it would fight every other client
// over the PTY sizes. So while such a client is attached the session takes
// the smallest client, whatever the policy says, and that client sees what it
// always saw.

// latestHold is how long the latest client keeps the session after its last
// input before another client's input can take it.
//
// Without a hold, two people typing at once move the session between their
// sizes on every key, and each move resizes every PTY in it, so every program
// redraws: the layout flaps. Typing has gaps of 100 to 300 ms between keys,
// and key repeat sends faster than that, so a hold well above 300 ms keeps a
// person who is typing from losing the session between two keys. A hold much
// longer than a second makes the switch feel broken to a person who moves to
// the other device and starts typing. One second is the middle of that range.
//
// The switch is not lost when the hold is still running: the daemon arms one
// timer for the moment the hold ends and decides again then. The timer is
// armed only while two clients contend, so an idle session has none.
const latestHold = time.Second

// activityRecalcGap is the least time between two reports from one client
// that each recalculate the session's size. See handleClientActivity.
const activityRecalcGap = 50 * time.Millisecond

// windowSizePolicy resolves a window_size value. Anything unknown is
// smallest, the default, which is the policy no client can be surprised by.
func windowSizePolicy(v string) string {
	switch v {
	case config.WindowSizeLargest, config.WindowSizeLatest:
		return v
	}
	return config.WindowSizeSmallest
}

// optionWindowSize is the option path of the policy. set-option records it on
// the session, which overrides the daemon's [daemon] window_size.
const optionWindowSize = "daemon.window_size"

// sessionWindowSize is the policy the session is set to: its own override, or
// the daemon's configured one. It says nothing about the gate; see
// effectiveWindowSize.
func (d *Daemon) sessionWindowSize(s *Session) string {
	if s != nil {
		if v, ok := s.GetOption(optionWindowSize); ok {
			return windowSizePolicy(v)
		}
	}
	return windowSizePolicy(d.windowSize)
}

// latestState is the latest client of each session, and the one timer that
// decides a contended switch.
type latestState struct {
	mu       sync.Mutex
	sessions map[string]*sessionLatest
}

type sessionLatest struct {
	client string
	timer  *time.Timer
}

func (l *latestState) get(sessionID string) *sessionLatest {
	if l.sessions == nil {
		l.sessions = make(map[string]*sessionLatest)
	}
	st := l.sessions[sessionID]
	if st == nil {
		st = &sessionLatest{}
		l.sessions[sessionID] = st
	}
	return st
}

// sizedClient is one TUI client of a session, as the size calculation reads
// it.
type sizedClient struct {
	id       string
	w, h     int
	capable  bool
	viewOnly bool
	activity time.Time
	seq      uint64
}

// sessionSizedClients lists the TUI clients attached to a session that have
// a size.
func (d *Daemon) sessionSizedClients(sessionID string) []sizedClient {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	var out []sizedClient
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
		c := sizedClient{
			id: cs.clientID, w: cs.width, h: cs.height,
			capable: cs.windowSizeCap, viewOnly: cs.viewOnly,
			activity: cs.lastActivity, seq: cs.attachSeq,
		}
		cs.mu.Unlock()
		if !match || c.w == 0 || c.h == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// pickLatest settles which client of a session is the latest, from what
// each client last did, and returns it. holder is the client that had it.
//
// The holder keeps it while it is attached and no other client has had input
// since its own, or while its own input is younger than latestHold. In the
// second case the timer is armed for the end of the hold. A session whose
// holder has gone hands it to the client with the newest input, and with no
// input anywhere to the client that attached last, which is the order tmux
// uses.
func (d *Daemon) pickLatest(sessionID string, clients []sizedClient, now time.Time) string {
	d.latest.mu.Lock()
	defer d.latest.mu.Unlock()
	st := d.latest.get(sessionID)

	var holder *sizedClient
	var best *sizedClient // the other client with the newest input
	for i := range clients {
		c := &clients[i]
		if c.id == st.client {
			holder = c
			continue
		}
		if best == nil || c.activity.After(best.activity) ||
			(c.activity.Equal(best.activity) && c.seq > best.seq) {
			best = c
		}
	}
	switch {
	case holder == nil && best == nil:
		st.client = ""
	case holder == nil:
		st.client = best.id
	case best == nil || !best.activity.After(holder.activity):
		// Nobody has had input since the holder.
	case now.Sub(holder.activity) >= latestHold:
		st.client = best.id
	default:
		d.armLatestTimer(sessionID, st, holder.activity.Add(latestHold).Sub(now))
		return st.client
	}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	return st.client
}

// armLatestTimer runs the decision again when the holder's hold ends. Called
// with d.latest.mu held.
func (d *Daemon) armLatestTimer(sessionID string, st *sessionLatest, after time.Duration) {
	if st.timer != nil {
		st.timer.Reset(after)
		return
	}
	st.timer = time.AfterFunc(after, func() {
		d.latest.mu.Lock()
		if cur := d.latest.sessions[sessionID]; cur == st {
			st.timer = nil
		}
		d.latest.mu.Unlock()
		d.recalculateAndBroadcastSize(sessionID, "")
	})
}

// forgetLatest drops a session's latest state when the session ends.
func (d *Daemon) forgetLatest(sessionID string) {
	d.latest.mu.Lock()
	defer d.latest.mu.Unlock()
	if st := d.latest.sessions[sessionID]; st != nil {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(d.latest.sessions, sessionID)
	}
}

// effectiveWindowSize is the policy in force for a session with these
// clients: its own policy, or smallest while a client that cannot draw a
// larger session is attached.
func (d *Daemon) effectiveWindowSize(s *Session, clients []sizedClient) string {
	for _, c := range clients {
		if !c.capable {
			return config.WindowSizeSmallest
		}
	}
	return d.sessionWindowSize(s)
}

// sizingClients is the clients whose size the policy reads. Under smallest
// that is every client, as it always was. Under largest and latest a client
// that sends no input (a read-only viewer) is left out: it cannot make itself
// the latest, and a viewer's large window should not make the session larger
// than anyone typing can see. A session of viewers only counts them all.
func sizingClients(policy string, clients []sizedClient) []sizedClient {
	if policy == config.WindowSizeSmallest {
		return clients
	}
	out := make([]sizedClient, 0, len(clients))
	for _, c := range clients {
		if !c.viewOnly {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return clients
	}
	return out
}

// sizeForPolicy is the session's size under a policy. Each client counts at
// no less than minClientWidth x minClientHeight.
func sizeForPolicy(policy string, clients []sizedClient, latest string) (width, height int) {
	if policy == config.WindowSizeLatest {
		for _, c := range clients {
			if c.id == latest {
				return clampClientSize(c.w, c.h)
			}
		}
	}
	first := true
	for _, c := range clients {
		cw, ch := clampClientSize(c.w, c.h)
		if first {
			width, height, first = cw, ch, false
			continue
		}
		if policy == config.WindowSizeLargest {
			width, height = max(width, cw), max(height, ch)
		} else {
			width, height = min(width, cw), min(height, ch)
		}
	}
	return width, height
}

// handleClientActivity records that the person at a client gave input, and
// lets the session follow it when the policy is latest.
func (d *Daemon) handleClientActivity(cs *connState) error {
	now := time.Now()
	cs.mu.Lock()
	previous := cs.lastActivity
	cs.lastActivity = now
	sessionID := cs.sessionID
	attached := cs.isTUIClient
	cs.mu.Unlock()
	if sessionID == "" || !attached {
		return nil
	}
	// A client throttles its reports to one per activityInterval. One that
	// sends faster gets its time recorded and nothing recalculated, so a
	// flood of reports costs a lock and a clock read each.
	if now.Sub(previous) < activityRecalcGap {
		return nil
	}
	if d.sessionWindowSize(d.manager.GetSessionByID(sessionID)) != config.WindowSizeLatest {
		// Recorded all the same, so a session switched to latest later starts
		// from who really typed last.
		return nil
	}
	d.latest.mu.Lock()
	holder := d.latest.get(sessionID).client
	d.latest.mu.Unlock()
	// The holder typing again only moves its own hold, which pickLatest
	// reads from the client the next time anything is decided. Nothing has
	// to be recalculated for it.
	if holder == cs.clientID {
		return nil
	}
	d.recalculateAndBroadcastSize(sessionID, "")
	return nil
}
