package session

import (
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// defaultAgentBinaries is the built-in set of AI-agent CLI binary names the
// foreground-process auto-detector recognises. A pane whose foreground process is
// one of these is marked as running an agent (AgentStateWorking) without the user
// running set-agent-state.
//
// The list is intentionally the well-known coding-agent CLIs. Users extend it,
// they do not have to replace it: the daemon merges these with any names from the
// TUIOS_AGENT_BINARIES environment override and the daemon.agent_binaries config
// list. Matching is on the binary's base name, so a full path resolves the same.
var defaultAgentBinaries = []string{
	"claude",
	"claude-code",
	"codex",
	"aider",
	"cursor-agent",
	"opencode",
	"goose",
	"crush",
	"gemini",
	"amp",
	// Names distinctive enough to match on their own. Harnesses whose command is
	// a common English word (agent, pi, cn, forge) are deliberately absent: a
	// false positive labels an unrelated pane as an agent, which is worse than
	// missing one, and a user who wants them can add them by name.
	"droid",
	"cline",
	"kilocode",
	"auggie",
	"octofriend",
	"qwen",
}

// agentGroupWalkLimit and agentGroupWalkDepth bound the walk behind a wrapper.
// A wrapper runs one program, so a match is found within a handful of reads; the
// bounds are what keep a launcher that happens to run a build from being walked
// to the end of its tree on every tick.
const (
	agentGroupWalkLimit = 24
	agentGroupWalkDepth = 4
)

// agentMatcher decides whether a foreground process is a known AI-agent CLI. It
// holds the resolved set of agent names (defaults merged with user additions),
// lowercased for case-insensitive matching.
type agentMatcher struct {
	names    map[string]struct{}
	registry *harness.Registry
}

// newAgentMatcher builds a matcher from the manifest registry plus the built-in
// defaults and any extra names. Extra names are trimmed and lowercased; blanks
// are ignored.
//
// The registry and the name list are both consulted, and neither replaces the
// other. The registry is what a user extends without a rebuild and is what can
// name the harness it matched; the flat name list is what TUIOS_AGENT_BINARIES
// and daemon.agent_binaries have always fed, and those configs have to keep
// working exactly as they did.
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
		// Named and logged rather than dropped: a manifest a user wrote and that
		// silently does nothing is the failure mode this registry exists to avoid.
		log.Printf("harness manifest %s: %v", e.Source, e.Err)
	}
	return agentMatcher{names: names, registry: registry}
}

// identityTier says what kind of evidence named a pane's agent. It is the
// identity half of the confidence model: the state half is AgentSource. A tier
// is a rank rather than a score on purpose. A score invites adding weak signals
// together until they clear a threshold, which is exactly how a directory name
// in a script path came to count as an agent; a rank means a weak signal can
// never add up to a strong verdict, because nothing is added.
type identityTier string

const (
	// identityReport is the harness naming itself through a report, which is
	// the one source that cannot be mistaken about what it is.
	identityReport identityTier = "report"
	// identityManifest is a manifest rule matching the process's own identity:
	// its name, its argv[0], the token an interpreter runs, or its executable.
	identityManifest identityTier = "manifest"
	// identityList is the built-in or user name list matching the process's own
	// name. It names no harness, so no screen rules run for it.
	identityList identityTier = "list"
)

// confidence is the plain word a tier is reported as.
func (t identityTier) confidence() string {
	switch t {
	case identityReport:
		return "certain"
	case identityManifest, identityList:
		return "strong"
	default:
		return "none"
	}
}

// detection is what the matcher decided about a pane and the evidence it rests
// on, so the decision can be explained rather than only announced.
type detection struct {
	// harness is the manifest id, empty for a name-list match.
	harness string
	// rule is the predicate that decided it, as the manifest or the name list
	// spells it.
	rule string
	// tier says which kind of evidence decided it.
	tier identityTier
	// proc is the process that matched. It is the foreground process group
	// leader, or a member of the group behind a wrapper.
	proc foregroundInfo
	// via lists the wrappers between the leader and proc, leader first, empty
	// when the leader itself matched.
	via []string
	// visited lists the group members that were read, in the order they were
	// read, whether or not one of them matched.
	visited []foregroundInfo
}

