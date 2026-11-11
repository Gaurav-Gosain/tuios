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
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// The add-a-machine sheet: one modal sheet in five steps (Pick, Test, Set up
// tuios, Both ways, Done) over internal/hostsetup, the engine behind
// `tuios hosts setup`. The sheet runs the engine in this client's process,
// never the command. The engine runs in a tea.Cmd goroutine and asks through
// a hostsetup.Bridge: its events and prompts come back as messages, each
// prompt waits until the person answers it, and the steps and their words are
// hostsetup.Flow's, the same model the interactive command draws. Update does
// no I/O: the config path, the ssh config, the tailnet and the binary are
// read in Cmds.
//
// The engine writes [hosts.NAME] only at its add step, after the far daemon
// answers. Esc before that cancels its context, and the config file stays as
// it was.
//
// Idle cost: nothing ticks. The engine's goroutine and the one that waits for
// its next message park on channels, and a frame is drawn only when a message
// lands. The mark of a running step is a still glyph.
//
// A step that needs the person (a password for ssh-copy-id, a host key to
// check, a package manager that asks for sudo) opens a pane here that shows
// the command and runs it only when the person presses enter. The sheet steps
// aside while that pane runs, and the setup tests again when it ends.

// overlayKindMachineSheet is the sheet's overlay kind.
const overlayKindMachineSheet = "machinesheet"

// MachineReleases reads a tuios release for the sheet's "release" install
// choice. The tuios command sets it, as it gives hosts sync the helpers of
// tuios update. Nil leaves the release choice out.
var MachineReleases hostsync.Releases

// The views of the sheet.
const (
	msViewPick = iota
	msViewRun
	msViewDone
)

// The Pick step's tabs.
const (
	msTabAddress = iota
	msTabTailnet
	msTabSSH
	msTabs
)

// machineSheet is the sheet's state. It is nil while the sheet is closed.
type machineSheet struct {
	view int
	pick msPickState
	// runs counts the runs, so a message of an old one is dropped.
	runs int
	run  *msRun
	// flows are the machines of the run, one for a single machine.
	flows []*hostsetup.Flow
	dest  string
	added bool
	res   *hostsetup.Result
	bulk  *hostsetup.BulkResult
	err   string
	// pane is the person step whose pane runs, and paneName finds that pane
	// among the windows. paneSeen says it has been there.
	pane     *hostsetup.Prompt
	paneName string
	paneSeen bool
	paneAt   time.Time
}

// msPickState is the Pick step: its tab, the Address tab's fields, and the
// ssh config and tailnet lists.
type msPickState struct {
	tab int
	// addr, name and back are the Address tab's fields: the machine, its
	// name here, and this machine's address for it. field has the keys.
	addr, name, back string
	field            int

	aliases     []string
	aliasesRead bool
	aliasCur    int

	peers    *hostsetup.Prompt
	peersErr string
	loading  bool
	gen      int
	noBoot   bool
	oneWay   bool
}

// msRun is one run of the engine, single or bulk.
type msRun struct {
	id     int
	ch     chan any
	cancel context.CancelFunc
}

// The sheet's messages.
type (
	// msRunMsg is what a run sent: a hostsetup.BridgeEvent, a
	// *hostsetup.Prompt or an msDone. nil with ended says the run is over.
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

// machineSheetMsg is every message of the sheet, so Update hands them over
// in one case.
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
		m.machineSheet.pick.addr = dest
		return m.msStart()
	}
	return nil
}

// closeMachineSheet ends the run, if any, and closes the sheet with a note
// that says what stayed.
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

// name is the machine of a single run.
func (s *machineSheet) name() string {
	if len(s.flows) == 1 {
		return s.flows[0].Name
	}
	return "the machine"
}

// msStop cancels the run. Its prompts are dropped with it.
func (m *OS) msStop() {
	if s := m.machineSheet; s != nil && s.run != nil {
		s.run.cancel()
		s.run = nil
	}
}

// msBase is the engine's options for this client. The parts that read files
// are filled in the Cmd by msFill. It does no I/O.
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

// msFill reads the config path and this binary, in the engine's goroutine.
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

