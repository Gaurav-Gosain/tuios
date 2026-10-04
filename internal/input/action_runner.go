package input

import (
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// The tape executor and run-command run actions by name through this, since
// the handlers are here and the executor is in internal/app.
func init() {
	app.SetActionRunner(RunActionByName)
	// Every name the dispatcher knows is one a tape can name, including the
	// ones with no description.
	tape.RegisterActions(slices.Collect(maps.Keys(GetDispatcher().handlers))...)
	tape.RegisterActions(slices.Collect(maps.Keys(app.PrefixWorkActions))...)
}

// RunActionByName runs a keybinding action the way the key bound to it would,
// through the same dispatcher, so the action is noted for a crash report,
// recorded into a tape and heard by Learn mode like a key press. The prefix
// work keys go through the path the prefix gives them first, as they do from
// a key. handled is false when nothing knows the action.
func RunActionByName(name string, o *app.OS) (handled bool, cmd tea.Cmd) {
	if app.PrefixWorkActions[name] {
		if cmd, handled := runPrefixWork(name, o); handled {
			return true, cmd
		}
	}
	d := GetDispatcher()
	if !d.HasAction(name) {
		return false, nil
	}
	_, cmd = d.Dispatch(name, tea.KeyPressMsg{}, o)
	return true, cmd
}
