package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadOtherHarnesses reads a Codex rollout and a Gemini CLI chat
// in the agent thread, the way the Claude Code transcript is read in
// TestCockpitThreadCards: the real hook of each harness names its file, and
// the cockpit draws the conversation. Each thread has to show a tool card
// with a failed command's output, a diff card with its added line, and the
// harness's error. Gemini writes a message again when a tool call finishes,
// and the thread has to show that edit once.
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
	}{
		{
			name: "codex", harness: "codex", fixture: "codex-rollout.jsonl", sessionID: "0199aa00-0000-7000-8000-000000000001",
			file: filepath.Join(".codex", "sessions", "2026", "10", "18", "rollout-2026-10-18T09-00-00-0199aa00-0000-7000-8000-000000000001.jsonl"),
			want: []string{"Run now takes two. One test fails.", "--- FAIL: TestRun", "apply_patch", "run(2)", "The turn stopped: interrupted"},
			once: "Make run take two and add notes.",
		},
		{
			name: "gemini", harness: "gemini-cli", fixture: "gemini-chat.jsonl", sessionID: "11111111-2222-4333-8444-555555555555",
			file: filepath.Join(".gemini", "tmp", "project", "chats", "session-2026-10-18T09-00-11111111.jsonl"),
			want: []string{"b is three now. The tests fail.", "Command exited with code 1", "replace", "const b = 3;", "quota reached"},
			once: "const b = 3;",
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
			hook.Stdin = bytes.NewReader(hookPayload(path, c.sessionID))
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
			alive(t, term, "after the "+c.name+" thread")
		})
	}
}
