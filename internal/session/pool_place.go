package session

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Machines as one pool: "Any machine" placement.
//
// place-agent picks the machine a new agent starts on: this one or a linked
// one. Each machine answers for itself through agent-readiness: whether the
// agent is installed and signed in there, whether it hit a usage limit there,
// whether it has a checkout of the repository, its load now, its cap, and its
// name for this daemon (a far agent needs a link back to claim a card here).
// This daemon asks every machine whose link is up at once, then decides.
//
// The rule, in order:
//
//  1. Gate. A machine is out when its link is not up or refuses the call, the
//     agent is not installed, signed out or usage limited there, the
//     repository has no checkout there (and clone is off) or more than one,
//     its memory is 95% used, it is at its [tasks] machine_cap, or the agent
//     would work on a card the machine cannot reach.
//  2. Load. Spare cores: the cores (held to poolCoresMax) times the share the
//     one-minute load leaves free, less half a core per busy agent (working,
//     starting, or picked here and not started yet). Memory over 80% used
//     scales it down. A machine with no known load ranks after every known
//     one, by fewest busy agents.
//  3. Locality. Spares within one core are a tie: the machine with the
//     checkout wins, then this machine, then the first name.
//
// A pick reserves the machine: it counts as one more busy agent there until
// that machine reports a start, or for poolHold. Two callers on this daemon
// pick one at a time, so the second sees the first's reservation. Two
// daemons that pick for one far machine at once can both pick it: the far
// answer counts an agent as starting as soon as its pane exists, which is
// after the other caller's start lands.
//
// Nothing here polls. A pick costs one agent-readiness call to each machine,
// bounded by poolAskTimeout.

// ErrVerbNoMachine reports a placement that no machine passed.
const ErrVerbNoMachine = "no_machine"

// Placement bounds.
const (
	// poolAskTimeout bounds one machine's answer. The signed-in check is
	// held to 2 seconds there.
	poolAskTimeout = 5 * time.Second
	// poolHold is how long a pick reserves its machine, and how long a
	// started agent that is not working yet counts as starting.
	poolHold = 90 * time.Second
	// poolCoresMax bounds the cores a machine is ranked by, so a machine
	// cannot win by claiming more.
	poolCoresMax = 256
	// poolTie is how many spare cores apart two machines tie.
	poolTie = 1.0
	// poolMemFull is the memory use, in percent, that gates a machine, and
	// poolMemHigh where its score starts to go down.
	poolMemFull = 95
	poolMemHigh = 80
)

// poolLoad is a machine's load as agent-readiness says it.
type poolLoad struct {
	Cores  int     `json:"cores"`
	Load1  float64 `json:"load1"`
	LoadOK bool    `json:"load_ok"`
	MemPct float64 `json:"mem_pct,omitempty"`
	// Working is the agents working now, and Starting the agents started
	// in the last poolHold that are not working yet.
	Working  int `json:"working"`
	Starting int `json:"starting"`
	// Started counts the agents started since this daemon started, and
	// Instance names the run, so a caller can tell its reservation landed.
	Started  int    `json:"started"`
	Instance string `json:"instance"`
}

// bounded holds an answer from another machine to what a machine reports.
func (l poolLoad) bounded() poolLoad {
	l.Cores = min(max(l.Cores, 0), 1<<16)
	l.Load1 = min(max(l.Load1, 0), 1<<20)
	l.MemPct = min(max(l.MemPct, 0), 100)
	if l.Load1 != l.Load1 || l.MemPct != l.MemPct {
		l.Load1, l.MemPct, l.LoadOK = 0, 0, false
	}
	l.Working = min(max(l.Working, 0), 1<<16)
	l.Starting = min(max(l.Starting, 0), 1<<16)
	l.Started = max(l.Started, 0)
	if len(l.Instance) > 64 {
		l.Instance = ""
	}
	return l
}

// poolState is this daemon's placement state.
type poolState struct {
	// pick serializes the decision and its reservation.
	pick sync.Mutex

	mu sync.Mutex
	// reserved are the picks not started yet, by host ("" is this
	// machine).
	reserved map[string][]poolReservation
	// started are the agents start-agent started here in the last
	// poolHold, and startedTotal every one since the daemon started.
	started      []poolStart
	startedTotal int
}

// poolReservation is one pick: when, and the machine's started count and
// run then.
type poolReservation struct {
	at       time.Time
	started  int
	instance string
}

// poolStart is one agent start-agent started here.
type poolStart struct {
	sess   *Session
	window string
	at     time.Time
}

