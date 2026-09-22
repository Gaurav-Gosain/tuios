package integration

// maxAncestors bounds the parent walk. A hook is rarely more than a handful of
// processes below the pane's shell; the bound only matters for a cycle a racy
// read could make.
const maxAncestors = 32

// SelfProcess reports what the current process can say about where it runs:
// its session id, which is the pane shell's pid when its controlling terminal
// is a tuios pane, and its ancestors, nearest first, for when it is not. The
// daemon's resolve-pane verb matches either against the shells it started.
// A platform with no way to read them reports zero and nil.
func SelfProcess() (sid int, ancestors []int) {
	sid = selfSID()
	seen := map[int]bool{}
	for pid := parentPID(0); pid > 1 && len(ancestors) < maxAncestors && !seen[pid]; pid = parentPID(pid) {
		seen[pid] = true
		ancestors = append(ancestors, pid)
	}
	return sid, ancestors
}
