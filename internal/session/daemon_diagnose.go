package session

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"
)

// This file answers one question precisely: why can this process not talk to a
// daemon? "Not running" covers several genuinely different states, and each one
// has a different fix, so collapsing them into a single message is what leaves a
// user stuck.

// DaemonState classifies the daemon socket's condition.
type DaemonState int

const (
	// DaemonRunning means a process accepted a connection on the socket.
	DaemonRunning DaemonState = iota
	// DaemonAbsent means no socket file exists: the daemon has never run, or
	// was shut down cleanly.
	DaemonAbsent
	// DaemonStaleSocket means the socket file exists but nothing is listening
	// on it, which is what a crashed or SIGKILLed daemon leaves behind.
	DaemonStaleSocket
	// DaemonPermissionDenied means the socket exists but this user may not
	// connect to it, typically another user's socket or a bad mode.
	DaemonPermissionDenied
	// DaemonUnreachable is any other connection failure.
	DaemonUnreachable
)

// DaemonDiagnosis describes the daemon socket's condition, with enough detail
// for a caller to write a message that names the fix.
type DaemonDiagnosis struct {
	State DaemonState
	// SocketPath is the socket that was probed, empty only when the path itself
	// could not be resolved.
	SocketPath string
	// PID is the daemon's process id when it is running and left a pid file.
	PID int
	// Err is the underlying failure, when there was one.
	Err error
}

// Running reports whether a daemon is actually accepting connections.
func (d DaemonDiagnosis) Running() bool { return d.State == DaemonRunning }

// DiagnoseDaemon probes the daemon socket and classifies what it finds.
//
// It is deliberately more informative than IsDaemonRunning, which answers only
// yes or no: a stale socket and a permission-denied socket both read as "not
// running" there, yet one is fixed by starting a session and the other is not
// fixed by anything the user would think to try.
func DiagnoseDaemon() DaemonDiagnosis {
	socketPath, err := GetSocketPath()
	if err != nil {
		return DaemonDiagnosis{State: DaemonUnreachable, Err: err}
	}

	d := DaemonDiagnosis{SocketPath: socketPath}

	if _, statErr := os.Stat(socketPath); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			d.State = DaemonAbsent
			return d
		}
		if errors.Is(statErr, fs.ErrPermission) {
			d.State = DaemonPermissionDenied
			d.Err = statErr
			return d
		}
		d.State = DaemonUnreachable
		d.Err = statErr
		return d
	}

	conn, dialErr := net.DialTimeout("unix", socketPath, time.Second)
	if dialErr == nil {
		_ = conn.Close()
		d.State = DaemonRunning
		d.PID = GetDaemonPID()
		return d
	}

	d.Err = dialErr
	switch {
	case errors.Is(dialErr, syscall.ECONNREFUSED), errors.Is(dialErr, syscall.ENOENT):
		// The file is there but no process is behind it: a crashed daemon.
		d.State = DaemonStaleSocket
	case errors.Is(dialErr, fs.ErrPermission), errors.Is(dialErr, syscall.EACCES):
		d.State = DaemonPermissionDenied
	default:
		d.State = DaemonUnreachable
	}
	return d
}

// ErrShutdownTimeout reports that a daemon was asked to stop but did not
// finish within the caller's deadline. It is distinct from a failure to signal
// the daemon: the request was delivered, the confirmation never arrived.
var ErrShutdownTimeout = errors.New("timed out waiting for the daemon to finish shutting down")

// WaitForDaemonShutdown blocks until a daemon that was asked to stop has
// finished, or until timeout elapses.
//
// The completion signal is the socket file being unlinked, which Daemon.shutdown
// does last, after every session has written its final resurrection state. That
// makes this a persistence guarantee and not merely a liveness one: a refused
// connection would prove neither, because the listener closes at the top of
// shutdown while state is still unsaved.
//
// It returns ErrShutdownTimeout if the deadline passes with the socket still in
// place, so a caller can say that the daemon is wedged rather than reporting
// success and letting a subsequent start race the old process.
func WaitForDaemonShutdown(timeout time.Duration) error {
	socketPath, err := GetSocketPath()
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for {
		if _, statErr := os.Stat(socketPath); errors.Is(statErr, fs.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrShutdownTimeout
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Explain renders the diagnosis as a message that states what failed, the most
// likely cause, and the exact command that resolves it. It returns an empty
// string when the daemon is running, so a caller can use it as a guard.
func (d DaemonDiagnosis) Explain() string {
	switch d.State {
	case DaemonRunning:
		return ""

	case DaemonAbsent:
		return "The TUIOS daemon is not running.\n" +
			"Most likely cause: no session has been started yet, or the daemon was shut down.\n" +
			"Fix: run 'tuios new' to start a session, or 'tuios start-server' for a daemon with no session."

	case DaemonStaleSocket:
		return fmt.Sprintf("The TUIOS daemon is not running, but a stale socket is left over at %s.\n"+
			"Most likely cause: the daemon crashed or was killed without cleaning up.\n"+
			"Fix: run 'tuios kill-server' to clear it, then 'tuios new' to start a session.", d.SocketPath)

	case DaemonPermissionDenied:
		return fmt.Sprintf("Permission denied connecting to the TUIOS daemon socket at %s.\n"+
			"Most likely cause: the socket belongs to another user, or its directory permissions changed.\n"+
			"Fix: check 'ls -l %s'. If it belongs to another user, set XDG_RUNTIME_DIR to a directory you own; if it is yours, remove it and run 'tuios new'.",
			d.SocketPath, d.SocketPath)

	default:
		reason := "unknown error"
		if d.Err != nil {
			reason = d.Err.Error()
		}
		return fmt.Sprintf("Could not reach the TUIOS daemon at %s: %s.\n"+
			"Most likely cause: the socket path is unusable (a full or read-only XDG_RUNTIME_DIR, or a leftover file that is not a socket).\n"+
			"Fix: run 'tuios kill-server', then 'tuios new'. If that fails, remove %s and try again.",
			d.SocketPath, reason, d.SocketPath)
	}
}
