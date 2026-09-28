package app

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The machines as the attached daemon names them.
//
// A window is made by the daemon that holds the session. While the client is
// attached to a session on another machine, that is the other machine's
// daemon, and it reads a host name in its own [hosts] table. The two tables
// need not agree: alpha may call this machine "beta", and may not know a
// machine this one calls "beta" at all. So the window picker lists alpha's
// hosts by alpha's names, with alpha's link states, and this machine appears
// only when one of alpha's hosts is this machine's own daemon. That is decided
// by the daemon's run id (hello's instance), never by a name.

// attachedHostView is what the attached daemon last said about its hosts.
type attachedHostView struct {
	// Host is the attached machine, by this machine's name for it. A view for
	// another host than the one attached now is stale and not used.
	Host string
	// Hosts is the attached daemon's own list-hosts, in its order.
	Hosts []federation.HostReport
	// Here is the attached daemon's name for this machine, "" when none of
	// its hosts is this machine's daemon.
	Here string
	// Err is why the list could not be read, if it could not.
	Err error
}

// AttachedHostsMsg carries a fresh attachedHostView into Update.
type AttachedHostsMsg struct {
	View attachedHostView
}

// attachedHostsChan is the channel the fetch goroutine answers on, made on
// first use.
func (m *OS) attachedHostsChan() chan AttachedHostsMsg {
	if m.attachedHostsCh == nil {
		m.attachedHostsCh = make(chan AttachedHostsMsg, 4)
	}
	return m.attachedHostsCh
}

// ListenForAttachedHosts waits for one attached-hosts answer.
func ListenForAttachedHosts(ch chan AttachedHostsMsg) tea.Cmd {
	return listenOnce(ch, func(msg AttachedHostsMsg) tea.Msg { return msg })
}

// refreshAttachedHosts asks the attached daemon for its hosts, off the UI
// goroutine. It does nothing while the client is on this machine, where the
// rail's own snapshot is the attached daemon's list.
func (m *OS) refreshAttachedHosts() {
	host := m.AttachedHost
	if host == "" {
		return
	}
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	ch := m.attachedHostsChan()
	go func() {
		ch <- AttachedHostsMsg{View: fetchAttachedHosts(host, build)}
	}()
}

// fetchAttachedHosts reads host's list-hosts through the link, and this
// machine's daemon's run id, and finds this machine among host's hosts.
func fetchAttachedHosts(host, build string) attachedHostView {
	view := attachedHostView{Host: host}
	here := localDaemonInstance()
	far, _, err := session.DialVerbClientThroughHost(host, build)
	if err != nil {
		view.Err = err
		return view
	}
	defer func() { _ = far.Close() }()
	raw, err := far.Call("list-hosts", nil)
	if err != nil {
		view.Err = err
		return view
	}
	var res struct {
		Hosts []federation.HostReport `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		view.Err = err
		return view
	}
	view.Hosts = res.Hosts
	for _, r := range res.Hosts {
		if here != "" && r.Instance == here {
			view.Here = r.Host
		}
	}
	return view
}

// localDaemonInstance is this machine's daemon's run id, or "" when it cannot
// be read. An empty id matches no host, so this machine is then left out.
func localDaemonInstance() string {
	c, err := session.DialVerbClient()
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("hello", map[string]any{"client": "tuios"})
	if err != nil {
		return ""
	}
	var res struct {
		Instance string `json:"instance"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Instance
}

// applyAttachedHosts stores a view and, when the open window picker was
// still waiting for it, fills the picker in. A picker that already had its
// rows keeps them, so nothing moves under the cursor; a pick still checks the
// newest view.
func (m *OS) applyAttachedHosts(msg AttachedHostsMsg) {
	if msg.View.Host != m.AttachedHost {
		return
	}
	m.attachedHosts = msg.View
	m.attachedHostsLoaded = true
	if !m.ShowHostPicker || m.HostPickerPurpose != HostPickerNewWindow || !hostPickerWaiting(m.HostPickerItems) {
		return
	}
	m.HostPickerItems = m.buildHostPickerItems()
	m.HostPickerSelected = FirstPickableHostRow(FilterHostPickerItems(m.HostPickerItems, m.HostPickerQuery))
	m.HostPickerScroll = 0
}

// attachedView is the view for the machine attached now, and whether there
// is one.
func (m *OS) attachedView() (attachedHostView, bool) {
	if m.AttachedHost == "" || !m.attachedHostsLoaded || m.attachedHosts.Host != m.AttachedHost {
		return attachedHostView{}, false
	}
	return m.attachedHosts, true
}

// attachedWindowItems is the window picker's rows while the client is
// attached to another machine: that machine first, then this machine if it
// has a link here, then its other hosts by its names.
func (m *OS) attachedWindowItems() []HostPickerItem {
	items := []HostPickerItem{{
		Name:   "",
		Label:  m.AttachedHost,
		Detail: "this session",
		Up:     true,
	}}
	view, ok := m.attachedView()
	if !ok {
		return append(items, HostPickerItem{
			Label:   "other machines",
			Detail:  "loading",
			Pending: true,
		})
	}
	if view.Err != nil && len(view.Hosts) == 0 {
		return append(items, HostPickerItem{
			Label:  "other machines",
			Detail: "not available",
		})
	}
	var rest []HostPickerItem
	for _, r := range view.Hosts {
		up := r.Status == federation.StatusUp
		detail := hostStatusLabel(string(r.Status))
		if up {
			detail = pluralSessions(r.Sessions)
		}
		it := HostPickerItem{Name: r.Host, Label: r.Host, Detail: detail, Up: up}
		if r.Host == view.Here {
			it.Label = "this machine"
			items = append(items, it)
			continue
		}
		rest = append(rest, it)
	}
	return append(items, rest...)
}

// attachedHostUp reports whether the attached daemon's link to name is up, by
// the newest view.
func (m *OS) attachedHostUp(name string) bool {
	view, ok := m.attachedView()
	if !ok {
		return false
	}
	for _, r := range view.Hosts {
		if r.Host == name {
			return r.Status == federation.StatusUp
		}
	}
	return false
}

// attachedReachable counts the machines a window in the attached session can
// be put on, and whether that is known yet.
func (m *OS) attachedReachable() (int, bool) {
	view, ok := m.attachedView()
	if !ok {
		return 0, false
	}
	n := 1
	for _, r := range view.Hosts {
		if r.Status == federation.StatusUp {
			n++
		}
	}
	return n, true
}

// hostPickerWaiting reports whether the rows include the loading placeholder.
func hostPickerWaiting(items []HostPickerItem) bool {
	for _, it := range items {
		if it.Pending {
			return true
		}
	}
	return false
}

// FirstPickableHostRow is the index of the first row that can be chosen, or 0
// when none can. The picker's cursor starts there, so enter on a fresh picker
// never lands on a machine that is down.
func FirstPickableHostRow(items []HostPickerItem) int {
	for i, it := range items {
		if it.Up {
			return i
		}
	}
	return 0
}
