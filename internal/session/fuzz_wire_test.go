package session

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Fuzzing the payload half of the wire protocol. FuzzReadMessageFraming covers
// the frame; this covers what is inside it, the gob body every handler decodes
// with ParsePayload. The bytes come from any client on the socket and, for the
// messages a link policy allows, from another machine.
//
// The ways this could fail, written down before the target:
//
//  1. The decoder panics on a hostile body. A panic in a handler takes the
//     daemon's connection goroutine down, and every session with it.
//  2. A small body makes the decoder allocate far more than it holds: a
//     declared length, a map count or a string size the bytes cannot back.
//  3. The decoder spins on a body that is short.
//  4. A payload that decodes cannot be encoded again. The daemon relays some
//     of what it decodes (a state update is rebroadcast as a state sync), so
//     a value it accepts and cannot send is a client that silently stops
//     hearing about its session.
//  5. A payload changes on a round trip: decode, encode, decode gives a
//     different value, so what a relaying daemon forwards is not what it was
//     sent.
//
// Not covered here, and reported rather than fuzzed: a SerializedBSPNode tree
// nested a few million deep decodes by recursion and overflows the goroutine
// stack, which is a fatal error the fuzzer cannot survive to report.

// wirePayloads are the bodies the daemon and its clients decode, one
// constructor each. The first byte of a fuzz input picks one.
var wirePayloads = []func() any{
	func() any { return new(HelloPayload) },
	func() any { return new(WelcomePayload) },
	func() any { return new(AttachPayload) },
	func() any { return new(AttachedPayload) },
	func() any { return new(NewPayload) },
	func() any { return new(SessionListPayload) },
	func() any { return new(KillPayload) },
	func() any { return new(ResurrectPayload) },
	func() any { return new(SessionEndedPayload) },
	func() any { return new(ResizePayload) },
	func() any { return new(ErrorPayload) },
	func() any { return new(CreatePTYPayload) },
	func() any { return new(PTYCreatedPayload) },
	func() any { return new(ClosePTYPayload) },
	func() any { return new(ResizePTYPayload) },
	func() any { return new(SubscribePTYPayload) },
	func() any { return new(PTYResizedPayload) },
	func() any { return new(AgentMailPayload) },
	func() any { return new(UnsubscribePTYPayload) },
	func() any { return new(GetTerminalStatePayload) },
	func() any { return new(TerminalStatePayload) },
	func() any { return new(ExecuteCommandPayload) },
	func() any { return new(CommandResultPayload) },
	func() any { return new(RemoteCommandPayload) },
	func() any { return new(GetLogsPayload) },
	func() any { return new(LogsDataPayload) },
	func() any { return new(StateSyncPayload) },
	func() any { return new(ClientJoinedPayload) },
	func() any { return new(ClientLeftPayload) },
	func() any { return new(SessionResizePayload) },
	func() any { return new(ReadDirPayload) },
	func() any { return new(DirListingPayload) },
	func() any { return new(HostsChangedPayload) },
	func() any { return new(SessionState) },
}

// wireSamples are populated values, encoded to seed the corpus with bodies the
// decoder actually accepts, so mutation starts inside gob's grammar.
func wireSamples() []any {
	root := &SerializedBSPNode{SplitType: 1, SplitRatio: 0.5,
		Left: &SerializedBSPNode{WindowID: 1}, Right: &SerializedBSPNode{WindowID: 2}}
	return []any{
		&HelloPayload{},
		&AttachPayload{SessionName: "work", Width: 80, Height: 24},
		&NewPayload{SessionName: "n"},
		&ErrorPayload{Code: 3, Message: "no"},
		&ResizePTYPayload{PTYID: "p", Width: 10, Height: 5},
		&TerminalStatePayload{},
		&CommandResultPayload{Success: true, Data: map[string]any{"a": 1, "b": []string{"x"}}},
		&StateSyncPayload{State: &SessionState{Name: "s", WorkspaceTrees: map[int]*SerializedBSPTree{1: {Root: root}}}, TriggerType: "window"},
		&SessionState{Name: "s", WorkspaceTrees: map[int]*SerializedBSPTree{1: {Root: root}}},
		&SessionListPayload{Sessions: []SessionInfo{{Name: "a"}, {Name: "b"}}},
	}
}

// wireCmp compares a value to its round trip under gob's own rules: an empty
// slice or map and a nil one are the same thing on the wire, and NaN is
// carried as NaN.
var wireCmp = []cmp.Option{
	cmpopts.EquateEmpty(),
	cmpopts.EquateNaNs(),
	cmp.Exporter(func(reflect.Type) bool { return true }),
}

func FuzzWirePayloadDecode(f *testing.F) {
	for i, s := range wireSamples() {
		b, err := encodePayload(s)
		if err != nil {
			f.Fatalf("encoding sample %d: %v", i, err)
		}
		// The picker byte for the sample's own type, so the seed decodes.
		for k, mk := range wirePayloads {
			if reflect.TypeOf(mk()) == reflect.TypeOf(s) {
				f.Add(append([]byte{byte(k)}, b...))
			}
		}
	}
	f.Add([]byte{0})
	f.Add([]byte{26, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 1 || len(data) > 1<<16 {
			return
		}
		mk := wirePayloads[int(data[0])%len(wirePayloads)]
		body := data[1:]

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		v := mk()
		done := make(chan error, 1)
		go func() { done <- decodePayload(body, v) }()
		var err error
		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("decoding %d bytes into %T did not return", len(body), v)
		}
		runtime.ReadMemStats(&after)
		// A decode may allocate what it builds and gob's own buffers. A
		// multiple of the input far past that is a length the bytes did not
		// back.
		const budget = 64 << 20
		if grew := after.TotalAlloc - before.TotalAlloc; grew > budget+64*uint64(len(body)) {
			t.Fatalf("decoding %d bytes into %T allocated %d bytes", len(body), v, grew)
		}
		if err != nil {
			return
		}

		enc, err := encodePayload(v)
		if err != nil {
			t.Fatalf("%T decoded from %d bytes and cannot be encoded again: %v", v, len(body), err)
		}
		back := mk()
		if err := decodePayload(enc, back); err != nil {
			t.Fatalf("%T re-encoded to %d bytes that do not decode: %v", v, len(enc), err)
		}
		if diff := cmp.Diff(v, back, wireCmp...); diff != "" {
			t.Fatalf("%T changed on a round trip (-decoded +relayed):\n%s", v, trimDiff(diff))
		}
	})
}

// trimDiff keeps a mismatch report readable when a payload is large.
func trimDiff(s string) string {
	if len(s) > 4000 {
		return s[:4000] + fmt.Sprintf("\n... %d more bytes", len(s)-4000)
	}
	return s
}
