//go:build !windows

package tmuxcompat

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// holderSupported reports whether this platform runs pane holders.
const holderSupported = true

// EnsureDir creates the shim's runtime directory, or checks an existing one:
// it must be a real directory (not a link), owned by this user, and closed to
// everyone else, since a socket in it accepts respawn requests.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory, which needs the execute bit to be entered
			return fmt.Errorf("%s is open to other users and could not be closed: %w", dir, err)
		}
	}
	return nil
}

// InstallLink points <dir>/bin/tmux at exe, replacing whatever was there.
func InstallLink(dir, exe string) error {
	bin := BinDir(dir)
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	link := filepath.Join(bin, "tmux")
	if cur, err := os.Readlink(link); err == nil && cur == exe {
		return nil
	}
	tmp := fmt.Sprintf("%s.%d", link, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
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

// lookPathIn finds name on the PATH of env, the way exec.LookPath does on the
// process's own.
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
	return "", fmt.Errorf("%s: command not found", name)
}

// start starts one command of the pane on the holder's terminal.
func (o PaneOptions) start(cmd []string, cwd string, extra []string) (*exec.Cmd, error) {
	env := paneEnv(os.Environ(), o.Dir, o.Window, append(append([]string(nil), o.Env...), extra...))
	path, argv := paneArgv(cmd, o.Shell)
	full, err := lookPathIn(path, env)
	if err != nil {
		return nil, err
	}
	c := &exec.Cmd{Path: full, Args: argv, Env: env, Dir: cwd, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	// The command gets its own process group, and on a terminal that group
	// is made the foreground one, as a shell does for a job. Two things
	// follow. The terminal's signals (ctrl+c, a resize) go to the command
	// and not the holder. And the pane's foreground process is the command,
	// which is what agent detection reads: with the holder in the
	// foreground, the pane would look like a shell at its prompt. The child
	// sets the foreground itself before it execs, with every signal blocked,
	// so it works even while the holder is not in the foreground.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		c.SysProcAttr.Foreground = true
		c.SysProcAttr.Ctty = 0
		if err := c.Start(); err == nil {
			return c, nil
		}
		// A terminal that is not the holder's controlling one cannot take
		// a foreground group. Run the command in the background group
		// rather than not at all.
		c = &exec.Cmd{Path: full, Args: argv, Env: env, Dir: cwd, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
			SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

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
	// Signals from the terminal are for the command, not for the holder,
	// which can hold the foreground for a moment between two commands. They
	// are caught and dropped rather than ignored: an ignored signal stays
	// ignored in every program the holder starts, and a caught one does not.
	drop := make(chan os.Signal, 4)
	signal.Notify(drop, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		for range drop {
		}
	}()
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM)

	calls := make(chan respawnCall)
	var ln net.Listener
	// The socket is opened only in a directory that is the user's alone: a
	// directory anyone else can write to would let them take its place.
	// Without it the pane still runs, and respawn-pane reports it has no
	// holder.
	if EnsureDir(o.Dir) == nil && EnsureDir(filepath.Join(o.Dir, "p")) == nil {
		// Keep the tmux link pointing at this binary, for a pane opened by
		// `tuios tmux` with no launcher run before it.
		if exe, err := os.Executable(); err == nil {
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			_ = InstallLink(o.Dir, exe)
		}
		sock := paneSocket(o.Dir, o.Window)
		// A socket some live holder answers on is left alone: it is another
		// window whose number collides with this one's, and taking its
		// socket would send its respawns here. This pane then cannot be
		// respawned, which respawn-pane reports.
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
				go acceptRespawns(ln, o.Window, calls)
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
			return code
		case s := <-sigs:
			if sig, ok := s.(syscall.Signal); ok {
				_ = syscall.Kill(-child.Process.Pid, sig)
			}
		case call := <-calls:
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

// stop ends a running command the way closing its pane would: SIGHUP to its
// process group, so a program a shell started goes with the shell, then
// SIGKILL to the group if the command has not gone in two seconds. It
// consumes the exit.
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

// acceptRespawns reads one request per connection and hands it to the holder.
func acceptRespawns(ln net.Listener, window string, calls chan<- respawnCall) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			line, err := bufio.NewReader(io.LimitReader(conn, 1<<20)).ReadBytes('\n')
			if err != nil {
				return
			}
			var req RespawnRequest
			if err := json.Unmarshal(line, &req); err != nil {
				writeReply(conn, errors.New("the request is not JSON"))
				return
			}
			if req.Window != window {
				writeReply(conn, fmt.Errorf("this holder runs window %s, not %s", window, req.Window))
				return
			}
			result := make(chan error, 1)
			calls <- respawnCall{req: req, reply: func(err error) { result <- err }}
			writeReply(conn, <-result)
		}()
	}
}

func writeReply(w io.Writer, err error) {
	r := respawnReply{OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	}
	line, _ := json.Marshal(r)
	_, _ = w.Write(append(line, '\n'))
}
