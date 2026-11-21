package transcriptview

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Gaurav-Gosain/tuios/internal/winacl"
)

var procGetExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	tcpTableOwnerPIDAll = 5
	stillActive         = 259
	tcpTableMax         = 16 << 20
)

var currentUser = winacl.CurrentUser

func serverOwner(c serverSide, svcPID int) owner {
	if svcPID <= 0 || svcPID > 1<<31-1 {
		return ownerOther
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(svcPID))
	if err != nil {
		return ownerOther
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code != stillActive {
		return ownerOther
	}
	var tok windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) != nil {
		return ownerOther
	}
	defer func() { _ = tok.Close() }()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return ownerOther
	}
	me, _, err := currentUser()
	if err != nil || !tu.User.Sid.Equals(me) {
		return ownerOther
	}
	buf, ok := extendedTCPTable()
	if !ok {
		return ownerOther
	}
	pid, own := tcpTableOwner(buf, c)
	if own == ownerMine && pid != uint32(svcPID) {
		return ownerOther
	}
	return own
}

func extendedTCPTable() ([]byte, bool) {
	if procGetExtendedTCPTable.Find() != nil {
		return nil, false
	}
	size := uint32(64 << 10)
	for range 4 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTCPTable.Call(
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
			0, windows.AF_INET, tcpTableOwnerPIDAll, 0)
		switch windows.Errno(r) {
		case 0:
			return buf, true
		case windows.ERROR_INSUFFICIENT_BUFFER:
			if size > tcpTableMax {
				return nil, false
			}
			size += 4 << 10
		default:
			return nil, false
		}
	}
	return nil, false
}

func serviceFileSafe(f *os.File, _ os.FileInfo) bool {
	sd, err := winacl.FileSecurity(windows.Handle(f.Fd()))
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return false
	}
	me, mine, err := currentUser()
	if err != nil || (!owner.Equals(me) && !owner.Equals(mine)) {
		return false
	}
	trusted, err := winacl.Trusted(me, mine, owner)
	if err != nil {
		return false
	}
	other, err := winacl.GrantsOther(sd, winacl.WriteAccess, trusted)
	return err == nil && other == ""
}
