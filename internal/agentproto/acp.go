package agentproto

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/acp"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The ACP agent: internal/acp speaks the protocol, versions 1 and 2, and this
// file turns what it delivers into the pane's events.
//
// Version 2 is a draft of the spec, which asks clients not to ask for it by
// default until it is final, so a pane asks for DefaultACPVersion unless it
// is told otherwise (agent-proto --acp-version, or agents.acp_version in the
// config). Asking for 2 is safe with an agent that has only version 1: it
// answers 1, or refuses, and the pane speaks 1 (see acp.Connect).
//
// Source for version 1: the protocol overview and schema at
// agentclientprotocol.com, checked against two clients that ship it (emdash's
// acp-agent-connection.ts and t3code's AcpSessionRuntime.ts) and one agent
// (opencode's src/acp). For version 2: the v2 pages, the migration guide and
// schema/v2/schema.json on agent-client-protocol main, read on 2026-11-13.
//
// The client advertises no file system, no terminal and no elicitation, so a
// conforming agent never sends fs/*, terminal/* or elicitation/create; one
// that does gets method not found. auth/login is never called: an agent that
// needs a login refuses session/new, and the pane says to log in with the
// agent's own command.

// DefaultACPVersion is the ACP version a pane asks for when it is not told:
// 1 while version 2 is a draft of the spec. When version 2 is final this
// becomes acp.LatestVersion.
const DefaultACPVersion = acp.DefaultVersion

// ACPOptions configure an ACP agent.
type ACPOptions struct {
	// Version is tuios's version, given to the agent as the client's.
	Version string
	// MaxVersion is the ACP version to ask for, DefaultACPVersion when 0.
	MaxVersion int
	// Resume is a session id to reattach to with session/resume instead of
	// opening a new session.
	Resume string
}

// ACP is an ACP agent.
type ACP struct {
	t    acp.Transport
	opts ACPOptions
	emit func(Event)

	mu      sync.Mutex
	client  *acp.Client
	session *acp.Session
	tools   map[string]Tool
	// msgs is the text shown of each message, by role and id.
	msgs map[string]string
	// unknown is the update kinds already said to be unknown.
	unknown map[string]bool
	// commands and mode are what was last shown of them.
	commands string
	mode     string
	done     chan struct{}
}

// NewACP speaks ACP to the agent t opens. emit receives what the agent shows;
// it is called on the connection's goroutine, one event at a time.
func NewACP(t acp.Transport, o ACPOptions, emit func(Event)) *ACP {
	return &ACP{t: t, opts: o, emit: emit, tools: map[string]Tool{}, msgs: map[string]string{},
		unknown: map[string]bool{}, done: make(chan struct{})}
}

// Done is closed when the agent's connection ends, once Start has made one.
func (a *ACP) Done() <-chan struct{} { return a.done }

// Protocol is the ACP version the connection speaks, 0 before Start.
func (a *ACP) Protocol() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return 0
	}
	return a.client.Version()
}

