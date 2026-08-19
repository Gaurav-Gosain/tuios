package session

import (
	"log"
	"os"
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

// identify names the harness a pane's foreground process is running, reporting
// whether it is an agent at all. The manifest registry answers first because it
// can name what it matched; the built-in and user-configured name list is the
// fallback and yields an unnamed match.
func (m agentMatcher) identify(info foregroundInfo) (string, bool) {
	id, _, ok := m.identifyDetail(info)
	return id, ok
}

// identifyDetail is identify with the predicate that decided it, so the
// explain-agent-detect diagnostic can quote a manifest rule or say which of the
// name-list signals fired. The empty rule with ok false is "nothing matched".
func (m agentMatcher) identifyDetail(info foregroundInfo) (id, rule string, ok bool) {
	p := info.proc()
	if m.registry != nil {
		if id, rule, ok := m.registry.IdentifyDetail(p); ok {
			return id, rule, true
		}
	}
	if rule, ok := m.nameRule(p); ok {
		return "", rule, true
	}
	return "", "", false
}

// isAgent reports whether a pane's foreground process is a known agent. It reads
// three descriptions of the same process, because no one of them is reliable on
// its own:
//
//   - comm, which the kernel truncates at 15 characters and which a program can
//     rename to anything (Claude Code renames itself to "claude");
//   - exe, the real binary behind it, which survives that renaming but is a
//     version number rather than a name for installers that keep one binary per
//     release;
//   - argv, which names the script an interpreter is running.
//
// Any of them naming a known agent is enough. When none does and comm is not an
// interpreter, it stops rather than risk a false positive from an incidental
// argument, since mislabelling an unrelated pane is worse than missing an agent.
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
	// The install path names the harness even when the binary does not: Claude
	// Code's real executable is ".../share/claude/versions/2.1.222", whose own
	// name is a version and whose parent is the agent.
	if p.Exe != "" && m.argNamesAgent(p.Exe) {
		return "name exe path=" + p.Exe, true
	}
	// The one token an interpreter was asked to run, and nothing else on the
	// command line. Scan its path components rather than only its base name, so
	// the script file being cli.js does not hide the agent named by its directory.
	// RunToken is empty for a process that is not an interpreter, which is what
	// keeps an incidental argument from naming an agent.
	if run := p.RunToken(); run != "" && m.argNamesAgent(run) {
		return "name run token=" + run, true
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

// argNamesAgent reports whether any path component of a single argv token, once
// reduced to a base name, is a known agent. It is the wrapper-detection scan: an
// interpreter's script path carries the agent's name even when the file itself is
// a generic entry point.
func (m agentMatcher) argNamesAgent(arg string) bool {
	arg = strings.TrimRight(arg, "\x00")
	if arg == "" {
		return false
	}
	for comp := range strings.SplitSeq(arg, "/") {
		if comp == "" {
			continue
		}
		if _, ok := m.names[agentBaseName(comp)]; ok {
			return true
		}
	}
	return false
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
}

// proc is the process in the shape both matchers read it in.
func (i foregroundInfo) proc() harness.ProcInfo {
	return harness.ProcInfo{Comm: i.comm, Argv: i.argv, Exe: i.exe}
}

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
// controlling terminal of the shell with the given pid, returning its comm and
// full argv. It is the honest signal for "what is this pane actually running":
// the shell's /proc/<pid>/stat carries the tty's foreground process group id
// (tpgid), and the process whose pid equals that id is the program in the
// foreground, or the shell itself when nothing else is running.
//
// It is Linux-only (procfs). On any other platform, or when the process is gone,
// running is false and the caller treats the pane as running no agent. The comm
// and argv are read from the same /proc entry so a pid reused between the two
// reads yields at worst a stale-but-consistent name for one tick; the detector
// re-resolves every tick and only acts on a change.
func foregroundProcess(shellPid int) (foregroundInfo, bool) {
	if shellPid <= 0 {
		return foregroundInfo{}, false
	}
	tpgid, ok := readForegroundPGID(shellPid)
	if !ok || tpgid <= 0 {
		return foregroundInfo{}, false
	}
	info := foregroundInfo{
		comm: readComm(tpgid),
		argv: readCmdline(tpgid),
		exe:  readExe(tpgid),
		pid:  tpgid,
	}
	if info.comm == "" && len(info.argv) == 0 {
		// The foreground group leader vanished between reads, or procfs is
		// unavailable: report not-running rather than guess.
		return foregroundInfo{}, false
	}
	return info, true
}

// readExe resolves /proc/<pid>/exe, the real binary behind a process whatever it
// renamed itself to, or "" when it cannot be read. A deleted binary resolves to a
// path with a " (deleted)" suffix, which is stripped so the name still matches.
func readExe(pid int) string {
	target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(target, " (deleted)")
}

// readForegroundPGID reads field 8 (tpgid) of /proc/<pid>/stat, the foreground
// process group id of the process's controlling terminal. The comm field (2) is
// wrapped in parentheses and may itself contain spaces or parentheses, so the
// numeric fields are parsed from after the final ')'.
func readForegroundPGID(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return parseStatTPGID(string(data))
}

// parseStatTPGID extracts the tpgid (foreground process group id, field 8) from
// the contents of a /proc/<pid>/stat line. The comm field (2) is wrapped in
// parentheses and may itself contain spaces or parentheses, so the numeric fields
// are parsed from after the final ')'.
func parseStatTPGID(s string) (int, bool) {
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 >= len(s) {
		return 0, false
	}
	// Fields after "(comm) ": state(3) ppid(4) pgrp(5) session(6) tty_nr(7)
	// tpgid(8). Splitting the remainder gives tpgid at index 5 (state at 0).
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 6 {
		return 0, false
	}
	tpgid, err := strconv.Atoi(fields[5])
	if err != nil {
		return 0, false
	}
	return tpgid, true
}

// readComm returns the trimmed contents of /proc/<pid>/comm, or "" on error.
func readComm(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readCmdline returns the NUL-separated arguments of /proc/<pid>/cmdline as a
// slice, or nil on error or for a kernel thread (empty cmdline).
func readCmdline(pid int) []string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyAgentDetection reconciles each window's agent state with the foreground
// process of its pane, using the injected resolve and identify so it is testable
// without a real /proc or a real agent. It returns how many windows it changed.
//
// resolve reports the foreground process for a PTY and whether it is running;
// identify decides whether that process is an agent and names which harness it
// is, empty when it matched a bare name rather than a manifest.
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
//   - When the agent leaves the foreground (the pane returns to its shell) it
//     clears an owned window back to AgentStateNone and relinquishes ownership.
//
// It never sets any state other than working (on appearance) or none (on
// disappearance): a process name cannot honestly distinguish working from waiting
// or idle, so it does not pretend to.
func (s *Session) applyAgentDetection(
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (harnessID string, ok bool),
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
			harnessID, isAgent := identify(info)
			detected := running && isAgent
			owned := s.agentClaims[w.ID].auto
			switch {
			case detected && !owned:
				// Take ownership only if no state is set, so a manual report wins.
				if w.AgentState == AgentStateNone {
					w.AgentState = AgentStateWorking
					w.AgentMessage = ""
					w.AgentHarness = harnessID
					w.AgentStateAt = now
					s.setAgentClaim(w.ID, agentClaim{source: AgentSourceDetect, harness: harnessID, auto: true})
					changed++
				}
			case !detected && owned:
				// Agent gone from the foreground: relinquish and clear.
				delete(s.agentClaims, w.ID)
				w.AgentState = AgentStateNone
				w.AgentMessage = ""
				w.AgentHarness = ""
				w.AgentStateAt = now
				changed++
			}
			// detected && owned: leave the state to the stall heuristic and to
			// explicit reports. !detected && !owned: not ours, do not touch.
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
//   - The foreground is no longer an agent: the agent quit and the shell prompt
//     is what produced this output, so the state clears at once instead of
//     lingering until the next detection poll.
//   - The foreground is still the agent: the agent is producing output, so it is
//     working. This is the only path back out of the idle the silence timer
//     assigns, and without it a pane latched to idle for the rest of its life
//     however hard the agent then worked.
//
// It obeys the same precedence as applyAgentDetection: it only ever touches a
// window the auto-detector owns, and it only resumes one whose state is the idle
// a source no stronger than the detector left behind, so a harness reporting for
// itself is never overwritten. It reports whether it changed state.
func (s *Session) reconcileAgentOnOutput(
	ptyID string,
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (harnessID string, ok bool),
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
			harnessID, stillAgent := identify(info)
			if running && stillAgent {
				// Still the agent, and it just spoke. Only the idle left by the
				// silence timer (or by the detector itself) may be taken back:
				// anything a stronger source said outranks output activity, which
				// cannot tell working from a redraw while waiting for the user.
				if w.AgentState != AgentStateIdle || claim.source.rank() > AgentSourceDetect.rank() {
					return errNoAgentDetectChange
				}
				w.AgentState = AgentStateWorking
				w.AgentMessage = ""
				// Re-stated rather than cleared: the process just identified is the
				// same one the detector attributed, and a pane with no harness on it
				// has no screen rules to run, so clearing here blinded the very pane
				// that had just proved an agent is alive in it.
				w.AgentHarness = harnessID
				w.AgentStateAt = time.Now().UnixNano()
				claim.harness = harnessID
				claim.source = AgentSourceDetect
				s.setAgentClaim(w.ID, claim)
				changed = true
				return nil
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
