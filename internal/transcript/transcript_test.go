package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// jsonTagsOf lists the json field names of a struct, in declaration order.
func jsonTagsOf(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

// The fixtures below carry the record shape of a real Claude Code transcript
// (verified against a live 101 MB file: the key sets, the assistant/user
// alternation, and the stop_reason values) with entirely invented content. No
// line here came off anyone's disk.

const (
	fixCWD     = "/home/dev/proj"
	fixSession = "11111111-2222-3333-4444-555555555555"
	fixVersion = "2.1.222"
)

// assistantLine is an assistant record with a stop reason, carrying a message
// body full of the kind of thing that must never come back out.
func assistantLine(stop string) string {
	return line(map[string]any{
		"type": "assistant", "sessionId": fixSession, "cwd": fixCWD,
		"version": fixVersion, "timestamp": "2026-08-16T10:00:00.000Z",
		"isSidechain": false, "gitBranch": "main", "uuid": "u1", "userType": "external",
		"message": map[string]any{
			"role": "assistant", "model": "claude-opus-5", "type": "message",
			"stop_reason": stop, "stop_sequence": nil,
			"content": []any{map[string]any{
				"type": "text",
				"text": "SECRET-ASSISTANT-PROSE the database password is hunter2",
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
		},
	})
}

// userToolResultLine is the record that lands after a tool runs. toolUseResult
// is where command output and file contents live in a real transcript.
func userToolResultLine() string {
	return line(map[string]any{
		"type": "user", "sessionId": fixSession, "cwd": fixCWD,
		"version": fixVersion, "timestamp": "2026-08-16T10:00:01.000Z",
		"isSidechain": false, "uuid": "u2", "permissionMode": "bypassPermissions",
		"toolUseResult": map[string]any{
			"stdout": "SECRET-TOOL-OUTPUT contents of /etc/shadow",
			"file":   map[string]any{"content": "SECRET-FILE-BODY"},
		},
		"message": map[string]any{"role": "user", "content": "SECRET-TOOL-ECHO"},
	})
}

func userPromptLine() string {
	return line(map[string]any{
		"type": "user", "sessionId": fixSession, "cwd": fixCWD,
		"version": fixVersion, "timestamp": "2026-08-16T10:00:02.000Z",
		"isSidechain": false, "uuid": "u3", "promptSource": "cli",
		"message": map[string]any{"role": "user", "content": "SECRET-USER-PROMPT"},
	})
}

// sidechainLine is a subagent's finished turn. It is written to the same file as
// its parent's and must never be read as the pane's own turn ending.
func sidechainLine(stop string) string {
	return line(map[string]any{
		"type": "assistant", "sessionId": fixSession, "cwd": fixCWD,
		"version": fixVersion, "timestamp": "2026-08-16T10:00:03.000Z",
		"isSidechain": true, "uuid": "u4",
		"message": map[string]any{
			"role": "assistant", "stop_reason": stop,
			"content": []any{map[string]any{"type": "text", "text": "SECRET-SUBAGENT"}},
		},
	})
}

// noiseLines are the record types that make up most of a real file and say
// nothing about the turn. Every one of them carries content.
func noiseLines() string {
	return strings.Join([]string{
		line(map[string]any{"type": "last-prompt", "sessionId": fixSession,
			"leafUuid": "u9", "lastPrompt": "SECRET-LAST-PROMPT"}),
		line(map[string]any{"type": "ai-title", "sessionId": fixSession,
			"aiTitle": "SECRET-TITLE"}),
		line(map[string]any{"type": "agent-name", "sessionId": fixSession,
			"agentName": "SECRET-AGENT-NAME"}),
		line(map[string]any{"type": "permission-mode", "sessionId": fixSession,
			"permissionMode": "bypassPermissions"}),
		line(map[string]any{"type": "mode", "sessionId": fixSession, "mode": "normal"}),
		line(map[string]any{"type": "queue-operation", "sessionId": fixSession,
			"operation": "add", "content": "SECRET-QUEUED-PROMPT"}),
		line(map[string]any{"type": "file-history-snapshot", "messageId": "m1",
			"snapshot": map[string]any{"main.go": "SECRET-FILE-SNAPSHOT"}}),
		line(map[string]any{"type": "attachment", "sessionId": fixSession,
			"attachment": map[string]any{"content": "SECRET-ATTACHMENT"}}),
		line(map[string]any{"type": "system", "sessionId": fixSession, "cwd": fixCWD,
			"version": fixVersion, "subtype": "local_command", "isMeta": true}),
	}, "")
}

func line(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestTurnFromNewestRecord(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Turn
		ok   bool
	}{
		{"end_turn is done", noiseLines() + assistantLine("end_turn"), TurnDone, true},
		{"tool_use is working", noiseLines() + assistantLine("tool_use"), TurnWorking, true},
		{"tool result is working", assistantLine("tool_use") + userToolResultLine(), TurnWorking, true},
		{"fresh prompt is working", assistantLine("end_turn") + userPromptLine(), TurnWorking, true},
		{"a streaming record with no stop reason is working", assistantLine(""), TurnWorking, true},
		{"noise alone says nothing", noiseLines(), TurnUnknown, false},
		{"empty file says nothing", "", TurnUnknown, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.jsonl")
			write(t, path, tc.body)
			obs, ok, err := NewReader(path).Read()
			if err != nil {
				t.Fatal(err)
			}
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if obs.Turn != tc.want {
				t.Fatalf("turn = %v, want %v", obs.Turn, tc.want)
			}
		})
	}
}

// A subagent's Task tool finishing is the failure this guards: its end_turn is
// the newest record in the file while the agent that spawned it is still going.
func TestSidechainNeverEndsTheTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, assistantLine("tool_use")+sidechainLine("end_turn"))
	obs, ok, err := NewReader(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || obs.Turn != TurnWorking {
		t.Fatalf("turn = %v ok=%v, want working", obs.Turn, ok)
	}
}

func TestObservationCarriesJoinIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, assistantLine("end_turn"))
	obs, ok, err := NewReader(path).Read()
	if err != nil || !ok {
		t.Fatalf("read: %v ok=%v", err, ok)
	}
	if obs.SessionID != fixSession || obs.CWD != fixCWD || obs.Version != fixVersion {
		t.Fatalf("identity = %+v", obs)
	}
}

