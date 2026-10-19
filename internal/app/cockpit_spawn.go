package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The spawn sheet (DESIGN.md 3.3, frame d): n and the New agent button open
// it over the cockpit. It asks for the task, the agent, the model, where the
// agent works, the machine and how many attempts, and Start hands them to the
// daemon's own verbs: fan for new worktrees (one or several attempts), and
// start-agent for this folder. A pane appears, the task is typed into it once
// the agent is at its prompt, and the cockpit shows it as any other agent.
//
// Cost: opening reads /proc/loadavg once and starts one command that loads the
// harness manifests, looks each program up on PATH and reads the folder's git
// state. Nothing polls while the sheet is open, and a closed sheet costs
// nothing. The machines are ranked when the sheet opens and the order holds
// still while it is open, so a row never moves under the pointer.

// spawnField is one stop of the sheet's tab order.
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
	spCancel
	spStart
	spFields
)

// spawnMaxAttempts is the most attempts one sheet starts (DESIGN.md 3.3).
const spawnMaxAttempts = 8

// spawnStartTimeout bounds a start: start-agent answers once the agent is at
// its prompt, which it gives two minutes, and a worktree checkout of a large
// repository is not instant.
const spawnStartTimeout = 5 * time.Minute

// spawnAgent is one harness the sheet offers.
type spawnAgent struct {
	// id is the manifest's id, and cmd the program that starts it.
	id, cmd string
	// seen is how many panes run it now.
	seen int
	// installed says cmd is on this client's PATH.
	installed bool
}

// spawnMachine is one machine the sheet offers.
type spawnMachine struct {
	// host is the [hosts] name, empty for this machine.
	host, name string
	cores      int
	load       float64
	loadOK     bool
	working    int
	up         bool
}

// spawnSheet is the open sheet, or a closed one kept as a draft.
type spawnSheet struct {
	task, model, branch, base *composer.Model
	field                     spawnField
	agents                    []spawnAgent
	agent                     int
	// list says the list of every agent is open, and listAt is its cursor.
	list   bool
	listAt int
	// worktree is where the agent works: a new worktree, or this folder.
	worktree bool
	// branchAuto is the branch the task last named. The branch field
	// follows the task while it still holds that name.
	branchAuto string
	// The folder and what git says about it. read is false until the
	// command that reads them answers; repo is empty outside a repository.
	folder             string
	read               bool
	repo, head, origin string
	dirty              int
	machines           []spawnMachine
	machine            int
	attempts           int
	// vary is the agent of each attempt, by index into agents. Attempt A
	// is always the sheet's agent.
	vary []int
	// notice says why Start did nothing.
	notice string
	hits   []ckHit
	// gen tells an answer for this opening from one for an earlier one.
	gen uint64
}

// spawnInfoMsg carries what the sheet's opening command read.
type spawnInfoMsg struct {
	gen                uint64
	agents             []spawnAgent
	repo, head, origin string
	dirty              int
}

// spawnStartedMsg is the daemon's answer to a Start.
type spawnStartedMsg struct {
	sheet *spawnSheet
	text  string
	err   error
}

// CockpitSpawnOpen reports whether the spawn sheet is up.
func (m *OS) CockpitSpawnOpen() bool { return m.ShowInbox && m.Inbox.ck.spawn != nil }

// CockpitNewAgent is n and the New agent button: it opens the spawn sheet,
// with the draft of the last sheet when one was kept.
func (m *OS) CockpitNewAgent() tea.Cmd {
	ck := &m.Inbox.ck
	if ck.spawn != nil {
		return nil
	}
	m.Inbox.reply.editor = nil
	s := ck.spawnDraft
	ck.spawnDraft = nil
	if s == nil {
		s = &spawnSheet{
			task:     composer.New(composer.Options{MaxRows: 3, Placeholder: "What should the agent do?"}),
			model:    composer.New(composer.Options{MaxRows: 1, SingleLine: true, Placeholder: "The agent's default"}),
			branch:   composer.New(composer.Options{MaxRows: 1, SingleLine: true}),
			base:     composer.New(composer.Options{MaxRows: 1, SingleLine: true}),
			attempts: 1,
			worktree: true,
		}
	}
	s.field, s.list, s.notice, s.read = spTask, false, "", false
	ck.spawnGen++
	s.gen = ck.spawnGen
	s.folder = m.spawnFolder()
	m.spawnSeen(s)
	m.spawnRank(s)
	ck.spawn = s
	m.cockpitTouch()
	return spawnInfoCmd(s.gen, s.folder)
}

