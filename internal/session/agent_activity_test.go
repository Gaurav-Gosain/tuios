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

func intp(n int) *int { return &n }

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

// TestShellCommandTargetIsCut: a shell command line reaches the ring cut to
// activityTextMax like every other target, although the shell's own record of
// it keeps up to shellCmdlineMax bytes, and a secret in it is masked.
func TestShellCommandTargetIsCut(t *testing.T) {
	store := newActivityStore(nil)
	s := &Session{ID: "sid", Name: "work"}
	store.add(s.ID, s.Name, "w", AgentActivityEntry{Kind: ActivityPrompt, Text: "go"}, true)
	long := "API_TOKEN=abc123def go test " + strings.Repeat("./pkg/x ", shellCmdlineMax/8)
	store.noteSessionEvent(s, SessionEvent{Type: EventCommandFinished, Window: "w", Cmdline: long, ExitCode: intp(0)})
	got, _, _ := store.read(s.ID, "w")
	if len(got) != 2 {
		t.Fatalf("ring = %+v", got)
	}
	if n := len(got[1].Target); n == 0 || n > activityTextMax {
		t.Errorf("command target is %d bytes, want 1 to %d", n, activityTextMax)
	}
	if strings.Contains(got[1].Target, "abc123def") {
		t.Errorf("command target kept a secret: %q", got[1].Target)
	}
}

// TestActivityForAClosedWindowLeavesNoRing: the window a report named can
// close between the report resolving it and its activity being added. The
// sink has already forgotten the window's ring by then, so the add must not
// leave a new one behind.
func TestActivityForAClosedWindowLeavesNoRing(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	d.recordAgentActivity(sess, "closed-window", &AgentActivityReport{Event: ActivityTool, Tool: "Bash", Target: "make"}, AgentStateWorking)
	if d.activity.has(sess.ID, "closed-window") {
		t.Error("activity for a closed window left a ring")
	}
	id := sess.GetState().Windows[0].ID
	d.recordAgentActivity(sess, id, &AgentActivityReport{Event: ActivityTool, Tool: "Bash", Target: "make"}, AgentStateWorking)
	if !d.activity.has(sess.ID, id) {
		t.Error("activity for a live window left no ring")
	}
}
