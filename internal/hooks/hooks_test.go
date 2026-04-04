package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManager_RegisterAndFire(t *testing.T) {
	m := NewManager()

	// Register a hook that creates a temp file
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "hook_fired")

	m.Register(AfterNewWindow, "touch "+marker)

	// Fire the hook
	m.Fire(AfterNewWindow, Context{
		WindowID:  "test-123",
		Workspace: 1,
	})

	// Wait for async execution
	time.Sleep(200 * time.Millisecond)

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("hook did not fire: marker file not created")
	}
}

func TestManager_LoadFromConfig(t *testing.T) {
	m := NewManager()

	config := map[string]any{
		"after-new-window":   "echo new",
		"after-close-window": []any{"echo close1", "echo close2"},
	}

	m.LoadFromConfig(config)

	if !m.HasHooks() {
		t.Error("expected hooks to be registered")
	}

	m.mu.RLock()
	if len(m.hooks[AfterNewWindow]) != 1 {
		t.Errorf("expected 1 hook for new-window, got %d", len(m.hooks[AfterNewWindow]))
	}
	if len(m.hooks[AfterCloseWindow]) != 2 {
		t.Errorf("expected 2 hooks for close-window, got %d", len(m.hooks[AfterCloseWindow]))
	}
	m.mu.RUnlock()
}

func TestManager_Clear(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "echo test")
	m.Clear(AfterNewWindow)

	m.mu.RLock()
	if len(m.hooks[AfterNewWindow]) != 0 {
		t.Error("expected hooks to be cleared")
	}
	m.mu.RUnlock()
}

func TestParseEventName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"after-new-window", true},
		{"after-close-window", true},
		{"after-focus-change", true},
		{"invalid-event", false},
		{"", false},
	}

	for _, tt := range tests {
		_, ok := ParseEventName(tt.name)
		if ok != tt.valid {
			t.Errorf("ParseEventName(%q) = %v, want %v", tt.name, ok, tt.valid)
		}
	}
}

func TestContextEnvVars(t *testing.T) {
	m := NewManager()

	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "env.txt")

	m.Register(AfterFocusChange, "env | grep TUIOS_ > "+envFile)

	m.Fire(AfterFocusChange, Context{
		WindowID:   "win-abc",
		WindowName: "MyWindow",
		Workspace:  3,
		SessionID:  "sess-xyz",
	})

	time.Sleep(300 * time.Millisecond)

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}

	content := string(data)
	expected := []string{
		"TUIOS_EVENT=after-focus-change",
		"TUIOS_WINDOW_ID=win-abc",
		"TUIOS_WINDOW_NAME=MyWindow",
		"TUIOS_WORKSPACE=3",
		"TUIOS_SESSION_ID=sess-xyz",
	}

	for _, exp := range expected {
		if !contains(content, exp) {
			t.Errorf("expected env var %q in output:\n%s", exp, content)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