// spawnFolder is the directory "this folder" means: the focused pane's, or
// the client's own when the pane has not said where it is.
func (m *OS) spawnFolder() string {
	if w := m.GetFocusedWindow(); w != nil && m.paneIsLocal(w) && w.Cwd != "" {
		return w.Cwd
	}
	dir, _ := os.Getwd()
	return dir
}

// spawnSeen fills the agents from the panes that run one now, most first,
// keeping the one picked.
func (m *OS) spawnSeen(s *spawnSheet) {
	count := map[string]int{}
	for _, e := range m.agentEntries(m.BuildSessionTree().Sessions) {
		if e.Harness != "" {
			count[e.Harness]++
		}
	}
	for i := range s.agents {
		s.agents[i].seen = count[s.agents[i].id]
		delete(count, s.agents[i].id)
	}
	for id, n := range count {
		s.agents = append(s.agents, spawnAgent{id: id, cmd: id, seen: n})
	}
	m.spawnSortAgents(s)
}

// spawnSortAgents orders the agents: running ones by how many, then the
// installed ones, then the rest. The picked agent stays picked, and a new
// sheet picks the agent the last Start used, or the first.
func (m *OS) spawnSortAgents(s *spawnSheet) {
	picked := m.Inbox.ck.spawnLast
	if s.agent < len(s.agents) && s.read {
		picked = s.agents[s.agent].id
	}
	vary := make([]string, len(s.vary))
	for i, v := range s.vary {
		if v < len(s.agents) {
			vary[i] = s.agents[v].id
		}
	}
	rank := func(a spawnAgent) int {
		switch {
		case a.seen > 0:
			return 0
		case a.installed:
			return 1
		}
		return 2
	}
	// Among agents that do not run, the common ones come first, in the
	// order of frame d, then the rest by name.
	common := func(a spawnAgent) int {
		if i := slices.Index(spawnCommon, a.id); i >= 0 {
			return i
		}
		return len(spawnCommon)
	}
	slices.SortStableFunc(s.agents, func(a, b spawnAgent) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(b.seen, a.seen), cmp.Compare(common(a), common(b)), cmp.Compare(a.cmd, b.cmd))
	})
	find := func(id string) int {
		return max(slices.IndexFunc(s.agents, func(a spawnAgent) bool { return a.id == id }), 0)
	}
	s.agent = find(picked)
	for i, id := range vary {
		s.vary[i] = find(id)
	}
}

// spawnCommon are the harnesses most people run, by manifest id.
var spawnCommon = []string{"claude-code", "codex", "gemini", "opencode"}

