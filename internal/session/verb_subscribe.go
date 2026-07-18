package session

import (
	"encoding/json"
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

// defaultIdleWindow is the quiet period a window-idle wait uses when the request
// omits an idle duration.
const defaultIdleWindow = 500 * time.Millisecond

// waitOutputRecheck is a cheap in-process backstop interval for wait-for-output.
// The output events drive an immediate re-check; this ticker only guards the rare
// case where the final matching output event was dropped by the slow-subscriber
// policy, so the wait still resolves without a caller-side poll loop.
const waitOutputRecheck = 200 * time.Millisecond

// verbSubscribe opens a long-lived event stream on this connection. It registers
// a hub subscription with the requested filter, returns a subscribed ack (with
// the current sequence baseline), and hands the subscription to the dispatch loop
// which starts the streamer after the ack is written. Only a connection that
// issued this verb ever receives events.
func (d *Daemon) verbSubscribe(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Window  string   `json:"window"`
		Types   []string `json:"types"`
		Queue   int      `json:"queue"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	cs.mu.Lock()
	if cs.streaming {
		cs.mu.Unlock()
		return nil, newVerbError(ErrVerbInvalidRequest, "connection is already subscribed")
	}
	cs.mu.Unlock()

	filter := eventFilter{session: p.Session, window: p.Window}
	if len(p.Types) > 0 {
		filter.types = make(map[string]bool, len(p.Types))
		for _, t := range p.Types {
			filter.types[t] = true
		}
	}

	sub := d.events.subscribe(filter, p.Queue)

	cs.mu.Lock()
	cs.eventSub = sub
	cs.pendingStream = sub
	cs.streaming = true
	cs.mu.Unlock()

	return map[string]any{"type": EventSubscribed, "seq": d.events.currentSeq()}, nil
}

// verbUnsubscribe closes this connection's event stream. The streamer observes
// the stop signal, clears the connection's stream state, and unsubscribes from
// the hub.
func (d *Daemon) verbUnsubscribe(cs *connState, _ json.RawMessage) (any, *verbError) {
	cs.mu.Lock()
	sub := cs.eventSub
	cs.mu.Unlock()
	if sub == nil {
		return nil, newVerbError(ErrVerbInvalidRequest, "connection is not subscribed")
	}
	// Signal the streamer to exit; it clears cs.eventSub/streaming and removes the
	// hub subscription on its way out.
	sub.close()
	return map[string]any{"type": "unsubscribed"}, nil
}

// startPendingStream launches the event streamer for a subscription handed over
// by the subscribe handler, once its ack has been written. It is a no-op for
// every other verb.
func (d *Daemon) startPendingStream(cs *connState) {
	cs.mu.Lock()
	sub := cs.pendingStream
	cs.pendingStream = nil
	cs.mu.Unlock()
	if sub == nil {
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.streamEvents(cs, sub)
	}()
}

// streamEvents pushes events from a subscription to the connection until the
// connection closes, the daemon shuts down, or the subscription is stopped. A
// full subscriber queue drops events at publish time; the streamer surfaces those
// drops as a gap marker written just before the next surviving event, so a slow
// subscriber never blocks the daemon (the daemon_stream.go discipline).
func (d *Daemon) streamEvents(cs *connState, sub *eventSub) {
	defer d.events.unsubscribe(sub)
	defer func() {
		cs.mu.Lock()
		if cs.eventSub == sub {
			cs.eventSub = nil
			cs.streaming = false
		}
		cs.mu.Unlock()
	}()

	for {
		select {
		case <-cs.done:
			return
		case <-d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			if dropped := sub.dropped.Swap(0); dropped > 0 {
				if err := d.writeEventLine(cs, streamEvent{Type: EventGap, Dropped: dropped}); err != nil {
					cs.drop()
					return
				}
			}
			if err := d.writeEventLine(cs, ev); err != nil {
				cs.drop()
				return
			}
		}
	}
}

// writeEventLine serializes ev as one newline-terminated JSON line and writes it
// under the connection's send mutex with a write deadline.
func (d *Daemon) writeEventLine(cs *connState, ev streamEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, werr := cs.conn.Write(data)
	return werr
}

// verbWaitFor blocks until a condition matches or a timeout elapses, returning a
// wait_result on match and a timeout error otherwise. It is sugar over a
// short-lived internal hub subscription, so a caller need not poll capture-pane.
func (d *Daemon) verbWaitFor(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Condition string `json:"condition"`
		Session   string `json:"session"`
		Window    string `json:"window"`
		Pattern   string `json:"pattern"`
		Source    string `json:"source"`
		Idle      int    `json:"idle"`
		Timeout   int    `json:"timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	timeout := defaultWaitTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Millisecond
	}
	deadline := time.After(timeout)

	switch p.Condition {
	case "session-exists":
		return d.waitSessionExists(p.Session, deadline)
	case "window-output":
		return d.waitWindowOutput(p.Session, p.Window, p.Pattern, p.Source, deadline)
	case "window-exit":
		return d.waitWindowExit(p.Session, p.Window, deadline)
	case "window-idle":
		return d.waitWindowIdle(p.Session, p.Window, p.Idle, deadline)
	default:
		return nil, newVerbError(ErrVerbInvalidParams,
			"unknown condition "+p.Condition+" (want session-exists, window-output, window-exit, or window-idle)")
	}
}

// waitMatched builds a successful wait_result for the given condition.
func waitMatched(condition string, extra map[string]any) map[string]any {
	res := map[string]any{"type": "wait_result", "condition": condition, "matched": true}
	for k, v := range extra {
		res[k] = v
	}
	return res
}

// waitSessionExists resolves when a session named name exists. It subscribes
// before the initial check so a session created in the race window is not missed.
func (d *Daemon) waitSessionExists(name string, deadline <-chan time.Time) (any, *verbError) {
	if name == "" {
		return nil, newVerbError(ErrVerbInvalidParams, "session is required for session-exists")
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
			return nil, newVerbError(ErrVerbTimeout, "timed out waiting for session "+name)
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
		return nil, mapResolveErr(err)
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
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
			return nil, newVerbError(ErrVerbTimeout, "timed out waiting for window "+window+" to exit")
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
		return nil, mapResolveErr(err)
	}

	idle := defaultIdleWindow
	if idleMs > 0 {
		idle = time.Duration(idleMs) * time.Millisecond
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-deadline:
			return nil, newVerbError(ErrVerbTimeout, "timed out waiting for window "+window+" to go idle")
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
		return nil, newVerbError(ErrVerbInvalidParams, "pattern is required for window-output")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, newVerbError(ErrVerbInvalidParams, "invalid pattern: "+err.Error())
	}
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err)
	}

	// Default to matching recent (scrollback-inclusive) content so output that has
	// already scrolled off the visible screen still matches; source "visible"
	// restricts to the current screen.
	scrollback := source != "visible"
	matches := func() bool { return re.MatchString(pty.CaptureContent(scrollback, false)) }

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
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
			return nil, newVerbError(ErrVerbTimeout, "timed out waiting for output matching "+pattern)
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if matches() {
				return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
			}
		case <-backstop.C:
			if matches() {
				return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
			}
		}
	}
}
