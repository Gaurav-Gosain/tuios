package agentproto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// The ACP client, protocol version 1.
//
// Source: the protocol overview and schema at agentclientprotocol.com, checked
// against two clients that ship it (emdash's acp-agent-connection.ts and
// t3code's AcpSessionRuntime.ts, both sending protocolVersion 1) and one agent
// (opencode's src/acp). The methods used:
//
//	initialize                  client to agent, first
//	session/new                 client to agent, opens the conversation
//	session/prompt              client to agent, one turn; answers with stopReason
//	session/cancel              client to agent, notification
//	session/update              agent to client, notification: message and
//	                            thought chunks, tool calls and their updates, plans
//	session/request_permission  agent to client, request: the options to answer with
//
// The client advertises fs.readTextFile, fs.writeTextFile and terminal as
// false, so a conforming agent never sends fs/* or terminal/*; one that does
// gets method not found. authenticate is never called: an agent that needs a
// login refuses session/new, and the pane says to log in with the agent's own
// command.

// acpProtocolVersion is the ACP version this client speaks.
const acpProtocolVersion = 1

// ACP is an ACP agent.
type ACP struct {
	conn    *Conn
	emit    func(Event)
	version string

	mu      sync.Mutex
	session string
	tools   map[string]Tool
}

// NewACP speaks ACP to an agent whose stdout is r and whose stdin is w. emit
// receives what the agent shows; it is called on the connection's goroutine,
// one event at a time.
func NewACP(r io.Reader, w io.Writer, version string, emit func(Event)) *ACP {
	a := &ACP{emit: emit, version: version, tools: make(map[string]Tool)}
	a.conn = NewConn(r, w, ConnOptions{OnNotify: a.onNotify, OnRequest: a.onRequest})
	return a
}

// Done is closed when the agent's connection ends.
func (a *ACP) Done() <-chan struct{} { return a.conn.Done() }

// Start sends initialize and session/new.
func (a *ACP) Start(ctx context.Context, cwd string) (Info, error) {
	var init struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       *struct {
			Name    string `json:"name"`
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"agentInfo"`
		AuthMethods []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"authMethods"`
	}
	err := a.conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion": acpProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]string{"name": "tuios", "title": "tuios", "version": a.version},
	}, &init)
	if err != nil {
		return Info{}, fmt.Errorf("initialize: %w", err)
	}
	if init.ProtocolVersion != acpProtocolVersion {
		return Info{}, fmt.Errorf("the agent speaks ACP version %d, and tuios speaks version %d", init.ProtocolVersion, acpProtocolVersion)
	}
	var info Info
	if ai := init.AgentInfo; ai != nil {
		info.Agent = strings.TrimSpace(firstOf(ai.Title, ai.Name) + " " + ai.Version)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	err = a.conn.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &created)
	if err != nil {
		if len(init.AuthMethods) > 0 {
			names := make([]string, 0, len(init.AuthMethods))
			for _, m := range init.AuthMethods {
				names = append(names, firstOf(m.Name, m.ID))
			}
			return info, fmt.Errorf("session/new: %w. The agent may need a login (%s): log in by running it in a shell, then start it here again", err, strings.Join(names, ", "))
		}
		return info, fmt.Errorf("session/new: %w", err)
	}
	if created.SessionID == "" {
		return info, fmt.Errorf("session/new: the agent returned no sessionId")
	}
	a.mu.Lock()
	a.session = created.SessionID
	a.mu.Unlock()
	info.Session = created.SessionID
	return info, nil
}

// Prompt sends session/prompt and waits for its stopReason.
func (a *ACP) Prompt(ctx context.Context, text string) (TurnResult, error) {
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	var res struct {
		StopReason string `json:"stopReason"`
	}
	err := a.conn.Call(ctx, "session/prompt", map[string]any{
		"sessionId": session,
		"prompt":    []any{map[string]string{"type": "text", "text": text}},
	}, &res)
	if err != nil {
		return TurnResult{Stop: StopFailed, Detail: err.Error()}, err
	}
	switch res.StopReason {
	case "end_turn":
		return TurnResult{Stop: StopFinished}, nil
	case "cancelled":
		return TurnResult{Stop: StopCancelled}, nil
	case "refusal":
		return TurnResult{Stop: StopRefused, Detail: "the agent refused"}, nil
	case "max_tokens", "max_turn_requests":
		return TurnResult{Stop: StopLimit, Detail: "stopped at " + res.StopReason}, nil
	}
	return TurnResult{Stop: StopFinished, Detail: res.StopReason}, nil
}

// Cancel sends session/cancel.
func (a *ACP) Cancel() {
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if session != "" {
		_ = a.conn.Notify("session/cancel", map[string]string{"sessionId": session})
	}
}

// acpContent is a content block. Only text is shown; an image or a resource
// is named by its type.
type acpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URI  string `json:"uri"`
}

func (c acpContent) String() string {
	switch c.Type {
	case "text":
		return c.Text
	case "resource_link":
		return c.URI
	case "":
		return ""
	}
	return "[" + c.Type + "]"
}

// acpToolFields are the fields of a tool_call and a tool_call_update. In an
// update every field is optional, and one that is absent keeps its value.
type acpToolFields struct {
	ToolCallID string          `json:"toolCallId"`
	Title      *string         `json:"title"`
	Kind       *string         `json:"kind"`
	Status     *string         `json:"status"`
	Content    *[]acpToolItem  `json:"content"`
	RawInput   json.RawMessage `json:"rawInput"`
}

