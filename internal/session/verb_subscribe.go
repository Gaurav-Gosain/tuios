package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/charmbracelet/x/ansi"
)

const defaultWaitTimeout = 30 * time.Second

const maxWaitTimeoutMS = 24 * 60 * 60 * 1000

const waitClientPoll = 500 * time.Millisecond

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

const defaultIdleWindow = 500 * time.Millisecond

const waitOutputMinGap = 50 * time.Millisecond

var waitOutputCaptured atomic.Pointer[func(time.Duration)]

const waitOutputRecheck = 200 * time.Millisecond

const newOutputCarry = 64 << 10

func (d *Daemon) verbSubscribe(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session  string   `json:"session"`
		Window   string   `json:"window"`
		Types    []string `json:"types"`
		Queue    int      `json:"queue"`
		AfterSeq *uint64  `json:"after_seq"`
		BootID   string   `json:"boot_id"`
		Hosts    bool     `json:"hosts"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.BootID != "" && p.AfterSeq == nil {
		return nil, invalidParam("boot_id", "boot_id only means something with after_seq: pass the seq the stream last delivered under that boot id")
	}

	cs.mu.Lock()
	if cs.streaming {
		cs.mu.Unlock()
		return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is already subscribed", &VerbHint{
			Verb:   "unsubscribe",
			Detail: "One event stream per connection. Call unsubscribe first, or open a second connection for the second filter.",
		})
	}
	cs.mu.Unlock()

	filter := eventFilter{session: p.Session, window: p.Window, hosts: p.Hosts}
	if p.Session != "" {
		if live := d.manager.GetSession(p.Session); live != nil {
			filter.sess = live
		}
	}
	if !cs.viaLink {
		if verr := checkEventTypes(p.Types); verr != nil {
			return nil, verr
		}
	}
	if len(p.Types) > 0 {
		filter.types = make(map[string]bool, len(p.Types))
		for _, t := range p.Types {
			filter.types[t] = true
		}
	}

	var from *resumePoint
	if p.AfterSeq != nil {
		from = &resumePoint{afterSeq: *p.AfterSeq, bootID: p.BootID}
	}
	sub, baseline, err := d.events.subscribeFrom(filter, p.Queue, from)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{
			Param:  "after_seq",
			Detail: "Pass the seq of the last event the stream delivered. A seq this daemon has not reached yet cannot have been delivered under this boot id.",
		})
	}

	cs.mu.Lock()
	cs.eventSub = sub
	cs.pendingStream = sub
	cs.streaming = true
	cs.mu.Unlock()

	ack := map[string]any{"type": EventSubscribed, "seq": baseline, "boot_id": d.events.bootIdentity()}
	if from != nil {
		replayed := 0
		for _, ev := range sub.preface {
			if ev.Type != EventGap && d.eventInScope(cs, ev) {
				replayed++
			}
		}
		ack["replayed"] = replayed
	}
	return ack, nil
}

func (d *Daemon) verbUnsubscribe(cs *connState, _ json.RawMessage) (any, *verbError) {
	cs.mu.Lock()
	sub := cs.eventSub
	cs.mu.Unlock()
	if sub == nil {
		return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is not subscribed", &VerbHint{
			Verb:   "subscribe",
			Detail: "There is no event stream on this connection to close.",
		})
	}
	sub.close()
	return map[string]any{"type": "unsubscribed"}, nil
}

func (d *Daemon) startPendingStream(cs *connState) {
	cs.mu.Lock()
	sub := cs.pendingStream
	cs.pendingStream = nil
	cs.mu.Unlock()
	if sub == nil {
		return
	}
	if !d.goTracked(func() { d.streamEvents(cs, sub) }) {
		d.events.unsubscribe(sub)
	}
}

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

	for _, ev := range sub.preface {
		if !d.eventInScope(cs, ev) {
			continue
		}
		if err := d.writeEventLine(cs, ev); err != nil {
			cs.drop()
			return
		}
	}
	sub.preface = nil

	for {
		select {
		case <-cs.done:
			return
		case <-d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			if !d.eventInScope(cs, ev) {
				continue
			}
			if dropped := sub.dropped.Swap(0); dropped > 0 {
				if err := d.writeEventLine(cs, streamEvent{Type: EventGap, Dropped: dropped, Reason: GapOverflow, BootID: d.events.bootIdentity()}); err != nil {
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

func (d *Daemon) writeEventLine(cs *connState, ev streamEvent) error {
	data, err := jsonx.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(escapeC1(data), '\n')

	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, werr := cs.conn.Write(data)
	return werr
}

func (d *Daemon) verbWaitFor(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Condition  string  `json:"condition"`
		Session    string  `json:"session"`
		Window     string  `json:"window"`
		Pattern    string  `json:"pattern"`
		Source     string  `json:"source"`
		New        bool    `json:"new"`
		Until      string  `json:"until"`
		Idle       int     `json:"idle"`
		Thread     uint64  `json:"thread"`
		Timeout    int     `json:"timeout"`
		AnySession bool    `json:"any_session"`
		CommandSeq *uint64 `json:"command_seq"`
		Select     string  `json:"select"`
		Every      bool    `json:"every"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.New && p.Condition != "window-output" {
		return nil, invalidParam("new", "new only applies to the window-output condition")
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

	if p.Window != "" && p.Condition != "window-exit" && p.Condition != "session-exists" {
		if verr := d.endedPaneRefusal(cs, p.Session, p.Window); verr != nil {
			return nil, verr
		}
	}

	switch p.Condition {
	case "session-exists":
		return d.waitSessionExists(p.Session, deadline)
	case "window-output":
		return d.waitWindowOutput(cs, p.Session, p.Window, p.Pattern, p.Source, p.New, deadline)
	case "window-exit":
		return d.waitWindowExit(cs, p.Session, p.Window, deadline)
	case "window-idle":
		return d.waitWindowIdle(cs, p.Session, p.Window, p.Idle, deadline)
	case "agent-state":
		return d.waitAgentState(cs, p.Session, p.Window, p.Until, deadline)
	case "agent-message":
		return d.waitAgentMessage(cs, p.Session, p.Window, p.Thread, deadline)
	case waitCommandFinished:
		return d.waitCommandFinishedFor(cs, p.Session, p.Window, p.CommandSeq, deadline)
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

func waitMatched(condition string, extra map[string]any) map[string]any {
	res := map[string]any{"type": "wait_result", "condition": condition, "matched": true}
	maps.Copy(res, extra)
	return res
}

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

func (d *Daemon) waitWindowExit(cs *connState, sessionName, window string, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(cs, sessionName)
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

	matched := func() map[string]any {
		pty.awaitExitNote()
		res := waitMatched("window-exit", map[string]any{"window": window})
		exitResult(pty, res)
		return res
	}
	if pty.IsExited() {
		return matched(), nil
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
			return matched(), nil
		}
	}
}

func (d *Daemon) waitWindowIdle(cs *connState, sessionName, window string, idleMs int, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(cs, sessionName)
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

func (d *Daemon) waitAgentState(cs *connState, sessionName, window, until string, deadline <-chan time.Time) (any, *verbError) {
	states, verr := parseUntilStates(until)
	if verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, sessionName)
	if verr != nil {
		return nil, verr
	}
	targetID := ""
	if window != "" {
		state := sess.GetState()
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		targetID = state.Windows[idx].ID
	}

	types := map[string]bool{EventAgentState: true}
	if targetID != "" {
		types[EventWindowClosed] = true
	}
	sub := d.events.subscribe(eventFilter{session: sess.Name(), sess: sess, types: types}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	closed := func() *verbError {
		return newVerbError(ErrVerbWindowNotFound, "the watched window closed before reaching "+until)
	}
	check := func() (id, name string, ok, gone bool) {
		state := sess.GetState()
		for i := range state.Windows {
			w := &state.Windows[i]
			if targetID != "" && w.ID != targetID {
				continue
			}
			if states[w.AgentState.Name()] {
				return w.ID, w.AgentState.Name(), true, false
			}
			if targetID != "" {
				return "", "", false, false
			}
		}
		return "", "", false, targetID != ""
	}

	matched := func(id, name string) map[string]any {
		return waitMatched("agent-state", map[string]any{"session": sess.Name(), "window": id, "state": name})
	}
	if id, name, ok, gone := check(); ok {
		return matched(id, name), nil
	} else if gone {
		return nil, closed()
	}
	for {
		select {
		case <-deadline:
			return nil, agentStateTimeout(until)
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed && targetID != "" && ev.Window == targetID {
				return nil, closed()
			}
			if id, name, ok, gone := check(); ok {
				return matched(id, name), nil
			} else if gone {
				return nil, closed()
			}
		}
	}
}

func agentStateTimeout(until string) *verbError {
	return hintedVerbError(ErrVerbTimeout, "timed out waiting for agent state "+until, &VerbHint{
		Param:  "until",
		Verb:   "get-agent-state",
		Detail: "No agent reached the named state before the timeout. Read the current state, or raise timeout (milliseconds).",
	})
}

func (d *Daemon) waitAgentStateAnySession(until string, deadline <-chan time.Time) (any, *verbError) {
	states, verr := parseUntilStates(until)
	if verr != nil {
		return nil, verr
	}
	sub := d.events.subscribe(eventFilter{types: map[string]bool{EventAgentState: true}}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (map[string]any, bool) {
		sessions := d.manager.AllSessions()
		slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name(), b.Name()) })
		for _, sess := range sessions {
			state := sess.GetState()
			for i := range state.Windows {
				w := &state.Windows[i]
				if states[w.AgentState.Name()] {
					return waitMatched("agent-state", map[string]any{
						"session": sess.Name(), "window": w.ID, "state": w.AgentState.Name(),
					}), true
				}
			}
		}
		return nil, false
	}

	if res, ok := check(); ok {
		return res, nil
	}
	for {
		select {
		case <-deadline:
			return nil, agentStateTimeout(until)
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if res, ok := check(); ok {
				return res, nil
			}
		}
	}
}

func (d *Daemon) waitAgentStateSelect(sel *Selector, until string, every bool, deadline <-chan time.Time) (any, *verbError) {
	states, verr := parseUntilStates(until)
	if verr != nil {
		return nil, verr
	}
	sub := d.events.subscribe(eventFilter{types: map[string]bool{
		EventAgentState: true, EventWindowCreated: true, EventWindowClosed: true,
		EventSessionCreated: true, EventSessionClosed: true,
	}}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (map[string]any, bool) {
		panes := d.selectPanes(sel, false)
		if every {
			if len(panes) == 0 {
				return nil, false
			}
			matched := make([]map[string]any, 0, len(panes))
			for _, p := range panes {
				name := p.window.AgentState.Name()
				if !states[name] {
					return nil, false
				}
				matched = append(matched, map[string]any{"session": p.sess.Name(), "window": p.window.ID, "state": name})
			}
			return waitMatched("agent-state", map[string]any{"select": sel.String(), "every": true, "panes": matched, "total": len(matched)}), true
		}
		for _, p := range panes {
			if name := p.window.AgentState.Name(); states[name] {
				return waitMatched("agent-state", map[string]any{
					"select": sel.String(), "session": p.sess.Name(), "window": p.window.ID, "state": name,
				}), true
			}
		}
		return nil, false
	}

	if res, ok := check(); ok {
		return res, nil
	}
	for {
		select {
		case <-deadline:
			verr := agentStateTimeout(until)
			verr.Hint.Command = "tuios list-agents --select '" + sel.String() + "'"
			verr.Hint.Detail = "No pane the selector matches reached the named state before the timeout. list-agents with the same selector shows what it matches and where each pane is now."
			if every {
				verr.Hint.Detail = "Not every pane the selector matches reached the named state before the timeout. list-agents with the same selector shows which have not."
			}
			return nil, verr
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if res, ok := check(); ok {
				return res, nil
			}
		}
	}
}

func parseUntilStates(until string) (map[string]bool, *verbError) {
	if strings.TrimSpace(until) == "" {
		return nil, invalidParam("until", "until is required for the agent-state condition")
	}
	states := map[string]bool{}
	for part := range strings.SplitSeq(until, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		st, ok := ParseAgentState(part)
		if !ok {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state "+echoName(part), &VerbHint{
				Param:      "until",
				Accepted:   AgentStateNames,
				DidYouMean: closestMatch(part, AgentStateNames),
			})
		}
		states[st.Name()] = true
	}
	return states, nil
}

