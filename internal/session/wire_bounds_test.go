package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The bounds in wire_bounds.go are a security boundary: they are what stands
// between a client or linked peer and a fatal stack overflow in the daemon.
// This test drives a real daemon over its socket with two raw clients of one
// session, A pushing and B watching, and holds the daemon to each bound at its
// edge, with the largest real push on the accepted side. It runs the whole
// scenario at the 64-bit cap and again at the 32-bit one. No payload here is
// anywhere near deep enough to hurt a daemon without the bounds: the deepest
// tree is 257 levels and the largest frame just over 1 MB.
//
// The ways this could fail, written down before the code:
//
//  1. A real session is refused. largeRealisticState at 9 x 41 panes, layouts
//     40 deep, every field a client sends at its maximum, must be accepted
//     and rebroadcast under either cap, with at least a third of it spare.
//  2. A long title or name pushes a session over the cap, or a client and its
//     daemon disagree on it. The client clamps what it builds
//     (BuildSessionState); the daemon clamps what an older client pushes and
//     its own titles, so what the other client hears is the clamped text, cut
//     on a rune boundary.
//  3. The limit is off by one or counted on the wrong length: a frame of
//     exactly the limit must be read, one byte more refused.
//  4. The limit ignores the word size, and a 32-bit daemon still dies on a
//     tree under its cap: each word size's cap must keep the worst tree's
//     decode stack well under that size's stack limit, the daemon must use
//     the cap for its own word size, and at the 32-bit cap a frame just over
//     it is refused even though the 64-bit cap would take it.
//  5. A refused frame leaves the stream out of step, so the connection dies
//     or the next message is misread: the sender must go on being served.
//  6. The refusal is silent: the sender must get an error that says what
//     was refused and why.
//  7. One client's refusal takes the session or the daemon down for others:
//     the other client must keep receiving and sending.
//  8. The depth check is off by one or checks only one side of the tree: a
//     tree exactly maxBSPDepth deep is accepted, one level more is refused,
//     on either side.
//  9. A wide tree gets past a depth check: node count over the limit is
//     refused.
//  10. The bound is on one message type only: a command result's data, which
//     nests through map[string]any, is held to its own frame cap and depth.
func TestDaemonBoundsWhatClientsSend(t *testing.T) {
	// The decode stack of the worst tree a cap admits, at the 256 bytes of
	// stack per byte of frame measured in wire_bounds.go, has to stay well
	// under the runtime's stack limit for that word size: 1 GB on 64-bit,
	// 250 MB on 32-bit.
	const treeStackPerWireByte = 256
	for bits, ceiling := range map[int]int{64: 300 << 20, 32: 200 << 20} {
		if worst := payloadCap(bits) * treeStackPerWireByte; worst > ceiling {
			t.Errorf("on %d-bit the cap admits a tree needing %d MB of stack, over %d MB", bits, worst>>20, ceiling>>20)
		}
	}
	if maxStateUpdateBytes != payloadCap(strconv.IntSize) || maxCommandResultBytes != payloadCap(strconv.IntSize) {
		t.Errorf("the daemon's caps are not the ones for its own %d-bit word size", strconv.IntSize)
	}
	for _, bits := range []int{64, 32} {
		t.Run(fmt.Sprintf("%d-bit cap", bits), func(t *testing.T) {
			oldState, oldResult := maxStateUpdateBytes, maxCommandResultBytes
			maxStateUpdateBytes, maxCommandResultBytes = payloadCap(bits), payloadCap(bits)
			t.Cleanup(func() { maxStateUpdateBytes, maxCommandResultBytes = oldState, oldResult })
			runBoundsScenario(t)
		})
	}
}

