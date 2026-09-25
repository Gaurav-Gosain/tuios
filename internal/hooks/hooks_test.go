package hooks

import (
	"testing"
)

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
