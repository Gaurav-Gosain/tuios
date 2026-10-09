package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The person's view of an agent's conversation, end to end: a real daemon, a
// pane joined to a Claude Code transcript by the real hook, and the phone's
// path into the daemon, `tuios stdio-proxy`, reading it with agent-transcript.
//
// The transcript is a fixture in testdata/transcript, written the way Claude
// Code writes one: a prompt as a string, thinking and text blocks, Read, Edit
// (with the structuredPatch Claude Code adds to the result), Write of a new
// file, TodoWrite, a failing Bash call with a secret in it, a subagent's
// record, a meta record and an ExitPlanMode plan. The test copies it, appends
// to it as an agent would, and replaces it.
//
// It checks the entries and their diffs, plans and todos, the folded status of
// each call, what is masked and stripped, the cursor across appends, a reset
// for a cursor the file no longer matches, the limit, and the transcript event
// that says when to read again. And it checks who may read: not without a
// nonce, not from inside a pane even with the person's nonce, not over a link
// stream the hub did not vouch for, and not over a link without respond.
//
// The artifact is agent-transcript.txt under artifactDir: every request and
// reply, the events, and what the pane printed.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the nonce check in verbAgentTranscript cut, the read without a
//     nonce is served and the test fails at "a read without a nonce".
//   - with agent-transcript mapped to list instead of respond in
//     verbCapabilities, the test fails at "a link without respond".
//   - with the noteTranscriptGrowth call cut from readAgentTranscript, the
//     test fails at "no transcript event".
//   - with foldResults cut from forward, the test fails at "the appended
//     call".
//   - with the file id left out of parseCursor, the test fails at "a
//     made-up cursor".
//   - with the first record left out of fileID, the test fails at "a
//     replaced file".
//   - with the secret mask cut from transcriptText, the test fails at the
//     Bash call's target.
//   - with ansi.Strip cut from transcriptText, the test fails at the Bash
//     result's colours.

// transcriptSession is the session the test reads.
const transcriptSession = "convo"

// atMadeUpNonce is a nonce no daemon issued.
const atMadeUpNonce = "00112233445566778899aabbccddeeff"

