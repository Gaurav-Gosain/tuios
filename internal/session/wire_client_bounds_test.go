//go:build !windows

package session

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// The client half of the wire bounds. A client reads daemon frames of up to
// 16 MB of any type, and the daemon may be another machine's, so a hostile
// one could nest a payload deep enough to overflow the client's stack during
// gob's decode, which is fatal and which no check after the decode can stop.
// checkGobNesting (wire_gobscan.go) refuses such a payload on its raw bytes
// first. This test puts a hostile daemon on the socket and drives the real
// clients against it: the TUI client through attach and its read loop, and
// the control client through a command result. No payload here can hurt a
// client without the bound: the deepest nests 1025 levels and the largest
// type chain is 1024 definitions.
//
// The ways this could fail, written down before the scan:
//
//  1. A real payload is refused because the scan misreads gob: interface
//     values with their type definitions inline, nested interfaces, nil
//     interfaces, singletons, maps, and the largest real state. Every decode
//     in the package goes through the scan, so the rest of the suite covers
//     the real payloads; here the largest realistic state must still attach.
//  2. The depth count is off by one: a payload nesting exactly maxGobNesting
//     levels is decoded, one level more is refused, for a layout tree (struct
//     through pointer) and for command data (map through interface). The
//     levels are counted here independently of the scan.
//  3. Deep nesting through type definitions gets past a value depth check: a
//     chain of type definitions in a field the client does not have, with no
//     value at all, is refused past maxGobTypes and decoded at it.
//  4. The refusal happens after gob decodes, or is indistinguishable from a
//     later check: the attach error must name the nesting bound, not the
//     layout validator that runs after the decode.
//  5. A refused frame kills the read loop or leaves the stream out of step:
//     the frame is read whole before the decode, so the next good state sync
//     must reach the handler, the hostile ones must not, and the client must
//     stay connected.
//  6. The scan itself recurses or spins: it walks with an explicit stack and
//     refuses a count the input cannot back; the fuzz target over
//     decodePayload runs it on mutated bodies.
func TestClientBoundsWhatDaemonsSend(t *testing.T) {
	h := startHostileDaemon(t)

	// Levels as the scan counts them, the payload being level one: for a
	// state carried at payload.State, the state is 2, WorkspaceTrees 3, the
	// tree 4, its root 5, and a node k splits below the root is 5+k. bspChain
	// makes a tree whose deepest node is depth splits below the root.
	const treeLevelsAboveChain = 5
	atLimit := maxGobNesting - treeLevelsAboveChain

	attach := func(t *testing.T, reply any) (*SessionState, error) {
		t.Helper()
		h.serve(t, func(conn net.Conn) {
			if _, err := ReadMessage(conn); err != nil {
				return
			}
			h.write(t, conn, MsgAttached, reply)
		})
		c := NewTUIClient()
		if err := c.Connect("test", 80, 24); err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer func() { _ = c.Close() }()
		return c.AttachSession("hostile", false, 80, 24)
	}
	stateWithChain := func(depth int) *SessionState {
		st := largeRealisticState(1, 1)
		st.WorkspaceTrees[1].Root = bspChain(depth, true)
		return st
	}

	t.Run("the largest real state attaches", func(t *testing.T) {
		st, err := attach(t, &AttachedPayload{SessionName: "hostile", State: largeRealisticState(9, 41)})
		if err != nil || st == nil || len(st.Windows) != 9*41 {
			t.Fatalf("a real state was not decoded: %v", err)
		}
	})

	t.Run("a layout tree past the bound is refused before decode", func(t *testing.T) {
		// At the bound gob decodes it, and the layout validator refuses the
		// tree for being deeper than any real layout.
		_, err := attach(t, &AttachedPayload{SessionName: "hostile", State: stateWithChain(atLimit)})
		if err == nil || !strings.Contains(err.Error(), "layout tree is deeper") {
			t.Fatalf("a state nesting exactly %d levels was not decoded and handed to the layout check: %v", maxGobNesting, err)
		}
		_, err = attach(t, &AttachedPayload{SessionName: "hostile", State: stateWithChain(atLimit + 1)})
		if err == nil || !strings.Contains(err.Error(), errGobNested.Error()) {
			t.Fatalf("a state nesting %d levels was not refused before decode: %v", maxGobNesting+1, err)
		}
	})

	t.Run("a chain of type definitions past the bound is refused", func(t *testing.T) {
		// The chain is the payload's only other definitions: one for the
		// payload's own struct and one per slice level.
		st, err := attach(t, typeChainPayload(maxGobTypes-1))
		if err != nil || st != nil {
			t.Fatalf("a payload defining exactly %d types was not decoded: %v", maxGobTypes, err)
		}
		_, err = attach(t, typeChainPayload(maxGobTypes))
		if err == nil || !strings.Contains(err.Error(), errGobTypes.Error()) {
			t.Fatalf("a payload defining %d types was not refused: %v", maxGobTypes+1, err)
		}
	})

	t.Run("the read loop drops a hostile sync and goes on", func(t *testing.T) {
		release := make(chan struct{})
		h.serve(t, func(conn net.Conn) {
			if _, err := ReadMessage(conn); err != nil {
				return
			}
			h.write(t, conn, MsgAttached, &AttachedPayload{SessionName: "hostile", State: largeRealisticState(1, 2)})
			<-release
			h.write(t, conn, MsgStateSync, &StateSyncPayload{TriggerType: "deep", State: stateWithChain(atLimit + 1)})
			h.write(t, conn, MsgStateSync, typeChainSync(maxGobTypes))
			h.write(t, conn, MsgStateSync, &StateSyncPayload{TriggerType: "good", State: largeRealisticState(1, 3)})
			// Held open until the client is done, so a disconnect the test
			// sees is the client's own doing.
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			_, _ = ReadMessage(conn)
		})
		c := NewTUIClient()
		if err := c.Connect("test", 80, 24); err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer func() { _ = c.Close() }()
		if _, err := c.AttachSession("hostile", false, 80, 24); err != nil {
			t.Fatalf("attach: %v", err)
		}

		var mu sync.Mutex
		var heard []string
		good := make(chan int, 1)
		c.OnStateSync(func(state *SessionState, trigger, _ string) {
			mu.Lock()
			heard = append(heard, trigger)
			mu.Unlock()
			if trigger == "good" && state != nil {
				good <- len(state.Windows)
			}
		})
		disconnected := make(chan error, 1)
		c.OnDisconnect(func(err error) { disconnected <- err })
		c.StartReadLoop()
		close(release)

		select {
		case n := <-good:
			if n != 3 {
				t.Fatalf("the good sync arrived with %d windows, want 3", n)
			}
		case err := <-disconnected:
			t.Fatalf("the client disconnected on a hostile sync: %v", err)
		case <-time.After(10 * time.Second * testDeadlineScale):
			t.Fatal("the good sync after the hostile ones never reached the handler")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(heard) != 1 {
			t.Fatalf("the handler heard %v, want only the good sync", heard)
		}
	})

	t.Run("command result data past the bound is refused", func(t *testing.T) {
		// Levels: the payload 1, Data 2, and each wrapping map adds an
		// interface and a map. After k wraps the innermost map is 2+2k and
		// its value's interface 3+2k; a []int there is 4+2k, a map with a
		// value 5+2k. k = 510 puts them at 1024 and 1025.
		const wraps = (maxGobNesting - 4) / 2
		nest := func(leaf any) map[string]any {
			m := map[string]any{"leaf": leaf}
			for range wraps {
				m = map[string]any{"k": m}
			}
			return m
		}
		result := func(t *testing.T, data map[string]any) error {
			t.Helper()
			h.serve(t, func(conn net.Conn) {
				if _, err := ReadMessage(conn); err != nil {
					return
				}
				h.write(t, conn, MsgCommandResult, &CommandResultPayload{RequestID: "r", Success: true, Data: data})
			})
			c := NewClient(&ClientConfig{Version: "test"})
			if err := c.Connect(); err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer func() { _ = c.Close() }()
			msg, err := NewMessage(MsgExecuteCommand, &ExecuteCommandPayload{CommandType: "ListWindows", RequestID: "r"})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.SendControlMessage(msg)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			var got CommandResultPayload
			return resp.ParsePayload(&got)
		}
		if err := result(t, nest([]int{1})); err != nil {
			t.Fatalf("result data nesting exactly %d levels was refused: %v", maxGobNesting, err)
		}
		err := result(t, nest(map[string]any{"x": 1}))
		if err == nil || !strings.Contains(err.Error(), errGobNested.Error()) {
			t.Fatalf("result data nesting %d levels was not refused: %v", maxGobNesting+1, err)
		}
	})
}

// typeChainPayload is an attach reply that carries, beside its session name,
// a field no client has: a slice of a slice of ... of int, n slices deep and
// nil. gob sends a definition for the reply's struct and one for each slice
// level, n+1 in all, and a receiver compiles a decoder for the chain by
// recursion even though it skips the field.
func typeChainPayload(n int) any {
	v := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "SessionName", Type: reflect.TypeFor[string]()},
		{Name: "Chain", Type: sliceChain(n)},
	}))
	v.Elem().Field(0).SetString("hostile")
	return v.Interface()
}

