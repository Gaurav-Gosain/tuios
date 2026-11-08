package session

import (
	"cmp"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Auto dispatch: the daemon starts an agent for each open card.
//
// A board in auto mode has a preset: the agent, its model, effort and
// permission mode, and the machine (this one unless the person picked any or
// a linked one). For each open card (todo or plan, held by no pane, not
// blocked, not ask_first), oldest first, the daemon picks the machine as
// place-agent does, makes a worktree there on a new branch named from the
// card's title, and starts the agent with start-agent, which claims the card
// for the new pane and mails it the card. Nothing is typed into the pane.
//
// Bounds: at most cap agents at once in total, and machine_cap on one
// machine, starts in flight included. Each machine's own [tasks] machine_cap
// still gates it in placement. A card in doing is never started on its own:
// one let go mid-work (its pane closed, a restart) waits for a person, so a
// restart never starts a second agent beside one that came back.
//
// Every switch of pull holds: pause, quiet hours, a board stopped after 3
// failures in a row (a failed start counts one), and a usage limit. A preset
// whose agent no machine has stops the board at once. Auto never answers an
// approval.
//
// One goroutine decides every start, woken by a kick: a task event, a change
// of the settings, the end of quiet hours. Nothing polls.

// autoState is auto's state on this daemon.
type autoState struct {
	once sync.Once
	kick chan struct{}

	mu sync.Mutex
	// runs are the cards auto starts or started and that are still held,
	// by autoKey.
	runs map[string]autoRun
	// waiting and note are each auto board's cards that wait, and why.
	waiting map[string]int
	note    map[string]string
	quiet   *time.Timer
}

// autoRun is one card auto starts (starting) or started.
type autoRun struct {
	board, host string
	starting    bool
}

// autoCard is one open card of an auto board.
type autoCard struct {
	sess                                  *Session
	board, id, title, project, harness, m string
	at                                    int64
}

// autoKey is a card's key in runs.
func autoKey(session, id string) string { return session + "\x00" + id }

// expects is the by of a claim of card id in session that auto is making, or
// "" when it makes none. remote says the claim came over a link.
func (a *autoState) expects(session, id string, remote bool) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r, ok := a.runs[autoKey(session, id)]; ok && r.starting && (r.host != "") == remote {
		return taskByDispatch
	}
	return ""
}

