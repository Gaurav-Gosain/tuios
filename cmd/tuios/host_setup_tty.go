package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsetup"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The interactive mode of `tuios hosts setup`: on a terminal, a choice the
// flags did not make is asked in place. It is an inline Bubble Tea program
// (no alt screen), and it only draws: the steps, the questions, their keys
// and their words are hostsetup's Flow and Prompt, which the add sheet in
// the app draws too. The engine asks through a hostsetup.Bridge whose Send
// is this program's.
//
// What only a terminal needs is here: a person step runs in this terminal
// with tea.ExecProcess, ctrl+c stops the run at its next step boundary, and
// the done summary stays in the scrollback.
//
// The inks are escape prefixes made once from the dialog palette. The view
// writes lines with one-byte ink markers into one builder, and paint turns
// them into escapes, so drawing builds no styles. Nothing ticks while the
// program waits for the person: the spinner runs only while a step runs and
// no question is open.

// setupInteractive says the command can ask: stdin and stdout are terminals,
// and the output is not for a script.
func setupInteractive(o hostSetupOptions) bool {
	return !o.json && !o.dryRun && isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

type (
	// setupEndMsg says the engine ended. Its result is on setupModel.end.
	setupEndMsg   struct{}
	setupPeersMsg struct {
		peers []hostsetup.Peer
		err   error
	}
	setupExecMsg struct {
		p   *hostsetup.Prompt
		err error
	}
	setupTickMsg struct{}
)

// setupEnd is what the engine returned.
type setupEnd struct {
	res  *hostsetup.Result
	bulk *hostsetup.BulkResult
	err  error
}

// The inks.
const (
	inkText = iota
	inkBold
	inkDim
	inkMute
	inkKey
	inkOK
	inkFail
	inkAttn
	inkInfo
	inkAccent
	inkPill
	inkOff
	inkChip
	inkSel
	inkSelBold
	inkEdgeOK
	inkEdgeAttn
	inkEdgeWarn
	inkTrack
	inkN
)

// The ink markers: one byte each, written into a line where its ink starts.
// They are C0 controls but tab, newline, carriage return and escape. Every
// string from the far machine went through plainLine, which keeps none.
const (
	inkMarkers = "\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16"
	mText      = "\x01"
	mBold      = "\x02"
	mDim       = "\x03"
	mMute      = "\x04"
	mKey       = "\x05"
	mOK        = "\x06"
	mFail      = "\x07"
	mAttn      = "\x08"
	mInfo      = "\x0b"
	mAccent    = "\x0c"
	mPill      = "\x0e"
	mOff       = "\x0f"
	mChip      = "\x10"
	mSel       = "\x11"
	mSelBold   = "\x12"
	mTrack     = "\x16"
)

// setupModel draws one machine's Flow, or one line for each machine of a
// tailnet run, and the Prompt that waits.
type setupModel struct {
	cancel context.CancelFunc
	end    chan setupEnd
	ink    [inkN]string
	width  int
	// b is the frame being written.
	b strings.Builder

	dest  string
	flows []*hostsetup.Flow

	ask     *hostsetup.Prompt
	queue   []*hostsetup.Prompt
	note    string
	execing bool

	spin    int
	ticking bool

	started, finished, stopped bool
	// box is the summary at the end, edge its ink, and fail the error.
	box  []string
	edge int
	fail string

	// A tailnet run: the checklist, then a line for each machine.
	tailnet  bool
	list     tea.Cmd
	run      func([]hostsetup.Peer)
	peers    *hostsetup.Prompt
	added    string
	total    int
	listing  bool
	listErr  error
	choices  hostsetup.Choices
	all      bool
	only     []string
	answered string
	// own are the machines with their own answers, and local the prompts
	// that ask them, which have no engine behind them.
	own   map[string]*hostsetup.Choices
	local map[*hostsetup.Prompt]bool

	// waits is set when the running daemon kept a host change for the
	// person. The summary says so: the program owns the terminal, so the
	// note is not printed while it draws.
	waits *atomic.Bool
}

func newSetupModel() (*setupModel, context.Context) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	m := &setupModel{cancel: cancel, end: make(chan setupEnd, 1), width: 100}
	pal := theme.UI()
	no := overlay.NoColor
	edge := func(c color.Color) color.Color {
		if pal.Depth == overlay.Depth16 {
			return c
		}
		return overlay.MixColors(c, pal.Canvas, 0.5)
	}
	fg := [inkN]color.Color{pal.Fg, pal.Fg, pal.FgDim, pal.FgMute, pal.AccentBright, pal.Success, pal.Warn, pal.Attention, pal.Info,
		pal.Accent, pal.PillFg, pal.FgDim, pal.Fg, pal.FgDim, pal.Fg, edge(pal.Success), edge(pal.Attention), edge(pal.Warn), pal.Edge}
	for i, c := range fg {
		bg := no
		switch i {
		case inkPill:
			bg = pal.Accent
		case inkOff, inkChip:
			bg = pal.Card
		case inkSel, inkSelBold:
			bg = pal.RowSel
		}
		st := lipgloss.NewStyle().Foreground(overlay.Readable(c, bg))
		if bg != no {
			st = st.Background(bg)
		}
		switch i {
		case inkBold, inkKey, inkOK, inkFail, inkPill, inkSelBold:
			st = st.Bold(true)
		}
		m.ink[i], _, _ = strings.Cut(st.Render("\x01"), "\x01")
	}
	return m, ctx
}

