package agentproto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// The Codex app-server client, v2 threads and turns.
//
// Source: codex-rs/app-server-protocol in the openai/codex repository (commit
// d93909a9, 2026-09-22): src/protocol/common.rs for the method names and
// schema/typescript/v2 for the shapes. The protocol is large and moves fast at
// its edges (accounts, plugins, realtime, attachments); this client uses only
// its core, which has kept its shape:
//
//	initialize, then the initialized notification
//	thread/start {cwd}                     opens the conversation
//	turn/start {threadId, input}           one turn; answers at once with the turn
//	turn/interrupt {threadId, turnId}      cancels it
//	turn/completed                         notification: the turn ended, with its status
//	item/started, item/completed           notifications: commands, file changes, messages
//	item/agentMessage/delta                notification: the reply as it streams
//	item/reasoning/summaryTextDelta        notification: the reasoning summary
//	error                                  notification: a turn error, maybe retried
//	thread/tokenUsage/updated              notification: tokens used, of the model's window
//	turn/plan/updated                      notification: the turn's plan
//	model/rerouted                         notification: another model answers now
//	item/commandExecution/requestApproval  request: run this command?
//	item/fileChange/requestApproval        request: apply this change?
//
// The client does not opt into the experimental API, so the experimental
// requests (item/tool/requestUserInput among them) are not sent. Every other
// request, an MCP elicitation, a permissions request, a dynamic tool call, is
// answered with method not found, which Codex treats as not approved.
//
// Messages carry no "jsonrpc" member either way (codex-rs rpc.rs).

// Codex is a Codex app-server.
type Codex struct {
	conn    *Conn
	emit    func(Event)
	version string

	mu      sync.Mutex
	thread  string
	turn    string
	items   map[string]Tool
	streams map[string]bool
	ended   map[string]TurnResult
	waiters map[string]chan TurnResult
}

// NewCodex speaks the app-server protocol to a Codex whose stdout is r and
// whose stdin is w. emit is called as for NewACP.
func NewCodex(r io.Reader, w io.Writer, version string, emit func(Event)) *Codex {
	c := &Codex{
		emit:    emit,
		version: version,
		items:   make(map[string]Tool),
		streams: make(map[string]bool),
		ended:   make(map[string]TurnResult),
		waiters: make(map[string]chan TurnResult),
	}
	c.conn = NewConn(r, w, ConnOptions{OmitVersion: true, OnNotify: c.onNotify, OnRequest: c.onRequest})
	return c
}

// Done is closed when the connection ends.
func (c *Codex) Done() <-chan struct{} { return c.conn.Done() }

// Start sends initialize, initialized and thread/start.
func (c *Codex) Start(ctx context.Context, cwd string) (Info, error) {
	var init struct {
		UserAgent string `json:"userAgent"`
	}
	err := c.conn.Call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "tuios", "title": "tuios", "version": c.version},
		"capabilities": map[string]any{"experimentalApi": false, "requestAttestation": false},
	}, &init)
	if err != nil {
		return Info{}, fmt.Errorf("initialize: %w", err)
	}
	if err := c.conn.Notify("initialized", nil); err != nil {
		return Info{}, fmt.Errorf("initialized: %w", err)
	}
	info := Info{Agent: init.UserAgent}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	params := map[string]any{}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if err := c.conn.Call(ctx, "thread/start", params, &started); err != nil {
		return info, fmt.Errorf("thread/start: %w", err)
	}
	if started.Thread.ID == "" {
		return info, fmt.Errorf("thread/start: codex returned no thread id")
	}
	c.mu.Lock()
	c.thread = started.Thread.ID
	c.mu.Unlock()
	info.Session, info.Model = started.Thread.ID, started.Model
	return info, nil
}

// Prompt sends turn/start and waits for turn/completed.
func (c *Codex) Prompt(ctx context.Context, text string) (TurnResult, error) {
	c.mu.Lock()
	thread := c.thread
	c.mu.Unlock()
	var res struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	err := c.conn.Call(ctx, "turn/start", map[string]any{
		"threadId": thread,
		"input":    []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}},
	}, &res)
	if err != nil {
		return TurnResult{Stop: StopFailed, Detail: err.Error()}, err
	}
	id := res.Turn.ID
	c.mu.Lock()
	// The turn can end before this goroutine reads the answer to turn/start:
	// the notification is then already waiting in ended.
	if r, ok := c.ended[id]; ok {
		delete(c.ended, id)
		c.mu.Unlock()
		return r, nil
	}
	ch := make(chan TurnResult, 1)
	c.waiters[id] = ch
	c.turn = id
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		if c.turn == id {
			c.turn = ""
		}
		c.mu.Unlock()
	}()
	select {
	case r := <-ch:
		return r, nil
	case <-c.conn.Done():
		return TurnResult{Stop: StopFailed, Detail: "codex exited during the turn"}, ErrClosed
	case <-ctx.Done():
		return TurnResult{Stop: StopFailed, Detail: ctx.Err().Error()}, ctx.Err()
	}
}

