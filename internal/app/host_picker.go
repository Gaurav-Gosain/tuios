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
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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
	// Pending marks the placeholder row shown while the attached machine's
	// host list is on its way. It is not a machine and cannot be chosen.
	Pending bool
}

func (m *OS) buildHostPickerItems() []HostPickerItem {
	if m.HostPickerPurpose == HostPickerNewWindow && m.AttachedHost != "" {
		return m.attachedWindowItems()
	}
	items := []HostPickerItem{{
		Name:   "",
		Label:  thisMachineLabel(),
		Detail: "local",
		Up:     true,
	}}
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
	m.refreshAttachedHosts()
	m.HostPickerItems = m.buildHostPickerItems()
	if len(m.HostPickerItems) <= 1 {
		m.ShowNotification("No other machines are configured. Add one with 'tuios hosts add'",
			"info", m.Settings.NotificationDuration)
		return
	}
	m.hostList.Reset()
	m.hostList.Cursor = FirstPickableHostRow(m.HostPickerItems)
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

	m.hostList.Reset()
	m.hostList.Cursor = FirstPickableHostRow(m.HostPickerItems)
	m.ShowHostPicker = true
}

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
// A row's Name is the machine as the daemon that holds the session names it,
// and "" is that daemon's own machine: this one, or the one the client is
// attached to. See buildHostPickerItems.
//
// Whether the machine is up is asked again here, of the same daemon's link.
// The list is a snapshot from when the picker opened, and a link that dropped
// since then would otherwise be dialled with a timeout on the end of it.
func (m *OS) ChooseHostForNewWindow(item HostPickerItem) tea.Cmd {
	m.ShowHostPicker = false
	m.hostList.Reset()
	if item.Pending {
		m.CancelPendingSplit()
		m.ShowNotification("The list of machines is still loading. Try again", "info",
			m.Settings.NotificationDuration)
		return nil
	}
	if !item.Up || (item.Name != "" && !m.windowHostUp(item.Name)) {
		m.CancelPendingSplit()
		m.ShowNotification(item.Label+" is unavailable", "warning",
			m.Settings.NotificationWarningDuration)
		return nil
	}
	if item.Name == "" {
		_ = m.CreateNewWindow()
		return nil
	}
	return m.newWindowOnHostCmd(item.Name)
}

func (m *OS) windowHostUp(name string) bool {
	if m.AttachedHost == "" {
		return m.hostIsUp(name)
	}
	return m.attachedHostUp(name)
}

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

func (m *OS) sessionDaemonDialer() func() (*session.VerbClient, error) {
	host, build := m.AttachedHost, ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() (*session.VerbClient, error) { return dialSessionDaemon(host, build) }
}

const newWindowOnHostTimeout = 30 * time.Second

// ApplyNewWindowOnHost reports what came back. Success says nothing: the window
// appearing is the answer, and a notification on top of it would be noise.
func (m *OS) ApplyNewWindowOnHost(msg NewWindowOnHostMsg) {
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

func (m *OS) hostPickerActivate(idx int) tea.Cmd {
	filtered := m.hostPickerRows()
	if idx < 0 || idx >= len(filtered) {
		return nil
	}
	return m.ChooseHost(filtered[idx])
}

func (m *OS) hostPickerRows() []HostPickerItem {
	return FilterHostPickerItems(m.HostPickerItems, m.hostList.Query.String())
}

func hostPicker() *listPicker {
	return &listPicker{
		kind:  "hostpicker",
		state: func(m *OS) *listnav.State { return &m.hostList },
		count: func(m *OS) int { return len(m.hostPickerRows()) },
		first: func(m *OS) int { return FirstPickableHostRow(m.hostPickerRows()) },
		keys: func(m *OS, key string) (tea.Cmd, bool) {
			if key == "ctrl+c" {
				m.CloseHostPicker()
				return nil, true
			}
			return nil, false
		},
		accept: func(m *OS) tea.Cmd { return m.hostPickerActivate(m.hostList.Cursor) },
		close: func(m *OS) tea.Cmd {
			m.CloseHostPicker()
			return nil
		},
		clearKey: true,
	}
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

func (m *OS) nextGlobalSessionName() string {
	if m.DaemonClient == nil {
		return freeGlobalSessionName(nil)
	}
	return freeGlobalSessionName(m.DaemonClient.AvailableSessionNames())
}

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
	m.hostList.Reset()
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

func (m *OS) newWindowShouldPickHost() bool {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return false
	}
	if !m.inGlobalSession() {
		return false
	}
	if m.AttachedHost != "" {
		n, known := m.attachedReachable()
		if !known {
			m.refreshAttachedHosts()
			return true
		}
		return n > 1
	}
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
