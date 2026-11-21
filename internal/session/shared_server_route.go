package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

type sharedServerRoute struct {
	sess     *Session
	windowID string
	pid      int
}

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
		session string
		joined  string
		exact   bool
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
			return false
		}
		return !file.Start.Before(c.id.started.Add(-viewStartSlack)) && !file.Start.After(c.id.started.Add(viewStartWindow))
	})
	return r, ok
}

func placeRoutedReport(r *AgentReport, route sharedServerRoute) {
	r.HarnessPID = route.pid
	r.HarnessIsCaller = true
	r.foreignPane, r.harnessGone, r.fromPane = false, false, false
}
