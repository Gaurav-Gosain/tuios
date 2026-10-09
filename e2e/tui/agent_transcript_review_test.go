package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The review fixes of agent-transcript, on the rig of
// agent_transcript_more_test.go. Each test names its negative controls,
// recorded in NEGATIVE_CONTROLS.md under "agent-transcript review fixes".

// atProjects is Claude Code's transcript folder under the test's home, the
// only folder a reported transcript_path is accepted under.
func atProjects(base string) string {
	return filepath.Join(xdgDir(base, "HOME"), ".claude", "projects")
}

// hookPayload is a SessionStart payload that names path.
func hookPayload(path, sessionID string) []byte {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "session_id": sessionID, "transcript_path": path, "source": "startup"})
	return b
}

// joinExplain runs the real SessionStart hook with --explain from outside
// every pane, and returns what it printed.
func (r *atRig) joinExplain(win, path, sessionID string) map[string]any {
	r.t.Helper()
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", transcriptSession, "--window", win, "--explain")
	hook.Dir = workDirIn(r.t, r.base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(r.base, key))
	}
	hook.Stdin = strings.NewReader(string(hookPayload(path, sessionID)))
	var stderr strings.Builder
	hook.Stderr = &stderr
	if err := hook.Run(); err != nil {
		r.t.Fatalf("agent-hook SessionStart: %v\n%s", err, stderr.String())
	}
	r.art.add("hook --explain for "+win, stderr.String())
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr.String())), &out); err != nil {
		r.t.Fatalf("decode the hook's --explain line %q: %v", stderr.String(), err)
	}
	return out
}

// noTranscript checks that win is not readable: no join, or no file to read.
func (r *atRig) noTranscript(win, why string) {
	r.t.Helper()
	p := map[string]any{"session": transcriptSession, "window": win, "human_nonce": r.nonce}
	res, verr := r.ctl.call(r.t, "agent-transcript", p)
	r.art.call("agent-transcript", p, res, verr)
	if verr == nil || verr.Code != "no_transcript" {
		r.t.Fatalf("%s: want no_transcript, got %v %v", why, res, verr)
	}
}

