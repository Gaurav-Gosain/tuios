//go:build unix

package herdrplugin

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// RunReaper runs argv as a reaper and returns its exit code: the command's,
// or 127 when it could not start. See reaper.go.
func RunReaper(argv []string) int {
	syscall.CloseOnExec(3)
	status := os.NewFile(3, "status")
	if len(argv) == 0 {
		return 127
	}
	becomeSubreaper()
	//tuios:allow-exec the reaper runs the plugin command it was given
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the runner resolved the plugin's own command
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
	cmd.Env = commandEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		if status != nil {
			fmt.Fprintf(status, "127\t%s\n", err)
			_ = status.Close()
		}
		return 127
	}
	child := cmd.Process.Pid
	code := -1
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			break
		}
		if pid != child {
			continue
		}
		code = ws.ExitStatus()
		text := fmt.Sprintf("exit status %d", code)
		if ws.Signaled() {
			code, text = -1, "signal: "+ws.Signal().String()
		}
		if status != nil {
			fmt.Fprintf(status, "%d\t%s\n", code, text)
			_ = status.Close()
			status = nil
		}
		if !waitsForGroup {
			break
		}
	}
	waitForGroup()
	if code < 0 {
		return 1
	}
	return code
}
