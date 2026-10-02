package session

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"regexp"
	"time"
)

// This file implements the subscribe and wait-for verbs on top of the event hub
// (daemon_events.go). subscribe turns a JSON connection into a long-lived event
// stream; wait-for is sugar over a short-lived internal subscription that blocks
// until a condition matches or a timeout elapses, replacing a caller's
// capture-pane polling loop.

// defaultWaitTimeout bounds a wait-for verb that omits an explicit timeout.
const defaultWaitTimeout = 30 * time.Second

// maxWaitTimeoutMS is the longest timeout a wait takes: 24 hours. A longer
// one is refused, which also keeps it clear of overflowing a time.Duration.
const maxWaitTimeoutMS = 24 * 60 * 60 * 1000

// waitClientPoll is how often a wait looks whether its caller has closed the
// connection.
const waitClientPoll = 500 * time.Millisecond

// waitDeadline is the channel a wait ends on: the timeout, or before it the
// caller going away. A caller on a connection has gone when the other end
// closed it (connPeerClosed). A call from a pane on another machine has gone
// when the report channel it came on ends, which is the only way its answer
// could go back. A wait whose caller has gone ends at once, so a client that
// closes does not leave its goroutine and event subscription behind until
// the timeout. stop ends the watch when the wait returns.
func (d *Daemon) waitDeadline(cs *connState, timeout time.Duration) (deadline <-chan time.Time, stop func()) {
	var conn net.Conn
	var ended <-chan struct{}
	if cs != nil {
		conn, ended = cs.conn, cs.hostedEnded
	}
	if conn == nil && ended == nil {
		timer := time.NewTimer(timeout)
		return timer.C, func() { timer.Stop() }
	}
	ends := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		var poll <-chan time.Time
		if conn != nil {
			ticker := time.NewTicker(waitClientPoll)
			defer ticker.Stop()
			poll = ticker.C
		}
		for {
			select {
			case now := <-timer.C:
				ends <- now
				return
			case <-ended:
				ends <- time.Now()
				return
			case <-poll:
				if connPeerClosed(conn) {
					ends <- time.Now()
					return
				}
			case <-done:
				return
			case <-d.ctx.Done():
				return
			}
		}
	}()
	return ends, func() { close(done) }
}

// defaultIdleWindow is the quiet period a window-idle wait uses when the request
// omits an idle duration.
const defaultIdleWindow = 500 * time.Millisecond

// waitOutputRecheck is a cheap in-process backstop interval for wait-for-output.
// The output events drive an immediate re-check; this ticker only guards the rare
// case where the final matching output event was dropped by the slow-subscriber
// policy, so the wait still resolves without a caller-side poll loop.
const waitOutputRecheck = 200 * time.Millisecond