func runBoundsScenario(t *testing.T) {
	_, socketPath := startTestDaemon(t)
	a := dialBoundsClient(t, socketPath, "bounds")
	b := dialBoundsClient(t, socketPath, "bounds")

	// B hears every accepted push from A as a state sync; that is the
	// evidence the daemon took a state and is still serving B.
	heard := func(t *testing.T, windows int) *SessionState {
		t.Helper()
		msg := b.await(t, MsgStateSync)
		var sync StateSyncPayload
		if err := msg.ParsePayload(&sync); err != nil {
			t.Fatalf("B could not read the state sync: %v", err)
		}
		if sync.State == nil || len(sync.State.Windows) != windows {
			t.Fatalf("B heard a state with the wrong windows: want %d", windows)
		}
		a.noError(t)
		return sync.State
	}
	pushAccepted := func(t *testing.T, st *SessionState, windows int) {
		t.Helper()
		a.send(t, MsgUpdateState, st)
		heard(t, windows)
	}
	small := func(n int) *SessionState {
		return largeRealisticState(1, n)
	}

	t.Run("the largest real push is accepted", func(t *testing.T) {
		st := largeRealisticState(9, 41)
		sb, _ := encodePayload(st)
		t.Logf("9 x 41 panes, every pushed field at its maximum: %d bytes; cap %d", len(sb), maxStateUpdateBytes)
		if 3*len(sb) > 2*maxStateUpdateBytes {
			t.Fatalf("the largest real push is %d bytes, which leaves less than a third of the %d byte cap spare", len(sb), maxStateUpdateBytes)
		}
		pushAccepted(t, st, 9*41)
	})

	t.Run("display text is clamped on a rune boundary", func(t *testing.T) {
		long := strings.Repeat("ét世", 1000) // 6 bytes a repeat, runes of 2, 1 and 3 bytes
		st := small(2)
		st.Windows[0].Title, st.Windows[0].CustomName = long, long
		a.send(t, MsgUpdateState, st)
		got := heard(t, 2)
		for _, s := range []string{got.Windows[0].Title, got.Windows[0].CustomName} {
			if len(s) > maxDisplayTextBytes || len(s) < maxDisplayTextBytes-3 || !utf8.ValidString(s) || !strings.HasPrefix(long, s) {
				t.Fatalf("display text was not clamped to %d bytes on a rune boundary: %d bytes, valid %v", maxDisplayTextBytes, len(s), utf8.ValidString(s))
			}
		}
	})

	t.Run("a state frame over the cap is refused and the sender still served", func(t *testing.T) {
		// The body is never decoded when it is over the cap, so its content
		// does not matter. At the cap it is read and fails to decode instead.
		a.sendRaw(t, MsgUpdateState, make([]byte, maxStateUpdateBytes))
		if msg := a.awaitError(t); strings.Contains(msg, "byte limit") {
			t.Fatalf("a frame of exactly the cap was refused for its size: %s", msg)
		}
		a.sendRaw(t, MsgUpdateState, make([]byte, maxStateUpdateBytes+1))
		msg := a.awaitError(t)
		if !strings.Contains(msg, "UpdateState") || !strings.Contains(msg, fmt.Sprintf("%d byte limit", maxStateUpdateBytes+2)) {
			t.Fatalf("the refusal does not say what was refused and why: %s", msg)
		}
		pushAccepted(t, small(3), 3)
	})

	t.Run("a tree past the depth limit is refused on either side", func(t *testing.T) {
		for _, side := range []string{"left", "right"} {
			ok := small(1)
			ok.WorkspaceTrees[1].Root = bspChain(maxBSPDepth, side == "left")
			pushAccepted(t, ok, 1)

			deep := small(1)
			deep.WorkspaceTrees[1].Root = bspChain(maxBSPDepth+1, side == "left")
			a.send(t, MsgUpdateState, deep)
			if msg := a.awaitError(t); !strings.Contains(msg, "deeper than") {
				t.Fatalf("a %s tree %d deep was not refused for its depth: %s", side, maxBSPDepth+1, msg)
			}
		}
		pushAccepted(t, small(2), 2)
	})

	t.Run("a tree with too many nodes is refused", func(t *testing.T) {
		wide := small(1)
		// maxBSPNodes is 2n-1 for n panes: the tree at the limit is taken, and
		// one more pane (two more nodes) is refused.
		wide.WorkspaceTrees[1].Root = bspBalanced((maxBSPNodes + 1) / 2)
		pushAccepted(t, wide, 1)
		wide.WorkspaceTrees[1].Root = bspBalanced((maxBSPNodes+1)/2 + 1)
		a.send(t, MsgUpdateState, wide)
		if msg := a.awaitError(t); !strings.Contains(msg, "nodes") {
			t.Fatalf("a tree of %d nodes was not refused: %s", maxBSPNodes+2, msg)
		}
		pushAccepted(t, small(2), 2)
	})

	t.Run("command result data is bounded", func(t *testing.T) {
		a.sendRaw(t, MsgCommandResult, make([]byte, maxCommandResultBytes+1))
		if msg := a.awaitError(t); !strings.Contains(msg, "CommandResult") || !strings.Contains(msg, "byte limit") {
			t.Fatalf("an oversized command result was not refused for its size: %s", msg)
		}
		nested := map[string]any{"leaf": 1}
		for range maxResultDataDepth {
			nested = map[string]any{"k": nested}
		}
		a.send(t, MsgCommandResult, &CommandResultPayload{RequestID: "r", Success: true, Data: nested})
		if msg := a.awaitError(t); !strings.Contains(msg, "nests deeper") {
			t.Fatalf("result data %d deep was not refused: %s", maxResultDataDepth+1, msg)
		}
		// The shape a real command returns passes.
		a.send(t, MsgCommandResult, &CommandResultPayload{RequestID: "r2", Success: true, Data: map[string]any{
			"windows": []map[string]any{{"id": "w", "workspaces": []int{1, 2}}},
		}})
		a.noError(t)
	})

	t.Run("the other client is still served both ways", func(t *testing.T) {
		b.send(t, MsgUpdateState, small(4))
		msg := a.await(t, MsgStateSync)
		var sync StateSyncPayload
		if err := msg.ParsePayload(&sync); err != nil || sync.State == nil || len(sync.State.Windows) != 4 {
			t.Fatalf("A did not hear B's push after the refusals: %v", err)
		}
	})
}

