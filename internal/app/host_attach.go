package app

import (
	"errors"
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// A session on another machine, attached by this client.
//
// The client keeps one connection to one daemon at a time. Switching to a
// session on host "build" replaces that connection with one to build's daemon,
// opened through this machine's daemon over its link, and from then on every
// pane, keystroke and state push on screen is build's session drawn by this
// client with this machine's theme, config and prefix key. Switching back to a
// session here replaces the connection again. Nothing is nested and nothing
// runs ssh in a pane; that path still exists as `tuios attach --host X --ssh`
// for a machine whose tuios cannot serve this client's attach protocol.
//
// The rail keeps showing this machine's sessions while the client is away:
// they arrive in the same host listing the other machines' sessions do, and
// are drawn as a host group named local.

// SwitchToHostSession attaches a session on host in place of the current one.
// host may be federation.LocalHostName to come back to this machine. With
// create set, an empty name picks a free name on the host.
//
// The new connection is made and the attach taken before anything on screen
// is given up, so a host that refuses costs the user the switch and nothing
// else. Once it has succeeded the old connection is closed silently: its
// session keeps running where it is.
func (m *OS) SwitchToHostSession(host, name string, create bool) error {
	if m.DaemonClient == nil {
		return fmt.Errorf("not in daemon mode")
	}
	if host == "" || host == federation.LocalHostName {
		host = federation.LocalHostName
	}
	if host == m.AttachedHost && name != "" && !create {
		return m.SwitchToSession(name)
	}

	client := session.NewTUIClient()
	client.SetOwnLayoutReserve(m.DaemonClient.OwnLayoutReserve())
	version := m.DaemonClient.ClientVersion()
	width, height := m.Width, m.Height
	caps := m.clientCapabilities()
	var err error
	if host == federation.LocalHostName {
		err = client.ConnectWithCapabilities(version, width, height, caps)
	} else {
		_, err = client.ConnectThroughHost(host, version, width, height, caps)
	}
	if err != nil {
		return err
	}
	if name == "" && create {
		name = freeSessionName(client.AvailableSessionNames())
	}
	state, err := client.AttachSession(name, create, width, height)
	if err != nil {
		_ = client.Close()
		if host == federation.LocalHostName {
			return fmt.Errorf("could not attach %q on this machine: %w", name, err)
		}
		return fmt.Errorf("tuios on %s could not attach %q: %w", host, name, err)
	}
	client.StartReadLoop()

	previousHost, previousSession := m.AttachedHost, m.SessionName
	m.adoptClient(client, state, host)
	if host != federation.LocalHostName && previousHost == "" {
		// Remember where to come back to if the link drops.
		m.hostReturn = previousSession
	}
	if host == federation.LocalHostName {
		m.hostReturn = ""
	}
	m.LogInfo("Attached %q on %s", m.SessionName, host)
	return nil
}

// adoptClient makes client the connection this model drives, tearing the old
// one down without letting its disconnect be heard, and rebuilds the screen
// from state. It is the host-crossing half of SwitchToSession.
func (m *OS) adoptClient(client *session.TUIClient, state *session.SessionState, host string) {
	// A switch the user asked for ends any attempt to get a lost link back.
	// The two are different events, and a dial that lands after this must not
	// pull the user off the session they just chose.
	m.hostReconnect = nil
	m.hostReconnectGen++

	old := m.DaemonClient
	if old != nil {
		// Its disconnect is this switch, not a loss, so nothing must hear it.
		m.UnwireDaemonClient(old)
		// Every pane's stream ends here; rebuildForSession closes the panes.
		_ = old.Detach()
		_ = old.Close()
	}

	savedWidth, savedHeight := m.Width, m.Height
	m.DaemonClient = client
	m.WireDaemonClient(client)
	m.AttachedHost = host
	if host == federation.LocalHostName {
		m.AttachedHost = ""
	}
	m.SessionName = client.SessionName()
	m.rebuildForSessionOn(state, savedWidth, savedHeight)
	m.SyncDockContext()
	m.resetAgentMail()
	m.QueueClientEvent(ClientEvent{Type: "agent-mail-load"})
	m.MarkAllDirty()
	if m.AttachedHost != "" {
		m.ShowNotification("Session: "+m.SessionName+" @ "+m.AttachedHost, "success", m.Settings.NotificationDuration)
	} else {
		m.ShowNotification("Session: "+m.SessionName, "success", m.Settings.NotificationDuration)
	}
	m.FireAttached()
}

// rebuildForSessionOn is rebuildForSession for a connection that is not the
// one the panes were subscribed on. The old connection is already closed, so
// the panes are closed without unsubscribing them from a daemon that no
// longer hears this client.
func (m *OS) rebuildForSessionOn(state *session.SessionState, savedWidth, savedHeight int) {
	for _, w := range m.Windows {
		w.Close()
	}
	m.Windows = nil
	m.rebuildForSession(state, savedWidth, savedHeight)
}

// clientCapabilities is this client's terminal, as the daemon wants it told.
func (m *OS) clientCapabilities() *session.ClientCapabilities {
	return ClientCapabilitiesOf(m.hostCaps())
}

// ClientCapabilitiesOf is a host terminal's capabilities in the form the hello
// hands them to the daemon. It returns nil for nil.
func ClientCapabilitiesOf(caps *HostCapabilities) *session.ClientCapabilities {
	if caps == nil {
		return nil
	}
	return &session.ClientCapabilities{
		PixelWidth:    caps.PixelWidth,
		PixelHeight:   caps.PixelHeight,
		CellWidth:     caps.CellWidth,
		CellHeight:    caps.CellHeight,
		KittyGraphics: caps.KittyGraphics,
		SixelGraphics: caps.SixelGraphics,
		TerminalName:  caps.TerminalName,
	}
}

// freeSessionName is the first session-N a daemon does not hold.
func freeSessionName(taken []string) string {
	have := make(map[string]bool, len(taken))
	for _, n := range taken {
		have[n] = true
	}
	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if !have[name] {
			return name
		}
	}
}

