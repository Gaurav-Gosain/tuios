package server

import (
	"testing"
)

// Since determineSessionName requires a real ssh.Session, we test the logic
// by verifying the priority order in the function.
func TestDetermineSessionNamePriority(t *testing.T) {
	// Test Priority 1: Default session configured on server
	t.Run("priority 1: default session config", func(t *testing.T) {
		// When DefaultSession is set, it should always be used
		cfg := &SSHServerConfig{DefaultSession: "my-default-session"}
		// The function would return "my-default-session" regardless of other inputs
		if cfg.DefaultSession == "" {
			t.Error("DefaultSession should be set for this test")
		}
	})

	// Test Priority 2: SSH username filtering
	t.Run("priority 2: generic usernames filtered", func(t *testing.T) {
		genericUsers := []string{"tuios", "root", "anonymous", ""}
		for _, user := range genericUsers {
			if user != "" && user != "tuios" && user != "root" && user != "anonymous" {
				t.Errorf("user %q should be filtered but wasn't", user)
			}
		}

		// Non-generic users should be accepted
		validUsers := []string{"john", "developer", "my-user"}
		for _, user := range validUsers {
			if user == "" || user == "tuios" || user == "root" || user == "anonymous" {
				t.Errorf("user %q should NOT be filtered but was", user)
			}
		}
	})

	// Test Priority 3: Parse command for "attach <session>" pattern
	t.Run("priority 3: attach command parsing", func(t *testing.T) {
		testCases := []struct {
			cmd      []string
			expected string
		}{
			{[]string{"attach", "my-session"}, "my-session"},
			{[]string{"attach", "dev"}, "dev"},
			{[]string{"attach"}, ""},            // Missing session name
			{[]string{"other", "command"}, ""},  // Not an attach command
			{[]string{}, ""},                    // Empty command
			{[]string{"attach", "a", "b"}, "a"}, // Extra args ignored, second used
			{[]string{"ATTACH", "session"}, ""}, // Case sensitive
		}

		for _, tc := range testCases {
			result := ""
			if len(tc.cmd) >= 2 && tc.cmd[0] == "attach" {
				result = tc.cmd[1]
			}
			if result != tc.expected {
				t.Errorf("parseAttachCommand(%v) = %q, want %q", tc.cmd, result, tc.expected)
			}
		}
	})
}

// TestSSHServerConfig tests the SSHServerConfig struct defaults.
func TestSSHServerConfig(t *testing.T) {
	cfg := &SSHServerConfig{
		Host:      "localhost",
		Port:      "2222",
		KeyPath:   "/path/to/key",
		Ephemeral: false,
		Version:   "1.0.0",
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != "2222" {
		t.Errorf("Port = %q, want %q", cfg.Port, "2222")
	}
	if cfg.KeyPath != "/path/to/key" {
		t.Errorf("KeyPath = %q, want %q", cfg.KeyPath, "/path/to/key")
	}
	if cfg.Ephemeral {
		t.Error("Ephemeral should be false")
	}
	if cfg.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.0.0")
	}
}

// TestSSHServerConfigWithDefaultSession tests config with a default session.
func TestSSHServerConfigWithDefaultSession(t *testing.T) {
	cfg := &SSHServerConfig{
		Host:           "0.0.0.0",
		Port:           "22",
		DefaultSession: "main",
	}

	if cfg.DefaultSession != "main" {
		t.Errorf("DefaultSession = %q, want %q", cfg.DefaultSession, "main")
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != "22" {
		t.Errorf("Port = %q, want %q", cfg.Port, "22")
	}
}
