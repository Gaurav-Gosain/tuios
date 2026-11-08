package tuie2e

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestMailFromAPaneOfAnotherSession checks that a pane in one session can
// mail a pane in another by naming itself with its full window id, and the
// reply can go back the same way. A lead in one session and its workers in
// another could not mail each other: the sender had to be a window of the
// recipient's session. A name from another session still does not resolve.
func TestMailFromAPaneOfAnotherSession(t *testing.T) {
	base := t.TempDir()
	useShippedLooks(base)
	killDaemon(t, base)
	defer killDaemon(t, base)
	for _, s := range []string{"lead", "team"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", s, err, out)
		}
	}
	first := func(session string) (id, name string) {
		out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
		if err != nil {
			t.Fatalf("list-windows %s: %v\n%s", session, err, out)
		}
		var d struct {
			Windows []struct {
				ID          string `json:"window_id"`
				DisplayName string `json:"display_name"`
			} `json:"windows"`
		}
		if err := json.Unmarshal([]byte(out), &d); err != nil || len(d.Windows) == 0 {
			t.Fatalf("list-windows %s: %v\n%s", session, err, out)
		}
		return d.Windows[0].ID, d.Windows[0].DisplayName
	}
	lead, _ := first("lead")
	worker, workerName := first("team")

	send := func(session, to, from, text string) (string, error) {
		return tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", to, "--from", from, text)
	}
	read := func(session, w string) []map[string]any {
		out, err := tuiosCLI(t, base, "read-agent-messages", "-s", session, "-w", w, "--json")
		if err != nil {
			t.Fatalf("read %s/%s: %v\n%s", session, w, err, out)
		}
		var d struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("read %s/%s: %v\n%s", session, w, err, out)
		}
		return d.Messages
	}

	if out, err := send("lead", lead, worker, "worker report"); err != nil {
		t.Fatalf("a worker in team could not mail the lead in lead: %v\n%s", err, out)
	}
	got := read("lead", lead)
	if len(got) != 1 || got[0]["from"] != worker || !strings.Contains(got[0]["from_label"].(string), "team") {
		t.Fatalf("the lead's inbox should hold one message from %s, labelled with its session: %v", worker, got)
	}
	if out, err := send("team", worker, lead, "lead answer"); err != nil {
		t.Fatalf("the lead could not answer the worker: %v\n%s", err, out)
	}
	if got := read("team", worker); len(got) != 1 || got[0]["from"] != lead {
		t.Fatalf("the worker's inbox should hold one message from the lead: %v", got)
	}
	// A name is not an id: it never reaches into another session.
	if out, err := send("lead", lead, workerName, "by name"); err == nil {
		t.Fatalf("a sender named %q from another session was taken: %s", workerName, out)
	}
}

// TestMailAndAskFromAPaneByItsOwnName is the rest of issue #1624. A pane in
// one session that sends to or asks a pane in another, and names itself with
// --from NAME, was looked for only in the recipient's session. The call failed
// with window_not_found and listed the recipient's windows, so it read as a
// wrong target. The daemon knows the caller's pane, so a name or a short id
// also resolves in the caller's own session. A name of a third session still
// does not resolve, and that error is about from.
func TestMailAndAskFromAPaneByItsOwnName(t *testing.T) {
	base := t.TempDir()
	useShippedLooks(base)
	killDaemon(t, base)
	defer killDaemon(t, base)
	ids := map[string]string{}
	for s, name := range map[string]string{"lead": "boss", "team": "worker", "other": "stranger"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", s, err, out)
		}
		out, err := tuiosCLI(t, base, "list-windows", "-s", s, "--json")
		var d struct {
			Windows []struct {
				ID string `json:"window_id"`
			} `json:"windows"`
		}
		if err != nil || json.Unmarshal([]byte(out), &d) != nil || len(d.Windows) == 0 {
			t.Fatalf("list-windows %s: %v\n%s", s, err, out)
		}
		ids[s] = d.Windows[0].ID
		if out, err := tuiosCLI(t, base, "set-window", "-s", s, "-w", ids[s], "--name", name); err != nil {
			t.Fatalf("name the window of %s: %v\n%s", s, err, out)
		}
	}

	// inLead types a command line at the lead pane's shell and returns what
	// the pane shows once the line has printed its mark and exit status.
	n := 0
	inLead := func(args string) (int, string) {
		t.Helper()
		n++
		mark := fmt.Sprintf("RC%d", n)
		line := fmt.Sprintf("'%s' %s; echo %s=$?\n", tuiosBin, args, mark)
		if out, err := tuiosCLI(t, base, "send-text", "-s", "lead", "-w", ids["lead"], line); err != nil {
			t.Fatalf("type in the lead pane: %v\n%s", err, out)
		}
		re := regexp.MustCompile(mark + `=(\d+)`)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			pane, _ := tuiosCLI(t, base, "capture-pane", "-s", "lead", "-w", ids["lead"], "--scrollback")
			if m := re.FindStringSubmatch(pane); m != nil {
				var rc int
				_, _ = fmt.Sscan(m[1], &rc)
				return rc, pane
			}
			time.Sleep(200 * time.Millisecond)
		}
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", "lead", "-w", ids["lead"])
		t.Fatalf("the line %q did not finish in the lead pane:\n%s", line, pane)
		return 0, ""
	}

	if rc, pane := inLead("send-agent-message -s team -w worker --from boss 'by my own name'"); rc != 0 {
		t.Fatalf("ASSERTION: a pane could not mail another session by its own name:\n%s", pane)
	}
	out, err := tuiosCLI(t, base, "read-agent-messages", "-s", "team", "-w", ids["team"], "--json")
	var d struct {
		Messages []map[string]any `json:"messages"`
	}
	if err != nil || json.Unmarshal([]byte(out), &d) != nil {
		t.Fatalf("read the worker's inbox: %v\n%s", err, out)
	}
	if len(d.Messages) != 1 || d.Messages[0]["from"] != ids["lead"] || d.Messages[0]["from_label"] != "boss in lead" {
		t.Fatalf("ASSERTION: the worker's inbox should hold one message from %s labelled \"boss in lead\": %v", ids["lead"], d.Messages)
	}
	if rc, pane := inLead("ask-agent -s team -w worker --from " + ids["lead"][:8] + " --timeout 1500 'a question' >/dev/null"); rc != 0 {
		t.Fatalf("ASSERTION: a pane could not ask another session by its own short id:\n%s", pane)
	}

	// A name of a third session is neither the recipient's nor the caller's.
	rc, pane := inLead("send-agent-message -s team -w worker --from stranger 'not me'")
	if rc == 0 {
		t.Fatalf("ASSERTION: a sender named after a pane of a third session was taken:\n%s", pane)
	}
	if !strings.Contains(pane, "sender") {
		t.Fatalf("ASSERTION: the refusal of an unknown sender does not say it is about the sender:\n%s", pane)
	}
	saveSyncArtifact(t, "lead-pane.txt", pane)
}
