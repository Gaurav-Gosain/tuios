//go:build !js

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsetup"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

const overlayKindMachineSheet = "machinesheet"

// MachineReleases reads a tuios release for the sheet's "release" install
// choice. The tuios command sets it, as it gives hosts sync the helpers of
// tuios update. Nil leaves the release choice out.
var MachineReleases hostsync.Releases

const (
	msViewPick = iota
	msViewRun
	msViewDone
)

const (
	msTabAddress = iota
	msTabTailnet
	msTabSSH
	msTabs
)

type machineSheet struct {
	view     int
	pick     msPickState
	runs     int
	run      *msRun
	flows    []*hostsetup.Flow
	dest     string
	added    bool
	res      *hostsetup.Result
	bulk     *hostsetup.BulkResult
	err      string
	pane     *hostsetup.Prompt
	paneName string
	paneSeen bool
	paneAt   time.Time
}

type msPickState struct {
	tab              int
	addr, name, back textedit.Line
	field            int

	aliases     []string
	aliasesRead bool
	aliasList   listnav.State

	peers    *hostsetup.Prompt
	peersErr string
	loading  bool
	gen      int
	noBoot   bool
	oneWay   bool
}

type msRun struct {
	id     int
	ch     chan any
	cancel context.CancelFunc
}

type (
	msRunMsg struct {
		run   int
		v     any
		ended bool
	}
	msDone struct {
		bulk *hostsetup.BulkResult
		err  error
	}
	msTailnetMsg struct {
		gen   int
		peers []hostsetup.Peer
		err   error
	}
	msAliasesMsg struct{ aliases []string }
)

type machineSheetMsg interface{ machineSheet() }

func (msRunMsg) machineSheet()     {}
func (msTailnetMsg) machineSheet() {}
func (msAliasesMsg) machineSheet() {}

// MachineSheetOpen reports whether the sheet owns the keyboard. It steps
// aside while a person step's pane runs.
func (m *OS) MachineSheetOpen() bool {
	return m.machineSheet != nil && m.machineSheet.pane == nil
}

