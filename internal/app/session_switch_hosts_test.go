package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// #196: every surface that switches sessions has to send the switch to the
// machine that holds the session. A row under another machine's group carries
// a rail identity for an ID ("\x00host/local:home"), and the switcher handed
// that identity to the daemon the client was connected to, which refused it as
// a session name with a path separator in it.
//
// These tests record where each surface sends a switch through the switch hook,
// so they need no daemon on either end. The attach across the link itself is
// proved end to end in e2e/tui.

type switchCall struct{ host, name string }

// recordSwitches installs the switch hook and returns the calls it saw.
func recordSwitches(m *OS) *[]switchCall {
	var calls []switchCall
	m.SetSessionSwitchHookForTest(func(host, name string) error {
		calls = append(calls, switchCall{host, name})
		return nil
	})
	return &calls
}

// hostsOS is a client with this machine holding "home" and "build" holding
// "api". With away set, the client is attached to build's "api", so this
// machine's "home" is a row under a group named local.
func hostsOS(t *testing.T, away bool) *OS {
	t.Helper()
	m := paletteHostOS(t)
	if away {
		m.AttachedHost = "build"
		m.SessionName = "api"
	}
	return m
}

// goToSession opens go-to on the sessions and goes to the row titled want,
// as enter or a click on it does, or fails.
func goToSession(t *testing.T, m *OS, want string) {
	t.Helper()
	m.OpenSessionSwitcher()
	rows := m.gotoFiltered()
	for i, r := range rows {
		if r.kind == gotoSession && r.node.Title == want {
			m.gotoPicker.list.Cursor = i
			m.GotoPickerActivate()
			return
		}
	}
	t.Fatalf("go-to does not list the session %q: %+v", want, rows)
}

func wantOneSwitch(t *testing.T, calls []switchCall, want switchCall) {
	t.Helper()
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("ASSERTION: the switch went to %+v, want exactly %+v", calls, want)
	}
	if strings.Contains(calls[0].name, "\x00") {
		t.Fatalf("ASSERTION: a rail identity reached the daemon as a session name: %q", calls[0].name)
	}
}

// TestSwitcherNeverListsAMachineHeader. The header "local" was the row the
// report selected: listed as a session, its Enter asked for "\x00host/local".
func TestSwitcherNeverListsAMachineHeader(t *testing.T) {
	for _, away := range []bool{false, true} {
		m := hostsOS(t, away)
		m.OpenSessionSwitcher()
		for _, r := range m.gotoFiltered() {
			if r.node.Kind == sessiontree.KindHost {
				t.Errorf("ASSERTION: away=%v: go-to lists the machine header %q as a session", away, r.node.Title)
			}
		}
	}
}

// TestSwitcherClickReachesThisMachineFromARemoteSession is the report: on a
// remote session, a click on this machine's session in the switcher.

func TestSwitcherClickReachesThisMachineFromARemoteSession(t *testing.T) {
	m := hostsOS(t, true)
	calls := recordSwitches(m)
	goToSession(t, m, "home")
	wantOneSwitch(t, *calls, switchCall{federation.LocalHostName, "home"})
}

// TestSwitcherClickReachesARemoteSessionFromHere is the follow-up comment: on
// this machine, a click on a session under build.
func TestSwitcherClickReachesARemoteSessionFromHere(t *testing.T) {
	m := hostsOS(t, false)
	calls := recordSwitches(m)
	goToSession(t, m, "api")
	wantOneSwitch(t, *calls, switchCall{"build", "api"})
}

// TestSwitcherReachesOneRemoteFromAnother: attached to build, a session on a
// third machine goes to that machine, not to build and not to this one.
func TestSwitcherReachesOneRemoteFromAnother(t *testing.T) {
	m := hostsOS(t, true)
	m.FederationHosts = append(m.FederationHosts, FederationHost{
		Name:     "gpu",
		Status:   string(federation.StatusUp),
		Sessions: []FederationSession{{Name: "train", WindowCount: 1}},
	})
	calls := recordSwitches(m)
	goToSession(t, m, "train")
	wantOneSwitch(t, *calls, switchCall{"gpu", "train"})
}

