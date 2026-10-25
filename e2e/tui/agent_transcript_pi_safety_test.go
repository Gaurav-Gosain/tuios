package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Pi has its own tree projection, but it must keep the boundaries every other
// reader has: only the person reads, only the manifest's folder is read, secrets
// and terminal controls never reach a client, a bad file is refused rather than
// guessed at, and a conversation that was replaced is never shown again.
func TestPiTranscriptSafety(t *testing.T) {
	r := newATRig(t, "pi-transcript-safety.txt", 1)
	path := piFixture(t, r)
	join := func(sessionID string) map[string]any {
		return piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": sessionID, "transcript_path": path})
	}
	humanRead := func() (res map[string]any, code, message string) {
		res, err := r.ctl.call(t, "agent-transcript", map[string]any{"session": transcriptSession, "window": r.wins[0], "human_nonce": r.nonce})
		r.art.call("agent-transcript", nil, res, err)
		if err != nil {
			return res, err.Code, err.Message
		}
		return res, "", ""
	}
	join("pi-fixture")
	initial := r.read(r.wins[0], nil)
	if len(atEntries(t, initial)) == 0 {
		t.Fatal("positive read was empty")
	}

	t.Run("only the person reads it", func(t *testing.T) {
		if _, err := r.ctl.call(t, "agent-transcript", map[string]any{"session": transcriptSession, "window": r.wins[0]}); err == nil || err.Code != "not_human" {
			t.Fatalf("non-human Pi transcript read: %v", err)
		}
	})

	t.Run("unsafe paths are refused and keep the join", func(t *testing.T) {
		outside := filepath.Join(r.base, "outside.jsonl")
		if err := os.WriteFile(outside, []byte("OUTSIDE-SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(filepath.Dir(path), "escape.jsonl")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		fifo := filepath.Join(filepath.Dir(path), "pipe.jsonl")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, unsafe := range []string{outside, link, fifo, filepath.Join(filepath.Dir(path), "notes.txt")} {
			out := piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": "pi-fixture", "transcript_path": unsafe})
			if why, _ := out["transcript_refused"].(string); why == "" {
				t.Fatalf("unsafe Pi path accepted: %v", out)
			}
			if got := r.read(r.wins[0], nil); got["cursor"] != initial["cursor"] {
				t.Fatalf("refused path replaced the join: %v", got)
			}
		}
	})

	t.Run("secrets and controls never reach a client", func(t *testing.T) {
		pem := func(body string) []string {
			return []string{"-----BEGIN PRIVATE KEY-----", body, "-----END PRIVATE KEY-----"}
		}
		blocks := func(kind string, parts []string) []any {
			var out []any
			for _, p := range parts {
				out = append(out, map[string]any{"type": kind, kind: p})
			}
			return out
		}
		// Masking of a multi-line secret runs per entry, so a key a message splits
		// across its blocks must still be seen whole. The unsplit key is the control.
		whole := strings.Repeat("WholeKeyMaterial", 4)
		text := strings.Repeat("SplitTextKeyMaterial", 3)
		thinking := strings.Repeat("SplitThinkKeyMaterial", 3)
		for _, m := range []struct {
			id, parent string
			content    []any
			stop       string
		}{
			{"hostile", "a3", []any{
				map[string]any{"type": "text", "text": "\x1b[31mSAFE-COLOR\x1b[0m\u202e"},
				map[string]any{"type": "toolCall", "id": "secret-tool", "name": "bash", "arguments": map[string]any{"command": "API_TOKEN=sk-live-pi-boundary-SECRET go test ./..."}},
			}, "toolUse"},
			{"whole", "hostile", []any{map[string]any{"type": "text", "text": strings.Join(pem(whole), "\n")}}, ""},
			{"split-text", "whole", blocks("text", pem(text)), ""},
			{"split-thinking", "split-text", blocks("thinking", pem(thinking)), ""},
		} {
			message := map[string]any{"role": "assistant", "content": m.content}
			if m.stop != "" {
				message["stopReason"] = m.stop
			}
			b, _ := json.Marshal(map[string]any{"type": "message", "id": m.id, "parentId": m.parent, "message": message})
			appendLines(t, path, string(b))
		}
		got := r.read(r.wins[0], nil)
		encoded, _ := json.Marshal(got["entries"])
		if strings.Contains(string(encoded), "sk-live-pi-boundary-SECRET") || !strings.Contains(string(encoded), "[redacted]") {
			t.Fatalf("Pi tool secret was not masked: %s", encoded)
		}
		for name, secret := range map[string]string{"unsplit key": whole, "split text blocks": text, "split thinking blocks": thinking} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("%s: PEM body reached the client: %s", name, encoded)
			}
		}
		for _, e := range atEntries(t, got) {
			if strings.ContainsAny(e.Text, "\x1b\u202e") {
				t.Fatalf("Pi text kept terminal/bidi controls: %q", e.Text)
			}
		}
	})

	t.Run("a bad tree is refused, never guessed at", func(t *testing.T) {
		header := `{"type":"session","version":3,"id":"pi-fixture","timestamp":"2026-10-22T12:00:00Z","cwd":"/work/project"}` + "\n"
		for name, records := range map[string]string{
			"cycle":          `{"type":"message","id":"a","parentId":"b","message":{"role":"user","content":"CYCLE-SECRET"}}` + "\n" + `{"type":"message","id":"b","parentId":"a","message":{"role":"user","content":"CYCLE-SECRET"}}` + "\n",
			"missing parent": `{"type":"message","id":"a","parentId":"missing","message":{"role":"user","content":"ORPHAN-SECRET"}}` + "\n",
			"duplicate id":   `{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":"DUPLICATE-SECRET"}}` + "\n" + `{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":"DUPLICATE-SECRET"}}` + "\n",
		} {
			if err := os.WriteFile(path, []byte(header+records), 0o600); err != nil {
				t.Fatal(err)
			}
			join("pi-fixture")
			if res, code, message := humanRead(); code != "internal" || !strings.Contains(message, "Pi") {
				t.Fatalf("%s: invalid Pi ancestry needs a safe, explicit diagnostic: %v %q %q", name, res, code, message)
			}
		}
	})

	t.Run("a replaced conversation is never shown again", func(t *testing.T) {
		replaced := func(what string, apply func()) {
			piFixture(t, r)
			join("pi-fixture")
			if len(atEntries(t, r.read(r.wins[0], nil))) == 0 {
				t.Fatalf("%s: positive read was empty", what)
			}
			apply()
			if res, code, _ := humanRead(); code != "no_transcript" {
				t.Fatalf("%s kept the previous conversation: %v %q", what, res, code)
			}
		}
		replaced("a refused path", func() {
			second := filepath.Join(r.base, "custom-sessions", "second.jsonl")
			if err := os.MkdirAll(filepath.Dir(second), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(second, []byte(`{"type":"session","version":3,"id":"second"}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			out := piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": "second", "transcript_path": second})
			if out["applied"] != true || out["transcript_refused"] == nil {
				t.Fatalf("expected an accepted identity and a refused path: %v", out)
			}
		})
		replaced("a session with no file (--no-session)", func() {
			piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": "ephemeral"})
		})
		replaced("an identity reported alone", func() {
			out, err := r.ctl.call(t, "set-agent-session", map[string]any{"session": transcriptSession, "window": r.wins[0], "harness": "pi", "agent_session_id": "replacement-without-file"})
			if err != nil || out["applied"] != true {
				t.Fatalf("identity replacement: %v %v", out, err)
			}
		})
	})

	t.Run("a vanished file reads as no transcript", func(t *testing.T) {
		piFixture(t, r)
		join("pi-fixture")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, code, _ := humanRead(); code != "no_transcript" {
			t.Fatalf("gone Pi file: %q", code)
		}
	})
}
