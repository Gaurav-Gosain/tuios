// Package integration connects coding-agent harnesses to tuios agent state.
//
// It has two halves. The hook half translates the payload a harness hands its
// lifecycle hooks into one set-agent-state report, or into a reason to report
// nothing: `tuios agent-hook <harness>` reads the payload and sends what
// Translate decides. The install half writes managed hook entries into each
// harness's own configuration, removes only what it wrote, and says whether
// what is installed is current: `tuios integration install|uninstall|status`
// and `tuios doctor agents`.
//
// Every harness mapping in this package cites the documentation or working
// code it was taken from, since a hook format is a thing other people change.
package integration

import (
	"encoding/json"
	"strings"
)

// Harness ids, as the manifests in internal/harness name them.
const (
	ClaudeCode = "claude-code"
	Codex      = "codex"
	GeminiCLI  = "gemini-cli"
	OpenCode   = "opencode"
)

// harnessAliases maps the names a person or a config types to a harness id.
var harnessAliases = map[string]string{
	"claude":      ClaudeCode,
	"claude-code": ClaudeCode,
	"claudecode":  ClaudeCode,
	"codex":       Codex,
	"gemini":      GeminiCLI,
	"gemini-cli":  GeminiCLI,
	"opencode":    OpenCode,
}

// Canonical resolves a harness name to the id this package knows it by,
// reporting whether it knows it at all.
func Canonical(name string) (string, bool) {
	id, ok := harnessAliases[strings.ToLower(strings.TrimSpace(name))]
	return id, ok
}

// HarnessIDs lists the harnesses with a hook mapping, in a stable order.
func HarnessIDs() []string { return []string{ClaudeCode, Codex, GeminiCLI, OpenCode} }

// Report is one set-agent-state call, in the verb's own field names. Empty
// fields are left out of the call.
type Report struct {
	State          string `json:"state"`
	Kind           string `json:"kind,omitempty"`
	Message        string `json:"message,omitempty"`
	SessionID      string `json:"agent_session_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	// IfState is a comma-separated state list: the report applies only when
	// the pane is in one of them.
	IfState string `json:"if_state,omitempty"`
}

// Decision is what one hook event comes to: a report, or the reason there is
// none. Exactly one of the two is set.
type Decision struct {
	Harness string  `json:"harness"`
	Event   string  `json:"event"`
	Report  *Report `json:"report,omitempty"`
	Skip    string  `json:"skip,omitempty"`
}

func skip(harness, event, why string) Decision {
	return Decision{Harness: harness, Event: event, Skip: why}
}

func send(harness, event string, r Report) Decision {
	return Decision{Harness: harness, Event: event, Report: &r}
}

// Input is everything a hook invocation carries: the event named on the
// command line (empty when only the payload names it), the payload, and the
// hook process's environment.
type Input struct {
	Event   string
	Payload []byte
	Getenv  func(string) string
}

func (in Input) env(name string) string {
	if in.Getenv == nil {
		return ""
	}
	return in.Getenv(name)
}

// AgentHintEnv is the variable a sandbox wrapper sets to say which harness
// owns the pane. It is the same variable the daemon's detector reads.
const AgentHintEnv = "TUIOS_AGENT"

// Translate decides what one hook event reports.
//
// It never guesses. A payload that does not parse, an event it has no mapping
// for, and a notification type it does not know all report nothing, because a
// wrong state is worse than a missing one: the old shim's habit of reading
// every notification as needs_input is what this replaces. In particular a
// failure never becomes done.
//
// The checks that apply to every harness run first: a TUIOS_AGENT naming a
// different harness means this event comes from an agent nested inside the one
// the pane belongs to, and it is dropped here rather than sent.
func Translate(harnessName string, in Input) Decision {
	id, ok := Canonical(harnessName)
	if !ok {
		return skip(harnessName, in.Event, "no hook mapping for harness "+harnessName)
	}
	if hint := strings.TrimSpace(in.env(AgentHintEnv)); hint != "" {
		if owner, known := Canonical(hint); known && owner != id {
			return skip(id, in.Event, "foreign harness: "+AgentHintEnv+" names "+owner)
		}
	}
	// Every harness hands its hooks a JSON object. Anything else, an empty
	// stdin included, means the payload was lost on the way, and the event
	// named on the command line is not enough to act on: a Stop that cannot
	// be read might be a subagent's.
	trimmed := strings.TrimSpace(string(in.Payload))
	if trimmed == "" {
		return skip(id, in.Event, "empty payload")
	}
	payload := map[string]any{}
	if !strings.HasPrefix(trimmed, "{") {
		return skip(id, in.Event, "payload is not a JSON object")
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return skip(id, in.Event, "payload is not a JSON object: "+err.Error())
	}
	p := fields(payload)
	switch id {
	case ClaudeCode:
		return translateClaude(in, p)
	case Codex:
		return translateCodex(in, p)
	case GeminiCLI:
		return translateGemini(in, p)
	default:
		return translateOpenCode(in, p)
	}
}

// fields reads a decoded payload without panicking on a field of the wrong
// type: a harness that changes a field's type costs that field, not the hook.
type fields map[string]any

func (f fields) str(key string) string {
	if v, ok := f[key].(string); ok {
		return v
	}
	return ""
}

func (f fields) obj(key string) fields {
	if v, ok := f[key].(map[string]any); ok {
		return fields(v)
	}
	return fields{}
}

// eventName is the event the command line named, else the payload's
// hook_event_name.
func eventName(in Input, p fields) string {
	if in.Event != "" {
		return in.Event
	}
	return p.str("hook_event_name")
}

// identity copies the session id and transcript path every Claude-shaped
// payload carries onto a report.
func identity(r Report, p fields) Report {
	r.SessionID = p.str("session_id")
	r.TranscriptPath = p.str("transcript_path")
	return r
}
