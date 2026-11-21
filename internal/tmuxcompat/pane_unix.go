//go:build !windows

package tmuxcompat

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/shimlink"
	"golang.org/x/term"
)

const holderSupported = true

// EnsureDir creates the shim's runtime directory, or checks an existing one.
// See shimlink.EnsureDir.
func EnsureDir(dir string) error { return shimlink.EnsureDir(dir) }

// InstallLink points <dir>/bin/tmux at exe, replacing whatever was there.
func InstallLink(dir, exe string) error {
	_, err := shimlink.Install(dir, "tmux", exe)
	return err
}

// ExecCommand replaces the process with argv, found on the PATH of env and
// run with env. It returns only on failure.
func ExecCommand(argv, env []string) error {
	if len(argv) == 0 {
		return errors.New("no command to run")
	}
	path, err := lookPathIn(argv[0], env)
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, env)
}

func lookPathIn(name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errfmt.Errorf("%s: command not found", name)
}

func (o PaneOptions) start(cmd []string, cwd string, extra []string) (*exec.Cmd, error) {
	env := paneEnv(os.Environ(), o.Dir, o.Window, append(append([]string(nil), o.Env...), extra...))
	path, argv := paneArgv(cmd, o.Shell)
	full, err := lookPathIn(path, env)
	if err != nil {
		return nil, err
	}
	c := &exec.Cmd{Path: full, Args: argv, Env: env, Dir: cwd, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		c.SysProcAttr.Foreground = true
		c.SysProcAttr.Ctty = 0
		if err := c.Start(); err == nil {
			return c, nil
		}
		c = &exec.Cmd{Path: full, Args: argv, Env: env, Dir: cwd, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
			SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

var errPaneEnded = errors.New("the pane's command ended")

var (
	beforeReply func()
	afterExit   func(waiting *atomic.Int32)
)

type respawnCall struct {
	req   RespawnRequest
	reply func(error)
}

// RunPane is the holder: it runs the pane's command and answers respawn
// requests until the running command exits, and returns its exit status.
func RunPane(o PaneOptions) int {
	if o.Window == "" {
		fmt.Fprintln(os.Stderr, "tuios tmux-pane: TUIOS_PANE_ID is unset; the holder runs as a tuios pane's process")
		return 1
	}
	drop := make(chan os.Signal, 4)
	signal.Notify(drop, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		for range drop {
		}
	}()
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM)

	calls := make(chan respawnCall)
	var waiting atomic.Int32
	var ln net.Listener
	if EnsureDir(o.Dir) == nil && EnsureDir(filepath.Join(o.Dir, "p")) == nil {
		if exe, err := os.Executable(); err == nil {
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			_ = InstallLink(o.Dir, exe)
		}
		sock := paneSocket(o.Dir, o.Window)
		if c, err := net.DialTimeout("unix", sock, 500*time.Millisecond); err == nil {
			_ = c.Close()
		} else {
			_ = os.Remove(sock)
			if l, err := net.Listen("unix", sock); err == nil {
				_ = os.Chmod(sock, 0o600)
				ln = l
				defer func() {
					_ = ln.Close()
					_ = os.Remove(sock)
				}()
				go acceptRespawns(ln, o.Window, o.Authorize, calls, &waiting)
			}
		}
	}

	child, err := o.start(o.Command, "", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	first := o.Command
	done := make(chan int, 1)
	wait := func(c *exec.Cmd) {
		_ = c.Wait()
		done <- exitCode(c)
	}
	go wait(child)

	for {
		select {
		case code := <-done:
			if afterExit != nil {
				afterExit(&waiting)
			}
			if ln != nil {
				_ = ln.Close()
			}
			for waiting.Load() > 0 {
				call := <-calls
				waiting.Add(-1)
				call.reply(errPaneEnded)
			}
			return code
		case s := <-sigs:
			if sig, ok := s.(syscall.Signal); ok {
				_ = syscall.Kill(-child.Process.Pid, sig)
			}
		case call := <-calls:
			waiting.Add(-1)
			cmd := call.req.Command
			if len(cmd) == 0 {
				cmd = first
			}
			stop(child, done)
			next, err := o.start(cmd, call.req.Cwd, call.req.Env)
			if err != nil {
				call.reply(err)
				fmt.Fprintln(os.Stderr, err)
				return 127
			}
			call.reply(nil)
			child = next
			go wait(child)
		}
	}
}

func stop(c *exec.Cmd, done chan int) {
	pgid := c.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGHUP)
	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	<-done
}

func exitCode(c *exec.Cmd) int {
	if c.ProcessState == nil {
		return 1
	}
	if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return c.ProcessState.ExitCode()
}

func acceptRespawns(ln net.Listener, window string, authorize func(int, uint64) error, calls chan<- respawnCall, waiting *atomic.Int32) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		pid := procinfo.PeerPID(conn)
		start, _ := procinfo.StartTime(pid)
		go func() {
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			line, err := bufio.NewReader(io.LimitReader(conn, 1<<20)).ReadBytes('\n')
			if err != nil {
				return
			}
			var req RespawnRequest
			if err := jsonx.Unmarshal(line, &req); err != nil {
				writeReply(conn, errors.New("the request is not JSON"))
				return
			}
			if req.Window != window {
				writeReply(conn, errfmt.Errorf("this holder runs window %s, not %s", window, req.Window))
				return
			}
			if authorize != nil {
				if err := authorize(pid, start); err != nil {
					writeReply(conn, err)
					return
				}
			}
			replied := make(chan struct{})
			waiting.Add(1)
			calls <- respawnCall{req: req, reply: func(err error) {
				if beforeReply != nil {
					beforeReply()
				}
				writeReply(conn, err)
				close(replied)
			}}
			<-replied
		}()
	}
}

func writeReply(w io.Writer, err error) {
	r := respawnReply{OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	}
	line, _ := jsonx.Marshal(r)
	_, _ = w.Write(append(line, '\n'))
}

func paneArgv(cmd []string, shell string) (path string, argv []string) {
	switch len(cmd) {
	case 0:
		if shell == "" {
			shell = "/bin/sh"
		}
		base := shell
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		return shell, []string{"-" + base}
	case 1:
		return "/bin/sh", []string{"sh", "-c", cmd[0]}
	}
	return cmd[0], cmd
}
