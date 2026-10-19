package transcriptview

// The decoders read files another program wrote, which hold whatever its
// model, its tools and the person put there. That makes them a security
// boundary: every string that leaves must go through Clean and be bounded, a
// record that does not decode must cost only itself, and no input may panic
// or grow without bound. Each format has a scrubbed fixture in testdata,
// written from that harness's real files or published source, and the same
// hostile records.
//
// Ways a decoder can fail, which the cases below cover:
//   - drop or misplace a part of the conversation (fixture tables);
//   - show a message twice that its harness wrote twice (Gemini), or keep
//     one the person took back ($rewindTo);
//   - show the harness's own injected context as the person's words (Codex);
//   - give a tool call the wrong status, or none while it runs;
//   - lose a diff, or pass a string out without Clean (hostile: escapes);
//   - panic or fail the page on a malformed or mistyped record (hostile);
//   - read a document past the bound, or a line past lineMax (hostile).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// summary is an entry in one line: role, kind, tool, target, status, and the
// diff's file and counts.
func summary(e Entry) string {
	s := e.Role + " " + e.Kind
	if e.Tool != "" {
		s += " " + e.Tool
	}
	if e.Target != "" {
		s += " [" + e.Target + "]"
	}
	if e.Status != "" {
		s += " " + e.Status
	}
	if e.Diff != nil {
		s += fmt.Sprintf(" diff %s +%d -%d", e.Diff.File, e.Diff.Added, e.Diff.Removed)
	}
	if len(e.Todos) > 0 {
		s += fmt.Sprintf(" todos %d", len(e.Todos))
	}
	if e.Kind == KindText || e.Kind == KindError || e.Kind == KindThinking {
		s += ": " + strings.ReplaceAll(e.Text, "\n", " ")
	}
	return s
}

