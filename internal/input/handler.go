// Package input routes keyboard and mouse input for TUIOS: the dispatch between
// Window Management and Terminal modes, the prefix commands, the encoding of
// keys for the focused pane's PTY, mouse handling, copy mode and paste.
package input

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/charmbracelet/x/ansi"
)

// HandleInput is the main input coordinator that routes messages to appropriate handlers
func HandleInput(msg tea.Msg, o *app.OS) (tea.Model, tea.Cmd) {
	var result tea.Model
	var cmd tea.Cmd

	defer o.SettleMultiCopy()

	msg = o.MapPointer(msg)
	defer o.SetPointerInLayout(false)
	if o.PointerSwallowed() {
		if _, ok := msg.(tea.MouseReleaseMsg); ok {
			o.ReleaseGestureAnnouncements()
		}
		return o, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		result, cmd = HandleKeyPress(msg, o)
	case tea.KeyReleaseMsg:
		if o.ReleaseHoldKey(readKey(tea.KeyPressMsg(msg.Key()))) {
			result, cmd = o, nil
			break
		}
		if o.HintsOpen() || o.PaneLabelsOpen() {
			return o, nil
		}
		forwardKeyReleaseToFocused(msg, o)
		return o, nil
	case tea.PasteStartMsg:
		return o, nil
	case tea.PasteEndMsg:
		return o, nil
	case tea.MouseClickMsg:
		if _, inside := o.SidebarSessionAt(msg.X, msg.Y); !inside {
			o.BlurSidebarSession()
		}
		o.CloseHints()
		o.ClosePaneLabels()
		if o.CaptureActive() {
			result, cmd = handleCaptureMouseClick(msg, o)
			break
		}
		if o.ReviewOpen() {
			return o, nil
		}
		if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseClick(msg, o)
		} else {
			result, cmd = handleMouseClick(msg, o)
		}
	case tea.MouseMotionMsg:
		if o.HintsOpen() || o.PaneLabelsOpen() {
			return o, nil
		}
		if o.CaptureActive() {
			return handleCaptureMouseMotion(msg, o)
		}
		if o.ReviewOpen() {
			return o, nil
		}
		if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseMotion(msg, o)
			return result, cmd
		}
		if quickLookCopyMotion(msg, o) {
			return o, nil
		}
		if o.ShowInbox {
			if taken, cmd := cockpitMouseMotion(msg, o); taken {
				return o, cmd
			}
		}
		prev := o.FocusedWindow
		moved, cmd := handleMouseMotion(msg, o)
		if moved != nil && moved.IsDaemonSession && moved.FocusedWindow != prev {
			moved.SyncStateToDaemon()
		}
		if moved != nil {
			cmd = tea.Batch(cmd, moved.TakePathStatCmd())
		}
		return moved, cmd
	case tea.MouseReleaseMsg:
		if o.CaptureActive() {
			result, cmd = handleCaptureMouseRelease(o)
		} else if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseRelease(o)
		} else {
			result, cmd = handleMouseRelease(msg, o)
		}
		o.ReleaseGestureAnnouncements()
	case tea.MouseWheelMsg:
		o.CloseHints()
		o.ClosePaneLabels()
		if o.ReviewOpen() {
			switch msg.Button {
			case tea.MouseWheelUp:
				o.ReviewWheel(-3)
			case tea.MouseWheelDown:
				o.ReviewWheel(3)
			}
			return o, nil
		}
		if o.ScreenshotPreviewOpen() {
			result, cmd = handleScreenshotPreviewWheel(msg, o)
		} else if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseWheel(msg, o)
		} else {
			result, cmd = handleMouseWheel(msg, o)
		}
	case app.StripTouchMsg:
		o.CloseHints()
		o.ClosePaneLabels()
		if o.ReviewOpen() || o.ScreenshotPreviewOpen() || o.ShowScrollbackBrowser {
			return o, nil
		}
		result, cmd = handleStripTouch(msg, o)
	case tea.PasteMsg:
		if o.ShowAgentMail && o.AgentMail.Composing {
			return o, o.AgentMailPaste(msg.Content)
		}
		if o.InboxReplyOpen() {
			return o, o.InboxReplyPaste(msg.Content)
		}
		if o.TaskCaptureOpen() {
			return o, o.TaskCapturePaste(msg.Content)
		}
		if o.CockpitSpawnOpen() {
			return o, o.CockpitSpawnPaste(msg.Content)
		}
		if cmd, taken := pasteTakenByOverlay(o, msg.Content); taken {
			return o, cmd
		}
		if o.SidebarSessionFocused() {
			if msg.Content != "" {
				o.PasteSidebarSession(msg.Content)
			}
			return o, nil
		}
		if o.Mode == app.TerminalMode {
			o.NotePaneKey()
			if msg.Content == "" {
				return o, o.PasteImageOnEmptyPaste()
			}
			if cmd := o.DropPaste(msg.Content); cmd != nil {
				return o, cmd
			}
			forwardPasteToFocused(o, msg.Content)
		}
		return o, nil
	case tea.ClipboardMsg:
		o.NotePasteArrived()
		if !o.ClaimPasteReply(msg.Content) {
			return o, nil
		}
		if o.ShowAgentMail && o.AgentMail.Composing {
			if msg.Content == "" {
				return o, nil
			}
			return o, o.AgentMailPaste(msg.Content)
		}
		if o.InboxReplyOpen() {
			if msg.Content == "" {
				return o, nil
			}
			return o, o.InboxReplyPaste(msg.Content)
		}
		if o.TaskCaptureOpen() {
			return o, o.TaskCapturePaste(msg.Content)
		}
		if o.CockpitSpawnOpen() {
			return o, o.CockpitSpawnPaste(msg.Content)
		}
		if cmd, taken := pasteTakenByOverlay(o, msg.Content); taken {
			return o, cmd
		}
		if o.Mode == app.TerminalMode {
			o.ClipboardContent = msg.Content
			handleClipboardPaste(o)
		}
		return o, nil
	default:
		return o, nil
	}

	if o.ShowInbox {
		cmd = tea.Batch(cmd, o.InboxApprovalFetch(), o.InboxQuestionFetch(), o.InboxRecapFetch(), o.InboxAgentsTick(), o.InboxThreadFetch(), o.ReplyHistoryFetch(), o.CockpitImagePicture(), o.CockpitSideFetch())
	} else if o.ShowAgentMail {
		cmd = tea.Batch(cmd, o.ReplyHistoryFetch())
	}
	cmd = tea.Batch(cmd, o.AgentRecapFetch())
	cmd = tea.Batch(cmd, o.QuickLookSync())
	cmd = tea.Batch(cmd, o.TakePathStatCmd())
	cmd = tea.Batch(cmd, o.ClipboardApprovalCmd())

	if o.IsDaemonSession {
		o.SyncStateToDaemon()
	}

	return result, cmd
}

