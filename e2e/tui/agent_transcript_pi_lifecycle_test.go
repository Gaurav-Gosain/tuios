package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func piUpdate(t *testing.T, r *atRig, path, session, leaf string) {
	t.Helper()
	out := piHook(t, r, map[string]any{"hook_event_name": "transcript_update", "session_id": session, "transcript_path": path, "transcript_leaf": leaf})
	if out["applied"] != true || out["transcript_refused"] != nil {
		t.Fatalf("Pi metadata update was not accepted: %v", out)
	}
}

func piState(t *testing.T, r *atRig) map[string]any {
	t.Helper()
	res, err := r.ctl.call(t, "get-agent-state", map[string]any{"session": transcriptSession, "window": r.wins[0]})
	if err != nil {
		t.Fatal(err)
	}
	state := make(map[string]any)
	for _, key := range []string{"state", "source", "agent_state_at", "blocked_by", "message"} {
		if _, present := res[key]; !present {
			t.Fatalf("agent state omitted %q", key)
		}
		state[key] = res[key]
	}
	// Completion accounting is exposed by list-agents, not get-agent-state.
	agents, err := r.ctl.call(t, "list-agents", map[string]any{"session": transcriptSession})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range agents["agents"].([]any) {
		a := item.(map[string]any)
		if a["window_id"] == r.wins[0] {
			if _, present := a["completion_seq"]; !present {
				t.Fatal("agent list omitted completion_seq")
			}
			state["completion_seq"] = a["completion_seq"]
			return state
		}
	}
	t.Fatal("Pi pane was absent from list-agents")
	return state
}

