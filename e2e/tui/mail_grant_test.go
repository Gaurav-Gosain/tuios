package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mailSenderScript is an agent pane held to its own session: it drops its own
// grants to read,write,mail,task, as a sandbox wrapper does before it execs
// the agent, then tries to type into an agent of another session, lists the
// agents of every session, and mails the one in the other session. Each step
// prints its exit status, so the capture says what the daemon allowed.
const mailSenderScript = `T="$1"; S="$2"; W="$3"; OUT="$4"
"$T" set-pane-grants --grants read,write,mail,task >/dev/null; echo "GRANTS=$?"
"$T" send-text -s "$S" -w "$W" 'typed-marker-9c1' >/dev/null 2>&1; echo "SENDTEXT=$?"
"$T" capture-pane -s "$S" -w "$W" >/dev/null 2>&1; echo "CAPTURE=$?"
"$T" list-agents --select "session:$S" --json >"$OUT" 2>&1; echo "LIST=$?"
"$T" send-agent-message -s "$S" -w "$W" --subject 'subject-marker-5e2' 'body-marker-0b7 the app needs a queue'; echo "MAIL=$?"
echo SENDER-DONE
`

// TestMailGrantReachesAnotherSessionByMailOnly is the setup the grant is for:
// one session per repository, and an agent in one that is dropped to its own
// session (an OS sandbox around it) and still has to tell the agent of another
// session something. A real pane drops its own grants, then works against a
// real daemon.
//
// How this could pass wrongly, written down first:
//   - The grant could open more than mail. The sender also tries send-text
//     and capture-pane into the other session, and both must be refused.
//   - The listing could hand over what the pane may not read. The row of the
//     other session's agent must carry limited and no cwd.
//   - The mail could arrive under another name. Its from must be the
//     sender's own pane, as the daemon placed it.
//   - The wake could carry the mail into the agent's prompt, or not come at
//     all for a sender that cannot type there. The agent at rest must get
//     the one fixed line and no marker text.
//
// The artifact is mail-grant.log in the test's artifact directory: what the
// sender printed, the listing it got, what the agent was typed, and the mail
// as the person reads it.
//
// Negative controls (NEGATIVE_CONTROLS.md): with the mail case cut from
// checkGrants, MAIL is non-zero. With mailWakeRefusal not consulted in
// queueOriginRefusal, the agent is never woken.
func TestMailGrantReachesAnotherSessionByMailOnly(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const app, infra = "e2e-mail-app", "e2e-mail-infra"
	for _, s := range []string{app, infra} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	agent := startMailAgent(t, base, infra, "infra-agent")
	if out, err := tuiosCLI(t, base, "new-window", "sender", "-s", app, "--no-focus"); err != nil {
		t.Fatalf("new-window sender: %v\n%s", err, out)
	}
	sender := windowID(t, base, app, "sender")

	var log strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "mail-grant.log")
		if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
			t.Errorf("save the artifact: %v", err)
		}
		t.Logf("artifact: %s", path)
	}()

	script := filepath.Join(base, "mail-sender.sh")
	if err := os.WriteFile(script, []byte(mailSenderScript), 0o755); err != nil {
		t.Fatal(err)
	}
	listing := filepath.Join(base, "listing.json")
	run := fmt.Sprintf("sh '%s' '%s' '%s' '%s' '%s'\n", script, tuiosBin, infra, agent, listing)
	if out, err := tuiosCLI(t, base, "send-text", "-s", app, "-w", sender, run); err != nil {
		t.Fatalf("start the sender: %v\n%s", err, out)
	}
	waitCapture(t, base, app, sender, "SENDER-DONE")
	sent := time.Now()
	shown := capture(t, base, app, sender)
	fmt.Fprintf(&log, "== the sender pane (%s) printed:\n%s\n", sender[:8], shown)
	for _, want := range []string{"GRANTS=0", "LIST=0", "MAIL=0"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("ASSERTION: the sender did not print %s:\n%s", want, shown)
		}
	}
	for _, refused := range []string{"SENDTEXT=0", "CAPTURE=0"} {
		if strings.Contains(shown, refused) {
			t.Fatalf("ASSERTION: the mail grant let the sender do more than mail (%s):\n%s", refused, shown)
		}
	}

	list, err := os.ReadFile(listing)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&log, "== list-agents --select session:%s from the sender:\n%s\n", infra, list)
	if !strings.Contains(string(list), agent) || !strings.Contains(string(list), `"limited": true`) || strings.Contains(string(list), `"cwd"`) {
		t.Fatalf("ASSERTION: the sender's listing of the other session is not cut to what addressing needs:\n%s", list)
	}

	// The agent at rest gets the fixed line, and nothing the sender wrote.
	waitUntil(t, "the agent in the other session is woken for its mail", time.Until(sent.Add(mailWakeWait+uiTimeout)), func() (bool, string) {
		n, out := gotLines(t, base, infra, agent)
		if strings.Contains(out, "marker") {
			fmt.Fprintf(&log, "== the agent was typed:\n%s\n", out)
			t.Fatalf("ASSERTION: text the sender chose was typed into the agent:\n%s", out)
		}
		if n == 1 && strings.Contains(out, "GOT[You have 1 unread tuios message. Read it with: tuios read-agent-messages -s "+infra+" -w "+agent[:8]+" --unread]") {
			fmt.Fprintf(&log, "== the agent was typed, %v after the mail:\n%s\n", time.Since(sent).Round(time.Millisecond), out)
			return true, out
		}
		return false, out
	})

	mail, err := tuiosCLI(t, base, "read-agent-messages", "-s", infra, "-w", agent, "--json")
	fmt.Fprintf(&log, "== the mail as the person reads it:\n%s\n", mail)
	if err != nil || !strings.Contains(mail, `"from": "`+sender+`"`) || !strings.Contains(mail, "body-marker-0b7") {
		t.Fatalf("ASSERTION: the mail is not in the agent's inbox from the sender's pane: %v\n%s", err, mail)
	}
}
