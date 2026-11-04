package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The New agent sheet: the TUI's spawn sheet (internal/app/cockpit_spawn.go)
// in guios. n in the agents view, its New agent button, the sidebar's, the
// palette and Ctrl+Shift+N open it over the agents view. It asks for the
// task (with images), the agent, the model, where the agent works (a new
// worktree on a branch from a base, or this folder), the machine and how many
// attempts, and Start hands them to the daemon's own verbs: fan for new
// worktrees, one attempt or several, and start-agent for this folder.
//
// Cost: opening starts one goroutine that loads the harness manifests, looks
// each program up on PATH and reads the folder's git state. Nothing polls
// while the sheet is open, and a closed sheet costs nothing. The machines are
// ranked when the sheet opens and hold their order while it is open, so a row
// never moves under the pointer.

// spawnField is one stop of the sheet, for the notice that points at it.
type spawnField uint8

const (
	spTask spawnField = iota
	spAgent
	spModel
	spWhere
	spBranch
	spBase
	spMachine
	spAttempts
	spStart
)

// spawnMaxAttempts is the most attempts one sheet starts, as the TUI's.
const spawnMaxAttempts = 8

// spawnStartTimeout bounds a start: start-agent answers once the agent is at
// its prompt, which it gives two minutes, and a worktree checkout of a large
// repository is not instant.
const spawnStartTimeout = 5 * time.Minute

// spawnCommon are the harnesses most people run, by manifest id, in the order
// the sheet offers them when none runs.
var spawnCommon = []string{"claude-code", "codex", "gemini", "opencode"}

// spawnAgent is one harness the sheet offers.
type spawnAgent struct {
	id, cmd   string
	seen      int  // panes that run it now
	installed bool // cmd is on this app's PATH
}

// spawnMachine is one machine the sheet offers: this one ("" host) or a
// linked one.
type spawnMachine struct {
	host, name string
	cores      int
	load       float64
	loadOK     bool
	working    int
	up         bool
}

// spawnSheet is the open sheet, or a closed one kept as the draft.
type spawnSheet struct {
	// byKeyAt is when n in a list opened the sheet: text typed into it
	// within guardWindow was prose, not a task (cockpit_guard.go).
	byKeyAt time.Time
	open    bool
	// task is the task and its images, as the reply box holds a message.
	task                ckDraft
	model, branch, base string
	// where is "worktree" or "folder", as the segmented control holds it.
	where string
	// branchAuto is the branch the task last named: the field follows the
	// task while it holds that name.
	branchAuto string
	agents     []spawnAgent
	agent      int
	models     map[string][]string
	branches   []string
	// The folder and what git says of it. read is false until the read
	// answers; repo is empty outside a repository.
	folder           string
	read             bool
	repo, root, head string
	origin           string
	dirty            int
	machines         []spawnMachine
	machine          int
	attempts         int
	vary             []int // the agent of each attempt; A is the sheet's
	notice           string
	noticeAt         spawnField
	starting         bool
	gen              uint64
	session          string // the session start-agent opens the pane in
	focusField       spawnField
	focusAgain       bool
	whereSeen        string
	// card is the task card the sheet opened on, nil for none: Start
	// hands it to the daemon, which claims it for the new pane.
	card              *spawnCard
	wantStart         bool
	listOpen, listWas bool
}

