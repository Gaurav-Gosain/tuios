package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// More of agent-transcript, on the rig TestPersonReadsAgentTranscript builds
// inline: a daemon, a session, the phone's link with a presence, and panes
// joined by the real agent-hook.

// atRig is a daemon with a session and the phone's link to it.
type atRig struct {
	t     *testing.T
	base  string
	art   *atArtifact
	link  *phoneLink
	ctl   *linkStream
	nonce string
	wins  []string
}

// newATRig starts a daemon with a session of n windows, and the phone's link
// with a presence.
func newATRig(t *testing.T, artifact string, n int) *atRig {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[hosts.phone]\nallow = [\"list\", \"write\", \"respond\"]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", transcriptSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for i := 1; i < n; i++ {
		if out, err := tuiosCLI(t, base, "new-window", "-s", transcriptSession, "--no-focus"); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
	}
	r := &atRig{t: t, base: base, art: &atArtifact{t: t, name: artifact}}
	t.Cleanup(r.art.save)
	r.link = startLinkAs(t, base, "phone")
	r.ctl = r.link.open(t, true)
	wins, verr := r.ctl.call(t, "list-windows", map[string]any{"session": transcriptSession})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	for _, w := range wins["windows"].([]any) {
		r.wins = append(r.wins, w.(map[string]any)["window_id"].(string))
	}
	if len(r.wins) != n {
		t.Fatalf("want %d windows, got %v", n, r.wins)
	}
	pres, verr := r.ctl.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence: %v", verr)
	}
	r.nonce = pres["human_nonce"].(string)
	return r
}

// join joins window win to the transcript at path with the real
// SessionStart hook.
func (r *atRig) join(win, path, sessionID string) {
	r.t.Helper()
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", transcriptSession, "--window", win)
	hook.Dir = workDirIn(r.t, r.base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(r.base, key))
	}
	payload, _ := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "session_id": sessionID, "transcript_path": path, "source": "startup"})
	hook.Stdin = strings.NewReader(string(payload))
	if out, err := hook.CombinedOutput(); err != nil {
		r.t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
}

// read calls agent-transcript for win and fails the test on an error.
func (r *atRig) read(win string, extra map[string]any) map[string]any {
	r.t.Helper()
	p := map[string]any{"session": transcriptSession, "window": win, "human_nonce": r.nonce}
	for k, v := range extra {
		p[k] = v
	}
	res, verr := r.ctl.call(r.t, "agent-transcript", p)
	r.art.call("agent-transcript", p, res, verr)
	if verr != nil {
		r.t.Fatalf("agent-transcript %v: %v", extra, verr)
	}
	return res
}

// atRecord is one Claude Code record with a text answer.
func atRecord(uuid, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": uuid, "timestamp": "2026-10-09T10:00:00.000Z", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stop_reason": "end_turn"},
	})
	return string(b)
}

// atEventPast waits for a transcript event about window whose cursor is none
// of old, and returns it. A join reads its file at once and may raise an
// event for what was already there; old skips those.
func atEventPast(t *testing.T, sub *linkStream, art *atArtifact, window string, old ...string) string {
	t.Helper()
	for {
		c := atEvent(t, sub, art, window)
		if !slices.Contains(old, c) {
			return c
		}
	}
}

// TestTranscriptWatchOutlivesItsDirectory deletes the directory of a joined
// transcript and makes it again while the daemon runs. The kernel drops the
// directory's watch with the directory. Before the fix the daemon still
// counted the directory as watched, so a new join there added no watch and
// no transcript event came for it, and the old join was deaf too.
//
// The artifact is transcript-watch.txt under artifactDir: every request and
// reply and every event.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md): on the watcher before the
// fix, and with the markLost call cut from TranscriptWatcher.run, the test
// fails at "no transcript event" for the new join.
func TestTranscriptWatchOutlivesItsDirectory(t *testing.T) {
	r := newATRig(t, "transcript-watch.txt", 2)
	winA, winB := r.wins[0], r.wins[1]
	dir := filepath.Join(r.base, "projects", "demo")
	pathA := filepath.Join(dir, "aaaa.jsonl")
	pathB := filepath.Join(dir, "bbbb.jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathA, []byte(atRecord("a1", "A-FIRST")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(winA, pathA, "aaaa")

	sub := r.link.open(t, true)
	ack, verr := sub.call(t, "subscribe", map[string]any{"session": transcriptSession, "types": []string{"transcript"}})
	if verr != nil || ack["type"] != "subscribed" {
		t.Fatalf("subscribe: %v %v", ack, verr)
	}
	// The watch works before the directory goes.
	before := r.read(winA, nil)["cursor"].(string)
	appendLines(t, pathA, atRecord("a2", "A-SECOND"))
	atEventPast(t, sub, r.art, winA, before)
	second := r.read(winA, nil)["cursor"].(string)

	// The directory goes and comes back.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A new join in the directory made again is watched.
	if err := os.WriteFile(pathB, []byte(atRecord("b1", "B-FIRST")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(winB, pathB, "bbbb")
	joined := r.read(winB, nil)["cursor"].(string)
	appendLines(t, pathB, atRecord("b2", "B-SECOND"))
	evB := atEventPast(t, sub, r.art, winB, joined)
	added := atEntries(t, r.read(winB, map[string]any{"after": joined}))
	if len(added) != 1 || added[0].Text != "B-SECOND" {
		t.Fatalf("the new join's append: %v", added)
	}
	// The old join is watched again, with the file back at its path.
	if err := os.WriteFile(pathA, []byte(atRecord("a3", "A-AGAIN")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendLines(t, pathA, atRecord("a4", "A-AGAIN-2"))
	evA := atEventPast(t, sub, r.art, winA, before, second)
	resA := r.read(winA, map[string]any{"after": evA})
	if resA["reset"] != false {
		t.Fatalf("the event's cursor does not fit the file made again: %v", resA)
	}
	got := atEntries(t, r.read(winA, nil))
	if len(got) != 2 || got[0].Text != "A-AGAIN" || got[1].Text != "A-AGAIN-2" {
		t.Fatalf("the old join after its directory came back: %v", got)
	}

	r.art.add("result", "event cursors: A "+evA+", B "+evB)
}

// atCallRecord is one Claude Code record that gives two entries: a text and
// a Bash call.
func atCallRecord(i int) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": fmt.Sprintf("p%d", i), "timestamp": "2026-10-09T10:00:00.000Z", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": fmt.Sprintf("STEP-%03d", i)},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%03d", i), "name": "Bash", "input": map[string]any{"command": fmt.Sprintf("echo %d", i)}},
		}},
	})
	return string(b)
}

