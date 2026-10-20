package transcriptview

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Gaurav-Gosain/tuios/internal/winacl"
)

var procGetExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	// tcpTableOwnerPIDAll is TCP_TABLE_OWNER_PID_ALL.
	tcpTableOwnerPIDAll = 5
	// stillActive is STILL_ACTIVE, the exit code of a running process.
	stillActive = 259
	// tcpTableMax bounds the table this reader allocates.
	tcpTableMax = 16 << 20
)

// currentUser gives the SID of this user, which the service's process must
// run as, and the SID that owns the files this process makes: the same SID,
// or the Administrators group for an elevated process. A test changes it to
// see the checks refuse.
var currentUser = winacl.CurrentUser

// serverOwner reports the server side of c as this user's when the process
// service.json names owns it in the TCP table and runs as this user. The
// process stays open while the table is read, so its pid cannot pass to
// another process in between.
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

// extendedTCPTable is the IPv4 TCP table with the owning pid of each row.
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
			// Room for rows that open before the next call.
			size += 4 << 10
		default:
			return nil, false
		}
	}
	return nil, false
}

// serviceFileSafe reports whether service.json is owned by this user, or
// by the owner this user's processes give their files, and whether no one
// else can change it: whoever can write it chooses where the password goes.
// Windows keeps no Unix mode, so the owner and the DACL in the file's
// security descriptor are what is checked, the Windows form of mode&022.
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
