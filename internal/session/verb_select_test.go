package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// selectFixture is two sessions with agents of two harnesses in them: api has
// a codex pane and a claude pane and is a fan-out group, web has one codex
// pane and a plain shell.
type selectFixture struct {
	d                   *Daemon
	c                   *verbConn
	api, web            *Session
	apiCodex, apiClaude string
	webCodex, webShell  string
}

func newSelectFixture(t *testing.T) *selectFixture {
	t.Helper()
	d, sp := startTestDaemon(t)
	f := &selectFixture{d: d, c: dialVerb(t, sp)}
	f.api, f.apiCodex, f.apiClaude = twoWindowSession(t, d, "api-fan-retry")
	f.web, f.webCodex, f.webShell = twoWindowSession(t, d, "web")
	if err := f.api.SetWorktree(&WorktreeInfo{Group: "fan/retry"}); err != nil {
		t.Fatalf("SetWorktree: %v", err)
	}
	report := func(s *Session, w string, state AgentState, harness string) {
		t.Helper()
		if _, _, err := s.ApplyAgentReport(w, AgentReport{State: state, Harness: harness}); err != nil {
			t.Fatalf("ApplyAgentReport: %v", err)
		}
	}
	report(f.api, f.apiCodex, AgentStateIdle, "codex")
	report(f.api, f.apiClaude, AgentStateWorking, "claude-code")
	report(f.web, f.webCodex, AgentStateIdle, "codex")
	return f
}

