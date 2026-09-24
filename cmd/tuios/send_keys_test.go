package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// echoProgram is a window process whose terminal echoes what it is sent with
// control bytes made visible, so ESC [ B shows as ^[[B.
var echoProgram = []string{"/bin/sh", "-c", "stty icanon echo echoctl; cat >/dev/null"}

// captureWindow is the visible screen of one window.
func captureWindow(t *testing.T, c *session.VerbClient, sess, window string) string {
	t.Helper()
	raw, err := c.Call("capture-pane", map[string]any{"session": sess, "window": window})
	if err != nil {
		t.Fatalf("capture-pane %s: %v", window, err)
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("capture-pane %s: %v", window, err)
	}
	return res.Content
}

// waitForScreen waits until a window's screen holds want.
func waitForScreen(t *testing.T, c *session.VerbClient, sess, window, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var screen string
	for time.Now().Before(deadline) {
		if screen = captureWindow(t, c, sess, window); strings.Contains(screen, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("window %s never showed %q:\n%s", window, want, screen)
}

// TestSendKeysCommandAddressesSpellsAndRepeats drives two windows the way an
// agent in a pane does: open both with new-window --print-id, send arrows to
// one by name, by id and by index in the spellings agents reach for, and check
// that only the window named read them and that the command says where the
// keys went.
func TestSendKeysCommandAddressesSpellsAndRepeats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the echo program is /bin/sh")
	}
	c := startSubscribeDaemon(t)
	if _, err := c.Call("new-session", map[string]any{"name": "sk", "window": false}); err != nil {
		t.Fatalf("new-session: %v", err)
	}

	var ids []string
	for _, name := range []string{"left", "right"} {
		var err error
		out := captureStdout(t, func() {
			err = runNewWindow("sk", name, 0, "", false, echoProgram, "", nil, false, true)
		})
		if err != nil {
			t.Fatalf("new-window %s: %v", name, err)
		}
		id := strings.TrimSpace(out)
		if len(id) != 36 || strings.ContainsAny(id, " \t") {
			t.Fatalf("new-window --print-id printed %q, want the full id alone", out)
		}
		ids = append(ids, id)
	}
	time.Sleep(300 * time.Millisecond)

	// By name, with a repeat, in DOM spelling.
	var err error
	out := captureStdout(t, func() { err = runSendKeys("sk", "arrow-down", false, false, "left", 3, false) })
	if err != nil {
		t.Fatalf("send-keys to left: %v", err)
	}
	if want := "sent 3 keys to window left (" + ids[0][:8] + ")"; strings.TrimSpace(out) != want {
		t.Errorf("send-keys printed %q, want %q", out, want)
	}
	waitForScreen(t, c, "sk", "left", "^[[B^[[B^[[B")

	// By id and by index, in every other spelling of Up.
	for _, target := range []string{ids[1], "1"} {
		for _, spelling := range []string{"Up", "up", "UP", "KEY_UP", "<Up>", `\e[A`} {
			if err := runSendKeys("sk", spelling, false, false, target, 1, false); err != nil {
				t.Fatalf("send-keys %q to %s: %v", spelling, target, err)
			}
		}
	}
	waitForScreen(t, c, "sk", "right", strings.Repeat("^[[A", 12))
	if left := captureWindow(t, c, "sk", "left"); strings.Contains(left, "^[[A") {
		t.Errorf("keys sent to right reached left:\n%s", left)
	}
	if right := captureWindow(t, c, "sk", "right"); strings.Contains(right, "^[[B") {
		t.Errorf("keys sent to left reached right:\n%s", right)
	}

	// Page keys and the JSON result.
	out = captureStdout(t, func() { err = runSendKeys("sk", "PgDn", false, false, "left", 0, true) })
	if err != nil {
		t.Fatalf("send-keys PgDn: %v", err)
	}
	var res map[string]any
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("send-keys --json printed %q: %v", out, jerr)
	}
	if res["sent_to"] != "window" || res["window_id"] != ids[0] || res["window"] != "left" {
		t.Errorf("send-keys --json = %v, want the left window", res)
	}
	waitForScreen(t, c, "sk", "left", "^[[6~")

	// A misspelled key sends nothing and names the key and the window.
	err = runSendKeys("sk", "Down Dwon", false, false, "right", 1, false)
	if err == nil || !strings.Contains(err.Error(), "Dwon") || !strings.Contains(err.Error(), "right") || !strings.Contains(err.Error(), "Down?") {
		t.Errorf("misspelled key: %v, want an error naming Dwon, right and Down", err)
	}
}
