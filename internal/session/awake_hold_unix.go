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

type awakeHolder struct {
	cur *awakeProc
}

type awakeProc struct {
	cmd      *exec.Cmd
	w        *os.File
	released atomic.Bool
	exited   chan struct{}
}

func awakeCommand(why string) (string, []string) {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/caffeinate", []string{"-i", "-s"}
	}
	return "systemd-inhibit", []string{"--what=idle:sleep", "--who=tuios", "--why=" + why, "--mode=block"}
}

func awakeMethod() string {
	if runtime.GOOS == "darwin" {
		return "caffeinate"
	}
	return "systemd-inhibit"
}

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
	select {
	case err := <-waitErr:
		_ = w.Close()
		return err
	case <-time.After(awakeHoldStartWait):
	}
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
	_ = p.w.Close()
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
}
