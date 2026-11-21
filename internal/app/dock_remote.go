package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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
		ConfigNoSave: payload.ConfigNoSave,
		RequestID:    payload.RequestID,
		afterSync:    m.stateSyncsArrived.Load(),
	}
	select {
	case m.RemoteCommandChan <- msg:
		return false
	default:
		return true
	}
}

func listenRemoteCommandsUntil(stop <-chan struct{}, ch <-chan RemoteCommandMsg) tea.Cmd {
	return listenOnce(stop, ch, func(msg RemoteCommandMsg) tea.Msg { return msg })
}
