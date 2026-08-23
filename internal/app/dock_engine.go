package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The dock's refresh engine.
//
// The property this file exists to hold is one line long: a dock with no
// interval component arms no timer. Not a cheap timer, none. The scheduler
// below parks on a channel receive when nothing is due, so a default session
// pays for the dock exactly what it paid before there was a dock engine, and
// BenchmarkIdleTick stays where it is.
//
// What replaced what: the bar used to be redrawn sixty times a second whenever
// the clock or either meter was on, because NeedsDockTick pinned the maintenance
// tick to the normal frame rate and marked every one of those frames as needing
// a render. A clock that shows seconds needs one frame per second and the meters
// need one per two seconds, so that was between 60x and 120x more drawing than
// the feature asked for. Here each of them is a component with a deadline, the
// engine wakes for the earliest one, and a value that has not moved draws
// nothing at all.
//
// Custom components are subprocesses, which is the whole of the extension
// contract: environment in, one line of text out. Everything about them that is
// careful is in this file, because a dock cell that hangs the render loop would
// be a catastrophe. They run off the Bubble Tea goroutine, under a timeout,
// reading a bounded amount of output, and a failure hides the cell rather than
// breaking the bar.

// dockComponentUpdate is one component's new state, travelling from an engine
// goroutine to the model.
type dockComponentUpdate struct {
	Name string

	// Builtin marks this as a due notice rather than a value. A built-in's
	// refresh reads model state (the window list, the CPU history), which only
	// the Bubble Tea goroutine may touch, so the engine says when and the model
	// does the work.
	Builtin bool

	Text string
	Exit int
	Err  string
}

// dockComponentMsg carries an update into Update. One message per update keeps
// the render gate simple: the handler compares the new text against the old and
// skips the frame when nothing moved.
type dockComponentMsg dockComponentUpdate

// dockComponent is one cell's definition and its live state.
type dockComponent struct {
	Name     string // "cpu", or "custom/git" for a user's own
	Builtin  bool
	Command  string
	OnClick  string
	MaxWidth int
	Refresh  config.DockRefresh

	// Everything below is guarded by dockEngine.mu.
	text     string
	lastRun  time.Time
	lastExit int
	lastErr  string
	failures int
	nextDue  time.Time
	running  bool
	stopped  bool // gave up after DockCustomFailureLimit consecutive failures
	reported bool // the failure has already been put in front of the user once

	// revive wakes a push reader that has given up. It is a channel rather than
	// a retry interval because a reader waiting on a timer is a timer, and the
	// whole point of this engine is that nothing holds one without being asked
	// to. A push component that cannot start is revived by refresh-dock and by
	// nothing else.
	revive chan struct{}
}

// dockEngine schedules the dock's components. One per client: components draw
// in the client, so they run in the client.
type dockEngine struct {
	mu    sync.Mutex
	comps map[string]*dockComponent
	order []string // definition order, so listings are stable

	updates chan dockComponentUpdate

	// wake re-plans the scheduler after the component set or a deadline
	// changes. It is a channel rather than a timer because parking on a receive
	// is what "no timer at idle" means.
	wake chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	// session and socket are handed to every command, the way hooks are given
	// their context. Guarded by mu because a client may attach elsewhere.
	session string
	socket  string

	// wakes counts scheduler firings and pushed lines. The idle guard reads it;
	// nothing else should.
	wakes atomic.Int64

	// eventDue debounces event-driven refreshes: a burst of daemon events
	// re-runs a component once, not once per event.
	eventDue map[string]time.Time
}

// dockEngineUpdateBuffer is how many updates may queue before an engine
// goroutine blocks. A component that pushes faster than the model drains is
// slowed to the model's pace rather than being allowed to grow a backlog.
const dockEngineUpdateBuffer = 64

// dockEventDebounce is how long a component waits after an event before it
// re-runs, so a burst of daemon events costs one execution.
const dockEventDebounce = 200 * time.Millisecond