// TestPersonReadsAgentTranscript is the whole path above.
func TestPersonReadsAgentTranscript(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	cfg := "[hosts.phone]\nallow = [\"list\", \"write\", \"respond\"]\n\n" +
		"[hosts.viewer]\nallow = [\"list\", \"write\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", transcriptSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	art := &atArtifact{t: t}
	defer art.save()

	fixture, err := os.ReadFile(filepath.Join("testdata", "transcript", "claude-session.jsonl"))
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	path := filepath.Join(base, "projects", "demo", "7c1d2e3f.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	link := startLinkAs(t, base, "phone")
	ctl := link.open(t, true)
	read := func(c *linkStream, params map[string]any) (map[string]any, *spVerbErr) {
		t.Helper()
		res, verr := c.call(t, "agent-transcript", params)
		art.call("agent-transcript", params, res, verr)
		return res, verr
	}
	wins, verr := ctl.call(t, "list-windows", map[string]any{"session": transcriptSession})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	win := wins["windows"].([]any)[0].(map[string]any)["window_id"].(string)
	pres, verr := ctl.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence: %v", verr)
	}
	nonce := pres["human_nonce"].(string)
	params := func(extra map[string]any) map[string]any {
		p := map[string]any{"session": transcriptSession, "window": win, "human_nonce": nonce}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}

	// --- Before the join there is nothing to read.
	if _, verr := read(ctl, params(nil)); verr == nil || verr.Code != "no_transcript" {
		t.Fatalf("a pane with no join: want no_transcript, got %v", verr)
	}

	// --- The real hook joins the pane, as Claude Code's SessionStart does.
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", transcriptSession, "--window", win)
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	payload, _ := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "session_id": "7c1d2e3f", "transcript_path": path, "source": "startup"})
	hook.Stdin = strings.NewReader(string(payload))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}

	// --- Who may read. Each refusal comes before the read that works.
	noNonce := params(nil)
	delete(noNonce, "human_nonce")
	if _, verr := read(ctl, noNonce); verr == nil || verr.Code != "not_human" {
		t.Fatalf("a read without a nonce: want not_human, got %v", verr)
	}
	if _, verr := read(ctl, params(map[string]any{"human_nonce": atMadeUpNonce})); verr == nil || verr.Code != "not_human" {
		t.Fatalf("a made-up nonce: want not_human, got %v", verr)
	}
	plain := link.open(t, false)
	if _, verr := plain.call(t, "attach-presence", nil); verr == nil || verr.Code != "forbidden" {
		t.Fatalf("attach-presence on a stream the hub did not vouch for: want forbidden, got %v", verr)
	}
	if _, verr := read(plain, params(nil)); verr == nil || verr.Code != "not_human" {
		t.Fatalf("the person's nonce on a stream the hub did not vouch for: want not_human, got %v", verr)
	}
	plain.close()

	viewer := startLinkAs(t, base, "viewer")
	vs := viewer.open(t, true)
	vpres, verr := vs.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence on the viewer link: %v", verr)
	}
	vp := params(map[string]any{"human_nonce": vpres["human_nonce"]})
	if _, verr := read(vs, vp); verr == nil || verr.Code != "forbidden" || !strings.Contains(verr.Message, "respond") {
		t.Fatalf("a link without respond: want forbidden naming respond, got %v", verr)
	}
	vs.close()

	// From inside a pane: its own presence is refused, and the person's
	// nonce does not work there either. A local presence is held open for
	// the length of the check, so the nonce it hands the pane is live.
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	local := dialVerbs(t, sock)
	lres, lerr := local.call("attach-presence", nil)
	if lerr != "" {
		t.Fatalf("a local attach-presence: %s", lerr)
	}
	localNonce := lres["human_nonce"].(string)
	if _, lerr := local.call("agent-transcript", params(map[string]any{"human_nonce": localNonce})); lerr != "" {
		t.Fatalf("the local presence did not read: %s", lerr)
	}
	helper := filepath.Join(t.TempDir(), "humancall")
	if out, err := exec.Command("go", "build", "-o", helper, "./testdata/humancall").CombinedOutput(); err != nil {
		t.Fatalf("build humancall: %v\n%s", err, out)
	}
	line := fmt.Sprintf("%s %s %s %s; %s %s %s %s %s; echo HC-$((40+2))\n", helper, sock, transcriptSession, win, helper, sock, transcriptSession, win, localNonce)
	if _, verr := ctl.call(t, "send-text", map[string]any{"session": transcriptSession, "window": win, "text": line}); verr != nil {
		t.Fatalf("send-text: %v", verr)
	}
	screen := atWaitScreen(t, ctl, win, "HC-42")
	art.add("the pane's own calls", screen)
	if strings.Count(screen, "PRESENCE=forbidden") != 2 || strings.Count(screen, "READ=not_human") != 2 || strings.Contains(screen, "READ=ok") {
		t.Fatalf("a pane read the person's conversation, or was not refused as it should be:\n%s", screen)
	}
	local.close()

	// --- The read that works: the newest entries of the whole fixture.
	res, verr := read(ctl, params(nil))
	if verr != nil {
		t.Fatalf("agent-transcript with the presence nonce: %v", verr)
	}
	if res["type"] != "agent_transcript" || res["harness"] != "claude-code" || res["window"] != win || res["untrusted"] != true || res["reset"] != false {
		t.Fatalf("the reply header: %v", res)
	}
	entries := atEntries(t, res)
	checkFixtureEntries(t, entries)
	cursor := res["cursor"].(string)
	start := cursor

	// The limit keeps the newest.
	res, verr = read(ctl, params(map[string]any{"limit": 3}))
	if verr != nil {
		t.Fatal(verr)
	}
	if got := atEntries(t, res); len(got) != 3 || got[2].ID != entries[len(entries)-1].ID {
		t.Fatalf("limit 3: want the newest 3 entries, got %v", got)
	}
	if _, verr := read(ctl, params(map[string]any{"limit": 1001})); verr == nil || verr.Code != "invalid_params" {
		t.Fatalf("limit 1001: want invalid_params, got %v", verr)
	}

	// Nothing after the cursor yet.
	res, verr = read(ctl, params(map[string]any{"after": cursor}))
	if verr != nil || len(atEntries(t, res)) != 0 || res["cursor"] != cursor || res["reset"] != false {
		t.Fatalf("a read at the end: want no entries and the same cursor, got %v %v", res, verr)
	}

	// --- The event, and the cursor across appends. The phone subscribes
	// on its own stream, as it does in the app.
	sub := link.open(t, true)
	ack, verr := sub.call(t, "subscribe", map[string]any{"session": transcriptSession, "types": []string{"transcript"}})
	if verr != nil || ack["type"] != "subscribed" {
		t.Fatalf("subscribe: %v %v", ack, verr)
	}
	appendLines(t, path,
		`{"type":"assistant","uuid":"b1","timestamp":"2026-10-09T10:01:00.000Z","isSidechain":false,"message":{"role":"assistant","content":[{"type":"text","text":"Running the tests again."},{"type":"tool_use","id":"toolu_bash2","name":"Bash","input":{"command":"go test ./api/..."}}],"stop_reason":"tool_use"}}`,
	)
	// Half of the next record: the cursor must stop before it.
	half := `{"type":"user","uuid":"b2","timestamp":"2026-10-09T10:01:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash2","content":"ok  api 0.02s"}]}}`
	appendRaw(t, path, half[:40])
	ev := atEvent(t, sub, art, win)
	res, verr = read(ctl, params(map[string]any{"after": cursor}))
	if verr != nil {
		t.Fatal(verr)
	}
	added := atEntries(t, res)
	if len(added) != 2 || added[0].Kind != "text" || added[1].Kind != "tool_call" || added[1].Tool != "Bash" || added[1].Status != "running" || added[1].ToolID != "toolu_bash2" {
		t.Fatalf("the appended call: want the text and a running Bash call, got %v", added)
	}
	if res["cursor"] != ev || res["reset"] != false || res["more"] != false {
		t.Fatalf("the cursor after the append: want the event's %v, got %v", ev, res)
	}
	cursor = res["cursor"].(string)
	appendRaw(t, path, half[40:]+"\n")
	ev = atEvent(t, sub, art, win)
	res, verr = read(ctl, params(map[string]any{"after": cursor}))
	if verr != nil {
		t.Fatal(verr)
	}
	added = atEntries(t, res)
	if len(added) != 1 || added[0].Kind != "tool_result" || added[0].ToolID != "toolu_bash2" || added[0].Status != "ok" || added[0].Text != "ok  api 0.02s" || res["cursor"] != ev {
		t.Fatalf("the result of the appended call: got %v (cursor %v, event %v)", added, res["cursor"], ev)
	}
	cursor = res["cursor"].(string)

	// A limit after a cursor ends a page inside a record that gives two
	// entries, and the next page goes on from there with nothing lost.
	var walked []string
	for at, pages := start, 0; ; pages++ {
		if pages > 5 {
			t.Fatalf("limit 1 after a cursor never reached the end: %v", walked)
		}
		res, verr = read(ctl, params(map[string]any{"after": at, "limit": 1}))
		if verr != nil {
			t.Fatal(verr)
		}
		page := atEntries(t, res)
		if len(page) != 1 {
			if len(page) == 0 && res["more"] == false {
				break
			}
			t.Fatalf("limit 1 after a cursor: got %v", res)
		}
		walked = append(walked, page[0].Kind+":"+page[0].Status)
		at = res["cursor"].(string)
		if res["more"] == false {
			if at != cursor {
				t.Fatalf("the walk ended at %s, not at %s", at, cursor)
			}
			break
		}
	}
	if strings.Join(walked, " ") != "text: tool_call:running tool_result:ok" {
		t.Fatalf("limit 1 after a cursor walked %v", walked)
	}

	// --- A cursor that is not one, and a replaced file, start over.
	res, verr = read(ctl, params(map[string]any{"after": "t1.0000000000000000.0"}))
	if verr != nil || res["reset"] != true || len(atEntries(t, res)) == 0 {
		t.Fatalf("a made-up cursor: want reset with the newest entries, got %v %v", res, verr)
	}
	fresh := `{"type":"user","uuid":"c1","timestamp":"2026-10-09T11:00:00.000Z","message":{"role":"user","content":"A new conversation in the same file"}}` + "\n"
	// The new file has a record boundary exactly at the old cursor, so only
	// the file's identity can tell the cursor is not into this file.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	shell := `{"type":"system","subtype":"pad","content":""}` + "\n"
	padLen := int(info.Size()) - len(fresh) - len(shell)
	if padLen < 0 {
		t.Fatalf("the fixture is too short to pad to: %d", padLen)
	}
	pad := `{"type":"system","subtype":"pad","content":"` + strings.Repeat("x", padLen) + `"}` + "\n"
	after := `{"type":"assistant","uuid":"c2","timestamp":"2026-10-09T11:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"AFTER-REPLACE"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(path, []byte(fresh+pad+after), 0o600); err != nil {
		t.Fatal(err)
	}
	res, verr = read(ctl, params(map[string]any{"after": cursor}))
	if verr != nil {
		t.Fatal(verr)
	}
	got := atEntries(t, res)
	if res["reset"] != true || len(got) != 2 || got[0].Text != "A new conversation in the same file" || got[1].Text != "AFTER-REPLACE" {
		t.Fatalf("a replaced file: want reset with its two entries, got %v", res)
	}
}

// checkFixtureEntries holds the fixture's entries to what the person should
// see.
func checkFixtureEntries(t *testing.T, entries []atEntry) {
	t.Helper()
	var kinds []string
	byTool := map[string]atEntry{}
	results := map[string]atEntry{}
	ids := map[string]bool{}
	for _, e := range entries {
		kinds = append(kinds, e.Role+"/"+e.Kind)
		if ids[e.ID] {
			t.Errorf("entry id %s twice", e.ID)
		}
		ids[e.ID] = true
		if e.At == 0 {
			t.Errorf("entry %s has no time", e.ID)
		}
		if e.Kind == "tool_result" {
			results[e.ToolID] = e
		} else if e.ToolID != "" {
			byTool[e.ToolID] = e
		}
		for _, bad := range []string{"SIDECHAIN-SHOULD-NOT-SHOW", "META-SHOULD-NOT-SHOW", "sk-live-e2e-SECRET", "\x1b", "[31m", "[2J", "‮", "FULL-FILE-CONTENT", "ORIGINAL-FILE"} {
			b, _ := json.Marshal(e)
			if strings.Contains(string(b), bad) || strings.Contains(e.Text+e.Target, bad) {
				t.Errorf("entry %s carries %q: %s", e.ID, bad, b)
			}
		}
	}
	want := []string{
		"user/text", "assistant/thinking", "assistant/text",
		"assistant/tool_call", "tool/tool_result", // Read
		"assistant/tool_call", "tool/tool_result", // Edit
		"assistant/tool_call", "tool/tool_result", // Write
		"assistant/todos", "tool/tool_result",
		"assistant/tool_call", "tool/tool_result", // Bash
		"assistant/plan", "tool/tool_result",
		"assistant/text",
	}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Fatalf("entries:\n got %v\nwant %v", kinds, want)
	}
	if e := entries[0]; e.Text != "Add retry to the API client, and show me a plan first" {
		t.Errorf("the prompt: %q", e.Text)
	}
	if r := results["toolu_read"]; !r.Truncated || strings.Count(r.Text, "\n") != 19 || !strings.HasSuffix(r.Text, "line 20 of the client") {
		t.Errorf("the Read result: want the first 20 lines, truncated, got %q truncated=%v", r.Text, r.Truncated)
	}
	if c := byTool["toolu_read"]; c.Tool != "Read" || c.Target != "/work/demo/api/client.go" || c.Status != "ok" {
		t.Errorf("the Read call: %+v", c)
	}
	edit := byTool["toolu_edit"]
	if edit.Diff == nil || edit.Diff.File != "/work/demo/api/client.go" || edit.Diff.Added != 1 || edit.Diff.Removed != 1 || len(edit.Diff.Hunks) != 1 {
		t.Fatalf("the Edit diff: %+v", edit.Diff)
	}
	h := edit.Diff.Hunks[0]
	if h.OldStart != 12 || h.NewStart != 12 || atDiffText(h) != " func Get() error {|-\treturn do()|+\treturn retry(3, do)| }" {
		t.Errorf("the Edit hunk: want the structuredPatch at line 12, got %d %d %q", h.OldStart, h.NewStart, atDiffText(h))
	}
	write := byTool["toolu_write"]
	if write.Diff == nil || write.Diff.File != "/work/demo/api/retry.go" || write.Diff.Added != 5 || write.Diff.Removed != 0 || len(write.Diff.Hunks) != 1 || write.Diff.Hunks[0].OldStart != 0 || write.Diff.Hunks[0].NewStart != 1 {
		t.Fatalf("the Write diff: %+v", write.Diff)
	}
	for _, l := range write.Diff.Hunks[0].Lines {
		if l.Op != "+" {
			t.Errorf("a Write of a new file has a %q line", l.Op)
		}
	}
	todo := byTool["toolu_todo"]
	if len(todo.Todos) != 3 || todo.Todos[1].Text != "Run the tests" || todo.Todos[1].Status != "in_progress" || todo.Todos[0].Status != "completed" {
		t.Errorf("the todo list: %+v", todo.Todos)
	}
	bash := byTool["toolu_bash"]
	if bash.Status != "error" || !strings.Contains(bash.Target, "[redacted]") || !strings.HasSuffix(bash.Target, "go test ./api/...") {
		t.Errorf("the Bash call: want error and a masked target, got %+v", bash)
	}
	if r := results["toolu_bash"]; r.Status != "error" || !strings.HasPrefix(r.Text, "FAIL api") {
		t.Errorf("the Bash result: want error with the colours gone, got %+v", r)
	}
	plan := byTool["toolu_plan"]
	if plan.Plan != "## Plan\n\n1. Wrap `Get` in `retry`.\n2. Add a test for three failures.\n" || plan.Status != "ok" {
		t.Errorf("the plan: %+v", plan)
	}
	if last := entries[len(entries)-1]; last.Text != "Done. The retry is in place.\nTwo lines." {
		t.Errorf("the last answer: want the escapes and bidi controls gone and the newline kept, got %q", last.Text)
	}
}

// --- What the test reads back.

type atEntry struct {
	ID        string   `json:"id"`
	At        int64    `json:"at"`
	Role      string   `json:"role"`
	Kind      string   `json:"kind"`
	Text      string   `json:"text"`
	Truncated bool     `json:"truncated"`
	Tool      string   `json:"tool"`
	Target    string   `json:"target"`
	Status    string   `json:"status"`
	ToolID    string   `json:"tool_id"`
	Diff      *atDiff  `json:"diff"`
	Plan      string   `json:"plan"`
	Todos     []atTodo `json:"todos"`
}

type atDiff struct {
	File    string   `json:"file"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	Hunks   []atHunk `json:"hunks"`
}

