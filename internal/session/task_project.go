package session

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// A board per project: task cards that name a repository.
//
// A card is still kept by the session that made it, on the daemon that made
// it, which owns it. A card with a project names its repository by the
// normalised origin URL (worktree.NormalizeRemote), the one name a repository
// has on every machine. list-tasks with project lists such cards from every
// session of this daemon, and with hosts also asks each linked machine for its
// own. A worker on another machine claims, releases and finishes a card over
// the link: its daemon checks its own pane, then calls the owner with
// remote_window, and the owner records the holder as that window on the
// link's peer, a name it takes from the link and never from a param.
//
// A call that came over a link answers with this daemon's cards only, never
// asking its own links: a card is listed by its owner alone, so rows cannot
// loop or double between two machines that link each other.

// remoteHolderPrefix starts a holder that is a pane on another machine:
// "@host/window". A local window id never starts with it.
const remoteHolderPrefix = "@"

// remoteHolder is the holder key of window on host.
func remoteHolder(host, window string) string {
	return remoteHolderPrefix + host + "/" + window
}

// splitRemoteHolder is the host and window of a remote holder key.
func splitRemoteHolder(holder string) (host, window string, ok bool) {
	rest, ok := strings.CutPrefix(holder, remoteHolderPrefix)
	if !ok {
		return "", "", false
	}
	host, window, ok = strings.Cut(rest, "/")
	return host, window, ok && host != "" && window != ""
}

// holderLabel is a holder key as a person reads it: a window id shortened,
// or "a pane on HOST" for a holder on another machine.
func holderLabel(holder string) string {
	if host, window, ok := splitRemoteHolder(holder); ok {
		return shortWindowID(window) + " on " + host
	}
	return shortWindowID(holder)
}

// projectParam is the project a verb names, normalised. A local path names
// no repository on another machine, so it is refused.
func projectParam(param, raw string) (string, *verbError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	p := worktree.NormalizeRemote(raw)
	if strings.HasPrefix(p, "file:") && isNormalisedProject(raw) {
		// The form a row carries, host/path, as a linked machine sends it
		// back.
		p = worktree.NormalizeRemote("https://" + raw)
	}
	if p == "" || strings.HasPrefix(p, "file:") {
		return "", hintedVerbError(ErrVerbInvalidParams, param+" "+echoName(raw)+" is not a repository's remote URL", &VerbHint{
			Param:  param,
			Detail: "Nothing changed. A project is the origin URL of a repository, such as git@github.com:acme/api.git, the name it has on every machine. Leave it out for a card of the session only.",
		})
	}
	if len(p) > 512 {
		return "", invalidParam(param, param+" is longer than 512 bytes")
	}
	return p, nil
}

// isNormalisedProject reports whether raw reads as a project in its
// normalised form, host/path: no scheme, no colon, not a local path, and a
// host with a dot in it before the first slash.
func isNormalisedProject(raw string) bool {
	if strings.ContainsAny(raw, ":\\ ") || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "~") {
		return false
	}
	host, path, ok := strings.Cut(raw, "/")
	return ok && path != "" && strings.Contains(host, ".")
}

// remoteClaim is what a worker's daemon sends the owner with a claim,
// release or finish over the link: the window on the worker's machine, and
// how it reads there.
type remoteClaim struct {
	RemoteWindow  string `json:"remote_window"`
	RemoteName    string `json:"remote_name"`
	RemoteHarness string `json:"remote_harness"`
}

// remoteCaller is the caller of a task verb that came over the link for a
// window of the peer: it holds and changes only that window's cards.
func remoteCaller(cs *connState, verb string, rc remoteClaim) (taskCaller, string, *verbError) {
	if rc.RemoteWindow == "" {
		return taskCaller{}, "", nil
	}
	if cs == nil || !cs.viaLink {
		return taskCaller{}, "", invalidParam("remote_window", "remote_window is for a machine's daemon calling over the link. Leave it out")
	}
	cs.mu.Lock()
	peer := cs.linkPeer
	cs.mu.Unlock()
	if peer == "" || strings.ContainsAny(rc.RemoteWindow, "/@") || len(rc.RemoteWindow) > 128 {
		return taskCaller{}, "", invalidParam("remote_window", verb+" over the link needs a named peer and one window id")
	}
	h := remoteHolder(peer, rc.RemoteWindow)
	return taskCaller{by: queueByLinkPrefix + peer, window: h}, h, nil
}

