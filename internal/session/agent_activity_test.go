package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// These tests cover the activity ring: what set-agent-state's activity puts in
// it and when, the reserved metadata keys it moves, what the session event
// sink adds, the recap, and the agent-activity verb and event.

func boolp(b bool) *bool { return &b }
func intp(n int) *int    { return &n }

// activityOf reads a pane's ring through the verb, the way a client does.
func activityOf(t *testing.T, c *verbConn, params map[string]any) (entries []AgentActivityEntry, res map[string]any) {
	t.Helper()
	res = result(t, callP(c, t, "agent-activity", params))
	raw, _ := json.Marshal(res["entries"])
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("entries: %v (%s)", err, raw)
	}
	return entries, res
}

// TestActivityRingIsBounded: a pane keeps the newest activityRingSize
// entries, numbered on, and says when it has dropped any.
func TestActivityRingIsBounded(t *testing.T) {
	store := newActivityStore(nil)
	for i := range activityRingSize + 44 {
		store.add("s", "work", "w", AgentActivityEntry{Kind: ActivityTool, Tool: "Bash", Target: strings.Repeat("x", i%3), At: int64(i + 1)}, true)
	}
	got, ok, dropped := store.read("s", "w")
	if !ok || !dropped {
		t.Fatalf("read = ok %v dropped %v, want both", ok, dropped)
	}
	if len(got) != activityRingSize {
		t.Fatalf("the ring holds %d entries, want %d", len(got), activityRingSize)
	}
	if got[0].Seq != 45 || got[len(got)-1].Seq != activityRingSize+44 {
		t.Errorf("the ring runs from seq %d to %d, want 45 to %d", got[0].Seq, got[len(got)-1].Seq, activityRingSize+44)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Seq != got[i-1].Seq+1 {
			t.Fatalf("entries out of order at %d: %d after %d", i, got[i].Seq, got[i-1].Seq)
		}
	}
	// A pane that never reported activity has no ring.
	if _, ok, _ := store.read("s", "other"); ok {
		t.Error("a pane with no activity has a ring")
	}
}

// TestActivityRecordedByTheIdentityGuardNotTheState: a PostToolUse refused by
// if_state still finished a tool call, so its activity is kept; a nested run
// the identity guard refuses is not the pane's agent, so its activity is not,
// even when if_state would also have refused it.
func TestActivityRecordedByTheIdentityGuardNotTheState(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	res := setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","harness":"claude-code","agent_session_id":"outer","activity":{"event":"tool","tool":"Bash","target":"go test ./..."}}`)
	if res["applied"] != true || res["activity_recorded"] != true {
		t.Fatalf("the pane's own tool call: %v", res)
	}
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","if_state":"needs_input","harness":"claude-code","agent_session_id":"outer","activity":{"event":"tool_done","tool":"Bash","target":"go test ./...","ok":true}}`)
	if res["applied"] != false || res["reason"] != agentRefusedIfState || res["activity_recorded"] != true {
		t.Fatalf("a PostToolUse refused by if_state: %v, want its activity recorded", res)
	}
	// A nested run, mid-turn: the guard refuses it by session.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","harness":"claude-code","agent_session_id":"nested","activity":{"event":"turn_end","text":"nested run finished"}}`)
	if res["reason"] != agentRefusedForeignSession || res["activity_recorded"] != false {
		t.Fatalf("a nested run's Stop: %v, want refused and not recorded", res)
	}
	// The same nested run's PostToolUse fails if_state first, and is still
	// not the pane's.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","if_state":"needs_input","harness":"claude-code","agent_session_id":"nested","activity":{"event":"tool_done","tool":"Bash","target":"rm -rf nested"}}`)
	if res["reason"] != agentRefusedIfState || res["activity_recorded"] != false {
		t.Fatalf("a nested run's PostToolUse: %v, want not recorded", res)
	}
	entries, out := activityOf(t, c, map[string]any{"session": "work", "window": "Window"})
	var kinds []string
	for _, e := range entries {
		if e.Kind != ActivityState {
			kinds = append(kinds, e.Kind+" "+e.Target+e.Text)
		}
	}
	if want := []string{"tool go test ./...", "tool_done go test ./..."}; !slices.Equal(kinds, want) {
		t.Errorf("the ring holds %q, want %q", kinds, want)
	}
	if out["untrusted"] != true {
		t.Error("agent-activity is not marked untrusted")
	}
	// A report without activity says nothing about it.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working"}`)
	if _, ok := res["activity_recorded"]; ok {
		t.Errorf("a report with no activity answered activity_recorded: %v", res)
	}
}