// The half-written last line is the normal state of a file being appended to,
// and reading it as a record is how this source would produce a confident wrong
// answer.
func TestPartialFinalLineIsNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	complete := assistantLine("end_turn")
	partial := assistantLine("tool_use")
	write(t, path, complete+partial[:len(partial)/2])

	r := NewReader(path)
	obs, ok, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || obs.Turn != TurnDone {
		t.Fatalf("turn = %v ok=%v, want done from the complete line", obs.Turn, ok)
	}
	if r.Skipped() != 0 {
		t.Fatalf("skipped = %d, want 0: the partial line must not even be parsed", r.Skipped())
	}

	// The rest of the record arrives, and only now does it count.
	appendTo(t, path, partial[len(partial)/2:])
	obs, ok, err = r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || obs.Turn != TurnWorking {
		t.Fatalf("turn = %v ok=%v, want working once the record completed", obs.Turn, ok)
	}
}

func TestIncrementalReadsOnlyConsumeWhatWasAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, assistantLine("tool_use"))
	r := NewReader(path)
	if _, ok, err := r.Read(); err != nil || !ok {
		t.Fatalf("first read: %v ok=%v", err, ok)
	}
	// Nothing appended: no answer, and no re-reading of what was already seen.
	if _, ok, err := r.Read(); err != nil || ok {
		t.Fatalf("second read: %v ok=%v, want no new answer", err, ok)
	}
	appendTo(t, path, assistantLine("end_turn"))
	obs, ok, err := r.Read()
	if err != nil || !ok || obs.Turn != TurnDone {
		t.Fatalf("third read: %v ok=%v turn=%v", err, ok, obs.Turn)
	}
}

// An append that only adds bookkeeping is a real event with no answer in it. The
// caller must keep believing what it already believed rather than being told
// "unknown".
func TestAppendOfNoiseYieldsNoAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, assistantLine("end_turn"))
	r := NewReader(path)
	if _, ok, _ := r.Read(); !ok {
		t.Fatal("want an answer from the first read")
	}
	appendTo(t, path, noiseLines())
	if _, ok, err := r.Read(); err != nil || ok {
		t.Fatalf("read: %v ok=%v, want no answer", err, ok)
	}
}