// inkMark is the marker of ink i.
func inkMark(i int) string { return inkMarkers[i : i+1] }

// paint turns the markers of a line into escapes, and resets at its end.
func (m *setupModel) paint(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if k := strings.IndexByte(inkMarkers, s[i]); k >= 0 {
			b.WriteString("\x1b[m")
			b.WriteString(m.ink[k])
		} else {
			b.WriteByte(s[i])
		}
	}
	b.WriteString("\x1b[m")
	return b.String()
}

// send is the Bridge's Send.
func send(prog *tea.Program) func(any) { return func(v any) { prog.Send(v) } }

// runHostSetupTTY is the interactive mode for one machine.
func runHostSetupTTY(s *hostsetup.Setup, dest string, o hostSetupOptions, waits *atomic.Bool) error {
	m, ctx := newSetupModel()
	m.waits = waits
	m.dest, m.flows, m.started = dest, []*hostsetup.Flow{{Name: s.Name()}}, true
	prog := tea.NewProgram(m, tea.WithOutput(os.Stdout))
	go func() {
		res, err := s.Run(ctx, o.choices(), &hostsetup.Bridge{Send: send(prog)})
		m.end <- setupEnd{res: res, err: err}
		prog.Send(setupEndMsg{})
	}()
	return m.finish(prog)
}

// runTailnetSetupTTY is the interactive mode for a tailnet: the checklist,
// the shared answers, then a line for each machine and the summary.
func runTailnetSetupTTY(base hostsetup.Options, o hostSetupOptions, waits *atomic.Bool) error {
	added, err := hostsetup.ConfiguredHosts(base.ConfigPath)
	if err != nil {
		return err
	}
	opt, _ := tailnetOptions()
	m, ctx := newSetupModel()
	m.waits = waits
	prog := tea.NewProgram(m, tea.WithOutput(os.Stdout))
	// The shared answers: the flags, else the same version as here, start
	// at boot, both ways. The checklist shows them, so enter on it is the
	// person's yes.
	c := o.choices()
	if c.Install == "" {
		c.Install = hostsetup.InstallCopy
	}
	if c.Service == nil {
		c.Service = new(true)
	}
	if c.BothWays == nil {
		c.BothWays = new(true)
	}
	c.Yes = true
	m.tailnet, m.choices, m.all, m.only, m.own, m.local = true, c, o.all, o.only, map[string]*hostsetup.Choices{}, map[*hostsetup.Prompt]bool{}
	m.list = func() tea.Msg {
		peers, err := hostsetup.ListTailnet(ctx, opt, added, version)
		return setupPeersMsg{peers, err}
	}
	m.run = func(peers []hostsetup.Peer) {
		go func() {
			own := map[string]hostsetup.Choices{}
			for name, c := range m.own {
				own[name] = *c
			}
			r := hostsetup.RunBulk(ctx, base, peers, c, own, &hostsetup.Bridge{Send: send(prog)})
			m.end <- setupEnd{bulk: r}
			prog.Send(setupEndMsg{})
		}()
	}
	return m.finish(prog)
}

// finish runs the program, then waits for the engine. The program can end
// first (ctrl+c, SIGTERM): the engine then stops at its next step boundary,
// and the stop line says what it left.
func (m *setupModel) finish(prog *tea.Program) error {
	_, perr := prog.Run()
	m.cancel()
	if m.listErr != nil {
		return &diagnosticError{What: "tuios cannot read your tailnet: " + m.listErr.Error(), Fix: "check that tailscale runs: tailscale status"}
	}
	var r setupEnd
	if m.started {
		r = <-m.end
	}
	stopped := m.stopped || perr != nil || errors.Is(r.err, context.Canceled)
	switch {
	case stopped && r.bulk != nil:
		fmt.Println("Stopped. " + bulkCount(r.bulk.Summary))
	case stopped:
		fmt.Println(stoppedLine(r.res))
	}
	code := 0
	switch {
	case stopped:
		code = 130
	case r.bulk != nil && r.bulk.Summary.Failed > 0:
		code = 1
	case r.bulk != nil && r.bulk.Summary.NeedYou > 0:
		code = setupNeedPerson
	case r.err != nil:
		code = setupExit(r.err, false).(*diagnosticError).ExitStatus()
	}
	if code != 0 {
		return &statusError{code: code}
	}
	return nil
}

