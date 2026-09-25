package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestContextEnvVars checks the variables a hook command is given. User hook
// scripts read these names, so a renamed or dropped variable breaks them.
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
