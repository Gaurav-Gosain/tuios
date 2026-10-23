package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

// spaceNameMax caps a space name in bytes. A space is a heading of tuios ls,
// and every session filed under it carries the name.
const spaceNameMax = 64

// GlobalSpaceRefusal is the one message for filing a global session under a
// space, whether set-session-space is asked to or tuios new is given --global
// with --space. Global is fixed when a session is made, so the refusal is too.
const GlobalSpaceRefusal = "a global session cannot be in a space"

// spaceName trims a space name as the sibling session verbs trim theirs, then
// refuses one that breaks the session-name rules or the length cap. Empty
// passes: it is no space. Keeping "/" out leaves room for a space/ filter.
func spaceName(raw string) (string, *verbError) {
	name := strings.TrimSpace(raw)
	if err := ValidateSessionName(name); err != nil {
		return "", invalidParam("space", "a space name follows the session name rules: "+err.Error())
	}
	if len(name) > spaceNameMax {
		return "", invalidParam("space", "a space name is at most "+strconv.Itoa(spaceNameMax)+" bytes, and this one is "+strconv.Itoa(len(name)))
	}
	return name, nil
}

func (d *Daemon) verbSetSessionSpace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Space   string `json:"space"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	space, verr := spaceName(p.Space)
	if verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if space != "" && sess.isGlobal() {
		return nil, hintedVerbError(ErrVerbInvalidParams, GlobalSpaceRefusal, &VerbHint{
			Param:  "space",
			Detail: "Session " + echoName(sess.Name()) + " is global. A global session holds panes from more than one machine and is listed in its own group. Nothing changed.",
		})
	}
	if err := sess.SetSpace(space); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session space: "+err.Error())
	}
	return map[string]any{"type": "session_space_set", "session": sess.Name(), "space": space}, nil
}