// msBegin starts a run of names: work runs the engine with the bridge and
// returns what to report.
func (m *OS) msBegin(names []string, work func(ctx context.Context, b *hostsetup.Bridge) msDone) tea.Cmd {
	s := m.machineSheet
	m.msStop()
	s.runs++
	ctx, cancel := context.WithCancel(context.Background())
	r := &msRun{id: s.runs, ch: make(chan any), cancel: cancel}
	*s = machineSheet{view: msViewRun, pick: s.pick, runs: s.runs, run: r, dest: strings.TrimSpace(s.pick.addr)}
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

// msListen waits for the next message of a run.
func msListen(r *msRun) tea.Cmd {
	return func() tea.Msg {
		v, ok := <-r.ch
		return msRunMsg{run: r.id, v: v, ended: !ok}
	}
}

// msStart sets up the machine of the Address tab. The sheet asks for every
// choice, so nothing is agreed in advance.
func (m *OS) msStart() tea.Cmd {
	p := m.machineSheet.pick
	dest := strings.TrimSpace(p.addr)
	if dest == "" {
		m.ShowNotification("Type the address of the machine, like user@host.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	return m.msRun([]hostsetup.Peer{{Name: strings.TrimSpace(p.name), Addr: dest}}, hostsetup.Choices{Address: strings.TrimSpace(p.back)})
}

// msStartBulk sets up the ticked tailnet machines with one set of answers.
func (m *OS) msStartBulk() tea.Cmd {
	p := m.machineSheet.pick
	// The person ticked these machines and gave the answers: that is the
	// consent for each step the answers name.
	return m.msRun(p.peers.Picked(), hostsetup.Choices{Install: hostsetup.InstallCopy, Service: new(!p.noBoot), BothWays: new(!p.oneWay), Yes: true})
}

// msRun sets up peers with c, one machine or many, in the engine's bulk run.
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

// msNameOf is a likely name for dest before the engine names it.
func msNameOf(dest string) string {
	h := dest[strings.LastIndexByte(dest, '@')+1:]
	if i := strings.IndexAny(h, ".:"); i > 0 {
		h = h[:i]
	}
	return h
}

// msFlow is the flow of the machine called name, the only one for "".
func (s *machineSheet) msFlow(name string) *hostsetup.Flow {
	for _, f := range s.flows {
		if f.Name == name || len(s.flows) == 1 {
			// One machine takes the name the engine gave it.
			f.Name = cmpOr(name, f.Name)
			return f
		}
	}
	return nil
}

// msAsk is the prompt the keys answer: the first one that waits.
func (s *machineSheet) msAsk() *hostsetup.Prompt {
	for _, f := range s.flows {
		if f.Ask != nil {
			return f.Ask
		}
	}
	return nil
}

// applyMachineSheetMsg takes a message of the sheet. It does no I/O.
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

// msTake records one thing the engine sent.
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
			// The config file watcher brings the new [hosts.NAME] in.
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
			// One machine: its end shows as the steps that stopped, or Done.
			if s.res = v.bulk.Machines[0]; !s.res.OK {
				s.err, s.view = s.res.Error, msViewRun
			}
		}
	}
}

// msListTailnet lists the tailnet's machines with each one's state. It runs
// when the tab opens and on ctrl+r, never in the background.
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

// msReadAliases reads the ssh config's Host names.
func msReadAliases() tea.Msg {
	return msAliasesMsg{aliases: federation.ReadSSHAliases(federation.UserSSHConfigPath())}
}

// msOpenTab switches the Pick step to tab, and reads what the tab lists the
// first time it opens.
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

// msPersonStep runs the person step of p: a sign-in page opens, and t tests
// again once the person signed in. A command opens a pane that shows it and
// runs it on enter. The sheet steps aside until the pane's program ends.
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

// machineSheetAfter follows the pane of a person step: once the pane was
// there and is gone, its program ended, and the step is finished. It runs
// after every message while such a pane is open, and costs one pass over the
// windows.
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
		*f += strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, text)
	}
}

// msField is the Address tab's field that has the keys, nil on other tabs.
func (m *OS) msField() *string {
	s := m.machineSheet
	if s == nil || s.view != msViewPick || s.pick.tab != msTabAddress {
		return nil
	}
	return [...]*string{&s.pick.addr, &s.pick.name, &s.pick.back}[s.pick.field]
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
		if key == "enter" && p.aliasCur < len(p.aliases) {
			p.addr, p.name, p.tab = p.aliases[p.aliasCur], "", msTabAddress
			return m.msStart()
		}
		if delta, ok := listDelta(m.ListKeys, key, listOverlayRows); ok {
			p.aliasCur = m.listStep(p.aliasCur, delta, len(p.aliases))
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
			// Own answers: the machine is set up alone, and the sheet asks
			// each of its questions.
			peer := p.peers.Peers[p.peers.Own]
			p.peers.Own = -1
			p.addr, p.name, p.tab = peer.Addr, peer.Name, msTabAddress
			return m.msStart()
		}
	}
	return nil
}

// msEdit applies an editing key to f.
func msEdit(f *string, msg tea.KeyPressMsg) {
	// A key with a modifier other than shift types nothing here, so alt+x
	// does not add an x.
	text := msg.Text
	if msg.Mod&^tea.ModShift != 0 {
		text = ""
	}
	textedit.EditLine(f, msg.String(), text, true)
}