func piWaitText(t *testing.T, r *atRig, want, absent string) map[string]any {
	t.Helper()
	until := time.Now().Add(uiTimeout)
	for {
		res, err := r.ctl.call(t, "agent-transcript", map[string]any{"session": transcriptSession, "window": r.wins[0], "human_nonce": r.nonce})
		b, _ := json.Marshal(res["entries"])
		if err == nil && strings.Contains(string(b), want) && (absent == "" || !strings.Contains(string(b), absent)) {
			r.art.call("agent-transcript", nil, res, nil)
			return res
		}
		if time.Now().After(until) {
			t.Fatalf("Pi conversation never showed %q without %q: %s %v", want, absent, b, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Removing the identity-only leaf transport or the branch-only event leaves
// this Inbox on a3 although Pi selected old, without changing a byte on disk.
func TestPiTranscriptFollowsSelectedBranch(t *testing.T) {
	r := newATRig(t, "pi-selected-branch.txt", 1)
	path := piFixture(t, r)
	piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": "pi-fixture", "transcript_path": path, "transcript_leaf": "a3", "busy": true})
	first := r.read(r.wins[0], nil)
	before := piState(t, r)
	stat, _ := os.Stat(path)
	term := startIn(t, r.base, startOpts{args: []string{"attach", transcriptSession}, cols: 160, rows: 50, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatal(err)
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "b is three.") }, uiTimeout); err != nil {
		t.Fatalf("initial Pi Inbox: %v\n%s", err, term.Snapshot())
	}
	sub := r.link.open(t, true)
	if _, err := sub.call(t, "subscribe", map[string]any{"session": transcriptSession, "types": []string{"transcript"}}); err != nil {
		t.Fatal(err)
	}
	piUpdate(t, r, path, "pi-fixture", "old")
	piTranscriptEvent(t, r, sub)
	res := r.read(r.wins[0], map[string]any{"after": first["cursor"]})
	b, _ := json.Marshal(res["entries"])
	if res["reset"] != true || !strings.Contains(string(b), "ABANDONED-BRANCH") || strings.Contains(string(b), "b is three.") {
		t.Fatalf("same-file branch selection did not reset the conversation: %v", res)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ABANDONED-BRANCH") && !strings.Contains(s.Text(), "b is three.")
	}, uiTimeout); err != nil {
		t.Fatalf("Pi Inbox kept the previous branch: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "pi-selected-branch")
	after, _ := os.Stat(path)
	if stat.Size() != after.Size() || !stat.ModTime().Equal(after.ModTime()) {
		t.Fatal("the branch-selection test changed the session file")
	}
	if !reflect.DeepEqual(before, piState(t, r)) {
		t.Fatal("metadata-only selection changed agent state, source, prompt or completion count")
	}
	// Rewinding before every root must clear the old cards, not select the
	// most recently appended branch by treating the empty string as absent.
	piUpdate(t, r, path, "pi-fixture", "")
	if got := r.read(r.wins[0], map[string]any{"after": res["cursor"]}); got["reset"] != true || len(atEntries(t, got)) != 0 {
		t.Fatalf("empty selected branch retained messages: %v", got)
	}
	piUpdate(t, r, path, "pi-fixture", "old")
	old := r.read(r.wins[0], nil)
	appendLines(t, path, `{"type":"message","id":"child","parentId":"old","timestamp":"2026-10-22T11:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"DESCENDANT-APPEND"}],"stopReason":"stop"}}`)
	piUpdate(t, r, path, "pi-fixture", "child")
	got := r.read(r.wins[0], map[string]any{"after": old["cursor"]})
	entries := atEntries(t, got)
	if got["reset"] != false || len(entries) != 1 || entries[0].Text != "DESCENDANT-APPEND" {
		t.Fatalf("a descendant append duplicated/reset the conversation: %v", got)
	}
	piUpdate(t, r, path, "pi-fixture", "pending")
	pending := r.read(r.wins[0], map[string]any{"after": got["cursor"]})
	if pending["reset"] != true || len(atEntries(t, pending)) != 0 {
		t.Fatalf("a leaf reported before persistence exposed another branch: %v", pending)
	}
	record := `{"type":"message","id":"pending","parentId":"child","timestamp":"2026-10-22T11:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"PENDING-NOW-COMPLETE"}],"stopReason":"stop"}}` + "\n"
	appendRaw(t, path, record[:30])
	if got := r.read(r.wins[0], nil); len(atEntries(t, got)) != 0 {
		t.Fatalf("an incomplete selected leaf exposed another branch: %v", got)
	}
	appendRaw(t, path, record[30:])
	piWaitText(t, r, "PENDING-NOW-COMPLETE", "b is three.")
}

// The installed generated extension drives the real hook and daemon. The fake
// Pi API supplies only lifecycle events, never a fake Tuios reply or decoder.
func TestPiTranscriptExtension(t *testing.T) {
	t.Run("the extension follows a session through a resume and ignores headless children", func(t *testing.T) {
		r := newATRig(t, "pi-transcript-lifecycle.txt", 1)
		path := piFixture(t, r)
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0])
		emit := func(name, file, session string, leaf any, mode string) {
			d.emit(t, map[string]any{"type": name, "sessionFile": file, "sessionId": session, "leaf": leaf, "mode": mode, "idle": true})
		}
		emit("session_start", path, "pi-fixture", "a3", "tui")
		piWaitText(t, r, "b is three.", "ABANDONED-BRANCH")
		emit("session_tree", path, "pi-fixture", "old", "tui")
		piWaitText(t, r, "ABANDONED-BRANCH", "b is three.")
		// Pi dispatches message_end before persistence. The driver changes the
		// leaf after the handler returns, as AgentSession does after appending.
		record := map[string]any{"type": "message", "id": "late", "parentId": "old", "timestamp": "2026-10-22T11:00:01Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "PERSISTED-MESSAGE"}}, "stopReason": "stop"}}
		d.emit(t, map[string]any{"type": "message_end", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "old", "persistLeaf": "late", "persistRecord": record, "mode": "tui"})
		piWaitText(t, r, "PERSISTED-MESSAGE", "b is three.")
		// A resume changes file and session together; late old-session metadata
		// may never take it back, including when the new session is at rest.
		other := filepath.Join(filepath.Dir(path), "2026-10-22_second.jsonl")
		text := `{"type":"session","version":3,"id":"second","timestamp":"2026-10-22T12:00:00Z","cwd":"/work/project"}` + "\n" +
			`{"type":"message","id":"second1","parentId":null,"timestamp":"2026-10-22T12:00:01Z","message":{"role":"user","content":"RESUMED-SESSION"}}` + "\n"
		if err := os.WriteFile(other, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		emit("session_start", other, "second", "second1", "tui")
		piWaitText(t, r, "RESUMED-SESSION", "PERSISTED-MESSAGE")
		piHook(t, r, map[string]any{"hook_event_name": "transcript_update", "session_id": "pi-fixture", "transcript_path": path, "transcript_leaf": "a3"})
		piWaitText(t, r, "RESUMED-SESSION", "b is three.")
		for _, mode := range []string{"rpc", "print", "json"} {
			emit("session_start", path, "headless", "old", mode)
			emit("session_tree", path, "headless", "old", mode)
		}
		// session_shutdown waits for pending child reports. Its acknowledgement
		// fences the ignored events before we inspect, rather than masking a
		// headless takeover with another successful TUI session_start.
		d.emit(t, map[string]any{"type": "session_shutdown", "ack": "headless-drained"})
		d.waitAck(t, "headless-drained")
		res := r.read(r.wins[0], nil)
		b, _ := json.Marshal(res["entries"])
		if !strings.Contains(string(b), "RESUMED-SESSION") || strings.Contains(string(b), "ABANDONED-BRANCH") {
			t.Fatalf("a headless child replaced the pane's transcript: %s", b)
		}
		emit("session_start", other, "second", "second1", "tui")
		piWaitText(t, r, "RESUMED-SESSION", "ABANDONED-BRANCH")
	})
	t.Run("a message is shown once it is persisted, not once its event fires", func(t *testing.T) {
		r := newATRig(t, "pi-actual-persistence.txt", 1)
		path := piFixture(t, r)
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0])
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "a3", "mode": "tui"})
		piWaitText(t, r, "b is three.", "ABANDONED-BRANCH")
		// A subsequent extension can await asynchronous work before Pi persists
		// this message. One setImmediate is NOT a post-persistence boundary.
		d.emit(t, map[string]any{"type": "message_end", "leaf": "a3", "persistLeaf": "afterasync", "persistenceDelay": 80,
			"persistRecord": map[string]any{"type": "message", "id": "afterasync", "parentId": "a3", "timestamp": "2026-10-22T13:00:00Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "AFTER-ASYNC-PERSISTENCE"}}, "stopReason": "stop"}}})
		piWaitText(t, r, "AFTER-ASYNC-PERSISTENCE", "ABANDONED-BRANCH")
	})
	t.Run("a custom message appended at rest is shown", func(t *testing.T) {
		r := newATRig(t, "pi-idle-custom.txt", 1)
		path := piFixture(t, r)
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0])
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "a3", "mode": "tui"})
		piWaitText(t, r, "b is three.", "ABANDONED-BRANCH")
		// _appendCustomMessage emits public events, not extension message_end.
		// Persisting this visible message must work without any lifecycle handler.
		d.emit(t, map[string]any{"type": "persist_only", "persistLeaf": "idlecustom", "persistRecord": map[string]any{"type": "custom_message", "id": "idlecustom", "parentId": "a3", "display": true, "content": "VISIBLE-IDLE-CUSTOM"}})
		piWaitText(t, r, "VISIBLE-IDLE-CUSTOM", "ABANDONED-BRANCH")
	})
	t.Run("a session with no file does not reuse another conversation", func(t *testing.T) {
		r := newATRig(t, "pi-transcript-ephemeral.txt", 1)
		path := piFixture(t, r)
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0])
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "a3", "mode": "tui"})
		piWaitText(t, r, "b is three.", "ABANDONED-BRANCH")
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": "", "sessionId": "ephemeral", "leaf": nil, "mode": "tui"})
		d.emit(t, map[string]any{"type": "session_shutdown", "ack": "ephemeral-drained"})
		d.waitAck(t, "ephemeral-drained")
		res, err := r.ctl.call(t, "agent-transcript", map[string]any{"session": transcriptSession, "window": r.wins[0], "human_nonce": r.nonce})
		r.art.call("agent-transcript", nil, res, err)
		if err == nil || err.Code != "no_transcript" {
			t.Fatalf("ephemeral Pi session reused another conversation's history: %v %v", res, err)
		}
	})
	// slowReporter stands in for the report command with one that takes a while
	// and keeps every report it receives, so a test can count what was sent.
	slowReporter := func(t *testing.T, delay string) (command string, count func() int) {
		capture := filepath.Join(t.TempDir(), "reports.jsonl")
		command = filepath.Join(t.TempDir(), "slow-report")
		// The driver runs in the pane, which does not inherit this process's
		// environment, so the script carries the capture path itself.
		if err := os.WriteFile(command, []byte("#!/bin/sh\ncat >> '"+capture+"'\nprintf '\\n' >> '"+capture+"'\nsleep "+delay+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return command, func() int {
			b, err := os.ReadFile(capture)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			return len(strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' }))
		}
	}
	t.Run("a slow daemon cannot hold up shutdown", func(t *testing.T) {
		r := newATRig(t, "pi-report-drain.txt", 1)
		path := piFixture(t, r)
		command, count := slowReporter(t, "0.3")
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0], command)
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "a3", "mode": "tui"})
		// State reports are never merged, so ten of them are a backlog of ten slow
		// children: three seconds of work for a drain that gives a second.
		for range 10 {
			d.emit(t, map[string]any{"type": "agent_start", "idle": false})
		}
		start := time.Now()
		d.emit(t, map[string]any{"type": "session_shutdown", "ack": "bounded-drain"})
		d.waitAck(t, "bounded-drain")
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("report backlog made Pi shutdown take %v, past the total drain budget", took)
		}
		r.art.add("shutdown drain", time.Since(start).String())
		// The bound is only worth something if reports were being sent.
		if n := count(); n < 2 {
			t.Fatalf("the slow daemon was sent %d reports, so the backlog was never built", n)
		}
	})
	t.Run("selections that supersede one another are merged", func(t *testing.T) {
		r := newATRig(t, "pi-report-merge.txt", 1)
		path := piFixture(t, r)
		command, count := slowReporter(t, "0.2")
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0], command)
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "a3", "mode": "tui"})
		// Only the newest of ten selections made while a report is out matters.
		for i := range 10 {
			d.emit(t, map[string]any{"type": "session_tree", "leaf": fmt.Sprintf("branch%d", i)})
		}
		time.Sleep(3 * time.Second)
		if n := count(); n < 2 || n > 3 {
			t.Fatalf("want the start and one merged selection, got %d reports", n)
		}
	})
	// Each report is a process, so the extension tells the daemon only what it
	// does not have: the end of a turn, or a repeated selection, spawns nothing.
	t.Run("a selection already reported spawns nothing", func(t *testing.T) {
		r := newATRig(t, "pi-ext-spawns.txt", 1)
		path := piFixture(t, r)
		log := filepath.Join(r.base, "spawns.log")
		wrapper := filepath.Join(t.TempDir(), "count-spawn")
		if err := os.WriteFile(wrapper, []byte("#!/bin/sh\necho spawn >> '"+log+"'\nexec '"+tuiosBin+"' \"$@\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		d := startPlugin(t, r.base, "pi", transcriptSession, r.wins[0], wrapper)
		spawns := func() int {
			b, _ := os.ReadFile(log)
			return strings.Count(string(b), "\n")
		}
		// settle waits until the extension has sent everything it is going to.
		settle := func() int {
			last := -1
			for {
				time.Sleep(300 * time.Millisecond)
				if n := spawns(); n == last {
					return n
				} else {
					last = n
				}
			}
		}
		d.emit(t, map[string]any{"type": "session_start", "sessionFile": path, "sessionId": "pi-fixture", "leaf": "a3", "mode": "tui"})
		start := settle()
		record := map[string]any{"type": "message", "id": "late", "parentId": "a3", "message": map[string]any{"role": "assistant", "content": "x"}}
		d.emit(t, map[string]any{"type": "persist_only", "persistLeaf": "late", "persistRecord": record})
		appended := settle()
		if appended != start+1 {
			t.Fatalf("an append reported %d times, want once", appended-start)
		}
		d.emit(t, map[string]any{"type": "turn_end"})
		d.emit(t, map[string]any{"type": "session_tree"})
		if got := settle(); got != appended {
			t.Fatalf("the entry the last report named was reported again %d times", got-appended)
		}
		d.emit(t, map[string]any{"type": "session_tree", "leaf": "old"})
		if got := settle(); got != appended+1 {
			t.Fatalf("a different selection reported %d times, want once", got-appended)
		}
	})
}
