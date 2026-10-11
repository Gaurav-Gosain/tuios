package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handlePrefixTransfers opens the Copies panel: the copies between machines.
func handlePrefixTransfers(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenTransfers()
}

// handleTransfersInput handles keys while the Copies panel is up. The keys
// are the transfers section of the keybinds, so each can be bound again.
func handleTransfersInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	action := lookupAction(o, msg, overlayKeys(o).GetTransfersAction)
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

// handleCopyDialogInput handles keys while a copy's question is up. It owns
// every key: the key that opened it cannot answer it.
func handleCopyDialogInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.CopyDialogKey(msg.String(), msg.Text)
}
