package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSendKeysDoNotDismissAnInboxItem: a call the client signs with its
// attach nonce acts as the person, so a key from send-keys must not make one.
// The person opens the Inbox, and send-keys presses d on an item.
// The item must stay. The person's own d then dismisses it, so the key did
// reach the item.
//
// The negative control is in NEGATIVE_CONTROLS.md, "Signed calls from
// send-keys".
func TestSendKeysDoNotDismissAnInboxItem(t *testing.T) {
	base := t.TempDir()
	approvalSession(t, base, 1)
	art := artifactDir(t)
	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("no shell prompt: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "work", "-w", "0", "errored", "-m", "signed-probe-item"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	waitForAttention(t, base, nil, "signed-probe-item")

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("signed-probe-item", uiTimeout); err != nil {
		t.Fatalf("the Inbox did not show the item: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(700 * time.Millisecond)
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "d"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	time.Sleep(1500 * time.Millisecond)
	saveArtifact(t, term, art, "signed-after-send-keys")
	if out, _ := tuiosCLI(t, base, "list-attention"); !strings.Contains(out, "signed-probe-item") {
		t.Fatalf("a d from send-keys dismissed the item as the person:\n%s\n%s", out, term.Snapshot())
	}

	if err := term.SendKeys("d"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if !strings.Contains(out, "signed-probe-item") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the person's d did not dismiss the item:\n%s\n%s", out, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "signed-probe-item") }, uiTimeout)
	saveArtifact(t, term, art, "signed-person-dismissed")
}
