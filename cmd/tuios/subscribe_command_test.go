package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/adrg/xdg"
)

// startSubscribeDaemon runs a real daemon in this process, on its own socket
// and with its state in a temp directory, so creating sessions cannot touch
// the developer's own saved sessions.
func startSubscribeDaemon(t *testing.T) *session.VerbClient {
	t.Helper()
	// Registered before the Setenv calls so it runs after they are undone,
	// and xdg reads the developer's directories back.
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	c, err := session.DialVerbClientAs("test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// subscribeLines runs tuios subscribe with opts and returns each printed line
// decoded.
func subscribeLines(t *testing.T, opts subscribeOptions) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runSubscribe(opts, &out) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runSubscribe: %v", err)
		}
	case <-time.After(10 * time.Second):
		// The stream is still open, so the events the test expected never
		// came. The daemon's cleanup closes the connection and ends it.
		t.Fatalf("runSubscribe did not print the %d lines expected within 10s", opts.count+1)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		lines = append(lines, m)
	}
	return lines
}

// TestSubscribeCommandResumes runs the command against a real daemon: the ack
// line prints the boot id, --after-seq replays what came after that seq, and
// a stale --boot-id prints a gap instead of a replay.
func TestSubscribeCommandResumes(t *testing.T) {
	c := startSubscribeDaemon(t)
	for _, name := range []string{"one", "two"} {
		if _, err := c.Call("new-session", map[string]any{"name": name, "window": false}); err != nil {
			t.Fatalf("new-session %s: %v", name, err)
		}
	}
	types := []string{session.EventSessionCreated}

	all := subscribeLines(t, subscribeOptions{types: types, resume: true, afterSeq: 0, count: 2})
	if len(all) != 3 {
		t.Fatalf("printed %d lines, want the ack and two events: %v", len(all), all)
	}
	ack := all[0]
	bootID, _ := ack["boot_id"].(string)
	if ack["type"] != session.EventSubscribed || bootID == "" || ack["replayed"] != float64(2) {
		t.Fatalf("ack = %v, want subscribed with a boot_id and replayed 2", ack)
	}
	if all[1]["session"] != "one" || all[2]["session"] != "two" {
		t.Fatalf("replayed %v, want sessions one then two", all[1:])
	}
	seqOne := uint64(all[1]["seq"].(float64))

	resumed := subscribeLines(t, subscribeOptions{types: types, resume: true, afterSeq: seqOne, bootID: bootID, count: 1})
	if resumed[0]["replayed"] != float64(1) || resumed[1]["session"] != "two" {
		t.Fatalf("resume after seq %d printed %v, want only session two", seqOne, resumed)
	}

	stale := subscribeLines(t, subscribeOptions{types: types, resume: true, afterSeq: seqOne, bootID: "an-older-boot", count: 1})
	if stale[1]["type"] != session.EventGap || stale[1]["reason"] != session.GapBootChanged {
		t.Fatalf("stale boot id printed %v, want a boot_changed gap", stale)
	}
}
