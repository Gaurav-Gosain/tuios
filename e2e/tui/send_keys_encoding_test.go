package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSendKeysEncodesKeysForThePaneMode sends keys to three panes that each
// asked for a different keyboard mode, and reads the bytes each pane got. The
// panes run testdata/keydump, which prints every read in hex.
//
//   - A pane in the kitty keyboard protocol (flags 1) gets ctrl+h as
//     CSI 104;5u, the bytes the client sends for a ctrl+h a person types.
//     Before the fix it got 0x08, and Vim in kitty mode read that as
//     Backspace. herdr's pane.send_keys takes the same route and gets the
//     same bytes. Text and literal keys stay text.
//   - A pane in modifyOtherKeys 2 gets CSI 27;5;104~.
//   - A pane that asked for nothing gets 0x08, as before: the positive half.
//
// Negative control: in sendKey.encode, return k.bytes(modes.appCursor) for
// every key, and the kitty pane's first read is 08.
func TestSendKeysEncodesKeysForThePaneMode(t *testing.T) {
	_, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	bin := filepath.Join(t.TempDir(), "keydump")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/keydump").CombinedOutput(); err != nil {
		t.Fatalf("build keydump: %v\n%s", err, out)
	}
	for _, mode := range []string{"legacy", "kitty", "mok"} {
		name := "keys-" + mode
		script := "stty raw -echo; exec '" + bin + "' " + mode
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--no-focus", name, "--", "/bin/sh", "-c", script); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		waitCapture(t, base, sess, name, "keydump "+mode+" ready")
	}

	// send runs one send-keys and waits for the pane to print the read.
	send := func(window, want string, args ...string) {
		t.Helper()
		full := append([]string{"send-keys", "-s", sess, "-w", window}, args...)
		if out, err := tuiosCLI(t, base, full...); err != nil {
			t.Fatalf("send-keys %v: %v\n%s", args, err, out)
		}
		waitLine(t, base, sess, window, want)
	}

	send("keys-legacy", "read1=08", "ctrl+h")
	send("keys-legacy", "read2=1b", "Escape")

	send("keys-kitty", "read1=1b5b3130343b3575", "ctrl+h")
	send("keys-kitty", "read2=6374726c2b68", "-l", "ctrl+h")
	send("keys-kitty", "read3=6869", "hi")
	send("keys-kitty", "read4=1b5b323775", "Escape")
	send("keys-kitty", "read5=0d", "Enter")
	send("keys-kitty", "read6=1b5b3130343b3775", "C-M-h")

	pane := herdrPaneByLabel(t, base, "keys-kitty")["pane_id"].(string)
	herdrCall(t, base, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"ctrl+h"}})
	waitLine(t, base, sess, "keys-kitty", "read7=1b5b3130343b3575")

	send("keys-mok", "read1=1b5b32373b353b3130347e", "ctrl+h")
	send("keys-mok", "read2=6869", "hi")

	dir := artifactDir(t)
	for _, mode := range []string{"legacy", "kitty", "mok"} {
		name := "keys-" + mode
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(capture(t, base, sess, name)), 0o644); err != nil {
			t.Errorf("save %s: %v", name, err)
		}
	}
}

// waitLine polls capture-pane until one line of the pane is want, with its
// spaces trimmed. A whole line is compared so that read2=1b does not match
// read2=1b5b.
func waitLine(t *testing.T, base, session, window, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var out string
	for {
		out, _ = tuiosCLI(t, base, "capture-pane", "-s", session, "-w", window)
		for line := range strings.SplitSeq(out, "\n") {
			if strings.TrimSpace(line) == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %s never showed the line %q:\n%s", window, want, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
