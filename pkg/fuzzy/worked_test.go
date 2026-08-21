package fuzzy

import "testing"

// TestWorkedExamplesPrintScores is documentation that runs: it prints the
// ranking with scores for the queries that decide whether the matcher agrees
// with a person. Run with -v to read it.
func TestWorkedExamplesPrintScores(t *testing.T) {
	cases := []struct {
		pattern    string
		candidates []string
	}{
		{"gc", []string{"gcc", "gnome-calculator", "git-credential-cache", "gnome-characters", "grub-check"}},
		{"ls", []string{"ls", "lsblk", "lsof", "lspci", "tools", "gnome-logs"}},
		{"py", []string{"python3", "python3-config", "pypy", "happy", "py"}},
		{"gnome", []string{"gnome-terminal", "gnome-calculator", "gimp-console"}},
	}
	for _, tc := range cases {
		t.Logf("query %q", tc.pattern)
		for _, h := range Filter(tc.pattern, tc.candidates) {
			t.Logf("  %5d  %-24s positions=%v", h.Score, h.Text, h.Positions)
		}
	}
}
