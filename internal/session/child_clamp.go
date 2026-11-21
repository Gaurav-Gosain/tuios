package session

import (
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func (d *Daemon) clampDefaultGrants(pa *paneAuth) *Grants {
	def := d.manager.grants.defaults()
	if pa == nil || pa.grants.Covers(def) {
		return nil
	}
	g := def.expand() & pa.grants.expand() &^ GrantUI
	return &g
}

func (d *Daemon) childDefaultGrants(cs *connState) *Grants {
	if cs == nil || cs.viaLink || cs.paneOnly {
		return nil
	}
	return d.clampDefaultGrants(d.paneAuthority(cs))
}

type callerAgentProc struct {
	harness string
	argv    []string
	pid     int
}

func (p callerAgentProc) env(name string) (string, bool) {
	return procinfo.EnvVar(p.pid, name)
}

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
		return argv, nil
	}
	limit := pp.Level
	if !pp.Known {
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

func childModeRefusal(verb, why string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" is refused: "+why, &VerbHint{
		Detail: "Nothing was started. A child agent never gets a wider permission mode than its parent. " +
			"Start the child in your mode or a narrower one. " +
			"If the work needs more, ask the person with tuios ask-human. The person can start the agent from outside every pane.",
	})
}

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
