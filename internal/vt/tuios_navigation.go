package vt

import "bytes"

func tuiosNavigatorParts(data []byte) ([][]byte, bool) {
	parts := bytes.Split(data, []byte{";"[0]})
	if len(parts) == 4 && string(parts[0]) == "7777" {
		parts = parts[1:]
	}
	if len(parts) != 3 || string(parts[0]) != "tuios-nvim-navigator" {
		return nil, false
	}
	return parts, true
}

func parseTuiosNavigation(data []byte) (string, bool) {
	parts, ok := tuiosNavigatorParts(data)
	if !ok || string(parts[1]) != "focus" {
		return "", false
	}
	direction := string(parts[2])
	switch direction {
	case "left", "right", "up", "down":
		return direction, true
	default:
		return "", false
	}
}

func parseTuiosNavigatorState(data []byte) (bool, bool) {
	parts, ok := tuiosNavigatorParts(data)
	if !ok || string(parts[1]) != "state" {
		return false, false
	}
	switch string(parts[2]) {
	case "active":
		return true, true
	case "inactive":
		return false, true
	default:
		return false, false
	}
}

func (e *Emulator) handleTuiosNavigation(data []byte) bool {
	direction, ok := parseTuiosNavigation(data)
	if ok && e.cb.TuiosNavigation != nil {
		e.cb.TuiosNavigation(direction)
	}
	if active, ok := parseTuiosNavigatorState(data); ok && e.cb.NvimNavigatorState != nil {
		e.cb.NvimNavigatorState(active)
	}
	return true
}
