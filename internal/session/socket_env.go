package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// TUIOS_SOCKET is set in every pane to the socket of the daemon that runs it.
// It reports; it does not select. The daemon a command reaches is chosen by
// XDG_RUNTIME_DIR (LOCALAPPDATA on Windows), as GetSocketPath says.
//
// It cannot become the selector. Every process started from a pane inherits
// it, so a script run from a pane that isolates itself with its own
// XDG_RUNTIME_DIR, which is how the test suites and the demo recorder keep
// away from the person's daemon, would be sent straight back to that daemon.
//
// What it can do is stop the mistake the name invites. Someone who sets it to
// a path where no daemon listens wants a daemon of their own there, and would
// otherwise get the one XDG_RUNTIME_DIR names, which is usually the person's:
// an agent created a session in the person's daemon exactly that way. So when
// it names a socket other than the one a command would reach, and nothing
// listens on it, the command refuses and says what to set instead. When it
// names the same socket, or another live daemon's (the isolated script above),
// nothing changes.

// SocketEnv is the variable a pane is given its daemon's socket in.
const SocketEnv = "TUIOS_SOCKET"

// SocketEnvError is the refusal checkSocketEnv returns. It is complete on its
// own: it names the variable, both sockets and what to set instead.
type SocketEnvError struct{ msg string }

func (e *SocketEnvError) Error() string { return e.msg }

// socketEnvCheck caches the check for one pair of TUIOS_SOCKET and resolved
// path, since GetSocketPath is called often and the check may dial.
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

// checkSocketEnv refuses when TUIOS_SOCKET names a socket other than path and
// no daemon listens on it.
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
			"To use a separate daemon, set XDG_RUNTIME_DIR (LOCALAPPDATA on Windows) and XDG_STATE_HOME, so it keeps its own saved sessions, to directories of your own; "+
			"its socket is then $XDG_RUNTIME_DIR/tuios/tuios.sock. Otherwise unset %s",
			SocketEnv, env, path, SocketEnv, SocketEnv)}
	}
	socketEnvCheck.env, socketEnvCheck.path, socketEnvCheck.err = env, path, err
	return err
}
