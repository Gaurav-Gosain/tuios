package session

import (
	"encoding/json"
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
