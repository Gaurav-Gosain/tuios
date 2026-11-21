package session

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const captureLastCommand = "last-command-output"

const waitCommandFinished = "command-finished"

const defaultRunTimeout = defaultWaitTimeout

var runFirstPromptWait = 3 * time.Second

func shellStarting(f ShellFacts) bool {
	return !f.Seen || (!f.AtPrompt && f.Running == "" && !f.PromptOnly && f.CommandSeq == 0)
}

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
		return WindowState{}, nil, mapResolveErr(errfmt.Errorf("PTY for window %q is gone", target), sess)
	}
	return w, pty, nil
}

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

func addPaneMeta(sess *Session, data map[string]any) {
	windows, _ := data["windows"].([]map[string]any)
	for _, w := range windows {
		id, _ := w["pty_id"].(string)
		if pty := sess.GetPTY(id); pty != nil {
			addOnePaneMeta(pty, w)
		}
	}
}

func addOnePaneMeta(pty *PTY, w map[string]any) {
	m := pty.Meta()
	w["history_rows"] = m.HistoryRows
	w["revision"] = m.Revision
	if pid := pty.ShellPID(); pid > 0 {
		w["pid"] = pid
	}
	if tty := pty.TTYName(); tty != "" {
		w["tty"] = tty
	}
}

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

func (d *Daemon) waitCommandFinishedFor(cs *connState, sessionName, window string, commandSeq *uint64, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(cs, sessionName)
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

func noShellIntegration(sess *Session, windowID, window string) *verbError {
	return hintedVerbError(ErrVerbNoShellIntegration, "the shell in window "+echoName(window)+" has not sent OSC 133 marks, so the daemon cannot tell where a command starts and ends", &VerbHint{
		Command: "tuios wait-for window-output " + paneArgs(sess, windowID) + " --pattern <marker>",
		Detail:  "Nothing was typed. Turn on the shell's prompt integration (OSC 133), or type with send-text and wait for a marker the command prints.",
	})
}

func promptMarksOnly(window, what string) *verbError {
	return hintedVerbError(ErrVerbNoShellIntegration, "the shell in window "+echoName(window)+" sends prompt marks only: it ran a command without the OSC 133 C mark, so the daemon cannot tell a running command from a prompt", &VerbHint{
		Command: "tuios doctor shell",
		Detail:  what + " bash needs 4.4 or newer for the C mark (older bash ignores PS0), and a prompt theme that sends only A needs the full integration. Until then, type with send-text and wait for a marker the command prints.",
	})
}

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
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types: map[string]bool{
			EventPrompt: true, EventCommandStarted: true, EventCommandFinished: true,
			EventWindowExit: true, EventWindowClosed: true,
		},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	gone, stopGone := askCancel(cs)
	defer stopGone()

	facts := pty.ShellFacts()
	if shellStarting(facts) {
		first := time.NewTimer(runFirstPromptWait)
		defer first.Stop()
	waitFirst:
		for shellStarting(facts) {
			select {
			case <-first.C:
				facts = pty.ShellFacts()
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
		return nil, noShellIntegration(sess, w.ID, windowLabelFor(w))
	}
	if !pty.runClaim.CompareAndSwap(false, true) {
		return nil, hintedVerbError(ErrVerbNotAtPrompt, "another run is typing or running in window "+echoName(windowLabelFor(w)), &VerbHint{
			Command: "tuios wait-for command-finished " + paneArgs(sess, w.ID) + " --command-seq " + strconv.FormatUint(facts.CommandSeq, 10),
			Detail:  "Nothing was typed. Wait for that command to finish, then run again, or run in another pane.",
		})
	}
	defer pty.runClaim.Store(false)
	if verr := d.recheckTyping(cs, "run", sess, w.ID); verr != nil {
		return nil, verr
	}
	facts = pty.ShellFacts()
	if facts.PromptOnly {
		return nil, promptMarksOnly(windowLabelFor(w), "Nothing was typed.")
	}
	if !facts.AtPrompt {
		msg := "the shell in window " + echoName(windowLabelFor(w)) + " is not at its prompt"
		if facts.Running != "" {
			msg += ": it is running " + strconv.Quote(facts.Running)
		}
		return nil, hintedVerbError(ErrVerbNotAtPrompt, msg, &VerbHint{
			Command: "tuios wait-for command-finished " + paneArgs(sess, w.ID) + " --command-seq " + strconv.FormatUint(facts.CommandSeq, 10),
			Detail:  "Nothing was typed. Wait for the running command to finish, then run again, or run in another pane.",
		})
	}
	baseline := facts.CommandSeq

	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	pty.shell.expect()
	defer pty.shell.stopExpecting()
	ctx = withSubmitGuard(ctx, func() *verbError { return d.recheckTyping(cs, "run", sess, w.ID) })
	if _, err := submitPrompt(ctx, pty, p.Command, harness.DefaultInputProfile()); err != nil {
		return nil, promptWriteError(err)
	}
	LogBasic("run: typed a command in %s of %s", shortID(w.ID), sess.Name())

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
				Command: "tuios wait-for command-finished " + paneArgs(sess, w.ID) + " --command-seq " + strconv.FormatUint(baseline, 10),
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
			case EventPrompt:
				if !started && pty.ShellFacts().PromptOnly {
					return nil, promptMarksOnly(windowLabelFor(w), "The command was typed and the shell has likely run it, but its exit status and output are not known.")
				}
			case EventCommandFinished:
				if ev.CommandSeq <= baseline {
					continue
				}
				output, truncated, _ := pty.LastCommandOutput(false)
				output = sliceCaptureLines(output, 0, 0, p.Lines)
				res := commandFinishedData(sess.Name(), w.ID, ev)
				res["type"] = "command_result"
				res["output"] = output
				res["truncated"] = truncated
				return res, nil
			}
		}
	}
}

func captureLastCommandOutput(sess *Session, pty *PTY, window string, styled, unwrap, trim bool, start, end, lines int) (any, *verbError) {
	if styled {
		return nil, invalidParam("styled", "last-command-output is plain text; drop styled, ansi and resolved")
	}
	facts := pty.ShellFacts()
	output, truncated, ok := pty.LastCommandOutput(unwrap)
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
		windowID := ""
		for _, w := range sess.GetState().Windows {
			if w.PTYID == pty.ID {
				windowID = w.ID
			}
		}
		return nil, noShellIntegration(sess, windowID, window)
	}
	if trim {
		output = trimCapture(output)
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
	if unwrap {
		res["unwrapped"] = true
	}
	if trim {
		res["trimmed"] = true
	}
	return res, nil
}

func windowLabelFor(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.ID
}
