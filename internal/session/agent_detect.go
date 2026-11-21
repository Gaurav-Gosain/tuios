package session

import (
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

var defaultAgentBinaries = []string{
	"claude",
	"claude-code",
	"codex",
	"aider",
	"cursor-agent",
	"opencode",
	"crush",
	"gemini",
	"amp",
	"droid",
	"cline",
	"kilocode",
	"auggie",
	"octofriend",
	"qwen",
}

const (
	agentGroupWalkLimit = 24
	agentGroupWalkDepth = 4
)

type agentMatcher struct {
	names    map[string]struct{}
	registry *harness.Registry
}

func newAgentMatcher(extra []string) agentMatcher {
	names := make(map[string]struct{}, len(defaultAgentBinaries)+len(extra))
	for _, n := range defaultAgentBinaries {
		names[n] = struct{}{}
	}
	for _, n := range extra {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			names[n] = struct{}{}
		}
	}
	registry, errs := harness.Load(harness.UserDir())
	for _, e := range errs {
		log.Printf("harness manifest %s: %v", e.Source, e.Err)
	}
	return agentMatcher{names: names, registry: registry}
}

type identityTier string

const (
	identityReport   identityTier = "report"
	identityManifest identityTier = "manifest"
	identityList     identityTier = "list"
	identityHint     identityTier = "hint"
)

func (t identityTier) confidence() string {
	switch t {
	case identityReport:
		return "certain"
	case identityManifest, identityList, identityHint:
		return "strong"
	default:
		return "none"
	}
}

type detection struct {
	harness string
	rule    string
	tier    identityTier
	proc    foregroundInfo
	via     []string
	visited []foregroundInfo
	flags   []string
}

func (m agentMatcher) identifyDetail(info foregroundInfo) (detection, bool) {
	if d, ok := m.matchProc(info); ok {
		return d, true
	}
	d, ok := m.walkGroup(info)
	if ok {
		return d, true
	}
	if hinted, ok := m.matchHint(info); ok {
		hinted.visited = d.visited
		return hinted, true
	}
	return d, false
}

func (m agentMatcher) identifyKept(info foregroundInfo) (detection, bool) {
	d, ok := m.identifyDetail(info)
	if ok && d.tier == identityManifest && m.registry != nil {
		d.flags = m.registry.KeptFlags(d.harness, d.proc.argv)
	}
	return d, ok
}

func (m agentMatcher) matchHint(info foregroundInfo) (detection, bool) {
	if info.hint == nil || m.registry == nil {
		return detection{}, false
	}
	name := info.hint()
	if name == "" {
		return detection{}, false
	}
	man, _, ok := m.registry.Resolve(name)
	if !ok {
		return detection{}, false
	}
	return detection{harness: man.ID, rule: AgentHintEnv + "=" + name, tier: identityHint, proc: info}, true
}

func (m agentMatcher) walkGroup(info foregroundInfo) (detection, bool) {
	if info.group == nil || !info.proc().Wraps() {
		return detection{}, false
	}
	chain := []string{processLabel(info)}
	var visited []foregroundInfo
	for member := range info.group {
		visited = append(visited, member)
		depth := max(member.depth, 1)
		if len(chain) > depth {
			chain = chain[:depth]
		}
		if d, ok := m.matchProc(member); ok {
			d.via = append([]string(nil), chain...)
			d.visited = visited
			return d, true
		}
		chain = append(chain, processLabel(member))
	}
	return detection{visited: visited}, false
}

func (m agentMatcher) matchProc(info foregroundInfo) (detection, bool) {
	p := info.proc()
	if m.registry != nil {
		if id, rule, ok := m.registry.IdentifyDetail(p); ok {
			return detection{harness: id, rule: rule, tier: identityManifest, proc: info}, true
		}
	}
	if rule, ok := m.nameRule(p); ok {
		return detection{rule: rule, tier: identityList, proc: info}, true
	}
	return detection{}, false
}

func processLabel(info foregroundInfo) string {
	if base := agentBaseName(info.comm); base != "" {
		return base
	}
	if len(info.argv) > 0 {
		return agentBaseName(info.argv[0])
	}
	return ""
}