// Start connects, negotiates the version and opens the session.
func (a *ACP) Start(ctx context.Context, cwd string) (Info, error) {
	max := a.opts.MaxVersion
	if max == 0 {
		max = DefaultACPVersion
	}
	c, err := acp.Connect(ctx, a.t, acp.ClientOptions{
		Info:         acp.Implementation{Name: "tuios", Title: "tuios", Version: a.opts.Version},
		MaxVersion:   max,
		OnUpdate:     a.onUpdate,
		OnPermission: a.onPermission,
	})
	if err != nil {
		if verr, ok := errors.AsType[*acp.VersionError](err); ok && max < acp.LatestVersion && slices.Contains(verr.Agent, acp.LatestVersion) {
			return Info{}, errfmt.Errorf("initialize: %w. ACP version %d is a draft of the spec: to speak it, set agents.acp_version = %d, or pass --acp-version %d to agent-proto", err, acp.LatestVersion, acp.LatestVersion, acp.LatestVersion)
		}
		return Info{}, errfmt.Errorf("initialize: %w", err)
	}
	a.mu.Lock()
	a.client = c
	a.mu.Unlock()
	go func() {
		<-c.Done()
		close(a.done)
	}()
	ai := c.Agent()
	info := Info{Agent: strings.TrimSpace(firstOf(ai.Title, ai.Name) + " " + ai.Version), Protocol: c.Version()}
	var s *acp.Session
	if a.opts.Resume != "" {
		s, err = c.ResumeSession(ctx, a.opts.Resume, cwd)
		if err != nil {
			return info, errfmt.Errorf("session/resume: %w", err)
		}
	} else {
		s, err = c.NewSession(ctx, cwd)
	}
	if err != nil {
		if len(ai.AuthMethods) > 0 {
			names := make([]string, 0, len(ai.AuthMethods))
			for _, m := range ai.AuthMethods {
				names = append(names, firstOf(m.Name, m.ID))
			}
			return info, errfmt.Errorf("session/new: %w. The agent may need a login (%s): log in by running it in a shell, then start it here again", err, strings.Join(names, ", "))
		}
		return info, errfmt.Errorf("session/new: %w", err)
	}
	a.mu.Lock()
	a.session = s
	a.mu.Unlock()
	info.Session = s.ID
	info.Model = firstOf(acpModel(s.Setup.Models), configModel(s.Setup.Options))
	if line := a.commandsLine(s.Setup.Commands); line != "" {
		a.emit(Line{Text: line})
	}
	return info, nil
}

// acpModel is the current model a version 1 session/new answer names: its
// name when the agent lists one, else its id, else "".
func acpModel(raw json.RawMessage) string {
	var m struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
	}
	if len(raw) == 0 || jsonx.Unmarshal(raw, &m) != nil || m.CurrentModelID == "" {
		return ""
	}
	for _, am := range m.AvailableModels {
		if am.ModelID == m.CurrentModelID && strings.TrimSpace(am.Name) != "" {
			return am.Name
		}
	}
	return m.CurrentModelID
}

// configModel is the model the config options name: the current value of
// the model category, by name when listed.
func configModel(opts []acp.ConfigOption) string {
	for _, o := range opts {
		if o.Category == "model" {
			return firstOf(o.ValueName, o.Value)
		}
	}
	return ""
}

// Prompt runs one turn.
func (a *ACP) Prompt(ctx context.Context, text string) (TurnResult, error) {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s == nil {
		return TurnResult{Stop: StopFailed, Detail: "no session"}, acp.ErrClosed
	}
	end, err := s.Prompt(ctx, acp.Text(text))
	if err != nil {
		return TurnResult{Stop: StopFailed, Detail: err.Error()}, err
	}
	switch end.Reason {
	case acp.StopEndTurn:
		return TurnResult{Stop: StopFinished}, nil
	case acp.StopCancelled:
		return TurnResult{Stop: StopCancelled}, nil
	case acp.StopRefusal:
		return TurnResult{Stop: StopRefused, Detail: "the agent refused"}, nil
	case acp.StopMaxTokens, acp.StopMaxTurnRequests:
		return TurnResult{Stop: StopLimit, Detail: "stopped at " + end.Reason}, nil
	case acp.StopError:
		detail := "the agent stopped with an error"
		if end.Error != nil {
			detail = end.Error.Message
		}
		return TurnResult{Stop: StopFailed, Detail: detail}, nil
	}
	return TurnResult{Stop: StopFinished, Detail: end.Reason}, nil
}

// Cancel sends session/cancel.
func (a *ACP) Cancel() {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s != nil {
		_ = s.Cancel()
	}
}

