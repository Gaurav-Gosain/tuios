package session

import (
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// A child is never wider than its parent.
//
// A pane that starts another pane is its parent, whatever verb it uses:
// start-agent, fan, new-window, new-session, new-worktree, a popup, a plugin
// pane. Two things are held to the parent:
//
//   - Grants. launchGrants already gives a pane only what its caller holds.
//     The one gap was a parent holding admin that names no grants: the child
//     got the default of [agents.permissions], and under strict that list can
//     hold respond, which admin does not imply. childDefaultGrants closes it.
//   - The harness permission mode. An agent started with
//     --dangerously-skip-permissions by an agent in plan mode would do,
//     unasked, what its parent has to ask the person for. clampChildMode
//     reads the parent agent from the caller's process ancestry and the
//     child's mode from the argv it is about to exec, both ranked by
//     harness.PermissionOf, and refuses a child that asks for more. A child
//     that names no mode while its harness default is wider than the parent
//     is started in the widest mode the harness has that is not wider, with
//     the flag added (harness.PermissionArgs); when the harness has none, the
//     child is refused.
//
// The person, outside every pane, is held to neither. Neither is a caller
// over a link, which the link policy holds, nor a native plugin, whose pane
// grants are cut to the plugin's own.

// clampDefaultGrants is what a pane started by pa that names no grants holds:
// nil for the default when pa holds all of it, else the default cut to what
// pa holds. Only a pane holding admin gets here; launchGrants gives a pane
// without admin its own grants.
func (d *Daemon) clampDefaultGrants(pa *paneAuth) *Grants {
	def := d.manager.grants.defaults()
	if pa == nil || pa.grants.Covers(def) {
		return nil
	}
	g := def.expand() & pa.grants.expand() &^ GrantUI
	return &g
}

// childDefaultGrants is clampDefaultGrants for the caller on cs.
func (d *Daemon) childDefaultGrants(cs *connState) *Grants {
	if cs == nil || cs.viaLink || cs.paneOnly {
		return nil
	}
	return d.clampDefaultGrants(d.paneAuthority(cs))
}

// callerAgentProc is the agent process that made a call: the nearest process
// in the caller's ancestry that a harness manifest names.
type callerAgentProc struct {
	harness string
	argv    []string
	pid     int
}

// env reads a variable from the agent's environment.
func (p callerAgentProc) env(name string) (string, bool) {
	return procinfo.EnvVar(p.pid, name)
}

// callerAgent finds the agent behind the caller on cs, walking up from the
// caller's own process: an agent calls tuios from a tool's shell, an MCP
// server it started, or the socket itself. The walk stops at this daemon,
// whose own ancestry is not the caller's, and at the first process a manifest
// names, which is the agent the call came from. It reports false on a
// platform whose process table cannot be read.
func (d *Daemon) callerAgent(cs *connState) (callerAgentProc, bool) {
	if cs == nil || cs.peerPID <= 1 || d.agentMatcher.registry == nil {
		return callerAgentProc{}, false
	}
	self := os.Getpid()
	for cur, depth := cs.peerPID, 0; depth < paneOriginMaxDepth && cur > 1 && cur != self; depth++ {
		info := readProcessInfo(cur)
		if id, _, ok := d.agentMatcher.registry.IdentifyDetail(info.proc()); ok {
			return callerAgentProc{harness: id, argv: info.argv, pid: cur}, true
		}
		ppid, _, ok := readProcLineage(cur)
		if !ok {
			break
		}
		cur = ppid
	}
	return callerAgentProc{}, false
}

// childHarness names the harness argv starts, "" for none. A shell line
// (sh -c '...') is not read: what it runs is the shell's to decide.
func (d *Daemon) childHarness(argv []string) string {
	if len(argv) == 0 || d.agentMatcher.registry == nil {
		return ""
	}
	p := harness.ProcInfo{Comm: harness.BaseName(argv[0]), Argv: argv}
	if harness.IsInterpreter(p.CommBase()) && strings.ContainsAny(p.RunToken(), " \t\n") {
		return ""
	}
	id, _, _ := d.agentMatcher.registry.IdentifyDetail(p)
	return id
}

// clampChildMode holds the permission mode of the agent argv starts to the
// mode of the agent that asks for it. hid is the child's harness, "" to read
// it from argv. env is the KEY=VALUE list the child gets on top of the
// daemon's. It returns the argv to exec: argv itself, or argv with the mode
// flag added that steps the child down. A protocol pane is only checked: its
// mode is set over the protocol, so a flag added there means nothing.
func (d *Daemon) clampChildMode(cs *connState, verb, hid string, argv, env []string, protocol bool) ([]string, *verbError) {
	if len(argv) == 0 {
		return argv, nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil || !pa.inPane() {
		return argv, nil
	}
	if hid == "" {
		hid = d.childHarness(argv)
	}
	if !harness.PermissionRanked(hid) {
		return argv, nil
	}
	parent, ok := d.callerAgent(cs)
	if !ok {
		return argv, nil
	}
	pp, ranked := harness.PermissionOf(parent.harness, parent.argv, parent.env)
	if !ranked {
		// The parent is an agent whose modes tuios does not rank.
		return argv, nil
	}
	limit := pp.Level
	if !pp.Known {
		// A mode tuios does not know may be anything: hold the child to the
		// narrowest.
		limit = harness.PermissionReadOnly
	}
	cp, _ := harness.PermissionOf(hid, argv, envLookup(env))
	level := cp.Level
	if !cp.Known {
		level = harness.PermissionFull
	}
	if level <= limit || protocol && !cp.Named {
		return argv, nil
	}
	parentMode := pp.Mode
	if parentMode == "" {
		parentMode = "the default mode"
	}
	parentMode = parent.harness + " in " + parentMode + " (" + limit.String() + ")"
	if cp.Named {
		LogBasic("Pane %s refused %s: child %s asks for %s, parent is %s", shortWindowID(pa.window), verb, hid, cp.Mode, parentMode)
		return nil, childModeRefusal(verb, "the new agent asks for permission mode "+cp.Mode+" ("+level.String()+"), which is wider than the agent that starts it: "+parentMode)
	}
	args, got, ok := harness.PermissionArgs(hid, limit)
	if !ok || protocol {
		LogBasic("Pane %s refused %s: child %s has no permission mode as narrow as %s", shortWindowID(pa.window), verb, hid, parentMode)
		return nil, childModeRefusal(verb, "the new agent starts in the default permission mode of "+hid+" ("+level.String()+"), which is wider than the agent that starts it: "+parentMode+". "+hid+" has no narrower mode that tuios can set")
	}
	at := modeArgsAt(argv)
	out := slices.Concat(argv[:at], args, argv[at:])
	LogBasic("Pane %s %s: child %s stepped down to %s (%s) to match %s", shortWindowID(pa.window), verb, hid, strings.Join(args, " "), got, parentMode)
	return out, nil
}

// modeArgsAt is where mode flags go in argv: after the program, or after the
// script an interpreter runs.
func modeArgsAt(argv []string) int {
	if !harness.IsInterpreter(harness.BaseName(argv[0])) {
		return 1
	}
	for i := 1; i < len(argv); i++ {
		if !strings.HasPrefix(argv[i], "-") {
			return i + 1
		}
	}
	return 1
}

// childModeRefusal is the refusal for a child wider than its parent.
func childModeRefusal(verb, why string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" is refused: "+why, &VerbHint{
		Detail: "Nothing was started. A child agent never gets a wider permission mode than its parent. " +
			"Start the child in your mode or a narrower one. " +
			"If the work needs more, ask the person with tuios ask-human. The person can start the agent from outside every pane.",
	})
}

// envLookup reads a KEY=VALUE list.
func envLookup(env []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				return v, true
			}
		}
		return "", false
	}
}
