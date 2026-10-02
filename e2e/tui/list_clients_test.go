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

	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	cmd := exec.CommandContext(ctx, tuiosBin, "subscribe", "--types", "client-session-changed", "--count", "2")
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
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() {
		t.Fatalf("subscribe printed no acknowledgement: %v\n%s", scan.Err(), stderr.String())
	}

	openSwitcherOn(t, term, "client-two", "client-two")

	var switched bool
	for range 2 {
		if !scan.Scan() {
			t.Fatalf("subscribe did not print both switch events: %v\n%s", scan.Err(), stderr.String())
		}
		var event clientRow
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			t.Fatalf("decode event: %v\n%s", err, scan.Text())
		}
		switched = switched || event.ClientID == clientID && event.Session == "client-two"
	}
	if !switched {
		t.Fatalf("no client-session-changed event moved %s to client-two", clientID)
	}
	for _, row := range list() {
		if row.ClientID == clientID && row.Session == "client-two" {
			saveArtifact(t, term, artifactDir(t), "client-switched")
			return
		}
	}
	t.Fatalf("client %s did not move to client-two", clientID)
}