// noteAgentStarted records an agent start, so this machine's answer counts
// it as starting until it works or poolHold passes.
func (d *Daemon) noteAgentStarted(sess *Session, window string) {
	p := &d.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = append(p.dropOldStarts(time.Now()), poolStart{sess: sess, window: window, at: time.Now()})
	p.startedTotal++
}

// poolLoadNow is this machine's load, with its starting agents.
func (d *Daemon) poolLoadNow() poolLoad {
	ml := machineLoad(d.manager.ListSessions())
	l := poolLoad{Cores: ml.Cores, Load1: ml.Load1, LoadOK: ml.Known(), MemPct: ml.MemPct, Working: ml.Working, Instance: d.instance}
	p := &d.pool
	p.mu.Lock()
	starts := slices.Clone(p.dropOldStarts(time.Now()))
	p.started = starts
	l.Started = p.startedTotal
	p.mu.Unlock()
	for _, s := range starts {
		if w, ok := findWindowState(s.sess.GetState(), s.window); ok && w.AgentState != AgentStateWorking {
			l.Starting++
		}
	}
	return l
}

// dropOldStarts is p.started without the starts older than poolHold. p.mu
// is held.
func (p *poolState) dropOldStarts(now time.Time) []poolStart {
	kept := p.started[:0]
	for _, s := range p.started {
		if now.Sub(s.at) < poolHold {
			kept = append(kept, s)
		}
	}
	return kept
}

// pending is how many picks for host have not landed: reservations younger
// than poolHold, less the starts the machine reported since the oldest.
func (p *poolState) pending(host string, l poolLoad, now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var list []poolReservation
	for _, r := range p.reserved[host] {
		if now.Sub(r.at) < poolHold {
			list = append(list, r)
		}
	}
	if p.reserved != nil {
		p.reserved[host] = list
	}
	if len(list) == 0 {
		return 0
	}
	seen := list[0].started
	landed := 0
	if list[0].instance == l.Instance && l.Instance != "" {
		landed = max(l.Started-seen, 0)
	}
	return max(len(list)-landed, 0)
}

// reserve notes a pick of host.
func (p *poolState) reserve(host string, l poolLoad, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reserved == nil {
		p.reserved = map[string][]poolReservation{}
	}
	p.reserved[host] = append(p.reserved[host], poolReservation{at: now, started: l.Started, instance: l.Instance})
}

// repoPresence is found, none or many: this machine's checkouts of url,
// looked for as start-agent looks.
func (d *Daemon) repoPresence(url, reposRoot string) (string, *verbError) {
	roots := worktree.DefaultSearchRoots(userHome())
	if reposRoot != "" {
		dir, verr := reposRootParam(reposRoot)
		if verr != nil {
			return "", verr
		}
		roots = []worktree.SearchRoot{{Dir: dir, Depth: 3}}
	}
	switch n := len(worktree.FindByOrigin(roots, url)); {
	case n == 1:
		return poolRepoFound, nil
	case n > 1:
		return poolRepoMany, nil
	}
	return poolRepoNone, nil
}

// What agent-readiness says of a repository.
const (
	poolRepoFound = "found"
	poolRepoNone  = "none"
	poolRepoMany  = "many"
)

// backName is this machine's [hosts] name for the daemon run instance, when
// its link is up.
func (d *Daemon) backName(instance string) string {
	if d.federation == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(d.ctx, 2*time.Second)
	defer cancel()
	for _, r := range d.federation.Reports(ctx) {
		if r.Status == federation.StatusUp && r.Instance == instance {
			return r.Host
		}
	}
	return ""
}

// usageLimitedHarnesses are the harnesses with a pane here that is errored
// with a usage limit.
func (d *Daemon) usageLimitedHarnesses() map[string]bool {
	out := map[string]bool{}
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if st == nil {
			continue
		}
		for _, w := range st.Windows {
			if w.AgentState == AgentStateErrored && w.AgentHarness != "" && usageLimited(w) {
				out[w.AgentHarness] = true
			}
		}
	}
	return out
}

// placeParams are what place-agent takes.
type placeParams struct {
	Agent     string `json:"agent"`
	RepoURL   string `json:"repo_url"`
	ReposRoot string `json:"repos_root"`
	Clone     bool   `json:"clone"`
	// LocalOrigin is the origin URL of the caller's folder on this machine.
	// When it names the same repository as repo_url, this machine has a
	// checkout, wherever it is.
	LocalOrigin string `json:"local_origin"`
	// Task and TaskSession name a card of this daemon the agent is to take.
	Task        string            `json:"task"`
	TaskSession string            `json:"task_session"`
	Reserve     *bool             `json:"reserve"`
	Exclude     []string          `json:"exclude"`
	Env         map[string]string `json:"env"`
}