// spawnRank lists the machines best fit first: by spare cores (cores minus
// load, minus half a core per working agent), then the machines that report
// no load by fewest working agents. A machine whose link is down is last and
// cannot be picked.
func (m *OS) spawnRank(s *spawnSheet) {
	picked := ""
	if s.machine < len(s.machines) {
		picked = s.machines[s.machine].host
	}
	working := map[string]int{}
	for _, e := range m.agentEntries(m.BuildSessionTree().Sessions) {
		if e.State == "working" {
			working[e.Host]++
		}
	}
	name, _ := os.Hostname()
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	local := spawnMachine{name: cmp.Or(name, "this machine"), cores: runtime.NumCPU(), working: working[""], up: true}
	local.load, local.loadOK = spawnLoadAvg()
	ms := []spawnMachine{local}
	for _, h := range m.FederationHosts {
		if h.Name == federation.LocalHostName {
			continue
		}
		ms = append(ms, spawnMachine{host: h.Name, name: h.Name, working: working[h.Name], up: h.Status == string(federation.StatusUp)})
	}
	score := func(x spawnMachine) float64 {
		return float64(x.cores) - x.load - 0.5*float64(x.working)
	}
	slices.SortStableFunc(ms, func(a, b spawnMachine) int {
		first := func(x, _ bool) int {
			if x {
				return -1
			}
			return 1
		}
		switch {
		case a.up != b.up:
			return first(a.up, b.up)
		case a.loadOK != b.loadOK:
			return first(a.loadOK, b.loadOK)
		case a.loadOK:
			return cmp.Compare(score(b), score(a))
		}
		return cmp.Compare(a.working, b.working)
	})
	s.machines, s.machine = ms, 0
	for i, x := range ms {
		if x.host == picked && x.up && picked != "" {
			s.machine = i
		}
	}
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

// spawnInfoCmd reads, off the update loop, the harness manifests, which of
// their programs are on PATH, and the folder's repository, branch, origin and
// uncommitted files.
func spawnInfoCmd(gen uint64, folder string) tea.Cmd {
	return func() tea.Msg {
		msg := spawnInfoMsg{gen: gen}
		if reg, _ := harness.Load(harness.UserDir()); reg != nil {
			for _, id := range reg.IDs() {
				cmd := reg.Lookup(id).Command()
				_, err := exec.LookPath(cmd)
				msg.agents = append(msg.agents, spawnAgent{id: id, cmd: cmd, installed: err == nil})
			}
		}
		if st, ok := gitstate.Read(folder); ok {
			msg.repo, msg.head = st.Repo, st.Branch
			if st.Detached {
				msg.head = ""
			}
			if d, ok := gitstate.ReadDirty(st.Root); ok {
				msg.dirty = d.Staged + d.Modified + d.Untracked
			}
			msg.origin, _ = worktree.OriginURL(folder)
		}
		return msg
	}
}

// applySpawnInfo takes what the opening command read.
func (m *OS) applySpawnInfo(msg spawnInfoMsg) {
	s := m.Inbox.ck.spawn
	if s == nil || s.gen != msg.gen {
		return
	}
	seen := map[string]int{}
	for _, a := range s.agents {
		seen[a.id] = a.seen
	}
	agents := msg.agents
	for i := range agents {
		agents[i].seen = seen[agents[i].id]
		delete(seen, agents[i].id)
	}
	for _, a := range s.agents {
		if _, ok := seen[a.id]; ok {
			agents = append(agents, a)
		}
	}
	s.agents = agents
	m.spawnSortAgents(s)
	s.read = true
	s.repo, s.head, s.origin, s.dirty = msg.repo, msg.head, msg.origin, msg.dirty
	if s.repo == "" {
		s.worktree = false
		s.attempts, s.vary = 1, nil
	}
	if s.base.Empty() && s.head != "" {
		s.base.SetText(s.head)
	}
}

// spawnCanWorktree reports whether a new worktree can be made: the folder is
// in a repository, and a repository on another machine is found by its origin.
func (s *spawnSheet) spawnCanWorktree() bool {
	if s.repo == "" {
		return false
	}
	return s.host() == "" || s.origin != ""
}

// host is the machine picked, empty for this one.
func (s *spawnSheet) host() string {
	if s.machine < len(s.machines) {
		return s.machines[s.machine].host
	}
	return ""
}

// spawnStops are the fields tab stops on, in order. The branch and the base
// are skipped while the agent works in this folder.
func (s *spawnSheet) stops() []spawnField {
	out := make([]spawnField, 0, spFields)
	for f := range spFields {
		if (f == spBranch || f == spBase) && !s.worktree {
			continue
		}
		out = append(out, f)
	}
	return out
}

// step moves the focus d stops along the tab order.
func (s *spawnSheet) step(d int) {
	stops := s.stops()
	i := max(slices.Index(stops, s.field), 0)
	s.field = stops[(i+d+len(stops))%len(stops)]
	s.list = false
}

// editor is the text field that has the keys, nil for any other field.
func (s *spawnSheet) editor() *composer.Model {
	switch s.field {
	case spTask:
		return s.task
	case spModel:
		return s.model
	case spBranch:
		return s.branch
	case spBase:
		return s.base
	}
	return nil
}

// CockpitSpawnPaste puts a paste into the text field that has the keys.
func (m *OS) CockpitSpawnPaste(text string) {
	s := m.Inbox.ck.spawn
	if s == nil {
		return
	}
	if ed := s.editor(); ed != nil {
		ed.Paste(text, m.ProcessingRemoteKeys)
		m.spawnFollowTask(s)
	}
}

// CockpitSpawnKey handles a key while the sheet is up.
func (m *OS) CockpitSpawnKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil {
		return nil
	}
	// The sheet is drawn afresh on every frame it is up, so a key on it
	// moves nothing the cockpit's own frame key holds.
	key := msg.String()
	if s.list {
		return m.spawnListKey(s, key)
	}
	switch key {
	case "tab":
		s.step(1)
		return nil
	case "shift+tab":
		s.step(-1)
		return nil
	case "ctrl+enter", "ctrl+s":
		return m.spawnStart()
	case "esc":
		m.spawnClose()
		return nil
	}
	if ed := s.editor(); ed != nil {
		if s.field != spTask && (key == "up" || key == "down") {
			d := 1
			if key == "up" {
				d = -1
			}
			s.step(d)
			return nil
		}
		switch ed.Update(msg, m.ProcessingRemoteKeys) {
		case composer.EventSend:
			return m.spawnStart()
		case composer.EventClose:
			m.spawnClose()
		}
		if s.field == spBranch {
			s.branchAuto = ""
		}
		m.spawnFollowTask(s)
		return nil
	}
	left, right := key == "left" || key == "h" || key == "-", key == "right" || key == "l" || key == "+" || key == "="
	switch s.field {
	case spAgent:
		switch {
		case left:
			s.agent = (s.agent - 1 + len(s.agents)) % max(len(s.agents), 1)
		case right:
			s.agent = (s.agent + 1) % max(len(s.agents), 1)
		case key == "space" || key == "down" || key == "j":
			s.list, s.listAt = len(s.agents) > 0, s.agent
		}
	case spWhere:
		if left || right || key == "space" {
			m.spawnSetWorktree(s, !s.worktree)
		}
	case spMachine:
		switch key {
		case "up", "k":
			m.spawnPickMachine(s, s.machine-1)
		case "down", "j":
			m.spawnPickMachine(s, s.machine+1)
		}
	case spAttempts:
		switch {
		case left:
			m.spawnSetAttempts(s, s.attempts-1)
		case right:
			m.spawnSetAttempts(s, s.attempts+1)
		case key == "v":
			m.spawnVary(s)
		}
	case spCancel:
		if left || right {
			s.field = spStart
		}
	case spStart:
		if left || right {
			s.field = spCancel
		}
	}
	switch key {
	case "enter":
		if s.field == spCancel {
			m.spawnClose()
			return nil
		}
		return m.spawnStart()
	case "space":
		switch s.field {
		case spCancel:
			m.spawnClose()
		case spStart:
			return m.spawnStart()
		}
	case "up", "k":
		if s.field != spMachine {
			s.step(-1)
		}
	case "down", "j":
		if s.field != spMachine && s.field != spAgent {
			s.step(1)
		}
	}
	return nil
}

