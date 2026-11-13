package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// versions this package speaks, newest first.
var versions = []int{2, 1}

// LatestVersion is the newest version this package speaks. Version 2 is a
// draft of the spec, which asks clients not to ask for it by default until it
// is final; DefaultVersion is what a client asks for when it does not say.
const (
	LatestVersion  = 2
	DefaultVersion = 1
)

// Implementation names a client or an agent.
type Implementation struct {
	// Name is the program's name, for programs.
	Name string `json:"name"`
	// Title is the name to show a person, when it differs from Name.
	Title string `json:"title,omitempty"`
	// Version is the program's version.
	Version string `json:"version"`
}

// Transport opens a connection to an agent. Connect calls Dial again for a
// fresh connection when the agent refuses the first version it is asked for:
// an agent that speaks only version 1 may not take a second initialize on
// the same connection. Closing what Dial returned ends that connection.
type Transport interface {
	Dial(ctx context.Context) (io.ReadWriteCloser, error)
}

// Pipe is a transport over one pair of streams, for an agent started
// elsewhere. It can be dialled once.
func Pipe(r io.Reader, w io.Writer) Transport { return &pipe{rw: pipeRW{r, w}} }

type pipe struct {
	mu   sync.Mutex
	rw   pipeRW
	used bool
}

type pipeRW struct {
	io.Reader
	io.Writer
}