func TestFormatFixtures(t *testing.T) {
	cases := []struct {
		file, format string
		want         []string
	}{
		{"codex-rollout.jsonl", FormatCodex, []string{
			"user text: Make run take two and add notes.",
			"assistant thinking: Looking at the entry point",
			"assistant todos update_plan ok todos 2",
			"tool tool_result ok",
			"assistant tool_call shell [grep -n run src/app.go] ok",
			"tool tool_result ok",
			"assistant tool_call apply_patch [src/app.go] ok diff src/app.go +2 -1",
			"assistant tool_call apply_patch [docs/notes.md] ok diff docs/notes.md +2 -0",
			"tool tool_result ok",
			"assistant tool_call exec_command [go test ./...] error",
			"tool tool_result error",
			"assistant tool_call web_search [go test flags] ok",
			"assistant text: Run now takes two. One test fails.",
			"assistant error: The turn stopped: interrupted",
			"assistant tool_call shell [sleep 100] running",
		}},
		{"codex-legacy.jsonl", FormatCodex, []string{
			"user text: List the files.",
			"assistant tool_call shell [ls] ok",
			"tool tool_result ok",
			"assistant text: Two files.",
		}},
		{"gemini-chat.jsonl", FormatGemini, []string{
			"user text: Change b to three.",
			"assistant thinking: Finding the line b is set in app.ts",
			"assistant tool_call replace [/work/project/app.ts] ok diff /work/project/app.ts +1 -1",
			"tool tool_result ok diff /work/project/app.ts +1 -1",
			"assistant tool_call run_shell_command [npm test] error",
			"tool tool_result error",
			"assistant todos write_todos ok todos 2",
			"tool tool_result ok",
			"assistant text: b is three now. The tests fail.",
			"assistant error: [API Error: quota reached]",
			"user text: Thanks!",
		}},
		{"gemini-legacy.json", FormatGemini, []string{
			"user text: Create the readme.",
			"assistant thinking: Plan One file.",
			"assistant text: Writing it.",
			"assistant tool_call write_file [/work/project/README.md] ok diff /work/project/README.md +2 -0",
			"tool tool_result ok",
			"assistant tool_call read_file [README.md] ok",
			"tool tool_result ok",
			"assistant text: Done.",
		}},
		{"qwen-chat.jsonl", FormatQwen, []string{
			"user text: Set b to three.",
			"assistant thinking: Checking app.ts",
			"assistant text: I will edit it.",
			"assistant tool_call edit [/work/project/app.ts] ok diff /work/project/app.ts +1 -1",
			"assistant todos todo_write ok todos 1",
			"tool tool_result ok diff app.ts +1 -1",
			"tool tool_result ok",
			"assistant tool_call run_shell_command [npm test] error",
			"tool tool_result error",
			"assistant text: b is three.",
		}},
		{"copilot-events.jsonl", FormatCopilot, []string{
			"user text: Set b to three.",
			"assistant thinking: The value lives in app.ts.",
			"assistant text: Editing app.ts.",
			"assistant tool_call edit [/work/project/app.ts] ok diff /work/project/app.ts +1 -1",
			"assistant tool_call bash [npm test] error",
			"assistant todos update_todo ok todos 2",
			"tool tool_result ok diff app.ts +1 -1",
			"tool tool_result error",
			"tool tool_result ok",
			"assistant text: b is three. One test fails.",
			"assistant error: Rate limit reached.",
		}},
		{"amp-thread.json", FormatAmp, []string{
			"user text: Set b to three.",
			"assistant thinking: Edit app.ts.",
			"assistant text: Editing.",
			"assistant tool_call edit_file [/work/project/app.ts] ok diff /work/project/app.ts +1 -1",
			"assistant tool_call Bash [npm test] error",
			"assistant todos todo_write ok todos 1",
			"assistant tool_call Bash [sleep 100] running",
			"tool tool_result ok diff /work/project/app.ts +1 -1",
			"tool tool_result error",
			"tool tool_result ok",
			"assistant text: b is three. One test fails.",
		}},
		{"cursor-agent.jsonl", FormatCursor, []string{
			"user text: Set b to three.",
			"assistant thinking: The value is in app.ts.",
			"assistant text: Editing app.ts.",
			"assistant tool_call read_file [/work/project/app.ts] ok",
			"assistant tool_call edit_file [/work/project/app.ts] ok diff /work/project/app.ts +3 -0",
			"assistant tool_call search_replace [/work/project/util.ts] ok diff /work/project/util.ts +1 -1",
			"assistant tool_call search_replace [/work/project/new.ts] ok diff /work/project/new.ts +2 -0",
			"assistant tool_call search_replace [/work/project/util.ts] ok diff /work/project/util.ts +1 -1",
			"assistant tool_call run_terminal_cmd [npm test] error",
			"assistant todos todo_write ok todos 1",
			"tool tool_result ok",
			"tool tool_result ok",
			"tool tool_result ok",
			"tool tool_result ok",
			"tool tool_result ok",
			"tool tool_result error",
			"tool tool_result ok",
			"assistant text: b is three. One test fails.",
		}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			page, err := Read(filepath.Join("testdata", c.file), Options{Format: c.format})
			if err != nil {
				t.Fatal(err)
			}
			if page.Skipped != 0 {
				t.Errorf("skipped %d records of a well-formed file", page.Skipped)
			}
			var got []string
			for _, e := range page.Entries {
				got = append(got, summary(e))
			}
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
			seen := map[string]bool{}
			for _, e := range page.Entries {
				if e.ID == "" || seen[e.ID] {
					t.Errorf("entry id %q is empty or repeated", e.ID)
				}
				seen[e.ID] = true
			}
		})
	}
}

// TestFormatPaging reads each fixture three entries at a time, from the
// newest back and then forward from the start, and gets the same entries as
// one read.
func TestFormatPaging(t *testing.T) {
	for _, c := range []struct{ file, format string }{
		{"codex-rollout.jsonl", FormatCodex}, {"gemini-chat.jsonl", FormatGemini},
		{"gemini-legacy.json", FormatGemini}, {"qwen-chat.jsonl", FormatQwen},
		{"copilot-events.jsonl", FormatCopilot}, {"amp-thread.json", FormatAmp},
		{"cursor-agent.jsonl", FormatCursor},
	} {
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join("testdata", c.file)
			whole, err := Read(path, Options{Format: c.format})
			if err != nil {
				t.Fatal(err)
			}
			ids := func(es []Entry) (out []string) {
				for _, e := range es {
					out = append(out, e.ID)
				}
				return out
			}
			var back []string
			page, err := Read(path, Options{Format: c.format, Limit: 3})
			for err == nil {
				back = append(ids(page.Entries), back...)
				if page.Older == "" {
					break
				}
				page, err = Read(path, Options{Format: c.format, Limit: 3, Before: page.Older})
				if page.Reset {
					t.Fatal("a cursor of this file reset")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(back) != fmt.Sprint(ids(whole.Entries)) {
				t.Errorf("paged back:\n%v\nwhole:\n%v", back, ids(whole.Entries))
			}
		})
	}
}

