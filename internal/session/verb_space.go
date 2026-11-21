package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

const spaceNameMax = 64

// GlobalSpaceRefusal is the one message for filing a global session under a
// space, whether set-session-space is asked to or tuios new is given --global
// with --space. set-session-space with global false takes the mark off first,
// so a call can move a global session into a space in one step.
const GlobalSpaceRefusal = "a global session cannot be in a space"

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
		Global  *bool  `json:"global"`
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
	if p.Global != nil && *p.Global && space != "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, GlobalSpaceRefusal, &VerbHint{
			Param:  "space",
			Detail: "Pass global or space, not both. Nothing changed.",
		})
	}
	if p.Global != nil {
		if err := sess.SetGlobal(*p.Global); err != nil {
			return nil, newVerbError(ErrVerbInternal, "could not set the global mark: "+err.Error())
		}
		if *p.Global {
			return map[string]any{"type": "session_space_set", "session": sess.Name(), "space": "", "global": true}, nil
		}
	}
	if space != "" && sess.isGlobal() {
		return nil, hintedVerbError(ErrVerbInvalidParams, GlobalSpaceRefusal, &VerbHint{
			Param:  "space",
			Detail: "Session " + echoName(sess.Name()) + " is global. A global session holds panes from more than one machine and is listed in its own group. Pass global false to take the mark off. Nothing changed.",
		})
	}
	if err := sess.SetSpace(space); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session space: "+err.Error())
	}
	return map[string]any{"type": "session_space_set", "session": sess.Name(), "space": space, "global": sess.isGlobal()}, nil
}
