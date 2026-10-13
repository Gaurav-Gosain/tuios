//go:build !windows

package sockpath

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
)

// The roots the folder is chosen from. They are variables so a test can move
// them; see SetRootsForTest and e2eRoots.
var (
	// runUserRoot holds the per-user runtime folders systemd makes:
	// /run/user/<uid>, created by pam_systemd for every login, ssh included.
	runUserRoot = "/run/user"
	// tmpRoot holds the folder used when there is no runtime folder.
	tmpRoot = "/tmp"
)

// Dir is the folder the daemon's socket lives in: BaseDir for the default
// server, and BaseDir/servers/<name> for a named one. It only names the
// folder; the daemon creates and checks it.
func Dir() string {
	return serverscope.SocketDir(BaseDir())
}

// BaseDir is the default server's socket folder: $XDG_RUNTIME_DIR/tuios, or
// /run/user/<uid>/tuios when XDG_RUNTIME_DIR is unset and that folder is this
// user's own private runtime folder, or /tmp/tuios-<uid> otherwise.
//
// The /run/user step is what keeps one user on one daemon. A desktop session
// sets XDG_RUNTIME_DIR, and a command run over ssh (a host link, hosts sync,
// a paired device) often does not. Without the step the two picked different
// folders and started two daemons that could not see each other's sessions.
func BaseDir() string {
	if rt := RuntimeDir(); rt != "" {
		return filepath.Join(rt, "tuios")
	}
	return LegacyDir()
}

// RuntimeDir is the user's runtime folder: $XDG_RUNTIME_DIR when it is set,
// else /run/user/<uid> when that is a private folder of this user, else "".
// macOS has no /run/user, and the step is skipped there.
func RuntimeDir() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return rt
	}
	if dir := systemRuntimeDir(); dir != "" && PrivateDir(dir, os.Getuid()) {
		return dir
	}
	return ""
}

// LegacyDir is the folder used without a runtime folder: /tmp/tuios-<uid>.
func LegacyDir() string {
	_, tmp := roots()
	return filepath.Join(tmp, fmt.Sprintf("tuios-%d", os.Getuid()))
}

// OtherDir is the second folder a daemon of this user may already listen in,
// or "" when there is none to look at. It is LegacyDir when Dir is the systemd
// runtime folder, whether XDG_RUNTIME_DIR named it or it was found: a daemon
// an older tuios started over ssh is there. A runtime folder set to anything
// else (a test, a script that wants a daemon of its own) has no other folder,
// and neither has a named server: no older tuios started one.
func OtherDir() string {
	sys := systemRuntimeDir()
	if sys == "" || filepath.Clean(Dir()) != filepath.Join(sys, "tuios") {
		return ""
	}
	return LegacyDir()
}

// PrivateDir reports whether dir is a real folder (not a link), owned by uid,
// and closed to everyone else. Whoever controls the folder of a socket can put
// their own socket there, so a folder tuios did not make is used only when
// this holds.
func PrivateDir(dir string, uid int) bool {
	st, err := os.Lstat(dir)
	if err != nil || !st.Mode().IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != uid {
		return false
	}
	return st.Mode().Perm() == 0o700
}

// systemRuntimeDir is /run/user/<uid>, or "" on macOS.
func systemRuntimeDir() string {
	if runtime.GOOS == "darwin" {
		return ""
	}
	run, _ := roots()
	return filepath.Join(run, strconv.Itoa(os.Getuid()))
}

// roots returns the two roots. The end-to-end suite moves them with
// TUIOS_E2E_RUN_USER and TUIOS_E2E_TMP, only under TUIOS_E2E=1, so it can
// test a missing XDG_RUNTIME_DIR without reaching the real daemons of the
// person who runs it.
func roots() (run, tmp string) {
	run, tmp = runUserRoot, tmpRoot
	if os.Getenv("TUIOS_E2E") == "1" {
		if v := os.Getenv("TUIOS_E2E_RUN_USER"); v != "" {
			run = v
		}
		if v := os.Getenv("TUIOS_E2E_TMP"); v != "" {
			tmp = v
		}
	}
	return run, tmp
}

// SetRootsForTest moves /run/user and /tmp for a test, and returns the
// function that puts them back.
func SetRootsForTest(runUser, tmp string) (restore func()) {
	oldRun, oldTmp := runUserRoot, tmpRoot
	runUserRoot, tmpRoot = runUser, tmp
	return func() { runUserRoot, tmpRoot = oldRun, oldTmp }
}
