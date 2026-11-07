package session

import (
	"log"
	"runtime"
	"strconv"
	"strings"
)

// debugTreeKeys renders every workspace tree of state by window ID, for the
// tree change log. Nil when debug logging is off, so the hot path pays only
// the switch.
func debugTreeKeys(state *SessionState) map[int]string {
	if state == nil || !debugEnabled() {
		return nil
	}
	name := SessionTreeNames(state)
	keys := make(map[int]string, len(state.WorkspaceTrees))
	for ws, tree := range state.WorkspaceTrees {
		keys[ws] = TreeKey(tree, name)
	}
	return keys
}

// debugLogTreeChange logs one state version: its cause, the code that made
// it, and each workspace tree it changed. A layout that changes after a
// client wrote it is found by reading this log. Only with TUIOS_DEBUG_INTERNAL.
func debugLogTreeChange(cause string, version int, before map[int]string, state *SessionState) {
	if !debugEnabled() {
		return
	}
	after := debugTreeKeys(state)
	var changed []string
	for ws, key := range after {
		if before[ws] != key {
			changed = append(changed, "ws"+strconv.Itoa(ws)+" "+key)
		}
	}
	for ws := range before {
		if _, ok := after[ws]; !ok {
			changed = append(changed, "ws"+strconv.Itoa(ws)+" removed")
		}
	}
	log.Printf("[tree] v%d %s by %s; trees changed: %s", version, cause, debugCallers(), strings.Join(changed, "; "))
}

// debugCallers names the functions above the state lock that asked for the
// change, nearest first, skipping the session's own plumbing.
func debugCallers() string {
	pcs := make([]uintptr, 12)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var names []string
	for len(names) < 4 {
		f, more := frames.Next()
		fn := f.Function[strings.LastIndex(f.Function, "/")+1:]
		if !strings.Contains(fn, "mutateState") && !strings.Contains(fn, "debugLog") && !strings.Contains(fn, "func") {
			names = append(names, fn)
		}
		if !more {
			break
		}
	}
	return strings.Join(names, " < ")
}