// TestTranscriptNonceScope holds agent-transcript to the nonce scope rule
// (docs/protocol.md, "Nonce scope"): a presence made for one session reads
// only that session's panes. It also checks the forbidden reply of a
// restricted connection, and that limit 0 is refused.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the humanNonceFor check cut from verbAgentTranscript, the test
//     fails at "a presence for session other read session convo".
//   - with agent-transcript made scopeOpen in verbScopes, the test fails at
//     "a restricted connection".
//   - with limit read as a plain int again, the test fails at "limit 0".
func TestTranscriptNonceScope(t *testing.T) {
	r := newATRig(t, "transcript-nonce-scope.txt", 1)
	win := r.wins[0]
	if out, err := tuiosCLI(t, r.base, "new", "other", "--detach"); err != nil {
		t.Fatalf("create the second session: %v\n%s", err, out)
	}
	path := filepath.Join(atProjects(r.base), "demo", "scope.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(atRecord("s1", "SCOPE-TEXT")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(win, path, "scope")

	read := func(s *linkStream, nonce any, extra map[string]any) (map[string]any, *spVerbErr) {
		t.Helper()
		p := map[string]any{"session": transcriptSession, "window": win, "human_nonce": nonce}
		for k, v := range extra {
			p[k] = v
		}
		res, verr := s.call(t, "agent-transcript", p)
		r.art.call("agent-transcript", p, res, verr)
		return res, verr
	}

	// A presence for the other session does not read this one.
	otherConn := r.link.open(t, true)
	other, verr := otherConn.call(t, "attach-presence", map[string]any{"session": "other"})
	if verr != nil {
		t.Fatalf("attach-presence for other: %v", verr)
	}
	if res, verr := read(otherConn, other["human_nonce"], nil); verr == nil || verr.Code != "not_human" || !strings.Contains(verr.Message, "another session") {
		t.Fatalf("a presence for session other read session convo: %v %v", res, verr)
	}
	// A presence for this session reads it.
	ownConn := r.link.open(t, true)
	own, verr := ownConn.call(t, "attach-presence", map[string]any{"session": transcriptSession})
	if verr != nil {
		t.Fatalf("attach-presence for convo: %v", verr)
	}
	res, verr := read(ownConn, own["human_nonce"], nil)
	if verr != nil {
		t.Fatalf("a presence for session convo did not read it: %v", verr)
	}
	if got := atEntries(t, res); len(got) != 1 || got[0].Text != "SCOPE-TEXT" {
		t.Fatalf("the read with the scoped presence: %v", got)
	}
	// A presence with no session reads it too (the rig's).
	if _, verr := read(r.ctl, r.nonce, nil); verr != nil {
		t.Fatalf("a presence with no session did not read: %v", verr)
	}

	// limit 0 is refused, and no limit is the default.
	if _, verr := read(r.ctl, r.nonce, map[string]any{"limit": 0}); verr == nil || verr.Code != "invalid_params" {
		t.Fatalf("limit 0: want invalid_params, got %v", verr)
	}
	if _, verr := read(r.ctl, r.nonce, map[string]any{"limit": 1}); verr != nil {
		t.Fatalf("limit 1: %v", verr)
	}

	// A restricted connection is refused with forbidden, after the same
	// connection read before it restricted itself.
	sock := filepath.Join(xdgDir(r.base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	local := dialVerbs(t, sock)
	lres, lerr := local.call("attach-presence", nil)
	if lerr != "" {
		t.Fatalf("a local attach-presence: %s", lerr)
	}
	params := map[string]any{"session": transcriptSession, "window": win, "human_nonce": lres["human_nonce"]}
	if _, lerr := local.call("agent-transcript", params); lerr != "" {
		t.Fatalf("the local presence did not read before it restricted: %s", lerr)
	}
	if _, lerr := local.call("restrict-connection", map[string]any{"scope": "all"}); lerr != "" {
		t.Fatalf("restrict-connection: %s", lerr)
	}
	if _, lerr := local.call("agent-transcript", params); lerr != "forbidden" {
		t.Fatalf("a restricted connection: want forbidden, got %q", lerr)
	}
	r.art.add("result", "scoped presence refused in another session, read in its own; restricted connection forbidden; limit 0 refused")
}

// TestTranscriptPathRules holds a reported transcript_path to the rules: a
// pane names a file only for its own pane, and only a regular file under
// the harness's transcript folder whose name matches the pattern. A refusal
// shows in the hook's --explain line. And a joined file swapped for a FIFO
// is refused at once: before the fix, the open waited for a writer, so the
// read hung.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the transcriptPathAllowed call cut from joinReportedTranscript,
//     the test fails at "a path outside the projects folder".
//   - with the own-pane check cut from joinReportedTranscript, the test
//     fails at "pane A named a transcript for pane B".
//   - with OpenRegular's O_NONBLOCK open and fstat replaced by os.Open in
//     transcriptview.Read, the test fails at "a FIFO in place of the
//     transcript" (the read times out).
func TestTranscriptPathRules(t *testing.T) {
	r := newATRig(t, "transcript-path-rules.txt", 2)
	winA, winB := r.wins[0], r.wins[1]
	projects := filepath.Join(atProjects(r.base), "demo")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(r.base, "elsewhere", "secret.jsonl")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(atRecord("o1", "OUTSIDE-TEXT")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := func(win, path, want, why string) {
		t.Helper()
		out := r.joinExplain(win, path, "x")
		got, _ := out["transcript_refused"].(string)
		if !strings.Contains(got, want) {
			t.Fatalf("%s: want transcript_refused with %q, got %v", why, want, out)
		}
		r.noTranscript(win, why)
	}
	refused(winA, outside, "not in the harness's transcript folder", "a path outside the projects folder")

	link := filepath.Join(projects, "link.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	refused(winA, link, "not in the harness's transcript folder", "a link in the projects folder that leads out")

	fifo := filepath.Join(projects, "pipe.jsonl")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	refused(winA, fifo, "not a regular file", "a FIFO in the projects folder")

	txt := filepath.Join(projects, "notes.txt")
	if err := os.WriteFile(txt, []byte(atRecord("t1", "TXT")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused(winA, txt, "pattern", "a file that does not match *.jsonl")

	// From inside pane A: a hook naming pane B is refused, and a hook naming
	// pane B from pane B joins it.
	good := filepath.Join(projects, "good.jsonl")
	if err := os.WriteFile(good, []byte(atRecord("g1", "GOOD-TEXT")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(r.base, "payload.json")
	if err := os.WriteFile(payload, hookPayload(good, "good"), 0o600); err != nil {
		t.Fatal(err)
	}
	inPane := func(from, target, marker string) map[string]any {
		t.Helper()
		out := filepath.Join(r.base, marker+".json")
		// The pane's environment is the daemon's, and it is set again so the
		// hook reaches this test's daemon whatever the pane's shell did.
		var envs []string
		for _, key := range xdgKeys {
			envs = append(envs, key+"="+xdgDir(r.base, key))
		}
		line := fmt.Sprintf("env %s %s agent-hook claude-code --session %s --window %s --explain < %s 2> %s; echo %s-$((40+2))\n", strings.Join(envs, " "), tuiosBin, transcriptSession, target, payload, out, marker)
		if _, verr := r.ctl.call(t, "send-text", map[string]any{"session": transcriptSession, "window": from, "text": line}); verr != nil {
			t.Fatalf("send-text: %v", verr)
		}
		atWaitScreen(t, r.ctl, from, marker+"-42")
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		r.art.add("hook --explain in pane "+from+" for "+target, string(b))
		var res map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &res); err != nil {
			t.Fatalf("decode the hook's --explain line %q: %v", b, err)
		}
		return res
	}
	if res := inPane(winA, winB, "AFORB"); !strings.Contains(fmt.Sprint(res["transcript_refused"]), "own pane") {
		t.Fatalf("pane A named a transcript for pane B: want transcript_refused, got %v", res)
	}
	r.noTranscript(winB, "pane A named a transcript for pane B")
	if res := inPane(winB, winB, "BFORB"); res["transcript_refused"] != nil {
		t.Fatalf("pane B naming its own transcript was refused: %v", res)
	}
	if got := atEntries(t, r.read(winB, nil)); len(got) != 1 || got[0].Text != "GOOD-TEXT" {
		t.Fatalf("pane B's own join: %v", got)
	}

	// The joined file is swapped for a FIFO. The read is refused at once,
	// and the daemon goes on answering.
	if err := os.Remove(good); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(good, 0o600); err != nil {
		t.Fatal(err)
	}
	r.noTranscript(winB, "a FIFO in place of the transcript")
	if _, verr := r.ctl.call(t, "list-windows", map[string]any{"session": transcriptSession}); verr != nil {
		t.Fatalf("the daemon after the FIFO: %v", verr)
	}
	r.art.add("result", "outside, link out, FIFO, wrong pattern and another pane's path refused; own pane joined; FIFO swap refused")
}

// TestTranscriptSharedFileWatch joins two windows to one file, then moves the
// first to another file. Before the fix, the move took every callback on the
// shared file with it, so the second window got no transcript event again.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md): with Unwatch deleting
// every callback of the path again, the test fails at "no transcript event".
func TestTranscriptSharedFileWatch(t *testing.T) {
	r := newATRig(t, "transcript-shared-watch.txt", 2)
	winA, winB := r.wins[0], r.wins[1]
	dir := filepath.Join(atProjects(r.base), "demo")
	shared := filepath.Join(dir, "shared.jsonl")
	moved := filepath.Join(dir, "moved.jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(atRecord("s1", "SHARED-1")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved, []byte(atRecord("m1", "MOVED-1")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(winA, shared, "shared")
	r.join(winB, shared, "shared")
	sub := r.link.open(t, true)
	if ack, verr := sub.call(t, "subscribe", map[string]any{"session": transcriptSession, "types": []string{"transcript"}}); verr != nil || ack["type"] != "subscribed" {
		t.Fatalf("subscribe: %v %v", ack, verr)
	}
	start := r.read(winB, nil)["cursor"].(string)

	// Window A leaves the shared file.
	r.join(winA, moved, "moved")
	if got := atEntries(t, r.read(winA, nil)); len(got) != 1 || got[0].Text != "MOVED-1" {
		t.Fatalf("window A after the move: %v", got)
	}
	appendLines(t, shared, atRecord("s2", "SHARED-2"))
	ev := atEventPast(t, sub, r.art, winB, start)
	added := atEntries(t, r.read(winB, map[string]any{"after": start}))
	if len(added) != 1 || added[0].Text != "SHARED-2" {
		t.Fatalf("window B's append after window A left: %v (event %s)", added, ev)
	}
	r.art.add("result", "window B still watched after window A left the shared file: event "+ev)
}

// atEditRecord is a record with one Edit call of file, from old to new.
func atEditRecord(id, file, old, new string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": id, "timestamp": "2026-10-09T10:00:00.000Z", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_" + id, "name": "Edit", "input": map[string]any{"file_path": file, "old_string": old, "new_string": new}},
		}},
	})
	return string(b)
}

// atWriteRecord is a record with one Write call of file.
func atWriteRecord(id, file, content string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": id, "timestamp": "2026-10-09T10:00:00.000Z", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_" + id, "name": "Write", "input": map[string]any{"file_path": file, "content": content}},
		}},
	})
	return string(b)
}

