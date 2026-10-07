package app

import (
	"errors"
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	d := m.hostDialFor(host, name, create, false)
	client, state, err := d.open()
	if err != nil {
		return err
	}
	m.adoptHostClient(client, state, host)
	return nil
}

// hostDial is everything opening a session on a machine needs from the model,
// taken on the UI goroutine so open can run off it.
type hostDial struct {
	host, name string
	// cwd is the start directory of a session the attach creates. See
	// session.AttachPayload.Cwd.
	cwd            string
	create, global bool
	reserve        session.LayoutReserve
	version        string
	width, height  int
	caps           *session.ClientCapabilities
	// sshAuthSock is the agent socket of the client the dial replaces. See
	// session.TUIClient.SSHAuthSock.
	sshAuthSock string
}

func (m *OS) hostDialFor(host, name string, create, global bool) hostDial {
	return hostDial{
		host: host, name: name, create: create, global: global,
		reserve:     m.DaemonClient.OwnLayoutReserve(),
		version:     m.DaemonClient.ClientVersion(),
		sshAuthSock: m.DaemonClient.SSHAuthSock,
		width:       m.Width, height: m.Height,
		caps: m.clientCapabilities(),
	}
}

// open connects to the machine's daemon and takes the attach. It touches no
// model state, so it may run on any goroutine. With create and no name it
// picks a free name there, and with global the session is a global one.
func (d hostDial) open() (*session.TUIClient, *session.SessionState, error) {
	client := session.NewTUIClient()
	client.SetOwnLayoutReserve(d.reserve)
	client.StartDir = d.cwd
	client.SSHAuthSock = d.sshAuthSock
	var err error
	if d.host == federation.LocalHostName {
		err = client.ConnectWithCapabilities(d.version, d.width, d.height, d.caps)
	} else {
		_, err = client.ConnectThroughHost(d.host, d.version, d.width, d.height, d.caps)
	}
	if err != nil {
		return nil, nil, err
	}
	name, create := d.name, d.create
	if name == "" && create {
		if d.global {
			name = freeGlobalSessionName(client.AvailableSessionNames())
		} else {
			name = freeSessionName(client.AvailableSessionNames())
		}
	}
	if d.global {
		// A global session is made empty and marked by the daemon, which an
		// attach that creates does not do. So it is made first and attached
		// as an existing session.
		if err := client.CreateGlobalSession(name, d.width, d.height); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
		create = false
	}
	state, err := client.AttachSession(name, create, d.width, d.height)
	if err != nil {
		_ = client.Close()
		if d.host == federation.LocalHostName {
			return nil, nil, fmt.Errorf("could not attach %q on this machine: %w", name, err)
		}
		return nil, nil, fmt.Errorf("tuios on %s could not attach %q: %w", d.host, name, err)
	}
	client.StartReadLoop()
	return client, state, nil
}

// applyStartupToUnarranged applies [startup] tiled to the session the client
// just switched to, when nobody has arranged it yet. A session the new-session
// verb or tuios new --detach made is one: the daemon built it with tiling off
// and no client has placed its windows. A session somebody laid out keeps the
// layout it has. See stateUnarranged.
//
// It is the one place a client applies [startup] after a switch or a create.
// Every path that lands the client on a session, new or not, ends here, so a
// session made from one surface cannot come up laid out differently from the
// same session made from another (#488).
func (m *OS) applyStartupToUnarranged() {
	if m.sessionUnarranged {
		m.applyStartupTiling()
	}
}

// switchToHostAsync is switch-session to a session on another machine. The
// connection and the attach run on a goroutine, as makeSessionHere's do, and
// finishHostSwitch adopts the result and answers requestID.
func (m *OS) switchToHostAsync(host, name string, create bool, cwd, requestID string) error {
	if m.DaemonClient == nil {
		return fmt.Errorf("not in daemon mode")
	}
	if !m.hostIsUp(host) {
		return &hostUnavailableError{host: host}
	}
	d := m.hostDialFor(host, name, create, false)
	d.cwd = cwd
	ch := m.sessionCreateChan()
	go func() {
		client, state, err := d.open()
		msg := SessionCreatedMsg{Err: err, Client: client, State: state, Host: host, RequestID: requestID, Switched: true, Create: create}
		if client != nil {
			msg.Name = client.SessionName()
		}
		ch <- msg
	}()
	return nil
}

// finishHostSwitch answers a switch-session to another machine and, when the
// attach there landed, adopts the connection.
func (m *OS) finishHostSwitch(msg SessionCreatedMsg) {
	if msg.Err != nil {
		refusal := &hostSwitchError{msg: hostAttachRefusal(msg.Host, msg.Err)}
		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, refusal.Error())
		}
		m.reportSwitchFailure(refusal)
		return
	}
	// The answer goes over the connection being left, so it goes before
	// adoptHostClient closes it.
	if m.DaemonClient != nil && msg.RequestID != "" {
		_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "command executed")
	}
	m.clearSidebarReturn()
	m.adoptHostClient(msg.Client, msg.State, msg.Host)
	// Tiling first, so the window below is tiled as it opens.
	m.applyStartupToUnarranged()
	// switch-session --create makes a session with one window on this
	// machine (the new-session verb). The attach that made this one made it
	// empty, so the window is opened here, in the session's start directory.
	if msg.Create && len(m.Windows) == 0 {
		m.AddWindow("")
	}
}

// adoptHostClient is the UI half of a switch across machines: the model takes
// the connection open made.
func (m *OS) adoptHostClient(client *session.TUIClient, state *session.SessionState, host string) {
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
}

// makeSessionHere makes a session on this machine's daemon and attaches it.
// With global set it is a global session.
//
// It is for a client attached to a session on another machine. That client's
// connection goes to the other machine's daemon, so a create sent over it
// makes the session there, and "this machine" in the picker would mean
// whichever machine the client last switched to.
//
// The connect, create and attach run on a goroutine, as SidebarNewSession's
// create does, so a slow daemon does not stop input and drawing. The result
// comes back as a SessionCreatedMsg carrying the new connection, and the
// handler adopts it there.
func (m *OS) makeSessionHere(global bool) {
	d := m.hostDialFor(federation.LocalHostName, "", true, global)
	ch := m.sessionCreateChan()
	go func() {
		client, state, err := d.open()
		msg := SessionCreatedMsg{Global: global, Err: err, Client: client, State: state}
		if client != nil {
			msg.Name = client.SessionName()
		}
		ch <- msg
	}()
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
	// The host list belongs to the machine left behind. The new machine's is
	// read now, so a window picker opened next has it.
	m.attachedHosts, m.attachedHostsLoaded = attachedHostView{}, false
	m.refreshAttachedHosts()
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
		// The daemon answers a frame edit a host cannot make, so the
		// refusal reaches the guest in order. See Session.SetKittyAnimation.
		KittyAnimation: caps.KittyAnimation,
		// Read from the process's settings: the hello is built before
		// any OS exists. See drawsSymbols.
		SymbolImages: drawsSymbols(config.Global.ImageSymbols, caps),
		Term:         caps.Term,
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
		m.applyStartupToUnarranged()
		return true
	}
	m.reportSwitchFailure(err)
	return false
}

// OpenOrCreateSession switches to the session name on the machine the client
// is attached to. The daemon makes the session when no session has the name.
// It is the session switcher's Enter on a name that matches no row, and it
// runs openSession, so [startup] applies to the new session as it does to
// every other switch. It reports a failure itself.
func (m *OS) OpenOrCreateSession(name string) bool {
	return m.openSession("", name)
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
