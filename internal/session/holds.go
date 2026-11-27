package session

import (
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Holds: named, machine-wide locks the daemon keeps.
//
// A hold says "do not do X on this machine now": a quiet window for timing
// runs, a frozen shared tool, a PR a landing train owns. Before holds this was
// a sentence in mail. Nothing enforced it, a worker started later never saw
// it, and when it ended someone mailed every worker by hand.
//
// A hold belongs to one daemon and covers all of its sessions. A build script
// runs "tuios hold check NAME" first, and a worker that must wait runs
// "tuios wait-for hold-released NAME", which is woken by the hold-changed
// event and costs nothing while it waits. A hold ends when its holder
// releases it, when its ttl runs out, or when the holder pane closes. A
// caller in no pane has no pane to close, so its hold needs a ttl.
//
// Nothing runs while no hold exists: the table is a map, and a ttl is one
// timer per hold.

// EventHoldChanged says a hold was taken, renewed or released. Hold carries
// the hold's row. Action is taken, renewed or released, and on released
// Reason says why: released, ttl, pane_closed or forced. It names no session.
const EventHoldChanged = "hold-changed"

// ErrVerbHeld reports a hold another holder has: take-hold and check-hold on
// a held name, and release-hold of another holder's hold without force.
const ErrVerbHeld = "held"

// Hold actions and release reasons, as hold-changed carries them.
const (
	holdTaken    = "taken"
	holdRenewed  = "renewed"
	holdReleased = "released"

	holdReasonReleased   = "released"
	holdReasonTTL        = "ttl"
	holdReasonPaneClosed = "pane_closed"
	holdReasonForced     = "forced"
)

// Bounds on the hold table.
const (
	holdMaxCount  = 256
	holdMaxReason = 200
	holdMaxTTL    = 7 * 24 * time.Hour
)

// holdNameRE is a hold name: a word a shell reads unquoted.
var holdNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// holdOwner is who holds a hold. Exactly one field names it: a pane by its
// window id, a linked machine by its peer name, a plugin by its id, or a
// caller on this machine in no pane. Every caller in no pane is one owner,
// so a hold taken from one shell is released from another.
type holdOwner struct {
	window  string
	link    string
	plugin  string
	outside bool
	// pid is the process that took the hold, for the row only.
	pid int
}

func (o holdOwner) same(p holdOwner) bool {
	return o.window == p.window && o.link == p.link && o.plugin == p.plugin && o.outside == p.outside
}

type holdEntry struct {
	name    string
	reason  string
	owner   holdOwner
	takenAt time.Time
	// expires is zero for a hold with no ttl.
	expires time.Time
	timer   *time.Timer
	// gen tells a ttl timer of an earlier take from the current one.
	gen uint64
}

// holdTable is the daemon's holds. Its zero value is empty and ready.
type holdTable struct {
	mu    sync.Mutex
	holds map[string]*holdEntry
	gen   uint64
}

// HoldRow is one hold as list-holds, check-hold and hold-changed give it.
type HoldRow struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Holder is the holding pane's window id, empty for a holder in no pane.
	Holder        string `json:"holder,omitempty"`
	HolderName    string `json:"holder_name,omitempty"`
	HolderSession string `json:"holder_session,omitempty"`
	// HolderHost is the linked machine that took the hold over a link.
	HolderHost   string `json:"holder_host,omitempty"`
	HolderPlugin string `json:"holder_plugin,omitempty"`
	HolderPID    int    `json:"holder_pid,omitempty"`
	// Label says who holds it in words, for a person.
	Label   string `json:"label"`
	TakenAt int64  `json:"taken_at"`
	// ExpiresAt is when the ttl ends, Unix nanoseconds, 0 for no ttl.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

func holdVerbs() map[string]verbEntry {
	name := verbParam{Name: "name", Type: "string", Required: true}
	row := []verbParam{
		{Name: "hold", Type: "object"},
	}
	return map[string]verbEntry{
		"take-hold": {
			// The same grant as send-agent-message: it leaves a record as
			// the caller and types nothing (checkGrants).
			scope: scopeMail,
			link:  needMail,
			params: []verbParam{
				name,
				{Name: "reason", Type: "string", Required: true},
				{Name: "ttl", Type: "int"},
			},
			returns: append([]verbParam{
				{Name: "renewed", Type: "bool"},
			}, row...),
			handler: (*Daemon).verbTakeHold,
		},
		"release-hold": {
			scope: scopeMail,
			link:  needMail,
			params: []verbParam{
				name,
				{Name: "force", Type: "bool", Default: "false"},
			},
			returns: append([]verbParam{
				{Name: "released", Type: "bool"},
			}, row...),
			handler: (*Daemon).verbReleaseHold,
		},
		"check-hold": {
			scope: scopeOpen,
			link:  needList,
			params: []verbParam{
				name,
			},
			returns: []verbParam{
				{Name: "name", Type: "string"},
				{Name: "held", Type: "bool"},
			},
			handler: (*Daemon).verbCheckHold,
		},
		"list-holds": {
			scope: scopeOpen,
			link:  needList,
			returns: []verbParam{
				{Name: "holds", Type: "[]object"},
				{Name: "total", Type: "int"},
			},
			handler: (*Daemon).verbListHolds,
		},
	}
}

// holdName checks a name a hold verb was given.
func holdName(raw string) (string, *verbError) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", invalidParam("name", "name is required")
	}
	if !holdNameRE.MatchString(name) {
		return "", invalidParam("name", "a hold name is 1 to 64 letters, digits and . _ : -, and starts with a letter or a digit, not "+echoName(raw))
	}
	return name, nil
}

