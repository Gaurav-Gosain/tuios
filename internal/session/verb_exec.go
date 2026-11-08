package session

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// exec runs one shell line on this machine and answers with what it printed,
// on stdout and stderr apart, and how it ended. It is what tuios run --host
// sends over a link: the one command, with no pane to open, wait on, read and
// close. It needs open on a link, the power new-window already has there,
// because a window runs any command too.
//
// The line runs under sh -c (cmd /c on Windows) in a process group of its
// own, with no stdin and the daemon's environment. The group ends at the
// timeout, when the caller goes away and when the daemon stops, so nothing
// it started is left running on this machine. Each stream keeps its last
// execMaxOutput bytes.

const (
	execDefaultTimeout = 30 * time.Second
	execMaxTimeout     = time.Hour
	execMaxLine        = 16 << 10
	execMaxOutput      = 1 << 20
	execMaxRunning     = 8
	// execWaitDelay is how long the pipes may stay open after the group is
	// gone, for a child that left the group and holds them.
	execWaitDelay = 2 * time.Second
)

func execVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"exec": {
			scope:       scopeDeny,
			link:        needOpen,
			description: "Run one shell line on this machine and return its stdout, its stderr and how it ended. No window is opened. The line runs under sh -c (cmd /c on Windows) in a process group of its own, with no stdin and the daemon's environment. The group is stopped at the timeout, when the caller goes away, and when the daemon stops. Each stream keeps its last 1 MiB. At most 8 run at once. Over a link it needs open, as new-window does.",
			params: []verbParam{
				{Name: "line", Type: "string", Required: true, Description: "The command line, parsed by the shell, at most 16384 bytes."},
				{Name: "cwd", Type: "string", Description: "Directory to run in. It must exist. Omit for the daemon's home directory."},
				{Name: "timeout_ms", Type: "int", Description: "How long the line may run, in milliseconds, at most one hour. Omit for 30000."},
			},
			returns: []verbParam{
				{Name: "stdout", Type: "bytes", Description: "What the line wrote to stdout, base64 in JSON."},
				{Name: "stderr", Type: "bytes", Description: "What the line wrote to stderr, base64 in JSON."},
				{Name: "stdout_dropped", Type: "int", Description: "Bytes cut from the start of stdout to keep the last 1 MiB."},
				{Name: "stderr_dropped", Type: "int", Description: "Bytes cut from the start of stderr to keep the last 1 MiB."},
				{Name: "exit", Type: "int", Description: "The exit status. Absent when the line was stopped by a signal, the timeout or the caller leaving."},
				{Name: "signal", Type: "string", Description: "The signal that stopped the line, such as killed. Absent when it exited."},
				{Name: "signal_number", Type: "int", Description: "The number of that signal, such as 9."},
				{Name: "timed_out", Type: "bool", Description: "The timeout stopped the line."},
				{Name: "duration_ms", Type: "int", Description: "How long the line ran."},
			},
			examples: []string{
				`{"id":1,"verb":"exec","params":{"line":"uname -a"}}`,
				`{"id":1,"verb":"exec","params":{"line":"ls -la","cwd":"/tmp","timeout_ms":60000}}`,
			},
			handler: (*Daemon).verbExec,
		},
	}
}

