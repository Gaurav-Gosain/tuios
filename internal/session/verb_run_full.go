//go:build !slim

package session

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

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
		session: sess.Name(),
		sess:    sess,
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
			Command: "tuios wait-for command-finished -s " + sess.Name() + " -w " + w.ID + " --command-seq " + strconv.FormatUint(facts.CommandSeq, 10),
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
			Command: "tuios wait-for command-finished -s " + sess.Name() + " -w " + w.ID + " --command-seq " + strconv.FormatUint(facts.CommandSeq, 10),
			Detail:  "Nothing was typed. Wait for the running command to finish, then run again, or run in another pane.",
		})
	}
	baseline := facts.CommandSeq

	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	pty.shell.expect()
	defer pty.shell.stopExpecting()
	if _, err := submitPrompt(ctx, pty, p.Command, harness.DefaultInputProfile()); err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
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
				Command: "tuios wait-for command-finished -s " + sess.Name() + " -w " + w.ID + " --command-seq " + strconv.FormatUint(baseline, 10),
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
				// A new prompt before the command was marked started: the
				// shell ran the line without a C mark and will never report
				// it finished. Say so now rather than at the timeout.
				if !started && pty.ShellFacts().PromptOnly {
					return nil, promptMarksOnly(windowLabelFor(w), "The command was typed and the shell has likely run it, but its exit status and output are not known.")
				}
			case EventCommandFinished:
				if ev.CommandSeq <= baseline {
					continue
				}
				output, truncated, _ := pty.LastCommandOutput()
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

// stopExpecting ends what expect started, when the run call ends.
func (t *shellTrack) stopExpecting() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expecting = false
}

// defaultRunTimeout is how long run waits for the command when the caller
// names no timeout. It is wait-for's default, so the two read the same.
const defaultRunTimeout = defaultWaitTimeout

// runFirstPromptWait is how long run waits for a pane that has not sent a
// mark yet to show its first prompt. A window opened a moment ago has a shell
// that is still starting, and refusing it would make "open a window, run in
// it" fail for no reason but timing.
var runFirstPromptWait = 3 * time.Second

// promptMarksOnly is the refusal for a pane whose shell marks its prompts and
// not its commands, so a running command looks like a prompt. what says what
// happened to the command line.
func promptMarksOnly(window, what string) *verbError {
	return hintedVerbError(ErrVerbNoShellIntegration, "the shell in window "+echoName(window)+" sends prompt marks only: it ran a command without the OSC 133 C mark, so the daemon cannot tell a running command from a prompt", &VerbHint{
		Command: "tuios doctor shell",
		Detail:  what + " bash needs 4.4 or newer for the C mark (older bash ignores PS0), and a prompt theme that sends only A needs the full integration. Until then, type with send-text and wait for a marker the command prints.",
	})
}

// windowLabelFor is how a refusal names a window: its name when it has one.
func windowLabelFor(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.ID
}
