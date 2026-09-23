package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Reports from a pane on another machine.
//
// A hosted pane's process runs on one machine and its window is in a session
// on another (hosted_pane.go). An agent in it wants to do what an agent in any
// pane does: report its state from a hook, set its meta, read its mail and
// send mail. All of that is about the window, which lives on the owning
// machine, and the link is dialled one way, from the owner, so the process has
// no socket that reaches the owner.
//
// So the calls travel the way everything else on the link does, from a
// connection the owner opened:
//
//  1. The owner sends its window id with open-pane. This machine exports it to
//     the process as TUIOS_PANE_ID, and the reply carries a calls token that
//     only the owner sees.
//  2. The owner opens one more connection, pane-calls, with the pane id and
//     the token. From then on it is the pane's report channel: this machine
//     writes a request line on it, the owner runs the call and writes the
//     answer back, matched by id.
//  3. The process calls this machine's daemon as usual, with the ordinary
//     commands. A call of one of hostedCallVerbs that names the pane (window,
//     to or from set to TUIOS_PANE_ID) is not run here. It is sent down the
//     channel and its answer is the owner's.
//
// What this grants, and how each limit is enforced:
//
//   - Only the process in the pane is forwarded for. This machine checks the
//     caller's pid, as the kernel gave it for the connection: it has to be the
//     pane's process or a descendant of it, or run on the pane's terminal. A
//     call from anywhere else that names the pane is refused, so a process
//     here that learned the id cannot report as the pane.
//   - Only the verbs in hostedCallVerbs cross, and wait-for only for
//     agent-message. Everything else a process asks this daemon is answered
//     here, as it always was.
//   - The owner does not trust the request. It runs it as the window the pane
//     is drawn in, whatever the request said: session, window, to and from are
//     overwritten with that window's own, paths and pids that name things on
//     the far machine are dropped, and the call runs as a caller inside a pane,
//     so it can never speak as the person, verified or claimed. A far machine
//     that writes its own requests on the channel gets exactly what the pane's
//     process gets, which is what an agent in a pane of the owner's gets for its
//     own window. One thing it gets less of: a message it sends may attach only
//     a file in the session's stash, the rule a sender on the link is held to,
//     because a path it names is a file on the owner's machine that its process
//     cannot see. The path is refused before it is looked at, so the answer
//     does not say whether a file exists on the owner.
//   - A forwarded wait-for runs on the owner for at most hostedWaitMax,
//     whatever the far side asked, and ends when the channel it came on ends.
//     The owner does not wait for calls in flight before it opens the next
//     channel, so a long wait never holds off the pane's reports.
//   - The channel is opened with the token, which the process never sees, so
//     the process cannot open one of its own. Lines are bounded both ways and
//     the calls in flight per pane are capped.
//
// Version skew: an owner from before this sends no window id and opens no
// channel. The process then has no TUIOS_PANE_ID, as before, and a call that
// names the pane id is answered with protocol_mismatch, which says to update
// tuios on the owning machine. A far machine from before this checks open-pane
// against a schema with no window param and refuses the request with
// invalid_params before it spawns anything. The owner then sends open-pane
// again on the same stream without the window (openPaneReply), gets no token,
// and opens no channel.

// hostedCallVerbs are the verbs a hosted pane's process may send to its owner,
// with the parameter that names the pane.
var hostedCallVerbs = map[string]string{
	"set-agent-state":     "window",
	"set-agent-meta":      "window",
	"set-agent-session":   "window",
	"read-agent-messages": "to",
	"send-agent-message":  "from",
	"wait-for":            "window",
}

// hostedCallDropped are parameters removed from a forwarded call before the
// owner runs it. Each names something on the far machine, a file or a process,
// which on the owner's machine is a different thing or nothing, or claims to be
// the person.
var hostedCallDropped = []string{"transcript_path", "harness_pid", "human_nonce", "from_host", "any_session", "host"}

const (
	// hostedCallsMaxLine bounds one line on a report channel, either way.
	hostedCallsMaxLine = 1 << 20
	// hostedCallsMaxInFlight caps the calls one pane has outstanding.
	hostedCallsMaxInFlight = 16
	// hostedCallBudget bounds a forwarded call that is not a wait.
	hostedCallBudget = 15 * time.Second
	// hostedCallsAttachWait is how long a call waits for the owner's channel
	// when the pane has just opened. A hook fires the moment an agent starts,
	// which can be before the owner's second connection lands.
	hostedCallsAttachWait = 5 * time.Second
	// hostedCallsRetry is how long the owner waits before opening a report
	// channel again after one dropped with the pane still open.
	hostedCallsRetry = 2 * time.Second
	// hostedWaitMax caps a forwarded wait-for.
	hostedWaitMax = time.Hour
)

