package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSessionFileJoin checks the join Amp's plugin makes by naming its thread
// id. The id is a file name under the transcript folder, so it is held to
// the rules of a reported path and to more: the file must exist, the report
// must be applied, and a report with no connection (herdr's socket, which
// any client writes to) names no file.
func TestSessionFileJoin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	threads := filepath.Join(home, ".local", "share", "amp", "threads")
	if err := os.MkdirAll(threads, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "T-00000000-0000-7000-8000-000000000001"
	if err := os.WriteFile(filepath.Join(threads, id+".json"), []byte(`{"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	// Each report goes to a pane of its own: a pane holds to the first
	// session id reported for it.
	n := 0
	pane := func() (string, func() string) {
		n++
		name := "work" + string(rune('a'+n))
		sess := makeSessionWithWindow(t, d, name)
		win := sess.GetState().Windows[0].ID
		return name, func() string {
			path, _, _, _, ok := sess.transcriptJoinOf(win)
			if !ok {
				return ""
			}
			return path
		}
	}
	report := func(name, sid string) map[string]any {
		return map[string]any{"session": name, "window": "0", "state": "working", "harness": "amp", "agent_session_id": sid}
	}

	// herdr reports through the verb with no connection.
	name, joined := pane()
	raw, _ := json.Marshal(report(name, id))
	if _, verr := d.verbSetAgentState(nil, raw); verr != nil {
		t.Fatal(verr.Message)
	}
	if got := joined(); got != "" {
		t.Fatalf("a report with no connection joined %s", got)
	}

	for _, sid := range []string{"T-missing", "../T-x", "..", "T-a/b"} {
		name, joined := pane()
		if res := result(t, callVerb(t, c, "set-agent-state", report(name, sid))); res["applied"] != true {
			t.Fatalf("the report of session id %q was not applied: %v", sid, res)
		}
		if got := joined(); got != "" {
			t.Fatalf("session id %q joined %s", sid, got)
		}
	}
	// A pane that holds another session refuses the report, and a refused
	// report joins nothing.
	name, joined = pane()
	result(t, callVerb(t, c, "set-agent-state", report(name, "T-other")))
	if res := result(t, callVerb(t, c, "set-agent-state", report(name, id))); res["applied"] == true {
		t.Fatalf("a pane holding another session applied the report: %v", res)
	}
	if got := joined(); got != "" {
		t.Fatalf("a refused report joined %s", got)
	}

	name, joined = pane()
	result(t, callVerb(t, c, "set-agent-state", report(name, id)))
	want := filepath.Join(threads, id+".json")
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	if got := joined(); got != want {
		t.Fatalf("joined %q, want %q", got, want)
	}
}

// sessionFolderHarness keeps each session's transcript at a fixed path in a
// folder named by the session id, and its hook reports only the id.
const sessionFolderHarness = `schema_version = 1
id = "folder-agent"
[detect]
comm = ["folder-agent"]
[transcript]
reader       = "amp-thread"
dir          = "{home}/.folder-agent/sessions"
glob         = "s-*/logs/transcript.json"
session_file = "{session_id}/logs/transcript.json"
`

// TestSessionFolderJoin checks the join of a harness whose integration
// reports identity only, through set-agent-session, and whose session_file
// names a file below a folder per session. A report that comes before the
// harness writes its file joins nothing, and the same report afterwards
// joins it, so a new conversation does not stay without a thread. A new
// session id whose file is not written yet, or whose file is refused, drops
// the previous conversation's join, on either verb, rather than keep showing
// it. A set-agent-state that names no harness joins and drops for the pane's.
func TestSessionFolderJoin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifests := t.TempDir()
	if err := os.WriteFile(filepath.Join(manifests, "folder-agent.toml"), []byte(sessionFolderHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_HARNESS_DIR", manifests)
	sessions := filepath.Join(home, ".folder-agent", "sessions")
	write := func(sid string) string {
		t.Helper()
		logs := filepath.Join(sessions, sid, "logs")
		if err := os.MkdirAll(logs, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(logs, "transcript.json")
		if err := os.WriteFile(path, []byte(`{"messages":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return path
	}
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	pane := func(name string) func() string {
		sess := makeSessionWithWindow(t, d, name)
		win := sess.GetState().Windows[0].ID
		return func() string {
			path, _, _, _, ok := sess.transcriptJoinOf(win)
			if !ok {
				return ""
			}
			return path
		}
	}
	identity := func(name, sid string) map[string]any {
		t.Helper()
		res := result(t, callVerb(t, c, "set-agent-session", map[string]any{"session": name, "window": "0", "harness": "folder-agent", "agent_session_id": sid}))
		if res["applied"] != true {
			t.Fatalf("set-agent-session of %q was not applied: %v", sid, res)
		}
		return res
	}

	joined := pane("worka")
	identity("worka", "s-1")
	if got := joined(); got != "" {
		t.Fatalf("session id %q joined %s before its file was written", "s-1", got)
	}
	want := write("s-1")
	identity("worka", "s-1")
	if got := joined(); got != want {
		t.Fatalf("a report after the file was written joined %q, want %q", got, want)
	}
	identity("worka", "s-2")
	if got := joined(); got != "" {
		t.Fatalf("a new session id kept the previous conversation's transcript %s", got)
	}
	want = write("s-2")
	identity("worka", "s-2")
	if got := joined(); got != want {
		t.Fatalf("the new session joined %q, want %q", got, want)
	}

	// The glob, not session_file, says which ids name a session folder, and
	// a file it does not match is refused in the reply.
	write("x-1")
	joined = pane("workb")
	if res := identity("workb", "x-1"); res["transcript_refused"] == nil {
		t.Fatalf("a file the glob does not match was not refused: %v", res)
	}
	if got := joined(); got != "" {
		t.Fatalf("session id %q joined %s", "x-1", got)
	}
	// A new id whose file is refused drops the join the pane had.
	joined = pane("workd")
	identity("workd", "s-1")
	if got := joined(); got == "" {
		t.Fatal("session id s-1 joined nothing")
	}
	if res := identity("workd", "x-1"); res["transcript_refused"] == nil {
		t.Fatalf("a file the glob does not match was not refused: %v", res)
	}
	if got := joined(); got != "" {
		t.Fatalf("a new session id whose file was refused kept the previous conversation's transcript %s", got)
	}

	// set-agent-state drops the previous join the same way.
	joined = pane("workc")
	state := func(sid string, named bool) {
		t.Helper()
		params := map[string]any{"session": "workc", "window": "0", "state": "idle", "agent_session_id": sid}
		if named {
			params["harness"] = "folder-agent"
		}
		res := result(t, callVerb(t, c, "set-agent-state", params))
		if res["applied"] != true {
			t.Fatalf("set-agent-state of %q was not applied: %v", sid, res)
		}
	}
	want = write("s-3")
	state("s-3", true)
	if got := joined(); got != want {
		t.Fatalf("set-agent-state joined %q, want %q", got, want)
	}
	state("s-4", true)
	if got := joined(); got != "" {
		t.Fatalf("set-agent-state with a new session id kept the previous conversation's transcript %s", got)
	}
	// A report with no harness is about the pane's.
	want = write("s-5")
	state("s-5", false)
	if got := joined(); got != want {
		t.Fatalf("set-agent-state with no harness joined %q, want %q", got, want)
	}
	state("s-6", false)
	if got := joined(); got != "" {
		t.Fatalf("set-agent-state with no harness and a new session id kept the previous conversation's transcript %s", got)
	}
}

// TestNewSessionDropsJoin checks which bundled harnesses lose a pane's join
// when a report changes the session id and joins nothing: a tree transcript
// (Pi), a session_file (Amp) and a by_session store (opencode), each joined
// only by what a hook names, and not one the daemon can find by search.
func TestNewSessionDropsJoin(t *testing.T) {
	d, _ := startTestDaemon(t)
	for id, want := range map[string]bool{"pi": true, "amp": true, "opencode": true, "claude-code": false, "codex": false, "": false} {
		if got := d.newSessionDropsJoin(id); got != want {
			t.Errorf("newSessionDropsJoin(%q) = %v, want %v", id, got, want)
		}
	}
}

// storeHarness keeps every session in one store and names a session by its
// id, as opencode does.
const storeHarness = `schema_version = 1
id = "store-agent"
[detect]
comm = ["store-agent"]
[transcript]
reader     = "opencode-service"
dir        = "{home}/.store-agent"
glob       = "service.json"
by_session = true
`

// TestStoreSessionDrop checks that a by_session pane joined to one session
// of its store loses that join when a new session id is refused, rather than
// keep showing the previous session.
func TestStoreSessionDrop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifests := t.TempDir()
	if err := os.WriteFile(filepath.Join(manifests, "store-agent.toml"), []byte(storeHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_HARNESS_DIR", manifests)
	if err := os.MkdirAll(filepath.Join(home, ".store-agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".store-agent", "service.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	sess := makeSessionWithWindow(t, d, "store")
	win := sess.GetState().Windows[0].ID
	identity := func(sid string) map[string]any {
		t.Helper()
		res := result(t, callVerb(t, c, "set-agent-session", map[string]any{"session": "store", "window": "0", "harness": "store-agent", "agent_session_id": sid}))
		if res["applied"] != true {
			t.Fatalf("set-agent-session of %q was not applied: %v", sid, res)
		}
		return res
	}
	identity("ses_1")
	if _, _, _, session, ok := sess.transcriptJoinOf(win); !ok || session != "ses_1" {
		t.Fatalf("session ses_1 joined %q (joined %v)", session, ok)
	}
	if res := identity("ses.2"); res["transcript_refused"] == nil {
		t.Fatalf("a session id a store does not name was not refused: %v", res)
	}
	if _, _, _, session, ok := sess.transcriptJoinOf(win); ok {
		t.Fatalf("a refused new session id kept the previous session %q", session)
	}
}

// jsonlFolderHarness is sessionFolderHarness with a transcript of JSONL
// records, which raises the transcript event only for a whole record.
const jsonlFolderHarness = `schema_version = 1
id = "jsonl-agent"
[detect]
comm = ["jsonl-agent"]
[transcript]
reader       = "cursor-jsonl"
dir          = "{home}/.jsonl-agent/sessions"
glob         = "s-*/transcript.jsonl"
session_file = "{session_id}/transcript.jsonl"
`

// TestSessionChangeRaisesTranscript checks that a client showing a pane's
// thread hears when a new session id takes the previous conversation away:
// the join moved to a file with no whole record yet, which raises no event
// of its own, or dropped because the new file is not there.
func TestSessionChangeRaisesTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifests := t.TempDir()
	if err := os.WriteFile(filepath.Join(manifests, "jsonl-agent.toml"), []byte(jsonlFolderHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_HARNESS_DIR", manifests)
	write := func(sid, body string) {
		t.Helper()
		dir := filepath.Join(home, ".jsonl-agent", "sessions", sid)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "events")
	win := sess.GetState().Windows[0].ID
	sub := dialVerb(t, sp)
	result(t, callP(sub, t, "subscribe", map[string]any{"session": "events", "types": []string{EventTranscript}}))
	c := dialVerb(t, sp)
	identity := func(sid string) {
		t.Helper()
		res := result(t, callVerb(t, c, "set-agent-session", map[string]any{"session": "events", "window": "0", "harness": "jsonl-agent", "agent_session_id": sid}))
		if res["applied"] != true {
			t.Fatalf("set-agent-session of %q was not applied: %v", sid, res)
		}
	}
	transcriptEvent := func(after string) {
		t.Helper()
		if ev := readEvent(t, sub); ev["type"] != EventTranscript || ev["window"] != win {
			t.Fatalf("after %s: event %v, want transcript for window %s", after, ev, win)
		}
	}

	write("s-1", `{"role":"user","message":{"content":[{"type":"text","text":"hi"}]}}`+"\n")
	identity("s-1")
	transcriptEvent("joining a file with a record")
	write("s-2", "")
	identity("s-2")
	transcriptEvent("moving to an empty file")
	identity("s-3")
	transcriptEvent("a session id that names no file yet")
	if path, _, _, _, ok := sess.transcriptJoinOf(win); ok {
		t.Fatalf("a session id with no file kept %s", path)
	}
}
