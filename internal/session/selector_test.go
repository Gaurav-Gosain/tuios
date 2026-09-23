package session

import (
	"strings"
	"testing"
)

func TestParseSelectorRefusesWhatIsNotASelector(t *testing.T) {
	for _, tc := range []struct {
		text, want string
	}{
		{"", "empty"},
		{"codex", "not key:value"},
		{"colour:red", "not a selector key"},
		{"state:sleeping", "not an agent state"},
		{"needs:me", "needs takes one value"},
		{"harness:", "has no value"},
		{"session:[", "not a valid glob"},
		{strings.Repeat("state:idle ", 17), "at most 16 terms"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			_, err := ParseSelector(tc.text, "")
			if err == nil {
				t.Fatalf("ParseSelector(%q) = nil error, want one saying %q", tc.text, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ParseSelector(%q) = %q, want it to say %q", tc.text, err, tc.want)
			}
		})
	}
}

func TestSelectorMatch(t *testing.T) {
	codex := SelectorTarget{
		Host: "local", Session: "api-fan-retry-2", Name: "reviewer", State: "idle",
		Harness: "codex", Cwd: "/home/me/src/api", Group: "fan/retry",
	}
	blocked := SelectorTarget{
		Host: "build", Session: "web", Name: "claude", State: "needs_input",
		Harness: "claude-code", Cwd: "/srv/web", NeedsYou: true,
	}
	for _, tc := range []struct {
		sel         string
		codex, blkd bool
	}{
		{"harness:codex", true, false},
		{"harness:CODEX", true, false},
		{"harness:codex,claude-code", true, true},
		{"harness:claude-*", false, true},
		{"harness:claude", false, true},
		{"harness:claude-c", false, false},
		{"state:idle,done", true, false},
		{"needs:you", false, true},
		{"session:api-fan-*", true, false},
		{"group:fan/*", true, false},
		// A glob does not cross a slash, and a target with no group never
		// matches a group term, not even one that matches everything.
		{"group:*", false, false},
		{"group:*/*", true, false},
		{"host:local", true, false},
		{"host:build", false, true},
		{"name:rev*", true, false},
		{"cwd:~/src", true, false},
		{"cwd:~/src/api", true, false},
		// A prefix is on a path boundary: /home/me/src/a is not /home/me/src/api.
		{"cwd:~/src/a", false, false},
		{"cwd:/", true, true},
		// Terms are AND.
		{"harness:codex state:needs_input", false, false},
		{"harness:codex session:api-*", true, false},
	} {
		t.Run(tc.sel, func(t *testing.T) {
			sel, err := ParseSelector(tc.sel, "/home/me")
			if err != nil {
				t.Fatalf("ParseSelector: %v", err)
			}
			if got := sel.Match(codex); got != tc.codex {
				t.Errorf("match the codex pane = %v, want %v", got, tc.codex)
			}
			if got := sel.Match(blocked); got != tc.blkd {
				t.Errorf("match the blocked pane = %v, want %v", got, tc.blkd)
			}
		})
	}
}

func TestSelectorLocalHostIsEmptyOrLocal(t *testing.T) {
	sel, err := ParseSelector("host:local", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sel.Match(SelectorTarget{Session: "s"}) {
		t.Error("a target with no host is this machine, and host:local did not match it")
	}
}

func TestSelectionTokenIgnoresOrderAndSeesEveryPane(t *testing.T) {
	a := SelectionToken([]string{"local/1", "local/2"})
	if b := SelectionToken([]string{"local/2", "local/1"}); a != b {
		t.Errorf("the token depends on order: %s and %s", a, b)
	}
	if c := SelectionToken([]string{"local/1"}); a == c {
		t.Error("dropping a pane left the token the same")
	}
	if c := SelectionToken([]string{"local/1", "local/2", "local/3"}); a == c {
		t.Error("adding a pane left the token the same")
	}
}