// PlaceMachine is one machine of a placement: whether it passed, and why in
// one line.
type PlaceMachine struct {
	Host string `json:"host"`
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Why  string `json:"why"`
	// Repo is found, none or many, empty when no repository was named or
	// the machine did not say.
	Repo string `json:"repo,omitempty"`

	load  poolLoad
	known bool
	busy  int
	score float64
	back  string
	capAt int
}

// PlaceAnswer is place-agent's result.
type PlaceAnswer struct {
	// Host is the machine picked, empty for this one, and Name how a
	// person reads it.
	Host string `json:"host"`
	Name string `json:"name"`
	Why  string `json:"why"`
	// TaskHost is the picked machine's name for this daemon, for
	// start-agent's task_host, when a card was named and the pick is
	// another machine.
	TaskHost string `json:"task_host,omitempty"`
	// Clone says the picked machine has no checkout and clones one.
	Clone    bool           `json:"clone,omitempty"`
	Machines []PlaceMachine `json:"machines"`
}

// verbPlaceAgent answers place-agent.
func (d *Daemon) verbPlaceAgent(cs *connState, params json.RawMessage) (any, *verbError) {
	if cs != nil && cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "place-agent is refused over the link: each machine places its own agents")
	}
	var p placeParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	ans, verr := d.placeAgent(cs, p)
	if verr != nil {
		return nil, verr
	}
	return ans, nil
}

// placeAgent is place-agent's decision. When no machine passes, the answer
// still lists every machine with its reason.
func (d *Daemon) placeAgent(cs *connState, p placeParams) (PlaceAnswer, *verbError) {
	if len(p.Exclude) > 64 {
		return PlaceAnswer{}, invalidParam("exclude", "exclude names at most 64 machines")
	}
	// The card, when one is named: its agent and repository are the
	// defaults, and a card of the session only stays here.
	sessionCard := false
	if p.Task != "" {
		sess, verr := d.resolveVerbSession(cs, p.TaskSession)
		if verr != nil {
			return PlaceAnswer{}, verr
		}
		st := sess.GetState()
		i := -1
		if st != nil {
			i = taskIndex(st.Tasks, p.Task)
		}
		if i < 0 {
			return PlaceAnswer{}, newVerbError(ErrVerbNoTask, "session "+echoName(sess.Name())+" has no task card "+echoName(p.Task))
		}
		tk := &st.Tasks[i]
		p.Agent = cmp.Or(strings.TrimSpace(p.Agent), tk.Harness)
		if p.RepoURL == "" && tk.Project != "" {
			p.RepoURL = "https://" + tk.Project
		}
		sessionCard = tk.Project == ""
	}
	agent := strings.TrimSpace(p.Agent)
	if agent == "" {
		return PlaceAnswer{}, invalidParam("agent", "agent is required: the harness to place, such as claude")
	}
	word, _, _ := strings.Cut(agent, " ")
	id, cmd := word, word
	if reg := d.agentMatcher.registry; reg != nil {
		if m, c, ok := reg.Resolve(word); ok && m != nil {
			id, cmd = m.ID, c
		}
	}
	if p.RepoURL != "" {
		if u := worktree.NormalizeRemote(p.RepoURL); u == "" || strings.HasPrefix(u, "file:") {
			return PlaceAnswer{}, invalidParam("repo_url", "repo_url is a repository's origin URL, such as git@github.com:acme/api.git")
		}
	}
	if p.Clone && p.RepoURL == "" {
		return PlaceAnswer{}, invalidParam("clone", "clone only means something with repo_url")
	}

	machines := d.askPool(cs, p, id, sessionCard)
	reserve := p.Reserve == nil || *p.Reserve
	d.pool.pick.Lock()
	defer d.pool.pick.Unlock()
	now := time.Now()
	for i := range machines {
		mc := &machines[i]
		if !mc.OK {
			continue
		}
		mc.busy = mc.load.Working + mc.load.Starting + d.pool.pending(mc.Host, mc.load, now)
		poolGate(mc, cmd, p)
		if mc.OK {
			mc.score = poolScore(mc)
			mc.Why = poolWhy(mc, p.Clone)
		}
	}
	best := -1
	for i := range machines {
		if machines[i].OK && (best < 0 || poolBetter(machines[i], machines[best])) {
			best = i
		}
	}
	if best < 0 {
		why := "no machine can start " + cmd + " now."
		for i := range machines {
			why += " " + machines[i].Why
		}
		return PlaceAnswer{Machines: machines}, newVerbError(ErrVerbNoMachine, why)
	}
	// The listing: the pick first, then the others in their order.
	pick := machines[best]
	copy(machines[1:best+1], machines[:best])
	machines[0] = pick
	if reserve {
		d.pool.reserve(pick.Host, pick.load, now)
	}
	ans := PlaceAnswer{Host: pick.Host, Name: pick.Name, Why: pick.Why, Machines: machines}
	if pick.Host != "" && p.Task != "" {
		ans.TaskHost = pick.back
	}
	ans.Clone = p.Clone && pick.Repo == poolRepoNone
	return ans, nil
}

