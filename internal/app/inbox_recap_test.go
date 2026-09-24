package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/charmbracelet/x/ansi"
)

// These tests pin the away recap: the return toast, gated on the time away
// and a finished turn, and the Inbox's Finished detail.

// recapFake answers agent-activity with a fixed recap and records the calls.
type recapFake struct {
	recap map[string]any
	calls []map[string]any
	err   error
}

func (r *recapFake) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	if verb != "agent-activity" {
		return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
	}
	r.calls = append(r.calls, params)
	if r.err != nil {
		return nil, r.err
	}
	return json.Marshal(map[string]any{"type": "agent_activity", "entries": []any{}, "recap": r.recap})
}

// sampleRecap is the plan's example: three turns, six files, eleven commands,
// a passing go test, and the agent back at its prompt.
func sampleRecap(at int64) map[string]any {
	return map[string]any{
		"since": at, "turns": 3,
		"files":       []string{"api/retry.go", "api/retry_test.go", "api/backoff.go"},
		"files_total": 6, "commands": 11,
		"tests":     map[string]any{"cmdline": "go test ./...", "ok": true, "at": time.Now().Add(-2 * time.Minute).UnixNano()},
		"last_said": "Added retry with backoff and tests.",
		"state":     "idle",
	}
}

// forgetSidebarState removes the sidebar.json a test's focus changes write,
// so the seen marks it left do not reach the next test in the binary.
func forgetSidebarState(t *testing.T) {
	t.Helper()
	path := filepath.Join(sidebarStateDir(), sidebarStateFileName)
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
}

// recapOS is a client whose second pane is an agent that finished turns
// while the person was on the first.
func recapOS(t *testing.T, away time.Duration, turns uint64) (*OS, *recapFake) {
	t.Helper()
	forgetSidebarState(t)
	m := inboxOS(t, zeroSettle())
	r := &recapFake{}
	m.SetInboxVerbCaller(r.call, func() string { return "nonce-1" })
	m.Windows[1].CustomName = "api"
	m.Windows[1].AgentState = "idle"
	m.Windows[1].AgentCompletionSeq = 5
	m.FocusWindow(0)
	left := time.Now().Add(-away).UnixNano()
	m.SidebarAgentSeenAt = map[string]int64{"w-2": left}
	m.SidebarAgentSeenSeq = map[string]uint64{"w-2": 5 - turns}
	r.recap = sampleRecap(left)
	return m, r
}

// TestReturnToastAfterTenMinutes: coming back to a pane that finished three
// turns while the person was away 42 minutes puts one line in the dock, read
// from the recap since they looked away.
func TestReturnToastAfterTenMinutes(t *testing.T) {
	m, r := recapOS(t, 42*time.Minute, 3)
	m.FocusWindow(1)
	cmd := m.AgentRecapFetch()
	if cmd == nil {
		t.Fatal("coming back asked for no recap")
	}
	m.Update(cmd())
	if len(r.calls) != 1 || r.calls[0]["window"] != "w-2" || r.calls[0]["recap"] != true || r.calls[0]["since"] == nil {
		t.Fatalf("agent-activity got %v", r.calls)
	}
	want := "api while you were away (42m): 3 turns, 6 files, 11 commands, go test passed, at prompt"
	if got := lastNotice(m).Message; got != want {
		t.Errorf("the dock said %q, want %q", got, want)
	}
	// Focusing it again says nothing more.
	m.FocusWindow(0)
	m.FocusWindow(1)
	if cmd := m.AgentRecapFetch(); cmd != nil {
		t.Error("a second look asked for another recap")
	}
}

// TestReturnToastIsGated: no toast when away for less than agents.recap
// away, when nothing finished, when the mode says inbox or off, and for a
// plain shell pane.
func TestReturnToastIsGated(t *testing.T) {
	for _, c := range []struct {
		name  string
		away  time.Duration
		turns uint64
		mode  string
	}{
		{"away 9m", 9 * time.Minute, 3, ""},
		{"no turn finished", time.Hour, 0, ""},
		{"mode inbox", time.Hour, 3, config.RecapInbox},
		{"mode off", time.Hour, 3, config.RecapOff},
	} {
		m, r := recapOS(t, c.away, c.turns)
		m.UserConfig.Agents.Recap.Mode = c.mode
		m.FocusWindow(1)
		if cmd := m.AgentRecapFetch(); cmd != nil || len(r.calls) != 0 {
			t.Errorf("%s: a recap was asked for", c.name)
		}
	}
	// A longer away setting holds a 42 minute absence back.
	m, _ := recapOS(t, 42*time.Minute, 3)
	m.UserConfig.Agents.Recap.Away = "1h"
	m.FocusWindow(1)
	if m.AgentRecapFetch() != nil {
		t.Error("away = 1h still toasted after 42 minutes")
	}
	// A pane no agent reported on records nothing and asks nothing.
	m, r := recapOS(t, time.Hour, 3)
	m.Windows[1].AgentState, m.Windows[1].AgentCompletionSeq = "", 0
	m.FocusWindow(1)
	if m.AgentRecapFetch() != nil || len(r.calls) != 0 {
		t.Error("a plain pane asked for a recap")
	}
}