// spawnPreset is what the sheet remembers for a folder: every choice but the
// task and the branch.
type spawnPreset struct {
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
	Folder   bool   `json:"thisFolder,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
	Host     string `json:"host,omitempty"`
	Base     string `json:"base,omitempty"`
}

// spawnInfo is what the sheet's read found.
type spawnInfo struct {
	agents                   []spawnAgent
	repo, root, head, origin string
	dirty                    int
	branches                 []string
}

func (s *spawnSheet) worktree() bool { return s.where != "folder" }

// host is the machine picked, "" for this one.
func (s *spawnSheet) host() string {
	if s.machine < len(s.machines) {
		return s.machines[s.machine].host
	}
	return ""
}

// presetKey is the folder a preset is kept for: the repository's root, else
// the folder.
func (s *spawnSheet) presetKey() string { return cmp.Or(s.root, s.folder) }

// newAgent opens the sheet over the agents view, with the draft of the last
// sheet when one was kept.
func (a *App) newAgent() {
	if a.cockpitClient() == nil {
		a.toast(locales.SpawnNoDaemon())
		return
	}
	ck := &a.ck
	if !ck.open {
		a.toggleCockpit()
	}
	if ck.spawn != nil && ck.spawn.open {
		return
	}
	ck.cmp = nil
	s := ck.spawnDraft
	ck.spawnDraft = nil
	folder, sess := a.spawnFolder()
	if s == nil || s.folder != folder {
		// The common case is one step: Enter starts the last agent in
		// this folder on this machine (council round 1, M1). A worktree,
		// once picked, is remembered for the folder.
		s = &spawnSheet{attempts: 1, where: "folder", folder: folder}
		a.spawnApplyPreset(s, a.prefs.Spawn[folder])
	}
	s.open, s.notice, s.starting, s.listOpen, s.card = true, "", false, false, nil
	s.focusField, s.focusAgain = spTask, true
	s.session = sess
	ck.spawnGen++
	s.gen = ck.spawnGen
	a.spawnSeen(s)
	a.spawnRank(s)
	ck.spawn = s
	gen := s.gen
	go func() {
		info := readSpawnInfo(folder)
		a.update(func() { a.applySpawnInfo(gen, info) })
	}()
}

// spawnCard is the task card a sheet starts an agent on.
type spawnCard struct{ session, id string }

// newAgentFor is n on a board card, as the TUI's: the sheet with the card's
// prompt as the task, its title until the prompt is read. Start claims the
// card for the new pane. A draft kept from Esc keeps its own task, and the
// card is not tied to it.
func (a *App) newAgentFor(t session.TaskRow) {
	a.newAgent()
	s := a.ck.spawn
	if s == nil {
		return
	}
	if strings.TrimSpace(s.task.text) != "" {
		s.notice, s.noticeAt = locales.SpawnKeepsTask(t.ID), spTask
		return
	}
	s.task.text = t.Title
	s.followTask()
	s.card = &spawnCard{session: t.Session, id: t.ID}
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	go func() {
		var d struct {
			Prompt string `json:"prompt"`
		}
		err := cl.VerbInto("get-task", map[string]any{"session": t.Session, "id": t.ID}, false, &d)
		a.update(func() {
			// The prompt takes the title's place while the person has not
			// changed it.
			if err == nil && d.Prompt != "" && a.ck.spawn == s && s.task.text == t.Title {
				s.task.text = d.Prompt
				s.followTask()
			}
		})
	}()
}

// spawnApplyPreset takes the choices a folder remembers.
func (a *App) spawnApplyPreset(s *spawnSheet, pr spawnPreset) {
	if pr == (spawnPreset{}) {
		pr = a.prefs.Spawn[""]
		// The agent alone carries over from another folder: where it
		// works and how many attempts are the folder's.
		pr = spawnPreset{Agent: pr.Agent, Model: pr.Model}
	}
	s.model, s.base = pr.Model, pr.Base
	if pr.Agent != "" && !pr.Folder && pr.Attempts > 0 {
		s.where = "worktree"
	}
	s.attempts = min(max(pr.Attempts, 1), spawnMaxAttempts)
	if pr.Agent != "" {
		a.ck.spawnLast = pr.Agent
	}
	if pr.Host != "" {
		s.machines = []spawnMachine{{host: pr.Host}}
	}
}

// spawnFolder is the folder "this folder" means, and the session a pane
// started there goes in: the selected session's focused pane, when it is on
// this machine, else home.
func (a *App) spawnFolder() (string, string) {
	if s := a.session(a.selected); s != nil && !a.hostOf(s.ID).remote() {
		if dir := s.WorkDir(); dir != "" && filepath.IsAbs(dir) {
			return filepath.Clean(dir), s.Name
		}
		return proto.Home(), s.Name
	}
	return proto.Home(), ""
}

// spawnSeen counts the agents that run now, by harness, and the models they
// report.
func (a *App) spawnSeen(s *spawnSheet) {
	count := map[string]int{}
	s.models = map[string][]string{}
	for _, ag := range a.ck.agents {
		if ag.Harness == "" {
			continue
		}
		count[ag.Harness]++
		if m, _ := ag.Meta["model"].(string); m != "" && !slices.Contains(s.models[ag.Harness], m) {
			s.models[ag.Harness] = append(s.models[ag.Harness], m)
		}
	}
	for i := range s.agents {
		s.agents[i].seen = count[s.agents[i].id]
		delete(count, s.agents[i].id)
	}
	for id, n := range count {
		s.agents = append(s.agents, spawnAgent{id: id, cmd: harnessName(id), seen: n})
	}
	a.spawnSortAgents(s)
}

// spawnSortAgents orders the agents: running ones by how many, then the
// installed ones, then the common ones, then the rest by name. The picked
// agent stays picked; a new sheet picks the one the folder or the last
// start used.
func (a *App) spawnSortAgents(s *spawnSheet) {
	picked, vary := a.spawnPicked(s)
	a.spawnSortAgentsAs(s, picked, vary)
}

// spawnPicked is the agent picked and the agent of each attempt, by id, to
// pick again after the list changes.
func (a *App) spawnPicked(s *spawnSheet) (string, []string) {
	picked := a.ck.spawnLast
	if s.agent < len(s.agents) && s.read {
		picked = s.agents[s.agent].id
	}
	vary := make([]string, len(s.vary))
	for i, v := range s.vary {
		if v < len(s.agents) {
			vary[i] = s.agents[v].id
		}
	}
	return picked, vary
}

func (a *App) spawnSortAgentsAs(s *spawnSheet, picked string, vary []string) {
	rank := func(x spawnAgent) int {
		switch {
		case x.seen > 0:
			return 0
		case x.installed:
			return 1
		case slices.Contains(spawnCommon, x.id):
			return 2
		}
		return 3
	}
	common := func(x spawnAgent) int {
		if i := slices.Index(spawnCommon, x.id); i >= 0 {
			return i
		}
		return len(spawnCommon)
	}
	slices.SortStableFunc(s.agents, func(x, y spawnAgent) int {
		return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(y.seen, x.seen), cmp.Compare(common(x), common(y)), cmp.Compare(x.cmd, y.cmd))
	})
	find := func(id string) int {
		return max(slices.IndexFunc(s.agents, func(x spawnAgent) bool { return x.id == id }), 0)
	}
	s.agent = find(picked)
	for i, id := range vary {
		s.vary[i] = find(id)
	}
}

// spawnRank lists the machines best fit first: by spare cores (the cores the
// load leaves free, less half a core per working agent), then the machines
// that report no load by fewest working agents. A machine whose link is down
// is last and cannot be picked.
func (a *App) spawnRank(s *spawnSheet) {
	picked := s.host()
	working := map[string]int{}
	for _, ag := range a.ck.agents {
		if ag.State == "working" {
			working[ag.Host]++
		}
	}
	name, _ := os.Hostname()
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	local := spawnMachine{name: cmp.Or(name, locales.HostThisComputer()), working: working[""], up: true}
	if l := a.localLoad; l.LoadKnown() {
		local.cores, local.load, local.loadOK = l.Cores, l.Load1, true
	} else if load, ok := spawnLoadAvg(); ok {
		// No load from the daemon while no machine is linked: this
		// machine's own, read once as the sheet opens.
		local.cores, local.load, local.loadOK = runtime.NumCPU(), load, true
	}
	ms := []spawnMachine{local}
	for _, h := range a.hosts[1:] {
		m := spawnMachine{host: h.name, name: h.name, working: max(working[h.name], h.rep.AgentsWorking), up: h.rep.Status == client.HostUp && h.cl != nil}
		if h.rep.LoadKnown() {
			m.cores, m.load, m.loadOK = h.rep.Cores, h.rep.Load1, true
		}
		ms = append(ms, m)
	}
	score := func(x spawnMachine) float64 {
		cores := float64(min(max(x.cores, 1), 256))
		free := 1 - min(max(x.load/float64(max(x.cores, 1)), 0), 1)
		return cores*free - 0.5*float64(x.working)
	}
	slices.SortStableFunc(ms, func(x, y spawnMachine) int {
		first := func(b bool) int {
			if b {
				return -1
			}
			return 1
		}
		switch {
		case x.up != y.up:
			return first(x.up)
		case x.loadOK != y.loadOK:
			return first(x.loadOK)
		case x.loadOK:
			return cmp.Compare(score(y), score(x))
		}
		return cmp.Compare(x.working, y.working)
	})
	s.machines, s.machine = ms, 0
	for i, x := range ms {
		if x.host == picked && x.up && picked != "" {
			s.machine = i
		}
	}
}

// readSpawnInfo reads, off the main thread, the harness manifests, which of
// their programs are on PATH, and the folder's repository, branch, origin,
// uncommitted files and local branches.
func readSpawnInfo(folder string) spawnInfo {
	var info spawnInfo
	if reg, _ := harness.Load(harness.UserDir()); reg != nil {
		for _, id := range reg.IDs() {
			cmd := reg.Lookup(id).Command()
			_, err := exec.LookPath(cmd)
			info.agents = append(info.agents, spawnAgent{id: id, cmd: cmd, installed: err == nil})
		}
	}
	if st, ok := gitstate.Read(folder); ok {
		info.repo, info.root, info.head = st.Repo, st.Root, st.Branch
		if st.Detached {
			info.head = ""
		}
		if d, ok := gitstate.ReadDirty(st.Root); ok {
			info.dirty = d.Staged + d.Modified + d.Untracked
		}
		info.origin, _ = worktree.OriginURL(folder)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := childproc.Git(ctx, st.Root, "for-each-ref", "--sort=-committerdate", "--count=30",
			"--format=%(refname:short)", "refs/heads").Output()
		cancel()
		if err == nil {
			info.branches = strings.Fields(string(out))
		}
	}
	return info
}

// applySpawnInfo takes what the read found.
func (a *App) applySpawnInfo(gen uint64, info spawnInfo) {
	s := a.ck.spawn
	if s == nil || s.gen != gen {
		return
	}
	picked, vary := a.spawnPicked(s)
	seen := map[string]int{}
	for _, x := range s.agents {
		seen[x.id] = x.seen
	}
	agents := info.agents
	for i := range agents {
		agents[i].seen = seen[agents[i].id]
		delete(seen, agents[i].id)
	}
	for _, x := range s.agents {
		if _, ok := seen[x.id]; ok {
			agents = append(agents, x)
		}
	}
	s.agents = agents
	a.spawnSortAgentsAs(s, picked, vary)
	s.read = true
	s.repo, s.root, s.head, s.origin, s.dirty, s.branches = info.repo, info.root, info.head, info.origin, info.dirty, info.branches
	if s.repo == "" {
		s.where = "folder"
		s.attempts, s.vary = 1, nil
	}
	if s.base == "" || !slices.Contains(s.branches, s.base) {
		s.base = s.head
	}
	// A preset kept for the repository's root, read now that the root is
	// known, when the folder itself had none.
	if pr, ok := a.prefs.Spawn[s.root]; ok && s.root != s.folder && a.prefs.Spawn[s.folder] == (spawnPreset{}) {
		a.spawnApplyPreset(s, pr)
		a.spawnSortAgents(s)
		a.spawnRank(s)
	}
	a.spawnSetAttempts(s, s.attempts)
}

// spawnCanWorktree reports whether a new worktree can be made: the folder is
// in a repository, and one on another machine is found by its origin.
func (s *spawnSheet) spawnCanWorktree() bool {
	return s.repo != "" && (s.host() == "" || s.origin != "")
}

// spawnNoWorktree says why there can be no new worktree.
func spawnNoWorktree(s *spawnSheet) string {
	switch {
	case !s.read:
		return locales.SpawnReading()
	case s.repo == "":
		return locales.SpawnNoRepo()
	}
	return locales.SpawnNoOrigin(s.host())
}

// spawnSetWhere sets where the agent works. This folder takes one attempt.
func (a *App) spawnSetWhere(s *spawnSheet, where string) {
	switch {
	case where == "worktree" && !s.spawnCanWorktree():
		s.where, s.notice, s.noticeAt = "folder", spawnNoWorktree(s), spWhere
		return
	case where == "folder" && s.host() != "":
		s.where, s.notice, s.noticeAt = "worktree", locales.SpawnFarWorktree(), spWhere
		return
	}
	s.where, s.notice = where, ""
	if where == "folder" {
		a.spawnSetAttempts(s, 1)
	}
}

// spawnPickMachine picks machine i when it can take an agent.
func (a *App) spawnPickMachine(s *spawnSheet, i int) {
	if i < 0 || i >= len(s.machines) || !s.machines[i].up {
		return
	}
	s.machine, s.notice = i, ""
	if s.machines[i].host != "" {
		if !s.spawnCanWorktree() {
			s.notice, s.noticeAt = spawnNoWorktree(s), spMachine
		}
		s.where = "worktree"
	}
}

// spawnSetAttempts sets how many attempts start, 1 to 8. More than one
// needs new worktrees.
func (a *App) spawnSetAttempts(s *spawnSheet, n int) {
	n = min(max(n, 1), spawnMaxAttempts)
	if n > 1 && !s.worktree() {
		if !s.spawnCanWorktree() {
			s.notice, s.noticeAt = spawnNoWorktree(s), spAttempts
			n = 1
		} else {
			s.where = "worktree"
		}
	}
	for len(s.vary) < n {
		s.vary = append(s.vary, s.agent)
	}
	s.vary, s.attempts = s.vary[:n], n
}

// spawnVary gives the attempts different agents: the installed ones and the
// running ones in turn, from the sheet's agent.
func (a *App) spawnVary(s *spawnSheet) {
	var pool []int
	for i, x := range s.agents {
		if x.installed || x.seen > 0 {
			pool = append(pool, i)
		}
	}
	if len(pool) == 0 {
		return
	}
	start := max(slices.Index(pool, s.agent), 0)
	for i := range s.vary {
		s.vary[i] = pool[(start+i)%len(pool)]
	}
}

// attemptAgent is the agent of attempt i.
func (s *spawnSheet) attemptAgent(i int) int {
	if i == 0 || i >= len(s.vary) {
		return s.agent
	}
	return s.vary[i]
}

// spawnFollowTask names the branch after the task while the person has not
// named it.
func (s *spawnSheet) followTask() {
	cur := strings.TrimSpace(s.branch)
	if cur != "" && cur != s.branchAuto {
		return
	}
	b := spawnBranchName(s.task.text)
	s.branch, s.branchAuto = b, b
}

// spawnStopWords are left out of a branch named after a task.
var spawnStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "and": true, "or": true, "for": true,
	"in": true, "on": true, "at": true, "with": true, "is": true, "are": true, "be": true, "it": true,
	"this": true, "that": true, "make": true, "please": true, "can": true, "you": true, "we": true, "i": true,
}

// spawnBranchName is a branch named after the task: agent/ and its first
// three words that say something, as the TUI's.
func spawnBranchName(task string) string {
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)))
	}) {
		if spawnStopWords[w] || len(w) > 20 {
			continue
		}
		words = append(words, w)
		if len(words) == 3 {
			break
		}
	}
	if len(words) == 0 {
		return ""
	}
	return "agent/" + strings.Join(words, "-")
}

// spawnClose closes the sheet and keeps it as the draft the next open shows.
func (a *App) spawnClose() {
	ck := &a.ck
	if ck.spawn != nil {
		ck.spawn.open = false
		ck.spawnDraft, ck.spawn = ck.spawn, nil
	}
	ck.fleetFocus.Focus()
}

// spawnSavePreset keeps the sheet's choices, but not the task or the branch,
// for the next sheet in this folder.
func (a *App) spawnSavePreset(s *spawnSheet, says bool) {
	pr := spawnPreset{Model: strings.TrimSpace(s.model), Folder: !s.worktree(), Attempts: s.attempts, Host: s.host(), Base: strings.TrimSpace(s.base)}
	if pr.Base == s.head {
		pr.Base = ""
	}
	if s.agent < len(s.agents) {
		pr.Agent = s.agents[s.agent].id
	}
	if a.prefs.Spawn == nil {
		a.prefs.Spawn = map[string]spawnPreset{}
	}
	// Bounded: a folder kept for each start would grow without end.
	if len(a.prefs.Spawn) > 200 {
		clear(a.prefs.Spawn)
	}
	a.prefs.Spawn[s.presetKey()] = pr
	a.prefs.Spawn[""] = spawnPreset{Agent: pr.Agent}
	a.savePrefs()
	if says {
		s.notice = ""
		a.toast(locales.SpawnSavedPreset())
	}
}

// spawnShellWord quotes s as one POSIX shell word.
func spawnShellWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// spec is the agent command of attempt i as fan reads it: the program, and
// the model as one shell word for an attempt of the sheet's agent.
func (s *spawnSheet) spec(i int) string {
	ai := s.attemptAgent(i)
	cmd := s.agents[ai].cmd
	if model := strings.TrimSpace(s.model); model != "" && ai == s.agent {
		return cmd + " --model " + spawnShellWord(model)
	}
	return cmd
}

// spawnStarted is the daemon's answer to a Start.
type spawnStarted struct {
	text string
	warn bool
	// selectKey is the agent row the agents view selects once it shows.
	selectKey string
}

// spawnStart checks the sheet and starts the agents. The sheet closes at once
// and is kept as the draft until the daemon says the start worked, so a
// refused start loses nothing.
func (a *App) spawnStart() {
	s := a.ck.spawn
	if s == nil {
		return
	}
	task := strings.TrimSpace(s.task.text)
	branch := strings.TrimSpace(s.branch)
	base := strings.TrimSpace(s.base)
	model := strings.TrimSpace(s.model)
	host := s.host()
	fail := func(f spawnField, why string) {
		s.notice, s.noticeAt = why, f
		s.focusField, s.focusAgain = f, true
	}
	switch {
	case s.starting:
		fail(spStart, locales.SpawnStarting())
		return
	case !s.read:
		fail(spStart, locales.SpawnReadingAll())
		return
	case task == "":
		fail(spTask, locales.SpawnWriteTask())
		return
	case len(s.agents) == 0:
		fail(spAgent, locales.SpawnNoAgent())
		return
	case host == "" && !s.agents[s.agent].installed:
		fail(spAgent, locales.SpawnNotInstalled(s.agents[s.agent].cmd))
		return
	case strings.HasPrefix(model, "-"):
		fail(spModel, locales.SpawnBadModel())
		return
	case s.worktree() && !s.spawnCanWorktree():
		fail(spWhere, spawnNoWorktree(s))
		return
	case s.worktree() && branch == "":
		fail(spBranch, locales.SpawnWriteBranch())
		return
	case s.worktree() && strings.IndexFunc(branch, func(r rune) bool { return !session.FanNameChar(r) }) >= 0:
		fail(spBranch, locales.SpawnBranchChars())
		return
	case s.worktree() && !session.FanNameRefOK(branch):
		fail(spBranch, locales.SpawnBranchGit())
		return
	case s.worktree() && strings.HasPrefix(base, "-"):
		fail(spBase, locales.SpawnBadBase())
		return
	case host != "" && len(s.task.images) > 0:
		fail(spTask, locales.SpawnFarImages())
		return
	}
	cl := a.cockpitClient()
	if host != "" {
		if h := a.hostNamed(host); h != nil {
			cl = h.cl
		}
	}
	if cl == nil {
		fail(spMachine, locales.HostNotConnected(host))
		return
	}
	// The images go as files the agent reads, named in the task as the
	// reply box's images are typed: "[Image #1] PATH".
	if len(s.task.images) > 0 {
		paths, err := spawnSaveImages(s.task.images)
		if err != nil {
			fail(spTask, locales.SpawnImagesNotSaved(err.Error()))
			return
		}
		for i, p := range paths {
			task += " [Image #" + strconv.Itoa(s.task.images[i].n) + "] " + p
		}
	}
	params := map[string]any{"prompt": task}
	if host == "" {
		// The agent is looked up where this app finds programs.
		params["env"] = map[string]string{"PATH": os.Getenv("PATH")}
	}
	verb, what := "start-agent", s.agents[s.agent].cmd
	if s.worktree() {
		verb = "fan"
		params["count"] = s.attempts
		params["name"] = branch
		if base != "" {
			params["base"] = base
		}
		specs := make([]string, s.attempts)
		for i := range specs {
			specs[i] = s.spec(i)
		}
		if slices.Equal(specs, slices.Repeat(specs[:1], len(specs))) {
			params["agent"] = specs[0]
		} else {
			params["agents"] = specs
		}
		if host == "" {
			params["repo"] = s.folder
		} else {
			params["repo_url"] = s.origin
		}
		what = branch
	} else {
		params["agent"] = s.agents[s.agent].cmd
		if model != "" {
			params["args"] = []string{"--model", model}
		}
		params["cwd"] = s.folder
		if s.session != "" {
			params["session"] = s.session
		}
		// Named after the task, as a worktree's branch is: the row says
		// what it works on from its first frame, not "Terminal 23a8…",
		// and the agents view selects it as soon as its pane shows.
		if name := strings.TrimPrefix(spawnBranchName(task), "agent/"); name != "" {
			params["name"] = name
			ck := &a.ck
			if ck.spawnTasks == nil {
				ck.spawnTasks = map[string]string{}
			}
			ck.spawnTasks[name] = flat(task)
			ck.selectName = name
		}
	}
	// The daemon takes the card in the call: start-agent opens the pane in
	// the card's session and claims the card for it, and fan stamps the
	// card with the fan it makes.
	card := s.card
	if host != "" {
		card = nil
	}
	switch {
	case card == nil:
	case verb == "fan":
		params["task"], params["task_session"] = card.id, card.session
	default:
		params["session"], params["task"] = card.session, card.id
	}
	a.ck.spawnLast = s.agents[s.agent].id
	a.spawnSavePreset(s, false)
	s.starting, s.notice = true, ""
	a.spawnClose()
	agentCmd := s.agents[s.agent].cmd
	switch {
	case !s.worktree():
		a.toast(locales.SpawnStartingFolder(what))
	case s.attempts > 1:
		a.toast(locales.SpawnStartingFan(s.attempts))
	default:
		a.toast(locales.SpawnStartingWorktree(agentCmd))
	}
	attempts := s.attempts
	cockpit := a.cockpitClient()
	go func() {
		if params["task"] != nil && cockpit != nil {
			// The card is the person's to hand out: the call carries
			// their nonce, which the daemon takes from any connection
			// of this app.
			if nonce, err := cockpit.HumanNonce(); err == nil {
				params["human_nonce"] = nonce
			}
		}
		raw, err := cl.LongVerb(verb, params, spawnStartTimeout)
		// A card that cannot be taken refuses the call: the agent starts
		// without it, and the notice says why.
		leftOut := ""
		if verr, ok := errors.AsType[*session.VerbCallError](err); ok && params["task"] != nil && spawnTaskRefused(verr) {
			leftOut = flat(verr.Message)
			delete(params, "task")
			delete(params, "task_session")
			delete(params, "human_nonce")
			raw, err = cl.LongVerb(verb, params, spawnStartTimeout)
		}
		var res struct {
			Session      string `json:"session"`
			Reason       string `json:"reason"`
			Total        int    `json:"total"`
			Ready        bool   `json:"ready"`
			Group        string `json:"group"`
			PromptStatus string `json:"prompt_status"`
			PromptNote   string `json:"prompt_note"`
			WindowID     string `json:"window_id"`
			Task         *struct {
				ID string `json:"id"`
			} `json:"task"`
			TaskNote string `json:"task_note"`
			Sessions []struct {
				Session  string `json:"session"`
				WindowID string `json:"window_id"`
			} `json:"sessions"`
		}
		if err == nil {
			_ = json.Unmarshal(raw, &res)
		}
		// What became of the card, said after the start's own words.
		held := ""
		switch {
		case err != nil || card == nil:
		case leftOut != "":
			held = " " + locales.SpawnCardLeftOut(card.id, leftOut)
		case res.Task != nil && verb == "fan":
			held = " " + locales.SpawnFanOnCard(res.Task.ID)
		case res.Task != nil:
			held = " " + locales.SpawnHoldsCard(res.Task.ID)
		case res.TaskNote != "":
			held = " " + flat(res.TaskNote)
		}
		a.update(func() {
			s.starting = false
			if err != nil {
				a.toast(locales.SpawnFailed(strings.TrimSuffix(strings.TrimSpace(err.Error()), ".")))
				return
			}
			var out spawnStarted
			hostKey := host
			switch {
			case verb == "fan" && len(res.Sessions) > 0:
				out.selectKey = client.Agent{Host: hostKey, Session: res.Sessions[0].Session, Window: res.Sessions[0].WindowID}.Key()
			case verb == "start-agent" && res.WindowID != "":
				out.selectKey = client.Agent{Host: hostKey, Session: res.Session, Window: res.WindowID}.Key()
			}
			switch {
			case verb == "fan" && attempts > 1:
				out.text = locales.SpawnStartedFan(res.Total, res.Group)
			case verb == "fan":
				out.text = locales.SpawnStartedWorktree(what)
			case !res.Ready && res.Reason != "":
				out.text, out.warn = locales.SpawnNotReady(what, res.Session, flat(res.Reason)), true
			case res.PromptStatus != "" && res.PromptStatus != session.PromptSent:
				out.text, out.warn = locales.SpawnTaskNotSent(what, res.Session, flat(res.PromptNote)), true
			default:
				out.text = locales.SpawnStartedIn(what, res.Session)
			}
			out.text += held
			a.applySpawnStarted(s, out)
		})
	}()
}

// applySpawnStarted says what became of a start that worked, drops its
// draft, and selects the new agent once the agents view lists it.
func (a *App) applySpawnStarted(s *spawnSheet, out spawnStarted) {
	ck := &a.ck
	if ck.spawnDraft == s {
		ck.spawnDraft = nil
	}
	if ck.spawn == s {
		s.task = ckDraft{}
		s.branch, s.branchAuto = "", ""
	}
	if out.selectKey != "" {
		ck.selectWhenSeen = out.selectKey
	}
	a.toast(out.text)
	a.refreshCockpit()
}

// spawnSaveImages writes the task's images where this machine's agents can
// read them: one private folder per start under the cache, kept a day.
func spawnSaveImages(images []ckImage) ([]string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "guios", "task-images")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	// The folders of earlier starts, a day old, go.
	if ents, err := os.ReadDir(root); err == nil {
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	dir, err := os.MkdirTemp(root, "task-")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, im := range images {
		ext := strings.ToLower(filepath.Ext(im.name))
		if !isPasteExt(ext) {
			// A pasted image has no name to tell its kind: its bytes do.
			switch http.DetectContentType(im.data) {
			case "image/jpeg":
				ext = ".jpg"
			case "image/gif":
				ext = ".gif"
			case "image/webp":
				ext = ".webp"
			default:
				ext = ".png"
			}
		}
		p := filepath.Join(dir, fmt.Sprintf("image-%d%s", im.n, ext))
		if err := os.WriteFile(p, im.data, 0o600); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// spawnLoadAvg is this machine's one-minute load, where the system says it.
func spawnLoadAvg() (float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	f, _, _ := strings.Cut(string(b), " ")
	v, err := strconv.ParseFloat(f, 64)
	return v, err == nil
}

// spawnTaskRefused reports whether a refusal names one of the parameters
// the sheet sends for a card: the card cannot be taken, or the daemon is
// older than them.
func spawnTaskRefused(err *session.VerbCallError) bool {
	if err.Hint == nil {
		return false
	}
	switch err.Hint.Param {
	case "task", "task_session", "human_nonce":
		return true
	}
	return false
}
