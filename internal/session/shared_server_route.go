package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// A shared server's report for a pane that closed finds the pane that runs
// its conversation (#1391).
//
// Codex runs every conversation's hooks in one app-server, and the server keeps
// the TUIOS_PANE_ID of the pane that started it. Once that pane closes, every
// hook names a window that is gone. The conversation still runs in a live pane,
// whose codex is a client of the server, so the report is placed there when one
// pane, and only one, is the conversation's by what the daemon reads itself:
//
//   - the pane already holds the conversation id, from an earlier report
//     placed here;
//   - the hookless search joined the pane to the rollout the report names;
//   - or the rollout the report names started in the pane's folder, within
//     viewStartWindow of the pane's codex starting, and near no other pane's.
//     This is the search's own rule for one file, so a SessionStart that comes
//     before the search has run is not lost.
//
// What the caller says picks among panes the daemon already ties to the
// conversation. It never names a pane by itself.
//
// Only a caller that holds no pane is placed: a hook in a shared server is a
// process outside every pane, and such a caller may already name any pane with
// --window. A pane's process, a plugin, a link or a hosted pane's channel is held
// to its own pane, and is answered window_not_found as before.
//
// The report is recorded with the pane's codex as its harness process, not the
// server, which outlives it. So the agent row clears when that codex exits, as
// for a pane whose hooks reach it.

// sharedServerRoute is the pane a report from a shared server is placed on.
type sharedServerRoute struct {
	sess     *Session
	windowID string
	// pid is the pane's agent process, recorded as the report's harness.
	pid int
}

// routeSharedServerReport places a report whose window is gone on the pane that
// runs its conversation. ok is false when the window exists, the report is not
// a shared server's, the caller holds a pane, or no single pane is the
// conversation's.
func (d *Daemon) routeSharedServerReport(cs *connState, sessionName, window, harnessID, sessionID, transcriptPath string) (sharedServerRoute, bool) {
	reg := d.agentMatcher.registry
	if window == "" || sessionID == "" || reg == nil || !reg.SharedServer(harnessID) {
		return sharedServerRoute{}, false
	}
	if sess := d.findTargetSession(cs, sessionName); sess != nil {
		sess.stateMu.RLock()
		_, err := findWindowStateIndex(sess.state.Windows, window)
		sess.stateMu.RUnlock()
		if err == nil {
			return sharedServerRoute{}, false
		}
	}
	if cs == nil || cs.viaLink || cs.paneOnly || cs.hostedWindow != "" || d.paneAuthority(cs) != nil {
		return sharedServerRoute{}, false
	}

	type candidate struct {
		route   sharedServerRoute
		session string // the conversation the pane holds
		joined  string // the file the pane is joined to
		exact   bool   // a hook named that file
		id      agentIdentity
	}
	var cands []candidate
	for _, sess := range d.manager.AllSessions() {
		type win struct{ id, pty, session string }
		var wins []win
		sess.stateMu.RLock()
		for i := range sess.state.Windows {
			w := &sess.state.Windows[i]
			if w.AgentHarness == harnessID && w.PTYID != "" && w.Host == "" {
				session := ""
				if w.AgentSessionHarness == harnessID {
					session = w.AgentSessionID
				}
				wins = append(wins, win{w.ID, w.PTYID, session})
			}
		}
		sess.stateMu.RUnlock()
		if len(wins) == 0 {
			continue
		}
		identify := d.paneAgentIdentifier(sess)
		for _, w := range wins {
			id := identify(w.pty)
			if !id.agent || id.pid <= 1 {
				// No agent runs in the pane now, whatever it last held.
				continue
			}
			c := candidate{route: sharedServerRoute{sess: sess, windowID: w.id, pid: id.pid}, session: w.session, id: id}
			sess.transcripts.mu.Lock()
			if j, ok := sess.transcripts.joins[w.id]; ok {
				c.joined, c.exact = j.reader.Path(), j.exact
			}
			sess.transcripts.mu.Unlock()
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return sharedServerRoute{}, false
	}
	only := func(match func(candidate) bool) (sharedServerRoute, bool, bool) {
		var found []sharedServerRoute
		for _, c := range cands {
			if match(c) {
				found = append(found, c.route)
			}
		}
		if len(found) == 1 {
			return found[0], true, true
		}
		// Two panes that both match is a refusal, not a reason to try the
		// next rule.
		return sharedServerRoute{}, false, len(found) > 1
	}
	if r, ok, decided := only(func(c candidate) bool { return c.session == sessionID }); decided {
		return r, ok
	}
	tr := reg.TranscriptFor(harnessID)
	if transcriptPath == "" || tr == nil || tr.Reader == harness.ReaderJSONL || !tr.Verifies("cwd") {
		return sharedServerRoute{}, false
	}
	path, why := transcriptPathAllowed(tr, transcriptPath, peerGetenv(cs))
	if why != "" {
		return sharedServerRoute{}, false
	}
	if r, ok, decided := only(func(c candidate) bool { return c.joined == path }); decided {
		return r, ok
	}
	file, ok := transcriptview.Identify(tr.Reader, path)
	if !ok {
		return sharedServerRoute{}, false
	}
	r, ok, _ := only(func(c candidate) bool {
		if c.id.cwd == "" || c.id.started.IsZero() || !file.In(c.id.cwd) {
			return false
		}
		if c.exact && c.joined != path {
			// A hook named this pane's own file, and it is another.
			return false
		}
		return !file.Start.Before(c.id.started.Add(-viewStartSlack)) && !file.Start.After(c.id.started.Add(viewStartWindow))
	})
	return r, ok
}

// placeRoutedReport points a report placed by routeSharedServerReport at the
// pane's own agent process. The server that ran the hook outlives that process,
// so its pid would keep the agent row after the agent exits.
func placeRoutedReport(r *AgentReport, route sharedServerRoute) {
	r.HarnessPID = route.pid
	// The pid is the daemon's own reading of the pane, as trusted as a caller's.
	r.HarnessIsCaller = true
	r.foreignPane, r.harnessGone, r.fromPane = false, false, false
}