// TestActivityTextIsCleaned: activity text is the agent's, so it is kept to
// one line with no control characters and likely secrets masked.
func TestActivityTextIsCleaned(t *testing.T) {
	e := activityEntryOf(&AgentActivityReport{
		Event:  ActivityPrompt,
		Tool:   "Ba\x1bsh",
		Target: "export API_TOKEN=abc123 &&\n make",
		Text:   "first \x1b[31mline\nsecond line",
		Files:  []string{"a.go", "a.go", "", "b.go"},
	})
	if e.Text != "first [31mline" {
		t.Errorf("text = %q", e.Text)
	}
	if strings.Contains(e.Target, "abc123") || strings.Contains(e.Target, "\n") {
		t.Errorf("target = %q, want one line with the token masked", e.Target)
	}
	if e.Tool != "Bash" {
		t.Errorf("tool = %q", e.Tool)
	}
	if !slices.Equal(e.Files, []string{"a.go", "b.go"}) {
		t.Errorf("files = %v", e.Files)
	}
}

// TestActivityMovesTheReservedMetaKeys: a prompt sets prompt and clears now,
// a tool call sets now, the end of a turn clears it, and the model a harness
// names is kept once.
func TestActivityMovesTheReservedMetaKeys(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	meta := func() map[string]string {
		return agentMetaMap(sess.GetState().Windows[0].AgentMeta, time.Now().UnixNano())
	}

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","activity":{"event":"prompt","text":"fix the retry test","model":"gpt-5"}}`)
	if m := meta(); m[AgentMetaPrompt] != "fix the retry test" || m[AgentMetaModel] != "gpt-5" || m[AgentMetaNow] != "" {
		t.Fatalf("after a prompt: %v", m)
	}
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","activity":{"event":"tool","tool":"Bash","target":"go test ./..."}}`)
	if m := meta(); m[AgentMetaNow] != "Bash: go test ./..." {
		t.Fatalf("after a tool call: %v", m)
	}
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","activity":{"event":"tool_done","tool":"Bash","target":"go test ./...","ok":true}}`)
	if m := meta(); m[AgentMetaNow] != "Bash: go test ./..." {
		t.Fatalf("a finished call cleared now: %v", m)
	}
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","activity":{"event":"turn_end","text":"Fixed it."}}`)
	m := meta()
	if _, ok := m[AgentMetaNow]; ok {
		t.Fatalf("now outlived the turn: %v", m)
	}
	if m[AgentMetaPrompt] != "fix the retry test" {
		t.Errorf("the prompt went with the turn: %v", m)
	}
	for _, tok := range sess.GetState().Windows[0].AgentMeta {
		want := agentMetaActivitySource
		if tok.Key == AgentMetaModel {
			want = agentMetaHookSource
		}
		if tok.Source != want {
			t.Errorf("%s has source %q, want %q", tok.Key, tok.Source, want)
		}
	}
}

// TestActivityMetaChangesPushNothingWhenTheSame: a hook fires on every tool
// call, and a write of what the pane already shows must not push state.
func TestActivityMetaChangesPushNothingWhenTheSame(t *testing.T) {
	sess := newTestSessionWithWindow(t)
	id := sess.GetState().Windows[0].ID
	now := "Bash: go test ./..."
	sess.applyActivityMeta(id, activityMeta{keys: []string{AgentMetaNow}, values: []*string{&now}, model: "opus"})
	version := sess.GetState().Version
	sess.applyActivityMeta(id, activityMeta{keys: []string{AgentMetaNow}, values: []*string{&now}, model: "opus"})
	sess.applyActivityMeta(id, activityMeta{keys: []string{AgentMetaPrompt}, values: []*string{nil}})
	if v := sess.GetState().Version; v != version {
		t.Errorf("rewriting the same activity metadata moved the version from %d to %d", version, v)
	}
	// A status line that already wrote the model is left alone.
	if _, err := sess.SetAgentMeta(id, AgentMetaUpdate{Keys: []string{AgentMetaModel}, Values: []*string{strp("sonnet")}, Source: "statusline"}); err != nil {
		t.Fatal(err)
	}
	version = sess.GetState().Version
	sess.applyActivityMeta(id, activityMeta{model: "sonnet"})
	if v := sess.GetState().Version; v != version {
		t.Error("a hook naming the model the status line already shows pushed state")
	}
}

