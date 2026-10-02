package session

import (
	"fmt"
	"maps"
	"time"
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
		"at_prompt":      f.AtPrompt,
		"command_seq":    f.CommandSeq,
		"marks_commands": f.MarksCommands,
	}
	if f.PromptOnly {
		out["prompt_marks_only"] = true
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
		maps.Copy(w, shellFactsData(pty.ShellFacts()))
	}
}

// addPaneMeta adds each window's history_rows and revision to a
// list-windows result, the numbers capture-pane reports beside its content.
// A window with no pane on this daemon has neither.
func addPaneMeta(sess *Session, data map[string]any) {
	windows, _ := data["windows"].([]map[string]any)
	for _, w := range windows {
		id, _ := w["pty_id"].(string)
		pty := sess.GetPTY(id)
		if pty == nil {
			continue
		}
		m := pty.Meta()
		w["history_rows"] = m.HistoryRows
		w["revision"] = m.Revision
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
	filter := eventFilter{session: sess.Name(), sess: sess, types: map[string]bool{EventCommandFinished: true}}
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
				return waitMatched(waitCommandFinished, commandFinishedData(sess.Name(), target.ID, ev)), nil
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
				return waitMatched(waitCommandFinished, commandFinishedData(sess.Name(), ev.Window, ev)), nil
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

// promptMarksOnly is the refusal for a pane whose shell marks its prompts and
// not its commands, so a running command looks like a prompt. what says what
// happened to the command line.
func promptMarksOnly(window, what string) *verbError {
	return hintedVerbError(ErrVerbNoShellIntegration, "the shell in window "+echoName(window)+" sends prompt marks only: it ran a command without the OSC 133 C mark, so the daemon cannot tell a running command from a prompt", &VerbHint{
		Command: "tuios doctor shell",
		Detail:  what + " bash needs 4.4 or newer for the C mark (older bash ignores PS0), and a prompt theme that sends only A needs the full integration. Until then, type with send-text and wait for a marker the command prints.",
	})
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
