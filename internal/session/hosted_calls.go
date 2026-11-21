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
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

var hostedCallVerbs = map[string]string{
	"set-agent-state":       "window",
	"set-agent-meta":        "window",
	"set-agent-session":     "window",
	"read-agent-messages":   "to",
	"send-agent-message":    "from",
	"wait-for":              "window",
	"report-agent-activity": "window",
	"agent-mail-notice":     "window",
	"report-program-status": "window",
}

var hostedCallDropped = []string{"transcript_path", "transcript_leaf", "harness_pid", "human_nonce", "from_host", "any_session", "host"}

const (
	hostedCallsMaxLine     = 1 << 20
	hostedCallsMaxInFlight = 16
	hostedCallBudget       = 15 * time.Second
	hostedCallsAttachWait  = 5 * time.Second
	hostedCallsRetry       = 2 * time.Second
	hostedWaitMax          = time.Hour
)

var hostedWindowIDPattern = lazyre.New(`^[A-Za-z0-9._-]{1,64}$`)

func newHostedCallsToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

type hostedCalls struct {
	mu     sync.Mutex
	ch     *hostedCallChannel
	ready  chan struct{}
	closed bool
}

func (h *hostedCalls) readyLocked() chan struct{} {
	if h.ready == nil {
		h.ready = make(chan struct{})
	}
	return h.ready
}

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

func (h *hostedCalls) detach(ch *hostedCallChannel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ch == ch {
		h.ch = nil
	}
}

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

func (h *hostedCalls) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.ch != nil {
		_ = h.ch.conn.Close()
		h.ch = nil
	}
}

type hostedCallChannel struct {
	conn    net.Conn
	writeMu sync.Mutex

	mu       sync.Mutex
	next     uint64
	pending  map[uint64]chan hostedCallReply
	inFlight int
	done     chan struct{}
}

type hostedCallRequest struct {
	ID     uint64          `json:"id"`
	Verb   string          `json:"verb"`
	Params json.RawMessage `json:"params,omitempty"`
}

type hostedCallReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *verbError      `json:"error,omitempty"`
}

func newHostedCallChannel(conn net.Conn) *hostedCallChannel {
	return &hostedCallChannel{conn: conn, pending: make(map[uint64]chan hostedCallReply), done: make(chan struct{})}
}

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

	line, err := jsonx.Marshal(hostedCallRequest{ID: id, Verb: verb, Params: params})
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

