package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mailReadScript is an agent pane dropped to read,write,mail,task, as a
// sandbox wrapper leaves it, that runs the read command of its mail notice as
// the notice prints it, with the pane's short id. It then reads the inbox of
// another pane by that pane's short id, and reads its own again for unread
// mail. Each step prints its exit status.
const mailReadScript = `T="$1"; S="$2"; MINE="$3"; OTHER="$4"; OUT="$5"; AGAIN="$6"
"$T" set-pane-grants --grants read,write,mail,task >/dev/null; echo "GRANTS=$?"
"$T" read-agent-messages -s "$S" -w "$MINE" --unread --json >"$OUT" 2>&1; echo "OWN=$?"
"$T" read-agent-messages -s "$S" -w "$OTHER" --unread >/dev/null 2>&1; echo "OTHER=$?"
"$T" read-agent-messages -s "$S" -w "$MINE" --unread --json >"$AGAIN" 2>&1; echo "AGAIN=$?"
echo READER-DONE
`

// TestMailNoticeCommandReadsWithShortID runs the command a mail notice tells
// an agent to run, from a pane held to its own session. The notice names the
// pane by its short id, and the grant check compared that with the full id,
// so the pane was refused the command tuios had just given it.
//
// How this could pass wrongly, written down first:
//   - The short id could pass because the check stopped looking at to at
//     all. Reading another pane's inbox by its short id must still fail.
//   - The command could pass and read nothing, or read the ring without
//     marking the mail read. The first read must hold the mail, and the
//     second --unread read must hold none.
//
// The artifact is mail-read-short-id.log: what the reader printed and both
// reads.
func TestMailNoticeCommandReadsWithShortID(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-mail-short"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	for _, name := range []string{"reader", "other"} {
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", session, "--no-focus"); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
	}
	reader := windowID(t, base, session, "reader")
	other := windowID(t, base, session, "other")
	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", reader, "--from", other, "short-id-marker-3a8"); err != nil {
		t.Fatalf("send-agent-message: %v\n%s", err, out)
	}

	var log strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "mail-read-short-id.log")
		if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
			t.Errorf("save the artifact: %v", err)
		}
		t.Logf("artifact: %s", path)
	}()

	script := filepath.Join(base, "mail-reader.sh")
	if err := os.WriteFile(script, []byte(mailReadScript), 0o755); err != nil {
		t.Fatal(err)
	}
	first, again := filepath.Join(base, "first.json"), filepath.Join(base, "again.json")
	run := fmt.Sprintf("sh '%s' '%s' '%s' '%s' '%s' '%s' '%s'\n", script, tuiosBin, session, reader[:8], other[:8], first, again)
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", reader, run); err != nil {
		t.Fatalf("start the reader: %v\n%s", err, out)
	}
	waitCapture(t, base, session, reader, "READER-DONE")
	shown := capture(t, base, session, reader)
	fmt.Fprintf(&log, "== the reader pane (%s) printed:\n%s\n", reader[:8], shown)
	for _, want := range []string{"GRANTS=0", "OWN=0", "AGAIN=0"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("ASSERTION: the reader did not print %s:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "OTHER=0") {
		t.Fatalf("ASSERTION: a pane read another pane's inbox by its short id:\n%s", shown)
	}

	got, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&log, "== the notice command, by short id:\n%s\n", got)
	if !strings.Contains(string(got), "short-id-marker-3a8") {
		t.Fatalf("ASSERTION: the notice command did not read the mail:\n%s", got)
	}
	got, err = os.ReadFile(again)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&log, "== the second --unread read:\n%s\n", got)
	if strings.Contains(string(got), "short-id-marker-3a8") {
		t.Fatalf("ASSERTION: the notice command left the mail unread:\n%s", got)
	}
}
