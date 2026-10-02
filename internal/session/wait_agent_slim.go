//go:build slim

package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/edition"
)

// tuios-slim follows no agent state and carries no agent mail, so wait-for
// has neither condition. A call for one says so.

func waitAgentMissing(condition string) *verbError {
	return newVerbError(ErrVerbInvalidParams, edition.MissingMessage("wait-for "+condition))
}

func refuseSelectFromHostedPane(*connState) *verbError { return nil }

func (d *Daemon) parseVerbSelector(string) (*Selector, *verbError) {
	return nil, waitAgentMissing("agent-state")
}

func (d *Daemon) waitAgentStateSelect(*Selector, string, bool, <-chan time.Time) (any, *verbError) {
	return nil, waitAgentMissing("agent-state")
}

func (d *Daemon) waitAgentStateAnySession(string, <-chan time.Time) (any, *verbError) {
	return nil, waitAgentMissing("agent-state")
}

func (d *Daemon) waitAgentState(string, string, string, <-chan time.Time) (any, *verbError) {
	return nil, waitAgentMissing("agent-state")
}

func (d *Daemon) waitAgentMessage(string, string, uint64, <-chan time.Time) (any, *verbError) {
	return nil, waitAgentMissing("agent-message")
}
