package input

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.InboxPopSettling() {
		return o, nil
	}
	top := o.CockpitTop()
	if top == app.CockpitLayerCopy {
		if !o.CockpitCopyImplicit() {
			return handleCockpitCopyKey(msg, o)
		}
		o.CockpitCopyEnd()
		if msg.String() == "esc" {
			return o, nil
		}
		top = o.CockpitTop()
	}
	if !plainTextKey(msg) && !(o.InboxMentionListOpen() && listMoveKey(msg)) {
		if out, cmd, ok := handleGlobalBinds(msg, o); ok {
			return out, cmd
		}
	}
	switch top {
	case app.CockpitLayerImage:
		return o, o.CockpitImageKey(msg.String())
	case app.CockpitLayerSpawn:
		return o, o.CockpitSpawnKey(msg)
	case app.CockpitLayerCompare:
		return o, o.CockpitCompareKey(msg.String())
	case app.CockpitLayerArtifact:
		return o, o.CockpitArtifactKey(msg.String())
	case app.CockpitLayerNewCard:
		return o, o.CockpitNewCardKey(msg)
	case app.CockpitLayerConfirm:
		return o, o.InboxConfirmKey(msg.String())
	}
	o.InboxKeyPressed(msg.String())
	switch top {
	case app.CockpitLayerPeek:
		return handleInboxPeekInput(msg, o)
	case app.CockpitLayerReply:
		return handleInboxReplyInput(msg, o)
	case app.CockpitLayerReason:
		return handleInboxReasonInput(msg, o)
	case app.CockpitLayerSelect:
		return handleInboxSelectInput(msg, o)
	case app.CockpitLayerMenu:
		return o, o.CockpitMenuKey(msg.String())
	case app.CockpitLayerShip:
		return o, o.CockpitShipKey(msg.String())
	case app.CockpitLayerSnooze:
		return o, o.InboxSnoozeKey(msg.String())
	case app.CockpitLayerBulkSheet:
		return o, o.InboxAgentsSheetKey(msg.String())
	}
	key := msg.String()
	marks := o.InboxAgentsMarks() > 0
	action := tableAction(o, msg, overlayKeys(o), config.TableInbox)
	switch action {
	case config.ActionInboxMark:
		noteInboxAction(o, action)
		o.InboxAgentsToggleMark()
		return o, nil
	case config.ActionInboxMarkAll:
		noteInboxAction(o, action)
		o.InboxAgentsMarkAll()
		return o, nil
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if marks {
			switch key {
			case "1":
				o.InboxAgentsBulkAllow()
			case "3":
				return o, o.InboxAgentsBulkDeny()
			}
			return o, nil
		}
		o.CockpitSync()
		return o, o.InboxNumber(int(key[0] - '0'))
	}
	if marks && action == config.ActionInboxReply {
		return o, o.InboxAgentsBulkReply()
	}
	if cmd, handled := o.CockpitKey(action); handled {
		noteInboxAction(o, action)
		return o, cmd
	}
	if action == "" {
		return o, nil
	}
	o.CockpitSync()
	if marks && action == config.ActionInboxSnooze {
		o.InboxAgentsBulkSnooze()
		return o, nil
	}
	if app.InboxWorkActions[action] {
		cmd, handled := o.InboxWorkAction(action)
		if handled {
			noteInboxAction(o, action)
		}
		return o, cmd
	}
	noteInboxAction(o, action)
	switch action {
	case config.ActionInboxSelect:
		o.InboxStartSelect()
	case config.ActionInboxClose:
		o.CloseInbox()
	case config.ActionInboxPeek:
		return o, o.InboxPeek()
	case config.ActionInboxDismiss:
		if marks {
			return o, o.InboxAgentsBulkDismiss()
		}
		return o, o.InboxDismiss()
	case config.ActionInboxResume:
		if marks {
			return o, o.InboxAgentsBulkResume()
		}
		return o, o.InboxResume()
	case config.ActionInboxPassOn:
		return o, o.InboxRelease()
	case config.ActionInboxMailbox:
		return o, o.InboxOpenMailbox()
	case config.ActionInboxSort:
		o.InboxAgentsCycleSort()
		o.CockpitTouch()
	case config.ActionInboxViewPane:
		return o, o.InboxAgentsViewPane()
	}
	return o, nil
}

var defaultOverlayKeys = sync.OnceValue(func() *config.KeybindRegistry {
	return config.NewKeybindRegistry(config.DefaultConfig())
})

func overlayKeys(o *app.OS) *config.KeybindRegistry {
	if o.KeybindRegistry != nil {
		return o.KeybindRegistry
	}
	return defaultOverlayKeys()
}

func handleInboxReasonInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.InboxReasonKey(msg)
}

func handleInboxReplyInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	action := ""
	if o.InboxAgentsOn() {
		action = tableAction(o, msg, overlayKeys(o), config.TableInbox)
	}
	if action == config.ActionInboxViewPane {
		o.InboxReplyCancel()
		return o, o.InboxAgentsViewPane()
	}
	if (action == config.ActionInboxRegionNext || action == config.ActionInboxRegionPrev) && !o.InboxMentionListOpen() {
		return o, o.CockpitKeyAction(action)
	}
	return o, o.InboxReplyKey(msg)
}

func handleInboxSelectInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.InboxSelectKey(msg)
	return o, nil
}

func handleInboxPeekInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if o.InboxPeekComposing() {
		return o, o.InboxPeekTextKey(msg)
	}
	if o.InboxPeekAskOpen() {
		return o, o.InboxPeekAskKey(key)
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		o.InboxPeekAsk(harness.ActionChoose, key)
		return o, nil
	}
	action := tableAction(o, msg, overlayKeys(o), config.TableInboxPeek)
	if action == "" {
		return o, nil
	}
	noteInboxAction(o, action)
	switch action {
	case config.ActionPeekBack:
		o.InboxPeekBack()
	case config.ActionPeekGo:
		o.InboxPeekGo()
	case config.ActionPeekApprove:
		o.InboxPeekAsk(harness.ActionApprove, "")
	case config.ActionPeekApproveAlways:
		o.InboxPeekAsk(harness.ActionApproveAlways, "")
	case config.ActionPeekDeny:
		o.InboxPeekAsk(harness.ActionDeny, "")
	case config.ActionPeekType:
		o.InboxPeekStartText()
	case config.ActionPeekReadAgain:
		return o, o.InboxPeekRefresh()
	}
	return o, nil
}

func noteInboxAction(o *app.OS, action string) {
	if action == actionMenuOpened {
		return
	}
	o.NoteAction(action)
	if o.OnAction != nil {
		o.OnAction(action)
	}
}

func plainTextKey(msg tea.KeyPressMsg) bool {
	return msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper|tea.ModHyper|tea.ModMeta) == 0
}

func listMoveKey(msg tea.KeyPressMsg) bool {
	return msg.Mod&tea.ModCtrl != 0 && (msg.Code == 'p' || msg.Code == 'n')
}