// TestReturnToastWithoutARing: a daemon that cannot answer still gets the
// turns this client counted in the dock.
func TestReturnToastWithoutARing(t *testing.T) {
	m, r := recapOS(t, 20*time.Minute, 2)
	r.err = &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: "agent-activity"}
	m.FocusWindow(1)
	m.Update(m.AgentRecapFetch()())
	if got := lastNotice(m).Message; got != "api while you were away (20m): 2 turns, at prompt" {
		t.Errorf("the dock said %q", got)
	}
}

// TestInboxFinishedDetailIsTheRecap: a Finished item's detail under the list
// is the recap since the person looked away, with the agent's model, context
// and cost.
func TestInboxFinishedDetailIsTheRecap(t *testing.T) {
	m, r := recapOS(t, 42*time.Minute, 3)
	m.Windows[1].AgentMeta = []sessiontree.MetaToken{{Key: "model", Value: "opus 4.7"}, {Key: "context", Value: "42%"}, {Key: "cost", Value: "$1.20"}}
	done := item("1", session.AttentionFinished, "here", "w-2", "Added retry with backoff and tests", time.Now().Add(-3*time.Minute).UnixNano())
	done.Name, done.Harness = "api", "claude"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{done}})
	m.OpenInbox("")
	cmd := m.InboxRecapFetch()
	if cmd == nil {
		t.Fatal("the finished item asked for no recap")
	}
	m.Update(cmd())
	if m.InboxRecapFetch() != nil {
		t.Error("the same item asked for its recap twice")
	}
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{
		"While you were away (42m)",
		"3 turns. 6 files: api/retry.go, api/retry_test.go and 4 more",
		"11 commands. Tests: go test ./... passed 2m ago",
		"Last said: Added retry with backoff and tests.",
		"claude · opus 4.7 · 42% ctx · $1.20",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the detail lacks %q:\n%s", want, plain)
		}
	}
	if since := r.calls[0]["since"]; since != m.SidebarAgentSeenAt["w-2"] {
		t.Errorf("the recap was asked since %v, want when the person looked away", since)
	}

	// Mode off: no recap in the Inbox either.
	m.UserConfig.Agents.Recap.Mode = config.RecapOff
	m.Inbox.recap = inboxRecapView{}
	if m.InboxRecapFetch() != nil {
		t.Error("mode off still read a recap for the Inbox")
	}
}

// TestFactsLineLeavesOutWhatWasNotSaid: a fact the harness did not state is
// left out with its separator, and a pane with no metadata has no line.
func TestFactsLineLeavesOutWhatWasNotSaid(t *testing.T) {
	forgetSidebarState(t)
	m := inboxOS(t, zeroSettle())
	if got := m.agentFactsLine("here", "w-1", "claude"); got != "" {
		t.Errorf("no metadata drew %q", got)
	}
	m.Windows[0].AgentMeta = []sessiontree.MetaToken{{Key: "model", Value: "gpt-5"}, {Key: "plan", Value: "3/7"}}
	if got := m.agentFactsLine("here", "w-1", "codex"); got != "codex"+sepWord()+"gpt-5"+sepWord()+"plan 3/7" {
		t.Errorf("facts = %q", got)
	}
}

// TestRecapTestName: the test command is cut to what names it.
func TestRecapTestName(t *testing.T) {
	for in, want := range map[string]string{
		"go test ./...":     "go test",
		"pytest -x tests/":  "pytest",
		"npm test":          "npm test",
		"cargo test --all":  "cargo test",
		"./scripts/test.sh": "./scripts/test.sh",
	} {
		if got := recapTestName(in); got != want {
			t.Errorf("recapTestName(%q) = %q, want %q", in, got, want)
		}
	}
}
