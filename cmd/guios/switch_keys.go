package main

import "time"

// A workspace switch on its way.
//
// A switch asks the daemon, and the new workspace's panes show only when its
// answer lands and their terminals attach (holdSwitches, shownSession), up to
// holdMax later. The switch holds the keys until then (key_owner.go), from
// the key that asks for it, and they go to the new workspace's pane. A
// switch that never lands lets the keys go after switchWait.

// switchState is the switch of the shown session on its way.
type switchState struct {
	sid    string    // the session whose switch is on its way, "" none
	since  time.Time // when it was asked
	landed bool      // the daemon's answer came (holdSwitches)
}

// switchStarted notes a switch of the session asked, and holds the keys
// until its panes show.
func (a *App) switchStarted(sid string) {
	a.sw = switchState{sid: sid, since: time.Now()}
	a.holdKeys("switch", switchWait+holdMax, func() bool { return !a.switching() })
}

// switchLanded notes the daemon's answer for the session.
func (a *App) switchLanded(sid string) {
	if a.sw.sid == sid {
		a.sw.landed = true
	}
}

// switching reports whether the shown session's switch is on its way: asked
// and not answered, or answered and held until its panes attach.
func (a *App) switching() bool {
	sw := &a.sw
	if sw.sid == "" || sw.sid != a.selected || a.quick.drop {
		// The scratch shows over every workspace and keeps the keys.
		return false
	}
	if a.holds[sw.sid] != nil {
		return true
	}
	return !sw.landed && time.Since(sw.since) < switchWait
}
