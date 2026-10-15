package terminal

import "bytes"

// nvimNavigatorOSC is the private OSC number the tuios nvim navigator plugin
// talks on: "7777;tuios-nvim-navigator;focus;<direction>" asks tuios to focus
// the neighbouring pane, and "...;state;active|inactive" says whether the
// pane takes the navigation keys itself. The emulator knows nothing of it and
// hands the body over through vt.Callbacks.PrivateOSC.
const nvimNavigatorOSC = 7777

// nvimNavigatorParts splits a navigator body into its name, verb and
// argument. The emulator passes the body with its number, and the number is
// optional so a body without it parses too.
func nvimNavigatorParts(data []byte) ([][]byte, bool) {
	parts := bytes.Split(data, []byte{';'})
	if len(parts) == 4 && string(parts[0]) == "7777" {
		parts = parts[1:]
	}
	if len(parts) != 3 || string(parts[0]) != "tuios-nvim-navigator" {
		return nil, false
	}
	return parts, true
}

// handleNvimNavigatorOSC calls nav for a focus request and state for a state
// report. Anything else is ignored.
func handleNvimNavigatorOSC(data []byte, nav func(direction string), state func(active bool)) {
	parts, ok := nvimNavigatorParts(data)
	if !ok {
		return
	}
	switch string(parts[1]) {
	case "focus":
		switch direction := string(parts[2]); direction {
		case "left", "right", "up", "down":
			if nav != nil {
				nav(direction)
			}
		}
	case "state":
		if state == nil {
			return
		}
		switch string(parts[2]) {
		case "active":
			state(true)
		case "inactive":
			state(false)
		}
	}
}
