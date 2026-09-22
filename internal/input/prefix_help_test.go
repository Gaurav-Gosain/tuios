package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Opening help from the leader has to start at the top, as it does from the
// window-management binding and the command palette. The prefix action used to
// toggle ShowHelp without resetting the scroll offset, so help opened wherever
// a previous view had left it.
func TestPrefixHelpOpensAtTheTop(t *testing.T) {
	for _, action := range []string{"prefix_help", "toggle_help"} {
		t.Run(action, func(t *testing.T) {
			o := &app.OS{
				Settings:         config.Global,
				HelpScrollOffset: 12,
				HelpCategory:     -1,
			}
			out, _ := GetDispatcher().Dispatch(action, tea.KeyPressMsg{}, o)
			if !out.ShowHelp {
				t.Fatalf("%s did not open help", action)
			}
			if out.HelpScrollOffset != 0 {
				t.Errorf("%s opened help at scroll offset %d, want 0", action, out.HelpScrollOffset)
			}
		})
	}
}
