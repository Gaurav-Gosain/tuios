package ptyspawn

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
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

var processCwdOffForTest = sync.OnceValue(func() bool {
	return testknob.String("no_process_cwd") == "1"
})
