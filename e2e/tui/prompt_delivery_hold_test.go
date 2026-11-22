package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDeliveredPromptStaysWhole: the daemon types a queued prompt into an
// agent at rest as a paste and then its Enter, up to 300 ms apart. Text that
// another caller types into the pane in that gap waits for the Enter, so the
// prompt and the text each arrive whole, the prompt first.
//
// The pane runs a stand-in agent with no echo, so the daemon waits the whole
// gap before its Enter. The agent copies each byte it reads to a file, and
// when the first byte of the paste comes in it tells the test through a
// FIFO. The test types its own line at that moment.
//
// How it could pass wrongly: the typed line could reach the pane after the
// Enter only because it was slow. It is sent the moment the paste starts,
// and the negative control shows that it then lands inside the gap.
//
// Negative control (NEGATIVE_CONTROLS.md, TestDeliveredPromptStaysWhole):
// with PTY.Write not waiting for the input hold, the file holds the typed line
// inside the prompt, before its Enter.
func TestDeliveredPromptStaysWhole(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-deliver-whole"
	const prompt, typed = "the queued prompt", "a line typed meanwhile"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	w := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent", "--no-focus", "--print-id"))

	dir := t.TempDir()
	got, ready, pasted := filepath.Join(dir, "read.bin"), filepath.Join(dir, "ready"), filepath.Join(dir, "pasted")
	if err := syscall.Mkfifo(pasted, 0o600); err != nil {
		t.Fatal(err)
	}
	// The stand-in agent: raw input with no echo. It waits for the first
	// byte, tells the test, and then copies what it reads to the file.
	agent := filepath.Join(dir, "agent.sh")
	script := "stty raw -echo\n" +
		": > '" + ready + "'\n" +
		"IFS= read -r -n1 -d '' c\n" +
		"echo > '" + pasted + "'\n" +
		"{ printf '%s' \"$c\"; exec cat; } > '" + got + "'\n"
	if err := os.WriteFile(agent, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "send-text", "-s", sess, "-w", w, "bash "+agent+"\n")
	waitForFileText(t, ready, func(string) bool { return true }, "the stand-in agent never started")

	// The first byte of the paste opens the FIFO from the agent's side.
	started := make(chan error, 1)
	go func() {
		_, err := os.ReadFile(pasted)
		started <- err
	}()

	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", w, "working", "--harness", "claude-code", "-m", "on it")
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", w, "done", "--harness", "claude-code", "-m", "ready")
	mustCLI(t, base, "queue", "-s", sess, "-w", w, "--", prompt)
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(scaled(30 * time.Second)):
		t.Fatalf("ASSERTION: the queued prompt was never typed into the agent's pane")
	}
	mustCLI(t, base, "send-text", "-s", sess, "-w", w, typed+"\r")

	want := prompt + "\r" + typed + "\r"
	out := waitForFileText(t, got, func(s string) bool { return strings.Count(s, "\r") >= 2 }, "the prompt and the typed line never both arrived")
	saveText(t, "delivered-prompt-stays-whole", "the agent read:\n"+strings.ReplaceAll(out, "\r", "<CR>\n"))
	if out != want {
		t.Fatalf("ASSERTION: the agent read %q, want %q: the typed line went into the prompt", out, want)
	}
}