// Cancel sends turn/interrupt for the running turn.
func (c *Codex) Cancel() {
	c.mu.Lock()
	thread, turn := c.thread, c.turn
	c.mu.Unlock()
	if turn == "" {
		return
	}
	go func() {
		_ = c.conn.Call(context.Background(), "turn/interrupt", map[string]string{"threadId": thread, "turnId": turn}, nil)
	}()
}

// codexItem is a ThreadItem, the fields the client shows.
type codexItem struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	Cwd              string `json:"cwd"`
	Status           string `json:"status"`
	ExitCode         *int   `json:"exitCode"`
	AggregatedOutput string `json:"aggregatedOutput"`
	Server           string `json:"server"`
	Tool             string `json:"tool"`
	Changes          []struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	} `json:"changes"`
}

func (c *Codex) onNotify(method string, params json.RawMessage) {
	switch method {
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) != nil || p.Delta == "" {
			return
		}
		c.mu.Lock()
		c.streams[p.ItemID] = true
		c.mu.Unlock()
		c.emit(Text{Text: p.Delta, Thought: method != "item/agentMessage/delta"})
	case "item/started", "item/completed":
		var p struct {
			Item codexItem `json:"item"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		c.onItem(p.Item, method == "item/completed")
	case "turn/completed":
		var p struct {
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		r := TurnResult{Stop: StopFinished}
		switch p.Turn.Status {
		case "interrupted":
			r.Stop = StopCancelled
		case "failed":
			r.Stop = StopFailed
			if p.Turn.Error != nil {
				r.Detail = p.Turn.Error.Message
			}
		}
		c.mu.Lock()
		if ch, ok := c.waiters[p.Turn.ID]; ok {
			ch <- r
		} else {
			c.ended[p.Turn.ID] = r
		}
		c.mu.Unlock()
	case "thread/tokenUsage/updated":
		// The context is the last request's tokens of the model's window.
		// The window is null when Codex does not know it, and then there is
		// no context to state.
		var p struct {
			TokenUsage struct {
				Last struct {
					TotalTokens *int64 `json:"totalTokens"`
				} `json:"last"`
				ModelContextWindow *int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		u := p.TokenUsage
		if u.Last.TotalTokens == nil || u.ModelContextWindow == nil || *u.ModelContextWindow <= 0 {
			return
		}
		c.emit(Usage{ContextUsed: *u.Last.TotalTokens, ContextSize: *u.ModelContextWindow})
	case "turn/plan/updated":
		var p struct {
			Plan []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		plan := Plan{Entries: []PlanEntry{}}
		for _, e := range p.Plan {
			status := e.Status
			if status == "inProgress" {
				status = "in_progress"
			}
			plan.Entries = append(plan.Entries, PlanEntry{Content: e.Step, Status: status})
		}
		c.emit(plan)
	case "model/rerouted":
		var p struct {
			ToModel string `json:"toModel"`
		}
		if json.Unmarshal(params, &p) == nil && p.ToModel != "" {
			c.emit(Usage{Model: p.ToModel})
		}
	case "error":
		var p struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			WillRetry bool `json:"willRetry"`
		}
		if json.Unmarshal(params, &p) != nil || p.Error.Message == "" {
			return
		}
		msg := p.Error.Message
		if p.WillRetry {
			msg += " (retrying)"
		}
		c.emit(Notice{Text: msg, Error: !p.WillRetry})
	}
}

// onItem shows a thread item as it starts and as it completes.
func (c *Codex) onItem(it codexItem, completed bool) {
	switch it.Type {
	case "agentMessage", "reasoning":
		// A message that streamed was shown as it came. One that did not is
		// shown whole when it completes.
		c.mu.Lock()
		streamed := c.streams[it.ID]
		delete(c.streams, it.ID)
		c.mu.Unlock()
		if completed && !streamed && it.Type == "agentMessage" && it.Text != "" {
			c.emit(Text{Text: it.Text})
		}
	case "commandExecution":
		t := Tool{ID: it.ID, Kind: "execute", Title: it.Command, Status: codexStatus(it.Status, completed), Input: map[string]string{"command": it.Command}}
		if completed && it.ExitCode != nil {
			t.Text = "exit code " + strconv.Itoa(*it.ExitCode)
		}
		c.storeItem(t)
		c.emit(t)
	case "fileChange":
		t := Tool{ID: it.ID, Kind: "edit", Status: codexStatus(it.Status, completed)}
		paths := make([]string, 0, len(it.Changes))
		for _, ch := range it.Changes {
			t.Diffs = append(t.Diffs, Diff{Path: ch.Path, Unified: ch.Diff})
			paths = append(paths, ch.Path)
		}
		t.Title = "edit " + strings.Join(paths, ", ")
		c.storeItem(t)
		c.emit(t)
	case "mcpToolCall":
		t := Tool{ID: it.ID, Kind: "other", Title: it.Server + "." + it.Tool, Status: codexStatus(it.Status, completed)}
		c.storeItem(t)
		c.emit(t)
	}
}

func (c *Codex) storeItem(t Tool) {
	c.mu.Lock()
	c.items[t.ID] = t
	c.mu.Unlock()
}

// codexStatus maps an item status to the Tool status.
func codexStatus(s string, completed bool) string {
	switch s {
	case "completed":
		return ToolDone
	case "failed", "declined":
		return ToolFailed
	case "inProgress":
		return ToolRunning
	}
	if completed {
		return ToolDone
	}
	return ToolRunning
}

func (c *Codex) onRequest(r *Request) {
	switch r.Method {
	case "item/commandExecution/requestApproval":
		var p struct {
			ItemID                 string          `json:"itemId"`
			Kind                   string          `json:"kind"`
			Command                *string         `json:"command"`
			Cwd                    *string         `json:"cwd"`
			Reason                 *string         `json:"reason"`
			NetworkApprovalContext json.RawMessage `json:"networkApprovalContext"`
		}
		if json.Unmarshal(r.Params, &p) != nil {
			r.ReplyError(codeInvalidParams, "could not read the approval request")
			return
		}
		c.mu.Lock()
		t, ok := c.items[p.ItemID]
		c.mu.Unlock()
		if !ok {
			t = Tool{ID: p.ItemID, Kind: "execute"}
		}
		if p.Command != nil {
			t.Title, t.Input = *p.Command, map[string]string{"command": *p.Command}
		}
		t.Status = ToolPending
		perm := codexPermission(r, t, []codexOption{
			{"allow once", DecisionOnce, "accept"},
			{"allow for this session", "", "acceptForSession"},
			{"deny", DecisionDeny, "decline"},
		})
		if p.Reason != nil {
			perm.Reason = *p.Reason
		}
		if p.Cwd != nil && *p.Cwd != "" {
			perm.Detail = "in " + *p.Cwd
		}
		// Input to a running command, or a command that needs the network,
		// is more than the command line says: the pane asks it.
		if (p.Kind != "" && p.Kind != "command") || (len(p.NetworkApprovalContext) > 0 && string(p.NetworkApprovalContext) != "null") || t.Title == "" {
			perm.Tool.OtherInput = true
			if p.Kind == "writeStdin" {
				perm.Detail = strings.TrimSpace("input to a running command " + perm.Detail)
			}
		}
		c.emit(perm)
	case "item/fileChange/requestApproval":
		var p struct {
			ItemID    string  `json:"itemId"`
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		if json.Unmarshal(r.Params, &p) != nil {
			r.ReplyError(codeInvalidParams, "could not read the approval request")
			return
		}
		c.mu.Lock()
		t, ok := c.items[p.ItemID]
		c.mu.Unlock()
		if !ok {
			t = Tool{ID: p.ItemID, Kind: "edit", Title: "edit files"}
		}
		t.Status = ToolPending
		// An edit whose files and content are not known, or one that also
		// asks for a write root for the rest of the session, is more than one
		// line can show: the pane asks it and the Inbox does not.
		if len(t.Diffs) == 0 {
			t.OtherInput = true
		}
		grantRoot := ""
		if p.GrantRoot != nil {
			grantRoot = strings.TrimSpace(*p.GrantRoot)
		}
		if grantRoot != "" {
			t.OtherInput = true
		}
		perm := codexPermission(r, t, []codexOption{
			{"allow once", DecisionOnce, "accept"},
			{"allow for this session", "", "acceptForSession"},
			{"deny", DecisionDeny, "decline"},
		})
		if p.Reason != nil {
			perm.Reason = *p.Reason
		}
		if grantRoot != "" {
			perm.Detail = "asks to allow writes under " + grantRoot + " for the session"
		}
		c.emit(perm)
	default:
		r.ReplyError(codeMethodNotFound, "tuios does not offer "+r.Method)
	}
}

// codexOption is one answer to a Codex approval: its label, its Inbox
// decision, and the decision Codex is sent.
type codexOption struct {
	label, decision, wire string
}

func codexPermission(r *Request, t Tool, opts []codexOption) *Permission {
	options := make([]Option, len(opts))
	for i, o := range opts {
		options[i] = Option{Label: o.label, Decision: o.decision}
	}
	return NewPermission(t, options, func(i int) {
		r.Reply(map[string]string{"decision": opts[i].wire})
	}, func() {
		r.Reply(map[string]string{"decision": "cancel"})
	})
}