// hostAttachRefusal turns an attach error into the sentence the rail shows.
func hostAttachRefusal(host string, err error) string {
	if hostErr, ok := errors.AsType[*session.HostConnectError](err); ok {
		return hostErr.Message
	}
	if shake, ok := errors.AsType[*session.HostHandshakeError](err); ok {
		return shake.Error() + " Run 'tuios attach --host " + host + " NAME --ssh' to open it over ssh instead."
	}
	return err.Error()
}

// Switching sessions by where they live.
//
// A session is addressed by two things: the machine that holds it and its name
// on that machine's daemon. The tree the rail, the switcher and the palette
// read carries both, but a row under another machine's group folds them into
// a rail identity (hostNodeID(host)+":"+name), which is no session's name.
// Handing that identity to SwitchToSession asks the daemon the client is
// connected to right now for a session literally called "\x00host/local:home".
// That was #196: from a remote session the switcher could not get back here,
// and from here it could not reach a remote session.
//
// Every switch that starts from a tree row or a rail hit goes through
// switchSession, which picks the connection first and the session second.

// hostUnavailableError is a switch aimed at a machine whose link is not up.
// Nothing was attempted, so it is reported as a warning.
type hostUnavailableError struct{ host string }

func (e *hostUnavailableError) Error() string { return e.host + " is unavailable" }

// hostSwitchError is a switch the other machine refused. Its message is the
// sentence hostAttachRefusal builds, and it is shown as it is.
type hostSwitchError struct{ msg string }

func (e *hostSwitchError) Error() string { return e.msg }

// sessionNodeTarget splits a session tree node into the machine that holds it
// and its name there. host is "" for a session on the attached machine, which
// is reached over the current connection. ok is false for a node that is not
// a session: a machine's header or a repository group.
func sessionNodeTarget(n sessiontree.Node) (host, name string, ok bool) {
	if n.Kind != sessiontree.KindSession {
		return "", "", false
	}
	if n.Host != "" {
		name = remoteSessionName(n)
		return n.Host, name, name != ""
	}
	return "", n.ID, n.ID != ""
}

// switchSession attaches the session name on host. An empty host, or the
// machine the client is already attached to, switches over the current
// connection. Any other host, this machine included while the client is away,
// replaces the connection.
func (m *OS) switchSession(host, name string) error {
	if host == m.attachedMachine() {
		host = ""
	}
	if host != "" && !m.hostIsUp(host) {
		return &hostUnavailableError{host: host}
	}
	if m.sessionSwitchHook != nil {
		return m.sessionSwitchHook(host, name)
	}
	if host == "" {
		return m.SwitchToSession(name)
	}
	if err := m.SwitchToHostSession(host, name, false); err != nil {
		return &hostSwitchError{msg: hostAttachRefusal(host, err)}
	}
	return nil
}

// openSession is switchSession for a surface: it reports a failure itself and
// says whether the client is now on the session.
func (m *OS) openSession(host, name string) bool {
	err := m.switchSession(host, name)
	if err == nil {
		return true
	}
	m.reportSwitchFailure(err)
	return false
}

// OpenSessionNode switches to the session a tree node names, on whichever
// machine holds it, and reports a failure itself. The switcher and the
// palette reach openSession through it. A node that is not a session opens
// nothing; no surface lists one, since the switcher drops machine headers.
func (m *OS) OpenSessionNode(n sessiontree.Node) bool {
	host, name, ok := sessionNodeTarget(n)
	if !ok {
		return false
	}
	return m.openSession(host, name)
}

// SetSessionSwitchHookForTest makes switchSession hand the machine and name it
// resolved to fn instead of connecting. Tests outside this package use it to
// see where a surface sends a switch. Production code never calls it.
func (m *OS) SetSessionSwitchHookForTest(fn func(host, name string) error) {
	m.sessionSwitchHook = fn
}

// reportSwitchFailure shows why a switch did not happen.
func (m *OS) reportSwitchFailure(err error) {
	if unavailable, ok := errors.AsType[*hostUnavailableError](err); ok {
		m.ShowNotification(unavailable.Error(), "warning", m.Settings.NotificationWarningDuration)
		return
	}
	if refused, ok := errors.AsType[*hostSwitchError](err); ok {
		m.ShowNotification(refused.Error(), "error", m.Settings.NotificationDuration*3)
		return
	}
	m.ShowNotification("Switch failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
}