// stoppedLine says what a stopped setup left behind.
func stoppedLine(res *hostsetup.Result) string {
	added, installed := false, false
	if res != nil {
		for _, st := range res.Steps {
			switch st.Step + st.State {
			case hostsetup.StepAdd + hostsetup.StateOK:
				added = true
			case hostsetup.StepAdd + hostsetup.StateSkip:
				added = false
			case hostsetup.StepInstall + hostsetup.StateOK:
				installed = true
			}
		}
	}
	switch {
	case added:
		return "Stopped. " + res.Name + " is added here. This machine was not added on " + res.Name + "."
	case installed && res.Probe != nil:
		return "Stopped. tuios " + res.Probe.TuiosVersion + " stays installed on " + res.Name + ". Nothing was added here."
	}
	return "Stopped. Nothing was added."
}

// bulkCount says the counts of a tailnet run in one sentence.
func bulkCount(sm hostsetup.Summary) string {
	s := strconv.Itoa(sm.Ready) + " machines are ready."
	if sm.Ready == 1 {
		s = "1 machine is ready."
	}
	if sm.NeedYou == 1 {
		s += " 1 needs you."
	} else if sm.NeedYou > 1 {
		s += " " + strconv.Itoa(sm.NeedYou) + " need you."
	}
	if sm.Failed > 0 {
		s += " " + strconv.Itoa(sm.Failed) + " failed."
	}
	return s
}

func (m *setupModel) Init() tea.Cmd {
	if m.tailnet {
		m.listing = true
		return tea.Batch(m.list, m.tick())
	}
	return nil
}

// tick starts the spinner when something runs and it is not running.
func (m *setupModel) tick() tea.Cmd {
	if m.ticking || !m.busy() {
		return nil
	}
	m.ticking = true
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return setupTickMsg{} })
}

// busy says something runs that the person waits on, with no question open.
func (m *setupModel) busy() bool {
	if m.finished || m.stopped || m.ask != nil || m.execing {
		return false
	}
	if m.listing {
		return true
	}
	for _, f := range m.flows {
		for _, l := range f.Lines {
			if l.State == hostsetup.StateRun {
				return true
			}
		}
	}
	return m.tailnet && m.peers == nil && len(m.flows) > 0
}

// flow is the Flow of the machine called name: the only one, or the one of
// that machine of a tailnet run.
func (m *setupModel) flow(name string) *hostsetup.Flow {
	for _, f := range m.flows {
		if !m.tailnet || f.Name == name {
			return f
		}
	}
	return &hostsetup.Flow{Name: name}
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = min(msg.Width, 110)
	case setupTickMsg:
		m.ticking = false
		m.spin++
	case hostsetup.BridgeEvent:
		m.flow(msg.Machine).Apply(msg.Event)
	case *hostsetup.Prompt:
		m.queue = append(m.queue, msg)
		if m.stopped {
			return m, m.stop()
		}
		return m, m.next()
	case setupExecMsg:
		m.execing = false
		err := msg.err
		if err != nil {
			err = errfmt.Errorf("%s ended with an error: %w", msg.p.Person.Line, err)
		}
		return m, m.done(err)
	case setupEndMsg:
		m.ended(<-m.end)
		return m, tea.Quit
	case setupPeersMsg:
		return m, m.peersArrived(msg)
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, m.tick()
}

// stop cancels the run. The engine stops at its next step boundary.
func (m *setupModel) stop() tea.Cmd {
	m.stopped = true
	m.cancel()
	for _, p := range append(m.queue, m.ask) {
		if p != nil {
			p.Finish(context.Canceled)
		}
	}
	m.ask, m.queue = nil, nil
	return tea.Quit
}

