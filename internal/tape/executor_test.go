package tape

import (
	"testing"
)

// nopExecutor records nothing and does nothing; it exists so Execute's argument
// checks can be exercised without an app.
type nopExecutor struct{ Executor }

func (nopExecutor) GetFocusedWindowID() string                      { return "w1" }
func (nopExecutor) SendToWindow(_ string, _ []byte) error           { return nil }
func (nopExecutor) SplitHorizontal() error                          { return nil }
func (nopExecutor) SplitVertical() error                            { return nil }
func (nopExecutor) SwitchWorkspace(_ int) error                     { return nil }
func (nopExecutor) FocusWindowByName(_ string) error                { return nil }
func (nopExecutor) FocusWindowByID(_ string) error                  { return nil }
func (nopExecutor) Preselect(_ string) error                        { return nil }
func (nopExecutor) SaveLayoutExec(_ string) error                   { return nil }
func (nopExecutor) SetConfig(_, _ string) error                     { return nil }
func (nopExecutor) ShowNotificationCmd(_, _ string) error           { return nil }
func (nopExecutor) RenameWindowByID(_, _ string) error              { return nil }
func (nopExecutor) MoveWindowToWorkspaceByID(_ string, _ int) error { return nil }

// TestExecuteRejectsUnusableCommands pins that a command with nothing to act on
// reports the problem. Every one of these used to fall through to a bare
// `return nil`, which is indistinguishable from having worked, so a truncated or
// mistyped line in a tape did nothing and said nothing.
func TestExecuteRejectsUnusableCommands(t *testing.T) {
	ce := NewCommandExecutor(nopExecutor{})

	tests := []struct {
		name string
		cmd  Command
	}{
		{"Type with no text", Command{Type: CommandTypeType}},
		{"Split with no direction", Command{Type: CommandTypeSplit}},
		{"Split with a bad direction", Command{Type: CommandTypeSplit, Args: []string{"sideways"}}},
		{"Focus with no target", Command{Type: CommandTypeFocusWindow}},
		{"Preselect with no direction", Command{Type: CommandTypePreselect}},
		{"RenameWindow with no name", Command{Type: CommandTypeRenameWindow}},
		{"SaveLayout with no name", Command{Type: CommandTypeSaveLayout}},
		{"Set with no value", Command{Type: CommandTypeSetConfig, Args: []string{"a.b"}}},
		{"Notify with no message", Command{Type: CommandTypeShowNotification}},
		{"Switch with no workspace", Command{Type: CommandTypeSwitchWS}},
		{"Switch with a non-numeric workspace", Command{Type: CommandTypeSwitchWS, Args: []string{"main"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ce.Execute(&tt.cmd); err == nil {
				t.Error("Execute returned nil; an unusable command must report why it did nothing")
			}
		})
	}
}