// askPool asks this machine and every linked one for its readiness, at
// once. A machine that cannot answer comes back with OK false and why.
func (d *Daemon) askPool(cs *connState, p placeParams, id string, sessionCard bool) []PlaceMachine {
	out := []PlaceMachine{{Name: cmp.Or(localHostLabel(), "this machine"), OK: true}}
	var reports []federation.HostReport
	if d.federation != nil {
		ctx, cancel := context.WithTimeout(d.ctx, 2*time.Second)
		reports = d.federation.Reports(ctx)
		cancel()
	}
	for _, r := range reports {
		if r.Host == federation.LocalHostName {
			continue
		}
		mc := PlaceMachine{Host: r.Host, Name: r.Host, OK: true}
		switch {
		case slices.Contains(p.Exclude, r.Host):
			mc.OK, mc.Why = false, r.Host+": left out."
		case sessionCard:
			mc.OK, mc.Why = false, r.Host+": the card is of this session only, so its agent works on this machine."
		case r.Status != federation.StatusUp:
			mc.OK, mc.Why = false, r.Host+": the link is not up."
		}
		if r.LoadKnown() || r.Cores > 0 {
			// The hello's load, until the machine answers with its own.
			ok := r.LoadKnown()
			mc.load = poolLoad{Cores: r.Cores, Load1: r.Load1, LoadOK: ok, MemPct: r.MemPct, Working: r.AgentsWorking}.bounded()
			mc.known = ok
		}
		out = append(out, mc)
	}
	if slices.Contains(p.Exclude, federation.LocalHostName) {
		out[0].OK, out[0].Why = false, out[0].Name+": left out."
	}
	roots := hostReposRoots()
	var wg sync.WaitGroup
	for i := range out {
		if !out[i].OK {
			continue
		}
		mc := &out[i]
		wg.Go(func() {
			rp := readinessParams{Harnesses: []string{id}, RepoURL: p.RepoURL}
			if mc.Host == "" {
				rp.Env, rp.ReposRoot = p.Env, p.ReposRoot
				here := p.LocalOrigin != "" && p.RepoURL != "" && worktree.NormalizeRemote(p.LocalOrigin) == worktree.NormalizeRemote(p.RepoURL)
				if here {
					rp.RepoURL = ""
				}
				ans, verr := d.readiness(cs, rp)
				if verr != nil {
					mc.OK, mc.Why = false, mc.Name+": "+strings.TrimSuffix(verr.Message, ".")+"."
					return
				}
				if here {
					ans.Repo = poolRepoFound
				}
				poolTake(mc, ans, id)
				return
			}
			if p.RepoURL != "" {
				rp.ReposRoot = roots[mc.Host]
			}
			rp.PeerInstance = d.instance
			ctx, cancel := context.WithTimeout(d.ctx, poolAskTimeout)
			defer cancel()
			raw, err := d.federation.Call(ctx, mc.Host, "agent-readiness", rp)
			if err != nil {
				mc.OK, mc.Why = false, mc.Name+": "+poolAskFailed(err)
				return
			}
			var ans readinessAnswer
			if jsonx.Unmarshal(raw, &ans) != nil {
				mc.OK, mc.Why = false, mc.Name+": its answer could not be read."
				return
			}
			if ans.Load != nil {
				l := ans.Load.bounded()
				ans.Load = &l
			}
			ans.Back = printableClaim(ans.Back, 64)
			poolTake(mc, ans, id)
		})
	}
	wg.Wait()
	return out
}

// hostReposRoots is [hosts.NAME] repos_root by host, from this machine's
// config file, read at each placement.
func hostReposRoots() map[string]string {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(hosts))
	for name, h := range hosts {
		if h.ReposRoot != "" {
			out[name] = h.ReposRoot
		}
	}
	return out
}

