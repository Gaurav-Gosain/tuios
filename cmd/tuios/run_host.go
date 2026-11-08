package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// execResult is the exec verb's answer as the CLI reads it.
type execResult struct {
	Stdout        []byte `json:"stdout"`
	Stderr        []byte `json:"stderr"`
	StdoutDropped int64  `json:"stdout_dropped"`
	StderrDropped int64  `json:"stderr_dropped"`
	Exit          *int   `json:"exit"`
	Signal        string `json:"signal"`
	SignalNumber  int    `json:"signal_number"`
	TimedOut      bool   `json:"timed_out"`
	DurationMS    int64  `json:"duration_ms"`
}

// runTimedOutStatus is the exit status of a command the timeout stopped, the
// one timeout(1) uses.
const runTimedOutStatus = 124

// runLineOnHost is `tuios run --host`: one command line on a linked machine,
// with no pane. It prints the command's stdout and stderr on its own and
// exits with the command's status.
func runLineOnHost(host, cwd string, args []string, timeout, lines int, jsonOutput bool) error {
	line := strings.Join(args, " ")
	if strings.TrimSpace(line) == "" {
		return errors.New("run needs a command line, e.g. tuios run --host build -- uname -a")
	}
	t, err := dialHost(host)
	if err != nil {
		return err
	}
	defer t.Close()
	params := map[string]any{"line": line}
	if cwd != "" {
		params["cwd"] = cwd
	}
	wait := 30 * time.Second
	if timeout > 0 {
		params["timeout_ms"] = timeout
		wait = time.Duration(timeout) * time.Millisecond
	}
	raw, err := t.client.CallWithTimeout("exec", params, wait+15*time.Second)
	if err != nil {
		return reportVerbError(explainHostVerbError(host, "run --host", err), jsonOutput)
	}
	var res execResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if lines > 0 {
		res.Stdout = lastLines(res.Stdout, lines)
	}
	if jsonOutput {
		outputJSON(map[string]any{
			"host": host, "stdout": string(res.Stdout), "stderr": string(res.Stderr),
			"stdout_dropped": res.StdoutDropped, "stderr_dropped": res.StderrDropped,
			"exit": res.Exit, "signal": res.Signal, "timed_out": res.TimedOut,
			"duration_ms": res.DurationMS,
		})
		return hostRunStatus(res, timeout, io.Discard)
	}
	_, _ = os.Stdout.Write(res.Stdout)
	_, _ = os.Stderr.Write(res.Stderr)
	if res.StdoutDropped > 0 || res.StderrDropped > 0 {
		fmt.Fprintf(os.Stderr, "tuios: the output was longer than 1 MiB, so its start was left out (%d bytes of stdout, %d of stderr)\n",
			res.StdoutDropped, res.StderrDropped)
	}
	return hostRunStatus(res, timeout, os.Stderr)
}

// hostRunStatus is the exit status run --host ends with, and the note on
// errs that says why when the command did not exit by itself.
func hostRunStatus(res execResult, timeout int, errs io.Writer) error {
	switch {
	case res.TimedOut:
		if timeout <= 0 {
			timeout = 30000
		}
		fmt.Fprintf(errs, "tuios: the command ran longer than %dms and was stopped. Use --timeout for a longer limit\n", timeout)
		return &statusError{code: runTimedOutStatus}
	case res.Signal != "":
		fmt.Fprintf(errs, "tuios: the command was stopped by signal %s\n", res.Signal)
		return &statusError{code: 128 + res.SignalNumber}
	case res.Exit == nil:
		fmt.Fprintln(errs, "tuios: the machine reported no exit status for this command")
		return &statusError{code: 1}
	case *res.Exit != 0:
		return &statusError{code: *res.Exit}
	}
	return nil
}

// lastLines keeps the last n lines of b.
func lastLines(b []byte, n int) []byte {
	trimmed := bytes.TrimSuffix(b, []byte("\n"))
	idx := len(trimmed)
	for range n {
		i := bytes.LastIndexByte(trimmed[:idx], '\n')
		if i < 0 {
			return b
		}
		idx = i
	}
	return b[idx+1:]
}
