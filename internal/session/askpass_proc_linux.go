package session

import (
	"os"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// canWaitForeignExit says waitForeignExit blocks until the process ends.
const canWaitForeignExit = true

// askpassParent is the parent of pid.
func askpassParent(pid int) (int, bool) { return procinfo.ParentPID(pid) }

// askingProgram reads the resolved executable of pid and its arguments from
// the kernel. Either is empty when it cannot be read.
func askingProgram(pid int) (exe string, args []string) {
	dir := "/proc/" + strconv.Itoa(pid)
	exe, _ = os.Readlink(dir + "/exe")
	raw, err := os.ReadFile(dir + "/cmdline")
	if err != nil || len(raw) == 0 {
		return exe, nil
	}
	return exe, strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
}