func (m agentMatcher) isAgent(info foregroundInfo) bool {
	_, ok := m.nameRule(info.proc())
	return ok
}

func (m agentMatcher) nameRule(p harness.ProcInfo) (string, bool) {
	if comm := p.CommBase(); m.named(comm) {
		return "name comm=" + comm, true
	}
	if exe := p.ExeBase(); m.named(exe) {
		return "name exe=" + exe, true
	}
	if argv0 := p.Argv0Base(); m.named(argv0) {
		return "name argv0=" + argv0, true
	}
	if run := p.RunToken(); run != "" {
		if base := agentBaseName(run); m.named(base) {
			return "name run token=" + base, true
		}
		if strings.Contains(run, "/") {
			for name := range m.names {
				if harness.PackageNamed(run, name) {
					return "name package=" + name, true
				}
			}
		}
	}
	return "", false
}

func (m agentMatcher) named(base string) bool {
	if base == "" {
		return false
	}
	_, ok := m.names[base]
	return ok
}

func (m agentMatcher) knownNames() map[string]struct{} {
	known := make(map[string]struct{}, len(m.names))
	for n := range m.names {
		known[n] = struct{}{}
	}
	if m.registry == nil {
		return known
	}
	for _, id := range m.registry.IDs() {
		known[id] = struct{}{}
		man := m.registry.Lookup(id)
		for _, n := range man.Detect.Comm {
			known[n] = struct{}{}
		}
		for _, n := range man.Detect.Argv0 {
			known[n] = struct{}{}
		}
	}
	return known
}

func (m agentMatcher) mentions(info foregroundInfo) []string {
	known := m.knownNames()
	var out []string
	seen := map[string]struct{}{}
	note := func(where, token, word, why string) {
		key := where + token + word
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, "The "+where+" "+strconv.Quote(token)+" contains the word "+
			strconv.Quote(word)+". "+why)
	}
	for i, arg := range info.argv {
		if i == 0 {
			continue
		}
		for _, word := range namedComponents(arg, known) {
			note("argument", arg, word, "A word inside an argument is not evidence.")
		}
	}
	if info.exe != "" {
		for _, word := range namedComponents(info.exe, known) {
			if word == agentBaseName(info.exe) {
				continue
			}
			note("executable path", info.exe, word, "A directory name is not evidence.")
		}
	}
	return out
}

func namedComponents(token string, known map[string]struct{}) []string {
	var out []string
	for comp := range strings.SplitSeq(strings.TrimRight(token, "\x00"), "/") {
		base := agentBaseName(comp)
		if base == "" {
			continue
		}
		if _, ok := known[base]; ok && !slices.Contains(out, base) {
			out = append(out, base)
		}
	}
	return out
}

func agentBaseName(s string) string { return harness.BaseName(s) }

var loginShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "csh": true, "tcsh": true, "nu": true, "xonsh": true,
	"elvish": true, "pwsh": true, "powershell": true, "cmd": true,
}

type foregroundInfo struct {
	comm     string
	argv     []string
	exe      string
	pid      int
	depth    int
	group    func(yield func(foregroundInfo) bool)
	hint     func() string
	shellPID int
}

func (i foregroundInfo) proc() harness.ProcInfo {
	return harness.ProcInfo{Comm: i.comm, Argv: i.argv, Exe: i.exe}
}

func (i foregroundInfo) atShell() bool { return i.pid == i.shellPID }

func foregroundCommand(info foregroundInfo, running bool, shell string) string {
	if !running {
		return ""
	}
	name := ""
	if len(info.argv) > 0 {
		name = agentBaseName(info.argv[0])
	}
	if name == "" {
		name = agentBaseName(info.comm)
	}
	if name == "" || name == shell || loginShells[name] {
		return ""
	}
	return name
}

