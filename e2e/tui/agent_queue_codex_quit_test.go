package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestQueuedMessageReachesAShellCodexLeft is the pane whose Codex quit back
// to its shell. The pane still carries the codex harness, and the last frame
// of Codex's box, with a draft in it, is still on the screen above the shell's
// prompt. Codex's box rule read that frame as the person's draft, so the
// queue held every message to the pane while nothing would ever clear it.
//
// The box rule applies only while the agent runs: with the stand-in Codex in
// the foreground and a draft in its box, a queued entry waits (the negative
// control). Once it quits, the shell holds the terminal, the shell rule reads
// the pane, and the entry is typed.
func TestQueuedMessageReachesAShellCodexLeft(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	cli := func(what string, args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return out
	}
	held := func() bool {
		return strings.Contains(strings.ReplaceAll(cli("get-agent-state", "get-agent-state", "-s", sess, "--json"), " ", ""), `"draft_held":true`)
	}
	script := filepath.Join(t.TempDir(), "codex.sh")
	if err := os.WriteFile(script, []byte(codexBoxScript), 0o644); err != nil {
		t.Fatal(err)
	}
	cli("start the stand-in agent", "send-text", "-s", sess, "bash "+script+"\n")
	if err := term.WaitForText("for agents", uiTimeout); err != nil {
		t.Fatalf("the stand-in agent never drew its box: %v\n%s", err, term.Snapshot())
	}
	cli("set working", "set-agent-state", "-s", sess, "working", "--harness", "codex")
	cli("set idle", "set-agent-state", "-s", sess, "idle", "--harness", "codex")

	// Codex runs, with a draft in its box: the entry waits.
	enterTerminalMode(t, term)
	const draft, queued = "half a thought", "queued-after-codex-quit-5c1a"
	if err := term.Type(draft); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("› "+draft, uiTimeout); err != nil {
		t.Fatalf("the draft never reached the box: %v\n%s", err, term.Snapshot())
	}
	cli("queue behind the draft", "queue", "-s", sess, "echo", queued)
	deadline := time.Now().Add(uiTimeout)
	for !held() {
		if strings.Contains(term.Screen().Text(), queued) {
			t.Fatalf("ASSERTION: the queued entry was typed into the running Codex's draft\n%s", term.Snapshot())
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the daemon never held the queue for the running Codex's draft\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Codex quits. Its last frame, draft and all, stays above the shell's
	// prompt, and the pane still says codex.
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatal(err)
	}
	cli("the harness reports again", "set-agent-state", "-s", sess, "idle", "--harness", "codex")
	if err := term.WaitForText(queued, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the queued entry was never typed once Codex quit to the shell (draft_held %v)\n%s", held(), term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "› "+draft) {
		t.Fatalf("the test lost its point: Codex's last frame with the draft is not on the screen\n%s", term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "codex-quit-queue-typed")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "codex-quit-queue-typed")
}
