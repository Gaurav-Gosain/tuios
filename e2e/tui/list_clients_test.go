package tuie2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestListClientsTracksSwitcherSwitches checks the CLI listing and event stream
// against a real client moving between two real daemon sessions.
func TestListClientsTracksSwitcherSwitches(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"client-one", "client-two"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "client-one"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	type clientRow struct {
		ClientID string `json:"client_id"`
		Session  string `json:"session"`
		Attached *bool  `json:"attached"`
	}
	list := func() []clientRow {
		out, err := tuiosCLI(t, base, "list-clients", "--json")
		if err != nil {
			t.Fatalf("list clients: %v\n%s", err, out)
		}
		var rows []clientRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("decode clients: %v\n%s", err, out)
		}
		return rows
	}
	var clientID string
	for _, row := range list() {
		if row.Session == "client-one" {
			clientID = row.ClientID
		}
	}
	if clientID == "" {
		t.Fatal("list-clients did not include the attached client in client-one")
	}

	// subscribe starts `tuios subscribe` with args and returns its output after
	// the acknowledgement line.
	subscribe := func(args ...string) *bufio.Scanner {
		ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
		cmd := exec.CommandContext(ctx, tuiosBin, append([]string{"subscribe", "--types", "client-session-changed"}, args...)...)
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start subscribe: %v", err)
		}
		// Waited on only at cleanup: Wait closes stdout, and the last event is
		// still unread when the process exits after --count events.
		t.Cleanup(func() {
			cancel()
			_ = cmd.Wait()
		})
		scan := bufio.NewScanner(stdout)
		if !scan.Scan() {
			t.Fatalf("subscribe %v printed no acknowledgement: %v\n%s", args, scan.Err(), stderr.String())
		}
		return scan
	}
	next := func(scan *bufio.Scanner, what string) clientRow {
		if !scan.Scan() {
			t.Fatalf("subscribe did not print %s: %v", what, scan.Err())
		}
		var event clientRow
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			t.Fatalf("decode event: %v\n%s", err, scan.Text())
		}
		return event
	}
	is := func(event clientRow, session string, attached bool) bool {
		return event.ClientID == clientID && event.Session == session && event.Attached != nil && *event.Attached == attached
	}
	all := subscribe("--count", "4")
	filtered := subscribe("--session", "client-one", "--count", "1")

	openSwitcherOn(t, term, "client-two", "client-two")

	if ev := next(all, "the detach event"); !is(ev, "client-one", false) {
		t.Fatalf("first switch event = %+v, want client %s leaving client-one", ev, clientID)
	}
	if ev := next(all, "the attach event"); !is(ev, "client-two", true) {
		t.Fatalf("second switch event = %+v, want client %s entering client-two", ev, clientID)
	}
	if ev := next(filtered, "the detach event to a --session client-one reader"); !is(ev, "client-one", false) {
		t.Fatalf("session-filtered event = %+v, want client %s leaving client-one", ev, clientID)
	}
	moved := false
	for _, row := range list() {
		moved = moved || row.ClientID == clientID && row.Session == "client-two"
	}
	if !moved {
		t.Fatalf("client %s did not move to client-two", clientID)
	}

	if out, err := tuiosCLI(t, base, "rename-session", "client-two", "client-renamed"); err != nil {
		t.Fatalf("rename client-two: %v\n%s", err, out)
	}
	if ev := next(all, "the rename event"); !is(ev, "client-renamed", true) {
		t.Fatalf("rename event = %+v, want client %s in client-renamed", ev, clientID)
	}
	renamed := false
	for _, row := range list() {
		renamed = renamed || row.ClientID == clientID && row.Session == "client-renamed"
	}
	if !renamed {
		t.Fatalf("client %s is not listed in client-renamed", clientID)
	}
	saveArtifact(t, term, artifactDir(t), "client-switched")

	// A killed session is gone from the daemon before its clients leave it, so
	// the leave event has to carry the name the client attached under.
	killed := subscribe("--session", "client-renamed", "--count", "1")
	if out, err := tuiosCLI(t, base, "kill-session", "client-renamed"); err != nil {
		t.Fatalf("kill client-renamed: %v\n%s", err, out)
	}
	if ev := next(all, "the kill event"); !is(ev, "client-renamed", false) {
		t.Fatalf("kill event = %+v, want client %s leaving client-renamed", ev, clientID)
	}
	if ev := next(killed, "the kill event to a --session client-renamed reader"); !is(ev, "client-renamed", false) {
		t.Fatalf("session-filtered kill event = %+v, want client %s leaving client-renamed", ev, clientID)
	}
}
