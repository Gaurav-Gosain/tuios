package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// attentionItems is list-attention --json under base, as kind and summary.
func attentionItems(t *testing.T, base string, env []string) []struct{ Kind, Summary, Name string } {
	t.Helper()
	out, err := tuiosCLIEnv(t, base, env, "list-attention", "--json")
	if err != nil {
		t.Fatalf("list-attention: %v\n%s", err, out)
	}
	var res struct {
		Items []struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
			Name    string `json:"name"`
		} `json:"items"`
	}
	if start := strings.Index(out, "{"); start < 0 || json.Unmarshal([]byte(out[start:]), &res) != nil {
		t.Fatalf("list-attention --json printed no JSON:\n%s", out)
	}
	items := make([]struct{ Kind, Summary, Name string }, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, struct{ Kind, Summary, Name string }{it.Kind, it.Summary, it.Name})
	}
	return items
}

// selfLinkName is the name a daemon on this machine gives itself on a link:
// the first label of the hostname, lowercased.
func selfLinkName(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Skip("no hostname")
	}
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(h)), ".")
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`).MatchString(name) {
		t.Skipf("the hostname %q is not a link name", h)
	}
	return name
}

// TestMailHeldForASignInIsToldOnBothMachines: this machine's link to build
// waits for the person to sign in (Tailscale SSH check mode), and a pane here
// sends build mail. The mail waits here. Before, the Inbox here said only how
// many messages wait, which reads the same as a machine that is off, and
// build's person was told nothing, though build's own link back to here was
// up. Mail between two machines sat for hours that way.
//
// How this could pass wrongly, written down first:
//   - The words could come from another item, such as the rail's sign-in. The
//     check reads the outbox item itself, by kind.
//   - build could be told by a link that carries the mail anyway. The link
//     here to build stays held for the sign-in until the end, so build can
//     only learn over its own link back.
//   - build's notice could leak the mail. It must hold the count and the
//     reason, and not the subject, the text or the sender.
//   - The notices could stay after the mail goes. The person signs in, the
//     mail is delivered, and both must close.
//
// Both daemons run on this machine, so each names the other by this
// machine's hostname, which is the name each gives itself on a link.
//
// Negative control: on origin/main the outbox item has no sign-in words and
// build has no item, so the first two assertions fail.
func TestMailHeldForASignInIsToldOnBothMachines(t *testing.T) {
	name := selfLinkName(t)
	base := t.TempDir()
	remote := remoteMachine(t)

	// This machine to build, through the Tailscale check stand-in.
	inner := writeFakeSSHTo(t, base, remote)
	wrapper, g := writeTailscaleSSH(t, base, inner)
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil { //nolint:gosec // the test's own opener
		t.Fatal(err)
	}
	writeConfig(t, base, "[appearance]\nlink_opener = \""+opener+"\"\n\n"+gatedHostTOML(name, "someone@gatedbox"))
	envHere := []string{"TUIOS_SSH=" + wrapper, "SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}

	// build back to this machine, a plain link.
	back := writeFakeSSHTo(t, remote, base)
	writeRemoteConfig(t, remote, "[hosts."+name+"]\naddr = \"someone@hubbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n")
	envFar := []string{"TUIOS_SSH=" + back}

	term := startIn(t, base, startOpts{cols: 120, rows: 34, args: []string{"new", "home"}, env: envHere})
	waitBoot(t, term)
	if out, err := tuiosCLIEnv(t, base, envHere, "run-command", "--session", "home", "NewWindow"); err != nil {
		t.Fatalf("open a pane here: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 1, "the pane here")
	killDaemon(t, remote)
	if out, err := tuiosCLIEnv(t, remote, envFar, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, name, "sign in") }, "this machine's link to build never waited for a sign-in")
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, _ := tuiosCLIEnv(t, remote, envFar, "hosts")
		if containsAll(out, name, "up") {
			break
		}
		if time.Now().After(deadline) {
			dumpLinkLogs(t, base, remote)
			t.Fatalf("build's link back to here never came up:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}

	here := windowIDs(t, base, "home")[0]
	far := windowIDs(t, remote, "far")[0]
	out, err := tuiosCLIEnv(t, base, envHere, "send-agent-message", "-s", name+":far", "-w", far,
		"--from", here, "--subject", "SECRET-SUBJECT", "SECRET-BODY deploy is done")
	saveSyncArtifact(t, "outbox-send.txt", out)
	if err != nil || !strings.Contains(out, "waits here") {
		dumpLinkLogs(t, base, remote)
		t.Fatalf("the send did not wait here for the link: %v\n%s", err, out)
	}

	// This machine: the outbox item says the link waits for a sign-in.
	var hereText string
	deadline = time.Now().Add(30 * time.Second)
	for {
		hereText = ""
		for _, it := range attentionItems(t, base, envHere) {
			if it.Kind == "outbox" {
				hereText = it.Summary
			}
		}
		if strings.Contains(hereText, "sign in") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the outbox item here does not say the link waits for a sign-in: %q", hereText)
		}
		time.Sleep(250 * time.Millisecond)
	}
	saveSyncArtifact(t, "outbox-item-here.txt", hereText)

	// The item's key opens the sign-in the link waits on.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("To send", uiTimeout); err != nil {
		t.Fatalf("the Inbox does not show the outbox item: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-outbox-sign-in")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(uiTimeout)
	for len(openedLinks(record)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: Enter on the outbox item did not open the sign-in page:\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := openedLinks(record); !strings.Contains(got[0], "login.tailscale.com") {
		t.Fatalf("ASSERTION: Enter on the outbox item opened %q, not the sign-in page", got)
	}

	// build: an item says this machine holds mail for it, and why, and
	// nothing of the mail itself.
	var farText string
	deadline = time.Now().Add(45 * time.Second)
	for {
		farText = ""
		for _, it := range attentionItems(t, remote, envFar) {
			if it.Kind == "held_mail" {
				farText = it.Name + " | " + it.Summary
			}
		}
		if farText != "" {
			break
		}
		if time.Now().After(deadline) {
			dumpLinkLogs(t, base, remote)
			t.Fatalf("ASSERTION: build's Inbox does not say mail waits for it")
		}
		time.Sleep(500 * time.Millisecond)
	}
	saveSyncArtifact(t, "held-mail-item-far.txt", farText)
	if !strings.Contains(farText, "1 message") || !strings.Contains(farText, "sign in") {
		t.Fatalf("ASSERTION: build's item does not give the count and the reason: %q", farText)
	}
	for _, leak := range []string{"SECRET-SUBJECT", "SECRET-BODY", here, here[:8]} {
		if strings.Contains(farText, leak) {
			t.Fatalf("ASSERTION: build's item names %q, which is the mail's: %q", leak, farText)
		}
	}

	// The person signs in. The mail goes, and both items close.
	g.signIn(t)
	deadline = time.Now().Add(60 * time.Second)
	for {
		ring, _ := tuiosCLI(t, remote, "read-agent-messages", "-s", "far", "--peek")
		openHere, openFar := false, false
		for _, it := range attentionItems(t, base, envHere) {
			openHere = openHere || it.Kind == "outbox"
		}
		for _, it := range attentionItems(t, remote, envFar) {
			openFar = openFar || it.Kind == "held_mail"
		}
		if strings.Contains(ring, "SECRET-BODY") && !openHere && !openFar {
			break
		}
		if time.Now().After(deadline) {
			dumpLinkLogs(t, base, remote)
			t.Fatalf("ASSERTION: after the sign-in: delivered %v, item here open %v, item on build open %v",
				strings.Contains(ring, "SECRET-BODY"), openHere, openFar)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
