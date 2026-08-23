package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A routed command's way into a client that is not the local attach client.
//
// The local attach path holds the tea.Program and can Send straight into it
// (cmd/tuios/session_commands.go). The SSH server and tuios-web hold the model
// but hand the program to a per-connection goroutine, so they push through a
// channel the way every other cross-goroutine source in this package does.
//
// Without this, a verb the daemon routes to the attached TUI reaches a local
// client and nobody else: set-option, send-keys and now refresh-dock all timed
// out against a browser or SSH session while findTUIClient still reported one
// attached. Web and SSH are first-class, so the channel is not dock-specific
// even though the dock verbs are what found it missing.

// remoteCommandQueue is how many routed commands may wait. A routed verb blocks
// its caller for ten seconds, so a backlog deeper than this is a caller that has
// already given up.
const remoteCommandQueue = 16

// QueueRemoteCommand hands a routed command to the Update loop. Reports whether
// it was dropped, so the caller can log a drop rather than lose it silently.
func (m *OS) QueueRemoteCommand(payload *session.RemoteCommandPayload) bool {
	if m.RemoteCommandChan == nil || payload == nil {
		return false
	}
	msg := RemoteCommandMsg{
		CommandType:  payload.CommandType,
		TapeCommand:  payload.TapeCommand,
		TapeArgs:     payload.TapeArgs,
		TapeScript:   payload.TapeScript,
		Keys:         payload.Keys,
		Literal:      payload.Literal,
		Raw:          payload.Raw,
		WindowTarget: payload.WindowTarget,
		ConfigPath:   payload.ConfigPath,
		ConfigValue:  payload.ConfigValue,
		RequestID:    payload.RequestID,
	}
	select {
	case m.RemoteCommandChan <- msg:
		return false
	default:
		return true
	}
}

// ListenForRemoteCommands blocks on the routed-command channel. Re-armed by the
// RemoteCommandMsg handler like every other listener here.
func ListenForRemoteCommands(ch <-chan RemoteCommandMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}
