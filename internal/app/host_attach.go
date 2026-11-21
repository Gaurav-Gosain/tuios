package app

import (
	"errors"
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

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
		return errfmt.Errorf("not in daemon mode")
	}
	m.cancelHostOpen()
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

type hostDial struct {
	host, name       string
	cwd              string
	create, global   bool
	reserve          session.LayoutReserve
	version          string
	width, height    int
	caps             *session.ClientCapabilities
	sshAuthSock      string
	term, colorTerm  string
	kind             string
	served, viewOnly bool
	upgradeDialog    bool
}

func (m *OS) hostDialFor(host, name string, create, global bool) hostDial {
	return hostDial{
		host: host, name: name, create: create, global: global,
		reserve:     m.DaemonClient.OwnLayoutReserve(),
		version:     m.DaemonClient.ClientVersion(),
		sshAuthSock: m.DaemonClient.SSHAuthSock,
		term:        m.DaemonClient.Term, colorTerm: m.DaemonClient.ColorTerm,
		kind:          m.DaemonClient.Kind,
		served:        m.DaemonClient.Served,
		viewOnly:      m.DaemonClient.ViewOnly,
		upgradeDialog: m.DaemonClient.UpgradeDialog,
		width:         m.Width, height: m.Height,
		caps: m.clientCapabilities(),
	}
}

func (d hostDial) open() (*session.TUIClient, *session.SessionState, error) {
	client := session.NewTUIClient()
	client.SetOwnLayoutReserve(d.reserve)
	client.StartDir = d.cwd
	client.SSHAuthSock = d.sshAuthSock
	client.Term, client.ColorTerm = d.term, d.colorTerm
	client.Kind, client.Served, client.ViewOnly = d.kind, d.served, d.viewOnly
	client.UpgradeDialog = d.upgradeDialog
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
			return nil, nil, errfmt.Errorf("could not attach %q on this machine: %w", name, err)
		}
		return nil, nil, errfmt.Errorf("tuios on %s could not attach %q: %w", d.host, name, err)
	}
	client.StartReadLoop()
	return client, state, nil
}

func (m *OS) applyStartupToUnarranged() {
	if m.sessionUnarranged {
		m.applyStartupTiling()
	}
}

func (m *OS) switchToHostAsync(host, name string, create bool, cwd, requestID string) error {
	if m.DaemonClient == nil {
		return errfmt.Errorf("not in daemon mode")
	}
	if !m.hostIsUp(host) {
		return &hostUnavailableError{host: host}
	}
	m.cancelHostOpen()
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

func (m *OS) finishHostSwitch(msg SessionCreatedMsg) {
	if msg.Err != nil {
		refusal := &hostSwitchError{msg: hostAttachRefusal(msg.Host, msg.Err)}
		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, refusal.Error())
		}
		m.reportSwitchFailure(refusal)
		return
	}
	if m.DaemonClient != nil && msg.RequestID != "" {
		_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "command executed")
	}
	m.clearSidebarReturn()
	m.adoptHostClient(msg.Client, msg.State, msg.Host)
	m.applyStartupToUnarranged()
	if msg.Create && len(m.Windows) == 0 {
		m.AddWindow("")
	}
}

func (m *OS) adoptHostClient(client *session.TUIClient, state *session.SessionState, host string) {
	previousHost, previousSession := m.AttachedHost, m.SessionName
	from := lastSession{host: m.attachedMachine(), name: previousSession}
	m.adoptClient(client, state, host)
	m.leftSession(from)
	if host != federation.LocalHostName && previousHost == "" {
		m.hostReturn = previousSession
	}
	if host == federation.LocalHostName {
		m.hostReturn = ""
	}
	m.LogInfo("Attached %q on %s", m.SessionName, host)
}

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

