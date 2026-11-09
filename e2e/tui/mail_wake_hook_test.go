package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMailFromAHookWakesAnIdleAgent: a hook the daemon runs outside every
// pane mails an agent at rest and queues a prompt for it. The agent gets the
// wake line and the queued prompt, each typed once.
//
// How this could pass wrongly, written down first:
//   - The hook could inherit a pane's id and token, and send as that pane,
//     which was never broken. It drops both, as a hook that mails another
//     session must.
//   - The hook could never run. The positive check is what the agent prints,
//     and the queued prompt shows the hook ran even when no wake comes.
//
// Negative controls (NEGATIVE_CONTROLS.md): with outside left out of
// mailOriginOf, no wake is typed, and the daemon logs "wakes nothing". With it
// left out of queue-prompt's origin, the queued prompt is dropped.
func TestMailFromAHookWakesAnIdleAgent(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-hook-wake"
	hook := filepath.Join(base, "mail-hook.sh")
	script := `[ "$TUIOS_WINDOW_NAME" = trigger ] || exit 0
unset TUIOS_PANE_ID TUIOS_PANE_TOKEN
"` + tuiosBin + `" send-agent-message -s ` + session + ` -w idler 'hook mail'
"` + tuiosBin + `" queue -s ` + session + ` -w idler 'queued-from-hook'
`
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[hooks]\nafter-new-window = [\"sh '"+hook+"'\"]\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	idle := startMailAgent(t, base, session, "idler")

	if out, err := tuiosCLI(t, base, "new-window", "trigger", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window trigger: %v\n%s", err, out)
	}
	wake := `GOT[You have 1 unread tuios message. Read it with: tuios read-agent-messages -s ` + session + ` -w ` + idle[:8] + ` --unread]`
	deadline := time.Now().Add(mailWakeWait + uiTimeout)
	var out string
	for {
		_, out = gotLines(t, base, session, idle)
		if strings.Contains(out, "GOT[queued-from-hook]") && strings.Contains(out, wake) {
			break
		}
		if time.Now().After(deadline) {
			hooks, _ := tuiosCLI(t, base, "list-hooks")
			t.Fatalf("the hook's queued prompt and wake did not both reach the agent:\n%s\nhooks:\n%s", out, hooks)
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Room for a second wake to show, if there were one.
	time.Sleep(mailWakeWait)
	if n, out := gotLines(t, base, session, idle); n != 2 {
		t.Fatalf("the agent got %d typed lines, want the queued prompt and one wake:\n%s", n, out)
	}
}
