package app

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
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
	spEffort
	spPermission
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
	// launch is the manifest's [launch] table: the model, effort and
	// permission flags the agent takes. The daemon checks the choice again.
	launch harness.Launch
}

// spawnMachine is one machine the sheet offers.
type spawnMachine struct {
	// any is the "Any machine" row: the daemon picks the machine at Start
	// (place-agent). It comes first when a link is configured.
	any bool
	// host is the [hosts] name, empty for this machine.
	host, name string
	cores      int
	load       float64
	loadOK     bool
	working    int
	up         bool
	// ready is what the machine's daemon said of each harness, by id:
	// agent-readiness. Nil until it answers; an old daemon answers nothing.
	ready map[string]session.AgentReadiness
	// readyErr says the daemon could not answer agent-readiness.
	readyErr bool
}

// spawnSheet is the open sheet, or a closed one kept as a draft.
type spawnSheet struct {
	task, model, branch, base *composer.Model
	field                     spawnField
	agents                    []spawnAgent
	agent                     int
	// list says a list is open under the field listFor: every agent, the
	// models the running agents use, or the branches. dd is the open list,
	// the TUI's one dropdown (dropdown.go).
	list    bool
	listFor spawnField
	dd      *dropdown
	// models are the models running agents report, by harness id, and
	// branches the repository's local branches.
	models   map[string][]string
	branches []string
	// starting is set from Start until the daemon answers, so one sheet
	// never starts twice at once.
	starting bool
	// shown is how many machines the last frame drew. A pick stays among
	// them. at is the row of each field's control in the last frame, and
	// atX its column, where a field's list opens.
	shown   int
	at, atX [spFields]int
	// worktree is where the agent works: a new worktree, or this folder.
	worktree bool
	// taskWires tie the task's editor to the @ list of the folder's files
	// and to the picture previews, as the reply's editor is tied.
	taskWires draftWires
	// effort and permission are the picked effort and permission mode,
	// empty for the agent's own default. A value the picked agent does not
	// list counts as empty (choice).
	effort, permission string
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
	// card is the task card n opened the sheet on, nil for none.
	card *spawnCard
	// place is what the daemon last said of "Any machine": the pick and
	// why, or placeErr. placing is set while it is asked. placed says Start
	// asked and the pick holds for this one Start: placedHost is where the
	// agents go, and placedTaskHost that machine's name for this one.
	place                      *session.PlaceAnswer
	placeErr                   string
	placing, placed            bool
	placedHost, placedTaskHost string
}

// spawnPlacedMsg is the daemon's answer to place-agent for the sheet. start
// says Start asked, and starts once the pick is in.
type spawnPlacedMsg struct {
	gen   uint64
	ans   *session.PlaceAnswer
	err   error
	start bool
}

// spawnInfoMsg carries what the sheet's opening command read, or, with ready
// set, one machine's agent-readiness answer.
type spawnInfoMsg struct {
	gen                uint64
	ready              *spawnReady
	agents             []spawnAgent
	repo, head, origin string
	dirty              int
	branches           []string
}