// next opens the first waiting question. A person step with a command runs
// it in this terminal at once; one with only a page opens the page.
func (m *setupModel) next() tea.Cmd {
	if m.ask != nil || len(m.queue) == 0 {
		return nil
	}
	p := m.queue[0]
	m.queue, m.ask, m.note = m.queue[1:], p, ""
	if p.Kind != hostsetup.AskPerson {
		return nil
	}
	if len(p.Person.Argv) == 0 {
		m.note = openSetupPage(p.Person.URL)
		return nil
	}
	argv := p.Person.Argv
	if argv[0] == "ssh" {
		argv = append([]string{federation.SSHBinary()}, argv[1:]...)
	}
	m.execing = true
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the step the person agreed to, shown word for word
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return setupExecMsg{p, err} })
}

// done finishes the open question and opens the next one.
func (m *setupModel) done(err error) tea.Cmd {
	m.ask.Finish(err)
	m.ask = nil
	return tea.Batch(m.next(), m.tick())
}

// openSetupPage opens a sign-in page with the opener the app uses, and says
// what happened.
func openSetupPage(url string) string {
	opener := ""
	if cfg, err := config.LoadUserConfig(); err == nil {
		opener = strings.TrimSpace(cfg.Appearance.LinkOpener)
	}
	switch err := app.OpenWebLink(opener, url); {
	case errors.Is(err, app.ErrNoDesktop):
		return "This machine has no desktop to open it. Open the address in a browser on your own machine."
	case err != nil:
		return "Not opened: " + err.Error() + "."
	}
	return "The page is open. Press t when you are signed in."
}

// key gives a key to the open question, or to the checklist.
func (m *setupModel) key(k string) tea.Cmd {
	switch {
	case k == "ctrl+c":
		return m.stop()
	case m.ask != nil:
		if p := m.ask; p.Key(k) {
			if m.local[p] {
				m.ownAnswer(p)
			}
			m.ask = nil
			return tea.Batch(m.next(), m.tick())
		}
	case m.peers != nil && k == "ctrl+r":
		m.peers, m.listing = nil, true
		return tea.Batch(m.list, m.tick())
	case m.peers != nil && m.peers.Key(k):
		return m.goOn(m.peers.Picked())
	case m.peers != nil && m.peers.Own >= 0:
		name := m.peers.Peers[m.peers.Own].Name
		m.peers.Own = -1
		return m.ownAnswers(name)
	}
	return nil
}

// ownAnswers asks one machine of the checklist its own answers: how to
// install, start at boot, and both ways. These prompts have no engine
// behind them. ownAnswer keeps each answer.
func (m *setupModel) ownAnswers(name string) tea.Cmd {
	c := m.choices
	m.own[name] = &c
	pick := &hostsetup.Prompt{Kind: hostsetup.AskPick, Machine: name}
	for _, o := range [...][3]string{
		{hostsetup.InstallCopy, "The same version as here.", "A copy, or the release of this version for another platform."},
		{hostsetup.InstallPkg, "Its package manager.", "The first one the machine has."},
		{hostsetup.InstallScript, "The install script.", "It installs the newest release."},
		{hostsetup.InstallNone, "Do not install.", "Only add and test the machine."},
	} {
		pick.Options = append(pick.Options, hostsetup.InstallOption{ID: o[0], Label: o[1], Note: o[2], Available: true})
	}
	for _, p := range [...]*hostsetup.Prompt{pick,
		{Kind: hostsetup.AskConfirm, Machine: name, Yes: *c.Service, Question: &hostsetup.Question{ID: "service", Text: "Start tuios on " + name + " when it boots?"}},
		{Kind: hostsetup.AskConfirm, Machine: name, Yes: *c.BothWays, Question: &hostsetup.Question{ID: "both_ways", Text: "Add this machine on " + name + " too?"}}} {
		m.local[p] = true
		m.queue = append(m.queue, p)
	}
	return m.next()
}

// ownAnswer keeps one answer of a machine's own answers.
func (m *setupModel) ownAnswer(p *hostsetup.Prompt) {
	c := m.own[p.Machine]
	switch {
	case p.Kind == hostsetup.AskPick:
		c.Install = p.Options[p.Cursor].ID
	case p.Question.ID == "service":
		c.Service = new(p.Yes)
	default:
		c.BothWays = new(p.Yes)
	}
}

// peersArrived opens the checklist, or starts the run on the machines that
// --all or --only named.
func (m *setupModel) peersArrived(msg setupPeersMsg) tea.Cmd {
	m.listing, m.listErr = false, msg.err
	if msg.err != nil {
		return tea.Quit
	}
	var list, picked []hostsetup.Peer
	var added []string
	for _, p := range msg.peers {
		switch {
		case p.State == hostsetup.PeerAdded:
			added = append(added, p.Name)
		default:
			list = append(list, p)
			if p.CanSetUp && (m.all || slices.Contains(m.only, p.Name)) {
				picked = append(picked, p)
			}
		}
	}
	m.total, m.added = len(msg.peers), strings.Join(added, ", ")
	if m.all || len(m.only) > 0 {
		return m.goOn(picked)
	}
	m.peers = hostsetup.PeersPrompt(list)
	return nil
}

