package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
}

// wrapperInterpreters are interpreters and launchers that run an agent as a
// script rather than being the agent themselves. When the foreground process is
// one of these, the detector also inspects the command line arguments so a
// wrapped agent (for example "node .../claude" or "npx opencode") is still
// recognised. argv0 alone would name only the interpreter.
var wrapperInterpreters = map[string]struct{}{
	"node": {}, "nodejs": {}, "deno": {}, "bun": {},
	"python": {}, "python2": {}, "python3": {}, "uv": {}, "uvx": {},
	"npx": {}, "pnpm": {}, "yarn": {}, "bunx": {},
	"sh": {}, "bash": {}, "zsh": {}, "fish": {}, "env": {},
}

// scriptExtensions are stripped from an argv base name before matching, so a
// script argument such as "claude.js" matches the agent name "claude".
var scriptExtensions = []string{".js", ".mjs", ".cjs", ".ts", ".py"}

// agentMatcher decides whether a foreground process is a known AI-agent CLI. It
// holds the resolved set of agent names (defaults merged with user additions),
// lowercased for case-insensitive matching.
type agentMatcher struct {
	names map[string]struct{}
}

// newAgentMatcher builds a matcher from the built-in defaults plus any extra
// names. Extra names are trimmed and lowercased; blanks are ignored.
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
	return agentMatcher{names: names}
}