// holdCaller is who calls on cs, as a hold owner.
func (d *Daemon) holdCaller(cs *connState) (holdOwner, *verbError) {
	if cs == nil {
		return holdOwner{outside: true}, nil
	}
	if cs.viaLink {
		return holdOwner{link: cmp.Or(cs.linkPeer, "unknown"), pid: cs.peerPID}, nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		return holdOwner{outside: true, pid: cs.peerPID}, nil
	}
	if pa.plugin != "" {
		return holdOwner{plugin: pa.plugin, pid: cs.peerPID}, nil
	}
	ref := d.callerPaneRef(cs)
	if ref == nil {
		return holdOwner{}, newVerbError(ErrVerbForbidden, "the daemon cannot tell which pane makes this call, so it cannot take or release a hold for it. Nothing changed. Run the command again from the pane's own shell")
	}
	return holdOwner{window: ref.Window, pid: cs.peerPID}, nil
}

// holdAdmin reports whether the caller on cs may release another holder's
// hold: a pane with the admin grant, or a caller on this machine in no pane
// whose connection is not restricted.
func (d *Daemon) holdAdmin(cs *connState) bool {
	if cs == nil {
		return true
	}
	if cs.viaLink {
		return false
	}
	if pa := d.paneAuthority(cs); pa != nil {
		return pa.grants.Has(GrantAdmin)
	}
	return cs.scope.Load() == nil
}

// holdRow is the row of e. It reads the holder pane's session, so it is not
// called with a session's state lock held.
func (d *Daemon) holdRow(e *holdEntry) HoldRow {
	row := HoldRow{
		Name:         e.name,
		Reason:       e.reason,
		Holder:       e.owner.window,
		HolderHost:   e.owner.link,
		HolderPlugin: e.owner.plugin,
		HolderPID:    e.owner.pid,
		TakenAt:      e.takenAt.UnixNano(),
	}
	if !e.expires.IsZero() {
		row.ExpiresAt = e.expires.UnixNano()
	}
	switch {
	case e.owner.window != "":
		row.Label = "pane " + shortWindowID(e.owner.window)
		if sess := d.sessionHoldingWindow(e.owner.window); sess != nil {
			row.HolderSession = sess.Name()
			st := sess.GetState()
			if i, err := findWindowStateIndex(st.Windows, e.owner.window); err == nil {
				row.HolderName = windowLabelOf(st.Windows[i])
			}
			if row.HolderName != "" {
				row.Label = "pane " + row.HolderName + " (" + shortWindowID(e.owner.window) + ")"
			}
			row.Label += " in session " + row.HolderSession
		}
	case e.owner.link != "":
		row.Label = "machine " + e.owner.link + " over a link"
	case e.owner.plugin != "":
		row.Label = "plugin " + e.owner.plugin
	default:
		row.Label = "a process in no pane"
		if e.owner.pid > 0 {
			row.Label += " (pid " + strconv.Itoa(e.owner.pid) + ")"
		}
	}
	return row
}