// identify names the harness a pane's foreground process is running, reporting
// whether it is an agent at all. The manifest registry answers first because it
// can name what it matched; the built-in and user-configured name list is the
// fallback and yields an unnamed match.
func (m agentMatcher) identify(info foregroundInfo) (string, bool) {
	d, ok := m.identifyDetail(info)
	return d.harness, ok
}

// identifyDetail is identify with the evidence that decided it.
//
// The leader is read first. When it is not an agent but stands in for another
// program (a shell, an interpreter, a launcher such as timeout or npx), the
// other members of its foreground process group are read too, depth first, so
// an agent started through a wrapper script, a shell, a version manager or a
// package runner is still found. A process that is neither an agent nor a
// wrapper ends the search: an editor's children are not its identity.
func (m agentMatcher) identifyDetail(info foregroundInfo) (detection, bool) {
	if d, ok := m.matchProc(info); ok {
		return d, true
	}
	if info.group == nil || !info.proc().Wraps() {
		return detection{}, false
	}
	// chain holds the base names of the leader and the wrappers above the
	// member being read, indexed by depth. Members arrive depth first, so a
	// parent is always in place before its children are read.
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

// matchProc decides one process on its own identity.
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

// processLabel is the short name a process is reported by: its comm, which for
// a script is the script's own name rather than the shell running it, else the
// base name of its argv[0].
func processLabel(info foregroundInfo) string {
	if base := agentBaseName(info.comm); base != "" {
		return base
	}
	if len(info.argv) > 0 {
		return agentBaseName(info.argv[0])
	}
	return ""
}

// isAgent reports whether a pane's foreground process is a known agent by the
// name list alone. It reads what the process calls itself and nothing else:
//
//   - comm, which the kernel truncates at 15 characters and which a program can
//     rename to anything (Claude Code renames itself to "claude");
//   - exe, the real binary behind it, which survives that renaming;
//   - argv[0], the name it was invoked by;
//   - the one token an interpreter was asked to run, reduced to its base name,
//     because an interpreter's identity is the script it runs, or the package
//     that token sits under when a package manager installed it.
//
// It does not read any other directory in any of those paths. A script that lives
// in a checkout named after an agent is not that agent, and a binary built in
// one is not either. The shipped matcher scanned every path component of the
// executable and of the run token, which is how a deploy script under
// ~/claude/, a tool under ~/dev/codex/ and a build under ~/dev/claude-code/
// all became agents.
func (m agentMatcher) isAgent(info foregroundInfo) bool {
	_, ok := m.nameRule(info.proc())
	return ok
}

// nameRule is isAgent with the signal that decided it.
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
		// A generic entry point under a package named for the agent, the way
		// npm and pip lay a program out. The name has to sit under a package
		// directory: see harness.PackageNamed for why a directory elsewhere in
		// the path does not count. A token with no directory in it was already
		// judged by its base name above.
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

// named reports whether a base name is one of the known agent names.
func (m agentMatcher) named(base string) bool {
	if base == "" {
		return false
	}
	_, ok := m.names[base]
	return ok
}

// knownNames is every name the matcher would recognise as a program: the name
// list, and each manifest's id, comm and argv0 names. It is what mentions scans
// for, so the explanation can say which word in an argument a person may have
// taken for evidence.
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

// mentions lists, in plain words, every place an agent's name appears in a
// process that the matcher did not count: a directory in an argument, or a
// directory in the executable's path. It exists for the person who reads an
// explanation of a pane that was, or was not, called an agent and wants to know
// what the detector made of the word they can see on the command line.
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

// namedComponents returns the path components of a token whose base name is a
// known agent name, in order, without duplicates.
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

// agentBaseName reduces a comm value or an argv token to the base name used for
// matching. It is harness.BaseName, shared so the name list and the manifest
// registry cannot drift apart on what a process is called.
func agentBaseName(s string) string { return harness.BaseName(s) }

// loginShells are the shells a pane sits at when it is running nothing. Their
// names are noise in a row label: every idle pane in a session reports the same
// one. The session's own configured shell is checked too, but only that one
// name is known, and a user whose login shell differs from the daemon's $SHELL
// would otherwise get every row labelled with it.
var loginShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "csh": true, "tcsh": true, "nu": true, "xonsh": true,
	"elvish": true, "pwsh": true, "powershell": true, "cmd": true,
}