type atHunk struct {
	OldStart int `json:"old_start"`
	NewStart int `json:"new_start"`
	Lines    []struct {
		Op   string `json:"op"`
		Text string `json:"text"`
	} `json:"lines"`
}

type atTodo struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

func atDiffText(h atHunk) string {
	var parts []string
	for _, l := range h.Lines {
		parts = append(parts, l.Op+l.Text)
	}
	return strings.Join(parts, "|")
}

func atEntries(t *testing.T, res map[string]any) []atEntry {
	t.Helper()
	b, _ := json.Marshal(res["entries"])
	var out []atEntry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode entries: %v", err)
	}
	return out
}

// atEvent waits for the next transcript event about window and returns its
// cursor.
func atEvent(t *testing.T, sub *linkStream, art *atArtifact, window string) string {
	t.Helper()
	sub.timeout = uiTimeout
	defer func() { sub.timeout = 0 }()
	for {
		line, err := sub.br.ReadString('\n')
		if err != nil {
			t.Fatalf("no transcript event: %v", err)
		}
		art.add("event", strings.TrimSpace(line))
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		if ev["type"] != "transcript" || ev["window"] != window {
			continue
		}
		for k := range ev {
			switch k {
			case "type", "session", "window", "cursor", "seq", "boot_id", "time":
			default:
				t.Fatalf("the transcript event carries %q: %v", k, ev)
			}
		}
		c, _ := ev["cursor"].(string)
		if c == "" {
			t.Fatalf("the transcript event has no cursor: %v", ev)
		}
		return c
	}
}

