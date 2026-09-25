package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The policy for getting a lost link back, pinned where it can be read.
//
// The behaviour these hold is the one the maintainer asked for: a link that
// drops is dialed again without him asking, the pane he was working in stays on
// screen while that happens, and a failure that trying again cannot fix stops
// at once with the reason rather than being retried until the budget runs out.

// TestTheBudgetOutlastsWhatItWaitsOn is the number that decides whether the
// client gives up too early.
//
// This client dials the host through the daemon on its own machine, and that
// daemon only answers once its own link is back. Under that sit two waits it
// does not control: the daemon's redial backoff, and ssh's keepalives, which
// take their own window to notice a dead path at all. A budget under their sum
// would give up while the machinery below it was still working.
func TestTheBudgetOutlastsWhatItWaitsOn(t *testing.T) {
	var opts federation.Options
	daemonBackoff := 60 * time.Second
	if got := opts; got.MaxBackoff != 0 {
		daemonBackoff = got.MaxBackoff
	}
	floor := daemonBackoff + federation.KeepaliveWindow
	if hostReconnectBudget <= floor {
		t.Errorf("ASSERTION: the client gives up after %v, and the daemon under it can take %v to get the link back. It stops trying while the link is still coming",
			hostReconnectBudget, floor)
	}
}

// TestAFailureThatCannotBeFixedByTryingIsNotTried is the other half of the
// rule. Retrying a permission error, an unknown host or a session that no
// longer exists for three minutes teaches the user nothing and hides what is
// actually wrong.
func TestAFailureThatCannotBeFixedByTryingIsNotTried(t *testing.T) {
	final := map[string]error{
		"an unknown host":        &session.HostConnectError{Host: "oci", Code: session.ErrVerbUnknownHost, Message: "no such host"},
		"a local daemon too old": &session.HostConnectError{Host: "oci", Code: session.ErrVerbProtocolMismatch, Message: "too old"},
		"a remote tuios that cannot serve this client": &session.HostHandshakeError{
			Host: "oci", Err: &session.ProtocolMismatchError{},
		},
	}
	for what, err := range final {
		if !hostReconnectFinal(err) {
			t.Errorf("ASSERTION: %s is retried until the budget runs out, so the person waits three minutes to be told something that was true at once", what)
		}
		if r := hostReconnectReason("oci", err); !strings.Contains(r, "oci") && !strings.Contains(r, "machine") {
			t.Errorf("%s reads as %q, which names neither the machine nor what to do", what, r)
		}
	}

	retry := map[string]error{
		"a host that is not answering yet": &session.HostConnectError{Host: "oci", Code: "host_unreachable", Message: "down"},
		"a link with no room right now":    &session.HostConnectError{Host: "oci", Code: "host_refused", Message: "full"},
		"no answer at all":                 errors.New("dial unix: connection refused"),
	}
	for what, err := range retry {
		if hostReconnectFinal(err) {
			t.Errorf("ASSERTION: %s stops the client trying, so a link that was about to come back is given up on", what)
		}
	}
}

// TestALostLinkKeepsThePaneOnScreen is the rude part of the failure, in a test.
//
// The client used to close every pane and put the person back on their own
// machine the instant the pipe ended. The panes are a picture of a session that
// is still running, and they stay until there is something better to draw.
func TestALostLinkKeepsThePaneOnScreen(t *testing.T) {
	win := newTestWindow(t, "recon-0001", 40, 10)
	m := newTestOS(win)
	m.Settings = config.Global
	m.AttachedHost = "oci"
	m.SessionName = "work"
	m.hostReturn = "home"
	m.Width, m.Height = 80, 24

	cmd := m.beginHostReconnect(errors.New("the pipe closed"))
	if cmd == nil {
		t.Fatal("ASSERTION: a dropped link started no attempt to get it back")
	}
	if !m.ReconnectingToHost() {
		t.Fatal("ASSERTION: the client is not marked as reconnecting")
	}
	if len(m.Windows) != 1 {
		t.Errorf("ASSERTION: the panes were closed when the link dropped; the person lost the frame they were reading. %d windows left", len(m.Windows))
	}
	if m.AttachedHost != "oci" {
		t.Errorf("ASSERTION: the client left the host session while the link was still being dialed, host is now %q", m.AttachedHost)
	}
	if note := m.hostLinkNote(); !strings.Contains(note, "oci") {
		t.Errorf("ASSERTION: the dock says nothing about the link being dialed again: %q", note)
	}

	// The dead connection reporting itself a second time must not restart the
	// clock or start a second run of dials.
	first := m.hostReconnect
	if cmd := m.beginHostReconnect(errors.New("again")); cmd != nil {
		t.Error("ASSERTION: a second disconnect started a second run of dials")
	}
	if m.hostReconnect != first {
		t.Error("ASSERTION: a second disconnect replaced the attempt already running")
	}
}

