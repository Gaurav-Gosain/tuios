package session

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
)

// StateFingerprint reduces a session state to a 64-bit value that changes when
// the state changes and does not change when it does not.
//
// It exists so both ends of a state sync can tell a push that says something
// from a push that says nothing. A client sends its whole state after every
// keystroke and every click, and the daemon forwards every one of those to
// every peer, which rebuilds its window list, prunes its maps and redraws. When
// nothing moved, that whole round is spent arriving at the state everyone
// already held: measured on two attached clients, thirty-one keystrokes
// produced thirty-two peer broadcasts and all thirty-two were identical to the
// one before.
//
// Written by hand rather than by hashing an encoding, because gob and JSON both
// walk a Go map in the map's own iteration order, which is deliberately not
// stable between passes. A fingerprint built that way would differ for a state
// that had not changed, which is precisely the case this has to recognise. Map
// keys are sorted here for that reason.
//
// Every field a peer acts on is covered. Fields the daemon owns and rewrites on
// the way through (Version, BaseVersion) are covered too: the fingerprint
// describes the state that is about to be sent, not the state a client meant to
// send.
func StateFingerprint(s *SessionState) uint64 {
	if s == nil {
		return 0
	}
	h := fnv.New64a()

	str := func(v string) {
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte{0})
	}
	num := func(v int) { str(strconv.Itoa(v)) }
	flag := func(v bool) {
		if v {
			str("1")
		} else {
			str("0")
		}
	}
	// Bit pattern rather than a decimal rendering, so two ratios that print the
	// same but are not equal do not fingerprint the same.
	f64 := func(v float64) { str(strconv.FormatUint(math.Float64bits(v), 16)) }

	str(s.Name)
	str(s.DisplayName)
	str(s.Accent)
	flag(s.Restored)
	str(s.FocusedWindowID)
	num(s.CurrentWorkspace)
	f64(s.MasterRatio)
	flag(s.AutoTiling)
	num(s.Width)
	num(s.Height)
	num(s.NextBSPWindowID)
	num(s.TilingScheme)
	str(s.LayoutMode)
	num(s.NumWorkspaces)
	num(s.SidebarWidth)
	flag(s.SidebarCollapsed)
	num(s.ResurrectionVersion)
	num(s.Version)
	num(s.BaseVersion)

	// Windows are ordered, and the order is meaningful (it is the z-order the
	// peer rebuilds its list in), so they are hashed as they stand.
	num(len(s.Windows))
	for i := range s.Windows {
		w := &s.Windows[i]
		str(w.ID)
		str(w.Title)
		str(w.CustomName)
		num(w.X)
		num(w.Y)
		num(w.Width)
		num(w.Height)
		num(w.Z)
		num(w.Workspace)
		flag(w.Minimized)
		num(w.PreMinimizeX)
		num(w.PreMinimizeY)
		num(w.PreMinimizeW)
		num(w.PreMinimizeH)
		str(w.PTYID)
		flag(w.IsAltScreen)
		flag(w.IsFloating)
		str(w.Cwd)
		flag(w.Unplaced)
		str(string(w.AgentState))
		str(w.AgentMessage)
		num(int(w.AgentStateAt))
		str(w.AgentHarness)
		str(w.ForegroundCmd)
	}

	hashIntStr := func(m map[int]string) {
		num(len(m))
		for _, k := range sortedIntKeys(m) {
			num(k)
			str(m[k])
		}
	}
	hashIntStr(s.WorkspaceFocus)
	hashIntStr(s.WorkspaceNames)

	num(len(s.WorkspaceOrder))
	for _, v := range s.WorkspaceOrder {
		num(v)
	}

	num(len(s.WorkspaceTrees))
	for _, k := range sortedIntKeys(s.WorkspaceTrees) {
		num(k)
		hashBSPNode(h, s.WorkspaceTrees[k])
	}

	num(len(s.WindowToBSPID))
	for _, k := range sortedStringKeys(s.WindowToBSPID) {
		str(k)
		num(s.WindowToBSPID[k])
	}

	num(len(s.Options))
	for _, k := range sortedStringKeys(s.Options) {
		str(k)
		str(s.Options[k])
	}

	// Nil and the zero value are distinguished here too, and for the same
	// reason: a strip scrolled home is not a peer with nothing to say about it.
	if s.ScrollStrip == nil {
		str("nil-strip")
	} else {
		str("strip")
		num(s.ScrollStrip.ViewportX)
	}

	// Nil and the zero value are distinguished: nil is a peer that has not said,
	// and a peer adopting on receipt has to see the difference.
	if s.PaneGeometry == nil {
		str("nil-geometry")
	} else {
		str("geometry")
		flag(s.PaneGeometry.SharedBorders)
		num(s.PaneGeometry.PaneGap)
		num(s.PaneGeometry.ScrollColumnWidth)
	}

	return h.Sum64()
}

// hashBSPNode folds a serialized tree in, shape and all. A nil tree and an
// empty one are distinguished, because they mean different things to a peer.
func hashBSPNode(h interface{ Write([]byte) (int, error) }, t *SerializedBSPTree) {
	write := func(v string) {
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte{0})
	}
	if t == nil {
		write("nil-tree")
		return
	}
	write("tree")
	write(strconv.Itoa(t.AutoScheme))
	write(strconv.FormatUint(math.Float64bits(t.DefaultRatio), 16))

	var walk func(n *SerializedBSPNode)
	walk = func(n *SerializedBSPNode) {
		if n == nil {
			write("nil")
			return
		}
		write("n")
		write(strconv.Itoa(n.WindowID))
		write(strconv.Itoa(n.SplitType))
		write(strconv.FormatUint(math.Float64bits(n.SplitRatio), 16))
		walk(n.Left)
		walk(n.Right)
	}
	walk(t.Root)
}

func sortedIntKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
