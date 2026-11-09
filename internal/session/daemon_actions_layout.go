package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Keymap actions that change a workspace's layout shape (parity gap 1, step
// 4, batch C1; issue #162): the mode, tiling, the master-stack shape and
// the split tree's rotate and equalize. The daemon runs each through the
// arrange verb's engine (verb_arrange.go), one op per session at a time,
// with or without a TUI attached. A client used to work each out on its own
// copy and send the whole master shape or tree back, so two that pressed
// add_master at once each sent one master more than they read, and one
// press was lost.

// layoutTapeActions are the tape commands that are a batch C1 action under
// another name: run-command resolves rotate_split to RotateSplit, and so on.
// With no args they run as that action.
var layoutTapeActions = map[string]string{
	"RotateSplit":         "rotate_split",
	"EqualizeSplits":      "equalize_splits",
	"ToggleTiling":        "toggle_tiling",
	"AddMaster":           "add_master",
	"RemoveMaster":        "remove_master",
	"SwapWithMaster":      "swap_with_master",
	"CycleMasterPosition": "cycle_master_position",
}

// layoutActionOp is the arrange op of keymap action name, and its mode.
func layoutActionOp(name string) (op, mode string, ok bool) {
	switch name {
	case "toggle_tiling":
		return "tiling", "", true
	case "cycle_layout":
		return "next", "", true
	case "rotate_split":
		return "rotate", "", true
	case "equalize_splits":
		return "equalize", "", true
	case "cycle_master_position":
		return "master.position", "", true
	case "add_master":
		return "master.add", "", true
	case "remove_master":
		return "master.remove", "", true
	case "swap_with_master":
		return "master.swap", "", true
	}
	if side, found := strings.CutPrefix(name, "set_master_position_"); found {
		for _, p := range config.MasterPositions {
			if p == side {
				return "master.position", side, true
			}
		}
	}
	return "", "", false
}

// IsLayoutAction reports whether name is a batch C1 action: one the
// daemon runs as a layout op whatever the layout, or resize_master_grow and
// shrink, which it runs in the split layout only.
func IsLayoutAction(name string) bool {
	if _, _, ok := layoutActionOp(name); ok {
		return true
	}
	return isStepAction(name)
}

// isStepAction reports whether name is a resize step the daemon runs in the
// split layout only (layout.GrowPane): the TUI's width keys of 4 columns and
// height keys of 2 rows.
func isStepAction(name string) bool {
	switch name {
	case "resize_master_grow", "resize_master_shrink", "resize_height_grow", "resize_height_shrink":
		return true
	}
	return false
}

// headlessArrange runs arrange op on sess for the connection that asked,
// so the verbs the engine calls hold that connection's grants.
func (d *Daemon) headlessArrange(sess *Session, caller *connState, op, mode string) error {
	if caller == nil {
		return errNeedsClient{verb: "Action " + op}
	}
	params := map[string]any{"session": sess.Name(), "op": op}
	if mode != "" {
		params["mode"] = mode
	}
	if _, verr := d.callVerb(caller, "arrange", params); verr != nil {
		return verr
	}
	return nil
}
