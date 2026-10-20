//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/Gaurav-Gosain/tuios/internal/winacl"
)

// passwordFileAccess is the access to a password file that only its owner may
// have: read the password, change it, or change who may do either.
const passwordFileAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
	windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_ALL |
	windows.WRITE_DAC | windows.WRITE_OWNER

// checkPasswordFileMode refuses a password file that another user owns, or
// whose DACL lets anyone other than its owner, SYSTEM and Administrators read
// or change it. Windows keeps no Unix mode, so the owner and the DACL in the
// file's security descriptor are what is checked. They are read from the open
// handle, so the file cannot be swapped between the check and the read.
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
