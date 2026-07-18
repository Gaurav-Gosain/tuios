package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestKeysToBytes(t *testing.T) {
	tests := []struct {
		name    string
		keys    string
		literal bool
		raw     bool
		want    []byte
		wantErr bool
	}{
		{name: "literal text", keys: "echo hi", literal: true, want: []byte("echo hi")},
		{name: "raw text", keys: "a b", raw: true, want: []byte("a b")},
		{name: "enter", keys: "Enter", want: []byte("\r")},
		{name: "named sequence", keys: "h i Enter", want: []byte("hi\r")},
		{name: "comma separated", keys: "Escape,Tab", want: []byte("\x1b\t")},
		{name: "ctrl-c", keys: "ctrl+c", want: []byte{0x03}},
		{name: "ctrl-a", keys: "ctrl+a", want: []byte{0x01}},
		{name: "alt-x", keys: "alt+x", want: []byte{0x1b, 'x'}},
		{name: "arrow up", keys: "Up", want: []byte("\x1b[A")},
		{name: "prefix rejected", keys: "PREFIX", wantErr: true},
		{name: "bad ctrl", keys: "ctrl+shift", wantErr: true},
		{name: "empty", keys: "   ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keysToBytes(tc.keys, tc.literal, tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("keysToBytes(%q) = %v, want %v", tc.keys, got, tc.want)
			}
		})
	}
}

// newTestDaemonSession builds a daemon with one detached (no-client) session.
func newTestDaemonSession(t *testing.T) (*Daemon, *Session) {
	t.Helper()
	d := NewDaemon(&DaemonConfig{})
	sess, err := d.manager.CreateSession("headless", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(d.manager.Shutdown)
	return d, sess
}

func TestExecuteDaemonCommandLifecycle(t *testing.T) {
	d, sess := newTestDaemonSession(t)
	noExit := func(string) {}

	// NewWindow returns an ID and spawns a live PTY.
	data, err := d.executeDaemonCommand(sess, "NewWindow", []string{"build"}, noExit)
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}
	winID, _ := data["window_id"].(string)
	if winID == "" {
		t.Fatal("NewWindow returned no window_id")
	}
	if name, _ := data["name"].(string); name != "build" {
		t.Errorf("NewWindow name = %q, want build", name)
	}

	// A second window, then Next/Prev focus cycling.
	if _, err := d.executeDaemonCommand(sess, "NewWindow", nil, noExit); err != nil {
		t.Fatalf("second NewWindow failed: %v", err)
	}
	if len(sess.GetState().Windows) != 2 {
		t.Fatalf("window count = %d, want 2", len(sess.GetState().Windows))
	}
	if _, err := d.executeDaemonCommand(sess, "PrevWindow", nil, noExit); err != nil {
		t.Fatalf("PrevWindow failed: %v", err)
	}
	if sess.GetState().FocusedWindowID != winID {
		t.Errorf("focus after PrevWindow = %q, want %q", sess.GetState().FocusedWindowID, winID)
	}

	// ListWindows read verb works headless.
	list, err := d.executeDaemonCommand(sess, "ListWindows", nil, noExit)
	if err != nil {
		t.Fatalf("ListWindows failed: %v", err)
	}
	if total, _ := list["total"].(int); total != 2 {
		t.Errorf("ListWindows total = %v, want 2", list["total"])
	}

	// CloseWindow by name removes it.
	if _, err := d.executeDaemonCommand(sess, "CloseWindow", []string{"build"}, noExit); err != nil {
		t.Fatalf("CloseWindow failed: %v", err)
	}
	if len(sess.GetState().Windows) != 1 {
		t.Errorf("window count after close = %d, want 1", len(sess.GetState().Windows))
	}

	// A rendering-dependent verb is refused headless.
	if _, err := d.executeDaemonCommand(sess, "Split", []string{"horizontal"}, noExit); err == nil {
		t.Error("expected Split to be refused headless")
	}
}

func TestSendKeysAndCaptureDaemonSide(t *testing.T) {
	d, sess := newTestDaemonSession(t)

	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}

	// Type a command with a distinctive marker into the focused pane's PTY.
	const marker = "tuios-headless-marker"
	err := d.sendKeysDaemonSide(sess, &SendKeysPayload{
		Keys:    "echo " + marker + "\n",
		Literal: true,
	})
	if err != nil {
		t.Fatalf("sendKeysDaemonSide failed: %v", err)
	}

	// Poll the daemon-side capture until the marker's echoed output appears.
	deadline := time.Now().Add(5 * time.Second)
	var content string
	for time.Now().Before(deadline) {
		content, err = d.capturePaneDaemonSide(sess, &CapturePanePayload{})
		if err != nil {
			t.Fatalf("capturePaneDaemonSide failed: %v", err)
		}
		if strings.Contains(content, marker) {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("capture never showed marker %q; last content:\n%s", marker, content)
}

func TestSendKeysDaemonSideNoWindows(t *testing.T) {
	d, sess := newTestDaemonSession(t)
	err := d.sendKeysDaemonSide(sess, &SendKeysPayload{Keys: "Enter"})
	if err == nil {
		t.Error("expected error sending keys to a session with no windows")
	}
}