// hostedWindowIDPattern is what an owner's window id may be. It becomes an
// environment variable here, so it is a token and nothing else.
var hostedWindowIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// newHostedCallsToken is a fresh secret for one pane's report channel.
func newHostedCallsToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// hostedCalls is the far half: the channel the owner opened for one pane, if
// any.
type hostedCalls struct {
	mu     sync.Mutex
	ch     *hostedCallChannel
	ready  chan struct{}
	closed bool
}

// readyLocked is closed when the first channel attaches. The caller holds mu.
func (h *hostedCalls) readyLocked() chan struct{} {
	if h.ready == nil {
		h.ready = make(chan struct{})
	}
	return h.ready
}

// attach makes ch the pane's channel. A channel already attached is closed:
// only the owner holds the token, so a second channel is the owner coming
// back, and the old one is dead or about to be.
func (h *hostedCalls) attach(ch *hostedCallChannel) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	if h.ch != nil {
		_ = h.ch.conn.Close()
	}
	h.ch = ch
	ready := h.readyLocked()
	select {
	case <-ready:
	default:
		close(ready)
	}
	return true
}

// detach forgets ch if it is still the pane's channel.
func (h *hostedCalls) detach(ch *hostedCallChannel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ch == ch {
		h.ch = nil
	}
}

// current is the channel, waiting up to wait for the first one to attach.
func (h *hostedCalls) current(wait time.Duration) *hostedCallChannel {
	h.mu.Lock()
	ch, ready := h.ch, h.readyLocked()
	h.mu.Unlock()
	if ch != nil || wait <= 0 {
		return ch
	}
	select {
	case <-ready:
	case <-time.After(wait):
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ch
}

// close ends the channel for good, with the pane.
func (h *hostedCalls) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.ch != nil {
		_ = h.ch.conn.Close()
		h.ch = nil
	}
}

// hostedCallChannel is one open report channel, on this machine's side.
type hostedCallChannel struct {
	conn    net.Conn
	writeMu sync.Mutex

	mu       sync.Mutex
	next     uint64
	pending  map[uint64]chan hostedCallReply
	inFlight int
	done     chan struct{}
}

// hostedCallRequest is one line this machine writes on the channel.
type hostedCallRequest struct {
	ID     uint64          `json:"id"`
	Verb   string          `json:"verb"`
	Params json.RawMessage `json:"params,omitempty"`
}

// hostedCallReply is one line the owner writes back.
type hostedCallReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *verbError      `json:"error,omitempty"`
}

func newHostedCallChannel(conn net.Conn) *hostedCallChannel {
	return &hostedCallChannel{conn: conn, pending: make(map[uint64]chan hostedCallReply), done: make(chan struct{})}
}

// call sends one request to the owner and waits for its answer.
func (c *hostedCallChannel) call(ctx context.Context, verb string, params json.RawMessage) (json.RawMessage, *verbError) {
	c.mu.Lock()
	if c.inFlight >= hostedCallsMaxInFlight {
		c.mu.Unlock()
		return nil, newVerbError(ErrVerbRateLimited, "this pane has "+strconv.Itoa(hostedCallsMaxInFlight)+" calls waiting on the machine that owns it. Wait for one to finish.")
	}
	c.next++
	id := c.next
	reply := make(chan hostedCallReply, 1)
	c.pending[id] = reply
	c.inFlight++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.inFlight--
		c.mu.Unlock()
	}()

	line, err := json.Marshal(hostedCallRequest{ID: id, Verb: verb, Params: params})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode the call: "+err.Error())
	}
	c.writeMu.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.conn.Write(append(line, '\n'))
	_ = c.conn.SetWriteDeadline(time.Time{})
	c.writeMu.Unlock()
	if err != nil {
		return nil, hostedChannelLost()
	}
	select {
	case r := <-reply:
		if r.Error != nil {
			return nil, r.Error
		}
		return r.Result, nil
	case <-c.done:
		return nil, hostedChannelLost()
	case <-ctx.Done():
		return nil, newVerbError(ErrVerbTimeout, "the machine that owns this pane did not answer in time")
	}
}