func (d *Daemon) waitWindowOutput(cs *connState, sessionName, window, pattern, source string, fresh bool, deadline <-chan time.Time) (any, *verbError) {
	if pattern == "" {
		return nil, invalidParam("pattern", "pattern is required for the window-output condition")
	}
	if fresh && source != "" {
		return nil, invalidParam("new", "new matches the output written after the wait starts, so it takes no source. Drop source")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "invalid pattern: "+err.Error(), &VerbHint{
			Param:  "pattern",
			Detail: "pattern is a Go regular expression (RE2 syntax).",
		})
	}
	sess, verr := d.resolveVerbSession(cs, sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	scrollback := source != "visible"
	var checked captureState
	var lastCheck time.Time
	matches := func() bool {
		var content string
		start := time.Now()
		content, checked = pty.capturePlainAt(scrollback)
		lastCheck = time.Now()
		if hook := waitOutputCaptured.Load(); hook != nil {
			(*hook)(lastCheck.Sub(start))
		}
		return re.MatchString(content)
	}
	moved := func() bool { return pty.currentCaptureState() != checked }
	matched := map[string]any{"window": window, "pattern": pattern}

	if fresh {
		tap := pty.addOutputTap()
		defer pty.removeOutputTap(tap)
		var carry []byte
		matches = func() bool {
			carry = append(carry, tap.take()...)
			lastCheck = time.Now()
			text := strings.ReplaceAll(ansi.Strip(string(carry)), "\r\n", "\n")
			if re.MatchString(text) {
				return true
			}
			if cut := len(carry) - newOutputCarry; cut > 0 {
				if i := bytes.IndexByte(carry[cut:], '\n'); i >= 0 {
					cut += i + 1
				}
				carry = append([]byte(nil), carry[cut:]...)
			}
			return false
		}
		moved = tap.pending
		matched["new"] = true
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if matches() {
		return waitMatched("window-output", matched), nil
	}

	backstop := time.NewTicker(waitOutputRecheck)
	defer backstop.Stop()
	var deferred <-chan time.Time
	gapTimer := time.NewTimer(time.Hour)
	gapTimer.Stop()
	defer gapTimer.Stop()
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
			if deferred != nil {
				continue
			}
			if wait := waitOutputMinGap - time.Since(lastCheck); wait > 0 {
				gapTimer.Reset(wait)
				deferred = gapTimer.C
				continue
			}
			if matches() {
				return waitMatched("window-output", matched), nil
			}
		case <-deferred:
			deferred = nil
			if !moved() {
				continue
			}
			if matches() {
				return waitMatched("window-output", matched), nil
			}
		case <-backstop.C:
			if !moved() {
				continue
			}
			if matches() {
				return waitMatched("window-output", matched), nil
			}
		}
	}
}

