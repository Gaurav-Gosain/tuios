package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// whoamiOut is what tuios whoami --json prints.
type whoamiOut struct {
	Success          bool     `json:"success"`
	InPane           bool     `json:"in_pane"`
	WindowID         string   `json:"window_id"`
	Name             string   `json:"name"`
	Session          string   `json:"session"`
	Grants           []string `json:"grants"`
	FocusedWindowID  string   `json:"focused_window_id"`
	CurrentWorkspace int      `json:"current_workspace"`
	Clients          []struct {
		ClientID string `json:"client_id"`
		Session  string `json:"session"`
	} `json:"clients"`
	Daemon struct {
		Version string `json:"version"`
		PID     int    `json:"pid"`
		Socket  string `json:"socket"`
	} `json:"daemon"`
	StaleEnv []string `json:"stale_env"`
}

// TestWhoamiReportsTheIDsThatExistNow runs tuios whoami in a pane of a real
// daemon with a client attached, before and after the session is renamed, with
// a lying TUIOS_PANE_ID, and from outside every pane.
//
// How it could pass wrongly, written down first:
//   - whoami could echo the environment. Then the run after the rename would
//     print the old session name, which the pane's TUIOS_SESSION still holds,
//     and the run with a false TUIOS_PANE_ID would print that id. Both are
//     checked against what the daemon says, and both must name the variable
//     under stale_env.
//   - It could report the pane the person has in focus as the caller's pane.
//     The pane that runs whoami is not focused, so window_id and
//     focused_window_id must differ, and each must match list-windows.
//   - It could count its own CLI connection as a client. Only a client with a
//     session counts, and the attached client must be there with the session's
//     current name.
//   - A file read before the command finished could hold half a document. The
//     pane writes to a temporary name and renames it when the command is done.
//
// The artifact is the four JSON documents and the screen, under
// artifactDir(t).
//
// Negative control: with staleEnv returning nothing, or with the session taken
// from TUIOS_SESSION instead of pane-grants, the test fails on the run after
// the rename.
func TestWhoamiReportsTheIDsThatExistNow(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	const renamed = "e2e-whoami"
	dir := artifactDir(t)

	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "worker", "--no-focus"); err != nil {
		t.Fatalf("new-window failed: %v\n%s", err, out)
	}
	ids := grantWindowIDs(t, base, sess)
	if len(ids) != 2 {
		t.Fatalf("want two windows, got %v", ids)
	}
	focused, worker := ids[0], ids[1]

	current := sess
	// inPane runs whoami in the worker pane, with prefix before the command,
	// and returns what it printed.
	run := 0
	inPane := func(prefix, name string) whoamiOut {
		t.Helper()
		run++
		final := filepath.Join(base, name+".json")
		line := prefix + tuiosBin + " whoami --json > " + final + ".tmp; mv " + final + ".tmp " + final + "\n"
		if out, err := tuiosCLI(t, base, "send-text", "-s", current, "-w", worker, line); err != nil {
			t.Fatalf("send-text into the worker: %v\n%s", err, out)
		}
		waitUntil(t, fmt.Sprintf("whoami run %d to write %s", run, name), uiTimeout, func() (bool, string) {
			if _, err := os.Stat(final); err == nil {
				return true, ""
			}
			out, _ := tuiosCLI(t, base, "capture-pane", "-s", current, "-w", worker)
			return false, out
		})
		raw, err := os.ReadFile(final)
		if err != nil {
			t.Fatal(err)
		}
		return parseWhoami(t, dir, name, raw)
	}

	// 1. In the worker pane, as it started.
	first := inPane("", "in-pane")
	if !first.Success || !first.InPane {
		t.Fatalf("ASSERTION: whoami in a pane says it runs in no pane: %+v", first)
	}
	if first.WindowID != worker || first.Session != sess || first.Name != "worker" {
		t.Fatalf("ASSERTION: whoami names pane %q %q in session %q, want %q worker in %q", first.WindowID, first.Name, first.Session, worker, sess)
	}
	if first.FocusedWindowID != focused || first.FocusedWindowID == first.WindowID {
		t.Fatalf("ASSERTION: focused_window_id is %q, want the focused pane %q, not the caller", first.FocusedWindowID, focused)
	}
	if !slices.Contains(first.Grants, "admin") {
		t.Fatalf("ASSERTION: a pane in open mode holds admin, whoami says %v", first.Grants)
	}
	wantClient(t, first, sess)
	if len(first.StaleEnv) != 0 {
		t.Fatalf("ASSERTION: nothing is out of date yet, whoami says %v", first.StaleEnv)
	}
	if first.Daemon.PID == 0 || first.Daemon.Socket == "" || first.Daemon.Version == "" {
		t.Fatalf("ASSERTION: whoami does not name the daemon: %+v", first.Daemon)
	}

	// 2. After a rename, TUIOS_SESSION in the pane is out of date.
	if out, err := tuiosCLI(t, base, "rename-session", sess, renamed); err != nil {
		t.Fatalf("rename-session: %v\n%s", err, out)
	}
	current = renamed
	after := inPane("", "after-rename")
	if after.Session != renamed {
		t.Fatalf("ASSERTION: after the rename whoami says session %q, want %q", after.Session, renamed)
	}
	if !slices.Contains(after.StaleEnv, "TUIOS_SESSION") {
		t.Fatalf("ASSERTION: TUIOS_SESSION still holds %q, but stale_env is %v", sess, after.StaleEnv)
	}
	wantClient(t, after, renamed)

	// 3. A false TUIOS_PANE_ID does not move the caller.
	lying := inPane("TUIOS_PANE_ID=deadbeef00000000 ", "false-pane-id")
	if lying.WindowID != worker {
		t.Fatalf("ASSERTION: with a false TUIOS_PANE_ID whoami says pane %q, want %q", lying.WindowID, worker)
	}
	if !slices.Contains(lying.StaleEnv, "TUIOS_PANE_ID") {
		t.Fatalf("ASSERTION: the false TUIOS_PANE_ID is not under stale_env: %v", lying.StaleEnv)
	}

	// 4. Outside every pane.
	out, err := tuiosCLI(t, base, "whoami", "--json")
	if err != nil {
		t.Fatalf("whoami outside a pane: %v\n%s", err, out)
	}
	outside := parseWhoami(t, dir, "outside", []byte(out))
	if outside.InPane || outside.WindowID != "" || outside.Session != "" {
		t.Fatalf("ASSERTION: outside every pane whoami names a pane: %+v", outside)
	}
	wantClient(t, outside, renamed)

	saveArtifact(t, term, dir, "screen")
}

// parseWhoami decodes one whoami document and keeps it as an artifact.
func parseWhoami(t *testing.T, dir, name string, raw []byte) whoamiOut {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
	var w whoamiOut
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("whoami %s: %v\n%s", name, err, raw)
	}
	return w
}

// wantClient fails unless the attached client is listed, showing sess, and no
// connection without a session is.
func wantClient(t *testing.T, w whoamiOut, sess string) {
	t.Helper()
	found := false
	for _, c := range w.Clients {
		if c.Session == "" {
			t.Fatalf("ASSERTION: whoami lists a connection with no session: %+v", w.Clients)
		}
		if c.Session == sess {
			found = true
		}
	}
	if !found {
		t.Fatalf("ASSERTION: the client attached to %s is not listed: %+v", sess, w.Clients)
	}
}
