package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The machine picker: which machine a new window's process runs on.
//
// A session can hold windows whose processes are on different machines, and
// until now the only way to make one was `tuios new-window --host`. This is
// the way from inside the UI.
//
// It is a picker rather than a prompt because the answer is one of a known
// list, and a typed machine name is a typo waiting to become a twenty second
// link timeout. The list is fuzzy-filtered like every other list here.

// HostPickerItem is one machine the picker offers.
type HostPickerItem struct {
	// Name is the machine as the [hosts] table spells it, or "" for this one.
	Name string
	// Label is what the row shows.
	Label string
	// Detail is the quiet right-hand text: the link's state, or how many
	// sessions the machine holds.
	Detail string
	// Up is whether a window can actually be opened there. A machine that is
	// down is still listed, because hiding it would leave someone wondering
	// whether they had imagined configuring it, but it cannot be chosen.
	Up bool
	// Global marks the row that makes a global session rather than a session
	// on a machine. It is not a machine, and Name is empty for it, which is
	// why it needs a mark of its own rather than a reserved name.
	Global bool
}

// buildHostPickerItems is this machine followed by every configured one, in the
// order the rail lists them, so the picker and the rail agree.
func (m *OS) buildHostPickerItems() []HostPickerItem {
	items := []HostPickerItem{{
		Name:   "",
		Label:  "this machine",
		Detail: "local",
		Up:     true,
	}}
	// The rows name machines the way this machine's daemon does. A window is
	// made by the daemon that holds the session, and a session on another
	// machine has no link back here that this client knows of, so this
	// machine is listed and cannot be chosen. A session always can: it is
	// made on this machine's own daemon wherever the client is.
	if m.HostPickerPurpose == HostPickerNewWindow && m.AttachedHost != "" {
		items[0].Up = false
		items[0].Detail = "not from " + m.AttachedHost
	}
	// A global session is the answer to the same question, and the only place
	// the question is asked, so it is a row here rather than a second control
	// somewhere else. It is offered first because a session that will hold
	// panes from several machines is not a session on any of the rows below
	// it, and putting it among them would say it was.
	//
	// Only for a session. A window is made in the session you are already in,
	// and "global" is not a machine to run a process on.
	if m.HostPickerPurpose == HostPickerNewSession && m.GlobalSessionOffered() {
		items = append(items, HostPickerItem{
			Global: true,
			Label:  GlobalSessionName,
			Detail: "panes from any machine",
			Up:     true,
		})
	}
	for _, h := range m.FederationHosts {
		if h.Name == federation.LocalHostName {
			continue
		}
		up := h.Status == string(federation.StatusUp)
		detail := hostStatusLabel(h.Status)
		if up {
			detail = pluralSessions(len(h.Sessions))
		}
		items = append(items, HostPickerItem{
			Name:   h.Name,
			Label:  h.Name,
			Detail: detail,
			Up:     up,
		})
	}
	return items
}

// pluralSessions is the count a machine's row shows when its link is up.
func pluralSessions(n int) string {
	switch n {
	case 0:
		return "no sessions"
	case 1:
		return "1 session"
	default:
		return strconv.Itoa(n) + " sessions"
	}
}

// FilterHostPickerItems keeps the machines whose name matches the query, by the
// same subsequence match the other lists use.
func FilterHostPickerItems(items []HostPickerItem, query string) []HostPickerItem {
	if query == "" {
		return items
	}
	out := make([]HostPickerItem, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Label), strings.ToLower(query)) {
			out = append(out, it)
		}
	}
	return out
}

// OpenHostPicker shows the machine picker for a new window.
func (m *OS) OpenHostPicker() {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		m.ShowNotification("A window on another machine needs the daemon", "warning",
			m.Settings.NotificationWarningDuration)
		return
	}
	m.HostPickerPurpose = HostPickerNewWindow
	m.HostPickerItems = m.buildHostPickerItems()
	if len(m.HostPickerItems) <= 1 {
		// Only this machine. Offering a list of one is a dialog that asks a
		// question with one answer, so the window is simply made here.
		m.ShowNotification("No other machines are configured. Add one with 'tuios hosts add'",
			"info", m.Settings.NotificationDuration)
		return
	}
	m.HostPickerQuery = ""
	m.HostPickerSelected = 0
	m.HostPickerScroll = 0
	m.ShowHostPicker = true
}

