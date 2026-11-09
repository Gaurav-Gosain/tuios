package ptyspawn

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ProcessCwd returns the working directory of process pid, and says whether
// it got one. The read is the platform's business (see processCwd), and on a
// platform with no way to read it, native Windows among them, the answer is
// always false. A caller that wants a pane's folder there has to take what the
// shell reported over OSC 7.
//
// A folder under the home folder is spelled the way $HOME spells it (see
// homeSpelled).
func ProcessCwd(pid int) (string, bool) {
	if processCwdOffForTest() {
		return "", false
	}
	cwd, ok := processCwd(pid)
	return homeSpelled(cwd), ok
}

// homes is the home folder as $HOME spells it and with its symbolic links
// resolved, read once. Either is "" when it cannot be read.
var homes = sync.OnceValues(func() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", ""
	}
	home = filepath.Clean(home)
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return home, ""
	}
	return home, filepath.Clean(real)
})

// homeSpelled writes a folder the kernel gave under the home folder the way
// $HOME spells it. The kernel resolves the symbolic links in a process's
// folder, so with a home reached through one (FreeBSD's /home, a home under
// /var on macOS, an automounted one) a shell in ~/proj is in
// /usr/home/me/proj, and nothing that writes a folder under home as ~ (the
// path picker, the rail) sees that it is under home. Only a folder that is
// the resolved home or lies under it, a whole path component at a time, is
// spelled again: one outside home, or one whose name only starts with the
// same letters, is left as the kernel gave it.
func homeSpelled(path string) string {
	home, real := homes()
	if path == "" || home == "" || real == "" || real == home || real == string(filepath.Separator) {
		return path
	}
	if path == real {
		return home
	}
	if rest, ok := strings.CutPrefix(path, real+string(filepath.Separator)); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// processCwdOffForTest makes every process read fail, as it does on native
// Windows, so the end-to-end tests can prove on Linux that a pane's folder
// still comes from OSC 7 there. It holds only with TUIOS_E2E=1 and
// TUIOS_E2E_NO_PROCESS_CWD=1, which ordinary runs never set.
var processCwdOffForTest = sync.OnceValue(func() bool {
	return os.Getenv("TUIOS_E2E") == "1" && os.Getenv("TUIOS_E2E_NO_PROCESS_CWD") == "1"
})
