//go:build slim

package session

import (
	"errors"
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

// applyParkedPrompt takes the newest parked OSC 9;4 state and records whether
// the window waits on a prompt.
func (s *Session) applyParkedPrompt(p *PTY, windowID string) {
	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	v := p.agentProgress.Swap(0)
	if v == 0 {
		return
	}
	waiting := vt.ProgressState(v-1) == vt.ProgressWarning
	_ = s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return err
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
}
