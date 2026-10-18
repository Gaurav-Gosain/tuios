package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The stop and the start of a restart run in a process of their own.
//
// tuios restart-server is often run from a pane of the daemon it restarts:
// by a person, or by an agent. The stop closes that pane's terminal, and the
// kernel sends SIGHUP to everything on it, the command included. A command
// that did the steps itself died there, between the stop and the start, and
// left no daemon at all.
//
// So the command hands the steps to a worker: the same binary, in a session
// of its own (Setsid), with SIGHUP ignored and its output in the restart log
// rather than on the terminal. The command reads the log and shows the steps
// as they come. If the command dies, the worker goes on, and the log keeps
// what happened for `tuios restart-server --last`.
//
// The worker takes the restart lock, so a second restart that starts while
// one runs refuses rather than stopping the daemon the first one is starting.

// runDetachedRestart starts the worker for plan, shows each step through step,
// and returns the result the worker wrote.
func runDetachedRestart(plan *restartReport, step func(string)) *restartReport {
	fail := func(what, fix string) *restartReport {
		return &restartReport{Step: restartplan.StepDone, Binary: plan.Binary, Version: plan.Version, Error: what, Fix: fix}
	}
	// A restart that runs owns the log. Checked before the log is cut, so a
	// second restart cannot wipe the first one's steps.
	lock, err := session.LockRestart()
	if errors.Is(err, session.ErrRestartRunning) {
		return fail("Another restart of the daemon is running.", "wait for it to end. 'tuios restart-server --last' shows its steps.")
	}
	if lock != nil {
		_ = lock.Close()
	}

	logPath := session.RestartLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fail("The restart log could not be written: "+err.Error()+".", "check that "+filepath.Dir(logPath)+" can be written.")
	}
	writeRestartJSON(logFile, plan)
	data, _ := json.Marshal(plan)
	cmd := exec.Command(workerExecutable(), "restart-server", "--worker", string(data))
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = daemonSysProcAttr()
	err = cmd.Start()
	_ = logFile.Close()
	if err != nil {
		return fail("The restart could not start: "+err.Error()+".", "run 'tuios kill-server', then 'tuios start-server'.")
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	return followRestartLog(logPath, exited, step, fail)
}

// workerExecutable is the binary the worker runs: this one. The worker is
// this build's own code, so it is this file even after an update replaced
// the path it started from.
func workerExecutable() string {
	if runtime.GOOS == "linux" {
		return "/proc/self/exe"
	}
	exe, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return exe
}

// followRestartLog reads the worker's lines as they come, until the result.
func followRestartLog(path string, exited <-chan struct{}, step func(string), fail func(what, fix string) *restartReport) *restartReport {
	f, err := os.Open(path)
	if err != nil {
		return fail("The restart log could not be read: "+err.Error()+".", "run 'tuios restart-server --last' to see the steps.")
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	var partial string
	gone := false
	for {
		line, err := r.ReadString('\n')
		partial += line
		if err == nil {
			var rep restartReport
			if json.Unmarshal([]byte(partial), &rep) == nil {
				switch rep.Step {
				case restartplan.StepStopping, restartplan.StepStarting:
					step(rep.Step)
				case restartplan.StepDone:
					return &rep
				}
			}
			partial = ""
			continue
		}
		if gone {
			return fail("The restart stopped before it finished.", "run 'tuios restart-server --last' to see its steps, and 'tuios ls' to check the daemon.")
		}
		select {
		case <-exited:
			// One more pass reads what the worker wrote before it exited.
			gone = true
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// runRestartWorker is the detached worker: it takes the lock and runs the
// steps of the plan, writing each one to its output, the restart log.
func runRestartWorker(planJSON string) error {
	signal.Ignore(syscall.SIGHUP)
	var plan restartReport
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return err
	}
	out := os.Stdout
	lock, err := session.LockRestart()
	if err != nil {
		writeRestartJSON(out, &restartReport{Step: restartplan.StepDone, Error: "Another restart of the daemon is running.",
			Fix: "wait for it to end. 'tuios restart-server --last' shows its steps."})
		return nil
	}
	defer func() { _ = lock.Close() }()
	done := runRestart(&plan, func(step string) {
		writeRestartJSON(out, &restartReport{Step: step})
	})
	writeRestartJSON(out, done)
	return nil
}

// showLastRestart writes the restart log as the command would have.
func showLastRestart(w io.Writer) error {
	path := session.RestartLogPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(w, "No restart has run yet.")
		return nil
	}
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		fmt.Fprintf(w, "Last restart: %s\n\n", fi.ModTime().Format("2006-01-02 15:04:05"))
	}
	var plan *restartReport
	for line := range strings.SplitSeq(string(data), "\n") {
		var rep restartReport
		if json.Unmarshal([]byte(line), &rep) != nil {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Fprintln(w, line)
			}
			continue
		}
		switch rep.Step {
		case restartplan.StepPlan:
			plan = &rep
			fmt.Fprint(w, restartplan.Text(restartplan.Lines(&rep, "")))
		case restartplan.StepStopping:
			fmt.Fprintln(w, "Stopping the daemon...")
		case restartplan.StepStarting:
			v := ""
			if plan != nil {
				v = plan.Version + " "
			}
			fmt.Fprintf(w, "Starting the daemon %s...\n", strings.TrimSpace(v))
		case restartplan.StepDone:
			if rep.Error != "" {
				fmt.Fprintln(w, rep.Error)
				if rep.Fix != "" {
					fmt.Fprintln(w, "Fix: "+rep.Fix)
				}
				continue
			}
			writeRestartDone(w, &rep)
		}
	}
	return nil
}