// TestSetAgentMetaRefusesTheReservedKeys: now and prompt are written by tuios
// from hook activity, never by a caller, and a caller's clear leaves them.
func TestSetAgentMetaRefusesTheReservedKeys(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	for _, key := range reservedAgentMetaKeys {
		mustRefuse(t, callP(c, t, "set-agent-meta", map[string]any{"session": "work", "window": "Window", "tokens": map[string]any{key: "x"}}),
			ErrVerbInvalidParams, "set-agent-meta "+key)
		mustRefuse(t, callP(c, t, "set-agent-meta", map[string]any{"session": "work", "window": "Window", "tokens": map[string]any{key: nil}}),
			ErrVerbInvalidParams, "set-agent-meta removing "+key)
	}
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","activity":{"event":"tool","tool":"Bash","target":"make"}}`)
	result(t, callP(c, t, "set-agent-meta", map[string]any{"session": "work", "window": "Window", "tokens": map[string]any{"cost": "$1"}, "source": "activity"}))
	for _, source := range []string{"", "activity"} {
		result(t, callP(c, t, "set-agent-meta", map[string]any{"session": "work", "window": "Window", "clear": true, "source": source}))
		if m := agentMetaMap(sess.GetState().Windows[0].AgentMeta, time.Now().UnixNano()); m[AgentMetaNow] != "Bash: make" || m["cost"] != "" {
			t.Errorf("after clear with source %q: %v, want only now left", source, m)
		}
	}
}

// TestUnchangedAgentMetaWritePushesNothing: a status line rewriting the same
// values bumps no version, and its TTL is renewed only once less than half of
// it remains.
func TestUnchangedAgentMetaWritePushesNothing(t *testing.T) {
	sess := newTestSessionWithWindow(t)
	id := sess.GetState().Windows[0].ID
	write := func(v string, ttl time.Duration) {
		t.Helper()
		if _, err := sess.SetAgentMeta(id, AgentMetaUpdate{Keys: []string{"context"}, Values: []*string{&v}, Source: "statusline", TTL: ttl}); err != nil {
			t.Fatal(err)
		}
	}
	write("42%", time.Minute)
	version := sess.GetState().Version
	expires := sess.GetState().Windows[0].AgentMeta[0].Expires
	write("42%", time.Minute)
	if v := sess.GetState().Version; v != version {
		t.Errorf("an unchanged write moved the version from %d to %d", version, v)
	}
	if e := sess.GetState().Windows[0].AgentMeta[0].Expires; e != expires {
		t.Error("an unchanged write renewed a TTL with more than half of it left")
	}
	write("43%", time.Minute)
	if sess.GetState().Version == version {
		t.Error("a changed value did not move the version")
	}
}

// TestApplyAgentMetaRenewsAtHalfTheTTL is the expiry rule on its own.
func TestApplyAgentMetaRenewsAtHalfTheTTL(t *testing.T) {
	ttl := 10 * time.Second
	cur, changed, _ := applyAgentMeta(nil, AgentMetaUpdate{Keys: []string{"k"}, Values: []*string{strp("v")}, Source: "s", TTL: ttl}, 0)
	if !changed {
		t.Fatal("a new key was not a change")
	}
	same := AgentMetaUpdate{Keys: []string{"k"}, Values: []*string{strp("v")}, Source: "s", TTL: ttl}
	if _, changed, _ := applyAgentMeta(cur, same, int64(4*time.Second)); changed {
		t.Error("a rewrite with 6s of 10s left renewed it")
	}
	next, changed, _ := applyAgentMeta(cur, same, int64(6*time.Second))
	if !changed || next[0].Expires != int64(16*time.Second) {
		t.Errorf("a rewrite with 4s of 10s left: changed %v, expires %d", changed, next[0].Expires)
	}
	// Another source writing the same value is a change: it owns the key now.
	if _, changed, _ := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"k"}, Values: []*string{strp("v")}, Source: "other", TTL: ttl}, 1); !changed {
		t.Error("a different source was not a change")
	}
	// No TTL on a key that had one makes it permanent, which is a change.
	if _, changed, _ := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"k"}, Values: []*string{strp("v")}, Source: "s"}, 1); !changed {
		t.Error("dropping the TTL was not a change")
	}
	// Removing a key that is not there, and a clear that finds nothing,
	// change nothing.
	if _, changed, _ := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"gone"}, Values: []*string{nil}, Clear: true, Source: "nobody"}, 1); changed {
		t.Error("removing nothing was a change")
	}
}

