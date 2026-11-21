package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// RenamedSessionMessage is the refusal an attach by an old name gets. The
// wording is parsed back by RenamedSessionTarget, so change both together.
func RenamedSessionMessage(old, current string) string {
	return fmt.Sprintf("session '%s' was renamed to '%s'", old, current)
}

var renamedSessionRE = lazyre.New(`session '(.*)' was renamed to '(.*)'`)

// RenamedSessionTarget reads the new name out of an error that carries
// RenamedSessionMessage. ok is false for every other error.
func RenamedSessionTarget(err error) (current string, ok bool) {
	if err == nil {
		return "", false
	}
	m := renamedSessionRE().FindStringSubmatch(err.Error())
	if m == nil {
		return "", false
	}
	return m[2], true
}

func (d *Daemon) verbRenameSession(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Name    string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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

var renameNotFollowed = map[string]bool{
	"new-session":  true,
	"kill-session": true,
}

func (d *Daemon) followRenamedSession(verb string, params json.RawMessage) json.RawMessage {
	if renameNotFollowed[verb] || len(params) == 0 {
		return params
	}
	var m map[string]json.RawMessage
	if err := jsonx.Unmarshal(params, &m); err != nil {
		return params
	}
	raw, ok := m["session"]
	if !ok {
		return params
	}
	var name string
	if err := jsonx.Unmarshal(raw, &name); err != nil || name == "" {
		return params
	}
	sess, renamed := d.manager.ResolveSession(name)
	if !renamed || sess == nil {
		return params
	}
	m["session"], _ = jsonx.Marshal(sess.Name())
	out, err := jsonx.Marshal(m)
	if err != nil {
		return params
	}
	return out
}

func (d *Daemon) followOwnPane(cs *connState, verb string, params json.RawMessage) json.RawMessage {
	if renameNotFollowed[verb] || cs == nil || cs.viaLink || cs.paneOnly || len(params) == 0 {
		return params
	}
	var named struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	}
	if jsonx.Unmarshal(params, &named) != nil || named.Session == "" || named.Window == "" {
		return params
	}
	if s, renamed := d.manager.ResolveSession(named.Session); s != nil && !renamed {
		return params
	}
	holder := d.sessionHoldingWindow(named.Window)
	if holder == nil {
		return params
	}
	if placed, via := d.placePaneWindow(cs); placed != named.Window || (via != "token" && via != "pid") {
		return params
	}
	var m map[string]json.RawMessage
	if err := jsonx.Unmarshal(params, &m); err != nil {
		return params
	}
	var session, window string
	if jsonx.Unmarshal(m["session"], &session) != nil || session != named.Session ||
		jsonx.Unmarshal(m["window"], &window) != nil || window != named.Window {
		return params
	}
	m["session"], _ = jsonx.Marshal(holder.Name())
	out, err := jsonx.Marshal(m)
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
	if !verbKnown(verb) {
		return nil
	}
	return errfmt.Errorf("the running tuios daemon is older than this tuios and does not know %s. Run 'tuios restart-server'. Saved sessions come back with new shells", verb)
}
