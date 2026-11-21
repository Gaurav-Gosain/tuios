package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func handlePrefixTransfers(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenTransfers()
}

func handleTransfersInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	action := tableAction(o, msg, overlayKeys(o), config.TableTransfers)
	var cmd tea.Cmd
	switch action {
	case config.ActionTransfersDown:
		o.TransfersMove(1)
	case config.ActionTransfersUp:
		o.TransfersMove(-1)
	case config.ActionTransfersPause:
		cmd = o.TransfersPause()
	case config.ActionTransfersCancel:
		o.TransfersCancel()
	case config.ActionTransfersRetry:
		cmd = o.TransfersRetry()
	case config.ActionTransfersClear:
		cmd = o.TransfersClear()
	case config.ActionTransfersAnswer:
		o.TransfersAnswer()
	case config.ActionTransfersClose:
		o.CloseTransfers()
	default:
		return o, nil
	}
	o.NoteAction(action)
	return o, cmd
}

func handleCopyDialogInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.CopyDialogKey(msg.String(), msg.Text)
}