// heldError is the held refusal for e: who holds it, why, and for how long.
func (d *Daemon) heldError(e *holdEntry, now time.Time, detail string) *verbError {
	row := d.holdRow(e)
	msg := e.name + " is held by " + row.Label + ". It was taken " + holdSpan(now.Sub(e.takenAt)) + " ago. Reason: " + e.reason + "."
	if !e.expires.IsZero() {
		msg += " Its ttl ends in " + holdSpan(e.expires.Sub(now)) + "."
	}
	return hintedVerbError(ErrVerbHeld, msg, &VerbHint{
		Command: "tuios wait-for hold-released " + e.name,
		Detail:  detail,
	})
}

// holdSpan is a duration for a person: whole seconds.
func holdSpan(d time.Duration) string {
	return max(d, 0).Round(time.Second).String()
}

func (d *Daemon) publishHold(action, reason string, row HoldRow) {
	d.events.publish(streamEvent{Type: EventHoldChanged, Action: action, Reason: reason, Hold: &row})
}

// verbTakeHold answers take-hold.
func (d *Daemon) verbTakeHold(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
		TTL    int64  `json:"ttl"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	name, verr := holdName(p.Name)
	if verr != nil {
		return nil, verr
	}
	reason := strings.Join(strings.Fields(p.Reason), " ")
	if reason == "" {
		return nil, invalidParam("reason", "reason is required: it tells the panes that wait what the hold is for")
	}
	if utf8.RuneCountInString(reason) > holdMaxReason {
		return nil, invalidParam("reason", "reason is at most "+strconv.Itoa(holdMaxReason)+" characters")
	}
	if p.TTL < 0 || time.Duration(p.TTL)*time.Millisecond > holdMaxTTL {
		return nil, invalidParam("ttl", "ttl is milliseconds from 1 to "+strconv.FormatInt(holdMaxTTL.Milliseconds(), 10)+" (7 days)")
	}
	ttl := time.Duration(p.TTL) * time.Millisecond
	owner, verr := d.holdCaller(cs)
	if verr != nil {
		return nil, verr
	}
	if owner.window == "" && ttl == 0 {
		return nil, invalidParam("ttl", "a hold taken from outside a pane needs a ttl: no pane closes to end it. Pass ttl in milliseconds")
	}

	now := time.Now()
	h := &d.holds
	h.mu.Lock()
	e := h.holds[name]
	if e != nil && !e.owner.same(owner) {
		other := *e
		h.mu.Unlock()
		return nil, d.heldError(&other, now, "Wait for it to be released, or ask its holder.")
	}
	if e == nil && len(h.holds) >= holdMaxCount {
		h.mu.Unlock()
		return nil, newVerbError(ErrVerbInvalidParams, "the daemon keeps at most "+strconv.Itoa(holdMaxCount)+" holds. Release one first")
	}
	renewed := e != nil
	if e == nil {
		if h.holds == nil {
			h.holds = map[string]*holdEntry{}
		}
		e = &holdEntry{name: name, takenAt: now}
		h.holds[name] = e
	}
	h.gen++
	e.gen = h.gen
	e.reason = reason
	e.owner = owner
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	e.expires = time.Time{}
	if ttl > 0 {
		e.expires = now.Add(ttl)
		gen := e.gen
		e.timer = time.AfterFunc(ttl, func() { d.endHold(name, gen, holdReasonTTL) })
	}
	snap := *e
	h.mu.Unlock()

	row := d.holdRow(&snap)
	action := holdTaken
	if renewed {
		action = holdRenewed
	}
	LogBasic("Hold %s %s by %s: %s", name, action, row.Label, reason)
	d.publishHold(action, "", row)
	return map[string]any{"type": "hold", "renewed": renewed, "hold": row}, nil
}

// endHold drops the hold name when it is still the take gen names, and
// announces it. A ttl timer and a closed pane end a hold this way.
func (d *Daemon) endHold(name string, gen uint64, reason string) {
	h := &d.holds
	h.mu.Lock()
	e := h.holds[name]
	if e == nil || e.gen != gen {
		h.mu.Unlock()
		return
	}
	delete(h.holds, name)
	if e.timer != nil {
		e.timer.Stop()
	}
	snap := *e
	h.mu.Unlock()
	row := d.holdRow(&snap)
	LogBasic("Hold %s released (%s), held by %s", name, reason, row.Label)
	d.publishHold(holdReleased, reason, row)
}

// verbReleaseHold answers release-hold. A name no one holds is not an error:
// a script that releases on its way out finds the ttl may have ended first.
func (d *Daemon) verbReleaseHold(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	name, verr := holdName(p.Name)
	if verr != nil {
		return nil, verr
	}
	owner, verr := d.holdCaller(cs)
	if verr != nil {
		return nil, verr
	}
	admin := d.holdAdmin(cs)
	h := &d.holds
	h.mu.Lock()
	e := h.holds[name]
	if e == nil {
		h.mu.Unlock()
		return map[string]any{"type": "hold_release", "released": false, "name": name}, nil
	}
	reason := holdReasonReleased
	if !e.owner.same(owner) {
		if !admin {
			h.mu.Unlock()
			return nil, hintedVerbError(ErrVerbForbidden, "release-hold of "+name+" is refused: another holder has it, and releasing it needs the admin grant", &VerbHint{
				Detail: "Nothing changed. Ask its holder to release it, or wait for it with tuios wait-for hold-released " + name + ".",
			})
		}
		if !p.Force {
			other := *e
			h.mu.Unlock()
			return nil, d.heldError(&other, time.Now(), "Nothing changed. To release another holder's hold, pass force.")
		}
		reason = holdReasonForced
	}
	delete(h.holds, name)
	if e.timer != nil {
		e.timer.Stop()
	}
	snap := *e
	h.mu.Unlock()
	row := d.holdRow(&snap)
	LogBasic("Hold %s released (%s), held by %s", name, reason, row.Label)
	d.publishHold(holdReleased, reason, row)
	return map[string]any{"type": "hold_release", "released": true, "name": name, "hold": row}, nil
}

// verbCheckHold answers check-hold: free is a result, held is the held error,
// so a script that runs "tuios hold check NAME" stops on a held name.
func (d *Daemon) verbCheckHold(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	name, verr := holdName(p.Name)
	if verr != nil {
		return nil, verr
	}
	if e, ok := d.holds.get(name); ok {
		return nil, d.heldError(&e, time.Now(), "Wait for it with tuios wait-for hold-released "+name+", then run again.")
	}
	return map[string]any{"type": "hold_check", "name": name, "held": false}, nil
}

// verbListHolds answers list-holds.
func (d *Daemon) verbListHolds(_ *connState, _ json.RawMessage) (any, *verbError) {
	rows := d.holdRows()
	return map[string]any{"type": "hold_list", "holds": rows, "total": len(rows)}, nil
}

// addHolds puts the holds on a list-agents answer, when there are any, so
// the panes that list their peers also see what the machine is held for.
func (d *Daemon) addHolds(out map[string]any) {
	if rows := d.holdRows(); len(rows) > 0 {
		out["holds"] = rows
	}
}

// get is a copy of the hold name, when it is held.
func (h *holdTable) get(name string) (holdEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.holds[name]
	if e == nil {
		return holdEntry{}, false
	}
	return *e, true
}

// holdRows are the holds, oldest first.
func (d *Daemon) holdRows() []HoldRow {
	h := &d.holds
	h.mu.Lock()
	snaps := make([]holdEntry, 0, len(h.holds))
	for _, e := range h.holds {
		snaps = append(snaps, *e)
	}
	h.mu.Unlock()
	slices.SortFunc(snaps, func(a, b holdEntry) int {
		return cmp.Or(a.takenAt.Compare(b.takenAt), strings.Compare(a.name, b.name))
	})
	rows := make([]HoldRow, 0, len(snaps))
	for i := range snaps {
		rows = append(rows, d.holdRow(&snaps[i]))
	}
	return rows
}

// noteHoldPaneClosed ends the holds of a pane that closed. It runs from the
// session's event sink, which holds that session's state lock, so the check
// runs on its own goroutine. A pane moved to another session raises
// window-closed in the one it left and goes on running: the check waits for
// any move in flight and keeps the holds of a window a session still has.
func (d *Daemon) noteHoldPaneClosed(window string) {
	h := &d.holds
	h.mu.Lock()
	var held []holdEntry
	for _, e := range h.holds {
		if e.owner.window == window {
			held = append(held, *e)
		}
	}
	h.mu.Unlock()
	if len(held) == 0 {
		return
	}
	go func() {
		windowMove.Lock()
		kept := d.sessionHoldingWindow(window) != nil
		windowMove.Unlock()
		if kept {
			return
		}
		for _, e := range held {
			d.endHold(e.name, e.gen, holdReasonPaneClosed)
		}
	}()
}

// waitHoldReleased resolves when no one holds name. It subscribes before it
// looks, so a release between the two is not missed.
func (d *Daemon) waitHoldReleased(raw string, deadline <-chan time.Time) (any, *verbError) {
	name, verr := holdName(raw)
	if verr != nil {
		return nil, verr
	}
	sub := d.events.subscribe(eventFilter{types: map[string]bool{EventHoldChanged: true}}, defaultEventQueue)
	defer d.events.unsubscribe(sub)
	e, held := d.holds.get(name)
	if !held {
		return waitMatched("hold-released", map[string]any{"name": name, "was_held": false}), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for hold "+name+" to be released", &VerbHint{
				Param:  "timeout",
				Verb:   "list-holds",
				Detail: "It is held by " + d.holdRow(&e).Label + ": " + e.reason + ". Raise timeout (milliseconds), or ask its holder.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if e, held = d.holds.get(name); !held {
				return waitMatched("hold-released", map[string]any{"name": name, "was_held": true}), nil
			}
		}
	}
}

// handoffHold is one hold, carried across a live upgrade, so a quiet window
// does not end because the daemon was replaced.
type handoffHold struct {
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Window  string `json:"window,omitempty"`
	Link    string `json:"link,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
	Outside bool   `json:"outside,omitempty"`
	PID     int    `json:"pid,omitempty"`
	TakenAt int64  `json:"taken_at"`
	Expires int64  `json:"expires,omitempty"`
}

