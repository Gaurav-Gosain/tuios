package session

import "testing"

// BenchmarkAttentionOutputEvent is what the Inbox adds to the hottest path in
// the daemon: every chunk a pane writes is an output event through the same
// sink, and the queue has to ignore it.
func BenchmarkAttentionOutputEvent(b *testing.B) {
	a, _ := recordingAttention()
	ev := SessionEvent{Type: EventOutput, Window: "w", PTYID: "p", Bytes: 512}
	b.ReportAllocs()
	for b.Loop() {
		a.noteSessionEvent("work", ev)
	}
}

// BenchmarkAttentionRepeatedBlock is a pane reporting the same block again,
// which must cost a lookup and publish nothing.
func BenchmarkAttentionRepeatedBlock(b *testing.B) {
	a, _ := recordingAttention()
	ev := agentEvent("w1", "working", "needs_input", "approval", "approve Bash: go test ./...", 0, 0)
	a.noteSessionEvent("work", ev)
	b.ReportAllocs()
	for b.Loop() {
		a.noteSessionEvent("work", ev)
	}
}
