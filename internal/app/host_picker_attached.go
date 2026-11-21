package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type attachedHostView struct {
	Host  string
	Hosts []federation.HostReport
	Here  string
	Err   error
	OS    string
}

// AttachedHostsMsg carries a fresh attachedHostView into Update.
type AttachedHostsMsg struct {
	View attachedHostView
}

func (m *OS) attachedHostsChan() chan AttachedHostsMsg {
	if m.attachedHostsCh == nil {
		m.attachedHostsCh = make(chan AttachedHostsMsg, 4)
	}
	return m.attachedHostsCh
}

func listenAttachedHostsUntil(stop <-chan struct{}, ch chan AttachedHostsMsg) tea.Cmd {
	return listenOnce(stop, ch, func(msg AttachedHostsMsg) tea.Msg { return msg })
}

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

func fetchAttachedHosts(host, build string) attachedHostView {
	view := attachedHostView{Host: host}
	here := localDaemonInstance()
	far, _, err := session.DialVerbClientThroughHost(host, build)
	if err != nil {
		view.Err = err
		return view
	}
	defer func() { _ = far.Close() }()
	if hs := far.Daemon(); hs != nil {
		view.OS = hs.OS
	}
	raw, err := far.Call("list-hosts", nil)
	if err != nil {
		view.Err = err
		return view
	}
	var res struct {
		Hosts []federation.HostReport `json:"hosts"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
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
	if jsonx.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Instance
}

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
	m.hostList.Refiltered(FirstPickableHostRow(m.hostPickerRows()))
}

func (m *OS) attachedView() (attachedHostView, bool) {
	if m.AttachedHost == "" || !m.attachedHostsLoaded || m.attachedHosts.Host != m.AttachedHost {
		return attachedHostView{}, false
	}
	return m.attachedHosts, true
}

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
			it.Label = thisMachineLabel()
			items = append(items, it)
			continue
		}
		rest = append(rest, it)
	}
	return append(items, rest...)
}

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
