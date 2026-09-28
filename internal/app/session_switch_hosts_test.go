package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
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

// switcherIndex is the switcher row whose title is want, or fails.
func switcherIndex(t *testing.T, m *OS, want string) int {
	t.Helper()
	for i, n := range m.SessionSwitcherItems {
		if n.Title == want {
			return i
		}
	}
	t.Fatalf("the switcher does not list %q: %+v", want, m.SessionSwitcherItems)
	return -1
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
//
// Negative control: listing BuildSessionTree().Sessions as it was puts the
// local header in the list and this fails.
func TestSwitcherNeverListsAMachineHeader(t *testing.T) {
	for _, away := range []bool{false, true} {
		m := hostsOS(t, away)
		for _, n := range m.sessionSwitcherItems() {
			if n.Kind == sessiontree.KindHost {
				t.Errorf("ASSERTION: away=%v: the switcher lists the machine header %q as a session", away, n.Title)
			}
		}
	}
}

// TestSwitcherClickReachesThisMachineFromARemoteSession is the report: on a
// remote session, a click on this machine's session in the switcher.
//
// Negative control: restoring SwitchToSession(selected.ID) in
// sessionSwitcherActivate sends no call through the hook and this fails.
func TestSwitcherClickReachesThisMachineFromARemoteSession(t *testing.T) {
	m := hostsOS(t, true)
	calls := recordSwitches(m)
	m.SessionSwitcherItems = m.sessionSwitcherItems()

	m.sessionSwitcherActivate(switcherIndex(t, m, "home"))
	wantOneSwitch(t, *calls, switchCall{federation.LocalHostName, "home"})
}

// TestSwitcherClickReachesARemoteSessionFromHere is the follow-up comment: on
// this machine, a click on a session under build.
func TestSwitcherClickReachesARemoteSessionFromHere(t *testing.T) {
	m := hostsOS(t, false)
	calls := recordSwitches(m)
	m.SessionSwitcherItems = m.sessionSwitcherItems()

	m.sessionSwitcherActivate(switcherIndex(t, m, "api"))
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
	m.SessionSwitcherItems = m.sessionSwitcherItems()

	m.sessionSwitcherActivate(switcherIndex(t, m, "train"))
	wantOneSwitch(t, *calls, switchCall{"gpu", "train"})
}

// TestSwitcherLocalSessionStaysOnTheConnection: a session on the attached
// machine is switched over the current connection, by its own name.
func TestSwitcherLocalSessionStaysOnTheConnection(t *testing.T) {
	m := hostsOS(t, false)
	calls := recordSwitches(m)
	m.SessionSwitcherItems = []sessiontree.Node{
		sessiontree.BuildSession(sessiontree.SessionInput{Name: "home", IsCurrent: true}),
		sessiontree.BuildSession(sessiontree.SessionInput{Name: "notes"}),
	}

	m.sessionSwitcherActivate(1)
	wantOneSwitch(t, *calls, switchCall{"", "notes"})
}

// TestSwitcherToADownHostSaysUnavailable: a machine whose link is not up is
// not dialed, and the user is told.
func TestSwitcherToADownHostSaysUnavailable(t *testing.T) {
	m := hostsOS(t, false)
	m.FederationHosts[1].Status = string(federation.StatusReconnecting)
	calls := recordSwitches(m)
	m.SessionSwitcherItems = m.sessionSwitcherItems()

	m.sessionSwitcherActivate(switcherIndex(t, m, "api"))
	if len(*calls) != 0 {
		t.Fatalf("ASSERTION: a switch to a down host was attempted: %+v", *calls)
	}
	if len(m.Notifications) == 0 || m.Notifications[len(m.Notifications)-1].Message != "build is unavailable" {
		t.Fatalf("ASSERTION: the switch did not say build is unavailable: %+v", m.Notifications)
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

// TestSwitcherRowNamesTheMachine: a row under another machine reads as its
// name there and its machine, never as the rail identity.
func TestSwitcherRowNamesTheMachine(t *testing.T) {
	m := hostsOS(t, true)
	m.Settings = config.Global
	var node sessiontree.Node
	for _, n := range m.sessionSwitcherItems() {
		if n.Title == "home" {
			node = n
		}
	}
	pal := theme.UI()
	plain := ansi.Strip(m.sessionSwitcherRow(node, false, nil, pal, 58))
	if !strings.Contains(plain, "home @ local") {
		t.Errorf("ASSERTION: the row does not name the machine: %q", plain)
	}
	if strings.Contains(plain, "host/") {
		t.Errorf("ASSERTION: the row shows the rail identity: %q", plain)
	}
}

// TestSwitcherSearchFindsTheShownLabel: typing the label a remote row shows
// finds that row, and Enter on it switches to it. Matching only the title and
// the rail identity found nothing for "api @ build", and Enter then created a
// local session with that name.
//
// Negative control: matching item.Title and item.ID only, as before, leaves
// the filtered list empty and this fails.
func TestSwitcherSearchFindsTheShownLabel(t *testing.T) {
	m := hostsOS(t, false)
	calls := recordSwitches(m)
	m.SessionSwitcherItems = m.sessionSwitcherItems()

	for _, q := range []string{"api @ build", "API @ BUILD", "build"} {
		got := FilterSessionItems(m.SessionSwitcherItems, q)
		if len(got) != 1 || got[0].Title != "api" || got[0].Host != "build" {
			t.Fatalf("ASSERTION: %q found %+v, want only api on build", q, got)
		}
	}

	m.SessionSwitcherQuery = "api @ build"
	m.sessionSwitcherActivate(0)
	wantOneSwitch(t, *calls, switchCall{"build", "api"})
}

// TestSwitcherSearchIgnoresTheRailIdentity: the hidden rail identity is not
// something a person typed, so its pieces must not match every remote row.
func TestSwitcherSearchIgnoresTheRailIdentity(t *testing.T) {
	m := hostsOS(t, false)
	items := m.sessionSwitcherItems()
	for _, q := range []string{"host", "/", ":", "\x00"} {
		for _, n := range FilterSessionItems(items, q) {
			if n.Host != "" {
				t.Errorf("ASSERTION: %q matched the remote row %q through its rail identity", q, n.Title)
			}
		}
	}
}

// TestSwitcherSearchPutsAnExactLabelFirst: when one label contains another,
// typing the shorter one exactly selects its row, not the longer one listed
// before it.
func TestSwitcherSearchPutsAnExactLabelFirst(t *testing.T) {
	items := []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: hostNodeID("buildbox") + ":api", Title: "api", Host: "buildbox"},
		{Kind: sessiontree.KindSession, ID: hostNodeID("build") + ":api", Title: "api", Host: "build"},
	}
	got := FilterSessionItems(items, "api @ build")
	if len(got) != 2 || got[0].Host != "build" {
		t.Fatalf("ASSERTION: the exact label is not first: %+v", got)
	}
}

// TestSwitcherHintsDropRenameAndDeleteOnARemoteRow: the footer offers only
// what the selected row can do.
func TestSwitcherHintsDropRenameAndDeleteOnARemoteRow(t *testing.T) {
	m := hostsOS(t, false)
	items := m.sessionSwitcherItems()
	keys := func(sel int) string {
		var out []string
		for _, h := range sessionSwitcherHints(items, sel) {
			out = append(out, h.Key)
		}
		return strings.Join(out, " ")
	}
	local, remote := switcherIndexIn(t, items, "home"), switcherIndexIn(t, items, "api")
	if k := keys(local); !strings.Contains(k, "ctrl+r") || !strings.Contains(k, "ctrl+d") {
		t.Errorf("ASSERTION: a local row lost its rename and delete hints: %q", k)
	}
	if k := keys(remote); strings.Contains(k, "ctrl+r") || strings.Contains(k, "ctrl+d") {
		t.Errorf("ASSERTION: a remote row offers rename or delete: %q", k)
	}
}

// TestSwitcherMutesADownHostsRows: a row under a machine that is not up is
// drawn muted and without its agent glyph, as the rail draws it.
func TestSwitcherMutesADownHostsRows(t *testing.T) {
	m := hostsOS(t, false)
	m.Settings = config.Global
	node := sessiontree.Node{Kind: sessiontree.KindSession, ID: hostNodeID("build") + ":api",
		Title: "api", Host: "build", WindowCount: 1, AgentState: "needs_input"}
	pal := theme.UI()

	up := m.sessionSwitcherRow(node, false, nil, pal, 58)
	m.FederationHosts[1].Status = string(federation.StatusReconnecting)
	down := m.sessionSwitcherRow(node, false, nil, pal, 58)

	glyph := agentStateIndicator(sidebarGlyphState(node.AgentState, false))
	if glyph == "" || !strings.Contains(ansi.Strip(up), glyph) {
		t.Fatalf("the up row has no agent glyph %q to lose, so this proves nothing: %q", glyph, ansi.Strip(up))
	}
	if strings.Contains(ansi.Strip(down), glyph) {
		t.Errorf("ASSERTION: a down host's row still wears its agent glyph: %q", ansi.Strip(down))
	}
	if up == down {
		t.Errorf("ASSERTION: a down host's row is drawn the same as an up host's")
	}
}

func switcherIndexIn(t *testing.T, items []sessiontree.Node, title string) int {
	t.Helper()
	for i, n := range items {
		if n.Title == title {
			return i
		}
	}
	t.Fatalf("no row %q in %+v", title, items)
	return -1
}
