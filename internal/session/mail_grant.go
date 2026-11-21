package session

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The mail grant: leave mail in any session on this daemon without admin.
//
// A pane held to read, write and task (an agent in an OS sandbox, say) reaches
// only its own session, and fan, which reaches further, also starts programs
// the daemon runs outside the sandbox. Agents in different sessions, one per
// repository, still need to tell each other things. Mail is the one way to do
// that which types nothing: the body is stored, fenced as data, and read by the
// recipient on purpose under its own grants.
//
// So a pane holding mail may:
//
//   - send-agent-message to any session on this daemon, by session and window
//     or by selector, as itself: from is the pane the kernel placed the caller
//     in, and naming another pane, human included, is refused (holdMailSend).
//     host is refused: another machine is not this daemon.
//   - list-agents across sessions (all_sessions, a selector, or a session out
//     of its read reach), to find whom to address. A row out of its read reach
//     carries only the session, window, name, harness and state
//     (mailListingRow). all, which lists every window, stays with read.
//
// What mail does not open:
//
//   - Typing: send-text, send-keys, ask-agent, run and queue-prompt into
//     another session keep needing fan or admin. The mail wake types only the
//     fixed line tuios writes (mail_wake.go), and with mail it may wake a
//     pane the sender could not type into (queueOriginRefusal), because that
//     line holds nothing the sender chose.
//   - Reading: capture-pane, peek-prompt, transcripts and the mail of
//     another session stay with read in reach and admin beyond it.
//   - Files: an attachment to a session out of write reach must be a file in
//     the pane's own folder or its own session's stash (mailAttachmentRefusal),
//     since the daemon reads the file's size and kind, and the recipient is
//     pointed at it.
//
// Admin implies mail. Write does not, so no existing setup reaches further.

// holdMailSend holds a send-agent-message from a pane holding mail. The call
// may name any session of this daemon, or a selector, and is sent as the pane:
// from is filled in with the pane's window id, and any other from is refused.
// A call that names neither a session nor a selector goes to the pane's own
// session, as every call from a pane without admin does.
func (d *Daemon) holdMailSend(pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (json.RawMessage, *verbError) {
	m, verr := paramsObject(params)
	if verr != nil {
		return nil, verr
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = jsonx.Unmarshal(raw, &s)
		}
		return s
	}
	set := func(name, value string) {
		raw, _ := jsonx.Marshal(value)
		m[name] = raw
	}
	if h := str("host"); h != "" && h != "local" {
		return nil, deny("host " + echoName(h) + " is another machine, and the mail grant reaches the sessions of this daemon only")
	}
	switch f := str("from"); f {
	case "", pa.window:
	default:
		return nil, deny("from must be the caller's own window " + shortWindowID(pa.window) + ", or omitted: mail is sent as the pane that sends it")
	}
	set("from", pa.window)
	if str("select") == "" && str("session") == "" {
		set("session", pa.session)
	}
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	return out, nil
}

// holdMailListing holds a list-agents from a pane holding mail. held is false
// for a listing inside the pane's read reach, which the read rules hold as
// before. A listing beyond it (every session, a selector over every session,
// or a session the pane may not read) passes, and the handler cuts the rows
// it may not read down to what addressing needs (mailListingRow). all lists
// every window, not only agents, and is refused beyond the read reach.
func (d *Daemon) holdMailListing(pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (out json.RawMessage, verr *verbError, held bool) {
	m, verr := paramsObject(params)
	if verr != nil {
		return nil, verr, true
	}
	var p struct {
		Session     string `json:"session"`
		All         bool   `json:"all"`
		AllSessions bool   `json:"all_sessions"`
		Select      string `json:"select"`
	}
	_ = jsonx.Unmarshal(params, &p)
	wide := p.AllSessions || (p.Select != "" && p.Session == "")
	session := p.Session
	if !wide {
		if session == "" {
			session = pa.session
		}
		if d.paneReads(pa, session) {
			return nil, nil, false
		}
	}
	if p.All {
		return nil, deny("all lists every window, and beyond the sessions the pane may read the mail grant lists agent panes only"), true
	}
	if !wide {
		s, _ := jsonx.Marshal(session)
		m["session"] = s
	}
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params"), true
	}
	return out, nil, true
}