// spawnPreset is what ctrl+s keeps for the next new sheet: every choice but
// the task and the branch. It is saved with the rail's state.
type spawnPreset struct {
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Permission is never a mode that turns approvals off: a preset never
	// picks one for the person.
	Permission string `json:"permission,omitempty"`
	Folder     bool   `json:"this_folder,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	Host       string `json:"host,omitempty"`
}

// spawnStartedMsg is the daemon's answer to a Start.
type spawnStartedMsg struct {
	sheet *spawnSheet
	text  string
	err   error
	// warn says the agent started and its task was not sent.
	warn bool
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
	// A reply being written is kept as the pane's draft, as esc keeps it.
	if ed := m.Inbox.reply.editor; ed != nil {
		ed.closeAfter = false
		m.InboxReplyCancel()
	}
	s := ck.spawnDraft
	ck.spawnDraft = nil
	if s == nil {
		s = &spawnSheet{
			task:     composer.New(composer.Options{MaxRows: m.composerRows(spawnTaskRows), Placeholder: "What should the agent do?", Images: true, ListKey: m.composerListKey, ListKeyHint: m.composerListKeyHint}),
			model:    composer.New(composer.Options{MaxRows: 1, SingleLine: true, Placeholder: "The agent's default"}),
			branch:   composer.New(composer.Options{MaxRows: 1, SingleLine: true}),
			base:     composer.New(composer.Options{MaxRows: 1, SingleLine: true}),
			attempts: 1,
			worktree: true,
		}
		if pr := ck.spawnPreset; pr != nil {
			s.model.SetText(pr.Model)
			s.effort, s.permission = pr.Effort, pr.Permission
			s.worktree = !pr.Folder
			s.attempts = min(max(pr.Attempts, 1), spawnMaxAttempts)
			s.machines = []spawnMachine{{host: pr.Host, any: pr.Host == spawnHostAny}}
			if pr.Agent != "" {
				ck.spawnLast = pr.Agent
			}
		}
	}
	// A kept draft keeps what was read for it, so its Start works at once;
	// the command below reads it again.
	s.field, s.list, s.notice, s.card = spTask, false, "", nil
	ck.spawnGen++
	s.gen = ck.spawnGen
	s.folder = m.spawnFolder()
	m.spawnWireTask(s)
	m.spawnSeen(s)
	m.spawnRank(s)
	ck.spawn = s
	m.cockpitTouch()
	if m.LearnMode && m.learnSpawn != nil {
		// No programs and no git in the browser: the tour says what the
		// sheet offers.
		info := m.learnSpawn()
		s.folder = info.Folder
		return m.learnSpawnInfoCmd(s.gen, info)
	}
	return tea.Batch(spawnInfoCmd(s.gen, s.folder), m.spawnReadyCmd(s))
}

// spawnHostAny is the preset's host for the "Any machine" row.
const spawnHostAny = "any"

// anyPicked reports whether the "Any machine" row is picked.
func (s *spawnSheet) anyPicked() bool {
	return s.machine < len(s.machines) && s.machines[s.machine].any
}

// anyHere reports whether "Any machine" means this machine without asking:
// an agent in this folder works where the folder is.
func (s *spawnSheet) anyHere() bool { return !s.worktree }

// spawnPlaceCmd asks the daemon which machine "Any machine" picks, off the
// update loop. With start the pick is reserved and Start goes on with it.
// Nothing is asked when the row is not there.
func (m *OS) spawnPlaceCmd(s *spawnSheet, start bool) tea.Cmd {
	if len(s.machines) == 0 || !s.machines[0].any || s.agent >= len(s.agents) || m.LearnMode {
		return nil
	}
	params := map[string]any{"agent": s.agents[s.agent].cmd, "reserve": start}
	if s.origin != "" {
		params["repo_url"], params["local_origin"] = s.origin, s.origin
	}
	if c := s.card; c != nil {
		params["task"], params["task_session"] = c.id, c.session
	}
	if m.AttachedHost == "" {
		params["env"] = map[string]string{"PATH": os.Getenv("PATH")}
	}
	call, gen := m.inboxCaller(), s.gen
	s.placing = true
	return func() tea.Msg {
		ans, err := placeCall(call, params)
		if err != nil {
			return spawnPlacedMsg{gen: gen, err: err, start: start}
		}
		return spawnPlacedMsg{gen: gen, ans: &ans, start: start}
	}
}

// applySpawnPlaced takes the daemon's pick. A pick Start asked for starts the
// agents there.
func (m *OS) applySpawnPlaced(msg spawnPlacedMsg) tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil || s.gen != msg.gen {
		return nil
	}
	s.placing = false
	if msg.err != nil {
		why := msg.err.Error()
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.err); ok {
			why = callErr.Message
		}
		s.place, s.placeErr = nil, printableTitle(why)
		if msg.start {
			s.starting, s.field, s.notice = false, spMachine, "No machine can start the agent now. Pick a machine, or fix one."
		}
		return nil
	}
	s.place, s.placeErr = msg.ans, ""
	if !msg.start {
		return nil
	}
	s.starting = false
	s.placed, s.placedHost, s.placedTaskHost = true, msg.ans.Host, msg.ans.TaskHost
	return m.spawnStart()
}

// spawnWireTask ties the task's editor to the @ list of the folder the agent
// starts in, and to the picture previews. It is done at each opening, since
// the folder is read again.
func (m *OS) spawnWireTask(s *spawnSheet) {
	s.task.SetPreviewer(m.replyPreviewer())
	c := m.newReplyMentions("", "")
	if !m.BrowserClient && (!m.RemoteClient || m.SSHIsLoopback) {
		c.dir = s.folder
	}
	s.task.SetCompleter(c)
	s.taskWires = draftWires{mentions: c}
}

// spawnStashImages puts each image of the task into session's file store
// and returns the task with each image as its stored path, or "" when the
// task holds no image.
func spawnStashImages(call inboxVerbCall, sess string, parts []composer.Part) (string, error) {
	if !slices.ContainsFunc(parts, func(p composer.Part) bool { return p.Kind == composer.PartImage && p.Image != nil }) {
		return "", nil
	}
	var b strings.Builder
	for i, p := range parts {
		if p.Kind != composer.PartImage || p.Image == nil {
			b.WriteString(p.Text)
			continue
		}
		name := p.Image.Name
		if name == "" {
			name = "image-" + strconv.Itoa(i+1) + "." + cmp.Or(p.Image.Format, "png")
		}
		raw, err := call("stash-put", map[string]any{"session": sess, "path": name, "content": base64.StdEncoding.EncodeToString(p.Image.Data)}, 30*time.Second)
		if err != nil {
			return "", errfmt.Errorf("keep the image %s for the agent: %w", name, err)
		}
		var res struct {
			Path string `json:"path"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil || res.Path == "" {
			return "", errfmt.Errorf("keep the image %s for the agent: no stored path", name)
		}
		// The path is a word of its own in the task.
		if t := b.String(); t != "" && !strings.HasSuffix(t, " ") && !strings.HasSuffix(t, "\n") {
			b.WriteByte(' ')
		}
		b.WriteString(res.Path)
		b.WriteByte(' ')
	}
	return strings.TrimSpace(b.String()), nil
}

// spawnReady is one machine's agent-readiness answer.
type spawnReady struct {
	host   string
	agents map[string]session.AgentReadiness
	// err says the daemon gave no answer: an old daemon has no such verb.
	err bool
}

// spawnReadyTimeout bounds one machine's answer. The daemon bounds each
// check to 2 seconds and runs them at once.
const spawnReadyTimeout = 5 * time.Second

