package session

import (
	"tuios.dev/vt"

	"context"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

const focusInReport = "\x1b[I"

const promptSubmitMaxWait = 300 * time.Millisecond

const promptSubmitQuiet = 80 * time.Millisecond

const promptSubmitPoll = 10 * time.Millisecond

const inputLiveMaxWait = 15 * time.Second

const inputLiveQuiet = 150 * time.Millisecond

const inputLivePoll = 20 * time.Millisecond

type promptPane interface {
	Write([]byte) (int, error)
	BracketedPasteOn() bool
	LastOutput() int64
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
	return p.terminal != nil && p.terminal.FocusReportingEnabled()
}

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

func submitPrompt(ctx context.Context, pane promptPane, text string, in harness.InputProfile) (time.Time, error) {
	return submitPromptStamped(ctx, pane, text, in, promptSubmitQuiet, promptSubmitMaxWait)
}

func submitPromptStamped(ctx context.Context, pane promptPane, text string, in harness.InputProfile, quiet, maxWait time.Duration) (time.Time, error) {
	return submitPastesStamped(ctx, pane, []string{text}, in, quiet, maxWait)
}

func submitPastes(ctx context.Context, pane promptPane, pastes []string, in harness.InputProfile) (time.Time, error) {
	return submitPastesStamped(ctx, pane, pastes, in, promptSubmitQuiet, promptSubmitMaxWait)
}

type submitGuardKey struct{}

func withSubmitGuard(ctx context.Context, check func() *verbError) context.Context {
	return context.WithValue(ctx, submitGuardKey{}, check)
}

func submitPastesStamped(ctx context.Context, pane promptPane, pastes []string, in harness.InputProfile, quiet, maxWait time.Duration) (time.Time, error) {
	if in.SubmitKey == "" {
		in.SubmitKey = "\r"
	}
	if in.FocusBeforeSubmit && pane.FocusReportingOn() {
		if _, err := pane.Write([]byte(focusInReport)); err != nil {
			return time.Time{}, errfmt.Errorf("failed to report focus: %w", err)
		}
	}
	bracket := in.BracketedPaste && pane.BracketedPasteOn()
	for _, text := range pastes {
		body := promptBody(text)
		if body == "" && len(pastes) > 1 {
			continue
		}
		if bracket {
			body = bracketedPasteStart + body + bracketedPasteEnd
		}
		pastedAt := time.Now()
		if body != "" {
			if _, err := pane.Write([]byte(body)); err != nil {
				return time.Time{}, errfmt.Errorf("failed to write the prompt: %w", err)
			}
		}
		if err := waitPasteTaken(ctx, pane, pastedAt, quiet, maxWait); err != nil {
			return time.Time{}, err
		}
	}
	if check, ok := ctx.Value(submitGuardKey{}).(func() *verbError); ok && check != nil {
		if verr := check(); verr != nil {
			return time.Time{}, errfmt.Errorf("the prompt was pasted and not submitted: %w", verr)
		}
	}
	submittedAt := time.Now()
	if _, err := pane.Write([]byte(in.SubmitKey)); err != nil {
		return time.Time{}, errfmt.Errorf("failed to submit the prompt: %w", err)
	}
	return submittedAt, nil
}

func promptBody(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = vt.SanitizePaste(text)
	return strings.TrimRight(text, "\n")
}

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
			return errfmt.Errorf("the prompt was pasted and not submitted: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitInputLive(ctx context.Context, pane promptPane, in harness.InputProfile, quiet, maxWait time.Duration) error {
	if !in.BracketedPaste {
		return nil
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(inputLivePoll)
	defer ticker.Stop()
	for {
		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}
		if pane.BracketedPasteOn() && now.UnixNano()-pane.LastOutput() >= int64(quiet) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errfmt.Errorf("the prompt was not typed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