// remoteHold is a card this daemon's pane holds on another machine, or with
// host empty in another session of this daemon (start-agent task_session),
// which the pane's own session does not let go.
type remoteHold struct {
	host, session, id string
}

// remoteHoldSet is the cards each local window holds on other machines, so
// the window's close lets them go. The daemon keeps one (Daemon.remoteHolds).
type remoteHoldSet struct {
	mu sync.Mutex
	m  map[string][]remoteHold
}

// note records that window holds h, or with held false that it no longer
// does.
func (rs *remoteHoldSet) note(window string, h remoteHold, held bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.m == nil {
		rs.m = map[string][]remoteHold{}
	}
	list := rs.m[window]
	out := list[:0]
	for _, x := range list {
		if x != h {
			out = append(out, x)
		}
	}
	if held {
		out = append(out, h)
	}
	if len(out) == 0 {
		delete(rs.m, window)
		return
	}
	rs.m[window] = out
}

// far is every hold on another machine, for the handoff manifest.
func (rs *remoteHoldSet) far() []handoffFarHold {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []handoffFarHold
	for window, list := range rs.m {
		for _, h := range list {
			if h.host != "" {
				out = append(out, handoffFarHold{Window: window, Host: h.host, Session: h.session, ID: h.id})
			}
		}
	}
	return out
}

// rebuildTaskHolds notes again, from the cards, what only memory kept beside
// them: each card a pane of another local session holds (remoteHolds), and
// each card auto runs, which its cap counts. A live upgrade keeps every
// holder but not that memory. A cold start lets every holder go, so it
// finds nothing. The holds on other machines come from the handoff manifest.
func (d *Daemon) rebuildTaskHolds() {
	far := d.farHosts()
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if st == nil {
			continue
		}
		for _, tk := range st.Tasks {
			if tk.Holder == "" {
				continue
			}
			peer, _, remote := splitRemoteHolder(tk.Holder)
			if !remote {
				if other := d.sessionOfWindow(tk.Holder); other != "" && other != sess.Name() {
					d.remoteHolds.note(tk.Holder, remoteHold{session: sess.Name(), id: tk.ID}, true)
				}
			}
			board := dispatchBoardKey(sess.Name(), tk.Project)
			if !tk.Dispatched || tk.Stage == TaskStageDone || !d.dispatch.isMode(board, DispatchAuto) {
				continue
			}
			// A far holder is named by the name its machine gives itself;
			// the cap per machine counts by the [hosts] name.
			host := peer
			if remote && !slices.Contains(far, peer) {
				if h, ok := d.verifyPeers.Load(peer); ok {
					host, _ = h.(string)
				}
			}
			d.auto.noteRun(autoKey(sess.Name(), tk.ID), autoRun{board: board, host: host})
		}
	}
}

// take removes and returns what window holds.
func (rs *remoteHoldSet) take(window string) []remoteHold {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	list := rs.m[window]
	delete(rs.m, window)
	return list
}

// releaseRemoteHolds lets go, on their owners, the cards window held on other
// machines. It is best effort: a link that is down leaves the card held,
// and tuios task release on either machine frees it.
func (d *Daemon) releaseRemoteHolds(window string) {
	list := d.remoteHolds.take(window)
	for _, h := range list {
		if h.host == "" {
			if sess := d.manager.GetSession(h.session); sess != nil {
				d.releaseHeld(sess, window, taskReleasePaneClosed)
			}
			continue
		}
		if d.federation == nil {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(d.ctx, 10*time.Second)
			defer cancel()
			if _, err := d.federation.Call(ctx, h.host, "release-task", map[string]any{"session": h.session, "id": h.id, "remote_window": window}); err != nil {
				LogBasic("Warning: could not release task %s on %s after its pane closed: %v", h.id, h.host, err)
			}
		}()
	}
}

