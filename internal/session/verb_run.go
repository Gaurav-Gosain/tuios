package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// The verbs that read a shell's commands: run, wait-for command-finished and
// capture-pane with source last-command-output. They rest on the facts
// shell_commands.go keeps from the shell's OSC 133 marks.
//
// run is send-text, a wait and capture-pane in one call, and it grants nothing
// those three do not: any caller that may type into a pane and read it back
// may already do everything run does. What it adds is care. It types only at a
// prompt, so a command never lands in a running program, and it reads the exit
// status and the output from the shell's own marks rather than from a marker
// the caller has to make up and match.

// captureLastCommand is the capture-pane source for the last finished
// command's output.
const captureLastCommand = "last-command-output"

// waitCommandFinished is the wait-for condition for a command finishing.
const waitCommandFinished = "command-finished"

// defaultRunTimeout is how long run waits for the command when the caller
// names no timeout. It is wait-for's default, so the two read the same.
const defaultRunTimeout = defaultWaitTimeout

// runFirstPromptWait is how long run waits for a pane that has not sent a
// mark yet to show its first prompt. A window opened a moment ago has a shell
// that is still starting, and refusing it would make "open a window, run in
// it" fail for no reason but timing.
var runFirstPromptWait = 3 * time.Second

// resolveWindowPTY resolves a window target, the focused window when empty,
// to the window and its PTY.
func (d *Daemon) resolveWindowPTY(sess *Session, target string) (WindowState, *PTY, *verbError) {
	state := sess.GetState()
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return WindowState{}, nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return WindowState{}, nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	pty := sess.GetPTY(w.PTYID)
	if pty == nil {
		return WindowState{}, nil, mapResolveErr(fmt.Errorf("PTY for window %q is gone", target), sess)
	}
	return w, pty, nil
}

// shellFactsData is a pane's shell facts as list-windows reports them. It is
// empty for a pane whose shell never sent a mark, so such a window's entry
// keeps exactly the shape it had before.
func shellFactsData(f ShellFacts) map[string]any {
	if !f.Seen {
		return nil
	}
	out := map[string]any{
		"at_prompt":   f.AtPrompt,
		"command_seq": f.CommandSeq,
	}
	if f.Running != "" {
		out["running_cmdline"] = f.Running
	}
	if f.CommandSeq > 0 {
		out["last_cmdline"] = f.LastCmdline
		out["last_duration_ms"] = f.LastDuration.Milliseconds()
		if f.LastExit != nil {
			out["last_exit_code"] = *f.LastExit
		}
	}
	return out
}

// addShellFacts adds each window's shell facts to a list-windows result.
func addShellFacts(sess *Session, data map[string]any) {
	windows, _ := data["windows"].([]map[string]any)
	for _, w := range windows {
		id, _ := w["pty_id"].(string)
		pty := sess.GetPTY(id)
		if pty == nil {
			continue
		}
		for k, v := range shellFactsData(pty.ShellFacts()) {
			w[k] = v
		}
	}
}

// commandFinishedData is a command-finished event as a result's fields.
func commandFinishedData(sessionName, window string, ev streamEvent) map[string]any {
	out := map[string]any{
		"session":     sessionName,
		"window":      window,
		"cmdline":     ev.Cmdline,
		"duration_ms": ev.DurationMS,
		"command_seq": ev.CommandSeq,
	}
	if ev.ExitCode != nil {
		out["exit_code"] = *ev.ExitCode
	}
	return out
}

