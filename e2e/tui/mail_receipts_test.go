package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// sentRow is the line read-agent-messages --sent prints for message id, or
// "" when it prints none.
func sentRow(out, id string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#"+id+" ") {
			return line
		}
	}
	return ""
}

var sentIDRe = regexp.MustCompile(`message (\d+) (delivered|queued)`)

// sendAndID sends text from one window to another and returns the message
// id the send printed, and the whole output.
func sendAndID(t *testing.T, base string, env []string, args ...string) (string, string) {
	t.Helper()
	out, err := tuiosCLIEnv(t, base, env, append([]string{"send-agent-message"}, args...)...)
	if err != nil {
		t.Fatalf("send-agent-message %v: %v\n%s", args, err, out)
	}
	m := sentIDRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("send-agent-message printed no message id:\n%s", out)
	}
	return m[1], out
}

// TestASenderSeesWhenItsMailIsRead sends mail from a pane in one session to a
// pane in another, the way a worker reports to its lead, and reads the
// sender's view of it with read-agent-messages --sent while the recipient
// peeks and then reads. The recipient's copy has always recorded when it was
// seen and read; nothing showed the sender. A worker whose reports were read
// and acted on saw only "queued" on its side and mailed again to ask whether
// they arrived.
//
// How this could pass wrongly, written down first:
//   - The view could show the reader's state from the sender's own session,
//     where the message is not. The two panes are in two sessions.
//   - A --sent read could mark the mail read itself. The first --sent read
//     comes before the recipient reads, and must say unread.
//   - Seen and read could be one state. The recipient peeks first (seen),
//     then reads (read), and the sender's view must change at each step.
//   - Another pane's mail could show. A third pane sends too, and its
//     message must not be in the first sender's view.
//
// The outputs are saved under artifactDir as text.
//
// Negative control: on origin/main the send prints queued and --sent is an
// unknown flag, so the first assertion fails.
func TestASenderSeesWhenItsMailIsRead(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, s := range []string{"lead", "work"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	lead := windowIDs(t, base, "lead")[0]
	worker := windowIDs(t, base, "work")[0]
	if out, err := tuiosCLI(t, base, "new-window", "-s", "work", "other", "--no-focus"); err != nil {
		t.Fatalf("new-window other: %v\n%s", err, out)
	}
	var other string
	for _, id := range windowIDs(t, base, "work") {
		if id != worker {
			other = id
		}
	}
	dir := artifactDir(t)
	save := func(name, text string) {
		_ = os.WriteFile(filepath.Join(dir, name+".txt"), []byte(text), 0o644)
	}

	id, out := sendAndID(t, base, nil, "-s", "lead", "-w", lead, "--from", worker, "report: tests pass")
	save("1-send", out)
	if !strings.Contains(out, "delivered to") || strings.Contains(out, "queued") {
		t.Fatalf("ASSERTION: a send into another session's inbox did not say delivered:\n%s", out)
	}
	otherID, _ := sendAndID(t, base, nil, "-s", "lead", "-w", lead, "--from", other, "not yours")

	sent := func(step string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "read-agent-messages", "--sent", "-w", worker)
		if err != nil {
			t.Fatalf("read-agent-messages --sent: %v\n%s", err, out)
		}
		save(step, out)
		return out
	}

	out = sent("2-sent-unread")
	row := sentRow(out, id)
	if row == "" || !strings.Contains(row, "unread") || strings.Contains(row, "read ") {
		t.Fatalf("ASSERTION: --sent does not show message %s as delivered and unread:\n%s", id, out)
	}
	if sentRow(out, otherID) != "" {
		t.Fatalf("ASSERTION: --sent shows message %s, which another pane sent:\n%s", otherID, out)
	}
	if !strings.Contains(row, "lead") {
		t.Fatalf("ASSERTION: --sent does not name the session the message went to:\n%s", out)
	}
	// --sent itself marked nothing.
	if out := sent("3-sent-again"); !strings.Contains(sentRow(out, id), "unread") {
		t.Fatalf("ASSERTION: reading --sent marked the mail read:\n%s", out)
	}

	// Only the recipient pane marks its own mail seen, so the peek runs in it,
	// as the agent there would run it.
	if out, err := tuiosCLI(t, base, "send-text", "-s", "lead", "-w", lead,
		tuiosBin+" read-agent-messages -w \"$TUIOS_PANE_ID\" --peek\n"); err != nil {
		t.Fatalf("peek in the lead pane: %v\n%s", err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		out = sent("4-sent-seen")
		row := sentRow(out, id)
		if strings.Contains(row, "seen") && !strings.Contains(row, "unread") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: after the recipient peeked, --sent does not say seen:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if out, err := tuiosCLI(t, base, "read-agent-messages", "-s", "lead", "-w", lead); err != nil {
		t.Fatalf("read: %v\n%s", err, out)
	}
	out = sent("5-sent-read")
	if row := sentRow(out, id); !regexp.MustCompile(`read (just now|\d+s ago)`).MatchString(row) {
		t.Fatalf("ASSERTION: after the recipient read it, --sent does not say read and when:\n%s", out)
	}

	// The thread view says the same, for the sender reading the conversation.
	thread, err := tuiosCLI(t, base, "read-agent-messages", "-s", "lead", "--thread", id)
	if err != nil {
		t.Fatalf("read --thread: %v\n%s", err, thread)
	}
	save("6-thread", thread)
	if !regexp.MustCompile(`#` + id + ` .*read (just now|\d+s ago)`).MatchString(thread) {
		t.Fatalf("ASSERTION: the thread view does not say message %s was read:\n%s", id, thread)
	}
}

// TestASenderSeesWhenItsMailIsReadOnAnotherMachine sends mail to a pane on a
// linked machine, over the real link, and reads the sender's view while the
// far pane reads it, and then with the link down.
//
// How this could pass wrongly, written down first:
//   - The view could be built from this machine's copy only, which has none.
//     The message goes to a session on build, so only build can say.
//   - The far machine could tell this one about mail it did not send. A pane
//     on build sends to the same inbox, and its message must not show.
//   - A link that is down could read as unread. With the link cut, the row
//     must say the state cannot be read now.
//
// Negative control: on origin/main --sent is an unknown flag.
func TestASenderSeesWhenItsMailIsReadOnAnotherMachine(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	gate := filepath.Join(base, "link-down")
	ssh := writeGatedFakeSSH(t, base, remote, gate)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, remote)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start this machine's daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	here := windowIDs(t, base, "home")[0]
	far := windowIDs(t, remote, "far")[0]
	dir := artifactDir(t)
	save := func(name, text string) {
		_ = os.WriteFile(filepath.Join(dir, name+".txt"), []byte(text), 0o644)
	}

	id, out := sendAndID(t, base, env, "-s", "build:far", "-w", far, "--from", here, "deployed to staging")
	save("1-send", out)
	// A pane on build writes to the same inbox.
	if out, err := tuiosCLI(t, remote, "new-window", "-s", "far", "notes", "--no-focus"); err != nil {
		t.Fatalf("new-window on build: %v\n%s", err, out)
	}
	var farOther string
	for _, w := range windowIDs(t, remote, "far") {
		if w != far {
			farOther = w
		}
	}
	localID, _ := sendAndID(t, remote, nil, "-s", "far", "-w", far, "--from", farOther, "a note from build")

	sent := func(step string) string {
		t.Helper()
		out, err := tuiosCLIEnv(t, base, env, "read-agent-messages", "--sent", "-w", here)
		if err != nil {
			t.Fatalf("read-agent-messages --sent: %v\n%s", err, out)
		}
		save(step, out)
		return out
	}
	out = sent("2-sent-unread")
	row := sentRow(out, id)
	if row == "" || !strings.Contains(row, "build") || !strings.Contains(row, "unread") {
		dumpLinkLogs(t, base, remote)
		t.Fatalf("ASSERTION: --sent does not show the mail on build as delivered and unread:\n%s", out)
	}
	if strings.Contains(out, "a note from build") || (localID != id && sentRow(out, localID) != "") {
		t.Fatalf("ASSERTION: --sent shows build's own mail:\n%s", out)
	}

	if out, err := tuiosCLI(t, remote, "read-agent-messages", "-s", "far", "-w", far); err != nil {
		t.Fatalf("read on build: %v\n%s", err, out)
	}
	out = sent("3-sent-read")
	if row := sentRow(out, id); !regexp.MustCompile(`read (just now|\d+s ago)`).MatchString(row) {
		t.Fatalf("ASSERTION: after build's pane read it, --sent does not say read:\n%s", out)
	}

	cutTheLink(t, gate)
	deadline := time.Now().Add(30 * time.Second)
	for {
		out = sent("4-sent-link-down")
		if strings.Contains(out, "Not reachable now: build") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: with the link down, --sent does not say build cannot be read:\n%s", out)
		}
		time.Sleep(500 * time.Millisecond)
	}
	openTheLink(t, gate)
}