// HostPickerPurpose is what the machine picker is being asked for. The list is
// the same either way, because the question is the same: which machine.
type HostPickerPurpose int

const (
	// HostPickerNewWindow makes a pane in this session, on the chosen machine.
	HostPickerNewWindow HostPickerPurpose = iota
	// HostPickerNewSession makes a session on the chosen machine and attaches
	// to it.
	HostPickerNewSession
)

// OpenNewSessionPicker asks which machine a new session should be made on.
//
// With one machine there is nothing to ask, so the session is simply made
// here. A picker with a single row is a dialog that asks a question with one
// answer, which is the same rule OpenHostPicker follows.
func (m *OS) OpenNewSessionPicker() {
	if m.learnOff(learnNoteSessions) {
		return
	}
	if !m.CanCreateSession() {
		m.ShowNotification("Sessions need the daemon", "info", m.Settings.NotificationDuration)
		return
	}
	if !m.newSessionShouldPickHost() {
		m.SidebarNewSession()
		return
	}
	m.HostPickerPurpose = HostPickerNewSession
	m.HostPickerItems = m.buildHostPickerItems()

	m.HostPickerQuery = ""
	m.HostPickerSelected = 0
	m.HostPickerScroll = 0
	m.ShowHostPicker = true
}

// newSessionShouldPickHost reports whether making a session is a question.
//
// It is the same rule as newWindowShouldPickHost minus the global session
// part: a session belongs to a machine, so the machine is worth asking about
// wherever you are, as soon as there is more than one to choose from.
func (m *OS) newSessionShouldPickHost() bool {
	if !m.CanCreateSession() {
		return false
	}
	return m.reachableMachines() > 1
}

// ChooseHostForNewSession makes a session on the chosen machine and attaches
// to it.
func (m *OS) ChooseHostForNewSession(item HostPickerItem) tea.Cmd {
	m.CloseHostPicker()
	if !item.Up {
		m.ShowNotification(item.Label+" is unavailable", "warning",
			m.Settings.NotificationWarningDuration)
		return nil
	}
	if item.Global {
		m.SidebarNewGlobalSession()
		return nil
	}
	if item.Name == "" || item.Name == federation.LocalHostName {
		m.SidebarNewSession()
		return nil
	}
	m.createRemoteSession(item.Name)
	return nil
}

// ChooseHost runs whatever the picker was opened for.
func (m *OS) ChooseHost(item HostPickerItem) tea.Cmd {
	if m.HostPickerPurpose == HostPickerNewSession {
		return m.ChooseHostForNewSession(item)
	}
	return m.ChooseHostForNewWindow(item)
}

// NewWindowOnHostMsg is the answer to asking for a window on another machine.
type NewWindowOnHostMsg struct {
	Host string
	Err  error
}

