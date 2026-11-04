//go:build !linux && !darwin && !windows

package session

import "errors"

// awakeHolder has no way to hold this system awake.
type awakeHolder struct{}

func awakeMethod() string { return "none" }

func (h *awakeHolder) take(string, chan<- error) error {
	return errors.New("tuios cannot keep this system awake. It can on Linux with systemd, macOS and Windows")
}

func (h *awakeHolder) release() {}