func foregroundProcess(shellPid int) (foregroundInfo, bool) {
	if shellPid <= 0 {
		return foregroundInfo{}, false
	}
	tpgid, ok := readForegroundPGID(shellPid)
	if !ok || tpgid <= 0 {
		return foregroundInfo{}, false
	}
	info := readProcessInfo(tpgid)
	info.pid = tpgid
	if info.comm == "" && len(info.argv) == 0 {
		return foregroundInfo{}, false
	}
	if tpgid != shellPid {
		info.group = foregroundGroup(tpgid, agentGroupWalkLimit, agentGroupWalkDepth)
		info.hint = func() string { return agentHint(readAgentHintEnv, tpgid) }
	}
	return info, true
}

type harnessProc struct {
	pid   int
	start uint64
	timed bool
	gone  bool
}

func readHarnessProc(pid int) harnessProc {
	h := harnessProc{pid: pid}
	h.start, h.timed = procinfo.StartTime(pid)
	return h
}

func (h harnessProc) exited() bool {
	if h.gone {
		return true
	}
	if !h.timed {
		return false
	}
	start, ok := procinfo.StartTime(h.pid)
	return !ok || start != h.start
}

const agentDetectMissLimit = 6

type detectReading struct {
	at        int64
	ptyID     string
	info      foregroundInfo
	running   bool
	det       detection
	isAgent   bool
	offer     *SSHOffer
	offerRead bool
}

type detectOutcome struct {
	changed     int
	labels      int
	bookkeeping bool
	named       []string
}

func (s *Session) scanAgentDetection(
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (detection, bool),
	due func(ptyID string) bool,
) int {
	type target struct {
		windowID, ptyID string
		holdsAgent      bool
	}
	s.stateMu.RLock()
	targets := make([]target, 0, len(s.state.Windows))
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.PTYID == "" {
			continue
		}
		_, claimed := s.agentClaims[w.ID]
		holdsAgent := w.AgentState != AgentStateNone || claimed || s.agentHarnessPIDs[w.ID].pid != 0
		targets = append(targets, target{w.ID, w.PTYID, holdsAgent})
	}
	s.stateMu.RUnlock()

	readings := make(map[string]detectReading, len(targets))
	for _, t := range targets {
		if due != nil && !t.holdsAgent && !due(t.ptyID) {
			continue
		}
		at := time.Now().UnixNano()
		info, running := resolve(t.ptyID)
		det, isAgent := identify(info)
		offer, offerRead := s.probeSSHOffer(t.ptyID, info, running)
		readings[t.windowID] = detectReading{at: at, ptyID: t.ptyID, info: info, running: running, det: det, isAgent: isAgent, offer: offer, offerRead: offerRead}
	}

	shell := agentBaseName(s.getShell())
	now := time.Now().UnixNano()

	for _, r := range readings {
		if pty := s.GetPTY(r.ptyID); pty != nil {
			pty.endProgramStatusOfEndedGroups(r.info, r.running)
		}
	}

	s.stateMu.RLock()
	dry := s.detectionPass(s.state, readings, shell, now, false)
	s.stateMu.RUnlock()
	if dry.changed == 0 && dry.labels == 0 {
		if !dry.bookkeeping {
			return 0
		}
		s.stateMu.Lock()
		if again := s.detectionPass(s.state, readings, shell, now, false); again.changed == 0 && again.labels == 0 {
			s.detectionPass(s.state, readings, shell, now, true)
			s.stateMu.Unlock()
			return 0
		}
		s.stateMu.Unlock()
	}

	changed := 0
	var named []string
	_ = s.mutateState(func(st *SessionState) error {
		out := s.detectionPass(st, readings, shell, now, true)
		changed, named = out.changed, out.named
		if out.changed == 0 && out.labels == 0 {
			return errNoAgentDetectChange
		}
		return nil
	})
	if len(named) > 0 {
		s.harnessNamedMu.Lock()
		s.harnessNamed = append(s.harnessNamed, named...)
		s.harnessNamedMu.Unlock()
	}
	return changed
}

func (s *Session) takeHarnessNamed() []string {
	s.harnessNamedMu.Lock()
	defer s.harnessNamedMu.Unlock()
	named := s.harnessNamed
	s.harnessNamed = nil
	return named
}

