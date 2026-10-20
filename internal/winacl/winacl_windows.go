//go:build windows

package winacl

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WriteAccess is every right that lets an account change a file's content,
// replace it, or change who may: the rights a Unix mode&022 check covers.
const WriteAccess = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
	windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE |
	windows.GENERIC_WRITE | windows.GENERIC_ALL

// ACE types beyond the two x/sys names. Only ACCESS_ALLOWED_ACE and its
// callback form have the layout ACCESS_ALLOWED_ACE describes: a mask, then a
// SID. The deny types only take access away.
const (
	accessAllowedCallbackACEType = 0x9
	accessDeniedObjectACEType    = 0x6
	accessDeniedCallbackACEType  = 0xA
	accessDeniedCallbackObjType  = 0xC
)

// FileSecurity reads the owner and the DACL of the open file h. Reading them
// from the handle means the file cannot be swapped between the check and
// the read.
func FileSecurity(h windows.Handle) (*windows.SECURITY_DESCRIPTOR, error) {
	return windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
}

// CurrentUser gives the SID of this process's user and the SID that owns the
// files this process makes: the same SID, or the Administrators group for an
// elevated process.
func CurrentUser() (user, fileOwner *windows.SID, err error) {
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

// Trusted is the given SIDs plus the accounts that may always have access to
// a file of this user: SYSTEM, Administrators and OWNER RIGHTS. SYSTEM and
// Administrators can read any file anyway. OWNER RIGHTS stands for the
// owner, which the caller has already checked.
func Trusted(sids ...*windows.SID) ([]*windows.SID, error) {
	out := append([]*windows.SID(nil), sids...)
	for _, known := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid, windows.WinCreatorOwnerRightsSid,
	} {
		sid, err := windows.CreateWellKnownSid(known)
		if err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, nil
}

// GrantsOther returns the name of the first account that the DACL in sd
// grants any right in mask to and that is not in trusted, or "" when there
// is none. A missing or NULL DACL lets everyone in. A deny entry is not
// weighed against an allow entry: ignoring it only makes the check refuse
// more. An entry of a type the check does not know is refused.
func GrantsOther(sd *windows.SECURITY_DESCRIPTOR, mask uint32, trusted []*windows.SID) (string, error) {
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
		case windows.ACCESS_DENIED_ACE_TYPE, accessDeniedObjectACEType,
			accessDeniedCallbackACEType, accessDeniedCallbackObjType:
			continue
		default:
			// An allow entry of another layout (object, compound) or a type
			// this check does not know. A file does not normally carry one.
			// Refuse rather than guess what it grants.
			return fmt.Sprintf("An access entry of type %#x", ace.Header.AceType), nil
		}
		if uint32(ace.Mask)&mask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !containsSID(trusted, sid) {
			return AccountName(sid), nil
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

// AccountName names a SID as DOMAIN\name, or as *S-1-... when it has no
// name. icacls accepts both forms.
func AccountName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	if err != nil || account == "" {
		return "*" + sid.String()
	}
	if domain == "" {
		return account
	}
	return domain + `\` + account
}
