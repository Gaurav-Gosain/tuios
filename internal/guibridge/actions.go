package guibridge

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

func checkAction(name string) error {
	if name == "" {
		return errfmt.Errorf("action needs name, an action name")
	}
	if config.GUIRunnable(name) {
		return nil
	}
	if strings.HasPrefix(name, config.CommandActionPrefix) {
		return nil
	}
	if _, gui := config.GUIActionDescriptions[name]; gui {
		return errfmt.Errorf("%s is a GUI action: the GUI runs it itself", name)
	}
	if _, known := config.ActionDescriptions[name]; known {
		return errfmt.Errorf("%s shows something only a screen can show, so the bridge does not run it", name)
	}
	return errfmt.Errorf("%q is not an action the bridge runs", name)
}

var splitActions = map[string]layout.PreselectionDir{
	"split_vertical":   layout.PreselectionRight,
	"split_horizontal": layout.PreselectionDown,
}

func (m *model) splitRefusal(name string) error {
	dir, ok := splitActions[config.CanonicalAction(name)]
	if !ok {
		return nil
	}
	return m.os.SplitRefusal(dir)
}