// TestShellCommandsJoinOnlyAnExistingRing: OSC 133 commands and state changes
// are added to a pane that has a ring, and a plain shell pane gets none.
func TestShellCommandsJoinOnlyAnExistingRing(t *testing.T) {
	store := newActivityStore(nil)
	s := &Session{ID: "sid", Name: "work"}
	store.noteSessionEvent(s, SessionEvent{Type: EventCommandFinished, Window: "w", Cmdline: "make", ExitCode: intp(0)})
	store.noteSessionEvent(s, SessionEvent{Type: EventAgentState, Window: "w", State: "done"})
	if store.count() != 0 {
		t.Fatal("a plain shell pane got a ring")
	}
	store.add(s.ID, s.Name, "w", AgentActivityEntry{Kind: ActivityPrompt, Text: "go"}, true)
	store.noteSessionEvent(s, SessionEvent{Type: EventCommandFinished, Window: "w", Cmdline: "go test ./...", ExitCode: intp(1)})
	store.noteSessionEvent(s, SessionEvent{Type: EventAgentState, Window: "w", State: "done", completionSeq: 3, prevCompletionSeq: 2})
	got, _, _ := store.read(s.ID, "w")
	if len(got) != 3 {
		t.Fatalf("ring = %+v", got)
	}
	if got[1].Kind != ActivityCommand || got[1].Target != "go test ./..." || got[1].Exit == nil || *got[1].Exit != 1 {
		t.Errorf("command entry = %+v", got[1])
	}
	if got[2].Kind != ActivityState || got[2].Text != "done" || got[2].turns != 1 {
		t.Errorf("state entry = %+v", got[2])
	}
	store.noteSessionEvent(s, SessionEvent{Type: EventWindowClosed, Window: "w"})
	if store.count() != 0 {
		t.Error("a closed window kept its ring")
	}
	store.add(s.ID, s.Name, "w2", AgentActivityEntry{Kind: ActivityPrompt}, true)
	store.forgetSession(s.ID)
	if store.count() != 0 {
		t.Error("an ended session kept its rings")
	}
}