func (p pipeRW) Close() error {
	if c, ok := p.Writer.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (p *pipe) Dial(context.Context) (io.ReadWriteCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used {
		return nil, errors.New("this transport cannot open a second connection")
	}
	p.used = true
	return p.rw, nil
}

// ClientOptions configure a Client.
type ClientOptions struct {
	// Info names the client to the agent.
	Info Implementation
	// MaxVersion is the version asked for first: 2 or 1, DefaultVersion
	// when 0.
	MaxVersion int
	// NegotiateTimeout bounds the wait for the agent's answer to a version 2
	// initialize, after which the client tries version 1 on a fresh
	// connection. One minute when 0. The version 1 initialize waits as long
	// as ctx lets it.
	NegotiateTimeout time.Duration
	// OnUpdate receives every session/update, in the order the agent sent
	// them, on the connection's read goroutine: it must not block.
	OnUpdate func(Update)
	// OnPermission receives each permission request, on the read goroutine.
	// Answer it with Choose or Cancel, from any goroutine. Nil answers every
	// request with the cancelled outcome.
	OnPermission func(*PermissionRequest)
}

// AgentInfo is what the agent said about itself in initialize.
type AgentInfo struct {
	Implementation
	// AuthMethods are the ways the agent can log in.
	AuthMethods []AuthMethod
	// Capabilities is the agent's capabilities object as it came: v2's
	// capabilities or v1's agentCapabilities.
	Capabilities json.RawMessage
}

// AuthMethod is one way the agent can log in.
type AuthMethod struct {
	// ID names the method in auth/login.
	ID string
	// Name is the method's name, for the person.
	Name string
	// Description says more about it, or is empty.
	Description string
	// Type is the method's type, such as terminal, when the agent names
	// one.
	Type string
}

// VersionError says the agent and the client share no version.
type VersionError struct {
	// Client is the versions the client speaks, Agent the ones the agent
	// named, when it did.
	Client, Agent []int
	// Refused holds the agent's answer to each version asked for, by
	// version, when it refused one.
	Refused map[int]error
	client  string
}

// Error names the versions of both sides, and the agent's answers when it
// refused a version.
func (e *VersionError) Error() string {
	name := firstOf(e.client, "the client")
	speaks := name + " speaks ACP " + versionList(e.Client)
	switch {
	case len(e.Agent) > 0 && len(e.Refused) == 0:
		return "the agent chose ACP " + versionList(e.Agent) + ", and " + speaks
	case len(e.Agent) > 0:
		return "the agent speaks ACP " + versionList(e.Agent) + ", and " + speaks
	}
	var parts []string
	for _, v := range e.Client {
		if err, ok := e.Refused[v]; ok {
			parts = append(parts, "version "+strconv.Itoa(v)+" ("+err.Error()+")")
		}
	}
	return "the agent refused ACP " + strings.Join(parts, " and ") + ", and " + speaks
}

func versionList(vs []int) string {
	if len(vs) == 1 {
		return "version " + strconv.Itoa(vs[0])
	}
	s := make([]string, len(vs))
	for i, v := range vs {
		s[i] = strconv.Itoa(v)
	}
	return "versions " + strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// Client is the client end of one ACP connection, in the version the agent
// and the client agreed on.
type Client struct {
	conn    *Conn
	rw      io.ReadWriteCloser
	version int
	agent   AgentInfo
	opts    ClientOptions

	mu       sync.Mutex
	sessions map[string]*Session
}

// Connect opens a connection, negotiates the version and returns the client.
//
// It asks for MaxVersion. An agent that speaks version 2 answers 2, and one
// that speaks only version 1 may answer 1, which the client then speaks on
// the same connection. When the agent refuses version 2 (an error, or the
// connection ends) and the error does not rule version 1 out, the client
// dials again and asks for 1. A *VersionError says when neither fits.
func Connect(ctx context.Context, t Transport, o ClientOptions) (*Client, error) {
	max := o.MaxVersion
	if max == 0 {
		max = DefaultVersion
	}
	if !slices.Contains(versions, max) {
		return nil, errorf("ACP version %d is not one this client speaks (%s)", max, versionList(versions))
	}
	asked := []int{max}
	if max == 2 {
		asked = []int{2, 1}
	}
	refused := map[int]error{}
	for i, v := range asked {
		c, err := dialInit(ctx, t, o, v, i == 0 && len(asked) > 1)
		if err == nil {
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var verr *VersionError
		if errors.As(err, &verr) {
			verr.Client, verr.client = asked, o.Info.Name
			return nil, verr
		}
		refused[v] = err
		// An agent that names the versions it speaks, and not 1, is not
		// asked again.
		var rpc *RPCError
		if errors.As(err, &rpc) && rpc.Code == codeUnsupportedProtocolVersion {
			var data struct {
				Supported []int `json:"supported"`
			}
			if unmarshal(rpc.Data, &data) == nil && len(data.Supported) > 0 {
				rest := asked[i+1:]
				if !slices.ContainsFunc(rest, func(v int) bool { return slices.Contains(data.Supported, v) }) {
					return nil, &VersionError{Client: asked, Agent: data.Supported, Refused: refused, client: o.Info.Name}
				}
			}
		}
	}
	return nil, &VersionError{Client: asked, Refused: refused, client: o.Info.Name}
}

// dialInit opens a connection and sends initialize asking for version v.
// bounded puts NegotiateTimeout on the wait.
func dialInit(ctx context.Context, t Transport, o ClientOptions, v int, bounded bool) (*Client, error) {
	rw, err := t.Dial(ctx)
	if err != nil {
		return nil, err
	}
	c := &Client{rw: rw, opts: o, sessions: map[string]*Session{}, version: v}
	c.conn = NewConn(rw, rw, ConnOptions{Cancels: true, OnNotify: c.onNotify, OnRequest: c.onRequest})
	ictx := ctx
	if bounded {
		d := o.NegotiateTimeout
		if d <= 0 {
			d = time.Minute
		}
		var cancel context.CancelFunc
		ictx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	params := map[string]any{"protocolVersion": v}
	if v >= 2 {
		params["info"] = o.Info
		params["capabilities"] = map[string]any{}
	} else {
		// Version 1: no file system and no terminal, so a conforming agent
		// never sends fs/* or terminal/*.
		params["clientInfo"] = o.Info
		params["clientCapabilities"] = map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		}
	}
	var res struct {
		ProtocolVersion   *int            `json:"protocolVersion"`
		Info              *Implementation `json:"info"`
		AgentInfo         *Implementation `json:"agentInfo"`
		Capabilities      json.RawMessage `json:"capabilities"`
		AgentCapabilities json.RawMessage `json:"agentCapabilities"`
		AuthMethods       []struct {
			MethodID    string `json:"methodId"`
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Type        string `json:"type"`
		} `json:"authMethods"`
	}
	if err := c.conn.Call(ictx, "initialize", params, &res); err != nil {
		_ = rw.Close()
		return nil, err
	}
	chose := 0
	if res.ProtocolVersion != nil {
		chose = *res.ProtocolVersion
	}
	switch {
	case chose == v:
	case chose >= 1 && chose < v:
		// The agent chose an older version it speaks, as the spec lets it.
	default:
		_ = rw.Close()
		return nil, &VersionError{Agent: []int{chose}}
	}
	if chose >= 2 && res.Info == nil && (res.AgentInfo != nil || len(res.AgentCapabilities) > 0) {
		// info is required in version 2. An answer in the version 1 shape
		// that says 2 is a version 1 agent echoing what it was asked for.
		chose = 1
	}
	c.version = chose
	info := AgentInfo{}
	if chose >= 2 {
		if res.Info != nil {
			info.Implementation = *res.Info
		}
		info.Capabilities = res.Capabilities
	} else {
		if res.AgentInfo != nil {
			info.Implementation = *res.AgentInfo
		}
		info.Capabilities = res.AgentCapabilities
	}
	for _, m := range res.AuthMethods {
		id := m.MethodID
		if chose < 2 {
			id = firstOf(m.ID, m.MethodID)
		}
		info.AuthMethods = append(info.AuthMethods, AuthMethod{ID: id, Name: m.Name, Description: m.Description, Type: m.Type})
	}
	c.agent = info
	return c, nil
}

// Version is the version the connection speaks.
func (c *Client) Version() int { return c.version }

// Agent is what the agent said about itself.
func (c *Client) Agent() AgentInfo { return c.agent }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// Close ends the connection. For a process over stdio that closes its
// stdin, which ends every session it has open.
func (c *Client) Close() error { return c.rw.Close() }

// SessionSetup is what the agent said when a session opened.
type SessionSetup struct {
	// Models is version 1's unstable models object, as it came.
	Models json.RawMessage
	// Options and Commands are the session's config options and slash
	// commands, when the agent listed them.
	Options []ConfigOption
	// Commands are the slash commands, as a CommandsUpdate gives them.
	Commands []Command
}

// Session is one conversation with the agent.
type Session struct {
	// ID is the agent's id for the session.
	ID string
	// Setup is what the agent said when the session opened.
	Setup SessionSetup
	c     *Client

	mu   sync.Mutex
	turn *turn
}

// turn is a version 2 prompt waiting for the idle state that ends it.
type turn struct {
	accepted bool
	active   bool
	done     chan TurnEnd
	once     sync.Once
}

func (t *turn) end(e TurnEnd) { t.once.Do(func() { t.done <- e }) }

// TurnEnd is how a turn ended: the stop reason, empty when the agent did not
// say, and the error a stop reason error carries.
type TurnEnd struct {
	// Reason is one of the Stop constants, another reason the agent named,
	// or empty.
	Reason string
	// Error is the failure a stop reason error carries, or nil.
	Error *RPCError
}

type setupAnswer struct {
	SessionID         string            `json:"sessionId"`
	Models            json.RawMessage   `json:"models"`
	ConfigOptions     []json.RawMessage `json:"configOptions"`
	AvailableCommands json.RawMessage   `json:"availableCommands"`
}

func (c *Client) session(id string, a setupAnswer) *Session {
	s := &Session{ID: id, c: c}
	s.Setup.Models = a.Models
	s.Setup.Options = parseConfigOptions(c.version, a.ConfigOptions)
	if len(a.AvailableCommands) > 0 {
		raw, _ := marshal(map[string]json.RawMessage{"sessionUpdate": json.RawMessage(`"available_commands_update"`), "availableCommands": a.AvailableCommands})
		if u, ok := parseUpdate(c.version, id, raw).(*CommandsUpdate); ok {
			s.Setup.Commands = u.Commands
		}
	}
	c.mu.Lock()
	c.sessions[id] = s
	c.mu.Unlock()
	return s
}

// NewSession opens a conversation in cwd, which must be absolute.
func (c *Client) NewSession(ctx context.Context, cwd string) (*Session, error) {
	params := map[string]any{"cwd": cwd}
	if c.version < 2 {
		params["mcpServers"] = []any{}
	}
	var a setupAnswer
	if err := c.conn.Call(ctx, "session/new", params, &a); err != nil {
		return nil, err
	}
	if a.SessionID == "" {
		return nil, errors.New("the agent returned no sessionId")
	}
	return c.session(a.SessionID, a), nil
}

// ResumeSession reattaches to a session the agent kept, without a replay of
// its history. Version 2 agents all can; a version 1 agent must list
// sessionCapabilities.resume.
func (c *Client) ResumeSession(ctx context.Context, id, cwd string) (*Session, error) {
	params := map[string]any{"sessionId": id, "cwd": cwd}
	if c.version < 2 {
		params["mcpServers"] = []any{}
	}
	var a setupAnswer
	if err := c.conn.Call(ctx, "session/resume", params, &a); err != nil {
		return nil, err
	}
	return c.session(id, a), nil
}

// SessionInfo is one session session/list names.
type SessionInfo struct {
	// ID is the agent's id for the session.
	ID string `json:"sessionId"`
	// Cwd is the folder the session works in.
	Cwd string `json:"cwd"`
	// Title is the session's title, when it has one.
	Title string `json:"title,omitempty"`
	// UpdatedAt is when the session last changed, an ISO 8601 time, when
	// the agent says.
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ListSessions lists the sessions the agent kept, for cwd when it is not
// empty, following the agent's pages to the end.
func (c *Client) ListSessions(ctx context.Context, cwd string) ([]SessionInfo, error) {
	var all []SessionInfo
	cursor := ""
	for range 1000 {
		params := map[string]any{}
		if cwd != "" {
			params["cwd"] = cwd
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Sessions   []SessionInfo `json:"sessions"`
			NextCursor string        `json:"nextCursor"`
		}
		if err := c.conn.Call(ctx, "session/list", params, &res); err != nil {
			return all, err
		}
		all = append(all, res.Sessions...)
		if res.NextCursor == "" || res.NextCursor == cursor {
			return all, nil
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// Prompt sends one prompt and returns when the turn it started ends.
//
// In version 1 that is the agent's answer to session/prompt. In version 2
// the answer only says the prompt was taken, and the turn ends with the
// state_update to idle that follows it. An error means the prompt was not
// taken, or the connection ended.
func (s *Session) Prompt(ctx context.Context, blocks ...ContentBlock) (TurnEnd, error) {
	params := map[string]any{"sessionId": s.ID, "prompt": blocks}
	var res struct {
		MessageID  string `json:"messageId"`
		StopReason string `json:"stopReason"`
	}
	if s.c.version < 2 {
		if err := s.c.conn.Call(ctx, "session/prompt", params, &res); err != nil {
			return TurnEnd{}, err
		}
		return TurnEnd{Reason: res.StopReason}, nil
	}
	// The turn is set up before the prompt is sent: the agent may report
	// the state that ends it before it answers.
	t := &turn{done: make(chan TurnEnd, 1)}
	s.mu.Lock()
	s.turn = t
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.turn == t {
			s.turn = nil
		}
		s.mu.Unlock()
	}()
	if err := s.c.conn.Call(ctx, "session/prompt", params, &res); err != nil {
		return TurnEnd{}, err
	}
	if res.MessageID == "" && res.StopReason != "" {
		// An answer with a stop reason and no message id is the version 1
		// answer: that agent ends the turn here.
		return TurnEnd{Reason: res.StopReason}, nil
	}
	s.mu.Lock()
	t.accepted = true
	s.mu.Unlock()
	select {
	case e := <-t.done:
		return e, nil
	case <-s.c.conn.Done():
		// A turn whose end was reported just before the connection closed
		// still ended.
		select {
		case e := <-t.done:
			return e, nil
		default:
		}
		return TurnEnd{}, ErrClosed
	case <-ctx.Done():
		return TurnEnd{}, ctx.Err()
	}
}

// Cancel asks the agent to stop the session's foreground work. The turn ends
// as the agent reports it: with the cancelled stop reason.
func (s *Session) Cancel() error {
	return s.c.conn.Notify("session/cancel", map[string]string{"sessionId": s.ID})
}

// Close closes the session at the agent (session/close, version 2 only),
// which also stops its background work.
func (s *Session) Close(ctx context.Context) error {
	if s.c.version < 2 {
		return nil
	}
	return s.c.conn.Call(ctx, "session/close", map[string]string{"sessionId": s.ID}, nil)
}

// onState applies a state update to the session's turn.
func (s *Session) onState(u *StateUpdate) {
	s.mu.Lock()
	t := s.turn
	if t == nil {
		s.mu.Unlock()
		return
	}
	switch u.State {
	case StateRunning, StateRequiresAction:
		t.active = true
		s.mu.Unlock()
		return
	case StateIdle:
		// Idle ends the turn once the turn has run, or once the prompt was
		// taken and the idle says why it stopped. An idle with neither is a
		// state from before the prompt, and does not end it.
		ends := t.active || (t.accepted && u.StopReason != "")
		s.mu.Unlock()
		if ends {
			t.end(TurnEnd{Reason: u.StopReason, Error: u.Error})
		}
		return
	}
	s.mu.Unlock()
}

func (c *Client) lookup(id string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[id]
}

func (c *Client) onNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var p struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if unmarshal(params, &p) != nil {
		return
	}
	u := parseUpdate(c.version, p.SessionID, p.Update)
	if u == nil {
		return
	}
	if st, ok := u.(*StateUpdate); ok {
		if s := c.lookup(p.SessionID); s != nil {
			s.onState(st)
		}
	}
	if c.opts.OnUpdate != nil {
		c.opts.OnUpdate(u)
	}
}

// PermissionRequest is the agent asking before it does something.
type PermissionRequest struct {
	// Session is the id of the session that asks.
	Session string
	// Title and Description are version 2's words for the question; empty
	// in version 1, where the tool call is the question.
	Title, Description string
	// SubjectType is tool_call, command, empty for none, or a type this
	// package does not know.
	SubjectType string
	// ToolCall is the call the request is about: version 1's toolCall, or a
	// version 2 tool_call subject.
	ToolCall *ToolCallUpdate
	// Command is a version 2 command subject.
	Command *CommandSubject
	// Options are the answers the agent offers, in its order.
	Options []PermissionOption

	req *Request
}

// CommandSubject is a command the agent asks to run, in cwd.
type CommandSubject struct {
	// Command is the command line.
	Command string `json:"command"`
	// Cwd is the folder it runs in.
	Cwd string `json:"cwd"`
	// ToolCallID names the tool call the command belongs to, when it does.
	ToolCallID string `json:"toolCallId,omitempty"`
	// TerminalID names the terminal it runs in, when the agent says.
	TerminalID string `json:"terminalId,omitempty"`
}

// PermissionOption is one answer to a permission request. Kind is
// allow_once, allow_always, reject_once, reject_always, or another.
type PermissionOption struct {
	// ID is what Choose takes.
	ID string `json:"optionId"`
	// Name is the answer, in words for the person.
	Name string `json:"name"`
	// Kind says what the answer allows.
	Kind string `json:"kind"`
}

// Choose answers with the option whose id is optionID.
func (p *PermissionRequest) Choose(optionID string) {
	p.req.Reply(map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": optionID}})
}

// Cancel answers that the request is dismissed, which allows nothing.
func (p *PermissionRequest) Cancel() {
	p.req.Reply(map[string]any{"outcome": map[string]string{"outcome": "cancelled"}})
}

// Withdrawn is closed when the agent withdraws the request with
// $/cancel_request, or the connection ends, before it is answered. A
// withdrawn request must still be answered: Cancel does that.
func (p *PermissionRequest) Withdrawn() <-chan struct{} { return p.req.Cancelled() }

func (c *Client) onRequest(r *Request) {
	if r.Method != "session/request_permission" {
		r.ReplyError(CodeMethodNotFound, "this client does not offer "+r.Method)
		return
	}
	var p struct {
		SessionID   string             `json:"sessionId"`
		Title       string             `json:"title"`
		Description string             `json:"description"`
		ToolCall    *wireTool          `json:"toolCall"`
		Subject     json.RawMessage    `json:"subject"`
		Options     []PermissionOption `json:"options"`
	}
	if unmarshal(r.Params, &p) != nil || len(p.Options) == 0 {
		r.ReplyError(CodeInvalidParams, "a permission request needs options")
		return
	}
	pr := &PermissionRequest{Session: p.SessionID, Options: p.Options, req: r}
	h := head{SessionID: p.SessionID, KindName: "tool_call_update"}
	if c.version < 2 {
		if p.ToolCall != nil {
			pr.SubjectType = "tool_call"
			pr.ToolCall = p.ToolCall.update(h, false)
		}
	} else {
		pr.Title, pr.Description = p.Title, p.Description
		var subject struct {
			Type     string    `json:"type"`
			ToolCall *wireTool `json:"toolCall"`
			CommandSubject
		}
		if len(p.Subject) > 0 && unmarshal(p.Subject, &subject) == nil {
			pr.SubjectType = subject.Type
			switch subject.Type {
			case "tool_call":
				if subject.ToolCall != nil {
					pr.ToolCall = subject.ToolCall.update(h, false)
				}
			case "command":
				cmd := subject.CommandSubject
				pr.Command = &cmd
			}
		}
	}
	if c.opts.OnPermission == nil {
		pr.Cancel()
		return
	}
	c.opts.OnPermission(pr)
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
