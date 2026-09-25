package app_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/input"
)

// TestKeysRightAfterAPopDoNothing sends keys through the real input path just
// after a question opened the Inbox by itself. They were most likely typed for
// the pane, so d does not dismiss the question, q does not close it and a
// digit does not answer it. Once the question has been on screen for a
// moment, the same d reaches the dismiss.
func TestKeysRightAfterAPopDoNothing(t *testing.T) {
	for _, mode := range []app.Mode{app.TerminalMode, app.WindowManagementMode} {
		m := app.InboxOSForTest(t)
		m.Mode = mode
		app.ApplyInboxEventsForTest(m, app.AskItemForTest("1", "here", "w-1", "yes", "no"))
		if !m.ShowInbox {
			t.Fatalf("mode %v: the question did not pop", mode)
		}
		for _, key := range []tea.KeyPressMsg{
			{Code: 'd', Text: "d"}, {Code: 'q', Text: "q"}, {Code: '1', Text: "1"}, {Code: tea.KeyEnter},
		} {
			m, _ = input.HandleKeyPress(key, m)
			note := app.LastNoteForTest(m)
			if !m.ShowInbox || strings.Contains(note, "Dismissing") || strings.Contains(note, "Answering") {
				t.Fatalf("mode %v: %q right after the pop acted (open=%v, note %q)", mode, key.String(), m.ShowInbox, note)
			}
			if !strings.Contains(note, "just appeared") {
				t.Fatalf("mode %v: %q right after the pop said nothing: %q", mode, key.String(), note)
			}
		}

		app.AgePopForTest(m)
		m, _ = input.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"}, m)
		if note := app.LastNoteForTest(m); !strings.Contains(note, "Dismissing") {
			t.Fatalf("mode %v: d on a question that has been read did not reach the dismiss: %q", mode, note)
		}
	}
}
