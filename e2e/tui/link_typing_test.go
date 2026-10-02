package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Typing over a link is held to the prompt rule a pane on the same machine is
// held to: keys typed into a pane on needs_input answer it, so a caller needs
// respond. A pane's grants do not travel over a link, so on the far machine
// the link policy stands for them, and a link without respond may not type
// into a prompt. The person, typing from outside every pane on the hub, is
// not held by it.

// linkTypingFixture is a hub with a link to build, a pane on build waiting on
// a prompt, and the id of that pane. farAllow is build's allow list for every
// machine linked to it.
func linkTypingFixture(t *testing.T, farAllow string) (base, remote string, env []string, prompted string) {
	t.Helper()
	base = t.TempDir()
	remote = remoteMachine(t)
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = ["+farAllow+"]\n")
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	before, err := daemonWindows(remote, "far")
	if err != nil {
		t.Fatalf("list the far windows: %v", err)
	}
	if out, err := tuiosCLI(t, remote, "new-window", "-s", "far", "prompted", "--no-focus", "--", "/bin/sh", "-c", "echo READY; exec cat"); err != nil {
		t.Fatalf("create the prompted window on build: %v\n%s", err, out)
	}
	after, err := daemonWindows(remote, "far")
	if err != nil {
		t.Fatalf("list the far windows: %v", err)
	}
	seen := map[string]bool{}
	for _, w := range before.Windows {
		seen[w.ID] = true
	}
	for _, w := range after.Windows {
		if !seen[w.ID] {
			prompted = w.ID
		}
	}
	if prompted == "" {
		t.Fatalf("the prompted window never appeared on build: %+v", after)
	}
	waitForCaptureOn(t, remote, "far", prompted, "READY")
	if out, err := tuiosCLI(t, remote, "set-agent-state", "-s", "far", "-w", prompted, "needs_input", "--kind", "approval", "--message", "approve Bash: rm -rf build"); err != nil {
		t.Fatalf("set-agent-state on build: %v\n%s", err, out)
	}
	env = hubWithBuild(t, base, remote)
	return base, remote, env, prompted
}

// waitForCaptureOn polls a pane on the daemon at base until its capture holds
// want, and returns the capture.
func waitForCaptureOn(t *testing.T, base, sess, window, want string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", window)
		if strings.Contains(out, want) {
			return out
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("pane %s in %s never showed %q:\n%s", window, sess, want, out)
	return out
}

// typeFromAHubPane runs send-text in a pane on the hub, aimed at the prompted
// pane on build. It returns the pane's screen once the exit code is on it, and
// what send-text printed, read from a file so the pane's line wrap cannot cut
// a word.
func typeFromAHubPane(t *testing.T, base string, env []string, prompted, text string) (screen, said string) {
	t.Helper()
	outFile := filepath.Join(base, "send-text.out")
	line := tuiosBin + " send-text -s build:far -w " + prompted + " '" + text + "\\n' >" + outFile + " 2>&1; echo RESP_EXIT=$?\n"
	if out, err := tuiosCLIEnv(t, base, env, "send-text", "-s", "home", line); err != nil {
		t.Fatalf("type the call into the hub pane: %v\n%s", err, out)
	}
	hub, err := daemonWindows(base, "home")
	if err != nil || len(hub.Windows) == 0 {
		t.Fatalf("list the hub windows: %v", err)
	}
	// The command line itself holds "RESP_EXIT=$?", so only a digit after it
	// is the exit code.
	deadline := time.Now().Add(uiTimeout)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "capture-pane", "-s", "home", "-w", hub.Windows[0].ID)
		if strings.Contains(out, "RESP_EXIT=0") || strings.Contains(out, "RESP_EXIT=1") {
			data, _ := os.ReadFile(outFile)
			return out, string(data)
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("the hub pane never printed its exit code:\n%s", out)
	return out, ""
}

// TestAPaneOverALinkCannotAnswerAPrompt: a pane on the hub types into a pane on
// build that waits on a prompt. build's policy for the link has no respond, so
// the call is refused with respond named, and the far pane receives nothing.
// The person on the hub, typing from outside every pane, still reaches it.
// With respond in build's policy, the same call from the same hub pane goes
// through.
//
// Negative control: with the holdLinkTyping call cut from checkGrants, the
// refused half prints RESP_EXIT=0 and the far pane shows the text.
func TestAPaneOverALinkCannotAnswerAPrompt(t *testing.T) {
	t.Run("without respond", func(t *testing.T) {
		base, remote, env, prompted := linkTypingFixture(t, `"list", "mail", "open", "write"`)
		screen, said := typeFromAHubPane(t, base, env, prompted, "LINK-ANSWER")
		if !strings.Contains(screen, "RESP_EXIT=1") || !strings.Contains(said, "respond") {
			dumpLinkLogs(t, base, remote)
			t.Fatalf("ASSERTION: a hub pane typed into a prompt on build without respond:\n%s\nsend-text said:\n%s", screen, said)
		}
		t.Logf("send-text said:\n%s", said)
		time.Sleep(500 * time.Millisecond)
		if far, _ := tuiosCLI(t, remote, "capture-pane", "-s", "far", "-w", prompted); strings.Contains(far, "LINK-ANSWER") {
			t.Fatalf("ASSERTION: the refused text reached the prompted pane on build:\n%s", far)
		}

		// The person on the hub is not held by it.
		if out, err := tuiosCLIEnv(t, base, env, "send-text", "-s", "build:far", "-w", prompted, "PERSON-ANSWER\n"); err != nil {
			t.Fatalf("ASSERTION: the person's typing over the link into a prompt was refused: %v\n%s", err, out)
		}
		waitForCaptureOn(t, remote, "far", prompted, "PERSON-ANSWER")
	})

	t.Run("with respond", func(t *testing.T) {
		base, remote, env, prompted := linkTypingFixture(t, `"list", "mail", "open", "write", "respond"`)
		screen, said := typeFromAHubPane(t, base, env, prompted, "LINK-ANSWER")
		if !strings.Contains(screen, "RESP_EXIT=0") {
			dumpLinkLogs(t, base, remote)
			t.Fatalf("ASSERTION: a hub pane over a link with respond could not type into a prompt on build:\n%s\nsend-text said:\n%s", screen, said)
		}
		waitForCaptureOn(t, remote, "far", prompted, "LINK-ANSWER")
	})
}