func TestTruncationRestartsFromTheTop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, assistantLine("tool_use")+assistantLine("tool_use")+assistantLine("tool_use"))
	r := NewReader(path)
	if _, ok, _ := r.Read(); !ok {
		t.Fatal("want an answer from the first read")
	}
	// Replaced by a shorter file at the same path.
	write(t, path, assistantLine("end_turn"))
	obs, ok, err := r.Read()
	if err != nil || !ok || obs.Turn != TurnDone {
		t.Fatalf("read after truncation: %v ok=%v turn=%v", err, ok, obs.Turn)
	}
}

func TestMalformedLinesAreSkippedAndCounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, "{not json at all\n"+"\n"+assistantLine("end_turn")+"}{\n")
	r := NewReader(path)
	obs, ok, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || obs.Turn != TurnDone {
		t.Fatalf("turn = %v ok=%v", obs.Turn, ok)
	}
	if r.Skipped() != 2 {
		t.Fatalf("skipped = %d, want 2", r.Skipped())
	}
}

func TestMissingFileIsDistinguishable(t *testing.T) {
	_, _, err := NewReader(filepath.Join(t.TempDir(), "gone.jsonl")).Read()
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want ErrNoFile", err)
	}
}

// A cold read of a file far larger than the window must look only at the tail.
// This is the property that keeps a 100 MB transcript from being parsed.
func TestColdReadOnlyTouchesTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.jsonl")
	var b strings.Builder
	for b.Len() < 3*tailWindow {
		b.WriteString(noiseLines())
	}
	// The only turn-bearing record in the file, and it is behind the window.
	head := assistantLine("end_turn")
	write(t, path, head+b.String()+assistantLine("tool_use"))

	r := NewReader(path)
	obs, ok, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || obs.Turn != TurnWorking {
		t.Fatalf("turn = %v ok=%v, want the tail's answer", obs.Turn, ok)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Everything before the window was never consumed, which is only true if it
	// was never read.
	if r.off < info.Size()-tailWindow {
		t.Fatalf("offset %d, want within the last %d bytes of %d", r.off, tailWindow, info.Size())
	}
	if r.Skipped() != 0 {
		t.Fatalf("skipped = %d: the partial leading record must be dropped, not parsed", r.Skipped())
	}
}

// The privacy guarantee, asserted rather than described: every piece of content
// the fixtures carry is absent from everything this package hands back.
func TestNoContentEscapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := noiseLines() + assistantLine("tool_use") + userToolResultLine() +
		userPromptLine() + sidechainLine("end_turn") + assistantLine("end_turn")
	write(t, path, body)

	r := NewReader(path)
	obs, ok, err := r.Read()
	if err != nil || !ok {
		t.Fatalf("read: %v ok=%v", err, ok)
	}

	// Everything the caller can see, rendered.
	seen := obs.Turn.String() + "\x00" + obs.SessionID + "\x00" + obs.CWD +
		"\x00" + obs.Version + "\x00" + obs.Timestamp
	for _, secret := range secretsIn(body) {
		if strings.Contains(seen, secret) {
			t.Fatalf("observation leaked %q", secret)
		}
	}

	// And the buffer the bytes passed through is zeroed, so a dump taken now
	// finds no page of it.
	for _, secret := range secretsIn(body) {
		if strings.Contains(string(r.buf[:cap(r.buf)]), secret) {
			t.Fatalf("read buffer still holds %q after Read", secret)
		}
	}
}

// TestRecordTypeCannotGrowAField pins the struct that is the privacy guarantee.
// Adding a field here is allowed, but it has to be a deliberate act with a
// reason, not a field someone added to debug something and left behind.
func TestRecordTypeCannotGrowAField(t *testing.T) {
	got := jsonTagsOf(record{})
	want := []string{"type", "sessionId", "cwd", "version", "timestamp", "isSidechain", "message"}
	if len(got) != len(want) {
		t.Fatalf("record has fields %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record field %d is %q, want %q", i, got[i], want[i])
		}
	}
	inner := jsonTagsOf(record{}.Message)
	if len(inner) != 1 || inner[0] != "stop_reason" {
		t.Fatalf("record.Message has fields %v, want exactly [stop_reason]", inner)
	}
}

func secretsIn(body string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(body, func(r rune) bool {
		return !(r == '-' || r >= 'A' && r <= 'Z')
	}) {
		if strings.HasPrefix(tok, "SECRET-") {
			out = append(out, tok)
		}
	}
	if len(out) < 8 {
		panic("fixtures lost their secrets")
	}
	return out
}
