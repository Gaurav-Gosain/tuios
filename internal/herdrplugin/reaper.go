package herdrplugin

import "strings"

// The reaper: a small tuios process that leads each plugin command's
// process session.
//
// The daemon knows a plugin's process by its kernel session id, which is the
// pid of the session's leader (Runner.Owner). That answer stays true only
// while the pid cannot name another session. The kernel does not hand out a
// pid while a live process has it, so the runner starts each command under
// the reaper, which leads the session and lives until nothing the command
// started is left. Its exit is the moment the session is empty, and the
// runner drops the owner entry then. No timer runs in the daemon for it.
//
// On Linux the reaper is a child subreaper (PR_SET_CHILD_SUBREAPER): every
// process the command leaves behind is reparented to it, so it waits for
// them all and exits when it has no child left. macOS has no subreaper. There
// the reaper looks at its process group once a second, and only after the
// command itself has ended while something it started still runs.
//
// The reaper tells the runner when the command itself exits, on fd 3, so a
// job's log, exit code and slot do not wait for what the command left
// behind.

// ReaperArg is the first argument that makes tuios run as a reaper:
// tuios ReaperArg PROGRAM ARGS...
const ReaperArg = "__plugin-reaper"

// The reaper starts with GODEBUG=disablethp=1. With transparent huge pages
// set to always, the Go heap faults in as 2 MB pages, which more than
// doubled a reaper's private memory (5 to 7 MB against 2.7 MB, measured).
// The setting has to be there when the process starts, so the runner sets
// it, and the reaper gives the plugin command the GODEBUG it would have had:
// reaperGodebugEnv carries that value, or its absence.
const reaperGodebugEnv = "TUIOS_PLUGIN_GODEBUG"

// reaperEnv is env for the reaper: GODEBUG with disablethp=1 added last, and
// the command's own GODEBUG kept in reaperGodebugEnv.
func reaperEnv(env []string) []string {
	orig, had := "", false
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "GODEBUG="); ok {
			orig, had = v, true
			continue
		}
		if strings.HasPrefix(kv, reaperGodebugEnv+"=") {
			continue
		}
		out = append(out, kv)
	}
	godebug, keep := "disablethp=1", "unset"
	if had {
		keep = "set:" + orig
		if orig != "" {
			godebug = orig + ",disablethp=1"
		}
	}
	return append(out, "GODEBUG="+godebug, reaperGodebugEnv+"="+keep)
}

// commandEnv is the reaper's environment as the command gets it: GODEBUG as
// the runner had it before reaperEnv.
func commandEnv(env []string) []string {
	keep := ""
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, reaperGodebugEnv+"="); ok {
			keep = v
			continue
		}
		if strings.HasPrefix(kv, "GODEBUG=") {
			continue
		}
		out = append(out, kv)
	}
	if v, ok := strings.CutPrefix(keep, "set:"); ok {
		out = append(out, "GODEBUG="+v)
	}
	return out
}