// isAgent reports whether the foreground process named by comm (its /proc/comm)
// and argv (its full command line) is a known agent. comm is checked first; if it
// names an interpreter, argv is scanned so a wrapped agent is still caught.
func (m agentMatcher) isAgent(comm string, argv []string) bool {
	base := agentBaseName(comm)
	if base == "" {
		// comm can be empty when only argv is available; fall through to argv.
	} else {
		if _, ok := m.names[base]; ok {
			return true
		}
		if _, wrapper := wrapperInterpreters[base]; !wrapper {
			// Not an interpreter, and comm did not match: trust comm and stop,
			// rather than risk a false positive from an incidental argument.
			//
			// The one exception is when comm is empty above; there we still scan
			// argv because comm told us nothing.
			return false
		}
	}
	// A wrapped agent is named somewhere inside the interpreter's arguments, most
	// often as a path component of the script it runs (for example
	// ".../node_modules/@anthropic-ai/claude-code/cli.js", or "/usr/bin/claude").
	// Scan each argument's path components, not just its base name, so the script
	// file being cli.js does not hide the agent named by its directory.
	for _, arg := range argv {
		if m.argNamesAgent(arg) {
			return true
		}
	}
	return false
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
	for _, comp := range strings.Split(arg, "/") {
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
// matching: it drops any directory, a trailing NUL, surrounding whitespace, and a
// known script extension, and lowercases the result. A leading '-' (a login
// shell's argv0, e.g. "-bash") is stripped too.
func agentBaseName(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
	if s == "" {
		return ""
	}
	s = filepath.Base(s)
	s = strings.TrimPrefix(s, "-")
	lower := strings.ToLower(s)
	for _, ext := range scriptExtensions {
		if strings.HasSuffix(lower, ext) {
			return strings.TrimSuffix(lower, ext)
		}
	}
	return lower
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
func foregroundProcess(shellPid int) (comm string, argv []string, running bool) {
	if shellPid <= 0 {
		return "", nil, false
	}
	tpgid, ok := readForegroundPGID(shellPid)
	if !ok || tpgid <= 0 {
		return "", nil, false
	}
	comm = readComm(tpgid)
	argv = readCmdline(tpgid)
	if comm == "" && len(argv) == 0 {
		// The foreground group leader vanished between reads, or procfs is
		// unavailable: report not-running rather than guess.
		return "", nil, false
	}
	return comm, argv, true
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
// process of its pane, using the injected resolve and isAgent so it is testable
// without a real /proc or a real agent. It returns how many windows it changed.
//
// resolve reports the foreground process (comm, argv) for a PTY and whether that
// process is running; isAgent decides whether that process is an agent.
//
// Precedence (auto-detection is deliberately subordinate to explicit reports):
//
//   - It promotes a window to AgentStateWorking only when an agent appears in the
//     foreground AND the window currently has no agent state (AgentStateNone).
//     A window a user already set through set-agent-state is never overwritten.
//   - It records the windows it promoted (autoAgentOwned) and only ever manages
//     those. While it owns a window and the agent is still in the foreground it
//     leaves the state alone, so the output-stall heuristic may demote it to idle
//     and an explicit set-agent-state may move it anywhere; either wins until the
//     agent exits.
//   - When the agent leaves the foreground (the pane returns to its shell) it
//     clears an owned window back to AgentStateNone and relinquishes ownership.
//
// It never sets any state other than working (on appearance) or none (on
// disappearance): a process name cannot honestly distinguish working from waiting
// or idle, so it does not pretend to.
func (s *Session) applyAgentDetection(
	resolve func(ptyID string) (comm string, argv []string, running bool),
	isAgent func(comm string, argv []string) bool,
) int {
	changed := 0
	_ = s.mutateState(func(st *SessionState) error {
		live := make(map[string]struct{}, len(st.Windows))
		now := time.Now().UnixNano()
		for i := range st.Windows {
			w := &st.Windows[i]
			if w.PTYID == "" {
				continue
			}
			live[w.ID] = struct{}{}
			comm, argv, running := resolve(w.PTYID)
			detected := running && isAgent(comm, argv)
			owned := s.autoAgentOwned[w.ID]
			switch {
			case detected && !owned:
				// Take ownership only if no state is set, so a manual report wins.
				if w.AgentState == AgentStateNone {
					w.AgentState = AgentStateWorking
					w.AgentMessage = ""
					w.AgentStateAt = now
					if s.autoAgentOwned == nil {
						s.autoAgentOwned = make(map[string]bool)
					}
					s.autoAgentOwned[w.ID] = true
					changed++
				}
			case !detected && owned:
				// Agent gone from the foreground: relinquish and clear.
				delete(s.autoAgentOwned, w.ID)
				w.AgentState = AgentStateNone
				w.AgentMessage = ""
				w.AgentStateAt = now
				changed++
			}
			// detected && owned: leave the state to the stall heuristic and to
			// explicit reports. !detected && !owned: not ours, do not touch.
		}
		// Drop ownership of windows that no longer exist so the map cannot grow
		// without bound. This touches only in-memory bookkeeping, never state, so
		// it does not count as a change.
		for id := range s.autoAgentOwned {
			if _, ok := live[id]; !ok {
				delete(s.autoAgentOwned, id)
			}
		}
		if changed == 0 {
			// No state change: skip the version bump and client push.
			return errNoAgentDetectChange
		}
		return nil
	})
	return changed
}

// clearExitedAgent clears the auto-detected agent state of the window backed by
// ptyID the moment its foreground process is no longer an agent, so the sidebar
// glyph disappears when the agent quits instead of lingering until the next
// detection poll. It is driven by the pane's own output (the shell prompt
// returning), not a timer, so it adds no idle cost.
//
// It obeys the same precedence as applyAgentDetection: it only ever touches a
// window the auto-detector owns and only ever clears it, so a manual
// set-agent-state and a still-running agent are both left alone. It reports
// whether it changed state.
func (s *Session) clearExitedAgent(
	ptyID string,
	resolve func(ptyID string) (comm string, argv []string, running bool),
	isAgent func(comm string, argv []string) bool,
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
			if !s.autoAgentOwned[w.ID] {
				return errNoAgentDetectChange
			}
			if comm, argv, running := resolve(ptyID); running && isAgent(comm, argv) {
				return errNoAgentDetectChange
			}
			delete(s.autoAgentOwned, w.ID)
			w.AgentState = AgentStateNone
			w.AgentMessage = ""
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
// keeps clearExitedAgent off the write lock for panes it would never touch.
func (s *Session) ownsAutoAgent(ptyID string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if s.state.Windows[i].PTYID == ptyID {
			return s.autoAgentOwned[s.state.Windows[i].ID]
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
