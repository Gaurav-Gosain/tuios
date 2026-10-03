package learnssh

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// sandbox locks the session process down before it reads a byte from the
// reader. tuios in Learn mode with the fake shell touches no file, opens no
// socket and runs no program, so the process is told it may do none of those:
//
//   - Landlock with every filesystem right handled and no rule granted, so
//     any open, write or exec fails. On a kernel with Landlock ABI 4 or
//     later, TCP bind and connect fail too, and with ABI 6 signals and
//     abstract unix sockets outside the process are refused.
//   - no_new_privs, which Landlock needs, on every thread.
//   - Resource limits: no core dumps, no file growth, few descriptors.
//
// Landlock applies per thread, and the Go runtime has several by now, so
// both calls go through AllThreadsSyscall. That needs a build without cgo,
// which is how cmd/tuios-learn is built.
func sandbox() error {
	for _, l := range []struct {
		res int
		val uint64
	}{
		{unix.RLIMIT_CORE, 0},
		{unix.RLIMIT_FSIZE, 0},
		{unix.RLIMIT_NOFILE, 32},
	} {
		if err := unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.val, Max: l.val}); err != nil {
			return fmt.Errorf("rlimit %d: %w", l.res, err)
		}
	}

	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return fmt.Errorf("landlock unavailable: %w", errno)
	}
	attr := unix.LandlockRulesetAttr{Access_fs: fsRights(int(abi))}
	if abi >= 4 {
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
	}
	if abi >= 6 {
		attr.Scoped = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock ruleset: %w", errno)
	}
	defer unix.Close(int(fd))

	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0); errno != 0 {
		return fmt.Errorf("no_new_privs: %w", errno)
	}
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock restrict: %w", errno)
	}
	return nil
}

// fsRights is every filesystem right the kernel's Landlock ABI knows.
func fsRights(abi int) uint64 {
	rights := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		rights |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		rights |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		rights |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return rights
}
