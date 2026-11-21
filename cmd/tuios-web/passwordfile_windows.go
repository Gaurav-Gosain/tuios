//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/Gaurav-Gosain/tuios/internal/winacl"
)

const passwordFileAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
	windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_ALL |
	windows.WRITE_DAC | windows.WRITE_OWNER

func checkPasswordFileMode(f *os.File, path string, _ os.FileInfo, _ int) error {
	sd, err := winacl.FileSecurity(windows.Handle(f.Fd()))
	if err != nil {
		return fmt.Errorf("cannot read the permissions of the password file %s: %w", path, err)
	}
	me, mine, err := winacl.CurrentUser()
	if err != nil {
		return fmt.Errorf("cannot find the current user: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("cannot read the owner of the password file %s", path)
	}
	if !owner.Equals(me) && !owner.Equals(mine) {
		return fmt.Errorf("the password file %s belongs to another user. Use a file that you own", path)
	}
	trusted, err := winacl.Trusted(me, mine, owner)
	if err != nil {
		return fmt.Errorf("cannot check the password file %s: %w", path, err)
	}
	other, err := winacl.GrantsOther(sd, passwordFileAccess, trusted)
	if err != nil {
		return fmt.Errorf("cannot read the permissions of the password file %s: %w", path, err)
	}
	if other != "" {
		return fmt.Errorf("%s can read or change the password file %s. Run 'icacls \"%s\" /inheritance:r /grant:r \"%s:F\"', then start tuios-web again",
			other, path, path, winacl.AccountName(me))
	}
	return nil
}