// verbWaitFor blocks until a condition matches or a timeout elapses, returning a
// wait_result on match and a timeout error otherwise. It is sugar over a
// short-lived internal hub subscription, so a caller need not poll capture-pane.
func (d *Daemon) verbWaitFor(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Condition string `json:"condition"`
		Session   string `json:"session"`
		Window    string `json:"window"`
		Pattern   string `json:"pattern"`
		Source    string `json:"source"`
		Until     string `json:"until"`
		Idle      int    `json:"idle"`
		Thread    uint64 `json:"thread"`
		Timeout   int    `json:"timeout"`
		// AnySession widens agent-state to every session on the daemon.
		AnySession bool `json:"any_session"`
		// CommandSeq makes command-finished match once the window has
		// finished more commands than this.
		CommandSeq *uint64 `json:"command_seq"`
		// Select narrows agent-state to the panes a selector matches, in
		// every session. Every waits for all of them rather than any one.
		Select string `json:"select"`
		Every  bool   `json:"every"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Every && p.Select == "" {
		return nil, invalidParam("every", "every applies to a wait with select: it waits for every pane the selector matches")
	}
	if p.Select != "" {
		if verr := refuseSelectFromHostedPane(cs); verr != nil {
			return nil, verr
		}
		if p.Condition != "agent-state" {
			return nil, invalidParam("select", "select only applies to the agent-state condition")
		}
		if p.Session != "" || p.Window != "" || p.AnySession {
			return nil, invalidParam("select", "select watches the panes it matches in every session, so it takes no session, window or any_session. Put a session: term in the selector instead")
		}
	}

	if p.Timeout < 0 || p.Timeout > maxWaitTimeoutMS {
		return nil, invalidParam("timeout", fmt.Sprintf("timeout is milliseconds from 1 to %d (24 hours)", maxWaitTimeoutMS))
	}
	timeout := defaultWaitTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Millisecond
	}
	deadline, stop := d.waitDeadline(cs, timeout)
	defer stop()

	if p.Select != "" {
		sel, verr := d.parseVerbSelector(p.Select)
		if verr != nil {
			return nil, verr
		}
		return d.waitAgentStateSelect(sel, p.Until, p.Every, deadline)
	}

	if p.AnySession {
		if p.Condition != "agent-state" {
			return nil, invalidParam("any_session", "any_session only applies to the agent-state condition")
		}
		if p.Session != "" || p.Window != "" {
			return nil, invalidParam("any_session", "any_session watches every session, so it takes no session or window. Drop one or the other")
		}
		return d.waitAgentStateAnySession(p.Until, deadline)
	}

	switch p.Condition {
	case "session-exists":
		return d.waitSessionExists(p.Session, deadline)
	case "window-output":
		return d.waitWindowOutput(p.Session, p.Window, p.Pattern, p.Source, deadline)
	case "window-exit":
		return d.waitWindowExit(p.Session, p.Window, deadline)
	case "window-idle":
		return d.waitWindowIdle(p.Session, p.Window, p.Idle, deadline)
	case "agent-state":
		return d.waitAgentState(p.Session, p.Window, p.Until, deadline)
	case "agent-message":
		return d.waitAgentMessage(p.Session, p.Window, p.Thread, deadline)
	case waitCommandFinished:
		return d.waitCommandFinishedFor(p.Session, p.Window, p.CommandSeq, deadline)
	default:
		message := "unknown condition " + p.Condition
		if p.Condition == "" {
			message = "condition is required"
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, message, &VerbHint{
			Param:      "condition",
			Accepted:   waitConditions,
			DidYouMean: closestMatch(p.Condition, waitConditions),
		})
	}
}

// waitMatched builds a successful wait_result for the given condition.
func waitMatched(condition string, extra map[string]any) map[string]any {
	res := map[string]any{"type": "wait_result", "condition": condition, "matched": true}
	maps.Copy(res, extra)
	return res
}

// waitSessionExists resolves when a session named name exists. It subscribes
// before the initial check so a session created in the race window is not missed.
func (d *Daemon) waitSessionExists(name string, deadline <-chan time.Time) (any, *verbError) {
	if name == "" {
		return nil, invalidParam("session", "session is required for the session-exists condition")
	}
	sub := d.events.subscribe(eventFilter{
		session: name,
		types:   map[string]bool{EventSessionCreated: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if d.manager.GetSession(name) != nil {
		return waitMatched("session-exists", map[string]any{"session": name}), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for session "+name, &VerbHint{
				Param:  "timeout",
				Verb:   "list-sessions",
				Detail: "The session was never created within the timeout. Check the name, or raise timeout (milliseconds).",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if d.manager.GetSession(name) != nil {
				return waitMatched("session-exists", map[string]any{"session": name}), nil
			}
		}
	}
}

// waitWindowExit resolves when the target window's shell process exits.
func (d *Daemon) waitWindowExit(sessionName, window string, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types:   map[string]bool{EventWindowExit: true, EventWindowClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if pty.IsExited() {
		return waitMatched("window-exit", map[string]any{"window": window}), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for window "+window+" to exit", &VerbHint{
				Param:  "timeout",
				Detail: "The window's shell was still running when the timeout elapsed. Raise timeout (milliseconds), or send the command that ends it.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			return waitMatched("window-exit", map[string]any{"window": window}), nil
		}
	}
}

// waitWindowIdle resolves when the target window produces no output for idleMs
// milliseconds. Each output event resets the idle timer.
func (d *Daemon) waitWindowIdle(sessionName, window string, idleMs int, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	idle := defaultIdleWindow
	if idleMs > 0 {
		idle = time.Duration(idleMs) * time.Millisecond
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for window "+window+" to go idle", &VerbHint{
				Param:  "idle",
				Detail: "The window never stayed quiet for the idle window. Raise timeout, or raise idle if the process outputs in bursts.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			// Output arrived; restart the idle window.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		case <-timer.C:
			return waitMatched("window-idle", map[string]any{"window": window, "idle_ms": int(idle / time.Millisecond)}), nil
		}
	}
}

// waitWindowOutput resolves when the target window's captured content matches
// pattern. It subscribes and checks once before waiting (so already-present
// output matches immediately), then re-checks on each output event; a gap marker
// or dropped event cannot hang the wait because a low-rate backstop ticker also
// re-checks.
func (d *Daemon) waitWindowOutput(sessionName, window, pattern, source string, deadline <-chan time.Time) (any, *verbError) {
	if pattern == "" {
		return nil, invalidParam("pattern", "pattern is required for the window-output condition")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "invalid pattern: "+err.Error(), &VerbHint{
			Param:  "pattern",
			Detail: "pattern is a Go regular expression (RE2 syntax).",
		})
	}
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	// Default to matching recent (scrollback-inclusive) content so output that has
	// already scrolled off the visible screen still matches; source "visible"
	// restricts to the current screen.
	scrollback := source != "visible"
	// checked is the captureState of the last capture matched against. The
	// backstop re-checks only when the pane has moved past it: with no new
	// output and no resize the capture would read the same, and on a pane with
	// a full scrollback one capture costs milliseconds.
	var checked captureState
	matches := func() bool {
		var content string
		content, checked = pty.capturePlainAt(scrollback)
		return re.MatchString(content)
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if matches() {
		return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
	}

	backstop := time.NewTicker(waitOutputRecheck)
	defer backstop.Stop()
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for output matching "+pattern, &VerbHint{
				Param:  "pattern",
				Verb:   "capture-pane",
				Detail: "No output matched before the timeout. Capture the pane to see what it actually printed, then adjust the pattern or raise timeout.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if matches() {
				return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
			}
		case <-backstop.C:
			if pty.currentCaptureState() == checked {
				continue
			}
			if matches() {
				return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
			}
		}
	}
}