// readLoop delivers the owner's answers until the channel ends.
func (c *hostedCallChannel) readLoop(br *bufio.Reader) {
	defer close(c.done)
	for {
		line, err := readLimitedLine(br, hostedCallsMaxLine)
		if err != nil {
			return
		}
		var r hostedCallReply
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[r.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- r:
			default:
			}
		}
	}
}

func hostedChannelLost() *verbError {
	return newVerbError(ErrVerbHostUnreachable, "the connection to the machine that owns this pane dropped before it answered")
}

// verbPaneCalls turns this connection into a hosted pane's report channel. Only
// the owner can open it: the token is in the open-pane reply and nowhere else.
func (d *Daemon) verbPaneCalls(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane  string `json:"pane"`
		Token string `json:"token"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "pane-calls needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	if hp.callsToken == "" || subtle.ConstantTimeCompare([]byte(hp.callsToken), []byte(p.Token)) != 1 {
		return nil, newVerbError(ErrVerbForbidden, "pane-calls needs the token the open-pane reply carried, and this is not it")
	}
	cs.takeover = func(br *bufio.Reader) {
		d.serveHostedCallChannel(cs, br, hp)
	}
	return map[string]any{"type": "pane_calls", "pane": p.Pane}, nil
}

// serveHostedCallChannel holds one report channel open until either end stops.
func (d *Daemon) serveHostedCallChannel(cs *connState, br *bufio.Reader, hp *hostedPane) {
	conn := cs.conn
	// The verb reply went out under a write deadline, and a channel can sit
	// quiet for as long as the agent does.
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	ch := newHostedCallChannel(conn)
	if !hp.calls.attach(ch) {
		_ = conn.Close()
		return
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-d.ctx.Done():
		case <-cs.done:
		case <-stop:
			return
		}
		_ = conn.Close()
	}()
	ch.readLoop(br)
	close(stop)
	hp.calls.detach(ch)
	_ = conn.Close()
}

// hostedPaneByAddress finds the hosted pane a call names, by the owner's window
// id or by the pane id.
func (d *Daemon) hostedPaneByAddress(addr string) *hostedPane {
	if addr == "" {
		return nil
	}
	d.hostedPanesMu.Lock()
	defer d.hostedPanesMu.Unlock()
	for _, hp := range d.hostedPanes {
		if hp.id == addr || (hp.window != "" && hp.window == addr) {
			return hp
		}
	}
	return nil
}

// callerInHostedPane reports whether the process on cs runs in the hosted pane:
// it is the pane's process or a descendant, or its terminal is the pane's. A
// caller whose pid the platform does not give, or whose record cannot be read,
// is not.
func (d *Daemon) callerInHostedPane(cs *connState, hp *hostedPane) bool {
	if cs == nil || cs.peerPID <= 0 || hp.cmd == nil || hp.cmd.Process == nil {
		return false
	}
	root := hp.cmd.Process.Pid
	pid := cs.peerPID
	if pid == root {
		return true
	}
	ppid, tty, ok := readProcLineage(pid)
	if !ok {
		return false
	}
	if tty != 0 {
		if _, rootTTY, ok := readProcLineage(root); ok && rootTTY == tty {
			return true
		}
	}
	for depth := 0; depth < paneOriginMaxDepth && ppid > 1; depth++ {
		if ppid == root {
			return true
		}
		next, _, ok := readProcLineage(ppid)
		if !ok {
			return false
		}
		ppid = next
	}
	return false
}

// forwardHostedCall sends a call that names a hosted pane to the machine that
// owns it. handled is false for a call that does not name one, which is then
// run here as it always was.
func (d *Daemon) forwardHostedCall(cs *connState, verb string, params json.RawMessage) (result any, verr *verbError, handled bool) {
	field, ok := hostedCallVerbs[verb]
	if !ok || len(params) == 0 {
		return nil, nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(params, &fields) != nil {
		return nil, nil, false
	}
	var addr string
	if raw, ok := fields[field]; !ok || json.Unmarshal(raw, &addr) != nil {
		return nil, nil, false
	}
	hp := d.hostedPaneByAddress(addr)
	if hp == nil {
		return nil, nil, false
	}
	budget := hostedCallBudget
	if verb == "wait-for" {
		var cond string
		if raw, ok := fields["condition"]; ok {
			_ = json.Unmarshal(raw, &cond)
		}
		if cond != "agent-message" {
			return nil, hintedVerbError(ErrVerbForbidden, "a pane on another machine can wait only for agent-message on its own inbox", &VerbHint{
				Param:  "condition",
				Detail: "The pane's window is on the machine that owns it. Wait there for anything else.",
			}), true
		}
		ms := int64(30000)
		if raw, ok := fields["timeout"]; ok {
			_ = json.Unmarshal(raw, &ms)
		}
		budget = min(time.Duration(max(ms, 0))*time.Millisecond+5*time.Second, hostedWaitMax)
	}
	if !d.callerInHostedPane(cs, hp) {
		return nil, hintedVerbError(ErrVerbForbidden, verb+" names a pane this machine runs for another machine, and the caller is not in that pane", &VerbHint{
			Param:  field,
			Detail: "Only the process in a hosted pane, or one it started, can report as that pane. Nothing was sent.",
		}), true
	}
	wait := time.Duration(0)
	if hp.window != "" {
		wait = hostedCallsAttachWait
	}
	ch := hp.calls.current(wait)
	if ch == nil {
		if hp.window == "" {
			return nil, hintedVerbError(ErrVerbProtocolMismatch, "the tuios that owns this pane is too old to take reports from it", &VerbHint{
				Detail: "Update tuios on the machine that holds the session, then restart its daemon with 'tuios kill-server'. Until then the pane's agent is detected from here, not reported.",
			}), true
		}
		return nil, hostedChannelLost(), true
	}
	ctx, cancel := context.WithTimeout(d.ctx, budget)
	defer cancel()
	raw, verr := ch.call(ctx, verb, params)
	if verr != nil {
		return nil, verr, true
	}
	return raw, nil, true
}

// The owner's half.

// errHostedCallsFinal ends the owner's attempts to hold a report channel.
var errHostedCallsFinal = errors.New("the far machine will not take a report channel for this pane")

// serveHostedCalls holds the report channel of one window whose process is on
// another machine, for as long as the pane is open.
func (d *Daemon) serveHostedCalls(s *Session, windowID string, p *remotePane) {
	if p.callsToken == "" {
		return
	}
	for {
		if p.isClosed() || d.ctx.Err() != nil {
			return
		}
		if err := d.hostedCallsOnce(s, windowID, p); errors.Is(err, errHostedCallsFinal) {
			return
		}
		select {
		case <-d.ctx.Done():
			return
		case <-p.done:
			return
		case <-time.After(hostedCallsRetry):
		}
	}
}

// hostedCallsOnce opens one report channel and serves it until it ends.
func (d *Daemon) hostedCallsOnce(s *Session, windowID string, p *remotePane) error {
	ctx, cancel := context.WithTimeout(d.ctx, remotePaneOpenBudget)
	stream, err := p.fed.OpenConnection(ctx, p.host)
	cancel()
	if err != nil {
		return err
	}
	stop := context.AfterFunc(d.ctx, func() { _ = stream.Close() })
	defer stop()
	go func() {
		select {
		case <-p.done:
			_ = stream.Close()
		case <-d.ctx.Done():
		}
	}()
	defer func() { _ = stream.Close() }()

	req, _ := json.Marshal(verbRequest{
		ID:     json.RawMessage(`1`),
		Verb:   "pane-calls",
		Params: mustJSON(map[string]string{"pane": p.id, "token": p.callsToken}),
	})
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return err
	}
	br := bufio.NewReaderSize(stream, 64<<10)
	line, err := readLimitedLine(br, maxRemotePaneReply)
	if err != nil {
		return err
	}
	var resp struct {
		Error *verbError `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return errHostedCallsFinal
	}
	if resp.Error != nil {
		switch resp.Error.Code {
		case ErrVerbUnknownVerb, ErrVerbUnknownPane, ErrVerbForbidden:
			return errHostedCallsFinal
		}
		return resp.Error
	}

	// The calls in flight are not waited for when the channel ends. Their
	// answers would go to a dead stream, and waiting would hold off the next
	// channel, and every report the pane makes, for as long as the longest
	// wait-for asked. ended tells them the channel is gone, and a wait-for
	// stops on it.
	var writeMu sync.Mutex
	slots := make(chan struct{}, hostedCallsMaxInFlight)
	ended := make(chan struct{})
	defer close(ended)
	for {
		line, err := readLimitedLine(br, hostedCallsMaxLine)
		if err != nil {
			return err
		}
		var call hostedCallRequest
		if json.Unmarshal(line, &call) != nil {
			continue
		}
		reply := func(r hostedCallReply) {
			out, err := json.Marshal(r)
			if err != nil {
				return
			}
			writeMu.Lock()
			defer writeMu.Unlock()
			_, _ = stream.Write(append(out, '\n'))
		}
		select {
		case slots <- struct{}{}:
		default:
			reply(hostedCallReply{ID: call.ID, Error: newVerbError(ErrVerbRateLimited, "this pane has too many calls waiting")})
			continue
		}
		go func() {
			defer func() { <-slots }()
			result, verr := d.runHostedCall(s, windowID, p.host, call.Verb, call.Params, ended)
			r := hostedCallReply{ID: call.ID, Error: verr}
			if verr == nil {
				raw, err := json.Marshal(result)
				if err != nil {
					r.Error = newVerbError(ErrVerbInternal, "could not encode the answer")
				} else {
					r.Result = raw
				}
			}
			reply(r)
		}()
	}
}

