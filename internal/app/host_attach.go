package app

import (
	"errors"
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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

// AttachedHostLabel is the host qualifier shown beside the session name, or ""
// on this machine.
func (m *OS) AttachedHostLabel() string {
	return m.AttachedHost
}

// clientCapabilities is this client's terminal, as the daemon wants it told.
func (m *OS) clientCapabilities() *session.ClientCapabilities {
	caps := m.hostCaps()
	if caps == nil {
		return nil
	}
	return &session.ClientCapabilities{
		PixelWidth:     caps.PixelWidth,
		PixelHeight:    caps.PixelHeight,
		CellWidth:      caps.CellWidth,
		CellHeight:     caps.CellHeight,
		KittyGraphics:  caps.KittyGraphics,
		KittyAnimation: caps.KittyAnimation,
		SixelGraphics:  caps.SixelGraphics,
		TerminalName:   caps.TerminalName,
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

// recoverFromHostLoss runs when the connection through a host ends under this
// client. The session on the host keeps running; that is the point of the
// daemon there. What this client does is come back to the session on this
// machine it left, when there is one, and say what happened. It reports
// whether the client is still on screen.
func (m *OS) recoverFromHostLoss(cause error) bool {
	lost := m.AttachedHost
	back := m.hostReturn
	m.LogWarn("The connection to %s ended: %v", lost, cause)
	if back == "" {
		return false
	}
	if err := m.SwitchToHostSession(federation.LocalHostName, back, false); err != nil {
		m.LogWarn("Could not return to %q: %v", back, err)
		return false
	}
	m.ShowNotification(fmt.Sprintf("The link to %s closed. The session keeps running there.", lost),
		"warning", m.Settings.NotificationWarningDuration*2)
	return true
}

// hostAttachRefusal turns an attach error into the sentence the rail shows.
func hostAttachRefusal(host string, err error) string {
	var hostErr *session.HostConnectError
	if errors.As(err, &hostErr) {
		return hostErr.Message
	}
	var shake *session.HostHandshakeError
	if errors.As(err, &shake) {
		return shake.Error() + " Run 'tuios attach --host " + host + " NAME --ssh' to open it over ssh instead."
	}
	return err.Error()
}