// bspChain is a tree depth levels deep, every split hanging off one side.
func bspChain(depth int, left bool) *SerializedBSPNode {
	n := &SerializedBSPNode{WindowID: 1}
	for i := range depth {
		leaf := &SerializedBSPNode{WindowID: i + 2}
		if left {
			n = &SerializedBSPNode{SplitType: 1, SplitRatio: 0.5, Left: n, Right: leaf}
		} else {
			n = &SerializedBSPNode{SplitType: 1, SplitRatio: 0.5, Left: leaf, Right: n}
		}
	}
	return n
}

// bspBalanced is a balanced tree of leaves panes, 2*leaves-1 nodes, built
// without recursion.
func bspBalanced(leaves int) *SerializedBSPNode {
	level := make([]*SerializedBSPNode, leaves)
	for i := range level {
		level[i] = &SerializedBSPNode{WindowID: i + 1}
	}
	for len(level) > 1 {
		var next []*SerializedBSPNode
		for i := 0; i+1 < len(level); i += 2 {
			next = append(next, &SerializedBSPNode{SplitType: 1, SplitRatio: 0.5, Left: level[i], Right: level[i+1]})
		}
		if len(level)%2 == 1 {
			next = append(next, level[len(level)-1])
		}
		level = next
	}
	return level[0]
}

// boundsClient is a raw binary client attached to one session.
type boundsClient struct {
	conn net.Conn
}

func dialBoundsClient(t *testing.T, socketPath, session string) *boundsClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &boundsClient{conn: conn}
	c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion})
	c.await(t, MsgWelcome)
	c.send(t, MsgAttach, &AttachPayload{SessionName: session, CreateNew: true, Width: 200, Height: 60})
	c.await(t, MsgAttached)
	return c
}

func (c *boundsClient) send(t *testing.T, typ MessageType, payload any) {
	t.Helper()
	msg, err := NewMessage(typ, payload)
	if err != nil {
		t.Fatalf("encode %s: %v", MessageTypeName(typ), err)
	}
	c.sendRaw(t, typ, msg.Payload)
}

// sendRaw writes a frame with this payload as it stands, so a test can send
// one of an exact size.
func (c *boundsClient) sendRaw(t *testing.T, typ MessageType, payload []byte) {
	t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second * testDeadlineScale))
	if err := WriteMessage(c.conn, &Message{Type: typ, Payload: payload}); err != nil {
		t.Fatalf("send %s: %v", MessageTypeName(typ), err)
	}
}

// await reads until a message of this type arrives, skipping the rest (size
// announcements, client joins), and fails on an error reply.
func (c *boundsClient) await(t *testing.T, typ MessageType) *Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second * testDeadlineScale)
	for {
		_ = c.conn.SetReadDeadline(deadline)
		msg, err := ReadMessage(c.conn)
		if err != nil {
			t.Fatalf("waiting for %s: %v", MessageTypeName(typ), err)
		}
		if msg.Type == typ {
			return msg
		}
		if msg.Type == MsgError && typ != MsgError {
			var e ErrorPayload
			_ = msg.ParsePayload(&e)
			t.Fatalf("waiting for %s, the daemon answered with an error: %s", MessageTypeName(typ), e.Message)
		}
	}
}

// awaitError reads until an error reply arrives and returns its message.
func (c *boundsClient) awaitError(t *testing.T) string {
	t.Helper()
	msg := c.await(t, MsgError)
	var e ErrorPayload
	if err := msg.ParsePayload(&e); err != nil {
		t.Fatalf("parse error reply: %v", err)
	}
	return e.Message
}

