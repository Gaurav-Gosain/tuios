package app

import "testing"

// TestCockpitLinkTarget checks absolute paths become escaped file URLs while
// unsupported addresses and terminal control characters remain blocked.
func TestCockpitLinkTarget(t *testing.T) {
	for _, tt := range []struct {
		target string
		want   string
	}{
		{"/home/user/file.go", "file:///home/user/file.go"},
		{"/tmp/a b#c?d%", "file:///tmp/a%20b%23c%3Fd%25"},
		{"https://example.com", "https://example.com"},
		{"mailto:user@example.com", "mailto:user@example.com"},
		{"relative/file.go", ""},
		{"/tmp/file\x1b", ""},
		{"/tmp/file\u009c", ""},
		{"https://example.com/a b", ""},
	} {
		t.Run(tt.target, func(t *testing.T) {
			if got := ckLinkTarget(tt.target); got != tt.want {
				t.Fatalf("ckLinkTarget(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}
