//go:build windows

package herdrcli

import "errors"

// LinkName is the name a link to tuios must have for tuios to run as herdr.
const LinkName = "herdr.exe"

// InstallLink is not supported on Windows, where a link needs a privilege a
// person rarely holds. A pane there is given the tuios binary itself as
// HERDR_BIN_PATH, which answers herdr's pane and notification commands.
func InstallLink(dir, exe string) (string, error) {
	return "", errors.New("the herdr link is not supported on Windows")
}