func (a *ACP) onUpdate(u acp.Update) {
	switch u := u.(type) {
	case *acp.MessageUpdate:
		a.onMessage(u)
	case *acp.StateUpdate:
		a.emit(AgentState{State: u.State})
	case *acp.ToolCallUpdate:
		a.emit(a.mergeTool(u))
	case *acp.ToolContentChunk:
		a.emit(a.appendTool(u))
	case *acp.TerminalUpdate:
		// The terminal's bytes are not shown: the call that names it says
		// it has one, as a version 1 terminal does.
	case *acp.PlanUpdate:
		if u.Type != "items" {
			a.unknownOnce("plan type " + u.Type)
			return
		}
		plan := Plan{}
		for _, e := range u.Entries {
			plan.Entries = append(plan.Entries, PlanEntry{Content: e.Content, Status: e.Status})
		}
		a.emit(plan)
	case *acp.UsageUpdate:
		usage := Usage{}
		if u.SizeKnown {
			usage.ContextUsed, usage.ContextSize = u.Used, u.Size
		}
		switch u.Cost.Presence {
		case acp.Set:
			usage.Cost, usage.HasCost, usage.Currency = u.Cost.Value.Amount, true, u.Cost.Value.Currency
		case acp.Null:
			usage.ClearCost = true
		}
		if usage.ContextSize > 0 || usage.HasCost || usage.ClearCost {
			a.emit(usage)
		}
	case *acp.Notice:
		text := u.Title
		if u.Description != "" {
			text += ": " + u.Description
		}
		a.emit(Notice{Text: text, Error: u.Severity == "error"})
	case *acp.CommandsUpdate:
		if line := a.commandsLine(u.Commands); line != "" {
			a.emit(Line{Text: line})
		}
	case *acp.ConfigUpdate:
		if m := configModel(u.Options); m != "" {
			a.emit(Usage{Model: m})
		}
		for _, o := range u.Options {
			if o.Category != "mode" {
				continue
			}
			mode := firstOf(o.ValueName, o.Value)
			a.mu.Lock()
			changed := mode != a.mode
			a.mode = mode
			a.mu.Unlock()
			if changed && mode != "" {
				a.emit(Line{Text: "mode: " + mode})
			}
		}
	case *acp.CompactionUpdate:
		switch u.Status {
		case "":
			// A chunk of the summary, which is for the model.
		case "in_progress":
			a.emit(Line{Text: "compacting the context"})
		case "completed":
			a.emit(Line{Text: "context compacted"})
		case "failed":
			a.emit(Notice{Text: "compacting the context failed: " + firstOf(u.Error, "no reason given"), Error: true})
		default:
			a.emit(Line{Text: "compacting the context: " + u.Status})
		}
	case *acp.SessionInfoUpdate:
		// The pane's title is the name start-agent gave it, on purpose.
	case *acp.UnknownUpdate:
		a.unknownOnce(u.UpdateKind())
	}
}

// unknownOnce says once per kind that the agent sent something the pane
// does not show.
func (a *ACP) unknownOnce(kind string) {
	a.mu.Lock()
	seen := a.unknown[kind]
	a.unknown[kind] = true
	a.mu.Unlock()
	if !seen {
		a.emit(Line{Text: "the agent sent an update tuios does not show: " + kind})
	}
}

// maxCommandsShown bounds the names one commands line lists.
const maxCommandsShown = 20

