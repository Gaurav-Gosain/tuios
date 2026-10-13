package session

import (
	"encoding/json"
	"sort"
)

func (d *Daemon) verbSetSessionSpace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Space   string `json:"space"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	// A space name follows the session-name rules so a later surface can show
	// and address it as space/session without a separator inside either half.
	if err := ValidateSessionName(p.Space); err != nil {
		return nil, invalidParam("space", "a space name follows the session name rules: "+err.Error())
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if err := sess.SetSpace(p.Space); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session space: "+err.Error())
	}
	return map[string]any{"type": "session_space_set", "session": sess.Name(), "space": p.Space}, nil
}

type spaceMember struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Attached bool   `json:"attached"`
	LastUsed int64  `json:"last_used,omitempty"`
}

type spaceListing struct {
	Name     string        `json:"name"`
	Sessions []spaceMember `json:"sessions"`
	Attached bool          `json:"attached"`
	LastUsed int64         `json:"last_used,omitempty"`
}

func (d *Daemon) verbListSpaces(_ *connState, _ json.RawMessage) (any, *verbError) {
	byName := map[string]*spaceListing{}
	for _, info := range d.listSessions() {
		if info.Space == "" {
			continue
		}
		member := spaceMember{Name: info.Name, ID: info.ID, Attached: info.Attached}
		if sess := d.manager.GetSessionByID(info.ID); sess != nil {
			if used := sess.LastUsed(); !used.IsZero() {
				member.LastUsed = used.Unix()
			}
		}
		sp := byName[info.Space]
		if sp == nil {
			sp = &spaceListing{Name: info.Space}
			byName[info.Space] = sp
		}
		sp.Sessions = append(sp.Sessions, member)
		sp.Attached = sp.Attached || member.Attached
		sp.LastUsed = max(sp.LastUsed, member.LastUsed)
	}

	spaces := make([]spaceListing, 0, len(byName))
	for _, sp := range byName {
		spaces = append(spaces, *sp)
	}
	sort.Slice(spaces, func(i, j int) bool { return spaces[i].Name < spaces[j].Name })
	return map[string]any{"type": "space_list", "spaces": spaces}, nil
}