// releaseHeld lets go, for reason, the cards of sess that window holds, here
// or on another machine.
func (d *Daemon) releaseHeld(sess *Session, window, reason string) {
	if window == "" {
		return
	}
	var rows []TaskRow
	_ = sess.mutateState(func(st *SessionState) error {
		rel := releaseHolders(sess, st, func(holder string) bool { return holder == window || strings.HasSuffix(holder, "/"+window) }, reason, time.Now().UnixNano())
		for i := range rel {
			rows = append(rows, taskRow(sess, st, &rel[i]))
		}
		if len(rel) == 0 {
			return errNoTaskMove
		}
		return nil
	})
	d.publishReleased(sess, rows, reason)
}

// cardSessions are the sessions whose cards window may hold: its own, and
// those it holds a card of from another session of this daemon.
func (d *Daemon) cardSessions(sess *Session, window string) []*Session {
	out := []*Session{sess}
	d.remoteHolds.mu.Lock()
	list := slices.Clone(d.remoteHolds.m[window])
	d.remoteHolds.mu.Unlock()
	for _, h := range list {
		if other := d.manager.GetSession(h.session); h.host == "" && other != nil && !slices.Contains(out, other) {
			out = append(out, other)
		}
	}
	return out
}

// forwardTask sends a claim, release or finish for a card owned by host, on
// behalf of the calling pane, and notes the hold so the pane's close lets it
// go.
func (d *Daemon) forwardTask(cs *connState, verb, host, sessionName, id string) (any, *verbError) {
	if cs != nil && cs.viaLink {
		return nil, invalidParam("host", "a call that came over the link is not passed on again")
	}
	pa := d.paneAuthority(cs)
	if pa == nil || !pa.inPane() || pa.hosted || pa.window == unplacedWindow {
		return nil, hintedVerbError(ErrVerbInvalidParams, verb+" with host is for a pane: the pane on this machine that holds the card", &VerbHint{
			Param:  "host",
			Detail: "Nothing changed. Run it in the agent's pane, or let the person change the card on " + echoName(host) + ".",
		})
	}
	return d.forwardTaskFor(pa.session, pa.window, verb, host, sessionName, id)
}

// forwardTaskFor is forwardTask for window, a pane of paneSession on this
// machine: start-agent uses it for the pane it opened.
func (d *Daemon) forwardTaskFor(paneSession, window, verb, host, sessionName, id string) (any, *verbError) {
	if sessionName == "" {
		return nil, invalidParam("session", verb+" with host names the card's session on "+echoName(host)+": list-tasks with project and hosts gives it")
	}
	if d.federation == nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "host "+echoName(host)+" is not a linked machine: this daemon has no [hosts]", &VerbHint{
			Param:  "host",
			Detail: "Nothing changed. tuios hosts ls lists the machines this daemon links to.",
		})
	}
	params := map[string]any{"session": sessionName, "id": id, "remote_window": window}
	if verb == "claim-task" {
		if sess := d.manager.GetSession(paneSession); sess != nil {
			if w, ok := findWindowState(sess.GetState(), window); ok {
				params["remote_name"], params["remote_harness"] = windowLabelOf(w), w.AgentHarness
			}
		}
	}
	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	raw, err := d.federation.Call(ctx, host, verb, params)
	if err != nil {
		return nil, federationTaskErr(verb, host, err)
	}
	var row TaskRow
	if err := jsonx.Unmarshal(raw, &row); err != nil {
		return nil, newVerbError(ErrVerbInternal, "read "+host+"'s answer: "+err.Error())
	}
	row.Host = host
	h := remoteHold{host: host, session: firstNonEmpty(row.Session, sessionName), id: firstNonEmpty(row.ID, id)}
	d.remoteHolds.note(window, h, verb == "claim-task")
	return row, nil
}

// federationTaskErr is the refusal of a task verb passed on to host that
// failed there or on the way.
func federationTaskErr(verb, host string, err error) *verbError {
	if rerr, ok := errors.AsType[*federation.RemoteError](err); ok {
		// A daemon from before remote_window reads the call as one from
		// outside every pane, and asks for a window.
		if strings.Contains(rerr.Message, "window is required") {
			return newVerbError(ErrVerbInvalidParams, host+" runs a tuios that cannot hand its cards to another machine. Update tuios there")
		}
		// host answered and refused: its words, with its code.
		return &verbError{Code: rerr.Code, Message: "on " + host + ": " + rerr.Message}
	}
	if _, ok := errors.AsType[*federation.RefusedError](err); ok {
		return newVerbError(ErrVerbForbidden, verb+" on "+host+" is refused by the link: "+err.Error())
	}
	return hintedVerbError(ErrVerbInternal, verb+" on "+echoName(host)+" did not answer: "+err.Error(), &VerbHint{
		Param:  "host",
		Detail: "The change may or may not have been made there. Run it again: a claim by the same pane is taken once, and an attach with the same name is refused or, with replace, made once.",
	})
}

