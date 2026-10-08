//go:build windows

package herdrcli

import "github.com/Gaurav-Gosain/tuios/internal/shimlink"

// LinkName is the name a link to tuios must have for tuios to run as herdr.
const LinkName = "herdr.exe"

// InstallLink makes <dir>/bin/herdr.exe run exe and returns its path: a
// hardlink, or a copy when exe is on another volume. See shimlink.Install.
func InstallLink(dir, exe string) (string, error) {
	return shimlink.Install(dir, LinkName, exe)
}