func atWaitScreen(t *testing.T, ctl *linkStream, window, marker string) string {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		res, verr := ctl.call(t, "capture-pane", map[string]any{"session": transcriptSession, "window": window})
		if verr != nil {
			t.Fatalf("capture-pane: %v", verr)
		}
		content := res["content"].(string)
		if strings.Contains(content, marker) {
			return content
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never showed in the pane:\n%s", marker, content)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	appendRaw(t, path, strings.Join(lines, "\n")+"\n")
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// --- A local verb connection, as the person's own tools hold one.

type verbConn struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
	id   int
}

func dialVerbs(t *testing.T, sock string) *verbConn {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &verbConn{t: t, conn: conn, br: bufio.NewReader(conn)}
}

func (c *verbConn) call(verb string, params map[string]any) (map[string]any, string) {
	c.t.Helper()
	c.id++
	line, _ := json.Marshal(map[string]any{"id": c.id, "verb": verb, "params": params})
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("%s: %v", verb, err)
	}
	reply, err := c.br.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("%s: read: %v", verb, err)
	}
	var env struct {
		Result map[string]any `json:"result"`
		Error  *spVerbErr     `json:"error"`
	}
	if err := json.Unmarshal(reply, &env); err != nil {
		c.t.Fatalf("%s: decode: %v", verb, err)
	}
	if env.Error != nil {
		return nil, env.Error.Code
	}
	return env.Result, ""
}

func (c *verbConn) close() { _ = c.conn.Close() }

// --- The artifact.

type atArtifact struct {
	t *testing.T
	b strings.Builder
}

func (a *atArtifact) add(title, body string) {
	fmt.Fprintf(&a.b, "=== %s\n%s\n\n", title, body)
}

func (a *atArtifact) call(verb string, params, res map[string]any, verr *spVerbErr) {
	p := map[string]any{}
	for k, v := range params {
		if k == "human_nonce" && v != atMadeUpNonce {
			v = "<nonce>"
		}
		p[k] = v
	}
	req, _ := json.Marshal(p)
	var reply []byte
	if verr != nil {
		reply, _ = json.Marshal(map[string]any{"error": verr})
	} else {
		reply, _ = json.MarshalIndent(map[string]any{"result": res}, "", " ")
	}
	a.add(verb+" "+string(req), string(reply))
}

func (a *atArtifact) save() {
	path := filepath.Join(artifactDir(a.t), "agent-transcript.txt")
	if err := os.WriteFile(path, []byte(a.b.String()), 0o644); err != nil {
		a.t.Errorf("save the artifact: %v", err)
		return
	}
	a.t.Logf("artifact: %s", path)
}
