package session

import (
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A viewer as the latest client.
//
// Under window_size = latest the session takes the size of the TUI client
// that last had input (window_size.go). A desktop app that streams the panes
// (guios) attaches no client, so it never was one: its yield leases step
// aside while any TUI client shows the session, and a TUI attached at
// 157x43 sized the panes the app drew in smaller tiles, which then ran off
// their right edge.
//
// So a viewer contends for latest too. The viewer is the process: one app
// holds a stream per pane, and every pane it shows must follow, not only the
// one typed in. Its input (I and J frames) and its window gaining focus (an
// A frame, which the app sends only for its own window's focus-in, never
// for a click on a pane) count as the viewer's activity. While the viewer
// holds latest, its yield leases size their panes as a fit lease does with
// no client attached, and each TUI client draws those panes smaller in its
// own rectangles, as it does for any lease. Input at a TUI client hands it
// back. Both ways the side that holds keeps it for latestHold after its own
// last activity, so two people typing at once do not flap the layout.
//
// What may contend: a stream with a yield lease whose caller may act as the
// person (mayActAsHuman). An agent in a pane must not take the layout from
// the person. The session's own size, its layout and the TUI clients'
// choice among themselves do not change: the lead acts only through the
// viewer's leases, pane by pane, so a pane the viewer holds no lease on
// keeps the TUI's size.

// viewerLeadState is each session's viewer contender.
type viewerLeadState struct {
	mu       sync.Mutex
	sessions map[string]*viewerLead
}

type viewerLead struct {
	// owner is the viewer with the newest activity, at its time.
	owner string
	at    time.Time
	// holds says the viewer is the latest now, over every TUI client.
	holds bool
	timer *time.Timer
}

// viewerOwner is the key of the viewer a stream belongs to, "" when the
// stream may not contend: no yield lease, or a caller that may not act as
// the person.
func (d *Daemon) viewerOwner(cs *connState, yield bool) string {
	if !yield || cs == nil || !d.mayActAsHuman(cs) {
		return ""
	}
	cs.mu.Lock()
	peer := cs.linkPeer
	cs.mu.Unlock()
	switch {
	case cs.viaLink && peer != "":
		return "link:" + peer
	case cs.viaLink:
		// A link that named no machine: only this stream.
		return "conn:" + cs.clientID
	case cs.peerPID > 0:
		return "pid:" + strconv.Itoa(cs.peerPID)
	}
	return "conn:" + cs.clientID
}

// viewerLeadOwner is the viewer that holds latest in the session now, "" when
// none does.
func (d *Daemon) viewerLeadOwner(sessionID string) string {
	d.viewerLead.mu.Lock()
	defer d.viewerLead.mu.Unlock()
	if st := d.viewerLead.sessions[sessionID]; st != nil && st.holds {
		return st.owner
	}
	return ""
}

// noteViewerActivity records the viewer's input or focus in the session, and
// lets it take latest.
func (d *Daemon) noteViewerActivity(sessionID, owner string) {
	if sessionID == "" || owner == "" {
		return
	}
	now := time.Now()
	d.viewerLead.mu.Lock()
	if d.viewerLead.sessions == nil {
		d.viewerLead.sessions = make(map[string]*viewerLead)
	}
	st := d.viewerLead.sessions[sessionID]
	if st == nil {
		st = &viewerLead{}
		d.viewerLead.sessions[sessionID] = st
	}
	// A flood of keys records its time and decides nothing new: the holder
	// typing again only moves its own hold.
	same := st.owner == owner && st.holds && now.Sub(st.at) < activityRecalcGap
	st.owner, st.at = owner, now
	d.viewerLead.mu.Unlock()
	if !same {
		d.decideViewerLead(sessionID)
	}
}

// decideViewerLead settles whether the session's viewer holds latest against
// its TUI clients, and tells every pane when that changed.
func (d *Daemon) decideViewerLead(sessionID string) {
	sess := d.manager.GetSessionByID(sessionID)
	if sess == nil {
		d.forgetViewerLead(sessionID)
		return
	}
	clients := d.sessionSizedClients(sessionID)
	latest := d.effectiveWindowSize(sess, clients) == config.WindowSizeLatest
	var tui time.Time
	for _, c := range sizingClients(config.WindowSizeLatest, clients) {
		if c.activity.After(tui) {
			tui = c.activity
		}
	}
	now := time.Now()
	d.viewerLead.mu.Lock()
	st := d.viewerLead.sessions[sessionID]
	if st == nil {
		d.viewerLead.mu.Unlock()
		return
	}
	was := st.holds
	var wait time.Duration
	switch {
	case !latest:
		st.holds = false
	case !st.holds && st.at.After(tui):
		if tui.IsZero() || now.Sub(tui) >= latestHold {
			st.holds = true
		} else {
			wait = tui.Add(latestHold).Sub(now)
		}
	case st.holds && tui.After(st.at):
		if now.Sub(st.at) >= latestHold {
			st.holds = false
		} else {
			wait = st.at.Add(latestHold).Sub(now)
		}
	}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	if wait > 0 {
		st.timer = time.AfterFunc(wait, func() { d.decideViewerLead(sessionID) })
	}
	holds, owner := st.holds, ""
	if holds {
		owner = st.owner
	}
	d.viewerLead.mu.Unlock()
	if was != holds || holds {
		switch {
		case was && !holds:
			LogBasic("Session %s: a viewer is no longer the latest client", sess.Name())
		case !was && holds:
			LogBasic("Session %s: a viewer is now the latest client", sess.Name())
		}
		for _, p := range sess.allPTYs() {
			p.setLeadOwner(owner)
		}
	}
}

// viewerLeadTUIActivity is a TUI client's input in the session: it may take
// latest back from a viewer.
func (d *Daemon) viewerLeadTUIActivity(sessionID string) {
	d.viewerLead.mu.Lock()
	st := d.viewerLead.sessions[sessionID]
	contended := st != nil && (st.holds || st.timer != nil)
	d.viewerLead.mu.Unlock()
	if contended {
		d.decideViewerLead(sessionID)
	}
}

// forgetViewerLead drops a session's viewer state when the session ends.
func (d *Daemon) forgetViewerLead(sessionID string) {
	d.viewerLead.mu.Lock()
	defer d.viewerLead.mu.Unlock()
	if st := d.viewerLead.sessions[sessionID]; st != nil {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(d.viewerLead.sessions, sessionID)
	}
}

// setLeadOwner records which viewer holds latest in the pane's session, and
// sizes the pane again when that changed and the viewer holds a lease on it.
func (p *PTY) setLeadOwner(owner string) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if p.leadOwner == owner {
		return
	}
	old := p.leadOwner
	p.leadOwner = owner
	held := false
	for _, l := range p.leases {
		held = held || l.yield && l.owner != "" && (l.owner == owner || l.owner == old)
	}
	if !held || p.ctx.Err() != nil {
		return
	}
	if err := p.applyLeasesLocked(); err != nil {
		LogBasic("PTY %s: the latest viewer's lease could not resize the pane: %v", shortID(p.ID), err)
	}
}

// noteLeadOwner records the viewer that holds latest without sizing the pane,
// for a lease about to be set, which sizes it.
func (p *PTY) noteLeadOwner(owner string) {
	p.streamMu.Lock()
	p.leadOwner = owner
	p.streamMu.Unlock()
}

// leadLeaseLocked is the lease of the viewer that holds latest, when it holds
// one on this pane. streamMu is held.
func (p *PTY) leadLeaseLocked() (paneLease, bool) {
	if p.leadOwner == "" || p.noClients.Load() {
		return paneLease{}, false
	}
	var lead paneLease
	found := false
	for _, l := range p.leases {
		if l.yield && l.owner == p.leadOwner && l.width > 0 && l.height > 0 {
			if !found || l.width*l.height > lead.width*lead.height {
				lead = l
			}
			found = true
		}
	}
	return lead, found
}