// TestPersonPagesBackThroughATranscript reads a transcript from its newest
// page back to its first with before, at a limit that ends pages inside
// records that give two entries. The pages joined must be the whole
// conversation once, in order. The reply carries older on every read: empty
// when the page starts at the first entry, and on a read with after too.
//
// The artifact is transcript-pages.txt under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the filter that keeps only the entries before the cursor cut from
//     back, the test fails at "the pages joined".
//   - with the record at a cursor inside a record left out of back's window,
//     the test fails at "page 10 is empty".
func TestPersonPagesBackThroughATranscript(t *testing.T) {
	r := newATRig(t, "transcript-pages.txt", 1)
	win := r.wins[0]
	path := filepath.Join(r.base, "projects", "demo", "pages.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// A first record that gives no entry, as Claude Code writes one.
	lines := []string{`{"type":"file-history-snapshot","messageId":"m0","snapshot":{"trackedFileBackups":{}},"isSnapshotUpdate":false}`}
	const records = 40
	for i := range records {
		lines = append(lines, atCallRecord(i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.join(win, path, "pages")

	// The whole conversation in one read has nothing older.
	all := r.read(win, map[string]any{"limit": 1000})
	want := atEntries(t, all)
	if len(want) != 2*records || all["older"] != "" {
		t.Fatalf("one read of everything: want %d entries and no older, got %d and older %q", 2*records, len(want), all["older"])
	}
	var wantIDs []string
	for _, e := range want {
		wantIDs = append(wantIDs, e.ID)
	}

	// From the newest page back, 7 at a time.
	res := r.read(win, map[string]any{"limit": 7})
	end := res["cursor"].(string)
	var pages [][]atEntry
	for n := 0; ; n++ {
		if n > 2*records {
			t.Fatalf("paging back never reached the first entry")
		}
		page := atEntries(t, res)
		if len(page) == 0 {
			t.Fatalf("page %d is empty: %v", n, res)
		}
		if res["reset"] != false {
			t.Fatalf("page %d: reset: %v", n, res)
		}
		pages = append([][]atEntry{page}, pages...)
		older, _ := res["older"].(string)
		if older == "" {
			break
		}
		res = r.read(win, map[string]any{"before": older, "limit": 7})
		if res["cursor"] != older {
			t.Fatalf("a page read with before: want its cursor %s, got %v", older, res["cursor"])
		}
	}
	var gotIDs []string
	for _, p := range pages {
		for _, e := range p {
			gotIDs = append(gotIDs, e.ID)
		}
	}
	if strings.Join(gotIDs, " ") != strings.Join(wantIDs, " ") {
		t.Fatalf("the pages joined:\n got %v\nwant %v", gotIDs, wantIDs)
	}
	if first := pages[0][0]; first.Text != "STEP-000" {
		t.Fatalf("the first page starts with %+v", first)
	}
	// A call and its text sit on two pages: a page ended inside a record.
	split := false
	for _, p := range pages {
		if p[0].Kind == "tool_call" {
			split = true
		}
	}
	if !split {
		t.Fatalf("no page ended inside a record, so the test proves less than it says")
	}

	// A read with after carries older too: the entries before what it read.
	appendLines(t, path, atCallRecord(records))
	res = r.read(win, map[string]any{"after": end})
	added := atEntries(t, res)
	older, _ := res["older"].(string)
	if len(added) != 2 || added[0].Text != fmt.Sprintf("STEP-%03d", records) || older == "" {
		t.Fatalf("a read after the end: want the two new entries and an older cursor, got %v", res)
	}
	back := atEntries(t, r.read(win, map[string]any{"before": older, "limit": 2}))
	if len(back) != 2 || back[0].ID != wantIDs[len(wantIDs)-2] || back[1].ID != wantIDs[len(wantIDs)-1] {
		t.Fatalf("before the older of a read with after: got %v", back)
	}

	// A cursor that is not one starts over, and after with before is refused.
	res = r.read(win, map[string]any{"before": "t1.0000000000000000.0", "limit": 3})
	if res["reset"] != true || len(atEntries(t, res)) != 3 {
		t.Fatalf("a made-up before: want reset with the newest entries, got %v", res)
	}
	p := map[string]any{"session": transcriptSession, "window": win, "human_nonce": r.nonce, "after": end, "before": older}
	if _, verr := r.ctl.call(t, "agent-transcript", p); verr == nil || verr.Code != "invalid_params" {
		t.Fatalf("after with before: want invalid_params, got %v", verr)
	}
	r.art.add("result", fmt.Sprintf("%d pages, %d entries, in order", len(pages), len(gotIDs)))
}