// foregroundInfo describes a pane's foreground process. The three fields are
// three different answers to "what is this", and the detector needs all of them;
// see agentMatcher.isAgent for why none of them is enough alone.
type foregroundInfo struct {
	// comm is /proc/<pid>/comm: the process name, truncated at 15 characters and
	// rewritable by the process itself.
	comm string
	// argv is the full command line.
	argv []string
	// exe is the resolved /proc/<pid>/exe, empty when it cannot be read. A
	// process with no permission to read its own target, or a deleted binary,
	// both yield empty rather than an error.
	exe string
	// pid is the foreground process itself, kept so the transcript source can
	// read its working directory. Zero when the process could not be resolved.
	pid int
	// depth is how many processes sit between this one and the foreground
	// process group leader: 0 for the leader, 1 for its child. It is set by the
	// group walk and read by the matcher to name the wrappers above a match.
	depth int
	// group yields the other members of the foreground process group behind
	// the leader, depth first and bounded, each with its depth set. It is nil
	// when there is nothing to walk: the leader is the pane's own shell at its
	// prompt, or the platform cannot list a process's children. It is read
	// lazily, so a pane whose leader is itself the agent never pays for it.
	group func(yield func(foregroundInfo) bool)
	// shellPID is the pane's shell, not its foreground process. It rides here
	// because the resolver is handed the shell pid to begin with, so nothing has
	// to be read to know it, and because this is the one value the detector's
	// poll already has for every pane. Zero when the pane has no live PTY.
	//
	// It is set whether or not the foreground process resolved: a pane whose
	// foreground group cannot be read still has a shell, and clearing the pid
	// there would drop the client's only way to check the pane's reported
	// directory. See WindowState.ShellPID.
	shellPID int
}

// proc is the process in the shape both matchers read it in.
func (i foregroundInfo) proc() harness.ProcInfo {
	return harness.ProcInfo{Comm: i.comm, Argv: i.argv, Exe: i.exe}
}

// atShell reports whether the foreground process is the pane's own shell, which
// is what a pane looks like at its prompt. A resolver that reports neither pid
// (a test double) is taken at its word.
func (i foregroundInfo) atShell() bool { return i.pid == i.shellPID }

// foregroundCommand is the label a pane earns from what it is running: the base
// name of the foreground process, or empty when that is just a shell. argv[0]
// is preferred over comm because the kernel truncates comm at 15 characters.
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

// foregroundProcess resolves the foreground process group leader of the
// controlling terminal of the shell with the given pid. It is the honest signal
// for "what is this pane actually running": the tty carries a foreground process
// group id, and the process whose pid equals that id is the program in the
// foreground, or the shell itself when nothing else is running.
//
// Both readings are per-platform. Linux takes them from procfs, darwin from
// kern.proc.pid and kern.procargs2; a platform with neither reports nothing and
// the caller treats the pane as running no agent. When the process is gone the
// answer is the same. The detector re-resolves every tick and only acts on a
// change, so a pid reused between two reads costs at worst one stale tick.
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
		// The foreground group leader vanished between reads, or this platform
		// cannot see it: report not-running rather than guess.
		return foregroundInfo{}, false
	}
	// The pane's own shell at its prompt runs nothing in the foreground, and an
	// interactive shell gives every job its own process group, so there is
	// nothing behind it to read. Every other leader may be a wrapper, and the
	// walk is handed over unread: the matcher only runs it for one that is.
	if tpgid != shellPid {
		info.group = foregroundGroup(tpgid, agentGroupWalkLimit, agentGroupWalkDepth)
	}
	return info, true
}

