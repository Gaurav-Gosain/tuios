package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SocketEnv is the variable a pane is given its daemon's socket in.
const SocketEnv = "TUIOS_SOCKET"

// SocketEnvError is the refusal checkSocketEnv returns. It is complete on its
// own: it names the variable, both sockets and what to set instead.
type SocketEnvError struct{ msg string }

func (e *SocketEnvError) Error() string { return e.msg }

var socketEnvCheck struct {
	sync.Mutex
	env, path string
	err       error
}

// CheckSocketEnv reports the TUIOS_SOCKET mistake, if it is being made, for
// the socket this process would use. GetSocketPath refuses the same way, but
// IsDaemonRunning reads that refusal as "no daemon" and a command would then
// try to start one; a command that may start a daemon asks here first.
func CheckSocketEnv() error {
	path, err := defaultSocketPath()
	if err != nil {
		return nil
	}
	return checkSocketEnv(path)
}

func checkSocketEnv(path string) error {
	env := os.Getenv(SocketEnv)
	if env == "" || filepath.Clean(env) == filepath.Clean(path) {
		return nil
	}
	socketEnvCheck.Lock()
	defer socketEnvCheck.Unlock()
	if socketEnvCheck.env == env && socketEnvCheck.path == path {
		return socketEnvCheck.err
	}
	var err error
	if !isDaemonRunningAt(env) {
		err = &SocketEnvError{msg: fmt.Sprintf("%s is set to %s, where no daemon is listening, and this command would use the daemon at %s instead. "+
			"%s only reports the daemon a pane belongs to; it does not choose one. "+
			"To use a separate daemon, run the command with --server NAME (or set TUIOS_SERVER=NAME), which gives it its own socket and its own saved sessions. "+
			"Otherwise unset %s",
			SocketEnv, env, path, SocketEnv, SocketEnv)}
	}
	socketEnvCheck.env, socketEnvCheck.path, socketEnvCheck.err = env, path, err
	return err
}