// TestAgentActivityRecap is the recap's arithmetic: turns from the
// completion_seq deltas, files once each, shell tool calls and shell commands
// counted, the newest test run and how it went, and the last thing said.
func TestAgentActivityRecap(t *testing.T) {
	patterns := []string{"go test", "pytest"}
	entries := []AgentActivityEntry{
		{Kind: ActivityPrompt, Text: "fix it", At: 1},
		{Kind: ActivityTool, Tool: "Bash", Target: "go test ./...", At: 2},
		{Kind: ActivityToolFailed, Tool: "Bash", Target: "go test ./...", At: 3},
		{Kind: ActivityTool, Tool: "Edit", Target: "a.go", At: 4},
		{Kind: ActivityToolDone, Tool: "Edit", Files: []string{"a.go"}, OK: boolp(true), At: 5},
		{Kind: ActivityToolDone, Tool: "apply_patch", Files: []string{"a.go", "b.go"}, At: 6},
		{Kind: ActivityState, Text: "done", turns: 1, At: 7},
		{Kind: ActivityTurnEnd, Text: "First turn.", At: 7},
		{Kind: ActivityCommand, Target: "make lint", Exit: intp(0), At: 8},
		{Kind: ActivityState, Text: "working", At: 9},
		{Kind: ActivityTool, Tool: "Bash", Target: "go test ./...", At: 10},
		{Kind: ActivityToolDone, Tool: "Bash", Target: "go test ./...", OK: boolp(true), At: 11},
		{Kind: ActivityState, Text: "done", turns: 2, At: 12},
		{Kind: ActivityTurnEnd, Text: "Added retry with backoff.", At: 12},
	}
	rc := agentActivityRecap(entries, 1, AgentStateDone, patterns)
	if rc.Turns != 3 {
		t.Errorf("turns = %d, want 3", rc.Turns)
	}
	if !slices.Equal(rc.Files, []string{"a.go", "b.go"}) || rc.FilesTotal != 2 {
		t.Errorf("files = %v (%d)", rc.Files, rc.FilesTotal)
	}
	if rc.Commands != 3 {
		t.Errorf("commands = %d, want 3: two shell tool calls and one shell command", rc.Commands)
	}
	if rc.Tests == nil || rc.Tests.Cmdline != "go test ./..." || rc.Tests.OK == nil || !*rc.Tests.OK || rc.Tests.At != 11 {
		t.Errorf("tests = %+v, want the passing run at 11", rc.Tests)
	}
	if rc.LastSaid != "Added retry with backoff." || rc.State != "done" || rc.Since != 1 {
		t.Errorf("recap = %+v", rc)
	}

	// Pass or fail comes from the newest result.
	cases := []struct {
		name    string
		entries []AgentActivityEntry
		want    *bool
		none    bool
	}{
		{"a failure event", []AgentActivityEntry{{Kind: ActivityToolFailed, Tool: "Bash", Target: "pytest -q"}}, boolp(false), false},
		{"a result that says it failed", []AgentActivityEntry{{Kind: ActivityToolDone, Tool: "Bash", Target: "go test ./x", OK: boolp(false)}}, boolp(false), false},
		{"a call still running", []AgentActivityEntry{{Kind: ActivityTool, Tool: "Bash", Target: "go test ./x"}}, nil, false},
		{"a shell command that failed", []AgentActivityEntry{{Kind: ActivityCommand, Target: "go test ./...", Exit: intp(2)}}, boolp(false), false},
		{"a shell command with no status", []AgentActivityEntry{{Kind: ActivityCommand, Target: "go test ./..."}}, nil, false},
		{"no test command", []AgentActivityEntry{{Kind: ActivityToolDone, Tool: "Bash", Target: "ls"}}, nil, true},
		{"a test pattern in a file edit", []AgentActivityEntry{{Kind: ActivityToolDone, Tool: "Edit", Target: "go test.md"}}, nil, true},
	}
	for _, tc := range cases {
		got := agentActivityRecap(tc.entries, 0, AgentStateWorking, patterns).Tests
		switch {
		case tc.none && got != nil:
			t.Errorf("%s: found a test run %+v", tc.name, got)
		case tc.none:
		case got == nil:
			t.Errorf("%s: found no test run", tc.name)
		case (got.OK == nil) != (tc.want == nil) || (got.OK != nil && *got.OK != *tc.want):
			t.Errorf("%s: ok = %v, want %v", tc.name, got.OK, tc.want)
		}
	}
}