func (h *holdTable) handoffState() []handoffHold {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]handoffHold, 0, len(h.holds))
	for _, e := range h.holds {
		r := handoffHold{
			Name: e.name, Reason: e.reason, Window: e.owner.window, Link: e.owner.link,
			Plugin: e.owner.plugin, Outside: e.owner.outside, PID: e.owner.pid, TakenAt: e.takenAt.UnixNano(),
		}
		if !e.expires.IsZero() {
			r.Expires = e.expires.UnixNano()
		}
		out = append(out, r)
	}
	return out
}

// restoreHolds takes the holds a live upgrade carried. A hold whose ttl ended
// during the upgrade is dropped, and one of a pane that did not come across
// is ended once the panes are in (checkRestoredHolds).
func (d *Daemon) restoreHolds(in []handoffHold) {
	now := time.Now()
	h := &d.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range in {
		if _, verr := holdName(r.Name); verr != nil || len(h.holds) >= holdMaxCount {
			continue
		}
		e := &holdEntry{
			name: r.Name, reason: r.Reason, takenAt: time.Unix(0, r.TakenAt),
			owner: holdOwner{window: r.Window, link: r.Link, plugin: r.Plugin, outside: r.Outside, pid: r.PID},
		}
		h.gen++
		e.gen = h.gen
		if r.Expires != 0 {
			e.expires = time.Unix(0, r.Expires)
			left := e.expires.Sub(now)
			if left <= 0 {
				continue
			}
			name, gen := e.name, e.gen
			e.timer = time.AfterFunc(left, func() { d.endHold(name, gen, holdReasonTTL) })
		}
		if h.holds == nil {
			h.holds = map[string]*holdEntry{}
		}
		h.holds[e.name] = e
	}
}

// checkRestoredHolds ends the carried holds of panes no session has.
func (d *Daemon) checkRestoredHolds() {
	h := &d.holds
	h.mu.Lock()
	var gone []holdEntry
	for _, e := range h.holds {
		if e.owner.window != "" {
			gone = append(gone, *e)
		}
	}
	h.mu.Unlock()
	for _, e := range gone {
		if d.sessionHoldingWindow(e.owner.window) == nil {
			d.endHold(e.name, e.gen, holdReasonPaneClosed)
		}
	}
}
