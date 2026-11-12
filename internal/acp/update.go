package acp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"slices"
)

// What the agent sends in session/update, one Go type per kind.
//
// The types follow version 2, which can say more than version 1: a version 1
// update arrives as the same type with the fields version 1 cannot state left
// at their zero value. That direction loses nothing. The other one would, so
// the package never turns a version 2 update into a version 1 one.

// Update is one session/update notification. It is one of *MessageUpdate,
// *StateUpdate, *ToolCallUpdate, *ToolContentChunk, *TerminalUpdate,
// *PlanUpdate, *UsageUpdate, *Notice, *CommandsUpdate, *ConfigUpdate,
// *SessionInfoUpdate, *CompactionUpdate or *UnknownUpdate.
type Update interface {
	// Session is the id of the session the update is for.
	Session() string
	// UpdateKind is the update's sessionUpdate value.
	UpdateKind() string
}

// head is what every update carries.
type head struct {
	SessionID string `json:"-"`
	KindName  string `json:"-"`
}

func (h head) Session() string    { return h.SessionID }
func (h head) UpdateKind() string { return h.KindName }

// Presence is whether a field of a patch was sent, and how.
type Presence uint8

// The three states of a patch field: version 2 keeps what is omitted, clears
// what is null and replaces what has a value.
const (
	Omitted Presence = iota
	Null
	Set
)

// Field is a patch field: Value holds the value when Presence is Set.
type Field[T any] struct {
	Presence Presence
	Value    T
}

// UnmarshalJSON is called only for a field that is present.
func (f *Field[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*f = Field[T]{Presence: Null}
		return nil
	}
	var v T
	if err := unmarshal(data, &v); err != nil {
		return err
	}
	*f = Field[T]{Presence: Set, Value: v}
	return nil
}

// IsZero is true for an omitted field, so a struct tag omitzero leaves it
// out when the field is written.
func (f Field[T]) IsZero() bool { return f.Presence == Omitted }

// MarshalJSON writes the value, or null.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if f.Presence != Set {
		return []byte("null"), nil
	}
	return marshal(f.Value)
}

// Role is who a message is from.
type Role string

// Message roles.
const (
	RoleUser    Role = "user"
	RoleAgent   Role = "agent"
	RoleThought Role = "thought"
)

// MessageOp is what a message update does to the message it names.
type MessageOp string

// Message operations. A chunk appends; a whole message replaces the content,
// clears it (content null or empty) or, with content omitted, leaves it.
const (
	MessageAppend  MessageOp = "append"
	MessageReplace MessageOp = "replace"
	MessageClear   MessageOp = "clear"
	MessageKeep    MessageOp = "keep"
)

// MessageUpdate is a user message, an agent message or a thought: a chunk of
// one (user_message_chunk, agent_message_chunk, agent_thought_chunk) or the
// whole of one (user_message, agent_message, agent_thought, version 2 only).
// Version 2 keys messages by role and MessageID; version 1 may leave the id
// empty.
type MessageUpdate struct {
	head
	Role      Role
	MessageID string
	Op        MessageOp
	// Content is the block a chunk appends, or the blocks a whole message
	// replaces the content with.
	Content []ContentBlock
}

// Text is the text the update's content holds, with each block that is not
// text named by its type.
func (m *MessageUpdate) Text() string {
	var b bytes.Buffer
	for _, c := range m.Content {
		b.WriteString(c.String())
	}
	return b.String()
}

// Foreground states a version 2 agent reports.
const (
	StateRunning        = "running"
	StateIdle           = "idle"
	StateRequiresAction = "requires_action"
)

// Stop reasons.
const (
	StopEndTurn         = "end_turn"
	StopMaxTokens       = "max_tokens"
	StopMaxTurnRequests = "max_turn_requests"
	StopRefusal         = "refusal"
	StopCancelled       = "cancelled"
	StopError           = "error"
)

// StateUpdate is version 2's state_update: whether the agent's foreground work
// is running, idle, or waiting on the person. An idle update names why the
// work stopped, when the agent knows.
type StateUpdate struct {
	head
	State      string
	StopReason string
	// Error is the failure an idle update with stop reason error carries.
	Error *RPCError
}