// hostile is what every format is fed besides its fixture: records that are
// not JSON, that have the wrong types, and strings with escapes in every
// field a page carries.
func hostile(format string) []string {
	esc := "\x1b[31mred\x1b[0m \u202ehidden"
	s := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	common := []string{
		`{"type": "user", "message": {`,
		`not json at all`,
		`[1, 2, 3]`,
		`"a string"`,
		`{"type": 7, "payload": "x", "message": 3, "content": {"x": [1]}, "data": []}`,
	}
	switch format {
	case FormatCodex:
		return append(common,
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+s(esc)+`}]}}`,
			`{"type":"response_item","payload":{"type":"function_call","name":`+s(esc)+`,"arguments":`+s(`{"command":["bash","-lc",`+s(esc)+`]}`)+`,"call_id":"c1"}}`,
			`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":`+s(esc)+`}}`,
			`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c2","input":`+s("*** Begin Patch\n*** Update File: "+esc+"\n@@\n-"+esc+"\n+"+esc+"\n*** End Patch")+`}}`,
			`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\": 5}","call_id":"c3"}}`,
			`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":12,"call_id":{"x":1}}}`,
		)
	case FormatGemini:
		deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
		return append(common,
			`{"id":"h1","type":"user","content":`+s(esc)+`}`,
			`{"id":"h2","type":"gemini","content":[{"text":`+s(esc)+`,"thought":true}],"thoughts":[{"subject":`+s(esc)+`}],"toolCalls":[{"id":"t1","name":"replace","args":{"file_path":`+s(esc)+`,"old_string":`+s(esc)+`,"new_string":"x"},"status":"success","result":[{"functionResponse":{"response":{"output":`+s(esc)+`}}}],"resultDisplay":{"fileDiff":`+s("@@ -1,1 +1,1 @@\n-"+esc+"\n+x\n")+`}}]}`,
			`{"id":"h3","type":"gemini","content":`+deep+`}`,
			`{"id":"h4","type":"gemini","toolCalls":"not a list"}`,
			`{"$rewindTo": 5}`,
		)
	case FormatQwen:
		return append(common,
			`{"type":"user","message":{"parts":[{"text":`+s(esc)+`}]}}`,
			`{"type":"assistant","message":{"parts":[{"functionCall":{"id":"c1","name":`+s(esc)+`,"args":{"command":`+s(esc)+`}}}]}}`,
			`{"type":"tool_result","message":{"parts":[{"functionResponse":{"id":"c1","response":{"output":`+s(esc)+`}}}]},"toolCallResult":{"status":"error","resultDisplay":{"fileDiff":"@@ -99999999999 +1 @@\n"}}}`,
			`{"type":"assistant","message":{"parts":"text"}}`,
		)
	case FormatCopilot:
		return append(common,
			`{"type":"user.message","data":{"content":`+s(esc)+`}}`,
			`{"type":"assistant.message","data":{"content":`+s(esc)+`,"toolRequests":[{"toolCallId":"t1","name":`+s(esc)+`,"arguments":{"path":`+s(esc)+`}}]}}`,
			`{"type":"tool.execution_complete","data":{"toolCallId":"t1","success":false,"error":{"message":`+s(esc)+`},"result":{"detailedContent":`+s("--- a/x\n+++ b/"+esc+"\n@@ -1 +1 @@\n-a\n+"+esc+"\n")+`}}}`,
			`{"type":"update_todo","data":{"toolRequests":[{"name":"update_todo","arguments":{"todos":7}}]}}`,
		)
	case FormatCursor:
		return append(common,
			`{"role":"user","message":{"content":"<user_query>`+esc+`</user_query>"}}`,
			`{"role":"assistant","message":{"content":[{"type":"text","text":`+s(esc)+`},{"type":"tool_use","id":"t1","name":`+s(esc)+`,"input":{"command":`+s(esc)+`}}]}}`,
			`{"role":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":`+s(esc)+`,"is_error":true}]}}`,
			`{"role":"assistant","message":{"content":7}}`,
		)
	case FormatAmp:
		return []string{`{"messages": [` +
			`{"role":"user","content":[{"type":"text","text":` + s(esc) + `}]},` +
			`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":` + s(esc) + `,"input":{"cmd":` + s(esc) + `}}]},` +
			`{"role":"user","content":[{"type":"tool_result","toolUseID":"t1","run":{"status":"error","error":{"message":` + s(esc) + `}}}]},` +
			`{"role":"assistant","content":"not blocks"},` +
			`7, null, "x"]}`}
	}
	return common
}

