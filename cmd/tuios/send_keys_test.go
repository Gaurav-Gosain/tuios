package main

import (
	"encoding/json"
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