// ToolCallUpdate creates a tool call or patches it: tool_call (version 1),
// or tool_call_update, whose first update for an id creates the call in
// version 2. Created is set for a version 1 tool_call, which starts the call
// afresh.
type ToolCallUpdate struct {
	head
	ToolCallID string
	Created    bool
	Name       Field[string] `json:",omitzero"`
	Title      Field[string] `json:",omitzero"`
	Kind       Field[string] `json:",omitzero"`
	Status     Field[string] `json:",omitzero"`
	// Content is replaced whole when it is set.
	Content Field[[]ToolContent] `json:",omitzero"`
	// RawInput is the call's input as the agent sent it; empty when omitted,
	// "null" when cleared.
	RawInput json.RawMessage `json:",omitempty"`
}

// ToolContent is one item of a tool call's content.
type ToolContent struct {
	// Type is content, diff or terminal, or a type this package does not
	// know, kept as it came.
	Type string
	// Content is the block of a content item.
	Content *ContentBlock `json:",omitempty"`
	// Path, OldText and NewText are a version 1 diff.
	Path    string  `json:",omitempty"`
	OldText *string `json:",omitempty"`
	NewText string  `json:",omitempty"`
	// Changes and Patch are a version 2 diff.
	Changes []DiffChange `json:",omitempty"`
	Patch   *DiffPatch   `json:",omitempty"`
	// TerminalID names the terminal of a terminal item.
	TerminalID string `json:",omitempty"`
}

// DiffChange is one file a version 2 diff changes.
type DiffChange struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	FileType  string `json:"fileType,omitempty"`
}

// DiffPatch is a version 2 diff's renderable text: a git patch with absolute
// paths, for format git_patch.
type DiffPatch struct {
	Format string `json:"format"`
	Text   string `json:"text"`
}

// ToolContentChunk is version 2's tool_call_content_chunk: one item appended
// to a tool call's content.
type ToolContentChunk struct {
	head
	ToolCallID string
	Item       ToolContent
}

// TerminalUpdate is version 2's terminal_update or terminal_output_chunk: a
// display-only terminal the agent runs. A chunk appends Output; an update
// with Snapshot set replaces all output kept so far.
type TerminalUpdate struct {
	head
	TerminalID string
	Command    string
	Output     []byte
	Snapshot   bool
	Exited     bool
	ExitCode   *int
}

// PlanUpdate is a plan: version 2's plan_update, replacing the plan PlanID
// names, or version 1's plan, the one plan of the session.
type PlanUpdate struct {
	head
	PlanID string
	// Type is items, or a plan type this package does not show.
	Type    string
	Entries []PlanEntry
}

// PlanEntry is one step of a plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

// UsageUpdate is usage_update: tokens in the context window, its size, and
// the session's cost. A version 2 cost is a patch: Null clears it.
type UsageUpdate struct {
	head
	Used, Size int64
	// SizeKnown is set when both used and size were read.
	SizeKnown bool
	Cost      Field[Cost] `json:",omitzero"`
}

// Cost is an amount in a currency.
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Notice is version 2's notice: something the agent tells the person, live,
// with severity info, warning or error.
type Notice struct {
	head
	Severity    string
	Title       string
	Description string
}

// CommandsUpdate is available_commands_update: the whole list of slash
// commands the agent offers now. A command runs by being sent as a prompt.
type CommandsUpdate struct {
	head
	Commands []Command
}

// Command is one slash command.
type Command struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// InputHint says what the command takes, when it takes text.
	InputHint string `json:"-"`
}

// ConfigUpdate is config_option_update: the session's whole set of options.
type ConfigUpdate struct {
	head
	Options []ConfigOption
}

// ConfigOption is one session option, such as the model or the mode.
type ConfigOption struct {
	ID string
	// Name is the option's name; Category is model, mode, model_config,
	// thought_level, or another.
	Name     string
	Category string
	// Type is select or boolean, or another.
	Type string
	// Value is the current value's id, or "true" or "false".
	Value string
	// ValueName is the current value's name, when the agent lists it.
	ValueName string
}

// SessionInfoUpdate is session_info_update: the session's title.
type SessionInfoUpdate struct {
	head
	Title Field[string] `json:",omitzero"`
}

// CompactionUpdate is compaction_update or compaction_summary_chunk: the agent
// compacting its context. Status is in_progress, completed, failed or
// cancelled; a summary chunk leaves it empty and sets Summary.
type CompactionUpdate struct {
	head
	CompactionID string
	Status       string
	Error        string
	Summary      *ContentBlock
}

// UnknownUpdate is an update of a kind this package does not know. Raw is the
// update as it came, kept so a client can show it or pass it on.
type UnknownUpdate struct {
	head
	Raw json.RawMessage
}