// paneReads reports whether pa may read session: its own session and its fan
// group, with read.
func (d *Daemon) paneReads(pa *paneAuth, session string) bool {
	return pa.grants.Has(GrantRead) && d.sessionInScope(pa.session, session)
}

// listingPane is the pane whose list-agents answer is cut to what it may
// read, nil when the caller reads every row: a caller in no pane, a pane with
// admin, and a native plugin, whose read reaches every session.
func listingPane(cs *connState) *paneAuth {
	pa := cs.paneViewOrNil()
	if pa == nil || pa.plugin != "" || pa.grants.Has(GrantAdmin) {
		return nil
	}
	return pa
}

// mailListingFields are the fields a list-agents row keeps for a pane that
// reaches the row's session through the mail grant alone: what addressing a
// message needs, and nothing of what the agent is doing or where.
var mailListingFields = []string{"session", "window_id", "name", "state", "harness_id"}

// mailListingRow is row cut to mailListingFields.
func mailListingRow(row map[string]any) map[string]any {
	out := make(map[string]any, len(mailListingFields)+1)
	for _, k := range mailListingFields {
		if v, ok := row[k]; ok {
			out[k] = v
		}
	}
	out["limited"] = true
	return out
}

// mailOnlyPane is the pane a send from cs reaches session through by the mail
// grant alone, nil when the caller reaches it some other way: no pane, admin,
// a plugin, or write or fan in reach.
func (d *Daemon) mailOnlyPane(cs *connState, session string) *paneAuth {
	pa := listingPane(cs)
	if pa == nil || !pa.grants.Has(GrantMail) || d.paneWriteReach(pa, session) == "" {
		return nil
	}
	return pa
}

// mailAttachmentRefusal says why a pane that reaches the target session by
// the mail grant alone may not attach path, "" when it may: a file inside the
// pane's own folder (paneFolder), or one in its own session's stash. The path
// is resolved before it is compared, so a link does not lead out, and nothing
// is read about a path that is refused.
func (d *Daemon) mailAttachmentRefusal(pa *paneAuth, path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	if pa.sessionID != "" && d.stash.owns(pa.sessionID, path) {
		return ""
	}
	if dir := d.paneFolder(pa); dir != "" && pathUnder(realPath(filepath.Clean(path)), dir) {
		return ""
	}
	return "the mail grant attaches only files inside the pane's own folder or its session's stash to mail for another session"
}

// paramsObject reads params as an object, empty for none.
func paramsObject(params json.RawMessage) (map[string]json.RawMessage, *verbError) {
	var m map[string]json.RawMessage
	if len(strings.TrimSpace(string(params))) > 0 {
		if err := jsonx.Unmarshal(params, &m); err != nil {
			return nil, invalidParam("params", "params must be an object")
		}
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

// mailWakeRefusal says why a pane holding the mail grant may not lend its
// authority to the wake line for mail it sent to target, "" when it may. The
// line is written by tuios alone (mailWakeText), so the rules about what typed
// text runs with, which keep a pane from typing into a pane that holds more
// or into another session, do not apply to it. The prompt rules do: the queue
// types only into an agent at rest, and the line is not typed into a pane
// whose prompt may still be on its screen, or, with the agent features off,
// into a pane nothing knows the state of.
func (d *Daemon) mailWakeRefusal(pa *paneAuth, sess *Session, target WindowState) string {
	if pa.grants.Has(GrantRespond) || target.ID == pa.window {
		return ""
	}
	if d.agentsOff.Load() {
		return "the agent features are off, so tuios cannot see whether window " + shortWindowID(target.ID) + " is waiting on a prompt"
	}
	if target.AgentState == AgentStateNeedsInput || sess.promptStillShown(target) {
		return "window " + shortWindowID(target.ID) + " may be waiting on a prompt, and the wake line would answer it"
	}
	return ""
}
