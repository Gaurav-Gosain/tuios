package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
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
