package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// The fixtures carry the record shape of a real Claude Code transcript with
// invented content. See internal/transcript for the shape they were checked
// against.

func recLine(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func assistantRec(t *testing.T, stop, cwd, version string) string {
	return recLine(t, map[string]any{
		"type": "assistant", "sessionId": "sess-1", "cwd": cwd, "version": version,
		"timestamp": "2026-08-16T10:00:00.000Z", "isSidechain": false,
		"message": map[string]any{
			"role": "assistant", "stop_reason": stop,
			"content": []any{map[string]any{"type": "text", "text": "invented"}},
		},
	})
}

func writeTranscript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeWatch stands in for filesystem notification so the join logic is testable
// without waiting on the kernel.
type fakeWatch struct {
	mu   sync.Mutex
	cbs  map[string]func()
	fail bool
}

func newFakeWatch() *fakeWatch { return &fakeWatch{cbs: map[string]func(){}} }

func (f *fakeWatch) Watch(path string, onChange func()) error {
	if f.fail {
		return errNoTranscriptWatcher
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cbs[path] = onChange
	return nil
}

func (f *fakeWatch) Unwatch(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cbs, path)
}

func (f *fakeWatch) fire(path string) {
	f.mu.Lock()
	cb := f.cbs[path]
	f.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (f *fakeWatch) watching(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.cbs[path]
	return ok
}

func TestJoinReadsTheTurnAndPublishesIt(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	w := newFakeWatch()
	s.SetTranscriptWatcher(w)

	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}
	if got := s.GetState().Windows[0].AgentState; got != AgentStateWorking {
		t.Fatalf("state = %q, want working", got)
	}
	if s.agentClaimFor(win).source != AgentSourceTranscript {
		t.Fatalf("claim source = %q", s.agentClaimFor(win).source)
	}

	// The turn ends and the file says so.
	appendLine(t, path, assistantRec(t, "end_turn", "/p", "2.1.222"))
	w.fire(path)
	waitFor(t, "the debounced read to publish done", func() bool {
		return s.GetState().Windows[0].AgentState == AgentStateDone
	})
}

// end_turn is the whole point: it is the same fact the Stop hook reports, with
// no hook installed.
func TestEndTurnIsDoneAndNothingElseIsAsserted(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	s.SetTranscriptWatcher(newFakeWatch())

	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "end_turn", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}
	if got := s.GetState().Windows[0].AgentState; got != AgentStateDone {
		t.Fatalf("state = %q, want done", got)
	}
}

// The source must never claim needs_input. That signature is an inference that
// was never validated against a live permission prompt, and it is the one state
// that raises an alert, so a wrong one is worse than a missing one.
func TestTranscriptNeverAssertsNeedsInputOrIdle(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	for _, state := range []AgentState{AgentStateNeedsInput, AgentStateIdle, AgentStateErrored} {
		if s.applyTranscriptTurn(win, "claude-code", 99) {
			t.Fatal("an unknown turn published something")
		}
		if got := s.GetState().Windows[0].AgentState; got == state {
			t.Fatalf("transcript published %q", state)
		}
	}
}

// A vanished transcript is an agent that is not coming back. The claim goes with
// the file, or the pane holds its last known state against every weaker tier for
// the rest of its life.
func TestAVanishedFileGivesTheClaimBack(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	w := newFakeWatch()
	s.SetTranscriptWatcher(w)

	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// One miss is a rotation, not a death, so the claim survives it.
	s.readAgentTranscript(win)
	if !s.hasTranscriptJoin(win) {
		t.Fatal("a single missed read ended the join")
	}
	for range transcriptMissingLimit {
		s.readAgentTranscript(win)
	}
	if s.hasTranscriptJoin(win) {
		t.Fatal("the join outlived its file")
	}
	if s.agentClaimFor(win).source == AgentSourceTranscript {
		t.Fatal("the claim outlived its file")
	}
	if w.watching(path) {
		t.Fatal("the watch outlived its file")
	}
	// The state is left where it was: it is still the best answer anyone has, it
	// is just no longer defended.
	if got := s.GetState().Windows[0].AgentState; got != AgentStateWorking {
		t.Fatalf("state = %q, want the last read left alone", got)
	}
	if _, applied, err := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateNeedsInput, Source: AgentSourceScreen,
	}); err != nil || !applied {
		t.Fatalf("screen after the file went: %v applied=%v", err, applied)
	}
}

