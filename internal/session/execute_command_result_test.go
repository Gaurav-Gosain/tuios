package session

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestARoutedCommandsResultReachesTheRequester pins the daemon's bookkeeping
// for a command the CLI sends and an attached client runs. The daemon forwards
// the command to the client and remembers who asked, and the client's result
// is matched against that record on its way back. The record has to exist
// before the command goes out: the client can answer in the time it takes the
// daemon to get from the write to the map, and a result with no record is
// dropped, which leaves the CLI waiting out its read deadline for an answer
// that was given at once.
//
// NEGATIVE CONTROL: with handleExecuteCommand recording the request after the
// send, this fails when the daemon goroutine loses the CPU between the two,
// which a busy machine arranges: pinned to one core beside a spinning process
// it failed within the first few rounds.
func TestARoutedCommandsResultReachesTheRequester(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "routed")

	// The attached client answers the moment the command lands, the way a
	// ToggleTiling is answered once the retile has run.
	tui := attachTestClient(t, "routed")
	tui.OnRemoteCommand(func(payload *RemoteCommandPayload) error {
		return tui.SendCommandResult(payload.RequestID, true, "ran "+payload.TapeCommand)
	})

	for round := range 200 {
		cli := NewClient(&ClientConfig{Version: "test"})
		if err := cli.Connect(); err != nil {
			t.Fatalf("round %d: connect: %v", round, err)
		}
		requestID := uuid.New().String()
		msg, err := NewMessage(MsgExecuteCommand, &ExecuteCommandPayload{
			SessionName: "routed",
			CommandType: "ToggleTiling",
			RequestID:   requestID,
		})
		if err != nil {
			t.Fatalf("round %d: build message: %v", round, err)
		}
		if err := cli.send(msg); err != nil {
			t.Fatalf("round %d: send: %v", round, err)
		}
		// The CLI's own deadline is thirty seconds. A result the client gave
		// at once is on the wire within milliseconds; five seconds is the
		// budget for the machine, not for the daemon.
		_ = cli.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, _, err := ReadMessageWithCodec(cli.conn)
		_ = cli.Close()
		if err != nil {
			t.Fatalf("round %d: the client answered the command and the requester never heard: %v", round, err)
		}
		if resp.Type != MsgCommandResult {
			t.Fatalf("round %d: reply type %d, want a command result", round, resp.Type)
		}
		var result CommandResultPayload
		if err := resp.ParsePayloadWithCodec(&result, cli.GetCodec()); err != nil {
			t.Fatalf("round %d: parse result: %v", round, err)
		}
		if result.RequestID != requestID || !result.Success {
			t.Fatalf("round %d: got %+v for request %s", round, result, requestID)
		}
	}
}
