package session

import (
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

const latestHold = time.Second

const activityRecalcGap = 50 * time.Millisecond

func windowSizePolicy(v string) string {
	switch v {
	case config.WindowSizeLargest, config.WindowSizeSmallest:
		return v
	}
	return config.WindowSizeLatest
}

const optionWindowSize = "daemon.window_size"

func (d *Daemon) sessionWindowSize(s *Session) string {
	if s != nil {
		if v, ok := s.GetOption(optionWindowSize); ok {
			return windowSizePolicy(v)
		}
	}
	return windowSizePolicy(d.windowSize)
}

type latestState struct {
	mu       sync.Mutex
	sessions map[string]*sessionLatest
}

type sessionLatest struct {
	client  string
	timer   *time.Timer
	left    time.Time
	leftSeq uint64
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

type sizedClient struct {
	id       string
	w, h     int
	viewOnly bool
	activity time.Time
	seq      uint64
}

func (d *Daemon) sessionSizedClients(sessionID string) []sizedClient {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	var out []sizedClient
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && !cs.passive
		c := sizedClient{
			id: cs.clientID, w: cs.width, h: cs.height,
			viewOnly: cs.viewOnly,
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

func (d *Daemon) pickLatest(sessionID string, clients []sizedClient, now time.Time) (latest string, kept bool) {
	d.latest.mu.Lock()
	defer d.latest.mu.Unlock()
	st := d.latest.get(sessionID)

	var holder *sizedClient
	var best *sizedClient
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
	if holder != nil {
		st.left = time.Time{}
	}
	switch {
	case holder == nil && !st.left.IsZero() && anyAttachedBy(clients, st.leftSeq) &&
		(best == nil || !best.activity.After(st.left)):
		kept = true
	case holder == nil && best == nil:
		st.client = ""
	case holder == nil:
		st.client = best.id
	case best == nil || !best.activity.After(holder.activity):
	case now.Sub(holder.activity) >= latestHold:
		st.client = best.id
	default:
		d.armLatestTimer(sessionID, st, holder.activity.Add(latestHold).Sub(now))
		return st.client, false
	}
	if !kept {
		st.left = time.Time{}
	}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	return st.client, kept
}

func anyAttachedBy(clients []sizedClient, seq uint64) bool {
	for _, c := range clients {
		if c.seq <= seq {
			return true
		}
	}
	return false
}

func (d *Daemon) latestLeft(sessionID, clientID string, input time.Time) {
	if input.IsZero() {
		return
	}
	d.latest.mu.Lock()
	defer d.latest.mu.Unlock()
	if st := d.latest.sessions[sessionID]; st != nil && st.client == clientID {
		st.left = input
		st.leftSeq = d.attachCount.Load()
	}
}

func (d *Daemon) latestReturnInput(sessionID, clientID string) time.Time {
	d.latest.mu.Lock()
	defer d.latest.mu.Unlock()
	if st := d.latest.sessions[sessionID]; st != nil && st.client == clientID {
		return st.left
	}
	return time.Time{}
}

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

func sizeForPolicy(policy string, clients []sizedClient, latest string) (width, height int) {
	if policy == config.WindowSizeLatest {
		for _, c := range clients {
			if c.id == latest {
				return clampClientSize(c.w, c.h)
			}
		}
	}
	if len(clients) == 0 {
		return 0, 0
	}
	width, height = clampClientSize(clients[0].w, clients[0].h)
	for _, c := range clients[1:] {
		cw, ch := clampClientSize(c.w, c.h)
		if policy == config.WindowSizeLargest {
			width, height = max(width, cw), max(height, ch)
		} else {
			width, height = min(width, cw), min(height, ch)
		}
	}
	return width, height
}

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
	if now.Sub(previous) < activityRecalcGap {
		return nil
	}
	if d.sessionWindowSize(d.manager.GetSessionByID(sessionID)) != config.WindowSizeLatest {
		return nil
	}
	d.latest.mu.Lock()
	holder := d.latest.get(sessionID).client
	d.latest.mu.Unlock()
	if holder == cs.clientID {
		return nil
	}
	d.recalculateAndBroadcastSize(sessionID, "")
	return nil
}