// spawnListKey handles a key while the list of every agent is open.
func (m *OS) spawnListKey(s *spawnSheet, key string) tea.Cmd {
	switch key {
	case "up", "k":
		s.listAt = max(s.listAt-1, 0)
	case "down", "j":
		s.listAt = min(s.listAt+1, len(s.agents)-1)
	case "enter", "space":
		s.agent, s.list = s.listAt, false
	case "esc", "tab", "shift+tab":
		s.list = false
	}
	return nil
}

// spawnFollowTask names the branch after the task while the person has not
// named it.
func (m *OS) spawnFollowTask(s *spawnSheet) {
	cur := strings.TrimSpace(s.branch.Text())
	if cur != "" && cur != s.branchAuto {
		return
	}
	b := spawnBranchName(s.task.Text())
	if b != cur {
		s.branch.SetText(b)
	}
	s.branchAuto = b
}

// spawnStopWords are left out of a branch named after a task.
var spawnStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "and": true, "or": true, "for": true,
	"in": true, "on": true, "at": true, "with": true, "is": true, "are": true, "be": true, "it": true,
	"this": true, "that": true, "make": true, "please": true, "can": true, "you": true, "we": true, "i": true,
}

// spawnBranchName is a branch named after the task: agent/ and its first
// three words that say something.
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

// spawnSetWorktree sets where the agent works. This folder takes one attempt.
func (m *OS) spawnSetWorktree(s *spawnSheet, on bool) {
	switch {
	case on && !s.spawnCanWorktree():
		s.notice = spawnNoWorktree(s)
		return
	case !on && s.host() != "":
		s.notice = "On another machine the agent works in a new worktree."
		return
	}
	s.worktree, s.notice = on, ""
	if !on {
		m.spawnSetAttempts(s, 1)
	}
}