// newDockEngine builds the engine for a set of components and starts the
// scheduler. The scheduler goroutine exists even with nothing to schedule: it
// parks on a receive, costing one goroutine and no timer, which is what keeps
// refresh-dock and event refreshes working on a dock that never polls.
func newDockEngine(comps []*dockComponent) *dockEngine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &dockEngine{
		comps:    make(map[string]*dockComponent, len(comps)),
		updates:  make(chan dockComponentUpdate, dockEngineUpdateBuffer),
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
func (e *dockEngine) Stop() {
	if e == nil {
		return
	}
	e.cancel()
}

// SetContext records the session name and socket path handed to every command.
func (e *dockEngine) SetContext(session, socket string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.session, e.socket = session, socket
	e.mu.Unlock()
}

// Updates is the channel the model listens on.
func (e *dockEngine) Updates() <-chan dockComponentUpdate {
	if e == nil {
		return nil
	}
	return e.updates
}

// Wakes is how many times the engine has had something to say. The idle guard
// asserts this stays at zero for a dock made only of once, push and event
// components.
func (e *dockEngine) Wakes() int64 {
	if e == nil {
		return 0
	}
	return e.wakes.Load()
}

// Text is a component's current cell text.
func (e *dockEngine) Text(name string) string {
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
func (e *dockEngine) Component(name string) (dockComponent, bool) {
	if e == nil {
		return dockComponent{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.comps[name]
	if !ok {
		return dockComponent{}, false
	}
	return *c, true
}

// Snapshot copies every component in definition order, for list-dock-components.
func (e *dockEngine) Snapshot() []dockComponent {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]dockComponent, 0, len(e.order))
	for _, name := range e.order {
		if c, ok := e.comps[name]; ok {
			out = append(out, *c)
		}
	}
	return out
}

// Names lists the components the engine holds, sorted.
func (e *dockEngine) Names() []string {
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
func (e *dockEngine) Start() {
	if e == nil {
		return
	}
	e.mu.Lock()
	var initial, pushes []*dockComponent
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
		// The first value is fetched off-goroutine like every later one, so a
		// slow component delays its own cell and nothing else. The bar draws
		// without it and gains it when it arrives.
		if !c.Builtin {
			go e.runOnce(c)
		}
	}
	e.replan()
}

// alignedDeadline is when a component next comes due. An interval that divides
// a second or a minute is aligned to it, so a clock showing 15:04 changes at the
// top of the minute rather than at some offset from when the session started.
func (e *dockEngine) alignedDeadline(c *dockComponent, now time.Time) time.Time {
	d := c.Refresh.Interval
	if d <= 0 {
		return now
	}
	if d == time.Second || d == time.Minute {
		return now.Truncate(d).Add(d)
	}
	return now.Add(d)
}

// replan nudges the scheduler to recompute the earliest deadline. Non-blocking:
// the channel holds one, and one pending nudge is as good as ten.
func (e *dockEngine) replan() {
	if e == nil {
		return
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// schedule is the single-timer loop. When no component has a deadline it parks
// on a receive and no timer exists at all, which is the invariant this whole
// design is built to hold.
func (e *dockEngine) schedule() {
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

		timer := time.NewTimer(time.Until(due))
		select {
		case <-timer.C:
			e.fire(next)
		case <-e.wake:
			timer.Stop()
		case <-e.ctx.Done():
			timer.Stop()
			return
		}
	}
}

// earliest is the component due soonest and when, or nil when nothing polls.
// Event components appear here only while their debounce is pending, which is
// what makes an event refresh cost one wake rather than a standing timer.
func (e *dockEngine) earliest() (*dockComponent, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var best *dockComponent
	var bestAt time.Time
	consider := func(c *dockComponent, at time.Time) {
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

// fire runs whatever came due. A built-in is reported to the model, which owns
// the state its refresh reads; a custom one is executed here, off the render
// goroutine, and skipped entirely if the previous run has not finished.
func (e *dockEngine) fire(c *dockComponent) {
	e.mu.Lock()
	delete(e.eventDue, c.Name)
	if c.Refresh.Kind == config.DockRefreshInterval {
		c.nextDue = e.alignedDeadline(c, time.Now())
	}
	builtin, running := c.Builtin, c.running
	e.mu.Unlock()

	if builtin {
		e.emit(dockComponentUpdate{Name: c.Name, Builtin: true})
		return
	}
	if running {
		// Drop-if-in-flight. A component slower than its own interval polls at
		// the rate it can manage instead of stacking processes.
		return
	}
	go e.runOnce(c)
}

// NotifyEvent marks every component watching this event type as due, debounced.
// Cost at idle is zero: events only arrive when something happened, and
// something happening is when the frame redraws anyway.
func (e *dockEngine) NotifyEvent(eventType string) {
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
		matched := false
		for _, want := range c.Refresh.Events {
			if want == eventType {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if _, pending := e.eventDue[name]; pending {
			continue // already coming; that is the debounce
		}
		e.eventDue[name] = at
		changed = true
	}
	e.mu.Unlock()
	if changed {
		e.replan()
	}
}

// Refresh re-runs one component now, whatever its refresh mode says, and clears
// a give-up so a fixed script recovers without a restart. This is what the
// refresh-dock verb calls, and it is what makes a component scriptable from a
// hook, from cron, and from an agent.
func (e *dockEngine) Refresh(name string) error {
	if e == nil {
		return fmt.Errorf("no dock components are loaded")
	}
	e.mu.Lock()
	c, ok := e.comps[name]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("no dock component named %q", name)
	}
	c.stopped, c.failures, c.reported = false, 0, false
	builtin, push := c.Builtin, c.Refresh.Kind == config.DockRefreshPush
	e.mu.Unlock()

	switch {
	case builtin:
		e.emit(dockComponentUpdate{Name: name, Builtin: true})
	case push:
		// A push component's value is whatever its process last wrote; there is
		// nothing to re-run without killing the process the user asked to keep
		// running. Waking its reader is what revives one that gave up.
		select {
		case c.revive <- struct{}{}:
		default:
		}
	default:
		go e.runOnce(c)
	}
	return nil
}

// RefreshAll re-runs every component. Used by the verb with no argument and by
// a config reload that kept the same component set.
func (e *dockEngine) RefreshAll() {
	for _, name := range e.Names() {
		_ = e.Refresh(name)
	}
}

// emit sends an update, counting the wake. It never blocks forever: a model
// that has stopped draining is a model that is shutting down.
func (e *dockEngine) emit(u dockComponentUpdate) {
	e.wakes.Add(1)
	select {
	case e.updates <- u:
	case <-e.ctx.Done():
	}
}

// commandEnv is the environment a component's command runs in: the session's
// own, plus the three variables that tell the command what it is and how to
// talk back. Same shape as a hook, deliberately, because a component is a hook
// that draws.
func (e *dockEngine) commandEnv(name string, extra ...string) []string {
	e.mu.Lock()
	session, socket := e.session, e.socket
	e.mu.Unlock()
	env := append(os.Environ(),
		"TUIOS_DOCK_COMPONENT="+strings.TrimPrefix(name, config.DockCustomPrefix),
		"TUIOS_SESSION="+session,
		"TUIOS_SOCKET="+socket,
	)
	return append(env, extra...)
}

// runOnce executes a component's command and reports its first line of stdout.
//
// The four ways a subprocess misbehaves are all handled here and all end the
// same way, with an empty cell and a recorded reason: it can be slow (the
// context timeout kills it), it can fail (a nonzero exit is not a value), it can
// never exit (the timeout again), and it can write without stopping (the read is
// bounded, and only the first line is used anyway).
func (e *dockEngine) runOnce(c *dockComponent) {
	e.mu.Lock()
	if c.running {
		e.mu.Unlock()
		return
	}
	c.running = true
	command := c.Command
	e.mu.Unlock()

	ctx, cancel := context.WithTimeout(e.ctx, config.DockCustomTimeout)
	defer cancel()

	// #nosec G204 - the command is the user's own config, run as the user, on
	// the same footing as [hooks]. There is no new trust boundary here.
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = e.commandEnv(c.Name)
	cmd.Stdin = nil
	cmd.Stderr = nil

	out, err := cmd.Output()
	if len(out) > config.DockCustomMaxOutput {
		out = out[:config.DockCustomMaxOutput]
	}

	update := dockComponentUpdate{Name: c.Name}
	switch {
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
		update.Text = dockFirstLine(out)
	}

	e.mu.Lock()
	c.running = false
	e.mu.Unlock()

	if e.ctx.Err() != nil {
		return
	}
	e.emit(update)
}

// readPushed keeps a persistent command running and turns each line it writes
// into an update. This is the native answer to "wake me when X changes": the
// component author brings their own inotifywait or upower --monitor, and the
// dock just reads a pipe. Wakes are driven by the fd, never by a clock.
//
// A command that exits is restarted with exponential backoff, and after enough
// consecutive immediate failures it is left alone: a script that cannot start is
// not made more likely to start by being started faster.
func (e *dockEngine) readPushed(c *dockComponent) {
	backoff := 500 * time.Millisecond
	failures := 0

	for e.ctx.Err() == nil {
		e.mu.Lock()
		command, stopped := c.Command, c.stopped
		e.mu.Unlock()
		if stopped {
			// Park until somebody asks for it again. No timer: a reader that
			// gave up costs one blocked goroutine and nothing else.
			select {
			case <-c.revive:
				continue
			case <-e.ctx.Done():
				return
			}
		}

		// #nosec G204 - the user's own config, as above.
		cmd := exec.CommandContext(e.ctx, "sh", "-c", command)
		cmd.Env = e.commandEnv(c.Name)
		cmd.Stderr = nil
		pipe, err := cmd.StdoutPipe()
		lines := 0
		if err == nil && cmd.Start() == nil {
			lines = e.drainPushed(c, pipe)
			_ = cmd.Wait()
		} else if err != nil {
			e.emit(dockComponentUpdate{Name: c.Name, Exit: -1, Err: err.Error()})
		}

		if lines > 0 {
			// It ran and said something, so the next restart starts patient
			// again. A component that emits once and exits is a "once" that
			// was written as a push; it should keep working.
			backoff, failures = 500*time.Millisecond, 0
		} else {
			failures++
			if failures >= config.DockCustomFailureLimit {
				e.mu.Lock()
				c.stopped = true
				c.lastErr = fmt.Sprintf("exited %d times without output; not restarting", failures)
				e.mu.Unlock()
				e.emit(dockComponentUpdate{Name: c.Name, Exit: -1, Err: "exited without output; not restarting"})
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

// drainPushed reads lines from a running component, one update each, and
// reports how many it saw. The reader is bounded per line: a process that writes
// forever without a newline fills a cell's worth and no more.
func (e *dockEngine) drainPushed(c *dockComponent, pipe io.Reader) int {
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
				e.emit(dockComponentUpdate{Name: c.Name, Text: dockSanitize(line)})
			}
			if len(acc) > config.DockCustomMaxOutput {
				// No newline in a very long run of bytes. Keep the tail so a
				// line arriving later is still parsed, and drop the rest.
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

// applyUpdate folds one update into the component's state and reports whether
// the cell text moved and whether this is a failure the user has not been told
// about yet.
//
// The render gate lives here: a poll whose value has not changed produces no
// frame. A one-second component watching a number that moves every five minutes
// costs sixty executions an hour and about zero frames.
func (e *dockEngine) applyUpdate(u dockComponentUpdate) (changed, newFailure bool) {
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
		// A component that failed shows nothing rather than showing stale
		// truth. A cell that keeps displaying a value its command can no longer
		// produce is worse than an absent one: it is confidently wrong.
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
	if trimmed := dockTruncateCell(text, c.MaxWidth); trimmed != c.text {
		c.text, changed = trimmed, true
	}
	return changed, newFailure
}

// SetBuiltinText records a built-in's rendered value, so the change gate and
// list-dock-components see built-ins exactly as they see custom cells.
func (e *dockEngine) SetBuiltinText(name, text string) bool {
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

// dockFirstLine is the first line of a command's stdout, laundered.
func dockFirstLine(out []byte) string {
	s := strings.TrimRight(string(out), "\r\n")
	line, _, _ := strings.Cut(s, "\n")
	return dockSanitize(strings.TrimRight(line, "\r"))
}

// dockSanitize keeps printable text and SGR colour, and drops every other
// control sequence.
//
// Colour survives because a meter without it is a worse meter, and because
// polybar and waybar components have always been written with it. Everything
// else goes: a cursor move, a scroll region or an erase written into a dock cell
// would be a component redrawing somebody else's screen. Cell text is drawn on
// the dock's own Panel ground afterwards, so a reset inside it cannot punch a
// transparent hole through the bar.
func dockSanitize(s string) string {
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
				// Some other CSI: skip it and its final byte.
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			// A string sequence: OSC, DCS, APC, PM, SOS. These carry a payload
			// that runs to a terminator, and dropping only the introducer would
			// leave the payload on the bar as text. A component setting the
			// window title is the case that found this.
			if i+1 < len(s) && strings.IndexByte("]P_^X", s[i+1]) >= 0 {
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 { // BEL
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' { // ST
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}
			// Any other two-byte escape (a charset selection, a save/restore).
			i += 2
			continue
		}
		if c < 0x20 || c == 0x7f {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}