func (c *hostedCallChannel) readLoop(br *bufio.Reader) {
	defer close(c.done)
	for {
		line, err := readLimitedLine(br, hostedCallsMaxLine)
		if err != nil {
			return
		}
		var r hostedCallReply
		if jsonx.Unmarshal(line, &r) != nil {
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

func (d *Daemon) serveHostedCallChannel(cs *connState, br *bufio.Reader, hp *hostedPane) {
	conn := cs.conn
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

func (d *Daemon) callerInHostedPane(cs *connState, hp *hostedPane) bool {
	if cs == nil || cs.peerPID <= 0 || hp.cmd == nil || hp.cmd.Process == nil {
		return false
	}
	in, _ := processInPane(cs.peerPID, hp.cmd.Process.Pid)
	return in
}

func (d *Daemon) forwardHostedCall(cs *connState, verb string, params json.RawMessage) (result any, verr *verbError, handled bool) {
	field, ok := hostedCallVerbs[verb]
	if !ok || len(params) == 0 {
		return nil, nil, false
	}
	var fields map[string]json.RawMessage
	if jsonx.Unmarshal(params, &fields) != nil {
		return nil, nil, false
	}
	var addr string
	if raw, ok := fields[field]; !ok || jsonx.Unmarshal(raw, &addr) != nil {
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
			_ = jsonx.Unmarshal(raw, &cond)
		}
		if cond != "agent-message" {
			return nil, hintedVerbError(ErrVerbForbidden, "a pane on another machine can wait only for agent-message on its own inbox", &VerbHint{
				Param:  "condition",
				Detail: "The pane's window is on the machine that owns it. Wait there for anything else.",
			}), true
		}
		ms := int64(30000)
		if raw, ok := fields["timeout"]; ok {
			_ = jsonx.Unmarshal(raw, &ms)
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

var errHostedCallsFinal = errors.New("the far machine will not take a report channel for this pane")

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

func (d *Daemon) hostedCallsOnce(s *Session, windowID string, p *remotePane) error {
	ctx, cancel := context.WithTimeout(d.ctx, remotePaneOpenBudget)
	stream, err := p.fed.OpenConnection(ctx, p.host)
	cancel()
	if err != nil {
		return err
	}
	stop := context.AfterFunc(d.ctx, func() { _ = stream.Close() })
	defer stop()
	exit := make(chan struct{})
	defer close(exit)
	go func() {
		select {
		case <-p.done:
			_ = stream.Close()
		case <-exit:
		}
	}()
	defer func() { _ = stream.Close() }()

	req, _ := jsonx.Marshal(verbRequest{
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
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return errHostedCallsFinal
	}
	if resp.Error != nil {
		switch resp.Error.Code {
		case ErrVerbUnknownVerb, ErrVerbUnknownPane, ErrVerbForbidden:
			return errHostedCallsFinal
		}
		return resp.Error
	}

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
		if jsonx.Unmarshal(line, &call) != nil {
			continue
		}
		reply := func(r hostedCallReply) {
			out, err := jsonx.Marshal(r)
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
				raw, err := jsonx.Marshal(result)
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

func (d *Daemon) runHostedCall(s *Session, windowID, host, verb string, params json.RawMessage, ended <-chan struct{}) (any, *verbError) {
	field, ok := hostedCallVerbs[verb]
	if !ok {
		return nil, newVerbError(ErrVerbForbidden, "a pane on another machine cannot call "+echoName(verb)+" on the machine that owns it")
	}
	if verr := d.agentsOffRefusal(verb, params); verr != nil {
		return nil, verr
	}
	entry, ok := verbRegistry()[verb]
	if !ok {
		return nil, newVerbError(ErrVerbUnknownVerb, "unknown verb "+echoName(verb))
	}
	if !windowOnHost(s, windowID, host) {
		return nil, newVerbError(ErrVerbWindowNotFound, "the window this pane was drawn in is gone")
	}
	fields := map[string]json.RawMessage{}
	if len(params) > 0 {
		if err := jsonx.Unmarshal(params, &fields); err != nil {
			return nil, invalidParam("params", "params must be an object")
		}
	}
	for _, name := range hostedCallDropped {
		delete(fields, name)
	}
	if _, ok := fields["select"]; ok {
		return nil, refuseSelectFromHostedPane(&connState{paneOnly: true})
	}
	if verb == "wait-for" {
		var cond string
		_ = jsonx.Unmarshal(fields["condition"], &cond)
		if cond != "agent-message" {
			return nil, newVerbError(ErrVerbForbidden, "a pane on another machine can wait only for agent-message on its own inbox")
		}
		fields["timeout"] = mustJSON(clampHostedWait(fields["timeout"]))
	}
	if verb == "send-agent-message" {
		var from string
		_ = jsonx.Unmarshal(fields["from"], &from)
		if from != "" && from != windowID {
			return nil, hintedVerbError(ErrVerbForbidden, "a pane on another machine can send only as itself", &VerbHint{
				Param:  "from",
				Detail: "Nothing was sent. Send with from set to $TUIOS_PANE_ID. Only the person at an attached client can speak as human.",
			})
		}
	}
	fields["session"] = mustJSON(s.Name())
	fields[field] = mustJSON(windowID)
	raw := mustJSON(fields)
	if verr := checkParamNames(verb, entry, raw); verr != nil {
		return nil, verr
	}
	cs := &connState{clientID: "hosted:" + host, done: make(chan struct{}), paneOnly: true, hostedEnded: ended, hostedWindow: windowID}
	return entry.handler(d, cs, raw)
}

func clampHostedWait(raw json.RawMessage) int {
	var ms int64
	if len(raw) > 0 && jsonx.Unmarshal(raw, &ms) != nil {
		ms = 0
	}
	if ms <= 0 {
		return 0
	}
	return int(min(ms, hostedWaitMax.Milliseconds()))
}

func windowOnHost(s *Session, windowID, host string) bool {
	st := s.GetState()
	for i := range st.Windows {
		if st.Windows[i].ID == windowID {
			return st.Windows[i].Host == host
		}
	}
	return false
}

func mustJSON(v any) json.RawMessage {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}
