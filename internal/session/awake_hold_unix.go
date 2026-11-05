//go:build linux || darwin

package session

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// awakeHolder holds the machine awake with a child process: on Linux
// systemd-inhibit, which takes logind's inhibitor lock on idle and sleep, and
// on macOS caffeinate, which takes a power assertion against idle sleep and,
// on AC power, system sleep. Each runs cat as its command, and cat reads a
// pipe whose write end only the daemon has. The lock lasts as long as cat
// runs, so it ends when the daemon closes the pipe: on release, and on its
// own when the daemon exits or execs a new image, since Go opens the pipe
// close-on-exec. No signal is needed and nothing is left behind.
//
// The idea and the systemd-inhibit and caffeinate arguments come from Kero
// (github.com/egoist/kero, awake_linux.go and awake_darwin.go, MIT). See
// NOTICE.
type awakeHolder struct {
	cur *awakeProc
}

type awakeProc struct {
	cmd      *exec.Cmd
	w        *os.File
	released atomic.Bool
	exited   chan struct{}
}

// awakeCommand is the holder's command line, cat excluded.
func awakeCommand(why string) (string, []string) {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/caffeinate", []string{"-i", "-s"}
	}
	return "systemd-inhibit", []string{"--what=idle:sleep", "--who=tuios", "--why=" + why, "--mode=block"}
}

// awakeMethod names the holder for status and doctor output.
func awakeMethod() string {
	if runtime.GOOS == "darwin" {
		return "caffeinate"
	}
	return "systemd-inhibit"
}

// awakeHoldStartWait is how long take waits for a holder that fails at once,
// such as systemd-inhibit refused by logind, to say so.
const awakeHoldStartWait = 300 * time.Millisecond

func (h *awakeHolder) take(why string, lost chan<- error) error {
	name, args := awakeCommand(why)
	path, err := exec.LookPath(name)
	if err != nil {
		return errfmt.Errorf("%s is not installed", name)
	}
	cat, err := exec.LookPath("cat")
	if err != nil {
		return errors.New("cat is not installed")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := childproc.Command(path, append(args, cat)...)
	cmd.Stdin = r
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return err
	}
	// The child has its copy of the read end.
	_ = r.Close()
	p := &awakeProc{cmd: cmd, w: w, exited: make(chan struct{})}
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		close(p.exited)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		if msg == "" {
			msg = "exited"
		}
		waitErr <- errfmt.Errorf("%s stopped: %s", name, msg)
	}()
	// A holder the system refuses exits at once. Wait a short time for that,
	// so the caller can say why instead of reporting a hold that is not
	// there. A holder that runs is not waited for.
	select {
	case err := <-waitErr:
		_ = w.Close()
		return err
	case <-time.After(awakeHoldStartWait):
	}
	// From here an exit that release did not ask for is a hold lost.
	go func() {
		err := <-waitErr
		if p.released.Load() {
			return
		}
		select {
		case lost <- err:
		default:
		}
	}()
	h.cur = p
	return nil
}

func (h *awakeHolder) release() {
	p := h.cur
	h.cur = nil
	if p == nil {
		return
	}
	p.released.Store(true)
	// Closing the pipe ends cat, and with it the holder and its lock.
	_ = p.w.Close()
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
}
