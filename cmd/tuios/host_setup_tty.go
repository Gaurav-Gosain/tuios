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
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	setupEndMsg  struct{}
	setupExecMsg struct {
		p   *hostsetup.Prompt
		err error
	}
	setupTickMsg struct{}
)

// setupEnd is what the engine returned.
type setupEnd struct {
	res *hostsetup.Result
	err error
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

// setupModel draws one machine's Flow and the Prompt that waits.
type setupModel struct {
	cancel context.CancelFunc
	end    chan setupEnd
	ink    [inkN]string
	width  int
	// b is the frame being written.
	b strings.Builder

	dest string
	flow hostsetup.Flow
	// home is the far HOME the probe read, so a path under it shows as ~.
	home string

	ask     *hostsetup.Prompt
	queue   []*hostsetup.Prompt
	note    string
	execing bool

	spin    int
	ticking bool

	finished, stopped bool
	// box is the summary at the end, edge its ink, and fail the error.
	box  []string
	edge int
	fail string
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
func runHostSetupTTY(s *hostsetup.Setup, dest string, o hostSetupOptions) error {
	m, ctx := newSetupModel()
	m.dest, m.flow.Name = dest, s.Name()
	prog := tea.NewProgram(m, tea.WithOutput(os.Stdout))
	go func() {
		res, err := s.Run(ctx, o.choices(), &hostsetup.Bridge{Send: send(prog)})
		m.end <- setupEnd{res: res, err: err}
		prog.Send(setupEndMsg{})
	}()
	return m.finish(prog)
}

// finish runs the program, then waits for the engine. The program can end
// first (ctrl+c, SIGTERM): the engine then stops at its next step boundary,
// and the stop line says what it left.
func (m *setupModel) finish(prog *tea.Program) error {
	_, perr := prog.Run()
	m.cancel()
	r := <-m.end
	switch {
	case m.stopped || perr != nil || errors.Is(r.err, context.Canceled):
		fmt.Println(stoppedLine(r.res))
		return &statusError{code: 130}
	case r.err != nil:
		return &statusError{code: setupExit(r.err).(*diagnosticError).ExitStatus()}
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

func (m *setupModel) Init() tea.Cmd { return nil }

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
	for _, l := range m.flow.Lines {
		if l.State == hostsetup.StateRun {
			return true
		}
	}
	return false
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = min(msg.Width, 110)
	case setupTickMsg:
		m.ticking = false
		m.spin++
	case hostsetup.BridgeEvent:
		m.flow.Apply(msg.Event)
		if msg.Probe != nil {
			m.home = msg.Probe.Home
		}
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
			err = fmt.Errorf("%s ended with an error: %w", msg.p.Person.Line, err)
		}
		return m, m.done(err)
	case setupEndMsg:
		m.ended(<-m.end)
		return m, tea.Quit
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
	case m.ask != nil && m.ask.Kind == hostsetup.AskPerson:
		if k == "t" {
			return m.done(nil)
		}
	case m.ask != nil:
		if m.ask.Key(k) {
			m.ask = nil
			return tea.Batch(m.next(), m.tick())
		}
	}
	return nil
}

// ended takes the engine's result: the summary box, or the error.
func (m *setupModel) ended(r setupEnd) {
	m.finished = true
	m.end <- r
	switch {
	case m.stopped || errors.Is(r.err, context.Canceled):
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

// text is a line's text for the screen: one line, and the far HOME as ~.
func (m *setupModel) text(s string) string {
	s = plainLine(s)
	if m.home != "" && m.home != "/" {
		s = strings.ReplaceAll(s, m.home+"/", "~/")
	}
	return s
}

// line writes one step of a Flow. w is the width of the label column.
func (m *setupModel) line(l hostsetup.Line, w int) {
	name := mText
	switch l.State {
	case hostsetup.StateRun, hostsetup.StateFailed, hostsetup.StatePerson:
		name = mBold
	case hostsetup.StateSkip:
		name = mMute
	}
	text := m.text(l.Text)
	if l.State == hostsetup.StateFailed && l.Detail != "" {
		text += " What ssh said: " + m.text(l.Detail)
	}
	bar := ""
	if l.Total > 0 {
		n := int(20 * l.Done / l.Total)
		bar = mAccent + strings.Repeat("━", n) + mTrack + strings.Repeat("─", 20-n) + "  "
		text = strconv.FormatFloat(float64(l.Done)/1e6, 'f', 1, 64) + " of " + strconv.FormatFloat(float64(l.Total)/1e6, 'f', 1, 64) + " MB"
	}
	m.ln(0, "  ", m.mark(l.State), " ", name, pad(l.Label, w), bar, mMute, text)
}

func (m *setupModel) View() tea.View {
	m.b.Reset()
	m.ln(0)
	f := &m.flow
	m.ln(0, "  ", mPill, " "+f.Name+" ", mMute, "  ", m.dest)
	m.ln(0)
	lines := f.Lines
	if len(lines) == 0 && !m.stopped {
		lines = []hostsetup.Line{{Step: hostsetup.StepProbe, State: hostsetup.StateRun, Text: "Open a connection."}}
	}
	w := 16
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l.Label)+2)
	}
	for _, l := range lines {
		if l.Label == "" {
			l.Label = hostsetup.StepLabel(l.Step, f.Name)
		}
		m.line(l, w)
	}
	switch {
	case m.ask != nil:
		m.ln(0)
		m.askView(m.ask)
	case len(m.box) > 0:
		m.ln(0)
		m.boxView()
		m.ln(0)
		m.ln(0, "  ", mMute, "Open a session: ", mDim, "tuios attach --host ", f.Name)
	case m.fail != "":
		m.ln(0)
		m.ln(inkFail, mBold, m.fail)
	case !m.finished && !m.stopped && !m.execing:
		m.hints([][2]string{{"ctrl+c", "stop"}})
	}
	return m.view()
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
	m.ln(bar, indent, mMute, pad(c.Where+":", 11), mDim, m.text(c.Line))
}

// askView writes the open question and its key hints.
func (m *setupModel) askView(p *hostsetup.Prompt) {
	who := m.flow.Name
	switch p.Kind {
	case hostsetup.AskPick:
		head := "Pick how to install tuios on " + who + "."
		for _, l := range m.flow.Lines {
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
		m.ln(inkAttn, mBold, m.text(p.Person.Why))
		if p.Person.URL != "" {
			m.ln(inkAttn, m.text(p.Person.URL))
			m.ln(inkAttn, mDim, m.note)
		}
		m.hints([][2]string{{"t", "test again"}, {"ctrl+c", "stop"}})
		return
	case hostsetup.AskFix:
		f := p.Failure
		m.ln(inkFail, mBold, m.text(f.Reason))
		m.ln(inkFail, mDim, m.text(f.Fix))
		if f.FixCommand != "" || f.URL != "" {
			m.ln(inkFail)
			m.ln(inkFail, mChip, " $ ", m.text(cmp.Or(f.FixCommand, f.URL))+" ")
		}
		if f.SSHSaid != "" {
			m.ln(inkFail)
			m.ln(inkFail, mMute, "What ssh said: ", m.text(f.SSHSaid))
		}
	}
	m.hints(p.Hints())
}
