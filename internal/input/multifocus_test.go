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

// A wheel scroll leaves the focused pane in implicit copy mode. A paste ends
// the scroll the way a typed key does and reaches every pane in the set.
func TestMultifocusPasteEndsScroll(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
		want [3]string
	}{
		{"terminal paste", tea.PasteMsg{Content: "x"}, [3]string{"\x1b[200~x\x1b[201~", "x", ""}},
		{"clipboard paste", tea.ClipboardMsg{Content: "x", Selection: 'c'}, [3]string{"\x1b[200~x\x1b[201~", "x", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, sent := multifocusHarness(t)
			w := o.Windows[0]
			for range 60 {
				_, _ = w.Terminal.Write([]byte("line\r\n"))
			}
			w.EnterCopyModeImplicit()
			scrollCopyModeUpBy(w, 5)
			if !w.InImplicitCopyMode() {
				t.Fatal("setup: the focused pane is not in implicit copy mode")
			}

			_, _ = HandleInput(tc.msg, o)

			if w.InCopyMode() || w.ScrollbackOffset != 0 {
				t.Errorf("after the paste the pane is still scrolled (copy mode %v, offset %d)", w.InCopyMode(), w.ScrollbackOffset)
			}
			for i, want := range tc.want {
				if got := sent[i].String(); got != want {
					t.Errorf("pane %d got %q, want %q", i, got, want)
				}
			}
		})
	}
}

// In real copy mode keys are motions. A paste is dropped: it must not reach
// the focused pane's shell under the copy-mode view, nor the set.
func TestMultifocusPasteDroppedInCopyMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{"terminal paste", tea.PasteMsg{Content: "x"}},
		{"clipboard paste", tea.ClipboardMsg{Content: "x", Selection: 'c'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, sent := multifocusHarness(t)
			o.Windows[0].EnterCopyMode()

			_, _ = HandleInput(tc.msg, o)

			for i, s := range sent {
				if s.Len() != 0 {
					t.Errorf("pane %d got %q from a paste in copy mode, want nothing", i, s.String())
				}
			}
			if !o.Windows[0].InCopyMode() {
				t.Error("the paste ended copy mode, want it kept")
			}
		})
	}
}

// The paste key's clipboard read passes the same prompts a terminal paste
// does, so the save prompt takes it before any pane.
func TestMultifocusClipboardPasteInterceptedByPrompts(t *testing.T) {
	o, sent := multifocusHarness(t)
	o.MultiCopy = &app.MultiCopy{Save: &app.MultiCopySave{}}
	_, _ = HandleInput(tea.ClipboardMsg{Content: "/tmp/x\n", Selection: 'c'}, o)
	for i, s := range sent {
		if s.Len() != 0 {
			t.Errorf("pane %d got %q while the save prompt was open", i, s.String())
		}
	}
}

// toggle_multifocus_all works on the current workspace only. Members on other
// workspaces do not stop it from filling the set, and a clear keeps them.
func TestMultifocusToggleAllScopedToWorkspace(t *testing.T) {
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.PrefixMode["toggle_multifocus_all"] = []string{"Y"}
	})
	ws := o.CurrentWorkspace
	o.Windows = []*terminal.Window{
		{ID: "a", Workspace: ws},
		{ID: "other", Workspace: ws + 1},
	}
	o.FocusedWindow = 0
	o.Mode = app.TerminalMode
	o.MultifocusSet = map[string]bool{"other": true}

	lastNote := func() string {
		if len(o.Notifications) == 0 {
			return ""
		}
		return o.Notifications[len(o.Notifications)-1].Message
	}

	o.PrefixActive = true
	_, _ = HandlePrefixCommand(press("Y"), o)
	if !o.MultifocusSet["a"] || !o.MultifocusSet["other"] {
		t.Fatalf("after toggle_multifocus_all the set is %v, want a and other", o.MultifocusSet)
	}
	if got, want := lastNote(), "Multifocus: 1 window"; got != want {
		t.Errorf("notification %q, want %q (panes on this workspace only)", got, want)
	}

	o.PrefixActive = true
	_, _ = HandlePrefixCommand(press("Y"), o)
	if o.MultifocusSet["a"] || !o.MultifocusSet["other"] {
		t.Fatalf("after the second toggle the set is %v, want only other", o.MultifocusSet)
	}
}

// A popup cannot join the set by toggle_multifocus_active, the same as
// toggle_multifocus_all leaves it out.
func TestMultifocusToggleActiveSkipsPopup(t *testing.T) {
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.PrefixMode["toggle_multifocus_active"] = []string{"y"}
	})
	o.Windows = []*terminal.Window{{ID: "pop", Workspace: o.CurrentWorkspace, IsPopup: true}}
	o.FocusedWindow = 0
	o.Mode = app.TerminalMode

	o.PrefixActive = true
	_, _ = HandlePrefixCommand(press("y"), o)
	if len(o.MultifocusSet) != 0 {
		t.Fatalf("toggle_multifocus_active on a popup gave the set %v, want it empty", o.MultifocusSet)
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