func pasteTakenByOverlay(o *app.OS, content string) (tea.Cmd, bool) {
	if o.ShowInbox && o.CockpitArtifactFocused() {
		return o.CockpitArtifactPaste(content), true
	}
	if o.InboxReplyOpen() {
		return nil, true
	}
	if o.ShowAgentMail && o.AgentMail.Composing {
		return nil, true
	}
	switch {
	case o.ShowInbox && o.InboxReasonOpen():
		o.InboxReasonPaste(content)
		return nil, true
	case o.ShowInbox && o.CockpitNewCardOpen():
		o.CockpitNewCardPaste(content)
		return nil, true
	case o.ShowInbox && o.InboxPeekComposing():
		o.InboxPeekPaste(content)
		return nil, true
	case o.ShowInbox && o.InboxSelecting():
		o.InboxSelectType(content)
		return nil, true
	}
	if o.MultiCopy != nil && o.MultiCopy.Save != nil {
		o.MultiCopySaveType(content)
		return nil, true
	}
	if paste := textFieldPaste(o); paste != nil {
		return paste(pasteLine(content)), true
	}
	return nil, o.HintsOpen() || o.PaneLabelsOpen() || o.OverlayOwnsKeyboard()
}

func shouldShowQuitDialog(o *app.OS) bool {
	if o.Settings.AlwaysConfirmQuit {
		return true
	}
	for _, win := range o.Windows {
		if win != nil && win.HasForegroundProcess() {
			return true
		}
	}
	return false
}

