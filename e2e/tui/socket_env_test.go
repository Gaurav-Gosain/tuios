package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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

// TestSocketEnvRefusalKeepsThePathWholeOnATerminal is the same refusal read
// by a person at a terminal narrower than the path. The error box used to cut
// the path at the box's width, ".../XDG_RUNTIME_DIR/fres" on one line and
// "h.sock" on the next, so it could not be copied back out, and a pipe got the
// same box because the renderer did not see through the writer it was handed.
//
// How this could pass wrongly: the path might fit the box and never be cut,
// so the test refuses to run with a path that fits; and the command might not
// see a terminal at all and print the bare text, so the box's header is
// required in the output. The bytes are read as tuios wrote them, before the
// terminal soft-wraps anything, so a line break inside the path is one tuios
// put there.
//
// Negative control: with RenderErrorText returning style.Render(s), the
// path's bytes are split by a newline and this fails.
func TestSocketEnvRefusalKeepsThePathWholeOnATerminal(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	// The runtime directory can be a short stand-in (see xdgDir), so the path
	// is made long on purpose. The hyphens are there because a hyphen is a
	// place a word wrapper will break a word.
	const cols = 60
	fresh := filepath.Join(base, "a-directory-named-past-the-width-of-the-box", "fresh.sock")
	if len(fresh) <= cols-4 {
		t.Fatalf("the path %q fits the %d column box, so nothing here could cut it", fresh, cols-4)
	}

	var raw syncBuffer
	term := startIn(t, base, startOpts{
		cols: cols, rows: 30,
		args: []string{"new", "stray", "--detach"},
		env:  []string{"TUIOS_SOCKET=" + fresh},
		out:  &raw,
		// --no-animations is a flag of the interface, not of `new`.
		animations: true,
	})
	code, err := term.WaitExit(15 * time.Second)
	if err != nil {
		t.Fatalf("the refused command did not exit: %v\n%s", err, term.Snapshot())
	}
	out := ansi.Strip(raw.String())
	if err := os.WriteFile(filepath.Join(artifactDir(t), "refusal-on-a-terminal.txt"), []byte(out), 0o644); err != nil {
		t.Errorf("save the output: %v", err)
	}
	if code == 0 {
		t.Errorf("a command with TUIOS_SOCKET naming no daemon ran:\n%s", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Fatalf("the refusal was not rendered as a terminal error, so this checked nothing:\n%s", out)
	}
	if !strings.Contains(out, fresh) {
		t.Errorf("the refusal on a terminal cut the path %q:\n%s", fresh, out)
	}
	// Only the path's own line may run past the terminal. lipgloss pads every
	// line of a block to its widest, so a whole path must not drag the rest of
	// the message past the edge, where each line wraps onto a blank row.
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.Contains(line, fresh) && ansi.StringWidth(line) > cols {
			t.Errorf("a line of the refusal is %d columns on a %d column terminal: %q", ansi.StringWidth(line), cols, line)
		}
	}
	if hasName(sessionNames(t, base), "stray") {
		t.Errorf("the refused command still created its session")
	}
}

// TestSocketEnvNamingNoDaemonIsRefused is the incident: TUIOS_SOCKET set to a
// path with no daemon, and a command that would otherwise have gone to the
// daemon XDG_RUNTIME_DIR names.
//
// Negative controls: with the check's call cut from GetSocketPath, the command
// succeeds and the session lands in the daemon under base. With
// isTerminalWriter checking the colorprofile.Writer itself rather than the
// file under it, the piped refusal is drawn in the error box and this fails.
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
	// The output is a pipe, so it gets the bare text. It used to get the
	// terminal's box, wrapped at 80 columns, which cut a long path in two.
	if strings.Contains(out, "ERROR") {
		t.Errorf("the refusal to a pipe was drawn as a terminal error box:\n%s", out)
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