// ContentBlock is a block of content: text, image, audio, resource_link,
// resource, or a type this package does not know.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Text is a text block.
func Text(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

// String is the block as text: its text, a link's URI, or its type in
// brackets.
func (c ContentBlock) String() string {
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

// wireTool is a tool call's fields on the wire, in both versions.
type wireTool struct {
	ToolCallID string                   `json:"toolCallId"`
	Name       Field[string]            `json:"name"`
	Title      Field[string]            `json:"title"`
	Kind       Field[string]            `json:"kind"`
	Status     Field[string]            `json:"status"`
	Content    Field[[]wireToolContent] `json:"content"`
	RawInput   json.RawMessage          `json:"rawInput"`
}

// wireToolContent is one tool content item on the wire, in both versions.
type wireToolContent struct {
	Type       string        `json:"type"`
	Content    *ContentBlock `json:"content"`
	Path       string        `json:"path"`
	OldText    *string       `json:"oldText"`
	NewText    string        `json:"newText"`
	Changes    []DiffChange  `json:"changes"`
	Patch      *DiffPatch    `json:"patch"`
	TerminalID string        `json:"terminalId"`
}

func (w wireToolContent) item() ToolContent { return ToolContent(w) }

func (w wireTool) update(h head, created bool) *ToolCallUpdate {
	u := &ToolCallUpdate{head: h, ToolCallID: w.ToolCallID, Created: created, Name: w.Name, Title: w.Title,
		Kind: w.Kind, Status: w.Status, RawInput: w.RawInput}
	u.Content.Presence = w.Content.Presence
	for _, c := range w.Content.Value {
		u.Content.Value = append(u.Content.Value, c.item())
	}
	return u
}

// v1Patch turns a version 1 field's null into omitted: version 1 reads null
// as not sent.
func v1Patch[T any](f Field[T]) Field[T] {
	if f.Presence == Null {
		return Field[T]{}
	}
	return f
}

// wireUpdate is every field a session/update's update object has, in either
// version, decoded once. A field whose shape differs by kind is kept raw and
// read by the kind that owns it. One type for all kinds keeps the binary
// smaller than a struct per kind.
type wireUpdate struct {
	SessionUpdate string                 `json:"sessionUpdate"`
	MessageID     string                 `json:"messageId"`
	Content       Field[json.RawMessage] `json:"content"`
	State         string                 `json:"state"`
	StopReason    json.RawMessage        `json:"stopReason"`
	Error         json.RawMessage        `json:"error"`
	ToolCallID    string                 `json:"toolCallId"`
	Name          Field[string]          `json:"name"`
	Title         Field[string]          `json:"title"`
	Kind          Field[string]          `json:"kind"`
	Status        Field[string]          `json:"status"`
	RawInput      json.RawMessage        `json:"rawInput"`
	TerminalID    string                 `json:"terminalId"`
	Command       string                 `json:"command"`
	Data          string                 `json:"data"`
	Output        *struct {
		Data string `json:"data"`
	} `json:"output"`
	ExitStatus *struct {
		ExitCode *int `json:"exitCode"`
	} `json:"exitStatus"`
	Entries []PlanEntry `json:"entries"`
	Plan    *struct {
		Type    string      `json:"type"`
		PlanID  string      `json:"planId"`
		Entries []PlanEntry `json:"entries"`
	} `json:"plan"`
	Used              json.RawMessage        `json:"used"`
	Size              json.RawMessage        `json:"size"`
	Cost              Field[json.RawMessage] `json:"cost"`
	Severity          string                 `json:"severity"`
	Description       string                 `json:"description"`
	AvailableCommands []struct {
		Type        string `json:"type"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Input       *struct {
			Hint string `json:"hint"`
		} `json:"input"`
	} `json:"availableCommands"`
	ConfigOptions []json.RawMessage `json:"configOptions"`
	CurrentModeID string            `json:"currentModeId"`
	CompactionID  string            `json:"compactionId"`
}

// knownKinds are the update kinds parseUpdate reads in some version.
var knownKinds = []string{
	"user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "user_message", "agent_message", "agent_thought",
	"state_update", "tool_call", "tool_call_update", "tool_call_content_chunk", "terminal_update", "terminal_output_chunk",
	"plan", "plan_update", "usage_update", "notice", "available_commands_update", "config_option_update",
	"current_mode_update", "session_info_update", "compaction_update", "compaction_summary_chunk",
}

// parseUpdate reads one session/update's update object in version v. It
// returns nil for an update it cannot read at all: one that is not an object
// or names no kind, or a known kind whose fields are malformed.
func parseUpdate(v int, sessionID string, raw json.RawMessage) Update {
	var u wireUpdate
	if err := unmarshal(raw, &u); err != nil {
		var kind struct {
			SessionUpdate string `json:"sessionUpdate"`
		}
		if unmarshal(raw, &kind) != nil || kind.SessionUpdate == "" || slices.Contains(knownKinds, kind.SessionUpdate) {
			return nil
		}
		// A kind this package does not know may use the field names in
		// its own way: it is kept as it came.
		u = wireUpdate{SessionUpdate: kind.SessionUpdate}
	}
	if u.SessionUpdate == "" {
		return nil
	}
	h := head{SessionID: sessionID, KindName: u.SessionUpdate}
	switch k := u.SessionUpdate; k {
	case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "compaction_summary_chunk":
		var c ContentBlock
		if u.Content.Presence == Set && unmarshal(u.Content.Value, &c) != nil {
			return nil
		}
		if k == "compaction_summary_chunk" {
			return &CompactionUpdate{head: h, CompactionID: u.CompactionID, Summary: &c}
		}
		return &MessageUpdate{head: h, Role: messageRole(k), MessageID: u.MessageID, Op: MessageAppend, Content: []ContentBlock{c}}
	case "user_message", "agent_message", "agent_thought":
		if v < 2 {
			break
		}
		if u.MessageID == "" {
			return nil
		}
		m := &MessageUpdate{head: h, Role: messageRole(k), MessageID: u.MessageID, Op: MessageKeep}
		var blocks []ContentBlock
		switch {
		case u.Content.Presence == Null:
			m.Op = MessageClear
		case u.Content.Presence == Set:
			if unmarshal(u.Content.Value, &blocks) != nil {
				return nil
			}
			m.Op, m.Content = MessageReplace, blocks
			if len(blocks) == 0 {
				m.Op, m.Content = MessageClear, nil
			}
		}
		return m
	case "state_update":
		if v < 2 {
			break
		}
		if u.State == "" {
			return nil
		}
		s := &StateUpdate{head: h, State: u.State}
		// A stop reason that is missing, null or not a string is no stop
		// reason; the state still holds.
		_ = unmarshal(u.StopReason, &s.StopReason)
		var e RPCError
		if len(u.Error) > 0 && unmarshal(u.Error, &e) == nil && e.Message != "" {
			s.Error = &e
		}
		return s
	case "tool_call", "tool_call_update":
		if k == "tool_call" && v >= 2 {
			break
		}
		if u.ToolCallID == "" {
			return nil
		}
		w := wireTool{ToolCallID: u.ToolCallID, Name: u.Name, Title: u.Title, Kind: u.Kind, Status: u.Status, RawInput: u.RawInput}
		w.Content.Presence = u.Content.Presence
		if u.Content.Presence == Set && unmarshal(u.Content.Value, &w.Content.Value) != nil {
			return nil
		}
		if v < 2 {
			w.Name, w.Title, w.Kind, w.Status = v1Patch(w.Name), v1Patch(w.Title), v1Patch(w.Kind), v1Patch(w.Status)
			w.Content = v1Patch(w.Content)
			if string(bytes.TrimSpace(w.RawInput)) == "null" {
				w.RawInput = nil
			}
		}
		return w.update(h, k == "tool_call")
	case "tool_call_content_chunk":
		if v < 2 {
			break
		}
		var item wireToolContent
		if u.ToolCallID == "" || u.Content.Presence != Set || unmarshal(u.Content.Value, &item) != nil {
			return nil
		}
		return &ToolContentChunk{head: h, ToolCallID: u.ToolCallID, Item: item.item()}
	case "terminal_update", "terminal_output_chunk":
		if v < 2 {
			break
		}
		if u.TerminalID == "" {
			return nil
		}
		t := &TerminalUpdate{head: h, TerminalID: u.TerminalID, Command: u.Command}
		data := u.Data
		if u.Output != nil {
			data, t.Snapshot = u.Output.Data, true
		}
		if data != "" {
			b, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return nil
			}
			t.Output = b
		}
		if u.ExitStatus != nil {
			t.Exited, t.ExitCode = true, u.ExitStatus.ExitCode
		}
		return t
	case "plan":
		if v >= 2 {
			break
		}
		return &PlanUpdate{head: h, Type: "items", Entries: u.Entries}
	case "plan_update":
		if v < 2 {
			break
		}
		if u.Plan == nil || u.Plan.PlanID == "" {
			return nil
		}
		return &PlanUpdate{head: h, PlanID: u.Plan.PlanID, Type: u.Plan.Type, Entries: u.Plan.Entries}
	case "usage_update":
		// used and size are tokens, and cost an {amount, currency}. Each is
		// read on its own, so a field that is missing or changes shape
		// costs only that field.
		usage := &UsageUpdate{head: h}
		if unmarshal(u.Used, &usage.Used) == nil && unmarshal(u.Size, &usage.Size) == nil && usage.Size > 0 && usage.Used >= 0 {
			usage.SizeKnown = true
		} else {
			usage.Used, usage.Size = 0, 0
		}
		switch u.Cost.Presence {
		case Null:
			if v >= 2 {
				usage.Cost.Presence = Null
			}
		case Set:
			var c struct {
				Amount   *float64 `json:"amount"`
				Currency string   `json:"currency"`
			}
			if unmarshal(u.Cost.Value, &c) == nil && c.Amount != nil {
				usage.Cost = Field[Cost]{Presence: Set, Value: Cost{Amount: *c.Amount, Currency: c.Currency}}
			}
		}
		return usage
	case "notice":
		if u.Title.Value == "" {
			return nil
		}
		return &Notice{head: h, Severity: u.Severity, Title: u.Title.Value, Description: u.Description}
	case "available_commands_update":
		c := &CommandsUpdate{head: h, Commands: []Command{}}
		for _, ac := range u.AvailableCommands {
			if ac.Name == "" {
				continue
			}
			cmd := Command{Type: ac.Type, Name: ac.Name, Description: ac.Description}
			if ac.Input != nil {
				cmd.InputHint = ac.Input.Hint
			}
			c.Commands = append(c.Commands, cmd)
		}
		return c
	case "config_option_update":
		return &ConfigUpdate{head: h, Options: parseConfigOptions(v, u.ConfigOptions)}
	case "current_mode_update":
		if v >= 2 {
			break
		}
		if u.CurrentModeID == "" {
			return nil
		}
		return &ConfigUpdate{head: h, Options: []ConfigOption{{ID: "mode", Name: "Mode", Category: "mode", Type: "select", Value: u.CurrentModeID}}}
	case "session_info_update":
		title := u.Title
		if v < 2 {
			title = v1Patch(title)
		}
		return &SessionInfoUpdate{head: h, Title: title}
	case "compaction_update":
		if u.Status.Value == "" {
			return nil
		}
		var errText string
		_ = unmarshal(u.Error, &errText)
		return &CompactionUpdate{head: h, CompactionID: u.CompactionID, Status: u.Status.Value, Error: errText}
	}
	return &UnknownUpdate{head: h, Raw: append(json.RawMessage(nil), raw...)}
}

func messageRole(kind string) Role {
	switch kind {
	case "user_message", "user_message_chunk":
		return RoleUser
	case "agent_thought", "agent_thought_chunk":
		return RoleThought
	}
	return RoleAgent
}

// parseConfigOptions reads session config options: configId in version 2,
// id in version 1. An option that cannot be read is left out.
func parseConfigOptions(v int, raws []json.RawMessage) []ConfigOption {
	out := []ConfigOption{}
	for _, raw := range raws {
		var o struct {
			ConfigID     string          `json:"configId"`
			ID           string          `json:"id"`
			Name         string          `json:"name"`
			Category     string          `json:"category"`
			Type         string          `json:"type"`
			CurrentValue json.RawMessage `json:"currentValue"`
			Options      json.RawMessage `json:"options"`
		}
		if unmarshal(raw, &o) != nil {
			continue
		}
		opt := ConfigOption{ID: o.ConfigID, Name: o.Name, Category: o.Category, Type: o.Type}
		if v < 2 {
			opt.ID = o.ID
		}
		if opt.ID == "" {
			continue
		}
		var s string
		var b bool
		switch {
		case unmarshal(o.CurrentValue, &s) == nil:
			opt.Value = s
		case unmarshal(o.CurrentValue, &b) == nil:
			opt.Value = map[bool]string{true: "true", false: "false"}[b]
		}
		opt.ValueName = selectName(o.Options, opt.Value)
		out = append(out, opt)
	}
	return out
}

// selectName finds the name of value among a select option's values, which
// are a flat list or a list of groups.
func selectName(raw json.RawMessage, value string) string {
	type choice struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	}
	var flat []struct {
		choice
		Options []choice `json:"options"`
	}
	if value == "" || unmarshal(raw, &flat) != nil {
		return ""
	}
	for _, c := range flat {
		if c.Value == value {
			return c.Name
		}
		for _, g := range c.Options {
			if g.Value == value {
				return g.Name
			}
		}
	}
	return ""
}
