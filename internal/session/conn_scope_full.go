//go:build !slim

package session

// callerSession is the session of the pane the caller on cs runs in, "" when it
// runs in none or cannot be placed.
func (d *Daemon) callerSession(cs *connState) string {
	if cs == nil {
		return ""
	}
	if sc := cs.scope.Load(); sc != nil {
		return d.sessionNameByID(sc.sessionID)
	}
	// A connection that presented its pane's token is in that pane.
	if w := cs.paneBound.Load(); w != nil {
		return d.sessionOfWindow(*w)
	}
	if cs.viaLink || cs.paneOnly || cs.peerPID <= 0 {
		return ""
	}
	if _, win := d.peerPane(cs); win != "" {
		return d.sessionOfWindow(win)
	}
	return ""
}

// eventInScope reports whether an event may be written to cs. Gap markers
// always may. Under scope own an event reaches the stream only when its
// session is one the connection reaches; an event with no session, such as
// host-changed, does not.
//
// A pane without the admin grant is held the same way to the sessions it may
// read, as they stood at its last call, which for a stream is its subscribe.
// See pane_grants.go.
func (d *Daemon) eventInScope(cs *connState, ev streamEvent) bool {
	if ev.Type == EventGap {
		return true
	}
	var owners []string
	if sc := cs.scope.Load(); sc != nil && sc.own {
		owners = append(owners, d.sessionNameByID(sc.sessionID))
	}
	if pa := cs.paneView.Load(); pa != nil && !pa.grants.Has(GrantAdmin) {
		// The pane's session as it is named now: the view was taken at the
		// stream's subscribe, and the session may have been renamed since.
		own := pa.session
		if pa.sessionID != "" {
			own = d.sessionNameByID(pa.sessionID)
		}
		owners = append(owners, own)
	}
	if len(owners) == 0 {
		return true
	}
	if ev.Host != "" {
		return false
	}
	session := ev.Session
	if session == "" && ev.Attention != nil {
		session = ev.Attention.Session
	}
	for _, own := range owners {
		if !d.sessionInScope(own, session) {
			return false
		}
	}
	return true
}