func (s *Session) detectionPass(st *SessionState, readings map[string]detectReading, shell string, now int64, write bool) detectOutcome {
	var out detectOutcome
	live := make(map[string]struct{}, len(st.Windows))
	for i := range st.Windows {
		w := &st.Windows[i]
		live[w.ID] = struct{}{}
		if w.PTYID == "" {
			continue
		}
		r, ok := readings[w.ID]
		if !ok || r.ptyID != w.PTYID {
			continue
		}
		info, running := r.info, r.running
		fg := foregroundCommand(info, running, shell)
		if cmd := ClampDisplayText(fg); cmd != w.ForegroundCmd {
			if write {
				w.ForegroundCmd = cmd
			}
			out.labels++
		}
		if syncSSHOffer(w, r, write) {
			out.labels++
		}
		if info.shellPID != w.ShellPID {
			if write {
				w.ShellPID = info.shellPID
			}
			out.labels++
		}
		det := r.det
		detected := running && r.isAgent
		if detected && det.tier == identityManifest && det.harness != "" &&
			(w.AgentFlags == nil || w.AgentFlags.Harness != det.harness || !slices.Equal(w.AgentFlags.Flags, det.flags)) {
			if write {
				w.AgentFlags = &KeptFlags{Harness: det.harness, Flags: det.flags}
			}
			out.labels++
		}
		if g, ok := s.agentSharedGuards[w.ID]; ok && g.armedAt < r.at && !detected && running && info.atShell() && fg == "" {
			out.bookkeeping = true
			if write {
				delete(s.agentSharedGuards, w.ID)
			}
		}
		claim, held := s.agentClaims[w.ID]
		owned := claim.auto
		if detected && !claim.sawProcess {
			if !held {
				claim.source = AgentSourceDetect
			}
			claim.sawProcess = true
			out.bookkeeping = true
			if write {
				s.setAgentClaim(w.ID, claim)
			}
		}
		switch {
		case detected && !owned:
			if w.AgentState == AgentStateNone {
				out.changed++
				if !write {
					continue
				}
				w.AgentState = AgentStateWorking
				clearAgentNote(w)
				w.AgentHarness = det.harness
				w.AgentStateAt = now
				if det.harness != "" {
					out.named = append(out.named, w.PTYID)
				}
				s.setAgentClaim(w.ID, agentClaim{
					source: AgentSourceDetect, harness: det.harness, identity: det.tier, auto: true,
				})
			} else if w.AgentHarness == "" && det.harness != "" && claim.source != AgentSourceReport {
				out.changed++
				if !write {
					continue
				}
				w.AgentHarness = det.harness
				out.named = append(out.named, w.PTYID)
			}
		case detected && owned:
			if w.AgentHarness == "" && det.harness != "" && claim.source != AgentSourceReport {
				out.changed++
				if write {
					w.AgentHarness = det.harness
					claim.harness, claim.identity = det.harness, det.tier
					s.setAgentClaim(w.ID, claim)
					out.named = append(out.named, w.PTYID)
				}
			}
			if claim.misses != 0 {
				out.bookkeeping = true
				if write {
					claim.misses = 0
					s.setAgentClaim(w.ID, claim)
				}
			}
		case !detected && owned:
			if running && !info.atShell() && claim.misses+1 < agentDetectMissLimit {
				out.bookkeeping = true
				if write {
					claim.misses++
					s.setAgentClaim(w.ID, claim)
				}
				continue
			}
			out.changed++
			if !write {
				continue
			}
			delete(s.agentClaims, w.ID)
			delete(s.agentHarnessPIDs, w.ID)
			delete(s.agentSharedGuards, w.ID)
			w.AgentState = AgentStateNone
			clearAgentNote(w)
			w.AgentHarness = ""
			w.AgentMeta = nil
			w.AgentStateAt = now

		case !detected && !owned && w.AgentState != AgentStateNone:
			lapsed := claim.sawProcess && claim.source != AgentSourceReport
			reportHeld := claim.source == AgentSourceReport || (claim.blocker && claim.prior.source == AgentSourceReport)
			if reportHeld && claim.herdrClaimLapsed(now) && fg == "" {
				lapsed = true
			}
			if !lapsed && reportHeld && running && info.atShell() && fg == "" && s.agentHarnessPIDs[w.ID].exited() {
				lapsed = true
			}
			if running && info.atShell() && lapsed {
				out.changed++
				if !write {
					continue
				}
				delete(s.agentClaims, w.ID)
				delete(s.agentHarnessPIDs, w.ID)
				delete(s.agentSharedGuards, w.ID)
				w.AgentState = AgentStateNone
				clearAgentNote(w)
				w.AgentHarness = ""
				w.AgentMeta = nil
				w.AgentStateAt = now
			}
		}
	}
	for id := range s.agentClaims {
		if _, ok := live[id]; !ok {
			out.bookkeeping = true
			if !write {
				break
			}
			delete(s.agentClaims, id)
			s.dropAgentHold(id)
			s.idle.forget(id)
		}
	}
	for id := range s.agentHarnessPIDs {
		if _, ok := live[id]; !ok {
			out.bookkeeping = true
			if !write {
				break
			}
			delete(s.agentHarnessPIDs, id)
		}
	}
	for id := range s.agentSharedGuards {
		if _, ok := live[id]; !ok {
			out.bookkeeping = true
			if !write {
				break
			}
			delete(s.agentSharedGuards, id)
		}
	}
	return out
}

