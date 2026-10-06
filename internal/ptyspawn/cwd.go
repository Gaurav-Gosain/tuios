package ptyspawn

import (
	"os"
	"sync"
)

// ProcessCwd returns the working directory of process pid, and says whether
// it got one. The read is the platform's business (see processCwd), and on a
// platform with no way to read it, native Windows among them, the answer is
// always false. A caller that wants a pane's folder there has to take what the
// shell reported over OSC 7.
func ProcessCwd(pid int) (string, bool) {
	if processCwdOffForTest() {
		return "", false
	}
	return processCwd(pid)
}

// processCwdOffForTest makes every process read fail, as it does on native
// Windows, so the end-to-end tests can prove on Linux that a pane's folder
// still comes from OSC 7 there. It holds only with TUIOS_E2E=1 and
// TUIOS_E2E_NO_PROCESS_CWD=1, which ordinary runs never set.
var processCwdOffForTest = sync.OnceValue(func() bool {
	return os.Getenv("TUIOS_E2E") == "1" && os.Getenv("TUIOS_E2E_NO_PROCESS_CWD") == "1"
})