// The harness naming its own file is not something a directory search can
// improve on.
func TestAnExactJoinOutranksASearchedOne(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	s.SetTranscriptWatcher(newFakeWatch())
	dir := t.TempDir()
	searched := writeTranscript(t, dir, "searched.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	exact := writeTranscript(t, dir, "exact.jsonl", assistantRec(t, "end_turn", "/p", "2.1.222"))

	if err := s.JoinAgentTranscript(win, "claude-code", searched, false); err != nil {
		t.Fatal(err)
	}
	if err := s.JoinAgentTranscript(win, "claude-code", exact, true); err != nil {
		t.Fatal(err)
	}
	if got := s.GetState().Windows[0].AgentState; got != AgentStateDone {
		t.Fatalf("state = %q, want the exact join's answer", got)
	}
	// And the searched one does not come back afterwards.
	if err := s.JoinAgentTranscript(win, "claude-code", searched, false); err != nil {
		t.Fatal(err)
	}
	if got := s.GetState().Windows[0].AgentState; got != AgentStateDone {
		t.Fatalf("a search displaced an exact join: state = %q", got)
	}
}

// When notification is unavailable the join still works, driven by the pane's
// own output, and nothing is watched.
func TestAnUnwatchableJoinFallsBackToOutput(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	w := newFakeWatch()
	w.fail = true
	s.SetTranscriptWatcher(w)

	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}
	if !s.transcriptFallbackDue(win) {
		t.Fatal("a join the watcher refused must be on the output fallback")
	}
	if got := s.GetState().Windows[0].AgentState; got != AgentStateWorking {
		t.Fatalf("state = %q: the join must still read once", got)
	}
	appendLine(t, path, assistantRec(t, "end_turn", "/p", "2.1.222"))
	s.readAgentTranscript(win)
	if got := s.GetState().Windows[0].AgentState; got != AgentStateDone {
		t.Fatalf("state = %q, want done from the output-driven read", got)
	}
}

// A watched join must not also read on output, or every machine with working
// notification would read each file twice.
func TestAWatchedJoinDoesNotReadOnOutput(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	s.SetTranscriptWatcher(newFakeWatch())
	dir := t.TempDir()
	path := writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/p", "2.1.222"))
	if err := s.JoinAgentTranscript(win, "claude-code", path, true); err != nil {
		t.Fatal(err)
	}
	if s.transcriptFallbackDue(win) {
		t.Fatal("a watched join asked for output-driven reads too")
	}
}

func TestSearchVerifiesAgainstThePane(t *testing.T) {
	tr := &harness.Transcript{
		Reader: harness.ReaderJSONL,
		Dir:    "{home}/none", Glob: "*.jsonl",
		Verify: []string{"cwd", "version"},
	}
	dir := t.TempDir()
	writeTranscript(t, dir, "mine.jsonl", assistantRec(t, "tool_use", "/proj", "2.1.222"))
	writeTranscript(t, dir, "other-version.jsonl", assistantRec(t, "tool_use", "/proj", "2.1.100"))
	writeTranscript(t, dir, "other-cwd.jsonl", assistantRec(t, "tool_use", "/elsewhere", "2.1.222"))

	got, ok := searchTranscriptIn(dir, tr, "/proj", "2.1.222")
	if !ok || filepath.Base(got) != "mine.jsonl" {
		t.Fatalf("search = %q ok=%v, want mine.jsonl", got, ok)
	}
	// A pane whose build cannot be read verifies nothing, so it joins nothing.
	if _, ok := searchTranscriptIn(dir, tr, "/proj", ""); ok {
		t.Fatal("joined without being able to check the version")
	}
}

// Two files that both verify means neither is used. A wrong join attributes one
// agent's state to another pane, which is a confident lie; no join is merely no
// state, and the pane behaves exactly as it did before this source existed.
func TestSearchRefusesWhenTwoCandidatesBothVerify(t *testing.T) {
	tr := &harness.Transcript{
		Reader: harness.ReaderJSONL,
		Dir:    "{home}/none", Glob: "*.jsonl",
		Verify: []string{"cwd", "version"},
	}
	dir := t.TempDir()
	writeTranscript(t, dir, "a.jsonl", assistantRec(t, "tool_use", "/proj", "2.1.222"))
	writeTranscript(t, dir, "b.jsonl", assistantRec(t, "end_turn", "/proj", "2.1.222"))
	// b is newer, and that is deliberately not a tie-break: which file was
	// written most recently is no evidence of which pane is looking at it.
	if got, ok := searchTranscriptIn(dir, tr, "/proj", "2.1.222"); ok {
		t.Fatalf("search picked %q instead of refusing", got)
	}
}

func TestDashPathJoinsThroughTheManifest(t *testing.T) {
	// The bundled manifest points at the real Claude Code layout, so this checks
	// the directory a pane in /x/y would be searched in.
	r, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	tr := r.TranscriptFor("claude-code")
	if tr == nil {
		t.Fatal("no transcript block")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	want := filepath.Join(home, ".claude", "projects", "-x-y")
	if got := tr.ExpandDir("/x/y"); got != want {
		t.Fatalf("ExpandDir = %q, want %q", got, want)
	}
}

func appendLine(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}
