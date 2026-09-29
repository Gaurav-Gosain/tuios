package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// multifocusPane is a daemon-mode pane whose PTY writes land in sent.
func multifocusPane(t *testing.T, id string, bracketed bool, sent *strings.Builder) *terminal.Window {
	t.Helper()
	em := vt.NewEmulator(40, 20)
	t.Cleanup(func() { _ = em.Close() })
	if bracketed {
		if _, err := em.Write([]byte("\x1b[?2004h")); err != nil {
			t.Fatalf("enable bracketed paste: %v", err)
		}
	}
	return &terminal.Window{
		ID:         id,
		Terminal:   em,
		Width:      42,
		Height:     22,
		DaemonMode: true,
		DaemonWriteFunc: func(b []byte) error {
			sent.Write(b)
			return nil
		},
	}
}

// multifocusHarness is three panes in terminal mode: a (focused, bracketed
// paste on), b (bracketed paste off) and c. a and b are in the multifocus
// set, c is not.
func multifocusHarness(t *testing.T) (*app.OS, [3]*strings.Builder) {
	t.Helper()
	var sent [3]*strings.Builder
	for i := range sent {
		sent[i] = &strings.Builder{}
	}
	o := &app.OS{
		Settings:      config.Global,
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows: []*terminal.Window{
			multifocusPane(t, "a", true, sent[0]),
			multifocusPane(t, "b", false, sent[1]),
			multifocusPane(t, "c", true, sent[2]),
		},
		MultifocusSet: map[string]bool{"a": true, "b": true},
		Width:         80,
		Height:        30,
	}
	return o, sent
}

// Issue #236: a paste from the host terminal reaches every pane in the
// multifocus set, as typed keys do. Each pane gets bracketed-paste markers
// only when its own app turned the mode on.
func TestMultifocusPasteBroadcast(t *testing.T) {
	o, sent := multifocusHarness(t)

	_, _ = HandleInput(tea.PasteMsg{Content: "echo hi"}, o)

	if got, want := sent[0].String(), "\x1b[200~echo hi\x1b[201~"; got != want {
		t.Errorf("focused pane got %q, want %q", got, want)
	}
	if got, want := sent[1].String(), "echo hi"; got != want {
		t.Errorf("multifocus pane got %q, want the raw paste %q (its app has bracketed paste off)", got, want)
	}
	if got := sent[2].String(); got != "" {
		t.Errorf("pane outside the set got %q, want nothing", got)
	}
}

// The clipboard read reply (OSC 52 or the native tool) is the paste key's
// paste, so it is broadcast the same way.
func TestMultifocusClipboardPasteBroadcast(t *testing.T) {
	o, sent := multifocusHarness(t)

	_, _ = HandleInput(tea.ClipboardMsg{Content: "ls", Selection: 'c'}, o)

	if got, want := sent[0].String(), "\x1b[200~ls\x1b[201~"; got != want {
		t.Errorf("focused pane got %q, want %q", got, want)
	}
	if got, want := sent[1].String(), "ls"; got != want {
		t.Errorf("multifocus pane got %q, want %q", got, want)
	}
	if got := sent[2].String(); got != "" {
		t.Errorf("pane outside the set got %q, want nothing", got)
	}
}

// A committed IME string arrives as a key with text, and takes the typing
// path, which broadcasts already. This pins that it stays so.
func TestMultifocusIMECommitBroadcast(t *testing.T) {
	o, sent := multifocusHarness(t)

	_, _ = HandleInput(tea.KeyPressMsg{Code: '中', Text: "中文"}, o)

	for i, want := range []string{"中文", "中文", ""} {
		if got := sent[i].String(); got != want {
			t.Errorf("pane %d got %q, want %q", i, got, want)
		}
	}
}

// Hints mode and the multi copy save prompt take a paste before any pane
// does, so no pane in the set may see it.
func TestMultifocusPasteInterceptedByPrompts(t *testing.T) {
	t.Run("save prompt", func(t *testing.T) {
		o, sent := multifocusHarness(t)
		o.MultiCopy = &app.MultiCopy{Save: &app.MultiCopySave{}}
		_, _ = HandleInput(tea.PasteMsg{Content: "/tmp/x\n"}, o)
		for i, s := range sent {
			if s.Len() != 0 {
				t.Errorf("pane %d got %q while the save prompt was open", i, s.String())
			}
		}
	})
}

// A focused pane in copy mode does not broadcast typing, and a paste from it
// must not reach the shells of the other panes in the set either.
func TestMultifocusPasteNotBroadcastFromCopyMode(t *testing.T) {
	o, sent := multifocusHarness(t)
	o.Windows[0].EnterCopyMode()

	_, _ = HandleInput(tea.PasteMsg{Content: "x"}, o)

	if got := sent[1].String(); got != "" {
		t.Errorf("multifocus pane got %q from a paste in copy mode, want nothing", got)
	}
}

// Issue #232: both toggles run from a key the user binds.
func TestMultifocusToggleActionsByKey(t *testing.T) {
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.PrefixMode["toggle_multifocus_active"] = []string{"y"}
		k.PrefixMode["toggle_multifocus_all"] = []string{"Y"}
	})
	ws := o.CurrentWorkspace
	o.Windows = []*terminal.Window{
		{ID: "a", Workspace: ws},
		{ID: "b", Workspace: ws},
		{ID: "min", Workspace: ws, Minimized: true},
		{ID: "other", Workspace: ws + 1},
	}
	o.FocusedWindow = 1
	o.Mode = app.TerminalMode

	prefixed := func(key string) {
		t.Helper()
		o.PrefixActive = true
		_, _ = HandlePrefixCommand(press(key), o)
	}

	prefixed("y")
	if !o.MultifocusSet["b"] || len(o.MultifocusSet) != 1 {
		t.Fatalf("after toggle_multifocus_active the set is %v, want only b", o.MultifocusSet)
	}
	prefixed("y")
	if len(o.MultifocusSet) != 0 {
		t.Fatalf("a second toggle_multifocus_active left %v, want an empty set", o.MultifocusSet)
	}

	o.MultifocusSet = map[string]bool{"a": true}
	prefixed("Y")
	want := map[string]bool{"a": true, "b": true}
	if len(o.MultifocusSet) != len(want) || !o.MultifocusSet["a"] || !o.MultifocusSet["b"] {
		t.Fatalf("after toggle_multifocus_all the set is %v, want %v (visible panes on the workspace only)", o.MultifocusSet, want)
	}
	prefixed("Y")
	if len(o.MultifocusSet) != 0 {
		t.Fatalf("toggle_multifocus_all with every pane in the set left %v, want it cleared", o.MultifocusSet)
	}
}

// The actions are in the registry's descriptions, so help, the keybind list
// and the palette's "#" search can name them.
func TestMultifocusActionsDescribed(t *testing.T) {
	for _, a := range []string{"toggle_multifocus_active", "toggle_multifocus_all"} {
		if config.ActionDescriptions[a] == "" {
			t.Errorf("%s has no description", a)
		}
		if !GetDispatcher().HasAction(a) {
			t.Errorf("%s has no handler", a)
		}
	}
}
