package session

import (
	"fmt"
	"reflect"
	"strconv"
	"unicode/utf8"
)

// Bounds on the gob payloads a client or a linked peer can send the daemon.
//
// gob decodes a recursive type by recursion and has no depth limit for a type
// the receiver knows. Two payloads the daemon decodes can nest without end:
// a SessionState, through SerializedBSPNode.Left and Right, and a
// CommandResultPayload, through Data, whose map[string]any values can hold
// another map or any registered type, SessionState included. A goroutine that
// runs out of stack is a fatal error, not a panic, so a deep enough payload
// stops the whole daemon and every session in it, and the 16 MB limit on any
// frame does not prevent that.
//
// Measured on darwin/arm64 with go1.27 at depths of a few thousand: a tree
// level costs at least 2 bytes on the wire and 512 bytes of decode stack, so
// 256 bytes of stack per byte of frame; a nested map level costs 32 bytes on
// the wire and about 2.2 KB of stack, about 70 per byte. The runtime's stack
// limit is 1 GB on 64-bit systems and 250 MB on 32-bit ones, which the
// releases include.
//
// So the two message types get a frame limit of their own, read from the
// header and enforced before the body is decoded, and sized to the daemon's
// word size: 1 MB on 64-bit, where the worst tree it admits needs about 256
// MB of stack, and 768 KB on 32-bit, about 192 MB. A 32-bit frame is smaller
// per level, so that bound is the high side.
//
// What a client pushes is bounded well under both. It sends the layout and,
// per pane, two ids, the title, the name the user gave it and the popup size;
// the directory, the agent fields and the rest are the daemon's own and are
// merged in on its side. The title and the name are clamped to
// maxDisplayTextBytes on both sides, so a program that sets a long title
// cannot push its session over the limit. largeRealisticState in the tests
// builds that push with every field at its maximum.
//
// After decoding, a state's trees are walked without recursion and refused
// when deeper or larger than any real layout, before anything that does
// recurse (the merge, the fingerprint, the save, the rebroadcast) sees them,
// and a command result's data is held to a depth no command produces.
//
// None of this changes the wire. An older client or peer sends the same
// frames, and only what is over a limit is refused.

// payloadCap is the frame limit for the two message types above on a daemon
// with this word size.
func payloadCap(intSize int) int {
	if intSize < 64 {
		return 768 << 10
	}
	return 1 << 20
}

var (
	// maxStateUpdateBytes is the largest MsgUpdateState payload the daemon
	// reads. A variable so a test can hold a 64-bit daemon to the 32-bit
	// limit.
	maxStateUpdateBytes = payloadCap(strconv.IntSize)

	// maxCommandResultBytes is the largest MsgCommandResult payload the
	// daemon reads. A real result is a window list, a dock or hook table, or
	// a message: tens of kilobytes for hundreds of panes. Data may hold a
	// SessionState, so it takes the state update's limit.
	maxCommandResultBytes = payloadCap(strconv.IntSize)
)

const (
	// maxBSPDepth is the deepest layout tree a state may carry. Each split
	// adds one level and every leaf is a pane, so a tree this deep takes 257
	// panes in one workspace, every one split off the same side.
	maxBSPDepth = 256

	// maxBSPNodes is the most tree nodes a state may carry over all its
	// workspaces. A tree of n panes has 2n-1 nodes, so this is 8192 panes.
	maxBSPNodes = 16383

	// maxResultDataDepth is how deeply a command result's data may nest, in
	// maps, slices and structs. The deepest a command makes is a map holding
	// a list of maps holding a list: 4.
	maxResultDataDepth = 16

	// maxDisplayTextBytes is the most a pane's title, user-given name, agent
	// message or foreground command line carries in session state. Wider
	// than any bar, sidebar row or list that draws them.
	maxDisplayTextBytes = 256
)

// maxFrameBytes is the limit on any frame, whatever its type.
const maxFrameBytes = 16 * 1024 * 1024

