package tuie2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Discussion #549 asked for tmux's attach -d and detach-client: one client
// per session, the newest attach wins, and the client that loses exits with a
// plain message instead of an error.

const (
	detachedByAttach  = "Another client attached to this session."
	detachedByCommand = "The tuios detach-client command detached this client."
)

// clientRows is tuios list-clients --json, the attached rows only.
func clientRows(t *testing.T, base string) []struct {
	ClientID string `json:"client_id"`
	PID      int    `json:"pid"`
	Session  string `json:"session"`
} {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-clients", "--json")
	if err != nil {
		t.Fatalf("list-clients: %v\n%s", err, out)
	}
	var rows []struct {
		ClientID string `json:"client_id"`
		PID      int    `json:"pid"`
		Session  string `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode list-clients: %v\n%s", err, out)
	}
	attached := rows[:0]
	for _, r := range rows {
		if r.Session != "" {
			attached = append(attached, r)
		}
	}
	return attached
}

// clientIDOf is the id list-clients gives the client process pid, waiting for
// it to show.
func clientIDOf(t *testing.T, base string, pid int, session string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		for _, r := range clientRows(t, base) {
			if r.PID == pid && r.Session == session {
				return r.ClientID
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no client with pid %d is attached to %s: %+v", pid, session, clientRows(t, base))
	return ""
}

// attachedClient starts a client on session with the PTY log kept, and waits
// for it to show the session's one window.
func attachedClient(t *testing.T, base, session string, extra ...string) (*tuitest.Terminal, string) {
	t.Helper()
	args := append([]string{"attach"}, extra...)
	args = append(args, session)
	term, logPath := startInLogged(t, base, startOpts{args: args})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client %v never attached: %v\n%s", args, err, term.Snapshot())
	}
	return term, logPath
}

// exitedDetached waits for a client to exit and checks it said why, in the
// words of reason, with status 0 and no error report.
func exitedDetached(t *testing.T, term *tuitest.Terminal, logPath, session, reason, what string) {
	t.Helper()
	code := waitExit(t, term, what)
	raw, _ := os.ReadFile(logPath)
	text := string(raw)
	if code != 0 {
		t.Fatalf("%s: the detached client exited %d, want 0\n%s", what, code, tailMessage(text))
	}
	if !strings.Contains(text, reason) {
		t.Fatalf("%s: the detached client did not print %q\n%s", what, reason, tailMessage(text))
	}
	if exitLine(text) != "Detached from session '"+session+"'." {
		t.Fatalf("%s: exit line %q, want the detach of %s", what, exitLine(text), session)
	}
	for _, bad := range []string{"Error", "terminated", "Cause:"} {
		if strings.Contains(text[strings.LastIndex(text, reason):], bad) {
			t.Fatalf("%s: the detached client printed an error report (%q)\n%s", what, bad, tailMessage(text))
		}
	}
}

// TestAttachDetachOthersEndsTheOtherClients attaches two clients the plain
// way, which must leave both attached, and then a third with -d, which must
// take the first two off. Each of them exits 0 with the message, the session
// keeps running, and the third is the only client left.
//
// Negative control: with the DetachOthers check cut from handleAttach, the
// first two clients stay attached and the test fails waiting for them to exit.
func TestAttachDetachOthersEndsTheOtherClients(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-solo"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}

	first, firstLog := attachedClient(t, base, sess)
	second, secondLog := attachedClient(t, base, sess)
	// The positive half: a plain attach detaches nobody.
	time.Sleep(500 * time.Millisecond)
	if _, exited := first.ExitCode(); exited {
		t.Fatalf("a plain attach ended the first client\n%s", first.Snapshot())
	}
	if n := len(clientRows(t, base)); n != 2 {
		t.Fatalf("after two plain attaches %d clients are attached, want 2", n)
	}

	third, _ := attachedClient(t, base, sess, "-d")
	exitedDetached(t, first, firstLog, sess, detachedByAttach, "first client after attach -d")
	exitedDetached(t, second, secondLog, sess, detachedByAttach, "second client after attach -d")

	thirdID := clientIDOf(t, base, third.Pid(), sess)
	if rows := clientRows(t, base); len(rows) != 1 || rows[0].ClientID != thirdID {
		t.Fatalf("after attach -d the clients are %+v, want only %s", rows, thirdID)
	}
	if !sessionListed(t, base, sess) {
		t.Fatalf("attach -d ended the session %s", sess)
	}
	alive(t, third, "after attach -d")
	saveArtifact(t, third, artifactDir(t), "attach-detach-others")
}

// TestSingleClientOptionDetachesOnEveryAttach turns [daemon] single_client on
// before the daemon starts. A plain attach then takes the earlier client off,
// with the same message attach -d gives.
//
// Negative control: with the single_client check cut from handleAttach, the
// first client stays attached and the test fails waiting for it to exit.
func TestSingleClientOptionDetachesOnEveryAttach(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[daemon]\nsingle_client = true\n")
	const sess = "e2e-single"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}

	first, firstLog := attachedClient(t, base, sess)
	second, _ := attachedClient(t, base, sess)
	exitedDetached(t, first, firstLog, sess, detachedByAttach, "first client under single_client")
	secondID := clientIDOf(t, base, second.Pid(), sess)
	if rows := clientRows(t, base); len(rows) != 1 || rows[0].ClientID != secondID {
		t.Fatalf("under single_client the clients are %+v, want only %s", rows, secondID)
	}
	alive(t, second, "after a single_client attach")
	saveArtifact(t, second, artifactDir(t), "single-client")
}

// TestDetachClientByIDAndSession detaches one of two clients by its id from
// list-clients, which must leave the other attached. A pane without the admin
// grant is refused the same command, and the client stays. Then the tmux
// shim's detach-client -s takes the last client off, and the session keeps
// running.
//
// Negative control: with detach-client classed scopeOpen in verbScopes, a
// read-write-fan pane detaches the client and DC_EXIT=1 never appears. With detach-client taken out of the shim's command table, the
// shim answers "unknown command" and the last client stays.
func TestDetachClientByIDAndSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-detach"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	first, firstLog := attachedClient(t, base, sess)
	second, secondLog := attachedClient(t, base, sess)
	firstID := clientIDOf(t, base, first.Pid(), sess)
	secondID := clientIDOf(t, base, second.Pid(), sess)

	out, err := tuiosCLI(t, base, "detach-client", "--client", firstID)
	if err != nil {
		t.Fatalf("detach-client --client %s: %v\n%s", firstID, err, out)
	}
	if !strings.Contains(out, "Detached client "+firstID) {
		t.Fatalf("detach-client did not name the client: %q", out)
	}
	exitedDetached(t, first, firstLog, sess, detachedByCommand, "client detached by id")
	if rows := clientRows(t, base); len(rows) != 1 || rows[0].ClientID != secondID {
		t.Fatalf("after detach-client --client the clients are %+v, want only %s", rows, secondID)
	}
	alive(t, second, "after the other client was detached by id")

	// A pane without admin may not detach the client that shows it.
	out, err = tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 {
		t.Fatalf("list-windows gave no single window: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", sess, "-w", listing.Windows[0].WindowID, "--grants", "read,write,fan"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	line := tuiosBin + " detach-client -s " + sess + "; echo DC_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "DC_EXIT=1") && strings.Contains(text, "admin grant")
	}, uiTimeout); err != nil {
		t.Fatalf("a pane without admin was not refused detach-client: %v\n%s", err, second.Snapshot())
	}
	alive(t, second, "after a pane without admin asked to detach it")
	saveArtifact(t, second, artifactDir(t), "detach-client-refused")

	// The tmux shim, run outside every pane, where every session is a tmux
	// session.
	if out, err := tuiosCLI(t, base, "tmux", "detach-client", "-s", sess); err != nil {
		t.Fatalf("tuios tmux detach-client -s: %v\n%s", err, out)
	}
	exitedDetached(t, second, secondLog, sess, detachedByCommand, "client detached through the tmux shim")
	if rows := clientRows(t, base); len(rows) != 0 {
		t.Fatalf("after the shim's detach-client -s the clients are %+v, want none", rows)
	}
	if !sessionListed(t, base, sess) {
		t.Fatalf("detach-client ended the session %s", sess)
	}
}