// agentDetectMissLimit is how many consecutive detection ticks may find no agent
// in a pane the detector holds before the claim clears, while something other
// than the pane's shell is in the foreground. An agent that opens an editor or a
// pager hands the terminal to it for a while and takes it back; one missed read
// is that, not an exit. The pane's own shell returning is an exit, and clears at
// once. At the default two-second tick this is twelve seconds.
const agentDetectMissLimit = 6

// applyAgentDetection reconciles each window's agent state with the foreground
// process of its pane, using the injected resolve and identify so it is testable
// without a real /proc or a real agent. It returns how many windows it changed.
//
// resolve reports the foreground process for a PTY and whether it is running;
// identify decides whether that process is an agent and says which harness it
// is and on what evidence, with an empty harness when it matched a bare name
// rather than a manifest.
//
// Precedence (auto-detection is deliberately subordinate to explicit reports):
//
//   - It promotes a window to AgentStateWorking only when an agent appears in the
//     foreground AND the window currently has no agent state (AgentStateNone).
//     A window a user already set through set-agent-state is never overwritten.
//   - It records the windows it promoted (the auto bit of agentClaims) and only
//     ever manages those. While it owns a window and the agent is still in the
//     foreground it leaves the state alone, so the output-stall heuristic may
//     demote it to idle and an explicit set-agent-state may move it anywhere;
//     either wins until the agent exits.
//   - When the agent leaves the foreground it clears an owned window back to
//     AgentStateNone and relinquishes ownership: at once when the pane is back
//     at its shell, and after agentDetectMissLimit consecutive misses when
//     another program holds the foreground, so an editor the agent opened does
//     not read as the agent exiting.
//
// It never sets any state other than working (on appearance) or none (on
// disappearance): a process name cannot honestly distinguish working from waiting
// or idle, so it does not pretend to.
func (s *Session) applyAgentDetection(
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (detection, bool),
) int {
	changed := 0
	shell := agentBaseName(s.getShell())
	_ = s.mutateState(func(st *SessionState) error {
		// Counted apart from the agent states this returns: a pane starting or
		// leaving a command has to reach the clients, but it is not a state change.
		labels := 0
		live := make(map[string]struct{}, len(st.Windows))
		now := time.Now().UnixNano()
		for i := range st.Windows {
			w := &st.Windows[i]
			// Recorded before the PTY check: live is what the claim sweep below
			// keeps, and a window with no PTY still exists and may hold a claim from
			// a source other than the detector.
			live[w.ID] = struct{}{}
			if w.PTYID == "" {
				continue
			}
			info, running := resolve(w.PTYID)
			// The row label rides this poll rather than one of its own: the
			// process was read for the agent check either way.
			if cmd := foregroundCommand(info, running, shell); cmd != w.ForegroundCmd {
				w.ForegroundCmd = cmd
				labels++
			}
			// The shell's pid rides the same poll, for the same reason and at the
			// same price: the resolver already held it. It is counted with the
			// labels rather than the agent states because it is not one, but it
			// still has to reach the clients, which is what a pane needs before
			// its reported directory can be checked. See WindowState.ShellPID.
			if info.shellPID != w.ShellPID {
				w.ShellPID = info.shellPID
				labels++
			}
			det, isAgent := identify(info)
			detected := running && isAgent
			claim := s.agentClaims[w.ID]
			owned := claim.auto
			switch {
			case detected && !owned:
				// Take ownership only if no state is set, so a manual report wins.
				if w.AgentState == AgentStateNone {
					w.AgentState = AgentStateWorking
					w.AgentMessage = ""
					w.AgentHarness = det.harness
					w.AgentStateAt = now
					s.setAgentClaim(w.ID, agentClaim{
						source: AgentSourceDetect, harness: det.harness, identity: det.tier, auto: true,
					})
					changed++
				}
			case detected && owned:
				// Still here. A miss count from an editor it opened is forgotten.
				if claim.misses != 0 {
					claim.misses = 0
					s.setAgentClaim(w.ID, claim)
				}
			case !detected && owned:
				if running && !info.atShell() && claim.misses+1 < agentDetectMissLimit {
					// Another program holds the foreground. Count it and wait: an
					// agent that opened an editor is still an agent.
					claim.misses++
					s.setAgentClaim(w.ID, claim)
					continue
				}
				// Agent gone from the foreground: relinquish and clear.
				delete(s.agentClaims, w.ID)
				w.AgentState = AgentStateNone
				w.AgentMessage = ""
				w.AgentHarness = ""
				w.AgentStateAt = now
				changed++
			}
			// !detected && !owned: not ours, do not touch.
		}
		// Drop claims on windows that no longer exist so the map cannot grow
		// without bound. This touches only in-memory bookkeeping, never state, so
		// it does not count as a change.
		for id := range s.agentClaims {
			if _, ok := live[id]; !ok {
				delete(s.agentClaims, id)
				// A window that went away must not leave a held state behind for
				// the settle sweep to publish against nothing.
				s.dropAgentHold(id)
			}
		}
		if changed == 0 && labels == 0 {
			// Nothing moved: skip the version bump and client push.
			return errNoAgentDetectChange
		}
		return nil
	})
	return changed
}

