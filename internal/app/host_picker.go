package app

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
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
}

// HostPickerItems is this machine followed by every configured one, in the
// order the rail lists them, so the picker and the rail agree.
func (m *OS) buildHostPickerItems() []HostPickerItem {
	items := []HostPickerItem{{
		Name:   "",
		Label:  "this machine",
		Detail: "local",
		Up:     true,
	}}
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
func (m *OS) ChooseHostForNewWindow(item HostPickerItem) tea.Cmd {
	m.ShowHostPicker = false
	m.HostPickerQuery = ""
	if !item.Up {
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
func (m *OS) newWindowOnHostCmd(host string) tea.Cmd {
	name := m.SessionName
	return func() tea.Msg {
		client, err := session.DialVerbClient()
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

// newWindowOnHostTimeout covers the far daemon spawning a process over a link
// that is already up, plus the room the open itself is given.
const newWindowOnHostTimeout = 30 * time.Second

// ApplyNewWindowOnHost reports what came back. Success says nothing: the window
// appearing is the answer, and a notification on top of it would be noise.
func (m *OS) ApplyNewWindowOnHost(msg NewWindowOnHostMsg) {
	if msg.Err != nil {
		m.ShowNotification("Could not open a window on "+msg.Host+": "+msg.Err.Error(),
			"error", m.Settings.NotificationDuration*3)
	}
}

// hostPickerActivate opens a window on the machine on row idx, for a click.
// The keyboard path reaches the same place through ChooseHostForNewWindow.
func (m *OS) hostPickerActivate(idx int) tea.Cmd {
	filtered := FilterHostPickerItems(m.HostPickerItems, m.HostPickerQuery)
	if idx < 0 || idx >= len(filtered) {
		return nil
	}
	return m.ChooseHostForNewWindow(filtered[idx])
}