// acpToolItem is one entry of a tool call's content.
type acpToolItem struct {
	Type    string      `json:"type"`
	Content *acpContent `json:"content"`
	Path    string      `json:"path"`
	OldText *string     `json:"oldText"`
	NewText string      `json:"newText"`
}

func (a *ACP) onNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var p struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var kind struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if json.Unmarshal(p.Update, &kind) != nil {
		return
	}
	switch kind.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk":
		var u struct {
			Content acpContent `json:"content"`
		}
		if json.Unmarshal(p.Update, &u) == nil {
			if s := u.Content.String(); s != "" {
				a.emit(Text{Text: s, Thought: kind.SessionUpdate == "agent_thought_chunk"})
			}
		}
	case "tool_call", "tool_call_update":
		var u acpToolFields
		if json.Unmarshal(p.Update, &u) == nil && u.ToolCallID != "" {
			a.emit(a.mergeTool(u, kind.SessionUpdate == "tool_call"))
		}
	case "plan":
		var u struct {
			Entries []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"entries"`
		}
		if json.Unmarshal(p.Update, &u) == nil {
			plan := Plan{}
			for _, e := range u.Entries {
				plan.Entries = append(plan.Entries, PlanEntry{Content: e.Content, Status: e.Status})
			}
			a.emit(plan)
		}
	}
	// user_message_chunk is the agent replaying what was sent, and the mode,
	// command and config updates change nothing the pane shows.
}

// mergeTool folds a tool_call or tool_call_update into what is known about
// the call. A tool_call starts it afresh.
func (a *ACP) mergeTool(u acpToolFields, fresh bool) Tool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tools[u.ToolCallID]
	if fresh || !ok {
		t = Tool{ID: u.ToolCallID, Status: ToolPending}
	}
	if u.Title != nil {
		t.Title = *u.Title
	}
	if u.Kind != nil {
		t.Kind = *u.Kind
	}
	if u.Status != nil {
		t.Status = acpToolStatus(*u.Status)
	}
	if u.Content != nil {
		t.Diffs, t.Text, t.Terminal = nil, "", false
		var text []string
		for _, item := range *u.Content {
			switch item.Type {
			case "diff":
				t.Diffs = append(t.Diffs, Diff{Path: item.Path, Old: item.OldText, New: item.NewText})
			case "terminal":
				t.Terminal = true
			case "content":
				if item.Content != nil {
					if s := item.Content.String(); s != "" {
						text = append(text, s)
					}
				}
			}
		}
		t.Text = strings.Join(text, "\n")
	}
	if len(u.RawInput) > 0 && string(u.RawInput) != "null" {
		t.Input, t.OtherInput = flatInput(u.RawInput)
	}
	a.tools[u.ToolCallID] = t
	return t
}

// acpToolStatus maps ACP's tool call status to the Tool status.
func acpToolStatus(s string) string {
	switch s {
	case "in_progress":
		return ToolRunning
	case "completed":
		return ToolDone
	case "failed":
		return ToolFailed
	}
	return ToolPending
}

// flatInput reads a raw input object as its string fields. other is true when
// it holds anything else, or is not an object.
func flatInput(raw json.RawMessage) (map[string]string, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, true
	}
	out := make(map[string]string, len(obj))
	other := false
	for k, v := range obj {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out[k] = s
			continue
		}
		if string(v) != "null" {
			other = true
		}
	}
	return out, other
}

func (a *ACP) onRequest(r *Request) {
	if r.Method != "session/request_permission" {
		r.ReplyError(codeMethodNotFound, "tuios does not offer "+r.Method)
		return
	}
	var p struct {
		ToolCall acpToolFields `json:"toolCall"`
		Options  []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(r.Params, &p) != nil || len(p.Options) == 0 {
		r.ReplyError(codeInvalidParams, "a permission request needs options")
		return
	}
	tool := a.mergeTool(p.ToolCall, false)
	options := make([]Option, len(p.Options))
	ids := make([]string, len(p.Options))
	for i, o := range p.Options {
		ids[i] = o.OptionID
		options[i] = Option{Label: firstOf(o.Name, o.OptionID), Decision: acpDecision(o.Kind)}
	}
	// Two options of one kind would make a decision ambiguous: only the
	// first keeps it, and the rest are for the pane.
	seen := map[string]bool{}
	for i := range options {
		if d := options[i].Decision; d != "" {
			if seen[d] {
				options[i].Decision = ""
			}
			seen[d] = true
		}
	}
	perm := NewPermission(tool, options, func(i int) {
		r.Reply(map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": ids[i]}})
	}, func() {
		r.Reply(map[string]any{"outcome": map[string]string{"outcome": "cancelled"}})
	})
	a.emit(perm)
}

// acpDecision is the Inbox decision an option kind is. allow_always is not
// offered to the Inbox: what it allows from then on is the agent's to decide
// and is not in the request, so the Inbox could not show it. reject_always
// would deny more than the call, so it is not the Inbox's deny either.
func acpDecision(kind string) string {
	switch kind {
	case "allow_once":
		return DecisionOnce
	case "reject_once":
		return DecisionDeny
	}
	return ""
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
