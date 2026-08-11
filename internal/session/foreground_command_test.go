package session

import "testing"

// TestForegroundCommandLabels covers what a pane row is allowed to be named
// after: the program in the foreground, never the shell it would otherwise
// report on every idle pane at once.
func TestForegroundCommandLabels(t *testing.T) {
	cases := []struct {
		name    string
		comm    string
		argv    []string
		running bool
		shell   string
		want    string
	}{
		{"a real command", "nvim", []string{"/usr/bin/nvim", "main.go"}, true, "fish", "nvim"},
		{"the session's own shell", "fish", []string{"-fish"}, true, "fish", ""},
		{"a login shell that is not the session's", "bash", []string{"-bash"}, true, "fish", ""},
		{"argv beats a truncated comm", "some-very-long-b", []string{"/opt/some-very-long-binary"}, true, "fish", "some-very-long-binary"},
		{"comm when argv is empty", "btop", nil, true, "fish", "btop"},
		{"nothing running", "", nil, false, "fish", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := foregroundCommand(c.comm, c.argv, c.running, c.shell); got != c.want {
				t.Errorf("foregroundCommand(%q, %v, %v, %q) = %q, want %q",
					c.comm, c.argv, c.running, c.shell, got, c.want)
			}
		})
	}
}

// TestForegroundCommandSurvivesClientSync guards the field against the merge
// that wipes anything a client omits: no client ever sets it, so the daemon's
// value has to carry over, and has to clear when the command exits.
func TestForegroundCommandSurvivesClientSync(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{{ID: "w1", ForegroundCmd: "nvim"}}}
	incoming := &SessionState{Windows: []WindowState{{ID: "w1"}}}

	retainDaemonExclusive(incoming, canonical)
	if got := incoming.Windows[0].ForegroundCmd; got != "nvim" {
		t.Errorf("a client sync wiped the command: got %q", got)
	}

	canonical.Windows[0].ForegroundCmd = "" // nvim exited
	incoming = &SessionState{Windows: []WindowState{{ID: "w1"}}}
	retainDaemonExclusive(incoming, canonical)
	if got := incoming.Windows[0].ForegroundCmd; got != "" {
		t.Errorf("an exited command stuck at %q", got)
	}
}

// TestWindowSummaryWithholdsCommandFromNamedPane: the listing is what a foreign
// session's rows are built from, and there a custom name has to keep winning.
func TestWindowSummaryWithholdsCommandFromNamedPane(t *testing.T) {
	sess, plain := bareSessionWithWindow(t)
	named, err := sess.AddDaemonWindow("Window", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	_ = sess.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			st.Windows[i].ForegroundCmd = "nvim"
			if st.Windows[i].ID == named.ID {
				st.Windows[i].CustomName = "editor"
			}
		}
		return nil
	})

	byID := make(map[string]WindowSummary)
	for _, s := range sess.windowSummaries() {
		byID[s.ID] = s
	}
	if got := byID[plain].ForegroundCmd; got != "nvim" {
		t.Errorf("unnamed pane lost its command: %q", got)
	}
	if got := byID[named.ID].ForegroundCmd; got != "" {
		t.Errorf("named pane offered a command: %q", got)
	}
	if got := byID[named.ID].Title; got != "editor" {
		t.Errorf("named pane title = %q, want the custom name", got)
	}
}