// kickAuto wakes the auto loop when a board is in auto mode. It never
// blocks.
func (d *Daemon) kickAuto() {
	if !d.dispatch.anyMode(DispatchAuto) {
		return
	}
	a := &d.auto
	a.once.Do(func() {
		a.kick = make(chan struct{}, 1)
		go func() {
			for {
				select {
				case <-d.ctx.Done():
					return
				case <-a.kick:
					d.autoPass()
				}
			}
		}()
	})
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// autoPass starts what the auto boards have room for.
func (d *Daemon) autoPass() {
	boards := map[string]DispatchPreset{}
	d.dispatch.mu.Lock()
	for k, b := range d.dispatch.f.Boards {
		if b.Mode == DispatchAuto && b.Preset != nil {
			boards[k] = *b.Preset
		}
	}
	capTotal, capMachine := d.dispatch.capsLocked()
	d.dispatch.mu.Unlock()

	open := map[string][]autoCard{}
	held := map[string]bool{}
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if st == nil {
			continue
		}
		for _, tk := range st.Tasks {
			board := dispatchBoardKey(sess.Name(), tk.Project)
			if _, ok := boards[board]; !ok {
				continue
			}
			if tk.Holder != "" && tk.Dispatched && tk.Stage != TaskStageDone {
				held[autoKey(sess.Name(), tk.ID)] = true
			} else if tk.Holder == "" && tk.Blocked == nil && !tk.AskFirst && (tk.Stage == TaskStageTodo || tk.Stage == TaskStagePlan) {
				// Oldest first: each card goes back past the newer ones.
				cards := append(open[board], autoCard{sess, board, tk.ID, tk.Title, tk.Project, tk.Harness, tk.Machine, tk.At})
				for i := len(cards) - 1; i > 0 && cards[i-1].at > cards[i].at; i-- {
					cards[i-1], cards[i] = cards[i], cards[i-1]
				}
				open[board] = cards
			}
		}
	}

	now := time.Now()
	a := &d.auto
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runs == nil {
		a.runs = map[string]autoRun{}
	}
	for k, r := range a.runs {
		if !r.starting && !held[k] {
			delete(a.runs, k)
		}
	}
	waiting, note := map[string]int{}, map[string]string{}
	quiet := false
	for board, preset := range boards {
		cards := open[board]
		if why := d.dispatch.blocked(board, DispatchAuto, now); why != "" {
			if len(cards) > 0 {
				note[board], quiet = why, quiet || why == "quiet hours"
			}
			continue
		}
		for i, c := range cards {
			key := autoKey(c.sess.Name(), c.id)
			if _, ok := a.runs[key]; ok {
				continue
			}
			if len(a.runs) >= capTotal {
				waiting[board], note[board] = len(cards)-i, "at the cap of "+strconv.Itoa(capTotal)
				break
			}
			exclude := d.autoExclude(cmp.Or(c.m, preset.Machine, dispatchMachineHere), capMachine)
			if len(exclude) > len(d.farHosts()) {
				waiting[board], note[board] = len(cards)-i, "each machine it may use is full"
				break
			}
			agent := cmp.Or(c.harness, preset.Agent)
			p := placeParams{Agent: agent, Task: c.id, TaskSession: c.sess.Name(), Exclude: exclude}
			if c.project != "" {
				p.LocalOrigin = d.autoOrigin(c.sess)
			}
			a.mu.Unlock()
			ans, verr := d.placeAgent(autoConn(), p)
			a.mu.Lock()
			if verr != nil {
				if notInstalledAnywhere(ans.Machines) {
					d.stopBoard(board, "the preset's agent "+agent+" is not installed on any machine")
					break
				}
				waiting[board], note[board] = len(cards)-i, verr.Message
				break
			}
			a.runs[key] = autoRun{board: board, host: ans.Host, starting: true}
			go d.autoStart(c, agent, preset, ans)
		}
	}
	changed := len(waiting) != len(a.waiting) || len(note) != len(a.note)
	for k, v := range note {
		changed = changed || a.note[k] != v || a.waiting[k] != waiting[k]
	}
	a.waiting, a.note = waiting, note
	if quiet && a.quiet == nil {
		a.quiet = time.AfterFunc(d.dispatch.untilQuietEnds(now), func() {
			a.mu.Lock()
			a.quiet = nil
			a.mu.Unlock()
			d.kickAuto()
		})
	}
	if changed {
		go d.publishDispatch("auto", "")
	}
}

// autoExclude is the machines placement leaves out for a card of machine:
// each one at capMachine of auto's agents, and each one but machine unless it
// is any. a.mu is held.
func (d *Daemon) autoExclude(machine string, capMachine int) []string {
	out := []string{}
	for _, h := range append(d.farHosts(), "") {
		n := 0
		for _, r := range d.auto.runs {
			if r.host == h {
				n++
			}
		}
		here := h == ""
		if n >= capMachine || machine != TaskMachineAny && (here != (machine == dispatchMachineHere) || !here && h != machine) {
			out = append(out, cmp.Or(h, federation.LocalHostName))
		}
	}
	return out
}

// notInstalledAnywhere reports whether placement left out every machine
// it asked because the agent is not installed there.
func notInstalledAnywhere(machines []PlaceMachine) bool {
	asked := 0
	for _, m := range machines {
		switch {
		case strings.HasSuffix(m.Why, ": left out."):
		case strings.HasSuffix(m.Why, " is not installed there."):
			asked++
		default:
			return false
		}
	}
	return asked > 0
}

// dispatchMachineHere is a preset's machine for this machine only.
const dispatchMachineHere = "here"

