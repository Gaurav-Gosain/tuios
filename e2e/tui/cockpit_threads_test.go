package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadOtherHarnesses reads a Codex rollout, a Gemini CLI chat, a
// Copilot CLI session and an Amp thread in the agent thread, the way the
// Claude Code transcript is read in TestCockpitThreadCards: the real hook of
// each harness names its file, and the cockpit draws the conversation. Amp's
// plugin names only the thread id, and the manifest's session_file turns it
// into the file. Each thread has to show a tool card
// with a failed command's output, a diff card with its added line, and the
// harness's error. Gemini writes a message again when a tool call finishes,
// and the thread has to show that edit once. A Gemini message written again
// with new text while the thread is open shows the new text.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the cockpit
// reading transcripts of Claude Code alone (threadTranscriptHarness), both
// threads show the recap and the card checks fail. With the decoder's drop of
// a message written again removed, this test still passes, because the client
// replaces an entry by its id; TestFormatFixtures in internal/transcriptview
// catches that one.
func TestCockpitThreadOtherHarnesses(t *testing.T) {
	cases := []struct {
		name, harness, fixture, sessionID string
		// file is the transcript's path under the fake home.
		file string
		// want is what the thread shows: the answer, the failed command's
		// output, the diff card's tool and added line, and the error.
		want []string
		// once is a line the thread shows exactly once.
		once string
		// payload is the hook's input, the SessionStart of hookPayload when
		// empty.
		payload string
		// rewrite is a line appended to the transcript while the thread is
		// open, which writes a message again with new text, and rewritten
		// the text the thread then shows in place of want[0].
		rewrite, rewritten string
	}{
		{
			name: "pi", harness: "pi", fixture: "pi-session.jsonl", sessionID: "pi-fixture",
			file: filepath.Join(".pi", "agent", "sessions", "--work-project--", "2026-10-22_pi-fixture.jsonl"),
			want: []string{"b is three. One test fails.", "Command failed with exit code 1", "edit", "const b = 3;", "Rate limit reached."},
			once: "Set b to three.",
		},
		{
			name: "codex", harness: "codex", fixture: "codex-rollout.jsonl", sessionID: "0199aa00-0000-7000-8000-000000000001",
			file: filepath.Join(".codex", "sessions", "2026", "10", "18", "rollout-2026-10-18T09-00-00-0199aa00-0000-7000-8000-000000000001.jsonl"),
			want: []string{"Run now takes two. One test fails.", "--- FAIL: TestRun", "apply_patch", "run(2)", "The turn stopped: interrupted"},
			once: "Make run take two and add notes.",
		},
		{
			name: "gemini", harness: "gemini-cli", fixture: "gemini-chat.jsonl", sessionID: "11111111-2222-4333-8444-555555555555",
			file:      filepath.Join(".gemini", "tmp", "project", "chats", "session-2026-10-18T09-00-11111111.jsonl"),
			want:      []string{"b is three now. The tests fail.", "Command exited with code 1", "replace", "const b = 3;", "quota reached"},
			once:      "const b = 3;",
			rewrite:   `{"id":"m-gem-2","timestamp":"2026-10-18T09:00:05.000Z","type":"gemini","content":"b is three now. **The tests pass.**","model":"gemini-3-pro"}` + "\n",
			rewritten: "b is three now. The tests pass.",
		},
		{
			name: "copilot", harness: "copilot", fixture: "copilot-events.jsonl", sessionID: "s-copilot",
			file: filepath.Join(".copilot", "session-state", "s-copilot", "events.jsonl"),
			want: []string{"b is three. One test fails.", "Command failed with exit code 1", "edit", "const b = 3;", "Rate limit reached."},
			once: "Set b to three.",
		},
		{
			name: "amp", harness: "amp", fixture: "amp-thread.json", sessionID: "T-00000000-0000-7000-8000-000000000001",
			file:    filepath.Join(".local", "share", "amp", "threads", "T-00000000-0000-7000-8000-000000000001.json"),
			want:    []string{"b is three. One test fails.", "1 failing", "edit_file", "const b = 3;", "Bash"},
			once:    "Set b to three.",
			payload: `{"hook_event_name":"agent.start","session_id":"T-00000000-0000-7000-8000-000000000001"}`,
		},
		{
			// opencode keeps no file of a session: the thread comes from a
			// service at 127.0.0.1 that this test runs, as opencode 2.0.32
			// answers, and the plugin's report names the session.
			name: "opencode", harness: "opencode", fixture: "opencode-messages.json", sessionID: "ses_01Fixture",
			file:    "service.json",
			want:    []string{"b is three. One test fails.", "Command exited with code 1", "edit", "const b = 3;", "Rate limit reached."},
			once:    "Set b to three.",
			payload: `{"hook_event_name":"chat.message","session_id":"ses_01Fixture"}`,
		},
		{
			name: "cursor", harness: "cursor-agent", fixture: "cursor-agent.jsonl", sessionID: "c0ffee00-0000-4000-8000-000000000001",
			file: filepath.Join(".cursor", "projects", "work-project", "agent-transcripts", "c0ffee00-0000-4000-8000-000000000001.jsonl"),
			want: []string{"b is three. One test fails.", "1 failing", "edit_file", "const b = 3;", "run_terminal_cmd"},
			once: "Set b to three.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			useShippedLooks(base)
			for _, s := range []string{"home", "api"} {
				if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
					t.Fatalf("create session %s: %v\n%s", s, err, out)
				}
			}
			// The decoder test's fixtures, so the two tests read one file.
			fixture, err := os.ReadFile(filepath.Join("..", "..", "internal", "transcriptview", "testdata", c.fixture))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(xdgDir(base, "HOME"), c.file)
			if c.harness == "opencode" {
				path = filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "opencode", "service.json")
				fixture = fakeOpenCodeService(t, fixture, c.sessionID)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, fixture, 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "agent"); err != nil {
				t.Fatalf("name the pane: %v\n%s", err, out)
			}
			hook := exec.Command(tuiosBin, "agent-hook", c.harness, "--session", "api", "--window", "agent", "--explain")
			hook.Dir = workDirIn(t, base)
			hook.Env = append(os.Environ(), "SHELL=/bin/sh")
			for _, key := range xdgKeys {
				hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
			}
			payload := hookPayload(path, c.sessionID)
			if c.harness == "pi" {
				payload, _ = json.Marshal(map[string]any{"hook_event_name": "session_start", "session_id": c.sessionID, "transcript_path": path})
			}
			if c.payload != "" {
				payload = []byte(c.payload)
			}
			if c.harness == "cursor-agent" {
				// Cursor names its events in camel case, and the transcript
				// in every payload.
				payload, _ = json.Marshal(map[string]any{"hook_event_name": "sessionStart", "session_id": c.sessionID, "transcript_path": path})
			}
			hook.Stdin = bytes.NewReader(payload)
			out, err := hook.CombinedOutput()
			if err != nil {
				t.Fatalf("agent-hook %s SessionStart: %v\n%s", c.harness, err, out)
			}
			if strings.Contains(string(out), "transcript_refused") {
				t.Fatalf("ASSERTION: the daemon refused the transcript the hook named:\n%s", out)
			}
			if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "api", "-w", "agent", "--harness", c.harness); err != nil {
				t.Fatalf("report working: %v\n%s", err, out)
			}

			term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 50, shippedLooks: true, env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
				t.Fatalf("open the cockpit: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return containsAll(s.Text(), c.want[0], c.want[1]) }, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the %s thread never showed the answer and the failed command: %v\n%s", c.name, err, term.Snapshot())
			}
			// The thread shows the bottom. Scroll up until every card has
			// been on screen.
			seen := map[string]bool{}
			count := 0
			for range 20 {
				text := term.Screen().Text()
				for _, w := range c.want {
					if strings.Contains(text, w) {
						seen[w] = true
					}
				}
				if n := strings.Count(text, c.once); n > count {
					count = n
				}
				if len(seen) == len(c.want) && count > 0 {
					break
				}
				if err := term.SendKeys("K"); err != nil {
					t.Fatal(err)
				}
				_ = term.WaitStable(uiTimeout)
			}
			for _, w := range c.want {
				if !seen[w] {
					t.Errorf("ASSERTION: the %s thread never showed %q\n%s", c.name, w, term.Snapshot())
				}
			}
			if count != 1 {
				t.Errorf("ASSERTION: the %s thread showed %q %d times on one screen, want once\n%s", c.name, c.once, count, term.Snapshot())
			}
			// The frame of the diff card.
			for range 20 {
				if strings.Contains(term.Screen().Text(), c.want[3]) {
					break
				}
				if err := term.SendKeys("J"); err != nil {
					t.Fatal(err)
				}
				_ = term.WaitStable(uiTimeout)
			}
			saveCockpitFrames(t, term, "thread-"+c.name)
			if c.rewrite != "" {
				// The message is written again under its id with new text,
				// and the thread draws the new text, not the card it kept.
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteString(c.rewrite); err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
				for range 20 {
					if strings.Contains(term.Screen().Text(), "b is three now.") {
						break
					}
					if err := term.SendKeys("J"); err != nil {
						t.Fatal(err)
					}
					_ = term.WaitStable(uiTimeout)
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					text := s.Text()
					return strings.Contains(text, c.rewritten) && !strings.Contains(text, c.want[0])
				}, uiTimeout); err != nil {
					t.Fatalf("ASSERTION: the %s thread kept the old text of a message written again: %v\n%s", c.name, err, term.Snapshot())
				}
			}
			alive(t, term, "after the "+c.name+" thread")
		})
	}
}

// fakeOpenCodeService runs a service that answers as opencode 2.0.32 does,
// with page as the session's messages, and returns the service.json that
// names it.
func fakeOpenCodeService(t *testing.T, page []byte, session string) []byte {
	t.Helper()
	// Made when the test runs, so no secret scanner reads a literal.
	password := strings.Repeat("y", 8) + strconv.Itoa(os.Getpid())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "opencode" || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/info":
			fmt.Fprintf(w, `{"version":"2.0.32","pid":%d}`, os.Getpid())
		case "/api/session/" + session + "/message":
			_, _ = w.Write(page)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	data, _ := json.Marshal(map[string]any{"id": "svc", "version": "2.0.32", "url": srv.URL, "pid": os.Getpid(), "password": password})
	return data
}
