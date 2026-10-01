//go:build !windows

package herdrcli

import "github.com/Gaurav-Gosain/tuios/internal/shimlink"

// LinkName is the name a link to tuios must have for tuios to run as herdr.
const LinkName = "herdr"

// InstallLink points <dir>/bin/herdr at exe, replacing whatever was there,
// and returns the link's path. See shimlink.Install.
func InstallLink(dir, exe string) (string, error) {
	return shimlink.Install(dir, LinkName, exe)
}