func (d *Daemon) waitAgentMessage(cs *connState, sessionName, window string, thread uint64, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(cs, sessionName)
	if verr != nil {
		return nil, verr
	}

	inbox := ""
	if window != "" {
		id, _, err := resolveMailParty(sess.GetState(), window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		inbox = id
	}

	baseline := d.agents.highestID()

	thread = d.agents.resolveThread(sess.Name(), thread)

	types := map[string]bool{EventAgentMessage: true}
	if inbox != "" {
		types[EventWindowClosed] = true
	}
	sub := d.events.subscribe(eventFilter{session: sess.Name(), sess: sess, types: types}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (AgentMessage, bool) {
		if inbox != "" {
			return d.agents.firstUnread(sess.Name(), inbox, thread)
		}
		return d.agents.newerThan(sess.Name(), baseline, thread)
	}

	if m, ok := check(); ok {
		return agentMessageMatch(inbox, m), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for an agent message", &VerbHint{
				Param:   "timeout",
				Command: "tuios read-agent-messages",
				Detail:  "Nothing was sent before the timeout. Read the ring to see what is already there, or raise timeout (milliseconds).",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed && inbox != "" && ev.Window == inbox {
				return nil, newVerbError(ErrVerbWindowNotFound, "the watched inbox closed before a message arrived")
			}
			if m, ok := check(); ok {
				return agentMessageMatch(inbox, m), nil
			}
		}
	}
}

func agentMessageMatch(inbox string, m AgentMessage) map[string]any {
	return waitMatched("agent-message", map[string]any{
		"window":     inbox,
		"message_id": m.ID,
		"kind":       m.Kind,
		"from":       m.From,
		"from_name":  m.FromLabel,
		"subject":    m.Subject,
		"reply_to":   m.ReplyTo,
		"thread_id":  m.ThreadID,
	})
}

func checkEventTypes(types []string) *verbError {
	for _, t := range types {
		if slices.Contains(knownEventTypes, t) {
			continue
		}
		for ev, hook := range sessionHookEvents {
			if string(hook) == t {
				return invalidParam("types", fmt.Sprintf("%s is a hook name. The event it fires on is %s", echoName(t), ev), knownEventTypes...)
			}
		}
		return invalidParam("types", "unknown event type "+echoName(t), knownEventTypes...)
	}
	return nil
}

func (d *Daemon) endedPaneRefusal(cs *connState, sessionName, window string) *verbError {
	sess, verr := d.resolveVerbSession(cs, sessionName)
	if verr != nil {
		return nil
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil
	}
	rp, ok := pty.pty.(*remotePane)
	if !ok {
		return nil
	}
	if why := rp.endedWhy(); why != "" {
		return paneEndedError(&remotePaneEndedError{host: rp.host, why: why})
	}
	return nil
}
