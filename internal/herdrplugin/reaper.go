package herdrplugin

import "strings"

// ReaperArg is the first argument that makes tuios run as a reaper:
// tuios ReaperArg PROGRAM ARGS...
const ReaperArg = "__plugin-reaper"

const reaperGodebugEnv = "TUIOS_PLUGIN_GODEBUG"

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