// runHostedCall runs one call a pane on another machine sent, as the window the
// pane is drawn in. Whatever the request said about which session, window,
// sender or reader it is, the answer is this window's; see the file comment.
// ended is closed when the report channel the call came on ends, which stops a
// wait-for; nil means it never ends.
func (d *Daemon) runHostedCall(s *Session, windowID, host, verb string, params json.RawMessage, ended <-chan struct{}) (any, *verbError) {
	field, ok := hostedCallVerbs[verb]
	if !ok {
		return nil, newVerbError(ErrVerbForbidden, "a pane on another machine cannot call "+echoName(verb)+" on the machine that owns it")
	}
	entry, ok := verbRegistry[verb]
	if !ok {
		return nil, newVerbError(ErrVerbUnknownVerb, "unknown verb "+echoName(verb))
	}
	if !windowOnHost(s, windowID, host) {
		return nil, newVerbError(ErrVerbWindowNotFound, "the window this pane was drawn in is gone")
	}
	fields := map[string]json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &fields); err != nil {
			return nil, invalidParam("params", "params must be an object")
		}
	}
	for _, name := range hostedCallDropped {
		delete(fields, name)
	}
	// A selector reaches past the window into every session here. It is
	// refused rather than dropped, since dropping it would turn a message
	// meant for many panes into a notice nobody asked for.
	if _, ok := fields["select"]; ok {
		return nil, refuseSelectFromHostedPane(&connState{paneOnly: true})
	}
	if verb == "wait-for" {
		var cond string
		_ = json.Unmarshal(fields["condition"], &cond)
		if cond != "agent-message" {
			return nil, newVerbError(ErrVerbForbidden, "a pane on another machine can wait only for agent-message on its own inbox")
		}
		fields["timeout"] = mustJSON(clampHostedWait(fields["timeout"]))
	}
	if verb == "send-agent-message" {
		// The sender is the window. A request that named someone else, the
		// person included, is refused rather than quietly rewritten, so the
		// caller learns its message did not go out as it asked.
		var from string
		_ = json.Unmarshal(fields["from"], &from)
		if from != "" && from != windowID {
			return nil, hintedVerbError(ErrVerbForbidden, "a pane on another machine can send only as itself", &VerbHint{
				Param:  "from",
				Detail: "Nothing was sent. Send with from set to $TUIOS_PANE_ID. Only the person at an attached client can speak as human.",
			})
		}
	}
	fields["session"] = mustJSON(s.Name)
	fields[field] = mustJSON(windowID)
	raw := mustJSON(fields)
	if verr := checkParamNames(verb, entry, raw); verr != nil {
		return nil, verr
	}
	cs := &connState{clientID: "hosted:" + host, done: make(chan struct{}), paneOnly: true, hostedEnded: ended}
	return entry.handler(d, cs, raw)
}

// clampHostedWait is the timeout, in milliseconds, a forwarded wait-for runs
// with here: what the far machine asked, capped at hostedWaitMax. The far side
// caps its own budget the same way, and this side does not take its word for
// it. A missing, zero or unreadable timeout keeps wait-for's default.
func clampHostedWait(raw json.RawMessage) int {
	var ms int64
	if len(raw) > 0 && json.Unmarshal(raw, &ms) != nil {
		ms = 0
	}
	if ms <= 0 {
		return 0
	}
	return int(min(ms, hostedWaitMax.Milliseconds()))
}

// windowOnHost reports whether the session still has the window, with its
// process on host.
func windowOnHost(s *Session, windowID, host string) bool {
	st := s.GetState()
	for i := range st.Windows {
		if st.Windows[i].ID == windowID {
			return st.Windows[i].Host == host
		}
	}
	return false
}

// mustJSON marshals a value that cannot fail to marshal.
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}