// spawnNoWorktree says why there can be no new worktree.
func spawnNoWorktree(s *spawnSheet) string {
	switch {
	case !s.read:
		return "Reading the folder. Try again in a moment."
	case s.repo == "":
		return "This folder is not in a git repository."
	}
	return "The repository has no origin remote, so " + s.host() + " cannot find it."
}

// spawnPickMachine picks machine i when it can take an agent.
func (m *OS) spawnPickMachine(s *spawnSheet, i int) {
	if i < 0 || i >= len(s.machines) || !s.machines[i].up {
		return
	}
	s.machine, s.notice = i, ""
	if s.machines[i].host != "" {
		if !s.spawnCanWorktree() {
			s.notice = spawnNoWorktree(s)
		}
		s.worktree = true
	}
}

// spawnSetAttempts sets how many attempts start, from 1 to 8. More than one
// needs new worktrees.
func (m *OS) spawnSetAttempts(s *spawnSheet, n int) {
	n = min(max(n, 1), spawnMaxAttempts)
	if n > 1 && !s.worktree {
		if !s.spawnCanWorktree() {
			s.notice = spawnNoWorktree(s)
			return
		}
		s.worktree = true
	}
	for len(s.vary) < n {
		s.vary = append(s.vary, s.agent)
	}
	s.vary, s.attempts, s.notice = s.vary[:n], n, ""
}