// typeChainSync is typeChainPayload as a state sync.
func typeChainSync(n int) any {
	v := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "TriggerType", Type: reflect.TypeFor[string]()},
		{Name: "Chain", Type: sliceChain(n)},
	}))
	v.Elem().Field(0).SetString("types")
	return v.Interface()
}

func sliceChain(n int) reflect.Type {
	typ := reflect.TypeFor[int]()
	for range n {
		typ = reflect.SliceOf(typ)
	}
	return typ
}

// hostileDaemon is a fake daemon on the test's socket that answers the hello
// like a real one and then sends what a test tells it to.
type hostileDaemon struct {
	ln net.Listener
}

func startHostileDaemon(t *testing.T) *hostileDaemon {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return &hostileDaemon{ln: ln}
}

// serve accepts the next connection, completes the handshake, and runs fn on
// it in the background.
func (h *hostileDaemon) serve(t *testing.T, fn func(conn net.Conn)) {
	t.Helper()
	go func() {
		conn, err := h.ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				t.Errorf("accept: %v", err)
			}
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := ReadMessage(conn); err != nil {
			t.Errorf("read hello: %v", err)
			return
		}
		h.write(t, conn, MsgWelcome, &WelcomePayload{Version: "test", Codec: wireCodecName, Protocol: ProtocolVersion})
		_ = conn.SetDeadline(time.Time{})
		fn(conn)
	}()
}

func (h *hostileDaemon) write(t *testing.T, conn net.Conn, typ MessageType, payload any) {
	msg, err := NewMessage(typ, payload)
	if err != nil {
		t.Errorf("encode %s: %v", MessageTypeName(typ), err)
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := WriteMessage(conn, msg); err != nil {
		t.Errorf("write %s: %v", MessageTypeName(typ), err)
	}
}