func (s *Session) reconcileAgentOnOutput(
	ptyID string,
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (detection, bool),
) bool {
	if !s.ownsAutoAgent(ptyID) {
		return false
	}

	changed := false
	_ = s.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			w := &st.Windows[i]
			if w.PTYID != ptyID {
				continue
			}
			claim := s.agentClaims[w.ID]
			if !claim.auto {
				return errNoAgentDetectChange
			}
			info, running := resolve(ptyID)
			det, stillAgent := identify(info)
			if running && stillAgent {
				quiet := w.AgentState == AgentStateIdle || w.AgentState == AgentStateUnknown
				if !quiet || claim.source.Rank() > AgentSourceDetect.Rank() {
					return errNoAgentDetectChange
				}
				w.AgentState = AgentStateWorking
				clearAgentNote(w)
				w.AgentHarness = det.harness
				w.AgentStateAt = time.Now().UnixNano()
				claim.harness = det.harness
				claim.identity = det.tier
				claim.source = AgentSourceDetect
				claim.misses = 0
				s.setAgentClaim(w.ID, claim)
				changed = true
				return nil
			}
			if running && !info.atShell() {
				return errNoAgentDetectChange
			}
			delete(s.agentClaims, w.ID)
			delete(s.agentHarnessPIDs, w.ID)
			delete(s.agentSharedGuards, w.ID)
			w.AgentState = AgentStateNone
			clearAgentNote(w)
			w.AgentHarness = ""
			w.AgentMeta = nil
			w.AgentStateAt = time.Now().UnixNano()
			changed = true
			return nil
		}
		return errNoAgentDetectChange
	})
	return changed
}

func (s *Session) ownsAutoAgent(ptyID string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if s.state.Windows[i].PTYID == ptyID {
			return s.agentClaims[s.state.Windows[i].ID].auto
		}
	}
	return false
}

var errNoAgentDetectChange = agentDetectNoChange{}

type agentDetectNoChange struct{}

func (agentDetectNoChange) Error() string { return "no agent-detection change" }

// ForegroundProgram is the base name of the program that holds the terminal
// the shell with pid shellPid runs on: the shell itself while it sits at its
// prompt, unlike WindowState.ForegroundCmd, which leaves every shell out.
// "" when it cannot be read, which a caller must treat as not known.
//
// The client asks this of a pane on its own machine when it has to know which
// shell will read what it types, such as the quoting of a dropped path. The
// process name comes first: a script's argv[0] is its interpreter, and the
// name is what the person ran.
func ForegroundProgram(shellPid int) string {
	info, ok := foregroundProcess(shellPid)
	if !ok {
		return ""
	}
	if name := agentBaseName(info.comm); name != "" {
		return name
	}
	if len(info.argv) > 0 {
		return agentBaseName(info.argv[0])
	}
	return ""
}
