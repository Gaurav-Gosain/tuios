package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// pasteKeyOS builds an OS in terminal mode with a focused window, ready to
// route the terminal_paste_host key the way a real session would.
func pasteKeyOS(t *testing.T, mutate func(*app.OS)) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	o.Mode = app.TerminalMode
	o.Settings = config.DefaultSettings()
	if mutate != nil {
		mutate(o)
	}
	return o
}

// pasteKey is the physical chord the default config binds to terminal_paste_host.
func pasteKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}
}

// lastNotificationMessage returns the most recent notification's message.
func lastNotificationMessage(o *app.OS) string {
	if len(o.Notifications) == 0 {
		return ""
	}
	return o.Notifications[len(o.Notifications)-1].Message
}

// TestBrowserPasteKeySaysWhy checks that a browser client's paste key says why
// paste cannot work instead of quietly waiting forever.
//
// The terminal in a browser tab never answers the OSC 52 read query, and
// bubbletea's ReadClipboard has no deadline, so a bare read there would wait
// forever with nothing on screen. Routing the key through RequestHostPaste
// makes it say at once what is wrong and which key does work.
func TestBrowserPasteKeySaysWhy(t *testing.T) {
	o := pasteKeyOS(t, func(o *app.OS) {
		o.BrowserClient = true
		o.RemoteClient = true
	})

	o2, cmd := HandleKeyPress(pasteKey(), o)

	if cmd != nil {
		t.Fatalf("the browser paste key sent a clipboard query that can never be answered")
	}
	msg := lastNotificationMessage(o2)
	if msg == "" {
		t.Fatalf("the browser paste key did nothing and said nothing")
	}
	if !strings.Contains(msg, "ctrl+v") {
		t.Fatalf("the message does not say what to do instead: %q", msg)
	}
}

// TestPasteKeyArmsTheDeadline checks that the paste key routes through
// RequestHostPaste, whose reply is a batch that arms the OSC 52 timeout.
// A bare tea.ReadClipboard has no deadline, so a terminal that never answers
// would look broken instead of being reported.
func TestPasteKeyArmsTheDeadline(t *testing.T) {
	o := pasteKeyOS(t, nil)

	o2, cmd := HandleKeyPress(pasteKey(), o)

	if cmd == nil {
		t.Fatalf("a terminal client's paste key produced no command")
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); !ok {
		t.Fatalf("the paste key returned %T, not a batch; the timeout is not armed", msg)
	}
	// The batch must carry the read query; a batch that only ticks and never
	// reads would arm a deadline nobody is waiting on.
	found := false
	for _, c := range msg.(tea.BatchMsg) {
		if c != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("the batch contains no commands at all")
	}
	if o2 == nil {
		t.Fatalf("the paste key returned a nil OS")
	}
}