func quitSession(o *app.OS) (*app.OS, tea.Cmd) {
	o.QuitSession()
	return o, o.QuitCmd()
}

func requestQuit(o *app.OS) (*app.OS, tea.Cmd) {
	if o.IsDaemonSession || shouldShowQuitDialog(o) {
		o.OpenQuitMenu()
		return o, nil
	}
	return quitSession(o)
}

func detachSession(o *app.OS) (*app.OS, tea.Cmd, bool) {
	cmd := o.DetachClient()
	if cmd == nil {
		return o, nil, false
	}
	return o, cmd, true
}

// HandleKeyPress handles all keyboard input and routes to mode-specific handlers
func HandleKeyPress(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.NoteHostKey(msg)
	msg = readKey(msg)

	if o.ShowKeys {
		o.CaptureKeyEvent(msg)
	}

	if !o.Locked() && !o.WindowModeOff() && o.PressHoldKey(msg) {
		return o, nil
	}
	msg = o.StripHoldModifier(msg)

	if o.FileDragActive() && msg.String() == "esc" {
		o.CancelFileDrag()
		return o, nil
	}

	if isModifierKeyPress(msg) &&
		(o.KeysGoToBindings() || o.PaneKeyboardFlags()&ansi.KittyReportAllKeysAsEscapeCodes == 0) {
		return o, nil
	}

	if chords := composedChords(msg, o.HostBaseCode(msg)); len(chords) > 0 &&
		chords[len(chords)-1] != msg.Keystroke() &&
		optionGlyphsApply(msg, o.Settings.KeyboardLayout, o.Settings.OptionGlyphs) {
		o.NoteComposedOptionChord(chords[len(chords)-1])
	}
	if got, arrow, ok := macRewrittenAltArrow(msg); ok {
		o.NoteRewrittenAltArrow(got, arrow)
	}

	var escCmd tea.Cmd
	if msg.String() == "esc" {
		o.DismissNotifications()
		escCmd = o.CancelHostOpen()
	}
	if escCmd != nil {
		m, cmd := routeAfterEsc(msg, o)
		return m, tea.Batch(escCmd, cmd)
	}
	return routeAfterEsc(msg, o)
}

func routeAfterEsc(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.CaptureActive() {
		return HandleCaptureKey(msg, o)
	}
	if o.ScreenshotPreviewOpen() {
		return HandleScreenshotPreviewKey(msg, o)
	}

	if m, cmd, ok := routeOverlayScreenshot(msg, o); ok {
		return m, cmd
	}
	return routeKey(msg, o)
}

func routeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.UpgradeConfirmOpen() {
		return o, o.UpgradeConfirmKey(msg.String())
	}
	if o.TaskCaptureOpen() {
		return o, o.TaskCaptureKey(msg)
	}
	if o.MachineSheetOpen() {
		if cmd, ok := pasteKeyIntoTextField(msg, o); ok {
			return o, cmd
		}
		return o, o.MachineSheetKey(msg)
	}
	if cmd, ok := pasteKeyIntoTextField(msg, o); ok {
		return o, cmd
	}
	if o.PromptOpen() {
		cmd, _ := o.ListPickerKey(msg.String(), msg)
		return o, cmd
	}
	if o.ShowSessionClose {
		return handleSessionCloseInput(msg, o)
	}
	if o.DaemonRestartOpen() {
		return handleDaemonRestartInput(msg, o)
	}

	if o.HintsOpen() {
		return handleHintsKey(msg, o)
	}
	if o.PaneLabelsOpen() {
		return handlePaneLabelsKey(msg, o)
	}

	if o.ShowQuitMenu {
		return handleQuitMenuKey(msg, o)
	}

	if o.ContextMenuActive() {
		return handleContextMenuKey(msg, o)
	}

	if o.PathPickerOpen() {
		return handlePathPickerKey(msg, o)
	}

	if o.Menu != "" && o.MenuState().Origin.Set {
		return HandleMenuCommand(msg, o)
	}
	if o.ReviewOpen() {
		return handleReviewInput(msg, o)
	}

	if o.ShowAccentPicker {
		return handleAccentPickerInput(msg, o)
	}
	if o.Renaming() {
		return handleRenameMode(msg, o)
	}
	if o.FilePromptOpen() {
		return handleFilePromptInput(msg, o)
	}
	if o.WorktreePromptOpen() {
		return handleWorktreePromptInput(msg, o)
	}
	if o.PluginApprovalOpen() {
		return o, o.PluginApprovalKey(msg.String(), msg.Text)
	}
	if o.IntegrationAskOpen() {
		return o, o.IntegrationAskKey(msg.String())
	}

	if o.CopyDialogOpen() {
		return handleCopyDialogInput(msg, o)
	}
	if o.QuickLookFree() && !o.PrefixActive && !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		return handleFreeQuickLookKey(msg, o)
	}
	if o.TransfersOwnsKeys() && !o.PrefixActive && !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		return handleTransfersInput(msg, o)
	}

	if o.SidebarSessionFocused() && !o.AnyOverlayOpen() && o.Nested() {
		if isNestExitKey(msg, &o.Settings, o.HostBaseCode(msg)) {
			o.LeaveNest()
			return o, nil
		}
		typeIntoRailPane(msg, o)
		return o, nil
	}
	if o.RailPaneFocused() && !o.AnyOverlayOpen() && !o.PrefixActive && isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		o.BlurSidebarSession()
	}
	if o.SidebarSessionFocused() && !o.AnyOverlayOpen() {
		if msg.String() == "alt+esc" {
			o.BlurSidebarSession()
			return o, nil
		}
		key := withoutChordText(msg).String()
		workspaceChord := !o.Locked() && strings.HasPrefix(key, "alt+") && len(key) == len("alt+1") && key[len(key)-1] >= '1' && key[len(key)-1] <= '9'
		if !o.PrefixActive && !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) && !workspaceChord {
			typeIntoRailPane(msg, o)
			return o, nil
		}
	}
	if o.SidebarFocused && !o.PanelOwnsKeyboard() && !o.PrefixActive && !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		return HandleSidebarKey(msg, o)
	}

	if o.ShowTapeReview {
		if o.HandleTapeReviewInput(commandKey(msg)) {
			return o, nil
		}
	}

	if o.ShowTapeManager {
		key := commandKey(msg)
		if o.TapeManager != nil && o.TapeManager.Mode == app.TapeManagerNaming {
			key = msg.String()
		}
		if o.HandleTapeManagerInput(key) {
			return o, nil
		}
	}

	if o.ScriptMode &&
		sectionAction(msg, o, config.TableScript) == "script_pause" {
		o.NoteAction("script_pause")
		o.ScriptPaused = !o.ScriptPaused
		return o, nil
	}

	o.CancelCopyFlash()

	if !o.PrefixActive && !o.Locked() {
		if m, cmd, ok := tryPrefixRepeat(msg, o); ok {
			return m, cmd
		}
	}

	if m, cmd, ok := routeKeyMode(msg, o); ok {
		return m, cmd
	}

	if o.Mode == app.TerminalMode {
		return HandleTerminalModeKey(msg, o)
	}

	if isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) && !leaderBoundInPrefix(msg, o) {
		return handlePrefixKey(msg, o)
	}

	if out, cmd, ok := routeMenuKey(msg, o); ok {
		return out, cmd
	}

	if o.PrefixActive {
		return HandlePrefixCommand(msg, o)
	}

	return HandleWindowManagementModeKey(msg, o)
}