// spawnReadyCmd asks each machine's daemon which harnesses are installed
// and signed in there, off the update loop, one command a machine. A row
// says "checking" until its answer lands.
func (m *OS) spawnReadyCmd(s *spawnSheet) tea.Cmd {
	call, gen := m.inboxCaller(), s.gen
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	// The PATH goes only to the daemon this client runs beside, as Start
	// sends it.
	local := m.AttachedHost == ""
	var cmds []tea.Cmd
	for _, mc := range s.machines {
		if (!mc.up && mc.host != "") || mc.any {
			continue
		}
		host := mc.host
		cmds = append(cmds, func() tea.Msg {
			params := map[string]any{}
			ask := call
			if host == "" && local {
				params["env"] = map[string]string{"PATH": os.Getenv("PATH")}
			}
			if host != "" {
				ask = hostCaller(host, build)
			}
			r := &spawnReady{host: host}
			var res struct {
				Agents []session.AgentReadiness `json:"agents"`
			}
			raw, err := ask("agent-readiness", params, spawnReadyTimeout)
			if err != nil || jsonx.Unmarshal(raw, &res) != nil {
				r.err = true
				return spawnInfoMsg{gen: gen, ready: r}
			}
			r.agents = make(map[string]session.AgentReadiness, len(res.Agents))
			for _, a := range res.Agents {
				r.agents[a.Harness] = a
			}
			return spawnInfoMsg{gen: gen, ready: r}
		})
	}
	return tea.Batch(cmds...)
}

// installedThere reports whether the picked agent is on the picked
// machine: as its daemon said, or, before it answers, as this client finds
// it for this machine. A far daemon that has not answered checks it itself.
func (s *spawnSheet) installedThere() bool {
	if s.agent >= len(s.agents) {
		return false
	}
	a := s.agents[s.agent]
	if s.machine < len(s.machines) {
		if mc := s.machines[s.machine]; mc.ready != nil {
			return mc.ready[a.id].Installed
		}
	}
	return s.host() != "" || a.installed
}

// machineName is the picked machine as a person reads it.
func (s *spawnSheet) machineName() string {
	if s.anyPicked() && s.placed && s.place != nil {
		return s.place.Name
	}
	if h := s.host(); h != "" {
		return h
	}
	return "this machine"
}

// spawnReadyWords is what machine mc says of the agent with id and cmd:
// "claude ready", "claude not signed in", "claude not installed", or
// "checking" until it answers. ok is false for a word that warns.
func spawnReadyWords(mc spawnMachine, id, cmd string) (words string, ok bool) {
	switch {
	case mc.readyErr:
		return cmd + " not known", false
	case mc.ready == nil:
		return "checking " + cmd, true
	}
	r, found := mc.ready[id]
	switch {
	case !found || !r.Installed:
		return cmd + " not installed", false
	case r.SignedIn == session.SignedInNo:
		return cmd + " not signed in", false
	case r.SignedIn == session.SignedInUnknown:
		return cmd + " installed", true
	}
	return cmd + " ready", true
}

// spawnFolder is the directory "this folder" means: the folder the focused
// pane's shell is in, or the client's own when that cannot be read. The sheet
// runs git there and Start makes a worktree of it, so an OSC 7 report alone
// never chooses it (paneShellDir).
func (m *OS) spawnFolder() string {
	if w := m.GetFocusedWindow(); w != nil && m.paneIsLocal(w) {
		if dir, ok := paneShellDir(w, true); ok {
			return dir
		}
	}
	dir, _ := os.Getwd()
	return dir
}

