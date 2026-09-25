package session

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// transcriptGoroutines counts goroutines currently running transcript
// machinery. This is the direct form of "an idle join wakes no goroutine": a
// regression back to a per-join poller sits in agent_transcript.go frames and
// is counted here, while goroutines owned by other tests in the same binary
// are not. The process-wide goroutine count this replaced measured the whole
// binary: any earlier test's leftover timer firing inside the sampling window
// grew the count and failed the test without anything here being wrong.
func transcriptGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	count := 0
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "agent_transcript.go") {
			count++
		}
	}
	return count
}

// The property the whole design is built to: a joined pane whose agent is doing
// nothing arms no timer, wakes no goroutine, and does no work. The watcher
// goroutine sits blocked in a channel receive, so an idle join is measured by
// finding no goroutine anywhere in the binary running transcript code.
func TestAnIdleJoinArmsNothing(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	fake := newFakeWatch()
	s.SetTranscriptWatcher(fake)

	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}

	// No debounce armed: nothing has changed since the join read.
	s.transcripts.mu.Lock()
	armed := s.transcripts.joins[win].debounce != nil
	s.transcripts.mu.Unlock()
	if armed {
		t.Fatal("a join with no file activity armed a timer")
	}

	// A quarter of a second of a completely silent agent.
	time.Sleep(250 * time.Millisecond)
	if n := transcriptGoroutines(); n != 0 {
		t.Fatalf("%d goroutines are running transcript code while nothing happened", n)
	}
	// And the quiet window armed nothing either.
	s.transcripts.mu.Lock()
	stillArmed := s.transcripts.joins[win].debounce != nil
	s.transcripts.mu.Unlock()
	if stillArmed {
		t.Fatal("a timer appeared on an idle join")
	}
}

// A turn appends several records, and each one is an event. They must collapse
// into one read, or a turn costs as many parses as it wrote records.
func TestABurstOfEventsCollapsesIntoOneRead(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	fake := newFakeWatch()
	s.SetTranscriptWatcher(fake)

	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}

	appendLine(t, path, assistantRec(t, "end_turn", "/p", "2.1.222"))
	for range 20 {
		fake.fire(path)
	}
	// Twenty events, one timer.
	s.transcripts.mu.Lock()
	j := s.transcripts.joins[win]
	armed := j.debounce != nil
	s.transcripts.mu.Unlock()
	if !armed {
		t.Fatal("a burst armed no read at all")
	}
	waitFor(t, "the single debounced read", func() bool {
		return s.GetState().Windows[0].AgentState == AgentStateDone
	})
	s.transcripts.mu.Lock()
	stillArmed := s.transcripts.joins[win].debounce != nil
	s.transcripts.mu.Unlock()
	if stillArmed {
		t.Fatal("the debounce did not disarm when it fired")
	}
}