// commandsLine is the line naming the agent's slash commands, "" when it is
// the same as the last one shown.
func (a *ACP) commandsLine(cmds []acp.Command) string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if n := oneLine(c.Name); n != "" {
			names = append(names, "/"+strings.TrimPrefix(n, "/"))
		}
	}
	slices.Sort(names)
	line := ""
	if len(names) > 0 {
		shown := names
		if len(shown) > maxCommandsShown {
			shown = shown[:maxCommandsShown]
		}
		line = "commands: " + strings.Join(shown, " ")
		if more := len(names) - len(shown); more > 0 {
			line += " (and " + strconv.Itoa(more) + " more)"
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if line == a.commands {
		return ""
	}
	a.commands = line
	if line == "" {
		return "the agent offers no commands now"
	}
	return line
}

// onMessage shows a message update. The transcript is written once and not
// rewritten, so a whole message that only adds to what was shown appends the
// rest, and one that changes it is shown again, said to replace the earlier.
func (a *ACP) onMessage(u *acp.MessageUpdate) {
	if u.Role == acp.RoleUser {
		// The pane shows the prompt as it was typed.
		return
	}
	thought := u.Role == acp.RoleThought
	key := string(u.Role) + "\x00" + u.MessageID
	a.mu.Lock()
	shown := a.msgs[key]
	var out []Event
	switch u.Op {
	case acp.MessageAppend:
		s := u.Text()
		if u.MessageID != "" {
			a.msgs[key] = shown + s
		}
		if s != "" {
			out = append(out, Text{Text: s, Thought: thought})
		}
	case acp.MessageReplace:
		s := u.Text()
		a.msgs[key] = s
		switch {
		case strings.HasPrefix(s, shown):
			if rest := s[len(shown):]; rest != "" {
				out = append(out, Text{Text: rest, Thought: thought})
			}
		default:
			out = append(out, Line{Text: "the agent replaced its message:"}, Text{Text: s, Thought: thought})
		}
	case acp.MessageClear:
		if shown != "" {
			out = append(out, Line{Text: "the agent removed the message above"})
		}
		delete(a.msgs, key)
	}
	a.mu.Unlock()
	for _, ev := range out {
		a.emit(ev)
	}
}

// mergeTool folds a tool call update into what is known about the call. A
// version 1 tool_call starts it afresh; in version 2 the first update for an
// id creates it. A field set to null is cleared.
func (a *ACP) mergeTool(u *acp.ToolCallUpdate) Tool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tools[u.ToolCallID]
	if u.Created || !ok {
		t = Tool{ID: u.ToolCallID, Status: ToolPending}
	}
	t = applyTool(t, u)
	a.tools[u.ToolCallID] = t
	return t
}

func applyTool(t Tool, u *acp.ToolCallUpdate) Tool {
	switch u.Title.Presence {
	case acp.Set:
		t.Title = u.Title.Value
	case acp.Null:
		t.Title = ""
	}
	switch u.Kind.Presence {
	case acp.Set:
		t.Kind = u.Kind.Value
	case acp.Null:
		t.Kind = ""
	}
	switch u.Status.Presence {
	case acp.Set:
		t.Status = acpToolStatus(u.Status.Value)
	case acp.Null:
		t.Status = ToolPending
	}
	switch u.Content.Presence {
	case acp.Set:
		t.Diffs, t.Text, t.Terminal = nil, "", false
		for _, item := range u.Content.Value {
			t = addToolItem(t, item)
		}
	case acp.Null:
		t.Diffs, t.Text, t.Terminal = nil, "", false
	}
	switch raw := strings.TrimSpace(string(u.RawInput)); {
	case raw == "null":
		t.Input, t.OtherInput = nil, false
	case raw != "":
		t.Input, t.OtherInput = flatInput(u.RawInput)
	}
	return t
}

// appendTool adds a streamed content item to a call.
func (a *ACP) appendTool(u *acp.ToolContentChunk) Tool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tools[u.ToolCallID]
	if !ok {
		t = Tool{ID: u.ToolCallID, Status: ToolPending}
	}
	t = addToolItem(t, u.Item)
	a.tools[u.ToolCallID] = t
	return t
}

// addToolItem adds one content item to a call.
func addToolItem(t Tool, item acp.ToolContent) Tool {
	switch item.Type {
	case "diff":
		if len(item.Changes) == 0 && item.Patch == nil {
			t.Diffs = append(t.Diffs, Diff{Path: item.Path, Old: item.OldText, New: item.NewText})
			break
		}
		t.Diffs = append(t.Diffs, v2Diffs(item)...)
	case "terminal":
		t.Terminal = true
	case "content":
		if item.Content != nil {
			if s := item.Content.String(); s != "" {
				if t.Text != "" {
					t.Text += "\n"
				}
				t.Text += s
			}
		}
	}
	return t
}

