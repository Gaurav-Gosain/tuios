package terminal

import "bytes"

const nvimNavigatorOSC = 7777

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
