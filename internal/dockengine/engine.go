package dockengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/crashexit"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Update is one component's new state, travelling from an engine
// goroutine to the model.
type Update struct {
	Name string

	// Builtin marks this as a due notice rather than a value. A built-in's
	// refresh reads model state (the window list, the CPU history), which only
	// the Bubble Tea goroutine may touch, so the engine says when and the model
	// does the work.
	Builtin bool

	Text string
	Exit int
	Err  string

	// From is the engine that sent the update, set by the listener. A reload
	// replaces the engine, and an update from the old one is not applied to
	// the new one.
	From *Engine
}

// Component is one cell's definition and its live state.
type Component struct {
	Name     string // "cpu", or "custom/git" for a user's own
	Builtin  bool
	Command  string
	OnClick  string
	MaxWidth int
	Refresh  config.DockRefresh

	// MultiLine keeps every line of stdout, newline separated, rather than
	// the first. The rail's custom section draws one row per line; a dock
	// cell has one line to draw.
	MultiLine bool
	// Coalesce keeps one pending re-run for any trigger that lands while the
	// command is running, an interval's included. The rail's rows are about the
	// focused pane, and a focus change dropped mid-run would leave the old
	// pane's text on screen until the next event. Every component refreshed by
	// an event keeps its re-run without this (see fire).
	Coalesce bool

	text     string
	lastRun  time.Time
	lastExit int
	lastErr  string
	failures int
	nextDue  time.Time
	running  bool
	stopped  bool
	reported bool
	pending  bool

	revive chan struct{}
}

// Engine schedules the dock's components. One per client: components draw
// in the client, so they run in the client.
type Engine struct {
	mu    sync.Mutex
	comps map[string]*Component
	order []string

	updates chan Update

	wake chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	session string
	socket  string

	rail          RailContext
	secondaryRail RailContext

	folder func(RailContext) string

	wakes atomic.Int64

	eventDue map[string]time.Time
}

const dockEngineUpdateBuffer = 64

const dockEventDebounce = 200 * time.Millisecond

// RailContext is what the rail section's command is told about where it
// draws and what has the focus.
//
// The pane's folder is carried as the model holds it, not as the command is
// told it. Turning it into a folder means a stat, and the model builds this
// on every message on the update goroutine, where a pane sitting in a hung
// network folder would freeze the client. So the run resolves it when it
// starts, on its own goroutine (RailContext.folder).
type RailContext struct {
	Width, Height int
	PaneID        string
	// PaneDir is the folder the pane last reported (OSC 7), unparsed, or
	// empty when it never reported one.
	PaneDir string
	// PanePgid is the pane's shell, for a local pane that reported no folder:
	// its working directory is the fallback, read at run start.
	PanePgid int
	// Remote says the session is on another machine, so PaneDir is a path
	// there and nothing on this machine can check it.
	Remote bool
}

// New builds the engine for a set of components and starts the
// scheduler. The scheduler goroutine exists even with nothing to schedule: it
// parks on a receive, costing one goroutine and no timer, which is what keeps
// refresh-dock and event refreshes working on a dock that never polls.
//
// folder resolves the focused pane's folder for the rail section's command
// (TUIOS_ACTIVE_PANE_CWD). It is the host's: telling a local folder from a
// remote one is the client's business. It runs on the run's goroutine, and
// nil gives an empty folder.
func New(comps []*Component, folder func(RailContext) string) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		folder:   folder,
		comps:    make(map[string]*Component, len(comps)),
		updates:  make(chan Update, dockEngineUpdateBuffer),
		wake:     make(chan struct{}, 1),
		ctx:      ctx,
		cancel:   cancel,
		eventDue: map[string]time.Time{},
	}
	for _, c := range comps {
		if _, dup := e.comps[c.Name]; dup {
			continue
		}
		if c.Refresh.Kind == config.DockRefreshPush {
			c.revive = make(chan struct{}, 1)
		}
		e.comps[c.Name] = c
		e.order = append(e.order, c.Name)
	}
	go e.schedule()
	return e
}

// Stop tears the engine down. Every command it started is killed with it: the
// components are UI, and UI that outlives the client that drew it is a leak.
func (e *Engine) Stop() {
	if e == nil {
		return
	}
	e.cancel()
}