// goOn starts the run on the picked machines.
func (m *setupModel) goOn(picked []hostsetup.Peer) tea.Cmd {
	if len(picked) == 0 {
		return nil
	}
	var names []string
	for _, p := range picked {
		names = append(names, p.Name)
		m.flows = append(m.flows, &hostsetup.Flow{Name: p.Name})
	}
	m.answered = strings.Join(names, ", ")
	m.peers, m.started = nil, true
	m.run(picked)
	return m.tick()
}

// ended takes the engine's result: the summary box, or the error.
func (m *setupModel) ended(r setupEnd) {
	m.finished = true
	m.end <- r
	switch {
	case m.stopped || errors.Is(r.err, context.Canceled):
	case r.bulk != nil:
		m.bulkEnded(r.bulk)
	case r.err != nil:
		m.fail = plainLine(r.err.Error())
	default:
		m.doneBox(r.res)
	}
}

// doneBox is the summary of a machine that is ready.
func (m *setupModel) doneBox(res *hostsetup.Result) {
	link := "up"
	if res.Host != nil && res.Host.RTTMillis > 0 {
		link += ", " + strconv.FormatInt(res.Host.RTTMillis, 10) + " ms"
	}
	tuios := ""
	if p := res.Probe; p != nil {
		switch res.Installed {
		case "":
			tuios = p.TuiosVersion + ", already there"
		case hostsetup.InstallCopy:
			tuios = p.TuiosVersion + ", copied to " + p.Short(p.Tuios)
		default:
			tuios = p.TuiosVersion + ", installed at " + p.Short(p.Tuios)
		}
	}
	daemon := "running"
	for _, st := range res.Steps {
		if st.Step == hostsetup.StepService && st.State == hostsetup.StateOK {
			daemon = "running, starts at boot"
		}
	}
	there := "nothing"
	if rv := res.Reverse; rv != nil && rv.Added {
		there = "[hosts." + rv.Name + "]"
	}
	m.edge = inkEdgeOK
	m.box = []string{mOK + "✓ " + mBold + res.Name + " is ready. Its agents reach your Inbox.", ""}
	keys := [...]string{"Link", "tuios", "Daemon", "Added here", "Added on " + res.Name}
	values := [...]string{link, tuios, daemon, "[hosts." + res.Name + "]", there}
	w := len(keys[4]) + 2
	for i := range keys {
		m.box = append(m.box, "  "+mMute+pad(keys[i], w)+mText+ansi.Truncate(plainLine(values[i]), max(m.width-w-14, 20), "…"))
	}
}

// bulkEnded gives each machine its last line, and makes the summary box.
func (m *setupModel) bulkEnded(res *hostsetup.BulkResult) {
	sm := res.Summary
	m.edge = inkEdgeOK
	switch {
	case sm.Failed > 0:
		m.edge = inkEdgeWarn
	case sm.NeedYou > 0:
		m.edge = inkEdgeAttn
	}
	m.box = []string{mBold + bulkCount(sm)}
	if m.edge == inkEdgeOK {
		m.box[0] = mOK + "✓ " + m.box[0]
	} else {
		m.box = append(m.box, "")
	}
	for _, r := range res.Machines {
		why := plainLine(r.Error)
		end := hostsetup.Line{Step: "end", State: hostsetup.StateFailed, Text: "failed: " + why}
		switch {
		case r.OK:
			end.State, end.Text = hostsetup.StateOK, "ready, link up"
		case r.Person != nil || (r.Failure != nil && r.Failure.NeedsPerson):
			// The reason only: the fix is the command the setup asks again.
			if r.Failure != nil {
				why = plainLine(r.Failure.Reason)
			} else {
				why = plainLine(r.Person.Why)
			}
			end.State, end.Text = hostsetup.StatePerson, "needs you: "+why
			m.box = append(m.box, mAttn+"! "+mText+r.Name+": "+mDim+why+" Fix it, then run "+mText+"tuios hosts setup "+r.Name)
		default:
			m.box = append(m.box, mFail+"✗ "+mText+r.Name+": "+mDim+why)
		}
		f := m.flow(r.Name)
		f.Lines = append(f.Lines, end)
	}
}

// The view.

