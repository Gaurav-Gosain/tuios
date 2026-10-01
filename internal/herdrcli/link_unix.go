//go:build !windows

package herdrcli

import (
	"fmt"
	"os"
	"path/filepath"
)

// LinkName is the name a link to tuios must have for tuios to run as herdr.
const LinkName = "herdr"

// InstallLink points <dir>/bin/herdr at exe, replacing whatever was there,
// and returns the link's path. dir is made owner only when it is new.
func InstallLink(dir, exe string) (string, error) {
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return "", err
	}
	link := filepath.Join(bin, LinkName)
	if cur, err := os.Readlink(link); err == nil && cur == exe {
		return link, nil
	}
	tmp := fmt.Sprintf("%s.%d", link, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return link, nil
}
