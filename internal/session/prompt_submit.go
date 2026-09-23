package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// This file is the one way the daemon types a prompt into an agent's pane and
// submits it. ask-agent and the fan prompt both go through it, so the two
// cannot disagree about what a submitted prompt looks like.
//
// Both used to write the text and a line feed. That is wrong in two ways for
// the TUIs agents run in. Claude Code and Codex submit on carriage return, which
// is what the Enter key sends, and several agent TUIs bind a line feed (Ctrl+J)
// to "insert a newline", so a prompt could sit in the input box unsent. And a
// multi-line prompt typed as raw lines is a sequence of Enters, so an agent that
// does submit on a line feed sent the first line alone.
//
// So the prompt is pasted and then submitted, the way a person pastes and
// presses Enter:
//
//  1. The text, with its trailing line breaks dropped. When the pane has
//     bracketed paste on (DECSET 2004), it is wrapped in ESC[200~ and ESC[201~,
//     so the application reads it as one paste whatever lines it holds.
//  2. A short wait, so the application has taken the paste in before the key
//     that submits it arrives. TUIs that detect a paste by timing (Codex does)
//     turn an Enter that arrives inside the burst into a newline.
//  3. The submit key: a carriage return, unless the harness's manifest says
//     it submits on a line feed.
//
// The harness's [input] block can change three things, and only these: the
// submit key, whether a bracketed paste is allowed at all (a harness that
// turns DECSET 2004 on and mishandles a paste can refuse it), and whether a
// focus-in report (CSI I) goes first, for a harness that ignores a submit while
// it believes it is unfocused. The focus report is only sent when the pane has
// focus reporting (DECSET 1004) on, so an application that never asked for
// focus events never sees the bytes. See harness.Input.

// Bracketed paste delimiters, DECSET 2004.
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// focusInReport is what a terminal sends when its window gains focus and the
// application has DECSET 1004 on.
const focusInReport = "\x1b[I"

// modeFocusEvents is DECSET 1004, focus event reporting.
const modeFocusEvents = 1004

// promptSubmitMaxWait bounds the wait between the paste and the carriage
// return. herdr waits this long unconditionally; see promptSubmitQuiet for when
// the wait ends sooner.
const promptSubmitMaxWait = 300 * time.Millisecond

// promptSubmitQuiet is how long the pane must stay silent, after printing
// something in answer to the paste, for the paste to count as taken in. An
// application that echoes the paste and then goes quiet has drawn it, and the
// full wait would only slow the ask down. One that prints nothing gets the whole
// promptSubmitMaxWait, since silence then says nothing about whether it has
// read the paste yet.
const promptSubmitQuiet = 80 * time.Millisecond

// promptSubmitPoll is how often the wait looks at the pane's output clock.
const promptSubmitPoll = 10 * time.Millisecond

// promptPane is what submitPrompt needs from a pane. *PTY implements it, and a
// test implements it to record the bytes.
type promptPane interface {
	Write([]byte) (int, error)
	// BracketedPasteOn reports whether the application in the pane has
	// bracketed paste mode on.
	BracketedPasteOn() bool
	// LastOutput is the unix-nano time the pane last printed anything.
	LastOutput() int64
	// FocusReportingOn reports whether the application in the pane has focus
	// event reporting (DECSET 1004) on.
	FocusReportingOn() bool
}

// BracketedPasteOn reports whether the application in the pane has turned on
// bracketed paste mode (DECSET 2004), as the daemon's emulator last read it.
func (p *PTY) BracketedPasteOn() bool {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.terminal != nil && p.terminal.BracketedPasteEnabled()
}

// FocusReportingOn reports whether the application in the pane has turned on
// focus event reporting (DECSET 1004), as the daemon's emulator last read it.
func (p *PTY) FocusReportingOn() bool {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.terminal != nil && p.terminal.GetModes()[modeFocusEvents]
}