func (m *OS) adoptClient(client *session.TUIClient, state *session.SessionState, host string) {
	m.hostReconnect = nil
	m.hostReconnectGen++
	m.cancelHostOpen()
	m.hostReconnectDeferred = false

	old := m.DaemonClient
	if old != nil {
		m.UnwireDaemonClient(old)
		_ = old.Detach()
		_ = old.Close()
	}

	savedWidth, savedHeight := m.Width, m.Height
	m.DaemonClient = client
	m.WireDaemonClient(client)
	leftHost := m.AttachedHost
	m.AttachedHost = host
	if host == federation.LocalHostName {
		m.AttachedHost = ""
	}
	if m.AttachedHost != leftHost {
		m.QueueClientEvent(ClientEvent{Type: "inbox-rewatch"})
	}
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

func (m *OS) rebuildForSessionOn(state *session.SessionState, savedWidth, savedHeight int) {
	for _, w := range m.Windows {
		w.Close()
	}
	m.Windows = nil
	m.rebuildForSession(state, savedWidth, savedHeight)
}

func (m *OS) clientCapabilities() *session.ClientCapabilities {
	return ClientCapabilitiesOf(m.hostCaps(), m.Settings.ImageSymbols)
}

// ClientCapabilitiesOf is a host terminal's capabilities in the form the hello
// hands them to the daemon. imageSymbols is appearance.image_symbols, which
// decides whether this client draws a pane's images as block glyphs: the
// hello is built before any OS exists, so the caller passes it. It returns nil
// for nil.
func ClientCapabilitiesOf(caps *HostCapabilities, imageSymbols string) *session.ClientCapabilities {
	if caps == nil {
		return nil
	}
	return &session.ClientCapabilities{
		PixelWidth:     caps.PixelWidth,
		PixelHeight:    caps.PixelHeight,
		CellWidth:      caps.CellWidth,
		CellHeight:     caps.CellHeight,
		KittyGraphics:  caps.KittyGraphics,
		SixelGraphics:  caps.SixelGraphics,
		TerminalName:   caps.TerminalName,
		KittyAnimation: caps.KittyAnimation,
		SymbolImages:   drawsSymbols(imageSymbols, caps),
		Term:           caps.Term,
	}
}

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

func hostAttachRefusal(host string, err error) string {
	if hostErr, ok := errors.AsType[*session.HostConnectError](err); ok {
		return hostErr.Message
	}
	if shake, ok := errors.AsType[*session.HostHandshakeError](err); ok {
		return shake.Error() + " Run 'tuios attach --host " + host + " NAME --ssh' to open it over ssh instead."
	}
	return err.Error()
}

type hostUnavailableError struct{ host string }

func (e *hostUnavailableError) Error() string { return e.host + " is unavailable" }

type hostSwitchError struct{ msg string }

func (e *hostSwitchError) Error() string { return e.msg }

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

type lastSession struct {
	host, name string
}

func (m *OS) leftSession(from lastSession) {
	m.lastWorkspace = 0
	if from.name == "" || (from.host == m.attachedMachine() && from.name == m.SessionName) {
		return
	}
	m.rememberShownOn(from.host, from.name)
	m.lastSession = from
}

// LastSession goes back to the session this client was on before the current
// one, as tmux's switch-client -l does.
func (m *OS) LastSession() {
	if m.lastSession.name == "" {
		m.ShowNotification("No last session", "info", m.Settings.NotificationDuration)
		return
	}
	m.openSession(m.lastSession.host, m.lastSession.name)
}

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
// It is go-to's Enter on a session name that matches no row, and it
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
	if host != "" && host != m.attachedMachine() {
		m.openRemoteSession(host, name)
		return true
	}
	return m.openSession(host, name)
}

// SetSessionSwitchHookForTest makes switchSession hand the machine and name it
// resolved to fn instead of connecting. Tests outside this package use it to
// see where a surface sends a switch. Production code never calls it.
func (m *OS) SetSessionSwitchHookForTest(fn func(host, name string) error) {
	m.sessionSwitchHook = fn
}

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