func TestFormatHostile(t *testing.T) {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == 0x1b || r == 0x202e {
				return -1
			}
			return r
		}, s)
	}
	for _, format := range []string{FormatCodex, FormatGemini, FormatQwen, FormatCopilot, FormatAmp, FormatCursor} {
		t.Run(format, func(t *testing.T) {
			name := "hostile.jsonl"
			if format == FormatAmp {
				name = "hostile.json"
			}
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, []byte(strings.Join(hostile(format), "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			page, err := Read(path, Options{Format: format, Clean: clean})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Entries) == 0 {
				t.Fatal("the hostile records with escapes gave no entries")
			}
			out, err := json.Marshal(page.Entries)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(out, []byte(`\u001b`)) || bytes.Contains(out, []byte("\u202e")) || bytes.Contains(out, []byte(`\u202e`)) {
				t.Errorf("a string left without Clean: %s", out)
			}
		})
	}
}

// TestFormatBounds checks the bounds a hostile file meets: a line past
// lineMax, a document past scanMax, and a patch past DiffLinesMax.
func TestFormatBounds(t *testing.T) {
	dir := t.TempDir()
	t.Run("long line", func(t *testing.T) {
		path := filepath.Join(dir, "long.jsonl")
		long := `{"type":"user.message","data":{"content":"` + strings.Repeat("a", lineMax) + `"}}`
		short := `{"type":"user.message","data":{"content":"after"}}`
		if err := os.WriteFile(path, []byte(long+"\n"+short+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		page, err := Read(path, Options{Format: FormatCopilot})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 || page.Entries[0].Text != "after" {
			t.Errorf("entries %v, want only the record after the long one", page.Entries)
		}
	})
	t.Run("large document", func(t *testing.T) {
		path := filepath.Join(dir, "large.json")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		// A sparse file: the size is past the bound and no disk is used.
		if err := f.Truncate(scanMax + 1); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if _, err := Read(path, Options{Format: FormatAmp}); err != ErrTooLarge {
			t.Errorf("err %v, want ErrTooLarge", err)
		}
	})
	t.Run("large patch", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("*** Begin Patch\n*** Add File: big.txt\n")
		for i := range 5000 {
			fmt.Fprintf(&b, "+line %d\n", i)
		}
		b.WriteString("*** End Patch\n")
		rec, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "name": "apply_patch", "call_id": "c", "input": b.String()}})
		path := filepath.Join(dir, "patch.jsonl")
		if err := os.WriteFile(path, append(rec, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		page, err := Read(path, Options{Format: FormatCodex})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 || page.Entries[0].Diff == nil {
			t.Fatalf("entries %v, want one call with a diff", page.Entries)
		}
		d := page.Entries[0].Diff
		lines := 0
		for _, h := range d.Hunks {
			lines += len(h.Lines)
		}
		if lines > DiffLinesMax || !d.Truncated {
			t.Errorf("diff shows %d lines, truncated %v; want at most %d and truncated", lines, d.Truncated, DiffLinesMax)
		}
	})
	t.Run("unknown format", func(t *testing.T) {
		if _, err := Read(filepath.Join("testdata", "codex-rollout.jsonl"), Options{Format: "no-such"}); err != ErrFormat {
			t.Errorf("err %v, want ErrFormat", err)
		}
	})
}

