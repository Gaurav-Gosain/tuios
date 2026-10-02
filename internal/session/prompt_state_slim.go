//go:build slim

package session

import (
	"errors"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// tuios-slim follows no agent state, with one exception. The respond grant
// stops a pane from typing into another pane that is waiting on a prompt,
// since the keys would answer it (see typingRefusal). That rule needs to know
// a pane is waiting, and without a source it never fires: any pane could
// answer another pane's permission prompt.
//
// The source kept is the OSC 9;4 warning state, the in-band way a program
// says it needs a person. It sets the window to needs_input, and any other
// OSC 9;4 report clears it. The full build reads the same sequence, but only
// on a pane something else has marked as an agent; tuios-slim has nothing
// else to mark one, so it believes the sequence on every pane. A program that
// is not an agent and sends the warning state only stops other panes without
// respond from typing into it until it sends another report.

// errPromptUnchanged ends a state mutation that would change nothing.
var errPromptUnchanged = errors.New("prompt state unchanged")

// progressParked applies the report the VT callback just parked. It runs on
// its own goroutine: the callback holds the terminal lock, and the session
// state must not be written under it. The output event is no place for it
// either, since it can run before the emulator has parsed the bytes, and a
// prompt is often the last thing a program prints.
func (s *Session) progressParked(p *PTY, windowID string) {
	go s.applyParkedPrompt(p, windowID)
}

// Bounds of the wait for a window that is not in the session state yet.
const (
	promptRetryFirst = 10 * time.Millisecond
	promptRetryMax   = 500 * time.Millisecond
)

// applyParkedPrompt takes the newest parked OSC 9;4 state and records whether
// the window waits on a prompt.
//
// The PTY starts before its window is in the session state: CreateWindow
// adds the window after the spawn, and a restore adds every window after it
// has spawned them all. A report from a program that asks at once can arrive
// in that gap. Dropping it left the prompt with no state, and any pane could
// answer it. So a report for a window not yet present goes back to the slot,
// unless a newer one took it, and is tried again until the window appears or
// the PTY closes.
func (s *Session) applyParkedPrompt(p *PTY, windowID string) {
	if windowID == "" {
		// A PTY with no window has no state to hold the report.
		p.agentProgress.Store(0)
		return
	}
	wait := promptRetryFirst
	for {
		if s.applyParkedPromptOnce(p, windowID) {
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, promptRetryMax)
	}
}

// applyParkedPromptOnce applies the parked report, reporting false when the
// window is not in the state yet and the report waits for it.
func (s *Session) applyParkedPromptOnce(p *PTY, windowID string) bool {
	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	v := p.agentProgress.Swap(0)
	if v == 0 {
		return true
	}
	waiting := vt.ProgressState(v-1) == vt.ProgressWarning
	missing := false
	_ = s.mutateState(func(st *SessionState) error {
		// The exact id only: findWindowStateIndex also takes a name, and a
		// pane could name its own window after this one's id.
		idx := slices.IndexFunc(st.Windows, func(w WindowState) bool { return w.ID == windowID })
		if idx < 0 {
			missing = true
			return errPromptUnchanged
		}
		w := &st.Windows[idx]
		switch {
		case waiting && w.AgentState != AgentStateNeedsInput:
			w.AgentState = AgentStateNeedsInput
		case !waiting && w.AgentState == AgentStateNeedsInput:
			w.AgentState = AgentStateNone
		default:
			return errPromptUnchanged
		}
		w.AgentStateAt = time.Now().UnixNano()
		return nil
	})
	if missing {
		// A newer report parked meanwhile wins: it is the one still true.
		p.agentProgress.CompareAndSwap(0, v)
		return false
	}
	return true
}
