package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// attachForce is tuios attach --force: attach even from a pane of the session.
var attachForce bool

// attachDetachOthers is tuios attach -d: detach every other client of the
// session as this one attaches.
var attachDetachOthers bool

// nestedAllowed reports whether this attach may go through from a pane of its
// own session: --force, or TUIOS_ALLOW_NESTED=1.
func nestedAllowed() bool {
	return attachForce || session.NestedAllowedByEnv()
}

// showMadeSession shows a session a command just made. Outside tuios it
// attaches, as before. In a tuios pane an attach would draw a second client
// inside the pane, so it switches the client that shows the pane instead, as
// tuios switch-session does. host is the machine of the session, "" for this
// one.
func showMadeSession(host, name string) error {
	if !inPane() {
		return runDaemonSessionOn(host, name, false)
	}
	if err := runSwitchSession(name, host, "", "", false, "", false); err != nil {
		// A second switch-session from this pane fails the same way, so the
		// hint names the way that works: an attach outside tuios.
		attach := "tuios attach " + name
		if host != "" {
			attach = "tuios attach --host " + host + " " + name
		}
		return errfmt.Errorf("session %s is ready, and the client did not switch to it: %w. To show it, run '%s' in a terminal outside tuios", name, err, attach)
	}
	return nil
}

// explainNestedAttach words the refusal of an attach from inside the session
// for the command line. A named attach gets the daemon's message. An unnamed
// one (a bare tuios in a pane) did not ask to attach to anything in
// particular, so it is told where it is and what else there is. names are the
// sessions the daemon listed, or nil to ask for them here.
func explainNestedAttach(nested *session.NestedAttachError, names []string) error {
	if !nested.Unnamed {
		return &diagnosticError{What: nested.Error(), Err: nested}
	}
	if names == nil && session.IsDaemonRunning() {
		if client, err := dialVerb(); err == nil {
			if infos, err := listSessionInfos(client); err == nil {
				for _, info := range infos {
					names = append(names, info.Name)
				}
			}
			_ = client.Close()
		}
	}
	var others []string
	for _, n := range names {
		if n != nested.Session {
			others = append(others, n)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are inside session %q.", nested.Session)
	if len(others) > 0 {
		fmt.Fprintf(&b, " Other sessions: %s.", strings.Join(truncateList(others, 12), ", "))
		b.WriteString(" To show one of them in this pane, run 'tuios attach NAME'.")
	} else {
		b.WriteString(" There are no other sessions.")
	}
	b.WriteString(" To start a new session, run 'tuios new NAME'.")
	return &diagnosticError{What: b.String(), Err: nested}
}