// spawnSeen fills the agents from the panes that run one now, most first,
// keeping the one picked.
func (m *OS) spawnSeen(s *spawnSheet) {
	count := map[string]int{}
	s.models = map[string][]string{}
	for _, e := range m.agentEntries(m.BuildSessionTree().Sessions) {
		if e.Harness == "" {
			continue
		}
		count[e.Harness]++
		if mod := printableTitle(ckMeta(e, "model")); mod != "" && !slices.Contains(s.models[e.Harness], mod) {
			s.models[e.Harness] = append(s.models[e.Harness], mod)
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
		case slices.Contains(spawnCommon, a.id):
			return 2
		}
		return 3
	}
	// Among agents that do not run, the common ones come first, in the
	// order of frame d, then the rest by name.
	common := func(a spawnAgent) int {
		if i := slices.Index(spawnCommon, a.id); i >= 0 {
			return i
		}
		return len(spawnCommon)
	}
	sortx.SortStableFunc(s.agents, func(a, b spawnAgent) int {
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
	picked, pickedAny := "", false
	if s.machine < len(s.machines) {
		picked, pickedAny = s.machines[s.machine].host, s.machines[s.machine].any
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
	// While attached, the tree's own sessions are the far machine's, and
	// this machine's come in under the local host name.
	hostWorking := func(host string) int {
		switch {
		case m.AttachedHost == "":
			return working[host]
		case host == "":
			return working[federation.LocalHostName]
		case host == m.AttachedHost:
			return working[""]
		}
		return working[host]
	}
	local := spawnMachine{name: cmp.Or(name, "this machine"), cores: runtime.NumCPU(), working: hostWorking(""), up: true}
	// The local row is the machine of the client's own daemon, where an
	// agent with no host starts, attached to another machine or not. Its
	// load is the one that daemon reported, on every system.
	if l := m.LocalLoad; l.Cores > 0 {
		// A load the daemon could not read ranks as unknown, not as idle.
		local.cores = l.Cores
		local.load, local.loadOK = l.Load1, l.Known()
	} else {
		local.load, local.loadOK = spawnLoadAvg()
	}
	if m.LearnMode && m.learnSpawn != nil {
		// A browser tab has no cores or load to rank by, and its host name
		// is the runtime's.
		local.name, local.cores, local.load, local.loadOK = cmp.Or(m.learnSpawn().Machine, local.name), 0, 0, false
	}
	ms := []spawnMachine{local}
	for _, h := range m.FederationHosts {
		if h.Name == federation.LocalHostName {
			continue
		}
		sm := spawnMachine{host: h.Name, name: h.Name, working: hostWorking(h.Name), up: h.Status == string(federation.StatusUp)}
		if h.Cores > 0 {
			// The load the link's last hello carried, so the sheet ranks
			// the machine with no call of its own.
			sm.cores, sm.load, sm.loadOK = h.Cores, h.Load1, h.LoadOK
		}
		ms = append(ms, sm)
	}
	// Spare cores: the cores, held to spawnCoresMax so a host cannot win by
	// claiming more, times the share of them the load leaves free.
	score := func(x spawnMachine) float64 {
		cores := float64(min(max(x.cores, 1), spawnCoresMax))
		free := 1 - min(max(x.load/float64(max(x.cores, 1)), 0), 1)
		return cores*free - 0.5*float64(x.working)
	}
	sortx.SortStableFunc(ms, func(a, b spawnMachine) int {
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
	// "Any machine" comes first when there is another machine to pick. A
	// person picks it: the default is this machine, so a new agent never
	// moves to another machine unless asked. A kept pick (the draft or the
	// preset) holds.
	if len(ms) > 1 && !m.LearnMode {
		ms = append([]spawnMachine{{any: true, name: "Any machine", up: true}}, ms...)
	}
	s.machines, s.machine = ms, 0
	for i, x := range ms {
		switch {
		case x.any && pickedAny:
			s.machine = i
		case !x.any && x.up && x.host == picked && !pickedAny:
			s.machine = i
		}
	}
}

// spawnCoresMax bounds the cores a machine is ranked by.
const spawnCoresMax = 256

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
				mf := reg.Lookup(id)
				cmd := mf.Command()
				_, err := exec.LookPath(cmd)
				msg.agents = append(msg.agents, spawnAgent{id: id, cmd: cmd, installed: err == nil, launch: mf.Launch})
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
			msg.branches = spawnBranches(st.Root)
		}
		return msg
	}
}

// spawnBranches are the repository's local branches, the most recent first,
// at most 30.
func spawnBranches(root string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := childproc.Git(ctx, root, "for-each-ref", "--sort=-committerdate", "--count=30",
		"--format=%(refname:short)", "refs/heads").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// applySpawnInfo takes what the opening command read. Once the agents and
// the repository are read, "Any machine" is asked for its pick.
func (m *OS) applySpawnInfo(msg spawnInfoMsg) tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil || s.gen != msg.gen {
		return nil
	}
	if r := msg.ready; r != nil {
		for i := range s.machines {
			if s.machines[i].host == r.host && !s.machines[i].any {
				s.machines[i].ready, s.machines[i].readyErr = r.agents, r.err
			}
		}
		return nil
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
	s.repo, s.head, s.origin, s.dirty, s.branches = msg.repo, msg.head, msg.origin, msg.dirty, msg.branches
	if s.repo == "" {
		s.worktree = false
		s.attempts, s.vary = 1, nil
	}
	if s.base.Empty() && s.head != "" {
		s.base.SetText(s.head)
	}
	return m.spawnPlaceCmd(s, false)
}

// spawnCanWorktree reports whether a new worktree can be made: the folder is
// in a repository, and a repository on another machine is found by its origin.
func (s *spawnSheet) spawnCanWorktree() bool {
	if s.repo == "" {
		return false
	}
	return s.host() == "" || s.origin != ""
}

// host is the machine picked, empty for this one. For "Any machine" it is
// the daemon's pick once Start asked, and this machine before.
func (s *spawnSheet) host() string {
	if s.machine < len(s.machines) {
		if s.machines[s.machine].any {
			return s.placedHost
		}
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
		// A choice the picked agent cannot take has no stop.
		l := s.launch()
		if (f == spModel && l.ModelFlag == "") || (f == spEffort && l.EffortFlag == "") || (f == spPermission && l.PermissionFlag == "") {
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

// CockpitSpawnPaste puts a paste into the text field that has the keys. In
// the task, a pasted image path becomes the image, as in a reply.
func (m *OS) CockpitSpawnPaste(text string) tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil {
		return nil
	}
	ed := s.editor()
	if ed == nil {
		return nil
	}
	defer m.spawnFollowTask(s)
	if ed == s.task {
		return m.composerPasteCmd(ed, "", text)
	}
	ed.Paste(text, m.ProcessingRemoteKeys)
	return nil
}

// CockpitSpawnKey handles a key while the sheet is up. A key that changes
// the agent asks "Any machine" again, since the pick depends on it.
func (m *OS) CockpitSpawnKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil {
		return nil
	}
	before := s.agentID()
	cmd := m.spawnKey(s, msg)
	if s2 := m.Inbox.ck.spawn; s2 == s && s.agentID() != before && !s.starting {
		return tea.Batch(cmd, m.spawnPlaceCmd(s, false))
	}
	return cmd
}

// agentID is the picked agent's id, "" for none.
func (s *spawnSheet) agentID() string {
	if s.agent < len(s.agents) {
		return s.agents[s.agent].id
	}
	return ""
}

// spawnKey is CockpitSpawnKey's work.
func (m *OS) spawnKey(s *spawnSheet, msg tea.KeyPressMsg) tea.Cmd {
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
	case "ctrl+enter":
		return m.spawnStart()
	case "ctrl+s":
		m.spawnSavePreset(s)
		return nil
	case "esc":
		m.spawnClose()
		return nil
	}
	if ed := s.editor(); ed != nil {
		if s.field != spTask && (key == "up" || key == "down") {
			// down on the model or the base opens its list, when it has one.
			if key == "down" && len(s.listItems(s.field)) > 0 {
				s.openList(s.field)
				return nil
			}
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
		case composer.EventCopy, composer.EventCut:
			return m.CopyFromEditor(ed.Copied())
		case composer.EventPaste:
			// ctrl+v: the clipboard's image into the task, as in a
			// reply, or its text into any field.
			if ed == s.task {
				return m.composerClipboardCmd(ed, "")
			}
			return m.RequestHostPaste()
		}
		m.spawnFollowTask(s)
		return nil
	}
	left, right := key == "left" || key == "h" || key == "-", key == "right" || key == "l" || key == "+" || key == "="
	// Up and down are the list keys of [keybindings.lists], letters included:
	// a field that types text took its keys above.
	motion := m.ListKeys.Keys(key, true)
	up, down := motion == listnav.Up, motion == listnav.Down
	switch s.field {
	case spAgent:
		switch {
		case left:
			s.agent = (s.agent - 1 + len(s.agents)) % max(len(s.agents), 1)
		case right:
			s.agent = (s.agent + 1) % max(len(s.agents), 1)
		case key == "space" || down:
			s.openList(spAgent)
		}
	case spEffort, spPermission:
		switch {
		case left:
			s.cycleChoice(s.field, -1)
		case right:
			s.cycleChoice(s.field, 1)
		case key == "space" || down:
			s.openList(s.field)
			return nil
		}
	case spWhere:
		if left || right || key == "space" {
			m.spawnSetWorktree(s, !s.worktree)
		}
	case spMachine:
		switch {
		case up:
			m.spawnPickMachine(s, s.machine-1)
		case down:
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
	}
	switch {
	case up:
		if s.field != spMachine {
			s.step(-1)
		}
	case down:
		if s.field != spMachine && s.field != spAgent && s.field != spEffort && s.field != spPermission {
			s.step(1)
		}
	}
	return nil
}

// spawnItem is one row of a field's list.
type spawnItem struct {
	label, note, value string
	muted              bool
}

// listItems are the rows of field f's list: every agent, the models the
// running agents of the picked agent report, or the local branches.
func (s *spawnSheet) listItems(f spawnField) []spawnItem {
	var out []spawnItem
	switch f {
	case spAgent:
		for _, a := range s.agents {
			it := spawnItem{label: a.cmd, note: "not found on this machine", muted: true}
			switch {
			case a.seen > 0:
				it.note, it.muted = strconv.Itoa(a.seen)+" running", false
			case a.installed:
				it.note, it.muted = "installed", false
			}
			out = append(out, it)
		}
	case spModel:
		if s.agent < len(s.agents) {
			running := s.models[s.agents[s.agent].id]
			known := s.agents[s.agent].launch.Models
			if len(running)+len(known) > 0 {
				out = append(out, spawnItem{label: "The agent's default", note: "no model flag"})
				for _, mod := range running {
					out = append(out, spawnItem{label: mod, value: mod, note: "from a running agent"})
				}
				for _, mod := range known {
					if !slices.Contains(running, mod) {
						out = append(out, spawnItem{label: mod, value: mod, note: "the agent takes it"})
					}
				}
			}
		}
	case spEffort, spPermission:
		values, def, note := s.choices(f)
		out = append(out, spawnItem{label: def, note: note})
		l := s.launch()
		for _, v := range values {
			it := spawnItem{label: v, value: v}
			if f == spPermission && l.RiskyPermission(v) {
				it.note = "approvals off"
			}
			out = append(out, it)
		}
	case spBase:
		for _, b := range s.branches {
			it := spawnItem{label: b, value: b}
			if b == s.head {
				it.note = "this folder"
			}
			out = append(out, it)
		}
	}
	return out
}

// openList opens field f's list, with the cursor on what the field holds.
func (s *spawnSheet) openList(f spawnField) {
	items := s.listItems(f)
	if len(items) == 0 {
		return
	}
	s.field, s.list, s.listFor = f, true, f
	at := 0
	defer func() {
		dd := make([]ddItem, len(items))
		for i, it := range items {
			dd[i] = ddItem{label: it.label, note: it.note, muted: it.muted, current: i == at}
		}
		s.dd = newDropdown(dd)
	}()
	cur := ""
	switch f {
	case spAgent:
		at = s.agent
		return
	case spModel:
		cur = strings.TrimSpace(s.model.Text())
	case spEffort, spPermission:
		cur = s.choice(f)
	case spBase:
		cur = strings.TrimSpace(s.base.Text())
	}
	for i, it := range items {
		if it.value == cur {
			at = i
		}
	}
}

// pickList takes row i of the open list.
func (m *OS) pickList(s *spawnSheet, i int) {
	items := s.listItems(s.listFor)
	s.list = false
	if i < 0 || i >= len(items) {
		return
	}
	switch s.listFor {
	case spAgent:
		s.agent = i
	case spModel:
		s.model.SetText(items[i].value)
	case spEffort:
		s.effort = items[i].value
	case spPermission:
		s.permission = items[i].value
	case spBase:
		s.base.SetText(items[i].value)
	}
}

// spawnTaskRows is how many rows the task's editor shows before it scrolls.
const spawnTaskRows = 6

// spawnTaskBoxRows is how tall the sheet draws the task box when [agents]
// reply_rows is unset. The editor scrolls inside it.
const spawnTaskBoxRows = 3

// launch is the picked agent's [launch] table.
func (s *spawnSheet) launch() harness.Launch {
	if s.agent < len(s.agents) {
		return s.agents[s.agent].launch
	}
	return harness.Launch{}
}

// choices are the values field f offers for the picked agent, with the
// label and note of its default, the value that passes no flag.
func (s *spawnSheet) choices(f spawnField) (values []string, def, note string) {
	l := s.launch()
	if f == spEffort {
		return l.Efforts, "default", "the agent's own"
	}
	return l.Permissions, "default", "your settings for the agent"
}

// choice is the picked value of field f, empty when the picked agent does
// not list it: a value picked for another agent passes no flag to this one.
func (s *spawnSheet) choice(f spawnField) string {
	values, _, _ := s.choices(f)
	v := s.effort
	if f == spPermission {
		v = s.permission
	}
	if !slices.Contains(values, v) {
		return ""
	}
	return v
}

// cycleChoice steps field f's value d places, the default first.
func (s *spawnSheet) cycleChoice(f spawnField, d int) {
	values, _, _ := s.choices(f)
	all := append([]string{""}, values...)
	i := max(slices.Index(all, s.choice(f)), 0)
	v := all[(i+d+len(all))%len(all)]
	if f == spEffort {
		s.effort = v
	} else {
		s.permission = v
	}
}

// launchChoice is attempt A's choice as harness.LaunchArgs takes it.
func (s *spawnSheet) launchChoice() harness.LaunchChoice {
	model := ""
	if s.launch().ModelFlag != "" {
		model = strings.TrimSpace(s.model.Text())
	}
	return harness.LaunchChoice{Model: model, Effort: s.choice(spEffort), Permission: s.choice(spPermission)}
}

// spawnListKey handles a key while a field's list is open: the dropdown's
// keys, and tab or shift+tab, which close it to move on. Any other key is
// spent, so a letter typed while the list is open does not reach a field.
func (m *OS) spawnListKey(s *spawnSheet, key string) tea.Cmd {
	if key == "tab" || key == "shift+tab" || s.dd == nil {
		s.list = false
		return nil
	}
	switch i, what := s.dd.Key(key, false, m.ListKeys); what {
	case ddKeyPicked:
		m.pickList(s, i)
	case ddKeyClosed:
		s.list = false
	}
	return nil
}

// spawnSavePreset keeps the sheet's choices, but not the task or the
// branch, for the next new sheet.
func (m *OS) spawnSavePreset(s *spawnSheet) {
	pr := &spawnPreset{Model: strings.TrimSpace(s.model.Text()), Effort: s.choice(spEffort), Permission: s.choice(spPermission),
		Folder: !s.worktree, Attempts: s.attempts, Host: s.host()}
	if s.anyPicked() {
		pr.Host = spawnHostAny
	}
	if s.launch().RiskyPermission(pr.Permission) {
		pr.Permission = ""
	}
	if s.agent < len(s.agents) {
		pr.Agent = s.agents[s.agent].id
	}
	m.Inbox.ck.spawnPreset = pr
	m.saveSidebarState()
	s.notice = ""
	m.ShowNotification("Saved as the preset. A new sheet starts with these choices.", "info", m.Settings.NotificationDuration)
}

// spawnFollowTask names the branch after the task while the person has not
// named it.
func (m *OS) spawnFollowTask(s *spawnSheet) {
	cur := strings.TrimSpace(s.branch.Text())
	if cur != "" && cur != s.branchAuto {
		return
	}
	b := spawnFreeBranch(spawnBranchName(s.task.Text()), s.branches)
	if b != cur {
		s.branch.SetText(b)
	}
	s.branchAuto = b
}

// spawnFreeBranch is name, or name-2, name-3 and on when the repository has
// a branch of that name already: a worktree cannot check out a branch that
// exists, so the name the task gives would fail at Start.
func spawnFreeBranch(name string, have []string) string {
	if name == "" || !slices.Contains(have, name) {
		return name
	}
	for n := 2; n < 100; n++ {
		if c := name + "-" + strconv.Itoa(n); !slices.Contains(have, c) {
			return c
		}
	}
	return name
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
	if i < 0 || i >= len(s.machines) || !s.machines[i].up || (s.shown > 0 && i >= s.shown) {
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

// modelOf is the model attempt i runs: the sheet's model for an attempt
// of the sheet's agent, none for an attempt of another agent or for an
// agent that takes no model.
func (s *spawnSheet) modelOf(i int) string {
	if s.attemptAgent(i) != s.agent {
		return ""
	}
	return s.launchChoice().Model
}

// spec is the agent command of attempt i as fan reads it: the program, and
// for attempt A's agent the model, effort and permission mode in the flags
// its manifest names, each as one shell word. The other attempts run other
// agents, which take their own defaults. fan splits the string the way a
// shell does and expands nothing; spawnStart checked the choice already.
func (s *spawnSheet) spec(i int) string {
	cmd := s.agents[s.attemptAgent(i)].cmd
	if s.attemptAgent(i) != s.agent {
		return cmd
	}
	args, _ := harness.LaunchArgs(&harness.Manifest{DisplayName: cmd, Launch: s.launch()}, s.launchChoice())
	for _, a := range args {
		cmd += " " + shquote.Quote(a)
	}
	return cmd
}

// spawnStart checks the sheet and starts the agents. The sheet closes at once
// and is kept as the draft until the daemon says the start worked, so a
// refused start loses nothing.
func (m *OS) spawnStart() tea.Cmd {
	s := m.Inbox.ck.spawn
	if s == nil {
		return nil
	}
	fail := func(f spawnField, why string) tea.Cmd {
		s.field, s.notice, s.list = f, why, false
		s.placed, s.placedHost, s.placedTaskHost = false, "", ""
		return nil
	}
	if _, ok := s.task.Submit(); !ok {
		// Invisible characters were removed before any machine is asked;
		// the task field says so, and the next Start sends what it shows.
		return fail(spTask, "")
	}
	task := strings.TrimSpace(s.task.Text())
	branch := strings.TrimSpace(s.branch.Text())
	if s.anyPicked() && !s.anyHere() && !s.placed {
		// The daemon picks the machine first, then Start goes on there.
		switch {
		case s.starting || s.placing:
			return fail(spStart, "This sheet is starting its agents. Wait for the result.")
		case !s.read:
			return fail(spStart, "Reading the folder and the agents. Try again in a moment.")
		case task == "":
			return fail(spTask, "Write the task first.")
		case len(s.agents) == 0:
			return fail(spAgent, "No agent is known on this machine.")
		}
		s.starting, s.notice = true, "Finding the machine with the most room."
		return m.spawnPlaceCmd(s, true)
	}
	placedTaskHost := s.placedTaskHost
	defer func() { s.placed, s.placedHost, s.placedTaskHost = false, "", "" }()
	base := strings.TrimSpace(s.base.Text())
	model := strings.TrimSpace(s.model.Text())
	switch {
	case s.starting:
		return fail(spStart, "This sheet is starting its agents. Wait for the result.")
	case !s.read:
		// Until the manifests are read, an agent is known only by its id,
		// which is not always a program.
		return fail(spStart, "Reading the folder and the agents. Try again in a moment.")
	case task == "":
		return fail(spTask, "Write the task first.")
	case len(ckDraftImages(s.task)) > 0 && s.host() != "":
		return fail(spTask, "An image goes only to an agent on this machine. Remove the image, or pick this machine.")
	case len(ckDraftImages(s.task)) > 0 && m.SessionName == "":
		return fail(spTask, "An image needs a session to keep it in. Remove the image.")
	case len(s.agents) == 0:
		return fail(spAgent, "No agent is known on this machine.")
	case !s.installedThere():
		return fail(spAgent, s.agents[s.agent].cmd+" is not installed on "+s.machineName()+".")
	case s.launch().ModelFlag != "" && model != "" && !harness.LaunchValueOK(model):
		return fail(spModel, "A model name is one word and cannot start with -.")
	case s.worktree && !s.spawnCanWorktree():
		return fail(spWhere, spawnNoWorktree(s))
	case s.worktree && branch == "":
		return fail(spBranch, "Write a branch name.")
	case s.worktree && strings.IndexFunc(branch, func(r rune) bool { return !session.FanNameChar(r) }) >= 0:
		return fail(spBranch, "A branch name can have only the letters A to Z and a to z, the digits 0 to 9, and / . _ -.")
	case s.worktree && !session.FanNameRefOK(branch):
		return fail(spBranch, "Git does not accept this branch name.")
	case s.worktree && strings.HasPrefix(base, "-"):
		return fail(spBase, "A base cannot start with -.")
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
		// start-agent maps each choice to the agent's own flag, and
		// refuses one the agent cannot take.
		params["agent"] = s.agents[s.agent].cmd
		ch := s.launchChoice()
		for k, v := range map[string]string{"model": ch.Model, "effort": ch.Effort, "permission_mode": ch.Permission} {
			if v != "" {
				params[k] = v
			}
		}
		if s.folder != "" {
			params["cwd"] = s.folder
		}
		if m.AttachedHost == "" && m.SessionName != "" {
			params["session"] = m.SessionName
		}
	}
	m.Inbox.ck.spawnLast = s.agents[s.agent].id
	s.starting, s.notice = true, ""
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
	call, nonce, card := m.inboxCaller(), m.inboxNonce(), s.card
	parts, stashIn := s.task.Parts(), m.SessionName
	if host != "" {
		// A card on this machine goes with an agent on another one only
		// through start-agent and that machine's link back (task_host).
		if card != nil && verb == "start-agent" && placedTaskHost != "" {
			params["task"], params["task_host"], params["task_session"] = card.id, placedTaskHost, card.session
		}
		card = nil
	}
	taskID := ""
	if card != nil {
		taskID = card.id
	}
	// The daemon takes the card in the call: start-agent claims it for the
	// pane it opens, and fan stamps it with the fan it makes. A card of
	// another session than the one the agent starts in is claimed after, as
	// before, or not at all for a fan.
	switch {
	case card == nil:
	case verb == "fan":
		params["task"], params["task_session"], params["human_nonce"] = card.id, card.session, nonce
		card = nil
	case params["session"] == card.session:
		params["task"], params["human_nonce"] = card.id, nonce
	}
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() tea.Msg {
		if host != "" {
			if root := config.HostReposRoot(host); root != "" {
				params["repos_root"] = root
			}
			call = hostCaller(host, build)
		}
		// Each image goes to the session's file store first, and the task
		// names it by the stored path, which the agent reads as a file.
		if prompt, err := spawnStashImages(call, stashIn, parts); err != nil {
			return spawnStartedMsg{sheet: s, err: err}
		} else if prompt != "" {
			params["prompt"] = prompt
		}
		raw, err := call(verb, params, spawnStartTimeout)
		// A daemon from before the task parameter refuses it by name, and a
		// card that cannot be taken refuses the call. The agent starts
		// either way, and the card is claimed after, which says why it was
		// not taken.
		var callErr *session.VerbCallError
		leftOut := ""
		if err != nil && params["task"] != nil && errors.As(err, &callErr) && taskParamRefused(callErr) {
			// admitTaskParam's message starts with the verb and names
			// the card; the started line names both already.
			leftOut = strings.TrimPrefix(callErr.Message, verb+" with task: ")
			if rest, ok := strings.CutPrefix(leftOut, "task card "+taskID+" "); ok {
				leftOut = "it " + rest
			}
			delete(params, "task")
			delete(params, "task_session")
			delete(params, "human_nonce")
			raw, err = call(verb, params, spawnStartTimeout)
		} else if params["task"] != nil && verb == "start-agent" {
			card = nil
		}
		if err != nil {
			return spawnStartedMsg{sheet: s, err: err}
		}
		var res struct {
			Session, Branch, Reason string
			Total                   int
			Ready                   bool
			Group                   string
			PromptStatus            string           `json:"prompt_status"`
			PromptNote              string           `json:"prompt_note"`
			WindowID                string           `json:"window_id"`
			Task                    *session.TaskRow `json:"task"`
			TaskNote                string           `json:"task_note"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return spawnStartedMsg{sheet: s, err: errfmt.Errorf("read the %s answer: %w", verb, err)}
		}
		held := spawnClaim(call, nonce, card, res.Session, res.WindowID)
		switch {
		case res.Task != nil && verb == "start-agent":
			held = " It holds " + res.Task.ID + "."
		case res.Task != nil:
			held = " They work on " + res.Task.ID + "."
		case res.TaskNote != "":
			held = " " + printableTitle(res.TaskNote)
		case leftOut != "" && verb == "fan":
			// A fan claims nothing after, so this is the one place the
			// person hears the card was left out.
			held = " " + taskID + " was left out: " + printableTitle(leftOut)
		}
		switch {
		case verb == "fan" && res.Total > 1:
			return spawnStartedMsg{sheet: s, text: fmt.Sprintf("Started %d agents on %s%s.", res.Total, res.Group, where) + held}
		case verb == "fan":
			return spawnStartedMsg{sheet: s, text: "Started " + what + where + ". The task is typed when the agent is ready." + held}
		case !res.Ready && res.Reason != "":
			return spawnStartedMsg{sheet: s, warn: true, text: "Started " + what + " in " + res.Session + where + ". It is not ready: " + res.Reason + held}
		case res.PromptStatus != "" && res.PromptStatus != session.PromptSent:
			// The agent is up and its task did not go through: the
			// notice says so, and not that all went well.
			return spawnStartedMsg{sheet: s, warn: true, text: "Started " + what + " in " + res.Session + where + ". The task was not sent: " + res.PromptNote + held}
		}
		return spawnStartedMsg{sheet: s, text: "Started " + what + " in " + res.Session + where + "." + held}
	}
}

// applySpawnStarted says what became of a start. A start that worked drops
// its draft; one that failed keeps it for the next n.
func (m *OS) applySpawnStarted(msg spawnStartedMsg) {
	ck := &m.Inbox.ck
	msg.sheet.starting = false
	if msg.err != nil {
		why := strings.TrimSpace(msg.err.Error())
		if !strings.HasSuffix(why, ".") {
			why += "."
		}
		m.ShowNotification("The agent did not start: "+why+" Press n in the agents view to change the sheet.", "error", m.Settings.NotificationDuration*2)
		return
	}
	if ck.spawnDraft == msg.sheet {
		ck.spawnDraft = nil
	}
	if ck.spawn == msg.sheet {
		// The sheet was open again: its task has started, so enter must
		// not start it twice.
		msg.sheet.task.Clear()
		msg.sheet.branch.Clear()
		msg.sheet.branchAuto = ""
	}
	if msg.warn {
		m.ShowNotification(msg.text, "warning", m.Settings.NotificationDuration*2)
		return
	}
	m.ShowNotification(msg.text, "info", m.Settings.NotificationDuration)
}

// spawnClick handles a click on the sheet. A click outside it does nothing,
// so a stray click never loses a draft.
func (m *OS) spawnClick(x, y int) tea.Cmd {
	s := m.Inbox.ck.spawn
	// An open list takes the click first: a row picks, the rest of its box
	// does nothing, and a click anywhere else closes it and does nothing
	// else, but on the control that opened it, which closes it too.
	if s.list && s.dd != nil {
		row, inside := s.dd.RowAt(x, y)
		switch {
		case row >= 0:
			m.pickList(s, row)
		case !inside:
			s.list = false
		}
		return nil
	}
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
		case "list":
			if s.list && s.listFor == spawnField(n) {
				s.list = false
			} else {
				s.openList(spawnField(n))
			}
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

// taskParamRefused reports whether a refusal names one of the parameters the
// sheet sends for a card. An older daemon names whichever unknown one it read
// first.
func taskParamRefused(err *session.VerbCallError) bool {
	if err.Hint == nil {
		return false
	}
	switch err.Hint.Param {
	case "task", "task_session", "human_nonce":
		return true
	}
	return false
}

// hostCaller calls verbs on host's daemon, one connection a call, through
// this machine's daemon and its link.
func hostCaller(host, build string) inboxVerbCall {
	return func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
		c, err := dialSessionDaemon(host, build)
		if err != nil {
			return nil, err
		}
		defer func() { _ = c.Close() }()
		return c.CallWithTimeout(verb, params, timeout)
	}
}

// placeCall asks the daemon behind call to place an agent (place-agent).
func placeCall(call inboxVerbCall, params map[string]any) (session.PlaceAnswer, error) {
	var ans session.PlaceAnswer
	raw, err := call("place-agent", params, 20*time.Second)
	if err == nil {
		err = jsonx.Unmarshal(raw, &ans)
	}
	return ans, err
}
