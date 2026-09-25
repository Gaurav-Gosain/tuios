package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
