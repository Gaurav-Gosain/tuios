package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// A session has one name, and the daemon owns it. rename-session changes it:
// the name the daemon lists, addresses the session by, saves it under and
// hands to new panes as TUIOS_SESSION. Every client that renames a session
// goes through this verb and shows the name the daemon then pushes.
//
// set-session-name is a different thing: an optional display label that some
// scripts set on top of the name. A rename clears it, so the name a person
// just typed is the name every view shows.

// RenamedSessionMessage is the refusal an attach by an old name gets. The
// wording is parsed back by RenamedSessionTarget, so change both together.
func RenamedSessionMessage(old, current string) string {
	return fmt.Sprintf("session '%s' was renamed to '%s'", old, current)
}

var renamedSessionRE = regexp.MustCompile(`session '(.*)' was renamed to '(.*)'`)

// RenamedSessionTarget reads the new name out of an error that carries
// RenamedSessionMessage. ok is false for every other error.
func RenamedSessionTarget(err error) (current string, ok bool) {
	if err == nil {
		return "", false
	}
	m := renamedSessionRE.FindStringSubmatch(err.Error())
	if m == nil {
		return "", false
	}
	return m[2], true
}

func (d *Daemon) verbRenameSession(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Name    string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	old := sess.Name()
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "name is required", &VerbHint{
			Param:  "name",
			Detail: "Give the session's new name. To set a display label instead, use set-session-name.",
		})
	}
	if name != old && d.manager.GetSession(name) != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+name+" already exists", &VerbHint{
			Param:     "name",
			Command:   "tuios ls",
			Available: d.sessionNames(),
			Detail:    "Each session has its own name. Pick a name no other session has.",
		})
	}
	if _, err := d.manager.RenameSession(old, name); err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{Param: "name"})
	}
	return map[string]any{"type": "session_renamed", "session": name, "old_name": old}, nil
}

// renameNotFollowed are the verbs whose session param is not rewritten from
// an old name to the current one. new-session makes a session of the name it
// is given, and kill-session must never destroy a session the caller named by
// a name it no longer has: it is refused with the new name instead.
var renameNotFollowed = map[string]bool{
	"new-session":  true,
	"kill-session": true,
}

// followRenamedSession rewrites a session param that names a session by a
// name it was renamed from to the name it has now. It runs before the pane
// grants and the connection scope, so a pane started before the rename, whose
// TUIOS_SESSION still holds the old name, is held to its own session as it
// was before, and every handler sees the current name.
func (d *Daemon) followRenamedSession(verb string, params json.RawMessage) json.RawMessage {
	if renameNotFollowed[verb] || len(params) == 0 {
		return params
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil {
		return params
	}
	raw, ok := m["session"]
	if !ok {
		return params
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil || name == "" {
		return params
	}
	sess, renamed := d.manager.ResolveSession(name)
	if !renamed || sess == nil {
		return params
	}
	m["session"], _ = json.Marshal(sess.Name())
	out, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return out
}

// StaleDaemonError explains an unknown_verb answer for a verb this tuios
// knows: the daemon that answered is older than the binary. It returns nil
// for every other error, including an unknown_verb for a verb this tuios
// does not know either.
func StaleDaemonError(verb string, err error) error {
	var call *VerbCallError
	if !errors.As(err, &call) || call.Code != ErrVerbUnknownVerb {
		return nil
	}
	if _, known := verbRegistry[verb]; !known {
		return nil
	}
	return fmt.Errorf("the running tuios daemon is older than this tuios and does not know %s. Run 'tuios kill-server' and start tuios again. Saved sessions come back with new shells", verb)
}