// TestSwitcherToADownHostWaitsForTheLink: the switcher opens a session on a
// machine whose link is not up the way the rail does (#720). Nothing is
// dialed yet, the click waits for the link, and the dock says so.
func TestSwitcherToADownHostWaitsForTheLink(t *testing.T) {
	m := hostsOS(t, false)
	m.FederationHosts[1].Status = string(federation.StatusReconnecting)
	goToSession(t, m, "api")
	if p := m.hostOpen; p == nil || p.host != "build" || p.name != "api" || p.dialing {
		t.Fatalf("ASSERTION: the switcher did not wait for build's link: %+v", m.hostOpen)
	}
	if note := m.hostLinkNote(); !strings.Contains(note, "Reconnecting to build") {
		t.Fatalf("ASSERTION: the dock says %q while the switch waits", note)
	}
}

// TestOpenSessionNodeRefusesAMachineHeader: a header is not a session, so
// asking to open one opens nothing and reaches no daemon.
func TestOpenSessionNodeRefusesAMachineHeader(t *testing.T) {
	m := hostsOS(t, true)
	calls := recordSwitches(m)
	header := sessiontree.Node{Kind: sessiontree.KindHost, ID: hostNodeID("local"), Title: "local", Host: "local"}

	if m.OpenSessionNode(header) {
		t.Fatal("ASSERTION: a machine header opened as a session")
	}
	if len(*calls) != 0 {
		t.Fatalf("ASSERTION: a machine header reached a daemon: %+v", *calls)
	}
}

// TestPaletteReachesThisMachineFromARemoteSession: the palette's rows under
// another machine go through the same helper as the switcher.
func TestPaletteReachesThisMachineFromARemoteSession(t *testing.T) {
	m := hostsOS(t, true)
	calls := recordSwitches(m)
	for _, item := range getSessionPaletteItems(m) {
		if item.Name == "Session: home @ local" {
			item.Action(m)
			wantOneSwitch(t, *calls, switchCall{federation.LocalHostName, "home"})
			return
		}
	}
	t.Fatalf("the palette offers no row for home @ local")
}

// TestRailRemoteRowSwitchesAcrossMachines: a click or Enter on a session row
// under another machine, in both directions.
func TestRailRemoteRowSwitchesAcrossMachines(t *testing.T) {
	cases := []struct {
		name string
		away bool
		hit  sidebarRowHit
		want switchCall
	}{
		{"back home", true, sidebarRowHit{Kind: sidebarRowHostSession, SessionID: federation.LocalHostName, WindowID: "home"},
			switchCall{federation.LocalHostName, "home"}},
		{"out to build", false, sidebarRowHit{Kind: sidebarRowHostSession, SessionID: "build", WindowID: "api"},
			switchCall{"build", "api"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := hostsOS(t, tc.away)
			calls := recordSwitches(m)
			m.sidebarActivateRow(tc.hit)
			wantOneSwitch(t, *calls, tc.want)
		})
	}
}

// TestRailKeyboardRemoteRowSwitchesAcrossMachines: the rail's Enter on the
// same rows.
func TestRailKeyboardRemoteRowSwitchesAcrossMachines(t *testing.T) {
	m := hostsOS(t, true)
	calls := recordSwitches(m)
	m.SidebarNav = []sidebarNavRow{{Kind: sidebarRowHostSession, SessionID: federation.LocalHostName, WindowID: "home"}}
	m.SidebarCursor = 0

	m.SidebarActivateCursor()
	wantOneSwitch(t, *calls, switchCall{federation.LocalHostName, "home"})
}

// TestRailLocalRowStaysOnTheConnection: a session row on the attached machine
// is switched by its own name over the current connection, from a click and
// from a pane row of another session.
func TestRailLocalRowStaysOnTheConnection(t *testing.T) {
	m := hostsOS(t, false)
	calls := recordSwitches(m)
	m.sidebarActivateRow(sidebarRowHit{Kind: sidebarRowSession, SessionID: "notes"})
	wantOneSwitch(t, *calls, switchCall{"", "notes"})

	*calls = nil
	m.sidebarFocusWindow(sidebarRowHit{Kind: sidebarRowWindow, SessionID: "notes", WindowID: "zz-not-here", WindowIndex: -1})
	wantOneSwitch(t, *calls, switchCall{"", "notes"})
}
