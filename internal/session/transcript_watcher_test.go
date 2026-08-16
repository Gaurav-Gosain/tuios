package session

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// The real watcher, against the real kernel. The fake one in
// agent_transcript_test.go covers the join logic; this covers the assumption the
// whole no-polling design rests on, which is that a directory watch reports a
// write to a file inside it and names the file.
func TestWatcherReportsAWriteToAFileInAWatchedDirectory(t *testing.T) {
	w, err := NewTranscriptWatcher()
	if err != nil {
		t.Skipf("no filesystem notification here: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var fired atomic.Int64
	if err := w.Watch(path, func() { fired.Add(1) }); err != nil {
		t.Fatalf("watch: %v", err)
	}
	appendLine(t, path, "{}\n")
	waitFor(t, "the watcher to report the append", func() bool { return fired.Load() > 0 })
}

// One watch per directory, not per file. Forty panes across twelve projects is
// twelve watches, which is what makes the cost at scale a non-issue.
func TestWatchesAreRefcountedPerDirectory(t *testing.T) {
	w, err := NewTranscriptWatcher()
	if err != nil {
		t.Skipf("no filesystem notification here: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a.jsonl", "b.jsonl", "c.jsonl"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := w.Watch(p, func() {}); err != nil {
			t.Fatalf("watch %s: %v", name, err)
		}
		paths = append(paths, p)
	}
	w.mu.Lock()
	dirs, refs := len(w.dirs), w.dirs[dir]
	w.mu.Unlock()
	if dirs != 1 || refs != 3 {
		t.Fatalf("three files in one directory took %d watches with refcount %d, want 1 and 3", dirs, refs)
	}

	// The last file out takes the watch with it, and no earlier one does.
	for i, p := range paths {
		w.Unwatch(p)
		w.mu.Lock()
		dirs = len(w.dirs)
		w.mu.Unlock()
		want := 1
		if i == len(paths)-1 {
			want = 0
		}
		if dirs != want {
			t.Fatalf("after unwatching %d of %d, %d directories watched, want %d",
				i+1, len(paths), dirs, want)
		}
	}
}

// The property the whole design is built to: a joined pane whose agent is doing
// nothing arms no timer, wakes no goroutine, and does no work. The watcher
// goroutine sits blocked in a channel receive, so an idle machine is measured by
// counting goroutines and finding the number does not grow.
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

	before := runtime.NumGoroutine()
	// A quarter of a second of a completely silent agent.
	time.Sleep(250 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines grew from %d to %d while nothing happened", before, after)
	}
	// And no read happened, so the file's offset never moved.
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

func TestWatchAfterCloseIsRefusedRatherThanPanicking(t *testing.T) {
	w, err := NewTranscriptWatcher()
	if err != nil {
		t.Skipf("no filesystem notification here: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Watch(filepath.Join(t.TempDir(), "a.jsonl"), func() {}); err == nil {
		t.Fatal("a closed watcher accepted a watch")
	}
	// Idempotent, because the daemon's shutdown path may run twice.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w.Unwatch("anything")
}

// A daemon that could not get a watcher is a working daemon.
func TestANilWatcherDegradesRatherThanCrashing(t *testing.T) {
	var w *TranscriptWatcher
	if err := w.Watch("/x", func() {}); err == nil {
		t.Fatal("a nil watcher accepted a watch")
	}
	w.Unwatch("/x")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