// TestASwitchTheUserAskedForEndsTheReconnect is the "a reconnect must not
// resurrect a session the person left" rule. Detaching on purpose and losing a
// link are different events, and a dial that lands after the user has moved on
// must not pull them back.
func TestASwitchTheUserAskedForEndsTheReconnect(t *testing.T) {
	win := newTestWindow(t, "recon-0002", 40, 10)
	m := newTestOS(win)
	m.Settings = config.Global
	m.AttachedHost = "oci"
	m.SessionName = "work"
	_ = m.beginHostReconnect(errors.New("the pipe closed"))
	stale := m.hostReconnect.gen

	// The shipped path a deliberate switch runs through.
	m.WorkspaceTrees = map[int]*layout.BSPTree{}
	m.WorkspaceScrollingLayouts = map[int]*layout.ScrollingLayout{}
	m.WindowToBSPID = map[string]int{}
	m.BSPIDToWindowID = map[int]string{}
	m.SubscribedPTYs = map[string]bool{}
	m.Windows = nil
	m.adoptClient(session.NewTUIClient(), nil, "local")

	if m.ReconnectingToHost() {
		t.Error("ASSERTION: a session the user chose still has a reconnect running against the one they left")
	}
	// A dial that finishes now belongs to nobody and must be dropped.
	if cmd := m.handleHostReconnectResult(hostReconnectResultMsg{gen: stale}); cmd != nil {
		t.Error("ASSERTION: a dial from the abandoned attempt was applied to the session the user switched to")
	}
	if cmd := m.handleHostReconnectTick(hostReconnectTickMsg{gen: stale}); cmd != nil {
		t.Error("ASSERTION: the abandoned attempt kept dialing after the user switched away")
	}
}

var _ = terminal.Window{}

// TestTheSamePanesAreKeptAcrossAReconnect is what makes the return cheap and
// what keeps a scrolled view where it was. A session whose panes did not change
// is resumed on the panes already drawn, so each one asks the daemon only for
// the rows it does not hold and keeps the scroll anchor it was carrying.
func TestTheSamePanesAreKeptAcrossAReconnect(t *testing.T) {
	win := newTestWindow(t, "recon-0003", 40, 10)
	m := newTestOS(win)

	same := &session.SessionState{Windows: []session.WindowState{{ID: "recon-0003", PTYID: win.PTYID}}}
	if !m.samePaneSet(same) {
		t.Errorf("ASSERTION: an unchanged session is rebuilt from scratch, which throws away every pane's history and its scroll position")
	}

	changed := &session.SessionState{Windows: []session.WindowState{
		{ID: "recon-0003", PTYID: win.PTYID},
		{ID: "recon-0004", PTYID: "pty-recon-0004"},
	}}
	if m.samePaneSet(changed) {
		t.Error("ASSERTION: a session that gained a pane while the link was down is resumed on the old panes, so the new one is never drawn")
	}
	if m.samePaneSet(nil) {
		t.Error("ASSERTION: an attach that returned no state is treated as an unchanged session")
	}
}
