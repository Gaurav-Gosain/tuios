package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTmuxShimKeysFollowTheCursorKeyMode: a key sent through the tmux shim
// reaches the pane as the daemon's send-keys encodes it for the pane's modes,
// as real tmux does. A program that turns on application cursor keys (vim,
// less) gets Up as ESC O A, and ESC [ A once it turns them off. The pane's
// program writes the bytes it read to a file, which is the artifact.
//
// How it could pass wrongly, and what stops it:
//   - the keys never arrive, and an old file is read: the files are new to
//     this test, and each phase waits for its own file;
//   - the mode is set after the keys are sent, so both phases read the normal
//     form: each phase waits until the pane shows the marker printed after
//     the mode change, and the daemon reads the pane's output in order;
//   - the shim sends keys and text in the wrong order: the second phase sends
//     text then a key, and wants the text's byte first.
//
// NEGATIVE CONTROL: with the shim writing its own bytes for each key (keys.go
// before this change), the first phase reads 1b 5b 41 1b 5b 48, not
// 1b 4f 41 1b 4f 48.
func TestTmuxShimKeysFollowTheCursorKeyMode(t *testing.T) {
	_, tr := shimDaemon(t, "keys")
	dir := artifactDir(t)
	script := filepath.Join(dir, "read-keys.sh")
	app, normal := filepath.Join(dir, "app-cursor.hex"), filepath.Join(dir, "normal-cursor.hex")
	body := `stty raw -echo
printf '\033[?1hAPP''READY\r\n'
dd bs=1 count=6 2>/dev/null | od -An -tx1 >` + app + `
printf '\033[?1lNORMAL''READY\r\n'
dd bs=1 count=4 2>/dev/null | od -An -tx1 >` + normal + `
stty sane
printf 'KEYS''DONE\n'
sleep 30
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // the pane runs it
		t.Fatal(err)
	}
	pane := tr.ok("new-window", "-P", "-F", "#{pane_id}", "-t", "keys:", "sh "+script)

	waitShimScreen(t, tr, pane, "APPREADY")
	tr.ok("send-keys", "-t", pane, "Up", "Home")
	if got := hexFields(waitForFileText(t, app, func(s string) bool { return len(strings.Fields(s)) == 6 }, "the pane read no 6 bytes in application cursor mode")); got != "1b 4f 41 1b 4f 48" {
		t.Errorf("ASSERTION: Up and Home through the shim in application cursor mode read %q, want 1b 4f 41 1b 4f 48 (ESC O A, ESC O H)", got)
	}

	waitShimScreen(t, tr, pane, "NORMALREADY")
	tr.ok("send-keys", "-t", pane, "x", "Up")
	if got := hexFields(waitForFileText(t, normal, func(s string) bool { return len(strings.Fields(s)) == 4 }, "the pane read no 4 bytes in normal cursor mode")); got != "78 1b 5b 41" {
		t.Errorf("ASSERTION: x then Up through the shim in normal cursor mode read %q, want 78 1b 5b 41 (x, ESC [ A)", got)
	}
	waitShimScreen(t, tr, pane, "KEYSDONE")
}

// hexFields is od's output as one line of bytes.
func hexFields(s string) string { return strings.Join(strings.Fields(s), " ") }

// waitShimScreen waits until the pane's screen, read through the shim, shows
// want.
func waitShimScreen(t *testing.T, tr *shimTranscript, pane, want string) {
	t.Helper()
	waitUntil(t, "pane "+pane+" shows "+want, shellTimeout, func() (bool, string) {
		r := tr.tmux("", "capture-pane", "-p", "-t", pane)
		return r.code == 0 && strings.Contains(r.out, want), r.out
	})
}