// OpenMachineSheet opens the add-a-machine sheet. With dest, the Address tab
// is filled with it and the test starts at once, as the offer on ssh wants.
func (m *OS) OpenMachineSheet(dest string) tea.Cmd {
	if m.ConfigReadOnly || m.LearnMode {
		m.ShowNotification("This session cannot add a machine. It does not own the config file.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	if s := m.machineSheet; s != nil {
		if s.pane != nil {
			m.ShowNotification("A setup step runs in a pane. Finish it first.", "info", m.Settings.NotificationDuration)
		}
		return nil
	}
	m.machineSheet = &machineSheet{}
	if dest = strings.TrimSpace(dest); dest != "" {
		m.machineSheet.pick.addr.Set(dest)
		return m.msStart()
	}
	return nil
}

func (m *OS) closeMachineSheet() {
	s := m.machineSheet
	if s == nil {
		return
	}
	running := s.run != nil && s.view == msViewRun
	m.msStop()
	m.machineSheet = nil
	switch {
	case !running:
	case s.added:
		m.ShowNotification("Stopped. "+s.name()+" is added here. This machine was not added on "+s.name()+".", "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification("Stopped. Nothing was added here.", "info", m.Settings.NotificationDuration)
	}
}

func (s *machineSheet) name() string {
	if len(s.flows) == 1 {
		return s.flows[0].Name
	}
	return "the machine"
}

func (m *OS) msStop() {
	if s := m.machineSheet; s != nil && s.run != nil {
		s.run.cancel()
		s.run = nil
	}
}

func (m *OS) msBase() hostsetup.Options {
	version, _ := buildIdentity()
	opts := hostsetup.Options{Version: version}
	if m.AttachedHost == "" && (m.DaemonClient != nil || m.Inbox.call != nil) {
		call := m.inboxCaller()
		opts.Apply = func(name string) error {
			_, err := call("apply-config", map[string]any{"host": name}, 5*time.Second)
			return err
		}
	}
	if MachineReleases != nil {
		src := hostsync.NewSource(nil)
		src.Kind, src.Version, src.Releases = hostsync.KindRelease, version, MachineReleases
		opts.Source = src
	}
	return opts
}

func msFill(opts *hostsetup.Options) error {
	path, err := config.GetConfigPath()
	if err != nil {
		return err
	}
	opts.ConfigPath = path
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	opts.Executable = exe
	return nil
}

func (m *OS) msBegin(names []string, work func(ctx context.Context, b *hostsetup.Bridge) msDone) tea.Cmd {
	s := m.machineSheet
	m.msStop()
	s.runs++
	ctx, cancel := context.WithCancel(context.Background())
	r := &msRun{id: s.runs, ch: make(chan any), cancel: cancel}
	*s = machineSheet{view: msViewRun, pick: s.pick, runs: s.runs, run: r, dest: strings.TrimSpace(s.pick.addr.String())}
	for _, n := range names {
		s.flows = append(s.flows, &hostsetup.Flow{Name: n})
	}
	send := func(v any) {
		select {
		case r.ch <- v:
		case <-ctx.Done():
		}
	}
	runCmd := func() tea.Msg {
		defer close(r.ch)
		send(work(ctx, &hostsetup.Bridge{Send: send}))
		return nil
	}
	return tea.Batch(runCmd, msListen(r))
}

func msListen(r *msRun) tea.Cmd {
	return func() tea.Msg {
		v, ok := <-r.ch
		return msRunMsg{run: r.id, v: v, ended: !ok}
	}
}

func (m *OS) msStart() tea.Cmd {
	p := m.machineSheet.pick
	dest := strings.TrimSpace(p.addr.String())
	if dest == "" {
		m.ShowNotification("Type the address of the machine, like user@host.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	return m.msRun([]hostsetup.Peer{{Name: strings.TrimSpace(p.name.String()), Addr: dest}}, hostsetup.Choices{Address: strings.TrimSpace(p.back.String())})
}

func (m *OS) msStartBulk() tea.Cmd {
	p := m.machineSheet.pick
	return m.msRun(p.peers.Picked(), hostsetup.Choices{Install: hostsetup.InstallCopy, Service: new(!p.noBoot), BothWays: new(!p.oneWay), Yes: true})
}

func (m *OS) msRun(peers []hostsetup.Peer, c hostsetup.Choices) tea.Cmd {
	if len(peers) == 0 {
		m.ShowNotification("Tick a machine first. Space ticks the machine under the cursor.", "info", m.Settings.NotificationDuration)
		return nil
	}
	names := make([]string, len(peers))
	for i, peer := range peers {
		names[i] = cmpOr(peer.Name, msNameOf(peer.Addr))
	}
	base := m.msBase()
	return m.msBegin(names, func(ctx context.Context, b *hostsetup.Bridge) msDone {
		if err := msFill(&base); err != nil {
			return msDone{err: err}
		}
		return msDone{bulk: hostsetup.RunBulk(ctx, base, peers, c, nil, b)}
	})
}

func msNameOf(dest string) string {
	h := dest[strings.LastIndexByte(dest, '@')+1:]
	if i := strings.IndexAny(h, ".:"); i > 0 {
		h = h[:i]
	}
	return h
}

func (s *machineSheet) msFlow(name string) *hostsetup.Flow {
	for _, f := range s.flows {
		if f.Name == name || len(s.flows) == 1 {
			f.Name = cmpOr(name, f.Name)
			return f
		}
	}
	return nil
}

func (s *machineSheet) msAsk() *hostsetup.Prompt {
	for _, f := range s.flows {
		if f.Ask != nil {
			return f.Ask
		}
	}
	return nil
}

func (m *OS) applyMachineSheetMsg(msg machineSheetMsg) tea.Cmd {
	s := m.machineSheet
	if s == nil {
		return nil
	}
	switch msg := msg.(type) {
	case msAliasesMsg:
		s.pick.aliases, s.pick.aliasesRead = msg.aliases, true
	case msTailnetMsg:
		p := &s.pick
		if msg.gen == p.gen {
			p.loading, p.peersErr = false, ""
			p.peers = hostsetup.PeersPrompt(msg.peers)
			if msg.err != nil {
				p.peersErr = "tuios cannot read your tailnet. " + msg.err.Error()
			}
		}
	case msRunMsg:
		if s.run == nil || msg.run != s.run.id {
			return nil
		}
		if msg.ended {
			s.run = nil
			return nil
		}
		m.msTake(msg.v)
		return msListen(s.run)
	}
	return nil
}

func (m *OS) msTake(v any) {
	s := m.machineSheet
	switch v := v.(type) {
	case hostsetup.BridgeEvent:
		f := s.msFlow(v.Machine)
		if f == nil {
			return
		}
		f.Apply(v.Event)
		if v.Step == hostsetup.StepAdd {
			s.added = v.State == hostsetup.StateOK
		}
	case *hostsetup.Prompt:
		if f := s.msFlow(v.Machine); f != nil {
			f.Ask = v
		}
	case msDone:
		for _, f := range s.flows {
			f.Ask = nil
		}
		s.bulk, s.view = v.bulk, msViewDone
		switch {
		case v.err != nil:
			s.err, s.view = v.err.Error(), msViewRun
		case len(s.flows) == 1 && len(v.bulk.Machines) == 1:
			if s.res = v.bulk.Machines[0]; !s.res.OK {
				s.err, s.view = s.res.Error, msViewRun
			}
		}
	}
}

func (m *OS) msListTailnet() tea.Cmd {
	p := &m.machineSheet.pick
	p.gen++
	p.loading = true
	gen := p.gen
	version, _ := buildIdentity()
	return func() tea.Msg {
		opt, _ := tailnetOptionsFromFile()
		var added map[string]federation.Host
		if path, err := config.GetConfigPath(); err == nil {
			added, _ = hostsetup.ConfiguredHosts(path)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		peers, err := hostsetup.ListTailnet(ctx, opt, added, version)
		return msTailnetMsg{gen: gen, peers: peers, err: err}
	}
}

func msReadAliases() tea.Msg {
	return msAliasesMsg{aliases: federation.ReadSSHAliases(federation.UserSSHConfigPath())}
}

func (m *OS) msOpenTab(tab int) tea.Cmd {
	p := &m.machineSheet.pick
	p.tab, p.field = (tab+msTabs)%msTabs, 0
	switch {
	case p.tab == msTabTailnet && p.peers == nil && !p.loading:
		return m.msListTailnet()
	case p.tab == msTabSSH && !p.aliasesRead:
		return msReadAliases
	}
	return nil
}

func (m *OS) msPersonStep(p *hostsetup.Prompt) tea.Cmd {
	s := m.machineSheet
	step := p.Person
	if step.URL != "" {
		return m.openSignInPage(cmpOr(p.Machine, s.name()), step.URL)
	}
	script := `printf '%s\n\n%s\n' "$1" 'Press enter to run this command. Press ctrl+c to stop.'; read _ || exit 1; shift; exec "$@"`
	argv := step.Argv
	if len(argv) == 0 {
		argv = []string{"sh", "-c", step.Line}
	}
	s.pane, s.paneName, s.paneSeen, s.paneAt = p, "Set up "+cmpOr(p.Machine, s.name()), false, time.Now()
	m.AddWindow(s.paneName, append([]string{"sh", "-c", script, "sh", step.Line}, argv...)...)
	m.ShowNotification("Run the command in the new pane. The setup goes on when it ends.", "info", m.Settings.NotificationDuration)
	return nil
}

func (m *OS) machineSheetAfter() {
	s := m.machineSheet
	if s == nil || s.pane == nil {
		return
	}
	for _, w := range m.Windows {
		if w != nil && w.CustomName == s.paneName && !w.ProcessExited() {
			s.paneSeen = true
			return
		}
	}
	if s.paneSeen || time.Since(s.paneAt) > time.Minute {
		p := s.pane
		s.pane = nil
		p.Finish(nil)
	}
}

// MachineSheetKey handles a key while the sheet is open. A key a script sent
// through the client (send-keys, a tape) answers no prompt.
func (m *OS) MachineSheetKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.machineSheet
	if s == nil {
		return nil
	}
	key := msg.String()
	switch {
	case key == "ctrl+c" || key == "esc" && s.view != msViewPick:
		m.closeMachineSheet()
		return nil
	case s.view == msViewPick:
		return m.msPickKey(msg)
	case s.view == msViewDone:
		return m.msDoneKey(key)
	}
	p := s.msAsk()
	if p == nil || m.ProcessingRemoteKeys || m.pressingTapeKeys {
		return nil
	}
	if p.Kind == hostsetup.AskPerson && key == "enter" {
		return m.msPersonStep(p)
	}
	if p.Key(key) {
		for _, f := range s.flows {
			if f.Ask == p {
				f.Ask = nil
			}
		}
	}
	return nil
}

func (m *OS) msDoneKey(key string) tea.Cmd {
	if key == "enter" {
		single := m.machineSheet.res != nil
		m.closeMachineSheet()
		if single {
			m.OpenNewSessionPicker()
		}
	}
	return nil
}

// MachineSheetType types pasted text into the sheet's field.
func (m *OS) MachineSheetType(text string) {
	if f := m.msField(); f != nil {
		lineInsert(f, text, cleanControl)
	}
}

func (m *OS) msField() *textedit.Line {
	s := m.machineSheet
	if s == nil || s.view != msViewPick || s.pick.tab != msTabAddress {
		return nil
	}
	return [...]*textedit.Line{&s.pick.addr, &s.pick.name, &s.pick.back}[s.pick.field]
}

func (m *OS) msPickKey(msg tea.KeyPressMsg) tea.Cmd {
	p := &m.machineSheet.pick
	key := msg.String()
	switch key {
	case "esc":
		m.closeMachineSheet()
		return nil
	case "tab":
		if p.tab == msTabAddress && p.field < 2 {
			p.field++
			return nil
		}
		return m.msOpenTab(p.tab + 1)
	case "shift+tab":
		if p.tab == msTabAddress && p.field > 0 {
			p.field--
			return nil
		}
		return m.msOpenTab(p.tab - 1)
	case "ctrl+r":
		if p.tab == msTabTailnet {
			return m.msListTailnet()
		}
		p.aliasesRead = false
		return msReadAliases
	}
	switch p.tab {
	case msTabAddress:
		if key == "enter" {
			return m.msStart()
		}
		msEdit(m.msField(), msg)
	case msTabSSH:
		if key == "enter" && p.aliasList.Cursor < len(p.aliases) {
			p.addr.Set(p.aliases[p.aliasList.Cursor])
			p.name.Set("")
			p.tab = msTabAddress
			return m.msStart()
		}
		if delta, ok := listDelta(m.ListKeys, key, listOverlayRows); ok {
			p.aliasList.Move(delta, len(p.aliases), len(p.aliases), m.listWraps(), nil)
		}
	case msTabTailnet:
		switch {
		case p.peers == nil:
		case key == "b":
			p.noBoot = !p.noBoot
		case key == "w":
			p.oneWay = !p.oneWay
		case m.ProcessingRemoteKeys || m.pressingTapeKeys:
		case p.peers.Key(key):
			return m.msStartBulk()
		case p.peers.Own >= 0:
			peer := p.peers.Peers[p.peers.Own]
			p.peers.Own = -1
			p.addr.Set(peer.Addr)
			p.name.Set(peer.Name)
			p.tab = msTabAddress
			return m.msStart()
		}
	}
	return nil
}

func msEdit(f *textedit.Line, msg tea.KeyPressMsg) {
	if f == nil {
		return
	}
	text := msg.Text
	if msg.Mod&^tea.ModShift != 0 {
		text = ""
	}
	lineKey(f, msg.String(), text, cleanControl)
}
