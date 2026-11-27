package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// runWaitForPid is tuios wait-for pid PID.
func runWaitForPid(arg string, timeout int, jsonOut bool) error {
	pid, err := strconv.Atoi(arg)
	if err != nil || pid <= 0 {
		return errfmt.Errorf("%q is not a process id. Give a number above 0", arg)
	}
	params := map[string]any{"condition": "pid", "pid": pid, "timeout": timeout}
	raw, err := localVerb("wait-for", params, waitGrace(timeout), jsonOut)
	if err != nil {
		return err
	}
	if jsonOut {
		return printVerbResult(raw, true)
	}
	var res struct {
		WasRunning bool `json:"was_running"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if res.WasRunning {
		fmt.Printf("Process %d exited.\n", pid)
	} else {
		fmt.Printf("No process %d runs.\n", pid)
	}
	return nil
}

// runWaitForFile is tuios wait-for file PATH. The daemon may run in another
// folder, so a relative path is made absolute here.
func runWaitForFile(arg, pattern string, timeout int, jsonOut bool) error {
	path := arg
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return errfmt.Errorf("cannot read the current folder for %q: %w", arg, err)
		}
		path = filepath.Join(wd, path)
	}
	params := map[string]any{"condition": "file", "path": path, "timeout": timeout}
	if pattern != "" {
		params["pattern"] = pattern
	}
	raw, err := localVerb("wait-for", params, waitGrace(timeout), jsonOut)
	if err != nil {
		return err
	}
	if jsonOut {
		return printVerbResult(raw, true)
	}
	var res struct {
		Line string `json:"line"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if pattern != "" {
		fmt.Printf("%s matched: %s\n", plainLine(arg), plainLine(res.Line))
	} else {
		fmt.Printf("%s exists.\n", plainLine(arg))
	}
	return nil
}

// waitGrace is how long the CLI waits for an answer to a wait of timeout
// milliseconds.
func waitGrace(timeout int) time.Duration {
	return time.Duration(timeout)*time.Millisecond + 10*time.Second
}