var spinFrames = [...]string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// mark is the mark of a state: hostsetup.Mark, inked, and the spinner for a
// step that runs.
func (m *setupModel) mark(state string) string {
	ink := mMute
	switch state {
	case hostsetup.StateRun:
		if m.busy() {
			return mAccent + spinFrames[m.spin%len(spinFrames)]
		}
	case hostsetup.StateOK:
		ink = mOK
	case hostsetup.StateFailed:
		ink = mFail
	case hostsetup.StatePerson:
		ink = mAttn
	}
	return ink + hostsetup.Mark(state)
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// ln writes one line of parts. bar starts it with the bar of a question
// block in that ink, when it is not zero.
func (m *setupModel) ln(bar int, parts ...string) {
	if bar != 0 {
		m.b.WriteString("  ")
		m.b.WriteString(inkMark(bar))
		m.b.WriteString("┃ " + mText)
	}
	for _, p := range parts {
		m.b.WriteString(p)
	}
	m.b.WriteString("\n")
}

// hints writes the key hints of a prompt: the key, then what it does.
func (m *setupModel) hints(h [][2]string) {
	m.ln(0)
	m.b.WriteString(" ")
	for _, kv := range h {
		for _, p := range [...]string{" " + mKey, kv[0], mMute + " ", kv[1], "  "} {
			m.b.WriteString(p)
		}
	}
	m.b.WriteString("\n")
}

// line writes one step of a Flow. w is the width of the label column.
func (m *setupModel) line(l hostsetup.Line, label string, w int) {
	name := mText
	switch l.State {
	case hostsetup.StateRun, hostsetup.StateFailed, hostsetup.StatePerson:
		name = mBold
	case hostsetup.StateSkip:
		name = mMute
	}
	text := plainLine(l.Text)
	if l.State == hostsetup.StateFailed && l.Detail != "" {
		text += " What ssh said: " + plainLine(l.Detail)
	}
	bar := ""
	if l.Total > 0 {
		n := int(20 * l.Done / l.Total)
		bar = mAccent + strings.Repeat("━", n) + mTrack + strings.Repeat("─", 20-n) + "  "
		text = strconv.FormatFloat(float64(l.Done)/1e6, 'f', 1, 64) + " of " + strconv.FormatFloat(float64(l.Total)/1e6, 'f', 1, 64) + " MB"
	}
	m.ln(0, "  ", m.mark(l.State), " ", name, pad(label, w), bar, mMute, text)
}

func (m *setupModel) View() tea.View {
	m.b.Reset()
	m.ln(0)
	switch {
	case !m.tailnet:
		f := m.flows[0]
		m.ln(0, "  ", mPill, " "+f.Name+" ", mMute, "  ", m.dest)
		m.ln(0)
		lines := f.Lines
		if len(lines) == 0 && !m.stopped {
			lines = []hostsetup.Line{{Step: hostsetup.StepProbe, State: hostsetup.StateRun, Text: "Open a connection."}}
		}
		w := 16
		for _, l := range lines {
			w = max(w, ansi.StringWidth(hostsetup.StepLabel(l.Step, f.Name))+2)
		}
		for _, l := range lines {
			m.line(l, hostsetup.StepLabel(l.Step, f.Name), w)
		}
	case m.listing:
		m.ln(0, "  ", m.mark(hostsetup.StateRun), " ", mBold, "Read your tailnet.", mMute, "  Each machine gets one short test over ssh.")
		m.hints([][2]string{{"ctrl+c", "stop"}})
		return m.view()
	case m.listErr != nil:
		return m.view()
	default:
		m.ln(0, "  ", mBold, "Your tailnet: ", strconv.Itoa(m.total), " machines.")
		m.ln(0)
		if m.peers == nil {
			m.machines()
		}
	}
	switch {
	case m.ask != nil:
		m.ln(0)
		m.askView(m.ask)
	case m.peers != nil:
		m.checklist()
	case len(m.box) > 0:
		m.ln(0)
		m.boxView()
		if m.waits != nil && m.waits.Load() {
			// configWaitsNote in two lines, so a narrow pane does not cut it.
			m.ln(0)
			m.ln(0, "  ", mDim, "The running daemon applies this change after tuios config apply")
			m.ln(0, "  ", mDim, "from a terminal outside tuios, or a daemon restart.")
		}
		if !m.tailnet {
			m.ln(0)
			m.ln(0, "  ", mMute, "Open a session: ", mDim, "tuios attach --host ", m.flows[0].Name)
		}
	case m.fail != "":
		m.ln(0)
		m.ln(inkFail, mBold, m.fail)
	case !m.finished && !m.stopped && !m.execing:
		m.hints([][2]string{{"ctrl+c", "stop"}})
	}
	return m.view()
}

// bulkSteps order the steps of a machine for its bar.
var bulkSteps = [...]string{hostsetup.StepProbe, hostsetup.StepInstall, hostsetup.StepService, hostsetup.StepStart, hostsetup.StepAdd, hostsetup.StepTest, hostsetup.StepReverse, "end"}

// machines writes the shared answers, then one line for each machine of a
// tailnet run: its last step, a bar, and what that step said.
func (m *setupModel) machines() {
	c := m.choices
	how := installWords(c.Install) + ", not at boot"
	if *c.Service {
		how = installWords(c.Install) + ", start at boot"
	}
	if *c.BothWays {
		how += ", both ways"
	} else {
		how += ", one way"
	}
	m.ln(0, "  ", mKey, "◆ ", mDim, pad("Set up", 16), mText, m.answered)
	m.ln(0, "  ", mKey, "◆ ", mDim, pad("Answers", 16), mText, how)
	m.ln(0)
	w := 12
	for _, f := range m.flows {
		w = max(w, len(f.Name)+2)
	}
	for _, f := range m.flows {
		l := hostsetup.Line{State: hostsetup.StateRun, Text: "waits for its turn"}
		if len(f.Lines) > 0 {
			l = f.Lines[len(f.Lines)-1]
		}
		// The bar shows how far the machine got: a machine that did not
		// end ready keeps the bar of the last step it ran.
		at := l.Step
		if at == "end" && l.State != hostsetup.StateOK {
			at = ""
			if k := len(f.Lines) - 2; k >= 0 {
				at = f.Lines[k].Step
			}
		}
		n := 0
		for i, s := range bulkSteps {
			if s == at {
				n = 20 * (i + 1) / len(bulkSteps)
			}
		}
		ink, name := mMute, mBold
		switch l.State {
		case hostsetup.StatePerson:
			ink = mAttn
		case hostsetup.StateFailed:
			ink = mFail
		case hostsetup.StateOK:
			name = mText
		}
		bar := mAccent
		if l.Step == "end" && l.State == hostsetup.StateOK {
			bar = mOK
		}
		m.ln(0, "  ", m.mark(l.State), " ", name, pad(f.Name, w), bar, strings.Repeat("━", n), mTrack, strings.Repeat("─", 20-n), "  ", ink, plainLine(l.Text))
	}
}

// view paints the lines. A line never wraps: a path or ssh's words can be
// longer than the terminal, and a wrapped line breaks the columns under it.
func (m *setupModel) view() tea.View {
	l := strings.Split(m.b.String(), "\n")
	for i := range l {
		l[i] = ansi.Truncate(m.paint(l[i]), max(m.width-1, 20), "…")
	}
	return tea.NewView(strings.Join(l, "\n"))
}

// boxView writes the summary in a rounded box.
func (m *setupModel) boxView() {
	w := 0
	for _, s := range m.box {
		w = max(w, ansi.StringWidth(s))
	}
	edge := "  " + inkMark(m.edge)
	side := edge + "│" + strings.Repeat(" ", w+6) + "│"
	m.ln(0, edge, "╭", strings.Repeat("─", w+6), "╮")
	m.ln(0, side)
	for _, s := range m.box {
		m.ln(0, edge, "│   ", pad(s, w), "   ", inkMark(m.edge), "│")
	}
	m.ln(0, side)
	m.ln(0, edge, "╰", strings.Repeat("─", w+6), "╯")
}

// cmdLine writes one command of a question block, and where it runs.
func (m *setupModel) cmdLine(bar int, c hostsetup.Command, indent string) {
	m.ln(bar, indent, mMute, pad(c.Where+":", 11), mDim, plainLine(c.Line))
}

// askView writes the open question and its key hints.
func (m *setupModel) askView(p *hostsetup.Prompt) {
	// A prompt of a tailnet names its machine, also before the run, when
	// there is no Flow yet.
	who := p.Machine
	if who == "" {
		who = m.flows[0].Name
	}
	switch p.Kind {
	case hostsetup.AskPick:
		head := "Pick how to install tuios on " + who + "."
		if m.local[p] {
			head = "How to install tuios on " + who + "?"
		}
		for _, l := range m.flow(p.Machine).Lines {
			if l.Step == hostsetup.StepProbe && strings.Contains(l.Text, "is not installed") {
				head = "tuios is not installed on " + who + ". Pick how to install it."
			}
		}
		m.ln(inkAccent, mBold, head)
		m.ln(inkAccent)
		for i, o := range p.Options {
			label := strings.TrimSpace(o.Label)
			switch {
			case !o.Available:
				m.ln(inkAccent, "  ", mMute, label, " ", o.WhyNot)
			case i == p.Cursor:
				fill := strings.Repeat(" ", max(m.width-9-ansi.StringWidth(label+o.Note), 0))
				m.ln(inkAccent, mSelBold, "> ", label, mSel, " ", o.Note, fill)
			default:
				m.ln(inkAccent, "  ", label, mMute, " ", o.Note)
			}
			if o.Available && (i == p.Cursor || p.All) {
				for _, c := range o.Commands {
					m.cmdLine(inkAccent, c, "    ")
				}
			}
		}
	case hostsetup.AskConfirm:
		q := p.Question
		m.ln(inkAccent, mBold, q.Text)
		if q.Note != "" {
			m.ln(inkAccent, mMute, q.Note)
		}
		for _, c := range q.Commands {
			m.cmdLine(inkAccent, c, "")
		}
		yes, no := mOff, mPill
		if p.Yes {
			yes, no = mPill, mOff
		}
		m.ln(inkAccent)
		m.ln(inkAccent, yes, "  Yes  ", mText, "  ", no, "  No  ")
	case hostsetup.AskPerson:
		m.ln(inkAttn, mBold, plainLine(p.Person.Why))
		if p.Person.URL != "" {
			m.ln(inkAttn, plainLine(p.Person.URL))
			m.ln(inkAttn, mDim, m.note)
		}
	case hostsetup.AskFix:
		f := p.Failure
		m.ln(inkFail, mBold, plainLine(f.Reason))
		m.ln(inkFail, mDim, plainLine(f.Fix))
		if f.FixCommand != "" || f.URL != "" {
			m.ln(inkFail)
			m.ln(inkFail, mChip, " $ ", plainLine(cmp.Or(f.FixCommand, f.URL))+" ")
		}
		if f.SSHSaid != "" {
			m.ln(inkFail)
			m.ln(inkFail, mMute, "What ssh said: ", plainLine(f.SSHSaid))
		}
	}
	m.hints(p.Hints())
}

// checklist writes the tailnet's machines and the shared answers.
func (m *setupModel) checklist() {
	p := m.peers
	ticked := 0
	nameW, osW := 12, 6
	for i, peer := range p.Peers {
		if p.Ticked[i] {
			ticked++
		}
		nameW, osW = max(nameW, len(peer.Name)+2), max(osW, len(peer.OS)+2)
	}
	m.ln(inkAccent, mBold, "Pick the machines to set up.", mMute, "  ", strconv.Itoa(ticked), " ticked")
	m.ln(inkAccent)
	for i, peer := range p.Peers {
		lead, name, box, state := "  ", mText, mMute+"[ ]", mMute
		if i == p.Cursor {
			lead, name = mKey+"> ", mBold
		}
		if p.Ticked[i] {
			box = mKey + "[x]"
		}
		switch peer.State {
		case hostsetup.PeerOlder:
			state = mInfo
		case hostsetup.PeerSignIn, hostsetup.PeerSSHFailed:
			state = mAttn
		}
		if !peer.CanSetUp {
			box, name, state = "   ", mMute, mMute
		}
		own := ""
		if m.own[peer.Name] != nil {
			own = mAccent + ", own answers"
		}
		m.ln(inkAccent, lead, box, " ", name, pad(peer.Name, nameW), mDim, pad(peer.OS, osW), state, plainLine(peer.Why), own)
	}
	if len(p.Peers) == 0 {
		m.ln(inkAccent, mMute, "No machine on your tailnet can be set up.")
	}
	if m.added != "" {
		m.ln(inkAccent)
		m.ln(inkAccent, "  ", mMute, "Already added: ", mDim, m.added)
	}
	c := m.choices
	m.ln(0)
	m.ln(0, "  ", mDim, "The same answers for each ticked machine")
	m.ln(0, "    ", mMute, "Install        ", mText, installWords(c.Install))
	m.ln(0, "    ", mMute, "Start at boot  ", mText, yesNo(*c.Service))
	m.ln(0, "    ", mMute, "Both ways      ", mText, yesNo(*c.BothWays))
	m.hints(p.Hints())
}

// installWords names an install choice in the shared answers.
func installWords(id string) string {
	switch id {
	case hostsetup.InstallRelease:
		return "the release of this version"
	case hostsetup.InstallPkg:
		return "its package manager"
	case hostsetup.InstallScript:
		return "the install script"
	case hostsetup.InstallNone:
		return "no install"
	}
	return "the same version as here"
}
