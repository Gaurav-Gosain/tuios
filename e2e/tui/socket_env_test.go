package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TUIOS_SOCKET is what a pane is told about the daemon that runs it. It never
// chose the daemon a command reaches: XDG_RUNTIME_DIR does. An agent that set
// TUIOS_SOCKET to a fresh path, expecting a daemon of its own, got the
// person's daemon instead and created a session in it. A command now refuses
// when TUIOS_SOCKET names a socket other than the one it would reach and no
// daemon listens there, which is exactly that mistake.
//
// How this could pass wrongly, written down first:
//   - the refusal might come from something else going wrong, so its output
//     is checked for the words that explain it;
//   - the command might refuse and still have acted, so the daemon is asked
//     afterwards whether the session exists;
//   - the check might refuse far more than the mistake, which would break
//     every script that isolates itself with XDG_RUNTIME_DIR from inside a
//     pane (whose TUIOS_SOCKET names the pane's own, live, daemon), so both
//     that case and TUIOS_SOCKET naming the same socket are shown to work.

// sessionNames lists the sessions of the daemon under base.
func sessionNames(t *testing.T, base string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ls --json did not print JSON: %v\n%s", err, out)
	}
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestSocketEnvNamingNoDaemonIsRefused is the incident: TUIOS_SOCKET set to a
// path with no daemon, and a command that would otherwise have gone to the
// daemon XDG_RUNTIME_DIR names.
//
// Negative control: with the check's call cut from GetSocketPath, the command
// succeeds and the session lands in the daemon under base.
func TestSocketEnvNamingNoDaemonIsRefused(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	own := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	dir := artifactDir(t)
	var transcript strings.Builder

	fresh := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "fresh.sock")
	out, err := tuiosCLIEnv(t, base, []string{"TUIOS_SOCKET=" + fresh}, "new", "stray", "--detach")
	transcript.WriteString("$ TUIOS_SOCKET=" + fresh + " tuios new stray --detach\n" + out + "\n")
	if err == nil {
		t.Errorf("a command with TUIOS_SOCKET naming no daemon ran:\n%s", out)
	}
	for _, want := range []string{"TUIOS_SOCKET", fresh, "XDG_RUNTIME_DIR"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	// A command that only reads, over the verb protocol, is refused the same
	// way rather than reading the other daemon.
	for _, args := range [][]string{{"ls"}, {"list-windows", "-s", "home"}} {
		out, err := tuiosCLIEnv(t, base, []string{"TUIOS_SOCKET=" + fresh}, args...)
		transcript.WriteString("$ TUIOS_SOCKET=" + fresh + " tuios " + strings.Join(args, " ") + "\n" + out + "\n")
		if err == nil || !strings.Contains(out, "TUIOS_SOCKET") {
			t.Errorf("tuios %v with TUIOS_SOCKET naming no daemon was not refused: %v\n%s", args, err, out)
		}
	}
	if names := sessionNames(t, base); hasName(names, "stray") {
		t.Errorf("the refused command still created its session in the daemon under base: %v", names)
	}

	// The positive halves. TUIOS_SOCKET naming the socket the command reaches
	// anyway, as it does in every pane.
	out, err = tuiosCLIEnv(t, base, []string{"TUIOS_SOCKET=" + own}, "new", "same", "--detach")
	transcript.WriteString("$ TUIOS_SOCKET=" + own + " tuios new same --detach\n" + out + "\n")
	if err != nil {
		t.Errorf("TUIOS_SOCKET naming the daemon's own socket was refused: %v\n%s", err, out)
	}
	// A script that isolates itself with XDG_RUNTIME_DIR from inside a pane:
	// TUIOS_SOCKET names another, live, daemon, and the command goes where
	// XDG_RUNTIME_DIR says, as it always did.
	other := t.TempDir()
	killDaemon(t, other)
	if out, err := tuiosCLI(t, other, "new", "elsewhere", "--detach"); err != nil {
		t.Fatalf("start the second daemon: %v\n%s", err, out)
	}
	otherSock := filepath.Join(xdgDir(other, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	out, err = tuiosCLIEnv(t, base, []string{"TUIOS_SOCKET=" + otherSock}, "new", "isolated", "--detach")
	transcript.WriteString("$ TUIOS_SOCKET=" + otherSock + " tuios new isolated --detach\n" + out + "\n")
	if err != nil {
		t.Errorf("TUIOS_SOCKET naming another live daemon was refused: %v\n%s", err, out)
	}
	names := sessionNames(t, base)
	for _, want := range []string{"same", "isolated"} {
		if !hasName(names, want) {
			t.Errorf("session %s is not in the daemon XDG_RUNTIME_DIR names: %v", want, names)
		}
	}
	if hasName(sessionNames(t, other), "isolated") {
		t.Errorf("TUIOS_SOCKET chose the daemon: the session went to the other one")
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.txt"), []byte(transcript.String()), 0o644); err != nil {
		t.Errorf("save the transcript: %v", err)
	}
}