// TestAgentActivityVerb walks the verb: since_seq, limit, the recap over
// everything after since whatever the limit, the ring dying with the window,
// and a pane with no ring answering an empty list.
func TestAgentActivityVerb(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	d.SetRecapTestPatterns([]string{"make check"})

	report := func(activity string) {
		t.Helper()
		res := setAgentStateVerb(t, c, `{"session":"work","window":"`+a+`","state":"working","activity":`+activity+`}`)
		if res["activity_recorded"] != true {
			t.Fatalf("not recorded: %v", res)
		}
	}
	report(`{"event":"prompt","text":"check it"}`)
	report(`{"event":"tool","tool":"Bash","target":"make check"}`)
	report(`{"event":"tool_done","tool":"Bash","target":"make check","ok":true}`)
	report(`{"event":"tool_done","tool":"Write","target":"notes.md","files":["notes.md"]}`)

	all, res := activityOf(t, c, map[string]any{"session": "work", "window": a, "recap": true})
	var own []AgentActivityEntry
	for _, e := range all {
		if e.Kind != ActivityState {
			own = append(own, e)
		}
	}
	if len(own) != 4 || own[0].Kind != ActivityPrompt || own[3].Files[0] != "notes.md" {
		t.Fatalf("entries = %+v", all)
	}
	if res["window"] != a || res["last_seq"] != float64(all[len(all)-1].Seq) {
		t.Errorf("result = %v", res)
	}
	rc, _ := res["recap"].(map[string]any)
	tests, _ := rc["tests"].(map[string]any)
	if rc["files_total"] != float64(1) || tests["cmdline"] != "make check" || tests["ok"] != true {
		t.Errorf("recap = %v", rc)
	}

	after := all[1].Seq
	got, _ := activityOf(t, c, map[string]any{"session": "work", "window": a, "since_seq": after, "limit": 1})
	if len(got) != 1 || got[0].Seq != all[len(all)-1].Seq {
		t.Errorf("since_seq %d limit 1 = %+v, want the newest entry", after, got)
	}
	_, res = activityOf(t, c, map[string]any{"session": "work", "window": a, "limit": 1, "recap": true})
	if rc, _ := res["recap"].(map[string]any); rc["commands"] != float64(1) {
		t.Errorf("a limited call's recap = %v, want it over every entry", rc)
	}

	got, res = activityOf(t, c, map[string]any{"session": "work", "window": b, "recap": true})
	if len(got) != 0 || res["last_seq"] != float64(0) {
		t.Errorf("a pane with no ring answered %v", res)
	}

	if _, err := sess.CloseDaemonWindow(a); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for d.activity.has(sess.ID, a) {
		if time.Now().After(deadline) {
			t.Fatal("a closed window kept its ring")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAgentActivityEventIsPublishedAndNotReplayed: each entry goes out as an
// agent-activity event to a subscriber that names it, and the replay ring
// does not keep it, so an agent at work cannot push the events a reconnecting
// client needs out of it.
func TestAgentActivityEventIsPublishedAndNotReplayed(t *testing.T) {
	h := newEventHub()
	sub := h.subscribe(eventFilter{types: map[string]bool{EventAgentActivity: true}}, 8)
	defer h.unsubscribe(sub)
	plain := h.subscribe(eventFilter{}, 8)
	defer h.unsubscribe(plain)
	store := newActivityStore(h.publish)
	store.add("sid", "work", "w", AgentActivityEntry{Kind: ActivityTool, Tool: "Bash", Target: "ls"}, true)

	select {
	case ev := <-sub.ch:
		if ev.Type != EventAgentActivity || ev.Session != "work" || ev.Window != "w" || ev.Entry == nil || ev.Entry.Target != "ls" || ev.Entry.Seq != 1 {
			t.Errorf("event = %+v", ev)
		}
		line, _ := json.Marshal(ev)
		if !strings.Contains(string(line), `"entry":{"seq":1`) {
			t.Errorf("wire form = %s", line)
		}
	case <-time.After(time.Second):
		t.Fatal("no agent-activity event")
	}
	select {
	case ev := <-plain.ch:
		t.Errorf("a subscriber naming no types got %+v", ev)
	default:
	}
	resumed, _, err := h.subscribeFrom(eventFilter{types: map[string]bool{EventAgentActivity: true}}, 8, &resumePoint{bootID: h.bootID})
	if err != nil {
		t.Fatal(err)
	}
	defer h.unsubscribe(resumed)
	if len(resumed.preface) != 1 || resumed.preface[0].Type != EventGap || resumed.preface[0].Reason != GapNotRetained {
		t.Errorf("a resume over an activity event got %+v, want one not_retained gap", resumed.preface)
	}
}

// TestHostedPaneForwardKeepsActivity: a pane on another machine reports its
// activity with its state, and the owner keeps it. It names nothing on the
// far machine that would mean something else here.
func TestHostedPaneForwardKeepsActivity(t *testing.T) {
	if slices.Contains(hostedCallDropped, "activity") {
		t.Error("a hosted pane's forwarded report drops its activity")
	}
}

// TestRecapTestPatternsDefault: a daemon given no patterns reads the
// defaults, and one given some reads those.
func TestRecapTestPatternsDefault(t *testing.T) {
	d := &Daemon{}
	if got := d.recapTestPatterns(); !slices.Contains(got, "go test") {
		t.Errorf("defaults = %v", got)
	}
	d.SetRecapTestPatterns([]string{"just test"})
	if got := d.recapTestPatterns(); !slices.Equal(got, []string{"just test"}) {
		t.Errorf("patterns = %v", got)
	}
	d.SetRecapTestPatterns(nil)
	if got := d.recapTestPatterns(); !slices.Contains(got, "go test") {
		t.Errorf("after reset = %v", got)
	}
}
