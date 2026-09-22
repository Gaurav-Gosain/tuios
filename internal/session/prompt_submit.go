package session

import (
	"context"
	"fmt"
	"strings"
	"time"
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
//  3. A carriage return.

// Bracketed paste delimiters, DECSET 2004.
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

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
}

// BracketedPasteOn reports whether the application in the pane has turned on
// bracketed paste mode (DECSET 2004), as the daemon's emulator last read it.
func (p *PTY) BracketedPasteOn() bool {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.terminal != nil && p.terminal.BracketedPasteEnabled()
}

// submitPrompt types text into the pane as one paste and submits it with a
// carriage return. It returns once the carriage return is written, or with the
// first write error, or with ctx's error if ctx ends during the wait, in which
// case the paste was written and the carriage return was not.
func submitPrompt(ctx context.Context, pane promptPane, text string) error {
	return submitPromptTimed(ctx, pane, text, promptSubmitQuiet, promptSubmitMaxWait)
}

// submitPromptTimed is submitPrompt with the waits as parameters.
func submitPromptTimed(ctx context.Context, pane promptPane, text string, quiet, maxWait time.Duration) error {
	body := promptBody(text)
	if pane.BracketedPasteOn() {
		body = bracketedPasteStart + body + bracketedPasteEnd
	}
	// Taken before the write, so output the paste caused counts however soon
	// it arrives.
	pastedAt := time.Now()
	if body != "" {
		if _, err := pane.Write([]byte(body)); err != nil {
			return fmt.Errorf("failed to write the prompt: %w", err)
		}
	}
	if err := waitPasteTaken(ctx, pane, pastedAt, quiet, maxWait); err != nil {
		return err
	}
	if _, err := pane.Write([]byte("\r")); err != nil {
		return fmt.Errorf("failed to submit the prompt: %w", err)
	}
	return nil
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