// reconcileAgentOnOutput settles the agent state of the window backed by ptyID
// against what the pane is actually running, driven by the pane's own output
// rather than a timer, so it adds no idle cost. Output is the one moment both
// answers it gives are known to be fresh.
//
// It resolves two cases:
//
//   - The foreground is the pane's own shell: the agent quit and the shell
//     prompt is what produced this output, so the state clears at once instead
//     of lingering until the next detection poll. Any other program in the
//     foreground is left to the detection tick, which counts misses before it
//     clears, since an agent that opened an editor has not quit.
//   - The foreground is still the agent: the agent is producing output, so it is
//     working. This is the only path back out of the idle or unknown the silence
//     timer assigns, and without it a pane latched there for the rest of its
//     life however hard the agent then worked.
//
// It obeys the same precedence as applyAgentDetection: it only ever touches a
// window the auto-detector owns, and it only resumes one whose state is the idle
// a source no stronger than the detector left behind, so a harness reporting for
// itself is never overwritten. It reports whether it changed state.
func (s *Session) reconcileAgentOnOutput(
	ptyID string,
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (detection, bool),
) bool {
	// Almost every output event is from a pane the auto-detector never promoted.
	// Rule those out under the read lock so a busy non-agent pane does not take the
	// state write lock (and push to clients) on every throttled event. mutateState
	// re-checks ownership under the write lock, so a race that clears ownership
	// between here and there only makes the mutation a no-op.
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
				// Still the agent, and it just spoke. Only the idle or unknown
				// left by the silence timer (or by the detector itself) may be
				// taken back: anything a stronger source said outranks output
				// activity, which cannot tell working from a redraw while
				// waiting for the user.
				quiet := w.AgentState == AgentStateIdle || w.AgentState == AgentStateUnknown
				if !quiet || claim.source.rank() > AgentSourceDetect.rank() {
					return errNoAgentDetectChange
				}
				w.AgentState = AgentStateWorking
				w.AgentMessage = ""
				// Re-stated rather than cleared: the process just identified is the
				// same one the detector attributed, and a pane with no harness on it
				// has no screen rules to run, so clearing here blinded the very pane
				// that had just proved an agent is alive in it.
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
				// Another program has the terminal. That is the tick's call to
				// make, over several misses, not this probe's to make on one.
				return errNoAgentDetectChange
			}
			delete(s.agentClaims, w.ID)
			w.AgentState = AgentStateNone
			w.AgentMessage = ""
			w.AgentHarness = ""
			w.AgentStateAt = time.Now().UnixNano()
			changed = true
			return nil
		}
		return errNoAgentDetectChange
	})
	return changed
}

// ownsAutoAgent reports whether the auto-detector currently owns the window
// backed by ptyID. It reads under the state read lock, the fast-path gate that
// keeps reconcileAgentOnOutput off the write lock for panes it would never touch.
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

// errNoAgentDetectChange tells mutateState an agent-detection tick changed no
// state, so it neither bumps the version nor pushes to clients. It never leaves
// the package.
var errNoAgentDetectChange = agentDetectNoChange{}

type agentDetectNoChange struct{}

func (agentDetectNoChange) Error() string { return "no agent-detection change" }
