package session

import (
	"bytes"
	"os"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// canWaitForeignExit says waitForeignExit blocks until the process ends.
const canWaitForeignExit = true

// askpassParent is the parent of pid.
func askpassParent(pid int) (int, bool) { return procinfo.ParentPID(pid) }

// askingProgram reads the executable and the arguments of pid from the
// kernel. Either is empty when it cannot be read.
func askingProgram(pid int) (exe string, args []string) {
	dir := "/proc/" + strconv.Itoa(pid)
	exe, _ = os.Readlink(dir + "/exe")
	raw, err := os.ReadFile(dir + "/cmdline")
	if err != nil {
		return exe, nil
	}
	for a := range bytes.SplitSeq(bytes.TrimRight(raw, "\x00"), []byte{0}) {
		args = append(args, string(a))
	}
	return exe, args
}
