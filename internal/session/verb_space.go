package session

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

// spaceNameMax caps a space name in bytes. A space is a row of the rail and a
// heading of tuios ls, and every session filed under it carries the name.
const spaceNameMax = 64

// checkSpaceName refuses a space name param may not carry. A space name
// follows the session-name rules so a surface can show and address it as
// space/session without a separator inside either half. Empty passes: it is
// no space.
func checkSpaceName(param, name string) *verbError {
	if err := ValidateSessionName(name); err != nil {
		return invalidParam(param, "a space name follows the session name rules: "+err.Error())
	}
	if len(name) > spaceNameMax {
		return invalidParam(param, "a space name is at most "+strconv.Itoa(spaceNameMax)+" bytes, and this one is "+strconv.Itoa(len(name)))
	}
	return nil
}

func (d *Daemon) verbSetSessionSpace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Space   string `json:"space"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkSpaceName("space", p.Space); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Space != "" && sess.isGlobal() {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+echoName(sess.Name())+" is global, and a global session is in no space. Nothing was changed", &VerbHint{
			Param:  "space",
			Detail: "A global session is drawn above the machines, never inside a space, so tuios new refuses --space with --global and this verb refuses to file one.",
		})
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
	if verr := checkSpaceName("name", p.Name); verr != nil {
		return nil, verr
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

	killed := make([]string, 0, len(members))
	for _, sess := range members {
		// By id, so a member renamed since the lookup still goes and a session
		// that took a member's old name stays. A member someone else killed
		// since is already gone, which is what was asked for, so it is not an
		// error. It is not listed as killed by this call either.
		if name, err := d.manager.DeleteSessionByID(sess.ID); err == nil {
			killed = append(killed, name)
		}
	}
	return map[string]any{"type": "space_killed", "space": p.Space, "sessions": killed}, nil
}

// switch-space switches a client to the session of a space the person used
// last, ranked as a bare attach ranks sessions: the latest use, then the latest
// activity. The use is the same last_used list-spaces reports, compared at full
// precision. Which client switches, and how, is switch-session's.
//
// none picks among the sessions in no space instead, which a client's rail
// draws as the none row of its spaces section. It is a flag rather than an
// empty space, which every other space verb refuses as a mistake.
//
// The pick is the daemon's alone, and pick-space answers it without switching
// anything: the tuios client asks it and then switches itself, as a click on a
// session row does. See spacePick.
func (d *Daemon) verbSwitchSpace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Space   string `json:"space"`
		None    bool   `json:"none"`
		Session string `json:"session"`
		Client  string `json:"client"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	target, verr := d.spacePick(p.Space, p.None)
	if verr != nil {
		return nil, verr
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
	if p.None {
		out["none"] = true
	}
	return out, nil
}

// pick-space answers which session switch-space would land on, and switches
// nothing. It reads what list-spaces and list-sessions already show, so it is
// held to what they are. The tuios client calls it for a space key or a click
// on a space's row, on the daemon that holds its session, and then switches
// itself; that keeps the ranking here and leaves the routed commands, and the
// fence a client keeps against another machine's, out of it.
//
// It is a verb of its own rather than a flag on switch-space because it only
// reads: it is held as list-spaces is (scopeGlobal, the list link capability),
// while switch-space moves a client and is held as switch-session is (scopeDeny,
// the write link capability). One verb could not carry both.
func (d *Daemon) verbPickSpace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Space string `json:"space"`
		None  bool   `json:"none"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	target, verr := d.spacePick(p.Space, p.None)
	if verr != nil {
		return nil, verr
	}
	return map[string]any{"type": "space_pick", "space": p.Space, "none": p.None, "session": target.Name()}, nil
}

// spacePick is the session of space the person used last, or with none of the
// sessions in no space: the latest use, then the latest activity, compared at
// full precision (mostRecentSession). It refuses both and neither, and a set
// with no session in it.
func (d *Daemon) spacePick(space string, none bool) (*Session, *verbError) {
	switch {
	case none && space != "":
		return nil, invalidParam("none", "none means the sessions in no space, so it cannot be passed with space "+echoName(space)+". Pass one of the two")
	case !none && space == "":
		return nil, invalidParam("space", "space cannot be empty. For the sessions in no space pass none")
	}
	if target := mostRecentSession(d.spacePickMembers(space), true); target != nil {
		return target, nil
	}
	return nil, d.noSpacePickMembers(space)
}

// spacePickMembers is the sessions spacePick chooses among for space, ""
// for the sessions in no space.
func (d *Daemon) spacePickMembers(space string) []*Session {
	var members []*Session
	for _, sess := range d.manager.AllSessions() {
		if sess.Space() == space {
			members = append(members, sess)
		}
	}
	return members
}

// noSpacePickMembers is spacePick's refusal when spacePickMembers is
// empty.
func (d *Daemon) noSpacePickMembers(space string) *verbError {
	available := d.spaceNames()
	if space == "" {
		return hintedVerbError(ErrVerbInvalidParams, "every session is in a space, so none has no session", &VerbHint{
			Param:     "none",
			Verb:      "list-spaces",
			Available: available,
			Detail:    "none means the sessions in no space. Name a space instead.",
		})
	}
	return hintedVerbError(ErrVerbInvalidParams, "no session is in space "+echoName(space), &VerbHint{
		Param:      "space",
		Verb:       "list-spaces",
		DidYouMean: closestMatch(space, available),
		Available:  available,
		Detail:     "a space exists while at least one session is filed under it. A global session is not counted in its space here, as the rail draws it on its own.",
	})
}