// farHosts are the [hosts] names of this daemon.
func (d *Daemon) farHosts() []string {
	if d.federation == nil {
		return nil
	}
	return d.federation.Table().Names()
}

// stopBoard stops board for reason, once.
func (d *Daemon) stopBoard(board, reason string) {
	ds := &d.dispatch
	ds.mu.Lock()
	b := ds.boardLocked(board, true)
	stop := b.Stopped == ""
	if stop {
		b.Stopped = reason
		ds.saveLocked()
	}
	ds.mu.Unlock()
	if stop {
		LogBasic("Task dispatch stopped on board %s: %s", board, reason)
		go d.publishDispatch("stopped", reason)
	}
}

// untilQuietEnds is how long until the quiet hours end, a second after.
func (ds *dispatchStore) untilQuietEnds(now time.Time) time.Duration {
	ds.mu.Lock()
	_, to, _ := parseQuietHours(ds.f.Quiet)
	ds.mu.Unlock()
	wait := (to - now.Hour()*60 - now.Minute() + 24*60) % (24 * 60)
	return time.Duration(wait)*time.Minute - time.Duration(now.Second()-1)*time.Second
}

// autoFolder is the folder of sess's repository: its worktree's main
// checkout, else its focused pane's folder, else its start folder.
func (d *Daemon) autoFolder(sess *Session) string {
	if st := sess.GetState(); st != nil {
		if st.Worktree != nil && st.Worktree.RepoRoot != "" {
			return st.Worktree.RepoRoot
		}
		if dir := sess.windowCwd(st.FocusedWindowID); dir != "" {
			return dir
		}
	}
	return sess.StartDir()
}

// autoOrigin is the origin URL of sess's folder, "" when it has none.
func (d *Daemon) autoOrigin(sess *Session) string {
	origin, _ := worktree.OriginURL(d.autoFolder(sess))
	return origin
}

// autoConn is the caller auto's own verb calls run as: the daemon, outside
// every pane.
func autoConn() *connState {
	return &connState{clientID: "tuios dispatch", done: make(chan struct{})}
}

// autoStartBudget bounds a start on another machine: the worktree (a clone,
// maybe) and the agent's ready wait.
const autoStartBudget = 6 * time.Minute