// spawnVary gives the attempts different agents: the installed ones and the
// running ones in turn, starting from the sheet's agent.
func (m *OS) spawnVary(s *spawnSheet) {
	var pool []int
	for i, a := range s.agents {
		if a.installed || a.seen > 0 {
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

// spawnClose closes the sheet and keeps it as the draft the next n opens.
func (m *OS) spawnClose() {
	ck := &m.Inbox.ck
	if ck.spawn != nil {
		ck.spawnDraft, ck.spawn = ck.spawn, nil
		m.cockpitTouch()
	}
}

// spawnSpec is the agent command of attempt i: the program, and the model
// when one is named and the attempt runs the sheet's agent.
func (s *spawnSheet) spec(i int) string {
	a := s.agents[s.attemptAgent(i)]
	model := strings.TrimSpace(s.model.Text())
	if model == "" || s.attemptAgent(i) != s.agent {
		return a.cmd
	}
	if strings.ContainsAny(model, " \t'\"\\") {
		model = strconv.Quote(model)
	}
	return a.cmd + " --model " + model
}

// spawnStart checks the sheet and starts the agents. The sheet closes at once
// and is kept as the draft until the daemon says the start worked, so a
// refused start loses nothing.
func (m *OS) spawnStart() tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil {
		return nil
	}
	task := strings.TrimSpace(s.task.Text())
	branch := strings.TrimSpace(s.branch.Text())
	fail := func(f spawnField, why string) tea.Cmd {
		s.field, s.notice, s.list = f, why, false
		return nil
	}
	switch {
	case task == "":
		return fail(spTask, "Write the task first.")
	case len(s.agents) == 0:
		return fail(spAgent, "No agent is known on this machine.")
	case s.worktree && !s.spawnCanWorktree():
		return fail(spWhere, spawnNoWorktree(s))
	case s.worktree && branch == "":
		return fail(spBranch, "Write a branch name.")
	case s.worktree && worktree.ValidBranch(branch) != nil:
		return fail(spBranch, "Git does not accept this branch name.")
	}
	host := s.host()
	params := map[string]any{"prompt": task}
	if host == "" {
		// The agent is looked up where this client finds programs.
		params["env"] = map[string]string{"PATH": os.Getenv("PATH")}
	}
	verb, what := "start-agent", s.agents[s.agent].cmd
	if s.worktree {
		verb = "fan"
		params["count"] = s.attempts
		params["name"] = branch
		if base := strings.TrimSpace(s.base.Text()); base != "" {
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
		params["agent"] = s.spec(0)
		if s.folder != "" {
			params["cwd"] = s.folder
		}
		if m.AttachedHost == "" && m.SessionName != "" {
			params["session"] = m.SessionName
		}
	}
	m.Inbox.ck.spawnLast = s.agents[s.agent].id
	m.spawnClose()
	where := ""
	if host != "" {
		where = " on " + host
	}
	switch {
	case !s.worktree:
		m.ShowNotification("Starting "+what+" in this folder"+where+".", "info", m.Settings.NotificationDuration)
	case s.attempts > 1:
		m.ShowNotification("Starting "+strconv.Itoa(s.attempts)+" agents in new worktrees"+where+".", "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification("Starting "+s.agents[s.agent].cmd+" in a new worktree"+where+".", "info", m.Settings.NotificationDuration)
	}
	call := m.inboxCaller()
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() tea.Msg {
		if host != "" {
			if root := spawnReposRoot(host); root != "" {
				params["repos_root"] = root
			}
			call = func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
				c, err := dialSessionDaemon(host, build)
				if err != nil {
					return nil, err
				}
				defer func() { _ = c.Close() }()
				return c.CallWithTimeout(verb, params, timeout)
			}
		}
		raw, err := call(verb, params, spawnStartTimeout)
		if err != nil {
			return spawnStartedMsg{sheet: s, err: err}
		}
		var res struct {
			Session, Branch, Reason string
			Total                   int
			Ready                   bool
			Group                   string
		}
		_ = json.Unmarshal(raw, &res)
		switch {
		case verb == "fan" && res.Total > 1:
			return spawnStartedMsg{sheet: s, text: fmt.Sprintf("Started %d agents on %s%s.", res.Total, res.Group, where)}
		case verb == "fan":
			return spawnStartedMsg{sheet: s, text: "Started " + what + where + ". The task is typed when the agent is ready."}
		case !res.Ready && res.Reason != "":
			return spawnStartedMsg{sheet: s, text: "Started " + what + " in " + res.Session + where + ". It is not ready: " + res.Reason}
		}
		return spawnStartedMsg{sheet: s, text: "Started " + what + " in " + res.Session + where + "."}
	}
}

// spawnReposRoot is [hosts.NAME] repos_root from this machine's config.
func spawnReposRoot(host string) string {
	path, err := config.GetConfigPath()
	if err != nil {
		return ""
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return ""
	}
	return hosts[host].ReposRoot
}

// applySpawnStarted says what became of a start. A start that worked drops
// its draft; one that failed keeps it for the next n.
func (m *OS) applySpawnStarted(msg spawnStartedMsg) {
	ck := &m.Inbox.ck
	if msg.err != nil {
		m.ShowNotification("The agent did not start: "+msg.err.Error()+" Press n in the agents view to change the sheet.", "error", m.Settings.NotificationDuration*2)
		return
	}
	if ck.spawnDraft == msg.sheet {
		ck.spawnDraft = nil
	}
	m.ShowNotification(msg.text, "info", m.Settings.NotificationDuration)
}

// spawnClick handles a click on the sheet. A click outside it does nothing,
// so a stray click never loses a draft.
func (m *OS) spawnClick(x, y int) tea.Cmd {
	s := m.Inbox.ck.spawn
	for _, h := range slices.Backward(s.hits) {
		if x < h.x0 || x >= h.x1 || y < h.y0 || y >= h.y1 {
			continue
		}
		name, arg, _ := strings.Cut(h.action, ":")
		n, _ := strconv.Atoi(arg)
		switch name {
		case "field":
			s.field, s.list = spawnField(n), false
		case "agent":
			s.field, s.agent, s.list = spAgent, n, false
		case "more":
			s.field, s.list, s.listAt = spAgent, !s.list, s.agent
		case "where":
			s.field = spWhere
			m.spawnSetWorktree(s, n == 0)
		case "machine":
			s.field = spMachine
			m.spawnPickMachine(s, n)
		case "fewer":
			s.field = spAttempts
			m.spawnSetAttempts(s, s.attempts-1)
		case "more-attempts":
			s.field = spAttempts
			m.spawnSetAttempts(s, s.attempts+1)
		case "vary":
			s.field = spAttempts
			m.spawnVary(s)
		case "chip":
			// A click on an attempt gives it the next agent.
			if n > 0 && n < len(s.vary) && len(s.agents) > 0 {
				s.vary[n] = (s.vary[n] + 1) % len(s.agents)
			}
		case "cancel", "close":
			m.spawnClose()
		case "start":
			return m.spawnStart()
		}
		return nil
	}
	if s.list {
		s.list = false
	}
	return nil
}