// poolAskFailed says in words why a machine did not answer. Its own text is
// not repeated: the far daemon is untrusted.
func poolAskFailed(err error) string {
	if rerr, ok := errors.AsType[*federation.RemoteError](err); ok {
		switch rerr.Code {
		case ErrVerbForbidden:
			return "the link does not allow starting agents there."
		case ErrVerbUnknownVerb:
			return "it runs a tuios that cannot say if the agent is ready. Update tuios there."
		}
		return "it refused the question (" + printableClaim(rerr.Code, 40) + ")."
	}
	if _, ok := errors.AsType[*federation.RefusedError](err); ok {
		return "the link does not allow starting agents there."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "it did not answer in time."
	}
	return "the link went down."
}

// poolTake puts a machine's answer on mc.
func poolTake(mc *PlaceMachine, ans readinessAnswer, id string) {
	if ans.Load != nil {
		mc.load, mc.known = *ans.Load, ans.Load.LoadOK && ans.Load.Cores > 0
	}
	mc.load.Cores = max(mc.load.Cores, 0)
	mc.Repo, mc.back = ans.Repo, ans.Back
	mc.Why = ""
	found := false
	for _, a := range ans.Agents {
		if a.Harness != id {
			continue
		}
		found = true
		switch {
		case !a.Installed:
			mc.Why = "not installed"
		case a.SignedIn == SignedInNo:
			mc.Why = "not signed in"
		case a.UsageLimit:
			mc.Why = "usage limit"
		}
	}
	if !found {
		mc.Why = "not installed"
	}
	if ans.Cap > 0 {
		mc.capAt = ans.Cap
	}
}

// poolGate applies the gates that need the busy count, and turns a gate
// poolTake found into its line. cmd is the agent's program.
func poolGate(mc *PlaceMachine, cmd string, p placeParams) {
	gate := ""
	switch {
	case mc.Why == "not installed":
		gate = cmd + " is not installed there."
	case mc.Why == "not signed in":
		gate = cmd + " is not signed in there."
	case mc.Why == "usage limit":
		gate = cmd + " reached its usage limit there."
	case p.RepoURL != "" && mc.Repo == poolRepoMany:
		gate = "more than one checkout has the repository, so tuios does not pick one."
	case p.RepoURL != "" && mc.Repo != poolRepoFound && !p.Clone:
		gate = "no checkout of the repository."
	case p.Task != "" && mc.Host != "" && mc.back == "":
		gate = "it has no link back to this machine, so its agent cannot take the card."
	case mc.load.MemPct >= poolMemFull:
		gate = "memory is " + strconv.Itoa(int(mc.load.MemPct)) + "% used."
	case mc.capAt > 0 && mc.busy >= mc.capAt:
		gate = "at its cap of " + strconv.Itoa(mc.capAt) + " agents."
	}
	if gate != "" {
		mc.OK, mc.Why = false, mc.Name+": "+gate
	}
}

// poolScore is mc's spare cores.
func poolScore(mc *PlaceMachine) float64 {
	l := mc.load
	cores := max(l.Cores, 1)
	free := 1 - min(max(l.Load1/float64(cores), 0), 1)
	s := float64(min(cores, poolCoresMax))*free - 0.5*float64(mc.busy)
	if l.MemPct > poolMemHigh {
		s *= max(100-l.MemPct, 0) / (100 - poolMemHigh)
	}
	return s
}

// poolBetter reports whether a is a better pick than b.
func poolBetter(a, b PlaceMachine) bool {
	if a.known != b.known {
		return a.known
	}
	if !a.known {
		if a.busy != b.busy {
			return a.busy < b.busy
		}
	} else if d := a.score - b.score; d > poolTie || d < -poolTie {
		return d > 0
	}
	// A tie: the checkout, then this machine, then the name.
	if ar, br := a.Repo == poolRepoFound, b.Repo == poolRepoFound; ar != br {
		return ar
	}
	if (a.Host == "") != (b.Host == "") {
		return a.Host == ""
	}
	return a.Name < b.Name
}

// poolWhy is the one line that says why mc fits: "arch-btw: 16 cores, 0
// working, the repo is there."
func poolWhy(mc *PlaceMachine, clone bool) string {
	w := mc.Name + ": "
	if mc.load.Cores > 0 {
		w += strconv.Itoa(mc.load.Cores) + " cores, "
	}
	w += strconv.Itoa(mc.load.Working) + " working"
	if n := mc.busy - mc.load.Working; n > 0 {
		w += ", " + strconv.Itoa(n) + " starting"
	}
	switch {
	case !mc.known:
		w += ", load not known"
	case mc.load.Load1 >= 0.5:
		w += ", load " + strconv.FormatFloat(mc.load.Load1, 'f', 1, 64)
	}
	switch {
	case mc.Repo == poolRepoFound:
		w += ", the repo is there"
	case mc.Repo == poolRepoNone && clone:
		w += ", the repo will be cloned"
	}
	return w + "."
}
