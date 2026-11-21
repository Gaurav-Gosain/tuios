package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

var attachForce bool

var attachDetachOthers bool

func nestedAllowed() bool {
	return attachForce || session.NestedAllowedByEnv()
}

func showMadeSession(host, name string) error {
	if !inPane() {
		return runDaemonSessionOn(host, name, false)
	}
	if err := runSwitchSession(name, host, "", "", false, "", false); err != nil {
		attach := "tuios attach " + name
		if host != "" {
			attach = "tuios attach --host " + host + " " + name
		}
		return errfmt.Errorf("session %s is ready, and the client did not switch to it: %w. To show it, run '%s' in a terminal outside tuios", name, err, attach)
	}
	return nil
}

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