// v2Diffs is a version 2 diff as the pane's diffs: the patch text when the
// agent sent one, and each change it has no text for named by its operation.
func v2Diffs(item acp.ToolContent) []Diff {
	paths := make([]string, 0, len(item.Changes))
	for _, c := range item.Changes {
		p := c.Path
		if c.OldPath != "" {
			p = c.OldPath + " -> " + c.Path
		}
		paths = append(paths, p)
	}
	if item.Patch != nil && item.Patch.Format == "git_patch" && strings.TrimSpace(item.Patch.Text) != "" {
		return []Diff{{Path: strings.Join(paths, ", "), Unified: gitPatchBody(item.Patch.Text)}}
	}
	out := make([]Diff, 0, len(item.Changes))
	for i, c := range item.Changes {
		note := "(" + firstOf(c.Operation, "change") + ", no text diff"
		if c.FileType != "" && c.FileType != "text" {
			note += ": " + c.FileType
		}
		out = append(out, Diff{Path: paths[i], Unified: note + ")"})
	}
	return out
}

// gitPatchBody is a git patch without the lines that are not a unified
// diff's: the diff --git header, index and mode lines. What is left starts
// at the first --- line, which the diff renderer shows as it is.
func gitPatchBody(text string) string {
	var keep []string
	for l := range strings.SplitSeq(text, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "), strings.HasPrefix(l, "index "),
			strings.HasPrefix(l, "new file mode "), strings.HasPrefix(l, "deleted file mode "),
			strings.HasPrefix(l, "old mode "), strings.HasPrefix(l, "new mode "),
			strings.HasPrefix(l, "similarity index "):
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// acpToolStatus maps ACP's tool call status to the Tool status.
func acpToolStatus(s string) string {
	switch s {
	case "in_progress":
		return ToolRunning
	case "completed":
		return ToolDone
	case "failed", "cancelled":
		return ToolFailed
	}
	return ToolPending
}

// flatInput reads a raw input object as its string fields. other is true when
// it holds anything else, or is not an object.
func flatInput(raw json.RawMessage) (map[string]string, bool) {
	var obj map[string]json.RawMessage
	if jsonx.Unmarshal(raw, &obj) != nil {
		return nil, true
	}
	out := make(map[string]string, len(obj))
	other := false
	for k, v := range obj {
		var s string
		if jsonx.Unmarshal(v, &s) == nil {
			out[k] = s
			continue
		}
		if string(v) != "null" {
			other = true
		}
	}
	return out, other
}

func (a *ACP) onPermission(p *acp.PermissionRequest) {
	var tool Tool
	switch {
	case p.ToolCall != nil:
		a.mu.Lock()
		t, ok := a.tools[p.ToolCall.ToolCallID]
		if !ok {
			t = Tool{ID: p.ToolCall.ToolCallID, Status: ToolPending}
		}
		tool = applyTool(t, p.ToolCall)
		a.tools[p.ToolCall.ToolCallID] = tool
		a.mu.Unlock()
	case p.Command != nil:
		// A command subject is the whole request: the command, and where
		// it runs.
		tool = Tool{ID: firstOf(p.Command.ToolCallID, "command"), Kind: "execute", Title: p.Command.Command,
			Status: ToolPending, Input: map[string]string{"command": p.Command.Command}}
	default:
		// No subject, or one tuios does not know: the title is all there
		// is, and the Inbox does not offer to answer it.
		tool = Tool{ID: "permission", Title: p.Title, Status: ToolPending, OtherInput: true}
	}
	options := make([]Option, len(p.Options))
	ids := make([]string, len(p.Options))
	for i, o := range p.Options {
		ids[i] = o.ID
		options[i] = Option{Label: firstOf(o.Name, o.ID), Decision: acpDecision(o.Kind)}
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
	perm := NewPermission(tool, options, func(i int) { p.Choose(ids[i]) }, p.Cancel)
	if p.Title != "" && p.Title != tool.Title {
		perm.Title = p.Title
	}
	perm.Reason = p.Description
	if p.Command != nil && p.Command.Cwd != "" {
		perm.Detail = "in " + p.Command.Cwd
	}
	a.emit(perm)
	go func() {
		select {
		case <-p.Withdrawn():
			// The agent withdrew the question: it is answered cancelled,
			// which the spec still wants, and the pane drops it.
			perm.Cancel()
			a.emit(Withdrawn{Permission: perm})
		case <-perm.answered:
		}
	}()
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