// ChooseHostForNewWindow creates a window whose process runs on the chosen
// machine and closes the picker.
//
// A machine that is not up is refused here rather than attempted, because the
// attempt is a link dial with a timeout on the end of it and the answer is
// already known.
//
// The row names a machine as this machine's daemon knows it, and the window
// is made by the daemon that holds the session, which is another machine's
// while the client is attached there. So the row is turned into a request to
// that daemon here: the machine it holds is a plain window on it, and any
// other machine is a window it opens over its own link.
//
// Whether the machine is up is asked again here. The list is a snapshot from
// when the picker opened, and a link that dropped since then would otherwise
// be dialled with a timeout on the end of it.
func (m *OS) ChooseHostForNewWindow(item HostPickerItem) tea.Cmd {
	m.ShowHostPicker = false
	m.HostPickerQuery = ""
	attached := m.AttachedHost // "" is this machine
	if item.Name == "" && attached != "" {
		m.CancelPendingSplit()
		m.ShowNotification(attached+" cannot open a pane on this machine. Open it from a session on this machine",
			"warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	if !item.Up || (item.Name != "" && !m.hostIsUp(item.Name)) {
		// The window is not being made, so a split waiting for it is not
		// happening either.
		m.CancelPendingSplit()
		m.ShowNotification(item.Label+" is unavailable", "warning",
			m.Settings.NotificationWarningDuration)
		return nil
	}
	if item.Name == attached {
		// The machine that holds the session: an ordinary window there.
		_ = m.CreateNewWindow()
		return nil
	}
	return m.newWindowOnHostCmd(item.Name)
}

// newWindowOnHostCmd asks the daemon for a window on host, off the UI
// goroutine.
//
// It goes over the verb plane rather than through AddWindow. AddWindow sends
// the NewWindow tape intent, whose arguments are positional (a name, then an
// argv) with nowhere to put a machine, and the verb already takes one and is
// already tested. The rail's own host poll reaches the daemon the same way.
//
// Off the UI goroutine because it is not fast: opening a pane on another
// machine dials a stream on the link and waits for that daemon to spawn a
// process, with a budget of twenty seconds when something is wrong. Doing that
// inline would freeze every pane on screen while it ran.
//
// Nothing here adds the window. The daemon creates it and pushes the session
// state, and this client adopts it the way it adopts a window any other client
// made. This only reports the failure, which is the half no push can carry.
//
// The call goes to the daemon that holds the session: this machine's, or the
// attached machine's through the link. It used to go to this machine's daemon
// always, so from a session on another machine the window was made in a
// session of the same name here, or not at all.
func (m *OS) newWindowOnHostCmd(host string) tea.Cmd {
	name := m.SessionName
	dial := m.sessionDaemonDialer()
	return func() tea.Msg {
		client, err := dial()
		if err != nil {
			return NewWindowOnHostMsg{Host: host, Err: err}
		}
		defer func() { _ = client.Close() }()
		_, err = client.CallWithTimeout("new-window", map[string]any{
			"session": name,
			"host":    host,
			"focus":   true,
		}, newWindowOnHostTimeout)
		return NewWindowOnHostMsg{Host: host, Err: err}
	}
}

// sessionDaemonDialer opens a verb connection to the daemon that holds the
// attached session: this machine's directly, or another machine's through the
// link. It is read on the UI goroutine and called off it.
func (m *OS) sessionDaemonDialer() func() (*session.VerbClient, error) {
	host, build := m.AttachedHost, ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() (*session.VerbClient, error) {
		if host == "" {
			return session.DialVerbClient()
		}
		c, _, err := session.DialVerbClientThroughHost(host, build)
		return c, err
	}
}

// newWindowOnHostTimeout covers the far daemon spawning a process over a link
// that is already up, plus the room the open itself is given.
const newWindowOnHostTimeout = 30 * time.Second

// ApplyNewWindowOnHost reports what came back. Success says nothing: the window
// appearing is the answer, and a notification on top of it would be noise.
func (m *OS) ApplyNewWindowOnHost(msg NewWindowOnHostMsg) {
	// A link that went down after the pick says so in its own words, which
	// already name the machine.
	var call *session.VerbCallError
	if errors.As(msg.Err, &call) && call.Code == session.ErrVerbHostUnreachable {
		m.ShowNotification(call.Message, "warning", m.Settings.NotificationWarningDuration)
		return
	}
	if msg.Err != nil {
		m.ShowNotification("Could not open a window on "+msg.Host+": "+msg.Err.Error(),
			"error", m.Settings.NotificationDuration*3)
	}
}

// hostPickerActivate runs row idx, for a click. It goes through ChooseHost,
// as enter does, so a click in the session picker makes a session. It used to
// make a window whatever the picker was open for.
func (m *OS) hostPickerActivate(idx int) tea.Cmd {
	filtered := FilterHostPickerItems(m.HostPickerItems, m.HostPickerQuery)
	if idx < 0 || idx >= len(filtered) {
		return nil
	}
	return m.ChooseHost(filtered[idx])
}

// GlobalSessionName is the session that holds panes from more than one
// machine.
//
// It is a session of its own rather than something any session can become, and
// that is the whole design. An ordinary session is the machine it is on: a new
// pane in it is a pane there, and asking which machine every time would be
// asking a question with one sensible answer. The global session is the one
// place the question is worth putting, so it is the one place it is asked, and
// somebody working locally never meets the picker at all.
const GlobalSessionName = "global"

// nextGlobalSessionName is the first free name for a new global session:
// "global", then "global-2" and up. The first one keeps the bare name because
// most people will only ever have one.
func (m *OS) nextGlobalSessionName() string {
	if m.DaemonClient == nil {
		return freeGlobalSessionName(nil)
	}
	return freeGlobalSessionName(m.DaemonClient.AvailableSessionNames())
}

// freeGlobalSessionName is the first global session name not in names.
func freeGlobalSessionName(names []string) string {
	taken := map[string]bool{}
	for _, n := range names {
		taken[n] = true
	}
	if !taken[GlobalSessionName] {
		return GlobalSessionName
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s-%d", GlobalSessionName, i)
		if !taken[name] {
			return name
		}
	}
}

// IsGlobalSession reports whether a session name is the global one.
//
// This is the fallback for a session created before the daemon marked global
// sessions as such. The mark is what says it now; see SessionState.Global.
func IsGlobalSession(name string) bool { return name == GlobalSessionName }

// inGlobalSession reports whether this client is attached to a global session.
func (m *OS) inGlobalSession() bool {
	return m.SessionGlobal || IsGlobalSession(m.SessionName)
}

// CloseHostPicker puts the picker away without making a window.
//
// It also drops any split that was waiting on the answer. A split records the
// direction and the pane to split before it asks for the window, because the
// window is made by the daemon and arrives later; if the question is cancelled
// that record has to go, or the next window made for any reason lands split
// against a pane the user has since forgotten about.
func (m *OS) CloseHostPicker() {
	m.ShowHostPicker = false
	m.HostPickerQuery = ""
	m.CancelPendingSplit()
}

// CancelPendingSplit forgets a split that was recorded for a window that is
// not going to arrive.
func (m *OS) CancelPendingSplit() {
	m.pendingSplitDir = layout.PreselectionNone
	m.pendingSplitTarget = ""
}

// NewWindowHere is what every "make me a pane" gesture calls: the key, the
// prefix key, the rail's "+", and the palette.
//
// In the global session it asks which machine, every time and by every route,
// because mixing machines is what that session is for. Anywhere else it makes
// the pane here without a word.
func (m *OS) NewWindowHere() {
	if !m.newWindowShouldPickHost() {
		m.AddWindow("")
		return
	}
	m.OpenHostPicker()
}

// newWindowShouldPickHost reports whether a new window has a machine to
// choose between.
func (m *OS) newWindowShouldPickHost() bool {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return false
	}
	if !m.inGlobalSession() {
		return false
	}
	// A global session with nothing to reach but this machine has no choice in
	// it, and a picker with one row is a question with one answer.
	return m.reachableMachines() > 1
}

// GlobalSessionOffered reports whether the rail should list the global session.
//
// Only once a second machine is reachable. Before that it would be a session
// that holds panes from several machines on a machine that knows of none, and
// switching into it would buy nothing but a session to switch back out of.
//
// It does not depend on where this client is attached. The row belongs to this
// machine's group and appears there whether that group is the attached one or
// a host group seen from elsewhere, because a row that came and went on every
// switch would move the machine headings under it, and those are ordered
// precisely so they do not move.
func (m *OS) GlobalSessionOffered() bool {
	if !m.Settings.GlobalSession || !m.IsDaemonSession || m.DaemonClient == nil {
		return false
	}
	return m.reachableMachines() > 1
}

// reachableMachines counts this machine and every configured one whose link is
// up. A machine that is not answering is not a choice: it is listed in the
// picker so nobody wonders where it went, but it cannot be picked, so it must
// not be the reason a picker appears at all.
func (m *OS) reachableMachines() int {
	n := 1
	for _, h := range m.FederationHosts {
		if h.Name == federation.LocalHostName {
			continue
		}
		if h.Status == string(federation.StatusUp) {
			n++
		}
	}
	return n
}