// TestFormatIdentity checks the identity the hookless search reads: the
// folder and the session's start, from the start of the first record. The
// Codex session_meta of codex-long-meta.jsonl is 200 KiB, with the long
// instructions before cwd, as Codex writes them.
func TestFormatIdentity(t *testing.T) {
	start := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		file, format string
		start        time.Time
	}{
		{"codex-rollout.jsonl", FormatCodex, start},
		{"codex-long-meta.jsonl", FormatCodex, start},
		{"gemini-chat.jsonl", FormatGemini, start},
		{"gemini-legacy.json", FormatGemini, start},
		{"qwen-chat.jsonl", FormatQwen, start},
		{"copilot-events.jsonl", FormatCopilot, start},
	} {
		id, ok := Identify(c.format, filepath.Join("testdata", c.file))
		if !ok {
			t.Errorf("%s: no identity", c.file)
			continue
		}
		if !id.Start.Equal(c.start) {
			t.Errorf("%s: start %v, want %v", c.file, id.Start, c.start)
		}
		if !id.In("/work/project") || id.In("/work/other") || id.In("") {
			t.Errorf("%s: identity %+v does not place the session in /work/project alone", c.file, id)
		}
	}
	if _, ok := Identify(FormatCodex, filepath.Join("testdata", "codex-legacy.jsonl")); ok {
		t.Error("a rollout with no session_meta gave an identity")
	}
	if _, ok := Identify(FormatAmp, filepath.Join("testdata", "amp-thread.json")); ok {
		t.Error("an Amp thread gave an identity: its folder is at the end of the document, and only its plugin's thread id joins it")
	}
}

// TestGeminiRewrites checks the two ways Gemini CLI changes what it wrote: a
// message written again keeps its first place, and a read after a cursor
// that meets $rewindTo returns the newest page with Reset, since the reader
// holds messages that are gone.
func TestGeminiRewrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-x.jsonl")
	lines := []string{
		`{"sessionId":"s","projectHash":"h","startTime":"2026-10-18T09:00:00Z"}`,
		`{"id":"u1","type":"user","content":"first"}`,
		`{"id":"g1","type":"gemini","content":"working","toolCalls":[{"id":"c1","name":"run_shell_command","args":{"command":"make"},"status":"executing"}]}`,
		`{"id":"u2","type":"user","content":"second"}`,
		`{"id":"g1","type":"gemini","content":"working","toolCalls":[{"id":"c1","name":"run_shell_command","args":{"command":"make"},"status":"success","result":[{"functionResponse":{"response":{"output":"done"}}}]}]}`,
	}
	write := func() {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	page, err := Read(path, Options{Format: FormatGemini})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Entries {
		got = append(got, summary(e))
	}
	want := []string{
		"user text: first",
		"assistant text: working",
		"assistant tool_call run_shell_command [make] ok",
		"tool tool_result ok",
		"user text: second",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A read after the cursor with nothing new gives nothing and no reset.
	after, err := Read(path, Options{Format: FormatGemini, After: page.Cursor})
	if err != nil || after.Reset || len(after.Entries) != 0 {
		t.Fatalf("read after the cursor: %d entries, reset %v, err %v", len(after.Entries), after.Reset, err)
	}
	lines = append(lines, `{"$rewindTo":"u2"}`, `{"id":"u3","type":"user","content":"again"}`)
	write()
	after, err = Read(path, Options{Format: FormatGemini, After: page.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Reset {
		t.Fatal("a rewind after the cursor did not reset the page")
	}
	got = got[:0]
	for _, e := range after.Entries {
		got = append(got, summary(e))
	}
	if strings.Contains(strings.Join(got, "\n"), "second") || !strings.Contains(strings.Join(got, "\n"), "again") {
		t.Errorf("after the rewind:\n%s\nwant the message taken back gone and the new one there", strings.Join(got, "\n"))
	}
}
