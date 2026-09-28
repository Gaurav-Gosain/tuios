package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// hintsTestOS is one focused pane on workspace 1 with body printed in it.
func hintsTestOS(t *testing.T, body string) (*OS, *terminal.Window) {
	t.Helper()
	win := newTestWindow(t, "hints0000001", 60, 10)
	win.Workspace = 1
	win.WriteOutput([]byte(body))
	m := newTestOS(win)
	m.CurrentWorkspace = 1
	m.UserConfig = config.DefaultConfig()
	return m, win
}

// ctrlLabel opens hints, finds the match text, and types its label with Ctrl.
func ctrlLabel(t *testing.T, m *OS, text string) {
	t.Helper()
	m.OpenHints()
	label, ok := m.HintLabels()[text]
	if !ok {
		t.Fatalf("no label on %q; labels %v", text, m.HintLabels())
	}
	for _, r := range label {
		m.HintsPress(r, HintOpen)
	}
	if m.HintsOpen() {
		t.Fatal("the label did not complete")
	}
}

// hintsLastNote is the newest dock message.
func hintsLastNote(m *OS) string {
	if len(m.Notifications) == 0 {
		return ""
	}
	return m.Notifications[len(m.Notifications)-1].Message
}

// A file named in a pane is opened only when every machine involved is this
// one. Each case below is a pane whose file is somebody else's, and each must
// copy the text, say why, and open nothing: a new pane would be an editor on a
// local file that happens to share the name.
func TestHintsDoNotOpenAnotherMachinesFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		text  string
		setup func(m *OS, w *terminal.Window)
		why   string
	}{
		{"a file:// link on a pane of another machine", "file://" + file,
			func(m *OS, w *terminal.Window) { w.Host = "otherbox" }, "another machine"},
		{"a file:// link naming another host", "file://otherbox" + file,
			func(m *OS, w *terminal.Window) {}, "another machine"},
		{"a path on a pane of another machine", file,
			func(m *OS, w *terminal.Window) { w.Host = "otherbox" }, "another machine"},
		{"a relative path whose folder the shell reported on another host", "notes/todo.md",
			func(m *OS, w *terminal.Window) { w.Cwd = "file://otherbox/home/u" }, "folder is on another machine"},
		{"a path in a session attached on another machine", file,
			func(m *OS, w *terminal.Window) { m.AttachedHost = "otherbox" }, "session runs on another machine"},
		{"a file:// link in a session attached on another machine", "file://" + file,
			func(m *OS, w *terminal.Window) { m.AttachedHost = "otherbox" }, "session runs on another machine"},
		{"a path on a remote client", file,
			func(m *OS, w *terminal.Window) { m.RemoteClient = true }, "remote client"},
		{"a path in a pane running ssh", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "ssh" }, "another machine"},
		{"a path in a pane running kitty's ssh kitten", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "kitten" }, "another machine"},
		{"a path in a pane running autossh", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "autossh" }, "another machine"},
		{"a path in a pane running tsh", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "tsh" }, "another machine"},
		{"a path in a pane running docker exec", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "docker" }, "another machine"},
		{"a path in a pane running kubectl exec", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "kubectl" }, "another machine"},
		{"a path in a pane running podman exec", file,
			func(m *OS, w *terminal.Window) { w.ForegroundCmd = "podman" }, "another machine"},
		{"a relative path while a program runs in front of the shell", "notes/todo.md",
			func(m *OS, w *terminal.Window) {
				w.Cwd = "file://localhost/srv/app"
				w.ForegroundCmd = "make"
			}, "folder is not known"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, win := hintsTestOS(t, "see "+c.text+" here\r\n")
			c.setup(m, win)
			ctrlLabel(t, m, c.text)
			if n := len(m.Windows); n != 1 {
				t.Fatalf("a pane was opened for another machine's file (%d panes)", n)
			}
			if note := hintsLastNote(m); !strings.Contains(note, c.why) || !strings.Contains(note, "Copied") {
				t.Errorf("the message does not say why and that it copied: %q", note)
			}
		})
	}
}

// The positive half of the rule above: on a local pane the checks pass, and a
// relative path is placed in the folder the shell reported for this machine.
func TestHintsPlaceALocalPath(t *testing.T) {
	m, win := hintsTestOS(t, "")
	if why := m.hintFileBlocked(win); why != "" {
		t.Fatalf("a plain local pane is blocked: %q", why)
	}
	win.Cwd = "file://localhost/srv/app"
	if got, why := hintLocalPath(win, "notes/todo.md:12:3"); why != "" || got != "/srv/app/notes/todo.md" {
		t.Errorf("hintLocalPath = %q, %q; want /srv/app/notes/todo.md", got, why)
	}
	win.Cwd = "file://otherbox/srv/app"
	if _, why := hintLocalPath(win, "notes/todo.md"); why == "" {
		t.Error("a folder on another host was used for a local path")
	}
}

// Hints mode ends when its pane leaves the screen: switched away from,
// unfocused, or gone. Left open, the frame pass never runs again (its pane is
// not drawn), the fast path stays off, and the next key goes to labels nobody
// can see.
func TestHintsCloseWhenThePaneLeaves(t *testing.T) {
	const body = "commit deadbee42\r\n"
	t.Run("workspace switch", func(t *testing.T) {
		m, _ := hintsTestOS(t, body)
		m.OpenHints()
		if !m.HintsOpen() {
			t.Fatal("hints did not open")
		}
		m.SwitchToWorkspace(2)
		if m.hints != nil {
			t.Error("hints stayed open on a workspace that is no longer shown")
		}
	})
	t.Run("pane deleted", func(t *testing.T) {
		m, _ := hintsTestOS(t, body)
		m.OpenHints()
		m.Windows = nil
		m.FocusedWindow = -1
		if m.HintsOpen() {
			t.Error("hints report open with their pane gone")
		}
		if m.hints != nil {
			t.Error("hints state outlived its pane")
		}
	})
	t.Run("focus moves", func(t *testing.T) {
		m, win := hintsTestOS(t, body)
		other := newTestWindow(t, "hints0000002", 60, 10)
		other.Workspace = 1
		m.Windows = append(m.Windows, other)
		m.OpenHints()
		m.FocusWindow(1)
		if m.hints != nil {
			t.Errorf("hints stayed open on %s after focus moved to %s", win.ID, other.ID)
		}
	})
	t.Run("the frame closes what nothing else did", func(t *testing.T) {
		m, win := hintsTestOS(t, body)
		m.OpenHints()
		win.Workspace = 3 // moved away behind hints' back
		m.closeStaleHints()
		if m.hints != nil {
			t.Error("a stale hints mode survived the frame's check")
		}
		if _, ok := m.fullscreenFastWindow(); ok && m.hints != nil {
			t.Error("the fast path is held off by closed hints")
		}
	})
}

// "Copied N chars" counts characters, not bytes.
func TestHintsCountCharacters(t *testing.T) {
	if got := hintChars("/tmp/文档/café.md"); got != 15 {
		t.Errorf("hintChars = %d, want 15", got)
	}
}
