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
		&TerminalStatePayload{PTYID: "p", State: &TerminalState{Width: 4, Height: 2, CursorX: 3, PendingWrap: true}},
		&TerminalStatePayload{PTYID: "p", State: &TerminalState{
			Width: 4, Height: 2, IsAltScreen: true,
			PenProtected: true, Protected: []int{0, 1, 2}, MainProtected: []int{1, 0, 4},
			LastPrinted: "日", LastPrintedKnown: true,
			SavedCursor: &SavedCursorState{X: 3, Y: 1, PendingWrap: true, Origin: true, Protected: true,
				Pen: &StyleState{FgColor: "a1", LinkURL: "https://example.test"}, Charsets: []int{'0', 'B', 'B', 'A', 1, 2}},
			MainSavedCursor: &SavedCursorState{X: 1},
		}},
		&CommandResultPayload{Success: true, Data: map[string]any{"a": 1, "b": []string{"x"}}},
		&StateSyncPayload{State: &SessionState{Name: "s", WorkspaceTrees: map[int]*SerializedBSPTree{1: {Root: root}}}, TriggerType: "window"},
		&SessionState{Name: "s", WorkspaceTrees: map[int]*SerializedBSPTree{1: {Root: root}}},
		&SessionListPayload{Sessions: []SessionInfo{{Name: "a"}, {Name: "b"}}},
		&StateSyncPayload{State: &SessionState{Name: "s", Windows: []WindowState{{ID: "w", AgentState: AgentStateDone, AgentSubagents: 1,
			AgentSubagentList: []SubagentInfo{
				{ID: "ab34da07d2d63ee3c", Type: "general-purpose", Description: "Echo one in background", State: SubagentRunning, StartedAt: 1, Now: "Bash: sleep 3; echo one", Tools: 1},
				{ID: "aa84fb4f695c1ee02", Type: "general-purpose", State: SubagentDone, StartedAt: 1, EndedAt: 2, Last: "Bash: echo two", Tools: 1, Result: "done"},
			}}}}},
		&SessionListPayload{Sessions: []SessionInfo{{Name: "a", Windows: []WindowSummary{{ID: "w", Subagents: 1,
			SubagentList: []SubagentInfo{{ID: "a1", State: SubagentFailed, Result: "boom"}}}}}}},
	}
}

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

func trimDiff(s string) string {
	if len(s) > 4000 {
		return s[:4000] + fmt.Sprintf("\n... %d more bytes", len(s)-4000)
	}
	return s
}
