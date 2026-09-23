// Package integration connects coding-agent harnesses to tuios agent state.
//
// It has two halves. The hook half translates the payload a harness hands its
// lifecycle hooks into one set-agent-state report, one set-agent-session
// report for a harness trusted with the conversation id alone, or a reason to
// report nothing: `tuios agent-hook <harness>` reads the payload and sends what
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
	ClaudeCode  = "claude-code"
	Codex       = "codex"
	GeminiCLI   = "gemini-cli"
	OpenCode    = "opencode"
	Amp         = "amp"
	Antigravity = "antigravity"
	Copilot     = "copilot"
	Crush       = "crush"
	CursorAgent = "cursor-agent"
	Devin       = "devin"
	Droid       = "droid"
	Grok        = "grok"
	Hermes      = "hermes"
	Kilo        = "kilo"
	Kimi        = "kimi"
	Pi          = "pi"
	Qoder       = "qoder"
	Qwen        = "qwen"
)

// harnessAliases maps the names a person or a config types to a harness id.
// Every id maps to itself; the rest are the program names and the names
// other tools use for the same harness.
var harnessAliases = map[string]string{
	"claude":          ClaudeCode,
	"claude-code":     ClaudeCode,
	"claudecode":      ClaudeCode,
	"codex":           Codex,
	"gemini":          GeminiCLI,
	"gemini-cli":      GeminiCLI,
	"opencode":        OpenCode,
	"amp":             Amp,
	"antigravity":     Antigravity,
	"antigravity-cli": Antigravity,
	"agy":             Antigravity,
	"copilot":         Copilot,
	"copilot-cli":     Copilot,
	"crush":           Crush,
	"cursor-agent":    CursorAgent,
	"cursor":          CursorAgent,
	"devin":           Devin,
	"droid":           Droid,
	"factory":         Droid,
	"grok":            Grok,
	"grok-cli":        Grok,
	"hermes":          Hermes,
	"hermes-agent":    Hermes,
	"kilo":            Kilo,
	"kilo-code":       Kilo,
	"kilocode":        Kilo,
	"kimi":            Kimi,
	"kimi-cli":        Kimi,
	"kimi-code":       Kimi,
	"pi":              Pi,
	"qoder":           Qoder,
	"qodercli":        Qoder,
	"qwen":            Qwen,
	"qwen-code":       Qwen,
}

// Canonical resolves a harness name to the id this package knows it by,
// reporting whether it knows it at all.
func Canonical(name string) (string, bool) {
	id, ok := harnessAliases[strings.ToLower(strings.TrimSpace(name))]
	return id, ok
}

// HarnessIDs lists the harnesses with a hook mapping, in a stable order.
func HarnessIDs() []string {
	return []string{
		ClaudeCode, Codex, GeminiCLI, OpenCode,
		Amp, Antigravity, Copilot, Crush, CursorAgent, Devin, Droid, Grok,
		Hermes, Kilo, Kimi, Pi, Qoder, Qwen,
	}
}

// ManifestIDs lists every harness the bundled manifests describe, with or
// without a hook mapping. A TUIOS_AGENT naming one of them names the pane's
// owner even when that owner has no integration, so a hook from any other
// harness in the pane is foreign. A test holds this to the manifests.
var ManifestIDs = []string{
	"aider", "amp", "antigravity", "claude-code", "cline", "codex", "copilot",
	"crush", "cursor-agent", "devin", "droid", "gemini-cli", "grok", "hermes",
	"kilo", "kimi", "kiro", "maki", "opencode", "pi", "qoder", "qwen",
}

// hintOwner resolves a TUIOS_AGENT value to a harness id: an alias this
// package knows, else a bundled manifest id. It reports false for a name
// neither knows, which says nothing about who owns the pane.
func hintOwner(hint string) (string, bool) {
	if id, ok := Canonical(hint); ok {
		return id, true
	}
	hint = strings.ToLower(strings.TrimSpace(hint))
	for _, id := range ManifestIDs {
		if id == hint {
			return id, true
		}
	}
	return "", false
}

// Report is one set-agent-state call, in the verb's own field names, or with
// SessionOnly one set-agent-session call. Empty fields are left out of the
// call.
type Report struct {
	// SessionOnly makes the report a set-agent-session call: it names the
	// conversation and says nothing about the pane's state. State is empty.
	SessionOnly    bool   `json:"session_only,omitempty"`
	State          string `json:"state,omitempty"`
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
	// Approval is set, beside a needs_input report, for an event whose
	// harness takes a decision back from the hook. The hook may then hold the
	// prompt for an answer from the Inbox. See approval.go.
	Approval *Approval `json:"approval,omitempty"`
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
		if owner, known := hintOwner(hint); known && owner != id {
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
	case OpenCode, Kilo:
		return translateOpenCode(id, in, p)
	case Amp:
		return translateAmp(in, p)
	case Kimi:
		return translateKimi(in, p)
	case Pi:
		return translatePi(in, p)
	default:
		return translateIdentity(id, in, p)
	}
}

// StdoutAnswer is what a hook for harness must print on stdout whatever it
// decides: the answer that changes nothing, for a harness that parses a
// hook's stdout as JSON. It is empty for the rest, which read an empty stdout
// as no opinion.
func StdoutAnswer(harness string) string {
	id, _ := Canonical(harness)
	switch id {
	case GeminiCLI, Antigravity:
		return "{}\n"
	}
	return ""
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
// hook_event_name, else the spellings other harnesses use for it: Copilot's
// camelCase payloads say hookEventName and Crush's say event.
func eventName(in Input, p fields) string {
	if in.Event != "" {
		return in.Event
	}
	return p.first("hook_event_name", "hookEventName", "event")
}

// first is the first of keys holding a non-empty string.
func (f fields) first(keys ...string) string {
	for _, k := range keys {
		if v := f.str(k); v != "" {
			return v
		}
	}
	return ""
}

// identity copies the session id and transcript path every Claude-shaped
// payload carries onto a report.
func identity(r Report, p fields) Report {
	r.SessionID = p.str("session_id")
	r.TranscriptPath = p.str("transcript_path")
	return r
}