// projectRows lists the cards of project from every session of this daemon.
func (d *Daemon) projectRows(p listTasksParams) []TaskRow {
	rows := []TaskRow{}
	for _, sess := range d.manager.AllSessions() {
		for _, r := range listedTasks(sess, sess.GetState(), p, "") {
			if r.Project == p.Project {
				rows = append(rows, r)
			}
		}
	}
	return rows
}

// hostTaskRows asks every linked machine for its own cards of project, or
// with no project for every card of every session, at once. The caller keeps
// the project cards only, so a machine from before projects costs nothing. A machine that does not answer is named in down, and costs the
// listing its rows only.
func (d *Daemon) hostTaskRows(p listTasksParams) (rows []TaskRow, down []map[string]string) {
	if d.federation == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	reports := d.federation.Reports(ctx)
	got := make([][]TaskRow, len(reports))
	errs := make([]string, len(reports))
	var wg sync.WaitGroup
	for i, r := range reports {
		if r.Status != federation.StatusUp {
			errs[i] = string(r.Status)
			continue
		}
		wg.Go(func() {
			params := map[string]any{"all_sessions": true, "open": p.Open}
			if p.Project != "" {
				params["project"] = p.Project
			}
			if p.Stage != "" {
				params["stage"] = p.Stage
			}
			raw, err := d.federation.Call(ctx, r.Host, "list-tasks", params)
			if err != nil {
				errs[i] = err.Error()
				return
			}
			var res struct {
				Tasks []TaskRow `json:"tasks"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				errs[i] = err.Error()
				return
			}
			for j := range res.Tasks {
				res.Tasks[j].Host = r.Host
			}
			got[i] = res.Tasks
		})
	}
	wg.Wait()
	for i, r := range reports {
		rows = append(rows, got[i]...)
		if errs[i] != "" {
			down = append(down, map[string]string{"host": r.Host, "error": errs[i]})
		}
	}
	return rows, down
}

// claimForRemote is claim-task over the link for a pane of the peer: the
// card is held by "@peer/window". A claim the same pane made before is
// taken again without a refusal, so a worker whose answer was lost to a
// dropped link can retry.
func (d *Daemon) claimForRemote(cs *connState, p claimTaskParams) (any, *verbError) {
	caller, holder, verr := remoteCaller(cs, "claim-task", p.remoteClaim)
	if verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	var row TaskRow
	err := sess.mutateState(func(st *SessionState) error {
		i := taskIndex(st.Tasks, p.ID)
		if i < 0 {
			verr = noTask(sess, p.ID)
			return errTaskRefused
		}
		tk := &st.Tasks[i]
		if tk.Project == "" {
			verr = hintedVerbError(ErrVerbForbidden, "task card "+tk.ID+" belongs to session "+echoName(sess.Name())+" only, and another machine takes only a project's cards", &VerbHint{
				Detail: "Nothing was claimed. Add the card with a project for workers on other machines to take it.",
			})
			return errTaskRefused
		}
		// A claim for a card auto is starting on another machine is auto's.
		auto := d.auto.expects(sess.Name(), tk.ID, true)
		if _, _, verr = sess.moveCard(tk, cardMove{kind: moveClaim, by: cmp.Or(auto, caller.by), window: holder, dispatched: auto != "",
			names: true, name: printableClaim(p.RemoteName, 64), harness: printableClaim(p.RemoteHarness, 64), refresh: true}, time.Now().UnixNano()); verr != nil {
			return errTaskRefused
		}
		row = taskRow(sess, st, tk)
		return nil
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not claim the card: "+err.Error())
	}
	LogBasic("A pane on %s claimed task %s in session %s", strings.TrimPrefix(caller.by, queueByLinkPrefix), row.ID, sess.Name())
	d.publishTask(sess, EventTaskClaimed, TaskActionClaimed, holder, "", row)
	return row.typed("task_claimed"), nil
}