// waitCommandFinishedFor resolves when a command finishes: in the named
// window, or with no window in any pane of the session. With commandSeq set it
// resolves once the window has finished more than that many commands, which
// is already true when the command finished before the wait began, so a
// caller that read command_seq before starting the command cannot miss it.
// Without it, the next command to finish after the wait starts matches.
func (d *Daemon) waitCommandFinishedFor(sessionName, window string, commandSeq *uint64, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	if window == "" && commandSeq != nil {
		return nil, invalidParam("command_seq", "command_seq counts one pane's commands, so it needs a window")
	}
	filter := eventFilter{session: sess.Name, types: map[string]bool{EventCommandFinished: true}}
	var (
		target WindowState
		pty    *PTY
	)
	if window != "" {
		target, pty, verr = d.resolveWindowPTY(sess, window)
		if verr != nil {
			return nil, verr
		}
		filter.ptyID = pty.ID
		filter.types[EventWindowExit] = true
		filter.types[EventWindowClosed] = true
	}
	sub := d.events.subscribe(filter, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	var baseline uint64
	if pty != nil {
		facts := pty.ShellFacts()
		baseline = facts.CommandSeq
		if commandSeq != nil {
			baseline = *commandSeq
			if facts.CommandSeq > baseline {
				// Finished before the wait began. The facts hold only the
				// newest command, which is the one to report.
				ev := streamEvent{Cmdline: facts.LastCmdline, DurationMS: facts.LastDuration.Milliseconds(), CommandSeq: facts.CommandSeq, ExitCode: facts.LastExit}
				return waitMatched(waitCommandFinished, commandFinishedData(sess.Name, target.ID, ev)), nil
			}
		}
	}
	for {
		select {
		case <-deadline:
			hint := &VerbHint{
				Param:  "timeout",
				Detail: "No command finished before the timeout. A pane whose shell does not send OSC 133 marks never reports one; list-windows shows at_prompt for a pane whose shell does.",
			}
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for a command to finish", hint)
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			switch ev.Type {
			case EventWindowExit, EventWindowClosed:
				return nil, newVerbError(ErrVerbPTYNotFound, "the pane's shell exited before a command finished")
			case EventCommandFinished:
				if pty != nil && ev.CommandSeq <= baseline {
					continue
				}
				return waitMatched(waitCommandFinished, commandFinishedData(sess.Name, ev.Window, ev)), nil
			}
		}
	}
}

// noShellIntegration is the refusal for a pane whose shell never marked a
// command.
func noShellIntegration(window string) *verbError {
	return hintedVerbError(ErrVerbNoShellIntegration, "the shell in window "+echoName(window)+" has not sent OSC 133 marks, so the daemon cannot tell where a command starts and ends", &VerbHint{
		Command: "tuios wait-for window-output -w " + window + " --pattern <marker>",
		Detail:  "Nothing was typed. Turn on the shell's prompt integration (OSC 133), or type with send-text and wait for a marker the command prints.",
	})
}

// verbRun types one command line at a pane's prompt, waits for the shell to
// report it finished, and returns its exit status and output.
func (d *Daemon) verbRun(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
		Lines   int    `json:"lines"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Command == "" {
		return nil, invalidParam("command", `command is required: the command line to run, e.g. "go test ./..."`)
	}
	for _, r := range p.Command {
		if unicode.IsControl(r) {
			return nil, invalidParam("command", "command is one line with no control characters. Join several commands with ; or &&, or put them in a script")
		}
	}
	if p.Timeout < 0 || p.Lines < 0 {
		return nil, invalidParam("timeout", "timeout and lines cannot be negative")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	w, pty, verr := d.resolveWindowPTY(sess, p.Window)
	if verr != nil {
		return nil, verr
	}
	timeout := defaultRunTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Millisecond
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types: map[string]bool{
			EventPrompt: true, EventCommandStarted: true, EventCommandFinished: true,
			EventWindowExit: true, EventWindowClosed: true,
		},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	// A call from a pane on another machine ends with the report channel it
	// came on, the rule wait-for follows.
	var gone <-chan struct{}
	if cs != nil && cs.hostedEnded != nil {
		gone = cs.hostedEnded
	}

	// A shell that has sent nothing yet may be one still starting. Give it
	// a moment to draw its first prompt before deciding it has no marks.
	facts := pty.ShellFacts()
	if !facts.Seen {
		first := time.NewTimer(runFirstPromptWait)
		defer first.Stop()
	waitFirst:
		for !facts.Seen {
			select {
			case <-first.C:
				break waitFirst
			case <-gone:
				return nil, newVerbError(ErrVerbInternal, "the caller went away")
			case <-d.ctx.Done():
				return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
			case ev := <-sub.ch:
				if ev.Type == EventWindowExit || ev.Type == EventWindowClosed {
					return nil, newVerbError(ErrVerbPTYNotFound, "the pane's shell exited before it showed a prompt")
				}
			}
			facts = pty.ShellFacts()
		}
	}
	if !facts.Seen {
		return nil, noShellIntegration(windowLabelFor(w))
	}
	// One run at a time in a pane. Without the claim two calls both pass the
	// prompt check, both paste, and the shell reads one line made of both
	// commands. The claim is held until this call ends.
	if !pty.runClaim.CompareAndSwap(false, true) {
		return nil, hintedVerbError(ErrVerbNotAtPrompt, "another run is typing or running in window "+echoName(windowLabelFor(w)), &VerbHint{
			Command: "tuios wait-for command-finished -s " + sess.Name + " -w " + w.ID + " --command-seq " + strconv.FormatUint(facts.CommandSeq, 10),
			Detail:  "Nothing was typed. Wait for that command to finish, then run again, or run in another pane.",
		})
	}
	defer pty.runClaim.Store(false)
	facts = pty.ShellFacts()
	if !facts.AtPrompt {
		msg := "the shell in window " + echoName(windowLabelFor(w)) + " is not at its prompt"
		if facts.Running != "" {
			msg += ": it is running " + strconv.Quote(facts.Running)
		}
		return nil, hintedVerbError(ErrVerbNotAtPrompt, msg, &VerbHint{
			Command: "tuios wait-for command-finished -s " + sess.Name + " -w " + w.ID + " --command-seq " + strconv.FormatUint(facts.CommandSeq, 10),
			Detail:  "Nothing was typed. Wait for the running command to finish, then run again, or run in another pane.",
		})
	}
	baseline := facts.CommandSeq

	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	if _, err := submitPrompt(ctx, pty, p.Command, harness.DefaultInputProfile()); err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	LogBasic("run: typed a command in %s of %s", shortID(w.ID), sess.Name)

	started := false
	for {
		select {
		case <-ctx.Done():
			if d.ctx.Err() != nil {
				return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
			}
			detail := "The command was typed and is still running; nothing was stopped. Wait for it with the command shown, then read its output with capture-pane --source last-command-output."
			if !started {
				detail = "The command was typed and the shell has not reported it running. Look at the pane with capture-pane: the line may sit at the prompt, or the shell may have stopped sending OSC 133 marks."
			}
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for the command to finish", &VerbHint{
				Command: "tuios wait-for command-finished -s " + sess.Name + " -w " + w.ID + " --command-seq " + strconv.FormatUint(baseline, 10),
				Detail:  detail,
			})
		case <-gone:
			return nil, newVerbError(ErrVerbInternal, "the caller went away; the command keeps running")
		case ev := <-sub.ch:
			switch ev.Type {
			case EventWindowExit, EventWindowClosed:
				return nil, newVerbError(ErrVerbPTYNotFound, "the pane's shell exited before the command finished")
			case EventCommandStarted:
				started = true
			case EventCommandFinished:
				if ev.CommandSeq <= baseline {
					continue
				}
				output, truncated, _ := pty.LastCommandOutput()
				output = sliceCaptureLines(output, 0, 0, p.Lines)
				res := commandFinishedData(sess.Name, w.ID, ev)
				res["type"] = "command_result"
				res["output"] = output
				res["truncated"] = truncated
				return res, nil
			}
		}
	}
}

// captureLastCommandOutput is capture-pane with source last-command-output:
// the plain text the pane's last finished command printed, with the facts the
// shell reported about it. It is plain only, because the rows are read out
// of the emulator cell by cell and carry no styling.
func captureLastCommandOutput(pty *PTY, window string, styled bool, start, end, lines int) (any, *verbError) {
	if styled {
		return nil, invalidParam("styled", "last-command-output is plain text; drop styled, ansi and resolved")
	}
	facts := pty.ShellFacts()
	output, truncated, ok := pty.LastCommandOutput()
	if !facts.Seen || !ok {
		if window == "" {
			window = "focused"
		}
		if facts.Seen {
			return nil, hintedVerbError(ErrVerbNoShellIntegration, "no command has finished in window "+echoName(window)+" since its shell started sending OSC 133 marks", &VerbHint{
				Verb:   "run",
				Detail: "Run a command in the pane first, or read the screen with source recent.",
			})
		}
		return nil, noShellIntegration(window)
	}
	res := map[string]any{
		"type":        "pane_content",
		"content":     sliceCaptureLines(output, start, end, lines),
		"source":      captureLastCommand,
		"styled":      false,
		"resolved":    false,
		"truncated":   truncated,
		"cmdline":     facts.LastCmdline,
		"command_seq": facts.CommandSeq,
	}
	if facts.LastExit != nil {
		res["exit_code"] = *facts.LastExit
	}
	return res, nil
}

// windowLabelFor is how a refusal names a window: its name when it has one.
func windowLabelFor(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.ID
}