// noError fails if an error reply is waiting or arrives within a short
// window. A refusal is the daemon's first answer to a push, so a push with no
// error by then was taken.
func (c *boundsClient) noError(t *testing.T) {
	t.Helper()
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		var lenBuf [4]byte
		if _, err := c.conn.Read(lenBuf[:1]); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return
			}
			t.Fatalf("reading after a push: %v", err)
		}
		// A byte arrived: read the rest of the frame it starts.
		_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(c.conn, lenBuf[1:]); err != nil {
			t.Fatalf("reading after a push: %v", err)
		}
		msg, err := readMessageBody(c.conn, binary.BigEndian.Uint32(lenBuf[:]), nil)
		if err != nil {
			t.Fatalf("reading after a push: %v", err)
		}
		if msg.Type == MsgError {
			var e ErrorPayload
			_ = msg.ParsePayload(&e)
			t.Fatalf("the daemon refused a push it should take: %s", e.Message)
		}
	}
}

// largeRealisticState is the largest state update a client can make for a
// session of workspaces with panes each: every field BuildSessionState in
// internal/app sets, at its maximum. The title and the user-given name are
// clamped display text at their full 256 bytes; ids are the 36 byte UUIDs
// panes carry; each workspace is tiled as a chain, the deepest layout its
// panes can make (panes-1 levels), and holds a scrolling column per pane as
// well, which a client only sends for one layout or the other. The fields a
// client does not send (the directory, the agent state, the command line)
// are the daemon's and are merged in on its side.
func largeRealisticState(workspaces, panes int) *SessionState {
	text := ClampDisplayText(strings.Repeat("é", maxDisplayTextBytes))
	uuid := func(n int) string { return fmt.Sprintf("%08d-0000-4000-8000-%012d", n, n) }
	st := &SessionState{
		Name:             "bounds",
		CurrentWorkspace: workspaces,
		MasterRatio:      0.618,
		AutoTiling:       true,
		Width:            1 << 16,
		Height:           1 << 16,
		BaseVersion:      1 << 30,
		PaneReportBg:     "#1e1e2eff",
		PaneReportFg:     "#cdd6f4ff",
		WorkspaceFocus:   map[int]string{},
		WorkspaceTrees:   map[int]*SerializedBSPTree{},
		WindowToBSPID:    map[string]int{},
		NextBSPWindowID:  1 << 30,
		TilingScheme:     3,
		LayoutMode:       "scrolling",
		NumWorkspaces:    workspaces,
		SidebarWidth:     1 << 10,
		SidebarCollapsed: true,
		PaneGeometry:     &PaneGeometryState{SharedBorders: true, PaneGap: 1 << 10, ScrollColumnWidth: 1 << 10},
		ScrollStrip:      &ScrollStripState{ViewportX: 1 << 20},

		WorkspaceMasterRatio:   map[int]float64{},
		WorkspaceStackRatio:    map[int]float64{},
		WorkspaceHasCustom:     map[int]bool{},
		WorkspaceScrollColumns: map[int][]SerializedScrollColumn{},
	}
	id := 0
	for ws := 1; ws <= workspaces; ws++ {
		var root *SerializedBSPNode
		for p := range panes {
			id++
			w := WindowState{
				ID: uuid(id), PTYID: uuid(id + 1<<20), Title: text, CustomName: text,
				X: 1 << 16, Y: 1 << 16, Width: 1 << 16, Height: 1 << 16, Z: 1 << 16, Workspace: ws,
				Minimized: true, PreMinimizeX: 1 << 16, PreMinimizeY: 1 << 16, PreMinimizeW: 1 << 16, PreMinimizeH: 1 << 16,
				IsAltScreen: true, IsFloating: true, Zoomed: true,
				PreZoomX: 1 << 16, PreZoomY: 1 << 16, PreZoomW: 1 << 16, PreZoomH: 1 << 16,
				Popup: true, PopupWidth: "100%", PopupHeight: "100%",
			}
			st.Windows = append(st.Windows, w)
			st.WindowToBSPID[w.ID] = id
			st.WorkspaceScrollColumns[ws] = append(st.WorkspaceScrollColumns[ws],
				SerializedScrollColumn{Windows: []string{w.ID}, Proportion: 0.5, FixedWidth: 1 << 10, Active: 1})
			leaf := &SerializedBSPNode{WindowID: id}
			if root == nil {
				root = leaf
				continue
			}
			root = &SerializedBSPNode{SplitType: 1 + p%2, SplitRatio: 0.618, Left: root, Right: leaf}
		}
		st.WorkspaceTrees[ws] = &SerializedBSPTree{Root: root, AutoScheme: 3, DefaultRatio: 0.618}
		st.WorkspaceFocus[ws] = uuid(id)
		st.WorkspaceMasterRatio[ws] = 0.618
		st.WorkspaceStackRatio[ws] = 0.618
		st.WorkspaceHasCustom[ws] = true
	}
	st.FocusedWindowID = uuid(id)
	return st
}