// SetContext records the session name and socket path handed to every command.
func (e *Engine) SetContext(session, socket string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.session, e.socket = session, socket
	e.mu.Unlock()
}

// SetRailContext records what the next rail run is told. Read under the lock
// in the hold that starts the run, so a run always sees the values the model
// had when it started, and the width it is skipped for is the width it would
// have been told.
func (e *Engine) SetRailContext(ctx RailContext) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.rail = ctx
	e.mu.Unlock()
}

func (e *Engine) SetSecondaryRailContext(ctx RailContext) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.secondaryRail = ctx
	e.mu.Unlock()
}

// Updates is the channel the model listens on.
func (e *Engine) Updates() <-chan Update {
	if e == nil {
		return nil
	}
	return e.updates
}

// Wakes is how many times the engine has had something to say. The idle guard
// asserts this stays at zero for a dock made only of once, push and event
// components.
func (e *Engine) Wakes() int64 {
	if e == nil {
		return 0
	}
	return e.wakes.Load()
}

// Text is a component's current cell text.
func (e *Engine) Text(name string) string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.comps[name]; ok {
		return c.text
	}
	return ""
}

// Component returns a copy of one component's definition and state.
func (e *Engine) Component(name string) (Component, bool) {
	if e == nil {
		return Component{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.comps[name]
	if !ok {
		return Component{}, false
	}
	return *c, true
}

// Snapshot copies every component in definition order, for list-dock-components.
func (e *Engine) Snapshot() []Component {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Component, 0, len(e.order))
	for _, name := range e.order {
		if c, ok := e.comps[name]; ok {
			out = append(out, *c)
		}
	}
	return out
}

// Names lists the components the engine holds, sorted.
func (e *Engine) Names() []string {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := append([]string(nil), e.order...)
	sort.Strings(out)
	return out
}

// Start runs the initial fill and spawns the push readers. It is separate from
// construction so a test can build an engine and inspect its plan without any
// subprocess running.
func (e *Engine) Start() {
	if e == nil {
		return
	}
	e.mu.Lock()
	var initial, pushes []*Component
	for _, name := range e.order {
		c := e.comps[name]
		switch c.Refresh.Kind {
		case config.DockRefreshPush:
			pushes = append(pushes, c)
		case config.DockRefreshInterval:
			c.nextDue = e.alignedDeadline(c, time.Now())
			initial = append(initial, c)
		default:
			initial = append(initial, c)
		}
	}
	e.mu.Unlock()

	for _, c := range pushes {
		go e.readPushed(c)
	}
	for _, c := range initial {
		if !c.Builtin {
			go e.runOnce(c)
		}
	}
	e.Replan()
}

func (e *Engine) alignedDeadline(c *Component, now time.Time) time.Time {
	d := c.Refresh.Interval
	if d <= 0 {
		return now
	}
	if d == time.Second || d == time.Minute {
		return now.Truncate(d).Add(d)
	}
	return now.Add(d)
}

// Replan nudges the scheduler to recompute the earliest deadline. Non-blocking:
// the channel holds one, and one pending nudge is as good as ten.
func (e *Engine) Replan() {
	if e == nil {
		return
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

const dockWallCheck = 15 * time.Second

func (e *Engine) schedule() {
	defer crashexit.Recover()
	for {
		next, due := e.earliest()
		if next == nil {
			select {
			case <-e.wake:
				continue
			case <-e.ctx.Done():
				return
			}
		}

		timer := time.NewTimer(min(time.Until(due), dockWallCheck))
		select {
		case <-timer.C:
			if time.Until(due) > 0 {
				continue
			}
			e.fire(next)
		case <-e.wake:
			timer.Stop()
		case <-e.ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (e *Engine) earliest() (*Component, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var best *Component
	var bestAt time.Time
	consider := func(c *Component, at time.Time) {
		if best == nil || at.Before(bestAt) {
			best, bestAt = c, at
		}
	}
	for _, name := range e.order {
		c := e.comps[name]
		if c.stopped {
			continue
		}
		if c.Refresh.Kind == config.DockRefreshInterval && !c.nextDue.IsZero() {
			consider(c, c.nextDue)
		}
		if at, pending := e.eventDue[name]; pending {
			consider(c, at)
		}
	}
	return best, bestAt
}

func (e *Engine) fire(c *Component) {
	e.mu.Lock()
	delete(e.eventDue, c.Name)
	if c.Refresh.Kind == config.DockRefreshInterval {
		c.nextDue = e.alignedDeadline(c, time.Now())
	}
	builtin, running := c.Builtin, c.running
	if running && !builtin && (c.Coalesce || c.Refresh.Kind == config.DockRefreshEvent) {
		c.pending = true
	}
	e.mu.Unlock()

	if builtin {
		e.emit(Update{Name: c.Name, Builtin: true})
		return
	}
	if running {
		return
	}
	go e.runOnce(c)
}

// NotifyEvent marks every component watching this event type as due, debounced.
// Cost at idle is zero: events only arrive when something happened, and
// something happening is when the frame redraws anyway.
func (e *Engine) NotifyEvent(eventType string) {
	if e == nil || eventType == "" {
		return
	}
	at := time.Now().Add(dockEventDebounce)
	changed := false
	e.mu.Lock()
	for _, name := range e.order {
		c := e.comps[name]
		if c.stopped || c.Refresh.Kind != config.DockRefreshEvent {
			continue
		}
		matched := slices.Contains(c.Refresh.Events, eventType)
		if !matched {
			continue
		}
		if _, pending := e.eventDue[name]; pending {
			continue
		}
		e.eventDue[name] = at
		changed = true
	}
	e.mu.Unlock()
	if changed {
		e.Replan()
	}
}

// Refresh re-runs one component now, whatever its refresh mode says, and clears
// a give-up so a fixed script recovers without a restart. This is what the
// refresh-dock verb calls, and it is what makes a component scriptable from a
// hook, from cron, and from an agent.
func (e *Engine) Refresh(name string) error {
	if e == nil {
		return errfmt.Errorf("no dock components are loaded")
	}
	e.mu.Lock()
	c, ok := e.comps[name]
	if !ok {
		e.mu.Unlock()
		return errfmt.Errorf("no dock component named %q", name)
	}
	c.stopped, c.failures, c.reported = false, 0, false
	builtin, push := c.Builtin, c.Refresh.Kind == config.DockRefreshPush
	e.mu.Unlock()

	switch {
	case builtin:
		e.emit(Update{Name: name, Builtin: true})
	case push:
		select {
		case c.revive <- struct{}{}:
		default:
		}
	default:
		go e.runOnce(c)
	}
	return nil
}

// Rerun asks for one more run of a component, debounced and fired the way an
// event is, so a run in flight keeps it as the one pending re-run rather than
// dropping it. Unlike Refresh it leaves a give-up alone: it is the client
// asking, not a person, and only a person asking clears a stop. The rail calls
// it when it opens, because the runs it skipped while shut left its rows
// behind the focus.
func (e *Engine) Rerun(name string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	c, ok := e.comps[name]
	_, due := e.eventDue[name]
	wake := ok && !c.stopped && !due
	if wake {
		e.eventDue[name] = time.Now().Add(dockEventDebounce)
	}
	e.mu.Unlock()
	if wake {
		e.Replan()
	}
}

// RefreshAll re-runs every component. Used by the verb with no argument and by
// a config reload that kept the same component set.
func (e *Engine) RefreshAll() {
	for _, name := range e.Names() {
		_ = e.Refresh(name)
	}
}

func (e *Engine) emit(u Update) {
	e.wakes.Add(1)
	select {
	case e.updates <- u:
	case <-e.ctx.Done():
	}
}

// CommandEnv is the environment a component's command runs in: the session's
// own, plus the three variables that tell the command what it is and how to
// talk back. Same shape as a hook, deliberately, because a component is a hook
// that draws.
func (e *Engine) CommandEnv(name string, extra ...string) []string {
	env := append(e.baseEnv(), "TUIOS_DOCK_COMPONENT="+strings.TrimPrefix(name, config.DockCustomPrefix))
	return append(env, extra...)
}

func (e *Engine) baseEnv() []string {
	e.mu.Lock()
	session, socket := e.session, e.socket
	e.mu.Unlock()
	return append(os.Environ(), "TUIOS_SESSION="+session, "TUIOS_SOCKET="+socket)
}

const dockKillGrace = 250 * time.Millisecond

func dockSupervise(cmd *exec.Cmd) {
	dockGroupCommand(cmd)
	cmd.Cancel = func() error { return dockKillGroup(cmd) }
	cmd.WaitDelay = dockKillGrace
}

func (e *Engine) railCommandEnv(rail RailContext) []string {
	env := e.baseEnv()
	folder := ""
	if e.folder != nil {
		folder = e.folder(rail)
	}
	return append(env,
		"TUIOS_RAIL_SECTION="+config.SidebarSectionCustom,
		"TUIOS_RAIL_WIDTH="+strconv.Itoa(rail.Width),
		"TUIOS_RAIL_HEIGHT="+strconv.Itoa(rail.Height),
		"TUIOS_ACTIVE_PANE_ID="+rail.PaneID,
		"TUIOS_ACTIVE_PANE_CWD="+folder,
	)
}

func (e *Engine) runOnce(c *Component) {
	defer crashexit.Recover()
	e.mu.Lock()
	if c.running {
		e.mu.Unlock()
		return
	}
	var rail RailContext
	if c.Name == RailComponent || c.Name == RailSecondaryComponent {
		rail = e.rail
		if c.Name == RailSecondaryComponent {
			rail = e.secondaryRail
		}
		if rail.Width <= 0 {
			e.mu.Unlock()
			return
		}
	}
	c.running = true
	command := c.Command
	e.mu.Unlock()

	ctx, cancel := context.WithTimeout(e.ctx, config.DockCustomTimeout)
	defer cancel()

	// #nosec G204 - the command is the user's own config, run as the user, on
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if c.Name == RailComponent || c.Name == RailSecondaryComponent {
		cmd.Env = e.railCommandEnv(rail)
	} else {
		cmd.Env = e.CommandEnv(c.Name)
	}
	cmd.Stdin = nil
	cmd.Stderr = nil
	dockSupervise(cmd)

	out := &dockCappedWriter{limit: config.DockCustomMaxOutput + 1, kill: func() { _ = dockKillGroup(cmd) }}
	cmd.Stdout = out
	err := cmd.Run()

	text := FirstLine
	if c.MultiLine {
		text = Lines
	}
	update := Update{Name: c.Name}
	switch {
	case out.overflowed:
		update.Text = text(out.buf[:min(len(out.buf), config.DockCustomMaxOutput)])
	case ctx.Err() == context.DeadlineExceeded:
		update.Exit = -1
		update.Err = fmt.Sprintf("timed out after %s", config.DockCustomTimeout)
	case err != nil:
		update.Exit = 1
		if exit, ok := err.(*exec.ExitError); ok {
			update.Exit = exit.ExitCode()
		}
		update.Err = err.Error()
	default:
		update.Text = text(out.buf)
	}

	e.mu.Lock()
	c.running = false
	rerun := c.pending
	c.pending = false
	e.mu.Unlock()

	if e.ctx.Err() != nil {
		return
	}
	e.emit(update)
	if rerun {
		go e.runOnce(c)
	}
}

type dockCappedWriter struct {
	limit      int
	buf        []byte
	overflowed bool
	kill       func()
}

var errDockOutputCap = errors.New("dock component output passed the cap")

func (w *dockCappedWriter) Write(p []byte) (int, error) {
	if w.overflowed {
		return 0, errDockOutputCap
	}
	room := w.limit - len(w.buf)
	if len(p) <= room {
		w.buf = append(w.buf, p...)
		return len(p), nil
	}
	w.buf = append(w.buf, p[:room]...)
	w.overflowed = true
	w.kill()
	return room, errDockOutputCap
}

func (e *Engine) readPushed(c *Component) {
	defer crashexit.Recover()
	backoff := 500 * time.Millisecond
	failures := 0

	for e.ctx.Err() == nil {
		e.mu.Lock()
		command, stopped := c.Command, c.stopped
		e.mu.Unlock()
		if stopped {
			select {
			case <-c.revive:
				continue
			case <-e.ctx.Done():
				return
			}
		}

		// #nosec G204 - the user's own config, as above.
		cmd := exec.CommandContext(e.ctx, "sh", "-c", command)
		cmd.Env = e.CommandEnv(c.Name)
		cmd.Stderr = nil
		dockSupervise(cmd)
		pipe, err := cmd.StdoutPipe()
		lines := 0
		if err == nil && cmd.Start() == nil {
			lines = e.drainPushed(c, pipe)
			_ = cmd.Wait()
		} else if err != nil {
			e.emit(Update{Name: c.Name, Exit: -1, Err: err.Error()})
		}

		if lines > 0 {
			backoff, failures = 500*time.Millisecond, 0
		} else {
			failures++
			if failures >= config.DockCustomFailureLimit {
				e.mu.Lock()
				c.stopped = true
				c.lastErr = fmt.Sprintf("exited %d times without output; not restarting", failures)
				e.mu.Unlock()
				e.emit(Update{Name: c.Name, Exit: -1, Err: "exited without output; not restarting"})
				continue
			}
		}

		select {
		case <-time.After(backoff):
		case <-e.ctx.Done():
			return
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
}

func (e *Engine) drainPushed(c *Component, pipe io.Reader) int {
	buf := make([]byte, 4096)
	acc := make([]byte, 0, 4096)
	lines := 0
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			acc = append(acc, buf[:n]...)
			for {
				idx := indexByte(acc, '\n')
				if idx < 0 {
					break
				}
				line := string(acc[:idx])
				acc = acc[idx+1:]
				lines++
				e.emit(Update{Name: c.Name, Text: Sanitize(line)})
			}
			if len(acc) > config.DockCustomMaxOutput {
				acc = acc[len(acc)-config.DockCustomMaxOutput:]
			}
		}
		if err != nil {
			return lines
		}
		if e.ctx.Err() != nil {
			return lines
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// ApplyUpdate folds one update into the component's state and reports whether
// the cell text moved and whether this is a failure the user has not been told
// about yet.
//
// The render gate lives here: a poll whose value has not changed produces no
// frame. A one-second component watching a number that moves every five minutes
// costs sixty executions an hour and about zero frames.
func (e *Engine) ApplyUpdate(u Update) (changed, newFailure bool) {
	if e == nil {
		return false, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.comps[u.Name]
	if !ok {
		return false, false
	}
	c.lastRun = time.Now()
	c.lastExit = u.Exit
	c.lastErr = u.Err

	text := u.Text
	if u.Err != "" {
		text = ""
		c.failures++
		if c.failures == 1 && !c.reported {
			c.reported, newFailure = true, true
		}
		if c.failures >= config.DockCustomFailureLimit && c.Refresh.Kind != config.DockRefreshPush {
			c.stopped = true
		}
	} else {
		c.failures, c.reported = 0, false
	}
	trimmed := text
	if !c.MultiLine {
		trimmed = TruncateCell(text, c.MaxWidth)
	}
	if trimmed != c.text {
		c.text, changed = trimmed, true
	}
	return changed, newFailure
}

// SetBuiltinText records a built-in's rendered value, so the change gate and
// list-dock-components see built-ins exactly as they see custom cells.
func (e *Engine) SetBuiltinText(name, text string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.comps[name]
	if !ok {
		return false
	}
	c.lastRun = time.Now()
	if c.text == text {
		return false
	}
	c.text = text
	return true
}

// FirstLine is the first line of a command's stdout, laundered.
func FirstLine(out []byte) string {
	s := strings.TrimRight(string(out), "\r\n")
	line, _, _ := strings.Cut(s, "\n")
	return Sanitize(strings.TrimRight(line, "\r"))
}

// Lines is every line of a command's stdout, laundered one line at a
// time and joined with newlines. A rail row keeps its OSC 8 links as well as
// its colour, so a row can name what a click on it opens (issue #2179). Split first and sanitise after, because
// Sanitize drops control characters and a newline is one; a line's
// worth of SGR is kept the same way a cell's is. Trailing blank lines go,
// so a command that ends with a newline does not draw an empty row.
func Lines(out []byte) string {
	s := strings.TrimRight(string(out), "\r\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = sanitize(strings.TrimRight(line, "\r"), true)
	}
	return strings.Join(lines, "\n")
}

// Sanitize keeps printable text and SGR colour, and drops every other
// control sequence.
//
// Colour survives because a meter without it is a worse meter, and because
// polybar and waybar components have always been written with it. Everything
// else goes: a cursor move, a scroll region or an erase written into a dock cell
// would be a component redrawing somebody else's screen. Cell text is drawn on
// the dock's own Panel ground afterwards, so a reset inside it cannot punch a
// transparent hole through the bar.
//
// Below the escape layer it works on runes. The C1 controls, U+0080 to U+009F,
// go: U+009B is a one-byte CSI to a terminal that reads them, and the rest are
// controls of the same family. A byte that is not UTF-8 goes too, since a raw
// 0x9B is that same CSI. The invisible characters go through internal/invisible,
// as everywhere else tuios shows another program's text: a bidi override would
// reorder the bar around the cell, and a zero-width character hides text a
// person cannot see. Printable text, wide characters and emoji stay.
func Sanitize(s string) string {
	return sanitize(s, false)
}

const (
	osc8Open  = "\x1b]8;;"
	osc8Close = "\x1b]8;;\x1b\\"
)

func sanitize(s string, links bool) string {
	s = invisible.Strip(s)
	open := false
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
					j++
				}
				if j < len(s) && s[j] == 'm' {
					b.WriteString(s[i : j+1])
					i = j + 1
					continue
				}
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			if i+1 < len(s) && strings.IndexByte("]P_^X", s[i+1]) >= 0 {
				j := i + 2
				end := -1
				for j < len(s) {
					if s[j] == 0x07 {
						end = j
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						end = j
						j += 2
						break
					}
					j++
				}
				if links && s[i+1] == ']' && end >= 0 {
					if uri, ok := osc8Address(s[i+2 : end]); ok {
						switch {
						case uri != "":
							b.WriteString(osc8Open)
							b.WriteString(uri)
							b.WriteString("\x1b\\")
							open = true
						case open:
							b.WriteString(osc8Close)
							open = false
						}
					}
				}
				i = j
				continue
			}
			i += 2
			continue
		}
		if c < 0x20 || c == 0x7f {
			i++
			continue
		}
		if c < utf8.RuneSelf {
			b.WriteByte(c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || (r >= 0x80 && r <= 0x9f) {
			i += size
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	if open {
		b.WriteString(osc8Close)
	}
	return b.String()
}

func osc8Address(payload string) (string, bool) {
	rest, found := strings.CutPrefix(payload, "8;")
	if !found {
		return "", false
	}
	_, uri, found := strings.Cut(rest, ";")
	if !found || !utf8.ValidString(uri) {
		return "", false
	}
	for _, r := range uri {
		if r <= 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return "", false
		}
	}
	return uri, true
}

// RailComponent and RailSecondaryComponent are the engine names of the
// rail's and the secondary rail's custom section commands. They are not under
// the dock's custom/ prefix, so neither is ever drawn as a bar cell, and
// tuios refresh-dock rail/custom reaches the first.
const (
	RailComponent          = "rail/custom"
	RailSecondaryComponent = "rail/secondary/custom"
)

// TruncateCell caps a cell at maxWidth cells, measured as rendered rather
// than in bytes so a component emitting wide runes or colour is cut where it
// looks cut.
func TruncateCell(text string, maxWidth int) string {
	if text == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = config.DockCustomDefaultMaxWidth
	}
	return overlay.Truncate(text, maxWidth)
}

// Output is the component's last text, LastExit and LastErr how its last run
// ended, and Stopped whether the engine gave up on it. They read a copy, such
// as Snapshot's.
func (c Component) Output() string  { return c.text }
func (c Component) LastExit() int   { return c.lastExit }
func (c Component) LastErr() string { return c.lastErr }
func (c Component) Stopped() bool   { return c.stopped }

// LastRun is when the component last ran.
func (c Component) LastRun() time.Time { return c.lastRun }

// Done is closed when the engine stops.
func (e *Engine) Done() <-chan struct{} { return e.ctx.Done() }