// daemonFrameLimit is the largest frame the daemon reads for a message type:
// the payload plus the type and codec bytes.
func daemonFrameLimit(t MessageType) uint32 {
	switch t {
	case MsgUpdateState:
		return uint32(maxStateUpdateBytes) + 2
	case MsgCommandResult:
		return uint32(maxCommandResultBytes) + 2
	}
	return maxFrameBytes
}

// ClampDisplayText cuts s to maxDisplayTextBytes on a rune boundary. Session
// state holds display text through it on both sides, so a client and its
// daemon agree on what a pane is called.
func ClampDisplayText(s string) string {
	if len(s) <= maxDisplayTextBytes {
		return s
	}
	cut := maxDisplayTextBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// clampPushedText clamps the display text a client pushed, for an older
// client that does not clamp its own.
func clampPushedText(s *SessionState) {
	for i := range s.Windows {
		s.Windows[i].Title = ClampDisplayText(s.Windows[i].Title)
		s.Windows[i].CustomName = ClampDisplayText(s.Windows[i].CustomName)
	}
}

// FrameTooLargeError is a frame over the limit for its message type. The
// reader has skipped its body, so the stream is still in step and the
// connection can go on.
type FrameTooLargeError struct {
	Type  MessageType
	Size  uint32
	Limit uint32
}

func (e *FrameTooLargeError) Error() string {
	return fmt.Sprintf("%s message of %d bytes is over the %d byte limit for that type and was not read",
		MessageTypeName(e.Type), e.Size, e.Limit)
}

// errLayoutTooDeep, errLayoutTooLarge and errResultTooDeep refuse what no
// real session or command produces.
var (
	errLayoutTooDeep  = fmt.Errorf("layout tree is deeper than %d levels", maxBSPDepth)
	errLayoutTooLarge = fmt.Errorf("layout trees hold more than %d nodes", maxBSPNodes)
	errResultTooDeep  = fmt.Errorf("result data nests deeper than %d levels", maxResultDataDepth)
)

// validateSessionState checks the layout trees of a state that came off the
// wire, without recursion. A nil state is valid.
func validateSessionState(s *SessionState) error {
	if s == nil {
		return nil
	}
	nodes := 0
	for ws, tree := range s.WorkspaceTrees {
		if tree == nil {
			continue
		}
		if err := checkBSPTree(tree.Root, &nodes); err != nil {
			return fmt.Errorf("workspace %d: %w", ws, err)
		}
	}
	return nil
}

// checkBSPTree walks one tree with an explicit stack, adding its nodes to
// *nodes, and stops at the first node past either limit, so the work is
// bounded by the limits and not by the tree.
func checkBSPTree(root *SerializedBSPNode, nodes *int) error {
	type item struct {
		n     *SerializedBSPNode
		depth int
	}
	if root == nil {
		return nil
	}
	stack := []item{{root, 0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if it.depth > maxBSPDepth {
			return errLayoutTooDeep
		}
		*nodes++
		if *nodes > maxBSPNodes {
			return errLayoutTooLarge
		}
		if it.n.Left != nil {
			stack = append(stack, item{it.n.Left, it.depth + 1})
		}
		if it.n.Right != nil {
			stack = append(stack, item{it.n.Right, it.depth + 1})
		}
	}
	return nil
}

// checkResultData holds a command result's data to maxResultDataDepth
// levels of maps, slices and structs, without recursion. A SessionState
// carried in it is held to the same depth, which its layout trees exceed long
// before they could matter.
func checkResultData(data map[string]any) error {
	type item struct {
		v     reflect.Value
		depth int
	}
	if data == nil {
		return nil
	}
	stack := []item{{reflect.ValueOf(data), 1}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		v := it.v
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			if v.IsNil() {
				break
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		default:
			continue
		}
		if it.depth > maxResultDataDepth {
			return errResultTooDeep
		}
		switch v.Kind() {
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				stack = append(stack, item{iter.Value(), it.depth + 1})
			}
		case reflect.Slice, reflect.Array:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				continue
			}
			for i := 0; i < v.Len(); i++ {
				stack = append(stack, item{v.Index(i), it.depth + 1})
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					stack = append(stack, item{v.Field(i), it.depth + 1})
				}
			}
		}
	}
	return nil
}
