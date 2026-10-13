package session

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
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

// sessionsInSpace is every live session filed under space, ordered by name so
// a cascade over them runs, and reports, in the same order every time.
func (d *Daemon) sessionsInSpace(space string) []*Session {
	var members []*Session
	for _, sess := range d.manager.AllSessions() {
		if sess.Space() == space {
			members = append(members, sess)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name() < members[j].Name() })
	return members
}

// spaceNames is every space a live session is filed under, sorted.
func (d *Daemon) spaceNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, sess := range d.manager.AllSessions() {
		if sp := sess.Space(); sp != "" && !seen[sp] {
			seen[sp] = true
			names = append(names, sp)
		}
	}
	sort.Strings(names)
	return names
}

// noSuchSpace is the refusal for a space no live session is filed under. A
// space has no record of its own, so "not found" and "empty" are the same.
func (d *Daemon) noSuchSpace(space string) *verbError {
	available := d.spaceNames()
	return hintedVerbError(ErrVerbInvalidParams, "no session is in space "+echoName(space), &VerbHint{
		Param:      "space",
		Verb:       "list-spaces",
		DidYouMean: closestMatch(space, available),
		Available:  available,
		Detail:     "a space exists while at least one session is filed under it. File one with set-session-space or tuios new --space.",
	})
}

// rename-space files every session of one space under another name. It is one
// SetSpace per member, not one transaction: a crash part way leaves some
// members under each name, and running the same rename again moves the rest,
// because the members are looked up afresh on every call.
func (d *Daemon) verbRenameSpace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Space string `json:"space"`
		Name  string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Space == "" {
		return nil, invalidParam("space", "space is the space to rename and cannot be empty")
	}
	if p.Name == "" {
		return nil, invalidParam("name", "name is the space's new name and cannot be empty. To take sessions out of a space, use set-session-space")
	}
	if err := ValidateSessionName(p.Name); err != nil {
		return nil, invalidParam("name", "a space name follows the session name rules: "+err.Error())
	}

	members := d.sessionsInSpace(p.Space)
	out := map[string]any{"type": "space_renamed", "space": p.Name, "old_space": p.Space}
	if len(members) == 0 || p.Space == p.Name {
		// The rename already finished: nothing is left under the old name and
		// the new one holds sessions. Answering that as success is what makes
		// running a rename again after a crash safe.
		if len(d.sessionsInSpace(p.Name)) > 0 {
			out["sessions"] = []string{}
			out["already"] = true
			return out, nil
		}
		return nil, d.noSuchSpace(p.Space)
	}

	renamed := make([]string, 0, len(members))
	for _, sess := range members {
		if err := sess.SetSpace(p.Name); err != nil {
			return nil, newVerbError(ErrVerbInternal, "renamed "+strconv.Itoa(len(renamed))+" of "+strconv.Itoa(len(members))+
				" sessions, then "+sess.Name()+" failed: "+err.Error()+". Run the same rename again to move the rest")
		}
		renamed = append(renamed, sess.Name())
	}
	out["sessions"] = renamed
	return out, nil
}

// kill-space kills every session filed under a space, as kill-session kills
// one. It ends every process in all of them, so it refuses unless force is
// passed, and the refusal names the sessions that would go.
func (d *Daemon) verbKillSpace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Space string `json:"space"`
		Force bool   `json:"force"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Space == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams,
			"space is required (kill-space never guesses which sessions to destroy)",
			&VerbHint{Param: "space", Verb: "list-spaces", Available: d.spaceNames()})
	}
	members := d.sessionsInSpace(p.Space)
	if len(members) == 0 {
		return nil, d.noSuchSpace(p.Space)
	}
	names := make([]string, len(members))
	for i, s := range members {
		names[i] = s.Name()
	}
	if !p.Force {
		return nil, hintedVerbError(ErrVerbInvalidParams,
			"kill-space would kill "+plural.Count(len(names), "session")+" in space "+echoName(p.Space)+" ("+strings.Join(names, ", ")+"). Nothing was killed",
			&VerbHint{
				Param:   "force",
				Command: "tuios kill-space --force " + p.Space,
				Detail:  "kill-space ends every process in every session of the space, so it asks to be confirmed: --force on the command line, or \"force\": true when calling the verb.",
			})
	}

	killed := make([]string, 0, len(names))
	for _, name := range names {
		// A member someone else killed since the lookup is already gone, which
		// is what was asked for, so it is not an error. It is not listed as
		// killed by this call either.
		if err := d.manager.DeleteSession(name); err == nil {
			killed = append(killed, name)
		}
	}
	return map[string]any{"type": "space_killed", "space": p.Space, "sessions": killed}, nil
}

// switch-space switches a client to the session of a space the person used
// last, ranked as a bare attach ranks sessions: the latest use, then the latest
// activity. The use is the same last_used list-spaces reports, compared at full
// precision. Which client switches, and how, is switch-session's.
func (d *Daemon) verbSwitchSpace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Space   string `json:"space"`
		Session string `json:"session"`
		Client  string `json:"client"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Space == "" {
		return nil, invalidParam("space", "space is the space to switch to and cannot be empty")
	}
	target := mostRecentSession(d.sessionsInSpace(p.Space), true)
	if target == nil {
		return nil, d.noSuchSpace(p.Space)
	}

	raw, err := json.Marshal(switchSessionParams{Name: target.Name(), Session: p.Session, Client: p.Client})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	res, verr := d.verbSwitchSession(cs, raw)
	if verr != nil {
		return nil, verr
	}
	out, ok := res.(map[string]any)
	if !ok {
		return nil, newVerbError(ErrVerbInternal, "switch-session answered with no result")
	}
	out["type"] = "space_switched"
	out["space"] = p.Space
	return out, nil
}
