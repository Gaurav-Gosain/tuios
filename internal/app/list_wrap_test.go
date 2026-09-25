package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func wrapOS() *OS {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.Settings.WrapLists = true
	return m
}

// TestConfirmationsDoNotWrap pins the exception for the two-row confirmations:
// the cursor opens on Cancel, and up from Cancel must not land on the answer
// that kills or deletes.
func TestConfirmationsDoNotWrap(t *testing.T) {
	m := wrapOS()
	m.OpenSessionCloseFor("")
	m.SessionCloseMove(-1)
	if m.SessionCloseSelected != SessionCloseRowCancel {
		t.Errorf("up from Cancel in the close-session dialog went to row %d", m.SessionCloseSelected)
	}

	m.filePrompt.Kind = filePromptConfirm
	m.filePrompt.Selected = fileConfirmRowCancel
	m.FileConfirmMove(-1)
	if m.filePrompt.Selected != fileConfirmRowCancel {
		t.Errorf("up from Cancel in the file dialog went to row %d", m.filePrompt.Selected)
	}
}
