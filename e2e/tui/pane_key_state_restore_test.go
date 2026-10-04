package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// keyStateProbe writes a script that sets up key state with setup, prints
// <name>SET, waits for the file go, runs then, prints <name>READY, and copies
// the keys it reads into out until four seconds of silence.
func keyStateProbe(t *testing.T, dir, name, setup, then string) (script, out, gate string) {
	t.Helper()
	out = filepath.Join(dir, name+".keys")
	gate = filepath.Join(dir, name+".go")
	script = filepath.Join(dir, name+".sh")
	body := "stty raw -echo min 0 time 40\n" +
		"printf '" + setup + "'\n" +
		"printf '" + name + "SET\\r\\n'\n" +
		"while [ ! -e " + gate + " ]; do sleep 0.1; done\n" +
		"printf '" + then + "'\n" +
		"printf '" + name + "READY\\r\\n'\n" +
		"cat > " + out + "\n" +
		"printf '\\033[>4;0m\\033[<u'\n" +
		"stty sane\n" +
		"echo " + splitMarker("PROBEDONE"+name) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return script, out, gate
}

// readProbeKeys sends ctrl+A as kitty CSI u, the way a kitty-protocol host
// reports it, and returns what the probe read.
func readProbeKeys(t *testing.T, term *tuitest.Terminal, name, out string) string {
	t.Helper()
	if err := term.WaitForText(name+"READY", shellTimeout); err != nil {
		t.Fatalf("the %s probe never got ready: %v\n%s", name, err, term.Snapshot())
	}
	sendRaw(t, term, "\x1b[97;5u")
	time.Sleep(insertGuard)
	if err := term.WaitForText("PROBEDONE"+name, shellTimeout); err != nil {
		t.Fatalf("the %s probe never finished: %v\n%s", name, err, term.Snapshot())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read the %s keys: %v", name, err)
	}
	_ = os.WriteFile(filepath.Join(artifactDir(t), name+".keys"), raw, 0o644)
	return string(raw)
}

// TestPaneModifyOtherKeysOffWhileHidden: a program turns modifyOtherKeys off
// while its pane is on a hidden workspace, as vim does when it quits. A hidden
// pane is unsubscribed, so its client emulator never sees the reset, and the
// snapshot that catches it up on the way back is all it has. Level 0 was
// skipped there as "an older daemon says nothing", the emulator kept level 2,
// and Ctrl+A (Ctrl+C alike) reached the shell as CSI 27 ; 5 ; 97 ~.
//
// How this could pass wrongly, written down first:
//   - the level might never have been set in the client, so the probe sets
//     level 2 while the pane is shown and the key is sent only after the
//     reset;
//   - the reset might reach the client through its stream, so the probe waits
//     for a gate file the test writes only once the pane is hidden.
//
// Negative control: with ApplyTerminalState back to restoring only a level
// above 0, the pane reads "\x1b[27;5;97~".
func TestPaneModifyOtherKeysOffWhileHidden(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-mokhide", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := attachIn(t, base, "e2e-mokhide", startOpts{cols: 120, rows: 30})
	enterTerminalMode(t, term)
	runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)

	script, out, gate := keyStateProbe(t, t.TempDir(), "mok", "\\033[>4;2m", "\\033[>4;0m")
	if err := term.SendKeys("sh "+script, tuitest.Enter); err != nil {
		t.Fatalf("run the probe: %v", err)
	}
	if err := term.WaitForText("mokSET", shellTimeout); err != nil {
		t.Fatalf("the probe never set the level: %v\n%s", err, term.Snapshot())
	}
	leaveTerminalMode(t, term)
	switchWorkspace(t, term, "2", 0)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The probe turns the level off and prints its marker while hidden. Give
	// it the time to, then come back.
	time.Sleep(time.Second)
	switchWorkspace(t, term, "1", 1)
	enterTerminalMode(t, term)
	if got := readProbeKeys(t, term, "mok", out); got != "\x01" {
		t.Errorf("after modifyOtherKeys went off while the pane was hidden, the pane read %q, want %q", got, "\x01")
	}
}

// TestPaneKittyMainStackAcrossAttach: each screen has its own kitty flag
// stack, and a client that attaches while a program runs on the alternate
// screen is handed that screen's stack. The main screen's has to come with it:
// a shell that pushed flags reads keys by them again once the program quits,
// and a client that kept only the alternate stack sent them in legacy form.
//
// How this could pass wrongly, written down first:
//   - the flags might not be pushed at all, so the key would be legacy either
//     way: the probe pushes them on the main screen and reads CSI u there;
//   - the first client might still be around to supply the stack, so it is
//     closed before the second attaches.
//
// Negative control: with the RestoreKittyKeyboardMainStack call cut from
// ApplyTerminalState, the pane reads "\x01".
func TestPaneKittyMainStackAcrossAttach(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-kittyattach", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	first := attachIn(t, base, "e2e-kittyattach", startOpts{cols: 120, rows: 30})
	enterTerminalMode(t, first)
	runInShell(t, first, "echo RE\"\"ADY", "READY", shellTimeout)

	script, out, gate := keyStateProbe(t, t.TempDir(), "kitty", "\\033[>1u\\033[?1049h", "\\033[?1049l")
	if err := first.SendKeys("sh "+script, tuitest.Enter); err != nil {
		t.Fatalf("run the probe: %v", err)
	}
	if err := first.WaitForText("kittySET", shellTimeout); err != nil {
		t.Fatalf("the probe never entered the alternate screen: %v\n%s", err, first.Snapshot())
	}
	_ = first.Close()

	term := attachIn(t, base, "e2e-kittyattach", startOpts{cols: 120, rows: 30})
	if err := term.WaitForText("kittySET", uiTimeout); err != nil {
		t.Fatalf("the second client never showed the alternate screen: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readProbeKeys(t, term, "kitty", out); got != "\x1b[97;5u" {
		t.Errorf("after the program quit, the pane read %q, want %q", got, "\x1b[97;5u")
	}
}