// inputProfileFor is the input profile of the harness running in a window as
// the session last recorded it, or the default when there is none: a pane no
// harness has claimed still gets a prompt pasted and submitted with a carriage
// return, as it always did.
func (d *Daemon) inputProfileFor(sess *Session, windowID string) harness.InputProfile {
	reg := d.agentMatcher.registry
	if reg == nil || sess == nil {
		return harness.DefaultInputProfile()
	}
	sess.stateMu.RLock()
	hid := ""
	for i := range sess.state.Windows {
		if sess.state.Windows[i].ID == windowID {
			hid = sess.state.Windows[i].AgentHarness
			break
		}
	}
	sess.stateMu.RUnlock()
	return reg.InputProfile(hid)
}

// submitPrompt types text into the pane as one paste and submits it with the
// harness's submit key. It returns once the submit key is written, or with the
// first write error, or with ctx's error if ctx ends during the wait, in which
// case the paste was written and the submit key was not.
//
// The time it returns is taken just before the submit key is written. It is
// the line the stall gate draws: output from before it is the application
// drawing the paste, and output after it is the application acting on Enter.
// See prompt_gate.go.
func submitPrompt(ctx context.Context, pane promptPane, text string, in harness.InputProfile) (time.Time, error) {
	return submitPromptStamped(ctx, pane, text, in, promptSubmitQuiet, promptSubmitMaxWait)
}

// submitPromptTimed is submitPrompt with the waits as parameters.
func submitPromptTimed(ctx context.Context, pane promptPane, text string, in harness.InputProfile, quiet, maxWait time.Duration) error {
	_, err := submitPromptStamped(ctx, pane, text, in, quiet, maxWait)
	return err
}

// submitPromptStamped is submitPromptTimed that also returns when the submit
// key was sent.
func submitPromptStamped(ctx context.Context, pane promptPane, text string, in harness.InputProfile, quiet, maxWait time.Duration) (time.Time, error) {
	if in.SubmitKey == "" {
		in.SubmitKey = "\r"
	}
	if in.FocusBeforeSubmit && pane.FocusReportingOn() {
		if _, err := pane.Write([]byte(focusInReport)); err != nil {
			return time.Time{}, fmt.Errorf("failed to report focus: %w", err)
		}
	}
	body := promptBody(text)
	if in.BracketedPaste && pane.BracketedPasteOn() {
		body = bracketedPasteStart + body + bracketedPasteEnd
	}
	// Taken before the write, so output the paste caused counts however soon
	// it arrives.
	pastedAt := time.Now()
	if body != "" {
		if _, err := pane.Write([]byte(body)); err != nil {
			return time.Time{}, fmt.Errorf("failed to write the prompt: %w", err)
		}
	}
	if err := waitPasteTaken(ctx, pane, pastedAt, quiet, maxWait); err != nil {
		return time.Time{}, err
	}
	submittedAt := time.Now()
	if _, err := pane.Write([]byte(in.SubmitKey)); err != nil {
		return time.Time{}, fmt.Errorf("failed to submit the prompt: %w", err)
	}
	return submittedAt, nil
}

// promptBody is the text as it is pasted. Line endings are made line feeds,
// since a carriage return inside the text is an Enter to an application
// without bracketed paste. Trailing line breaks are dropped, because the
// carriage return that follows is the Enter, and one more would submit an empty
// line after it. The bracketed paste delimiters are removed from the text, so
// the text cannot end the paste early and have the rest read as keystrokes.
func promptBody(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, bracketedPasteEnd, "")
	text = strings.ReplaceAll(text, bracketedPasteStart, "")
	return strings.TrimRight(text, "\n")
}

// waitPasteTaken waits until the pane has printed something since pastedAt and
// then been silent for quiet, or until maxWait has passed since pastedAt,
// whichever is first.
func waitPasteTaken(ctx context.Context, pane promptPane, pastedAt time.Time, quiet, maxWait time.Duration) error {
	deadline := pastedAt.Add(maxWait)
	ticker := time.NewTicker(promptSubmitPoll)
	defer ticker.Stop()
	for {
		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}
		if last := pane.LastOutput(); last > pastedAt.UnixNano() && now.UnixNano()-last >= int64(quiet) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the prompt was pasted and not submitted: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
