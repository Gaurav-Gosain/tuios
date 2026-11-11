package tuie2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMailFromYourLeadIsMarked is item 2 of the lead's list
// (design/lead-ergonomics.md): a worker read its lead's assignment as another
// agent's data and idled for two hours. Mail from the pane that started the
// reader now reads "from your lead", worked out when it is read, so the hint
// can say such mail directs the work.
//
// How it could pass wrongly, written down first:
//   - the label could follow the from the sender claims: a peer pane and a call
//     from outside every pane both send with --from naming the lead, and
//     neither may be marked;
//   - it could be stored with the message, so a lead that closed stays one:
//     after the lead pane closes, its own earlier mail must read without it;
//   - every mail could be marked: the peer's mail is read in the same listing.
//
// NEGATIVE CONTROL: on main no mail reads "from your lead".
func TestMailFromYourLeadIsMarked(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "crew"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	lead := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "lead", "--no-focus", "--print-id"))
	peer := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "peer", "--no-focus", "--print-id"))
	typeIn := func(pane, line string) {
		t.Helper()
		mustCLI(t, base, "send-text", "-s", sess, "-w", pane, line+"\n")
	}
	for _, p := range []string{lead, peer} {
		typeIn(p, "tuios set-agent-state idle --harness claude-code -m ready")
	}
	waitUntil(t, "the lead pane reads as an agent", 20*time.Second, func() (bool, string) {
		out, _ := tuiosCLI(t, base, "get-agent-state", "-s", sess, "-w", lead)
		return strings.Contains(out, "idle"), out
	})
	started := filepath.Join(base, "started")
	typeIn(lead, "tuios start-agent 'sleep 600' -s "+sess+" --name worker --ready-timeout 3000 > "+started+" 2>&1; echo DONE >> "+started)
	waitForFileText(t, started, func(s string) bool { return strings.Contains(s, "DONE") }, "start-agent never came back in the lead pane")

	sent := filepath.Join(base, "sent")
	typeIn(lead, "tuios send-agent-message -s "+sess+" -w worker 'LEAD-ASSIGNMENT' >> "+sent+" 2>&1")
	typeIn(peer, "tuios send-agent-message -s "+sess+" -w worker --from "+lead+" 'PEER-CLAIMS-LEAD' >> "+sent+" 2>&1")
	mustCLI(t, base, "send-agent-message", "-s", sess, "-w", "worker", "--from", lead, "OUTSIDE-CLAIMS-LEAD")

	var mail string
	waitUntil(t, "the worker has all three mails", 20*time.Second, func() (bool, string) {
		mail, _ = tuiosCLI(t, base, "read-agent-messages", "-s", sess, "-w", "worker")
		return strings.Count(mail, "-CLAIMS-LEAD")+strings.Count(mail, "LEAD-ASSIGNMENT") == 3, mail
	})
	marked := map[string]bool{}
	for _, block := range strings.Split(mail, "\n#") {
		for _, body := range []string{"LEAD-ASSIGNMENT", "PEER-CLAIMS-LEAD", "OUTSIDE-CLAIMS-LEAD"} {
			if strings.Contains(block, body) {
				marked[body] = strings.Contains(block, "from your lead")
			}
		}
	}
	if !marked["LEAD-ASSIGNMENT"] {
		t.Errorf("ASSERTION: the lead's own mail does not read from your lead:\n%s", mail)
	}
	if marked["PEER-CLAIMS-LEAD"] || marked["OUTSIDE-CLAIMS-LEAD"] {
		t.Errorf("ASSERTION: a mail that only claims to be from the lead reads from your lead:\n%s", mail)
	}

	mustCLI(t, base, "close-window", "-s", sess, lead)
	waitUntil(t, "the lead's mail no longer reads from your lead once the lead closed", 20*time.Second, func() (bool, string) {
		after, _ := tuiosCLI(t, base, "read-agent-messages", "-s", sess, "-w", "worker")
		return strings.Contains(after, "LEAD-ASSIGNMENT") && !strings.Contains(after, "from your lead"), after
	})
	saveText(t, "mail-from-lead", mail)
}