// atResultRecord is a record with one tool result.
func atResultRecord(id, toolID, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": id, "timestamp": "2026-10-09T10:00:01.000Z",
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": text},
		}},
	})
	return string(b)
}

// atLines is n lines named prefix-0 to prefix-(n-1).
func atLines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s-%04d\n", prefix, i)
	}
	return b.String()
}

// atFullDiff decodes the diffs of a reply with whole_replace and truncated.
type atFullDiff struct {
	File         string   `json:"file"`
	Added        int      `json:"added"`
	Removed      int      `json:"removed"`
	Truncated    bool     `json:"truncated"`
	WholeReplace bool     `json:"whole_replace"`
	Hunks        []atHunk `json:"hunks"`
}

func atFullDiffs(t *testing.T, res map[string]any) []atFullDiff {
	t.Helper()
	b, _ := json.Marshal(res["entries"])
	var out []struct {
		Diff *atFullDiff `json:"diff"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode entries: %v", err)
	}
	var diffs []atFullDiff
	for _, e := range out {
		if e.Diff != nil {
			diffs = append(diffs, *e.Diff)
		}
	}
	return diffs
}

// TestTranscriptDiffBudget reads three edits of 1500 changed lines each. One
// match of such an edit fills 1501×1501 of the 4,194,304 table cells a reply
// may use, so a reply matches one and shows the next as a whole replace. The
// diffs are built only for the entries on the page, newest first, so the
// newest is the one matched.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with every entry finished in decode, oldest first, the test fails at
//     "limit 1: the newest edit".
//   - with the cells not taken from the budget in lineDiff, the test fails
//     at "limit 2: the older edit".
func TestTranscriptDiffBudget(t *testing.T) {
	r := newATRig(t, "transcript-diff-budget.txt", 1)
	win := r.wins[0]
	path := filepath.Join(atProjects(r.base), "demo", "budget.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var recs []string
	for k := range 3 {
		recs = append(recs, atEditRecord(fmt.Sprintf("e%d", k), fmt.Sprintf("/work/demo/big%d.txt", k), atLines(fmt.Sprintf("old%d", k), 1500), atLines(fmt.Sprintf("new%d", k), 1500)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(recs, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(win, path, "budget")

	diffs := atFullDiffs(t, r.read(win, map[string]any{"limit": 1}))
	if len(diffs) != 1 || diffs[0].File != "/work/demo/big2.txt" || diffs[0].WholeReplace || diffs[0].Added != 1500 || diffs[0].Removed != 1500 {
		t.Fatalf("limit 1: the newest edit: want it matched, got %+v", diffs)
	}
	diffs = atFullDiffs(t, r.read(win, map[string]any{"limit": 2}))
	if len(diffs) != 2 || diffs[1].File != "/work/demo/big2.txt" || diffs[1].WholeReplace {
		t.Fatalf("limit 2: the newest edit: want it matched, got %+v", diffs)
	}
	older := diffs[0]
	if older.File != "/work/demo/big1.txt" || !older.WholeReplace || older.Added != 1500 || older.Removed != 1500 || !older.Truncated || len(older.Hunks) != 1 {
		t.Fatalf("limit 2: the older edit: want a whole replace of 1500 lines, got file %s whole %v added %d removed %d truncated %v hunks %d",
			older.File, older.WholeReplace, older.Added, older.Removed, older.Truncated, len(older.Hunks))
	}
	text := atDiffText(older.Hunks[0])
	if !strings.HasPrefix(text, "-old1-0000|") || !strings.Contains(text, "|-old1-0199|+new1-0000|") || !strings.HasSuffix(text, "|+new1-0199") {
		t.Fatalf("the whole replace: want 200 removed then 200 added lines, got %.200s ... %.200s", text, text[max(0, len(text)-200):])
	}
	r.art.add("result", "limit 1: newest matched; limit 2: newest matched, older whole_replace with 200+200 lines of 1500+1500")
}

// TestTranscriptMasksMultiLineSecrets reads a Write of an .env file, a Write
// of a private key, an Edit inside a key's body with its BEGIN line out of
// view, and a command result that prints a key. None of the secret lines
// may reach the reply. The keys, the BEGIN and END lines and the ordinary
// lines stay.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the CleanLines call cut from addHunk, the test fails at "a diff
//     carries a secret".
//   - with the maskSecretLines call cut from transcriptText, the test fails
//     at "the Bash result carries a secret".
func TestTranscriptMasksMultiLineSecrets(t *testing.T) {
	r := newATRig(t, "transcript-secrets.txt", 1)
	win := r.wins[0]
	path := filepath.Join(atProjects(r.base), "demo", "secrets.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body1 := "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW"
	body2 := "QyNTUxOQAAACBPEMBODYMARKERxyzAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" + body1 + "\n" + body2 + "\n-----END OPENSSH PRIVATE KEY-----\n"
	env := "STRIPE_LIVE=sk-ENVMARKER-ONE\nDATABASE_URL=postgres://u:ENVMARKER-TWO@db/app\n"
	edBody := "AAAAC3NzaC1lZDI1NTE5AAAAIEDITMARKERoneAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\nAAAAC3NzaC1lZDI1NTE5AAAAIEDITMARKERtwoAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"
	edNew := "AAAAC3NzaC1lZDI1NTE5AAAAIEDITMARKERoneAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\nAAAAC3NzaC1lZDI1NTE5AAAAIEDITMARKERthrAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"
	recs := []string{
		atWriteRecord("w1", "/work/demo/.env", env),
		atWriteRecord("w2", "/work/demo/id_ed25519", key),
		atEditRecord("w3", "/work/demo/id_rsa", edBody, edNew),
		atEditRecord("w4", "/work/demo/main.py", "x = 1\ny = 2\n", "x = 1\ny = 3\n"),
		atBashRecord("w5", "cat id_ed25519"),
		atResultRecord("w6", "toolu_w5", "here it is:\n"+key),
	}
	if err := os.WriteFile(path, []byte(strings.Join(recs, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(win, path, "secrets")
	res := r.read(win, nil)
	raw, _ := json.Marshal(res["entries"])
	entries := atEntries(t, res)
	var result atEntry
	for _, e := range entries {
		if e.Kind == "tool_result" {
			result = e
		}
	}
	for _, bad := range []string{"PEMBODYMARKER", "EDITMARKER"} {
		if strings.Contains(result.Text, bad) {
			t.Fatalf("the Bash result carries a secret %q: %q", bad, result.Text)
		}
	}
	for _, bad := range []string{"ENVMARKER", "PEMBODYMARKER", "EDITMARKER", body1} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("a diff carries a secret %q: %s", bad, raw)
		}
	}
	for _, good := range []string{"STRIPE_LIVE=[redacted]", "DATABASE_URL=[redacted]", "-----BEGIN OPENSSH PRIVATE KEY-----", "-----END OPENSSH PRIVATE KEY-----", "y = 3", "here it is:"} {
		if !strings.Contains(string(raw), good) {
			t.Fatalf("the reply lost %q, which is not a secret: %s", good, raw)
		}
	}
	r.art.add("result", string(raw))
}

// atBashRecord is a record with one Bash call.
func atBashRecord(id, command string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": id, "timestamp": "2026-10-09T10:00:00.000Z", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_" + id, "name": "Bash", "input": map[string]any{"command": command}},
		}},
	})
	return string(b)
}
