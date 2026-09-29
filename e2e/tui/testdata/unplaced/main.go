// Command unplaced runs a program in a pane so that the daemon cannot tell it
// runs there, which is what ssh to the same machine does: the program is not
// a descendant of the pane's shell, its terminal is not the pane's, and its
// environment holds none of the pane's TUIOS_ variables. The size of the pane
// still reaches it, as ssh passes a window change on.
//
// unplaced -- argv... starts a relay in a new session and leaves it, so the
// relay is re-parented away from the pane. The relay opens a PTY of its own,
// runs argv in it with the pane's variables removed, copies bytes between the
// pane and the PTY, and copies the pane's size to the PTY when it changes. The
// first process waits for the relay to finish, so the pane's shell does not
// read the pane's input in the meantime.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

const roleEnv = "UNPLACED_ROLE"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: unplaced -- argv...")
		os.Exit(2)
	}
	switch os.Getenv(roleEnv) {
	case "":
		os.Exit(first(args))
	case "middle":
		os.Exit(middle(args))
	default:
		os.Exit(relay(args))
	}
}

// first starts the middle process with the write end of a pipe, and waits for
// the pipe to close, which happens when the relay, the last holder, exits.
func first(args []string) int {
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cmd := exec.Command(os.Args[0], append([]string{"--"}, args...)...)
	cmd.Env = append(os.Environ(), roleEnv+"=middle")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = w.Close()
	_ = cmd.Wait()
	// The relay writes the program's exit status, one byte, before it exits.
	status, _ := io.ReadAll(r)
	if len(status) > 0 {
		return int(status[0])
	}
	return 1
}

// middle starts the relay and exits at once, so the relay loses its parent.
func middle(args []string) int {
	cmd := exec.Command(os.Args[0], append([]string{"--"}, args...)...)
	cmd.Env = append(os.Environ(), roleEnv+"=relay")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{os.NewFile(3, "done")}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// relay runs argv in a PTY of its own between the pane and the program.
func relay(args []string) int {
	cmd := exec.Command(args[0], args[1:]...)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TUIOS_") || strings.HasPrefix(kv, roleEnv+"=") {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	w, h, err := term.GetSize(0)
	if err != nil {
		w, h = 80, 24
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if old, err := term.MakeRaw(0); err == nil {
		defer func() { _ = term.Restore(0, old) }()
	}
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(os.Stdout, ptmx); close(copied) }()
	// The relay has no controlling terminal, so no SIGWINCH reaches it. The
	// pane's size is polled instead.
	go func() {
		for {
			time.Sleep(50 * time.Millisecond)
			if nw, nh, err := term.GetSize(0); err == nil && (nw != w || nh != h) {
				w, h = nw, nh
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
			}
		}
	}()
	_ = cmd.Wait()
	// What the program wrote last, such as a refusal, is still in the PTY.
	select {
	case <-copied:
	case <-time.After(time.Second):
	}
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		code = 1
	}
	_, _ = os.NewFile(3, "done").Write([]byte{byte(code)})
	return code
}
