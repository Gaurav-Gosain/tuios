package input

import (
	"testing"
	"unicode/utf8"
)

// TestTruncateForNotifKeepsUTF8Valid checks that the copied and pasted command
// text shown in a notification is cut on a rune boundary. It used to be cut by
// byte, so a command with multi-byte text near the limit put invalid UTF-8 into
// the notification.
func TestTruncateForNotifKeepsUTF8Valid(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"short ascii", "ls -la", 30, "ls -la"},
		{"long ascii", "abcdefghij", 8, "abcde..."},
		{"accent at the cut", "echo café café café", 9, "echo c..."},
		{"multi-byte fits by rune", "ééééé", 5, "ééééé"},
		{"multi-byte cut", "éééééééé", 6, "ééé..."},
		{"emoji cut", "\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", 4, "\U0001F600..."},
		{"too small for ellipsis", "éééé", 2, "éé"},
		{"zero", "abc", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForNotif(tt.in, tt.maxLen)
			if !utf8.ValidString(got) {
				t.Fatalf("truncateForNotif(%q, %d) = %q, which is not valid UTF-8", tt.in, tt.maxLen, got)
			}
			if got != tt.want {
				t.Errorf("truncateForNotif(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
			}
		})
	}
}