// execResult is the answer of exec.
type execResult struct {
	Stdout        []byte `json:"stdout"`
	Stderr        []byte `json:"stderr"`
	StdoutDropped int64  `json:"stdout_dropped,omitempty"`
	StderrDropped int64  `json:"stderr_dropped,omitempty"`
	Exit          *int   `json:"exit,omitempty"`
	Signal        string `json:"signal,omitempty"`
	SignalNumber  int    `json:"signal_number,omitempty"`
	TimedOut      bool   `json:"timed_out,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
}

func (d *Daemon) verbExec(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Line      string `json:"line"`
		Cwd       string `json:"cwd"`
		TimeoutMS int64  `json:"timeout_ms"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Line == "" {
		return nil, invalidParam("line", "line is required: the command line to run")
	}
	if len(p.Line) > execMaxLine {
		return nil, invalidParam("line", "line is longer than "+strconv.Itoa(execMaxLine)+" bytes. Put a long script in a file and run the file")
	}
	timeout := execDefaultTimeout
	if p.TimeoutMS < 0 {
		return nil, invalidParam("timeout_ms", "timeout_ms is milliseconds and cannot be negative")
	}
	if p.TimeoutMS > 0 {
		timeout = min(time.Duration(p.TimeoutMS)*time.Millisecond, execMaxTimeout)
	}
	dir := p.Cwd
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if dir != "" {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return nil, invalidParam("cwd", "cwd is not a directory on this machine: "+echoName(p.Cwd))
		}
	}
	if d.execRunning.Add(1) > execMaxRunning {
		d.execRunning.Add(-1)
		return nil, newVerbError(ErrVerbBusy, strconv.Itoa(execMaxRunning)+" commands run on this machine now, the most at once. Try again when one ends")
	}
	defer d.execRunning.Add(-1)

	// The run ends with the daemon, and with the caller: a CLI that was
	// stopped or a link that dropped leaves nothing running here.
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	gone, stopGone := askCancel(cs)
	defer stopGone()
	go func() {
		select {
		case <-gone:
			cancel()
		case <-ctx.Done():
		}
	}()
	res := runExecLine(ctx, p.Line, dir, timeout)

	who := "this machine"
	if cs.viaLink {
		who = "machine " + echoName(cs.linkPeer)
	}
	end := "exit " + strconv.Itoa(intOr(res.Exit, -1))
	if res.Signal != "" {
		end = "signal " + res.Signal
	}
	log.Printf("exec from %s in %s: %q, %s after %dms", who, dir, p.Line, end, res.DurationMS)
	return res, nil
}

// runExecLine runs line in dir until it ends, ctx ends or timeout passes.
func runExecLine(ctx context.Context, line, dir string, timeout time.Duration) execResult {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = childproc.CommandContext(rctx, "cmd.exe", "/c", line)
	} else {
		cmd = childproc.CommandContext(rctx, "/bin/sh", "-c", line)
	}
	cmd.Dir = dir
	childproc.OwnGroup(cmd)
	cmd.WaitDelay = execWaitDelay
	stdout, stderr := &execTail{max: execMaxOutput}, &execTail{max: execMaxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err := cmd.Run()
	res := execResult{
		Stdout: stdout.bytes(), Stderr: stderr.bytes(),
		StdoutDropped: stdout.dropped, StderrDropped: stderr.dropped,
		DurationMS: time.Since(start).Milliseconds(),
	}
	var exitErr *exec.ExitError
	switch {
	case errors.Is(rctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.TimedOut, res.Signal, res.SignalNumber = true, "killed", 9
	case ctx.Err() != nil:
		res.Signal, res.SignalNumber = "killed", 9
	case err == nil:
		code := 0
		res.Exit = &code
	case errors.As(err, &exitErr):
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal, res.SignalNumber = ws.Signal().String(), int(ws.Signal())
			break
		}
		code := exitErr.ExitCode()
		res.Exit = &code
	default:
		// The shell itself did not start.
		res.Stderr = append(res.Stderr, []byte("tuios could not start the shell: "+err.Error()+"\n")...)
		code := 127
		res.Exit = &code
	}
	return res
}

// execTail keeps the last max bytes written to it and counts the rest.
type execTail struct {
	buf     []byte
	max     int
	dropped int64
}

func (c *execTail) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	if n := len(c.buf); n > 2*c.max {
		c.dropped += int64(n - c.max)
		c.buf = append(c.buf[:0], c.buf[n-c.max:]...)
	}
	return len(p), nil
}

func (c *execTail) bytes() []byte {
	if n := len(c.buf); n > c.max {
		c.dropped += int64(n - c.max)
		c.buf = c.buf[n-c.max:]
	}
	if c.buf == nil {
		return []byte{}
	}
	return c.buf
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