// autoStart makes the worktree and starts agent for card c on the machine
// place picked, then counts the outcome.
func (d *Daemon) autoStart(c autoCard, agent string, preset DispatchPreset, place PlaceAnswer) {
	key := autoKey(c.sess.Name(), c.id)
	ok := false
	defer func() {
		d.auto.mu.Lock()
		if ok {
			d.auto.runs[key] = autoRun{board: c.board, host: place.Host}
		} else {
			delete(d.auto.runs, key)
		}
		d.auto.mu.Unlock()
		d.kickAuto()
	}()
	failed := func(why string) {
		LogBasic("Task dispatch could not start task %s in session %s: %s", c.id, c.sess.Name(), why)
		if d.dispatch.noteOutcome(c.board, false) {
			LogBasic("Task dispatch stopped on board %s: %s", c.board, dispatchStopFailures)
			d.publishDispatch("stopped", dispatchStopFailures)
		}
	}
	// The switches again: a pause that came after the pick holds the start.
	if d.dispatch.blocked(c.board, DispatchAuto, time.Now()) != "" {
		return
	}
	// call runs verb here, or on the picked machine, and reads its answer
	// the same way.
	call := func(verb string, params map[string]any) (map[string]any, *verbError) {
		var raw []byte
		var err error
		if place.Host == "" {
			raw, _ = jsonx.Marshal(params)
			h := (*Daemon).verbNewWorktree
			if verb != "new-worktree" {
				h = (*Daemon).verbStartAgent
			}
			res, verr := h(d, autoConn(), raw)
			if verr != nil {
				return nil, verr
			}
			raw, err = jsonx.Marshal(res)
		} else {
			ctx, cancel := context.WithTimeout(d.ctx, autoStartBudget)
			raw, err = d.federation.Call(ctx, place.Host, verb, params)
			cancel()
			if rerr, ok := errors.AsType[*federation.RemoteError](err); ok {
				// Its code, not its words: the far daemon is untrusted.
				code := printableClaim(rerr.Code, 40)
				return nil, newVerbError(code, place.Host+" refused "+verb+" ("+code+")")
			}
		}
		var out map[string]any
		if err == nil {
			err = jsonx.Unmarshal(raw, &out)
		}
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, verb+": "+err.Error())
		}
		return out, nil
	}
	params := map[string]any{"new_branch": true}
	switch {
	case c.project == "" || place.Host == "" && worktree.NormalizeRemote(d.autoOrigin(c.sess)) == c.project:
		params["repo"] = d.autoFolder(c.sess)
	default:
		params["repo_url"], params["clone"] = "https://"+c.project, place.Clone
		if place.Host != "" {
			params["repos_root"] = hostReposRoots()[place.Host]
		}
	}
	// A branch from the title, -2 to -9 when it or its folder or session is
	// there already.
	slug := strings.ToLower(worktree.Slug(c.title))
	slug = strings.TrimRight(slug[:min(len(slug), 40)], "-.")
	var wt map[string]any
	for n := 1; wt == nil; n++ {
		params["branch"] = "task/" + slug
		if n > 1 {
			params["branch"] = "task/" + slug + "-" + strconv.Itoa(n)
		}
		res, verr := call("new-worktree", params)
		if verr != nil && (verr.Code != ErrVerbBranchExists || n == 9) {
			failed("no worktree: " + verr.Message)
			return
		}
		wt = res
	}
	params = map[string]any{"session": wt["session"], "agent": agent, "task": c.id, "task_session": c.sess.Name(), "task_mail": true}
	if c.harness == "" {
		params["model"], params["effort"], params["permission_mode"] = preset.Model, preset.Effort, preset.PermissionMode
	}
	if place.Host != "" {
		params["task_host"] = place.TaskHost
	}
	if n, _ := strconv.Atoi(os.Getenv("TUIOS_DISPATCH_READY_SECONDS")); n > 0 {
		// Tests shorten the wait for an agent that never comes up.
		params["ready_timeout"] = min(n, 600) * 1000
	}
	res, verr := call("start-agent", params)
	switch outcome, _ := res["outcome"].(string); {
	case verr != nil && verr.Code == ErrVerbInvalidParams:
		d.stopBoard(c.board, "the preset was refused: "+verr.Message)
	case verr != nil:
		failed("the agent did not start: " + verr.Message)
	case outcome == string(agentStartReady) || outcome == string(agentStartBlocked):
		// Ready, or waiting on the person: the agent holds its card.
		ok = res["task"] != nil
		LogBasic("Task dispatch started %s for task %s in session %s on %s", agent, c.id, c.sess.Name(), cmp.Or(place.Host, "this machine"))
	case res["task"] == nil:
		failed("the agent did not start (" + outcome + ")")
	default:
		// The agent never came up, and its pane holds the card: letting it
		// go counts the failure. A closed pane let it go already.
		window, _ := res["window_id"].(string)
		d.releaseHeld(c.sess, window, "the agent did not start")
	}
}

// autoStatus adds auto's counts to st.
func (d *Daemon) autoStatus(st *DispatchStatus) {
	a := &d.auto
	a.mu.Lock()
	defer a.mu.Unlock()
	st.Running = len(a.runs)
	fill := func(row *DispatchBoardRow) {
		if row.Mode != DispatchAuto {
			return
		}
		for _, r := range a.runs {
			if r.board == row.Board {
				row.Running++
			}
		}
		row.Waiting, row.Note = a.waiting[row.Board], a.note[row.Board]
	}
	for i := range st.Boards {
		fill(&st.Boards[i])
	}
	if st.Board != nil {
		fill(st.Board)
	}
}