// windowsOf is the window ids of the rows of a list-agents answer.
func windowsOf(res map[string]any) []string {
	rows, _ := res["agents"].([]any)
	var out []string
	for _, r := range rows {
		out = append(out, r.(map[string]any)["window_id"].(string))
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestListAgentsSelectCoversEverySession(t *testing.T) {
	f := newSelectFixture(t)

	res := result(t, callVerb(t, f.c, "list-agents", map[string]any{"select": "harness:codex"}))
	if got := windowsOf(res); !sameSet(got, []string{f.apiCodex, f.webCodex}) {
		t.Errorf("harness:codex listed %v, want the codex pane of each session", got)
	}
	if res["confirm"] == "" || res["confirm"] == nil {
		t.Error("a listing by selector carried no confirm token")
	}

	// A program name is the harness it names.
	res = result(t, callVerb(t, f.c, "list-agents", map[string]any{"select": "harness:claude"}))
	if got := windowsOf(res); !sameSet(got, []string{f.apiClaude}) {
		t.Errorf("harness:claude listed %v, want the claude-code pane", got)
	}

	res = result(t, callVerb(t, f.c, "list-agents", map[string]any{"select": "group:fan/* state:idle"}))
	if got := windowsOf(res); !sameSet(got, []string{f.apiCodex}) {
		t.Errorf("group:fan/* state:idle listed %v, want the idle pane of the fan-out", got)
	}
	rows := res["agents"].([]any)
	if g := rows[0].(map[string]any)["group"]; g != "fan/retry" {
		t.Errorf("row group = %v, want fan/retry", g)
	}

	// With a session, the selector narrows that session only.
	res = result(t, callVerb(t, f.c, "list-agents", map[string]any{"session": "web", "select": "harness:codex"}))
	if got := windowsOf(res); !sameSet(got, []string{f.webCodex}) {
		t.Errorf("session web, harness:codex listed %v, want web's codex pane", got)
	}
	// A write by the selector reaches every session, so a listing of one
	// session hands out no token for it.
	if _, ok := res["confirm"]; ok {
		t.Errorf("a listing narrowed to one session carried a confirm token: %v", res["confirm"])
	}

	resp := callVerb(t, f.c, "list-agents", map[string]any{"select": "colour:red"})
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("a bad selector: code %q, want %q", code, ErrVerbInvalidParams)
	}
}

// confirmHint returns the confirm_required hint of a refused write.
func confirmHint(t *testing.T, resp map[string]any) (string, []string) {
	t.Helper()
	if code := errCode(t, resp); code != ErrVerbConfirmRequired {
		t.Fatalf("code = %q, want %q (%v)", code, ErrVerbConfirmRequired, resp)
	}
	hint := resp["error"].(map[string]any)["hint"].(map[string]any)
	token, _ := hint["confirm"].(string)
	if token == "" {
		t.Fatalf("confirm_required carried no token: %v", hint)
	}
	var labels []string
	for _, a := range hint["available"].([]any) {
		labels = append(labels, a.(string))
	}
	return token, labels
}

// unreadFor is how many unread messages wait in one pane's inbox.
func unreadFor(t *testing.T, c *verbConn, session, window string) int {
	t.Helper()
	res := result(t, callVerb(t, c, "read-agent-messages", map[string]any{"session": session, "to": window, "unread": true, "peek": true}))
	msgs, _ := res["messages"].([]any)
	return len(msgs)
}

// TestSendBySelectorSendsNothingUntilConfirmed is the rule that keeps a
// selector from becoming a silent broadcast: the first call sends nothing and
// names the set, and the token is bound to that set.
func TestSendBySelectorSendsNothingUntilConfirmed(t *testing.T) {
	f := newSelectFixture(t)
	params := map[string]any{"select": "harness:codex", "text": "main moved, rebase", "from": f.apiClaude}

	token, labels := confirmHint(t, callVerb(t, f.c, "send-agent-message", params))
	if len(labels) != 2 || !strings.Contains(strings.Join(labels, " "), "api-fan-retry/") || !strings.Contains(strings.Join(labels, " "), "web/") {
		t.Errorf("the refusal did not list both codex panes: %v", labels)
	}
	for _, w := range []struct{ s, w string }{{"api-fan-retry", f.apiCodex}, {"web", f.webCodex}} {
		if n := unreadFor(t, f.c, w.s, w.w); n != 0 {
			t.Fatalf("an unconfirmed send left %d messages for %s", n, w.w)
		}
	}

	// The token list-agents gives for the same selector is the same token.
	listed := result(t, callVerb(t, f.c, "list-agents", map[string]any{"select": "harness:codex"}))
	if listed["confirm"] != token {
		t.Errorf("list-agents token %v differs from the refusal's %v", listed["confirm"], token)
	}

	params["confirm"] = token
	res := result(t, callVerb(t, f.c, "send-agent-message", params))
	if res["sent"] != float64(2) || res["failed"] != float64(0) {
		t.Fatalf("confirmed send: %v", res)
	}
	if n := unreadFor(t, f.c, "api-fan-retry", f.apiCodex); n != 1 {
		t.Errorf("api's codex pane has %d unread, want 1", n)
	}
	// The sender is in api, and the message to web still names it.
	read := result(t, callVerb(t, f.c, "read-agent-messages", map[string]any{"session": "web", "to": f.webCodex, "peek": true}))
	msg := read["messages"].([]any)[0].(map[string]any)
	if msg["from"] != f.apiClaude {
		t.Errorf("the message to web is from %v, want the sender's pane %s", msg["from"], f.apiClaude)
	}

	// A pane joining the set changes the token, and the old one is refused
	// with the new set.
	if _, _, err := f.web.ApplyAgentReport(f.webShell, AgentReport{State: AgentStateIdle, Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	newToken, labels := confirmHint(t, callVerb(t, f.c, "send-agent-message", params))
	if newToken == token || len(labels) != 3 {
		t.Errorf("after a pane joined: token %s (was %s), %d panes listed, want a new token and 3", newToken, token, len(labels))
	}
	if n := unreadFor(t, f.c, "web", f.webShell); n != 0 {
		t.Errorf("a stale token still sent to the pane that joined")
	}
}

func TestSendBySelectorRefusesMixedAddressing(t *testing.T) {
	f := newSelectFixture(t)
	for _, extra := range []map[string]any{
		{"to": f.apiCodex},
		{"session": "web"},
		{"reply_to": 1},
	} {
		params := map[string]any{"select": "harness:codex", "text": "x"}
		for k, v := range extra {
			params[k] = v
		}
		if code := errCode(t, callVerb(t, f.c, "send-agent-message", params)); code != ErrVerbInvalidParams {
			t.Errorf("select with %v: code %q, want %q", extra, code, ErrVerbInvalidParams)
		}
	}
	if code := errCode(t, callVerb(t, f.c, "send-agent-message", map[string]any{"to": f.apiCodex, "session": "api-fan-retry", "text": "x", "confirm": "abc"})); code != ErrVerbInvalidParams {
		t.Errorf("confirm without select: code %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestSelectorIsRefusedFromAHostedPane: a hosted pane's calls are pinned to its
// own window, and a selector would reach every session here.
func TestSelectorIsRefusedFromAHostedPane(t *testing.T) {
	f := newSelectFixture(t)
	cs := &connState{paneOnly: true}
	raw, _ := json.Marshal(map[string]any{"select": "harness:codex", "text": "x"})
	if _, verr := f.d.verbSendAgentMessage(cs, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("send by selector from a hosted pane: %v, want %s", verr, ErrVerbForbidden)
	}
	raw, _ = json.Marshal(map[string]any{"select": "harness:codex", "text": "x"})
	if _, verr := f.d.verbAskAgent(cs, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("ask by selector from a hosted pane: %v, want %s", verr, ErrVerbForbidden)
	}
	raw, _ = json.Marshal(map[string]any{"condition": "agent-state", "select": "harness:codex", "until": "idle"})
	if _, verr := f.d.verbWaitFor(cs, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("wait by selector from a hosted pane: %v, want %s", verr, ErrVerbForbidden)
	}
}

// TestAskBySelectorRefusesABlockedPaneAndAsksTheRest: one pane on needs_input
// is refused in its own row, with nothing typed, and the pane at rest answers.
func TestAskBySelectorRefusesABlockedPaneAndAsksTheRest(t *testing.T) {
	f := newSelectFixture(t)
	// web's shell is the pane that answers: a shell prints what it is asked
	// to echo. api's codex pane is blocked on an approval.
	if _, _, err := f.web.ApplyAgentReport(f.webShell, AgentReport{State: AgentStateIdle, Harness: "aider"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.api.ApplyAgentReport(f.apiCodex, AgentReport{State: AgentStateNeedsInput, Harness: "aider", Kind: "approval", Message: "Run rm?"}); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"select": "harness:aider", "text": "echo tuios_select_reply", "settle": 700, "timeout": 4000}
	token, labels := confirmHint(t, callVerb(t, f.c, "ask-agent", params))
	if len(labels) != 2 {
		t.Fatalf("the refusal listed %v, want the two aider panes", labels)
	}
	params["confirm"] = token
	res := result(t, callVerb(t, f.c, "ask-agent", params))
	if res["answered"] != float64(1) || res["failed"] != float64(1) || res["untrusted"] != true {
		t.Fatalf("ask by selector: %v", res)
	}
	for _, r := range res["replies"].([]any) {
		row := r.(map[string]any)
		switch row["window"] {
		case f.apiCodex:
			e, _ := row["error"].(map[string]any)
			if row["ok"] != false || e["code"] != ErrVerbAgentBlocked {
				t.Errorf("the blocked pane's row: %v, want agent_blocked", row)
			}
		case f.webShell:
			if reply, _ := row["reply"].(string); row["ok"] != true || !strings.Contains(reply, "tuios_select_reply") {
				t.Errorf("the resting pane's row: %v, want its reply", row)
			}
		default:
			t.Errorf("a row for a pane that was not selected: %v", row)
		}
	}
}

// TestWaitForSelectEveryWaitsForTheWholeSet: with every, one pane at rest is not
// enough; the wait ends when the last matched pane gets there.
func TestWaitForSelectEveryWaitsForTheWholeSet(t *testing.T) {
	f := newSelectFixture(t)
	if _, _, err := f.api.ApplyAgentReport(f.apiClaude, AgentReport{State: AgentStateWorking, Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan map[string]any, 1)
	go func() {
		done <- f.c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-state","select":"group:fan/retry","until":"idle,done","every":true,"timeout":4000}}`)
	}()
	select {
	case resp := <-done:
		t.Fatalf("the wait ended with one pane still working: %v", resp)
	case <-time.After(400 * time.Millisecond):
	}
	if _, _, err := f.api.ApplyAgentReport(f.apiClaude, AgentReport{State: AgentStateDone, Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	res := result(t, <-done)
	if res["every"] != true || res["total"] != float64(2) {
		t.Errorf("every wait: %v, want both panes of the group", res)
	}

	// Without every, the first pane at rest is enough.
	res = result(t, f.c.call(t, `{"id":2,"verb":"wait-for","params":{"condition":"agent-state","select":"harness:codex session:web","until":"idle","timeout":1000}}`))
	if res["window"] != f.webCodex {
		t.Errorf("any wait matched %v, want web's codex pane", res["window"])
	}
	if code := errCode(t, f.c.call(t, `{"id":3,"verb":"wait-for","params":{"condition":"agent-state","select":"harness:codex","session":"web","until":"idle"}}`)); code != ErrVerbInvalidParams {
		t.Errorf("select with session: code %q, want %q", code, ErrVerbInvalidParams)
	}
	if code := errCode(t, f.c.call(t, `{"id":4,"verb":"wait-for","params":{"condition":"agent-state","every":true,"until":"idle"}}`)); code != ErrVerbInvalidParams {
		t.Errorf("every without select: code %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestHostAgentsSelectReadsTheEntryHost: the host term matches the machine an
// entry came from, and the rows of every host are narrowed alike.
func TestHostAgentsSelectReadsTheEntryHost(t *testing.T) {
	entries := []hostAgentsEntry{
		{Host: "local", Agents: []remoteAgentRow{{Session: "a", WindowID: "1", State: "idle", HarnessID: "codex"}}},
		{Host: "build", Agents: []remoteAgentRow{
			{Session: "b", WindowID: "2", State: "idle", HarnessID: "codex"},
			{Session: "c", WindowID: "3", State: "needs_input", HarnessID: "claude-code"},
		}},
	}
	sel, err := ParseSelector("host:build harness:codex", "")
	if err != nil {
		t.Fatal(err)
	}
	filterHostAgents(entries, sel)
	if len(entries[0].Agents) != 0 {
		t.Errorf("host:build kept a local row: %v", entries[0].Agents)
	}
	if len(entries[1].Agents) != 1 || entries[1].Agents[0].WindowID != "2" || entries[1].Session != "b" {
		t.Errorf("host:build harness:codex kept %v (session %q), want build's codex row in b", entries[1].Agents, entries[1].Session)
	}
}

func TestListAttentionSelect(t *testing.T) {
	f := newSelectFixture(t)
	setAgentState(t, f.c, "api-fan-retry", f.apiClaude, "needs_input", "approval", "Run rm?")
	setAgentState(t, f.c, "web", f.webCodex, "errored", "", "boom")

	items, res := listAttention(t, f.c, `{"select":"needs:you group:fan/*"}`)
	if len(items) != 1 || items[0]["window"] != f.apiClaude {
		t.Errorf("needs:you group:fan/* listed %v, want the blocked pane of the fan-out", items)
	}
	if res["select"] != "needs:you group:fan/*" {
		t.Errorf("select = %v", res["select"])
	}
	items, _ = listAttention(t, f.c, `{"select":"state:errored"}`)
	if len(items) != 1 || items[0]["window"] != f.webCodex {
		t.Errorf("state:errored listed %v, want web's codex pane", items)
	}
}