func handleRenameMode(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return o, o.CommitRename()
	case "esc":
		o.EndRename()
		return o, nil
	case "ctrl+c":
		if o.RenameText() == "" {
			o.EndRename()
		} else {
			o.RenameClear()
		}
		return o, nil
	}
	if cmd, ok := o.RenameClipboard(msg.String()); ok {
		return o, cmd
	}
	o.RenameKey(msg.String(), msg.Text)
	return o, nil
}

func isLeaderKey(msg tea.KeyPressMsg, s *config.Settings, base rune) bool {
	return isConfiguredKey(msg, s, base, func(key string) bool { return config.IsLeaderPress(key, s.LeaderKey) })
}

func isNestExitKey(msg tea.KeyPressMsg, s *config.Settings, base rune) bool {
	exit := s.GetNestExitKey()
	return isConfiguredKey(msg, s, base, func(key string) bool { return config.IsKeyPress(key, exit) })
}

func isConfiguredKey(msg tea.KeyPressMsg, s *config.Settings, base rune, is func(string) bool) bool {
	if is(producedKey(msg).String()) {
		return true
	}
	if pos, ok := baseLayoutKey(msg); ok && is(pos) {
		return true
	}
	if base == 0 {
		base = msg.BaseCode
	}
	if slices.ContainsFunc(composedChords(msg, base), is) {
		return optionGlyphsApply(msg, s.KeyboardLayout, s.OptionGlyphs)
	}
	return false
}

func leaderBoundInPrefix(msg tea.KeyPressMsg, o *app.OS) bool {
	return o.PrefixActive && o.KeybindRegistry != nil &&
		tableAction(o, msg, o.KeybindRegistry, config.TablePrefixMode) != ""
}

func handlePrefixKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.PrefixActive {
		o.SetMenuState(app.MenuState{})
		if o.KeybindRegistry != nil && o.KeybindRegistry.LeaderTwiceGoesBack() {
			o.LastWorkspace()
		}
		return o, nil
	}
	o.ArmLeader()
	return o, nil
}

func handleLogViewerKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := commandKey(msg)

	switch key {
	case "q", "esc":
		o.CloseLogViewer()
		return o, nil
	case "enter":
		o.LogViewerOpenSelected()
		return o, nil
	case "ctrl+u":
		o.LogViewerMove(-o.LogViewerPage())
		return o, nil
	case "ctrl+d":
		o.LogViewerMove(o.LogViewerPage())
		return o, nil
	case "A":
		return o, o.CopyLogs()
	case "E":
		return o, o.CopyLogErrors()
	}
	listKey(o, key, true, o.LogViewerPage(), o.LogViewerMove)

	return o, nil
}

func viewerTakesKey(msg tea.KeyPressMsg, o *app.OS) bool {
	return !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) && !o.PrefixActive && o.SubMenu == app.SubMenuNone && o.Menu == ""
}

func handleMessageViewKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := commandKey(msg)
	switch key {
	case "q", "esc":
		o.CloseMessageView()
		return o, nil
	case "y":
		return o, o.CopyMessageView()
	case "enter":
		o.MessageViewActivate()
		return o, o.TakeSidebarCmd()
	case "ctrl+u":
		o.MessageViewPage(-1)
		return o, nil
	case "ctrl+d", "space":
		o.MessageViewPage(1)
		return o, nil
	}
	switch o.ListKeys.Keys(key, true) {
	case listnav.Up:
		o.MessageViewScroll(-1)
	case listnav.Down:
		o.MessageViewScroll(1)
	case listnav.PageUp:
		o.MessageViewPage(-1)
	case listnav.PageDown:
		o.MessageViewPage(1)
	case listnav.Home:
		o.MessageViewTop()
	case listnav.End:
		o.MessageViewBottom()
	}
	return o, nil
}
