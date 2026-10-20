//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// passwordFileAccess is the access to a password file that only its owner may
// have: read the password, change it, or change who may do either.
const passwordFileAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
	windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_ALL |
	windows.WRITE_DAC | windows.WRITE_OWNER

// ACE types whose body starts with a mask and a SID, as ACCESS_ALLOWED_ACE
// does. The object ACE types put two GUIDs before the SID.
const (
	accessAllowedCallbackACEType       = 0x9
	accessAllowedObjectACEType         = 0x5
	accessAllowedCallbackObjectACEType = 0xB
)

// checkPasswordFileMode refuses a password file that another user owns, or
// whose DACL lets anyone other than its owner, SYSTEM and Administrators read
// or change it. Windows keeps no Unix mode, so the owner and the DACL in the
// file's security descriptor are what is checked. They are read from the open
// handle, so the file cannot be swapped between the check and the read.
func checkPasswordFileMode(f *os.File, path string, _ os.FileInfo, _ int) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot read the permissions of the password file %s: %w", path, err)
	}
	me, mine, err := currentUserSIDs()
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

	trusted := []*windows.SID{me, mine, owner}
	for _, known := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		sid, err := windows.CreateWellKnownSid(known)
		if err != nil {
			return fmt.Errorf("cannot check the password file %s: %w", path, err)
		}
		trusted = append(trusted, sid)
	}

	other, err := daclGrantsOther(sd, trusted)
	if err != nil {
		return fmt.Errorf("cannot read the permissions of the password file %s: %w", path, err)
	}
	if other != "" {
		return fmt.Errorf("%s can read or change the password file %s. Run 'icacls \"%s\" /inheritance:r /grant:r \"%s:F\"', then start tuios-web again",
			other, path, path, accountName(me))
	}
	return nil
}

// daclGrantsOther returns the name of the first account the DACL lets read or
// change the file that is not in trusted, or "" when there is none. A missing
// or NULL DACL lets everyone in. A deny entry is not weighed against an allow
// entry: the check only refuses more, never less, by ignoring it.
func daclGrantsOther(sd *windows.SECURITY_DESCRIPTOR, trusted []*windows.SID) (string, error) {
	dacl, _, err := sd.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) || (err == nil && dacl == nil) {
		return "Everyone", nil
	}
	if err != nil {
		return "", err
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "", err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE, accessAllowedCallbackACEType:
		case accessAllowedObjectACEType, accessAllowedCallbackObjectACEType:
			// A file does not carry these. Refuse rather than parse them.
			return "An object access entry", nil
		default:
			continue
		}
		if uint32(ace.Mask)&passwordFileAccess == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !containsSID(trusted, sid) {
			return accountName(sid), nil
		}
	}
	return "", nil
}

func containsSID(list []*windows.SID, sid *windows.SID) bool {
	for _, s := range list {
		if s != nil && s.Equals(sid) {
			return true
		}
	}
	return false
}

// accountName names a SID as DOMAIN\name, or as *S-1-... when it has no name.
// icacls accepts both forms.
func accountName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	if err != nil || account == "" {
		return "*" + sid.String()
	}
	if domain == "" {
		return account
	}
	return domain + `\` + account
}

// currentUserSIDs gives the SID of this user and the SID that owns the files
// this process makes: the same SID, or the Administrators group for an
// elevated process.
func currentUserSIDs() (user, fileOwner *windows.SID, err error) {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	n := uint32(64)
	for range 3 {
		b := make([]byte, n)
		err = windows.GetTokenInformation(tok, windows.TokenOwner, &b[0], n, &n)
		if err == nil {
			return tu.User.Sid, (*struct{ Owner *windows.SID })(unsafe.Pointer(&b[0])).Owner, nil
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER {
			return nil, nil, err
		}
	}
	return nil, nil, err
}
