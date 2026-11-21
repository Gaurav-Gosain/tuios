package learn

// EventTypes are the event types a page receives, lesson events aside.
var EventTypes = []string{
	EventReady, EventKey, EventAction, EventMode, EventPrefix,
	EventWindowOpen, EventWindowClose, EventWindowFocus, EventWindowRename,
	EventWindowMinimize, EventWindowZoom, EventWindowMove, EventWindowFloat,
	EventWindowWorkspace, EventSetting, EventWorkspace, EventTiling, EventLayout,
	EventTheme, EventOverlayOpen, EventOverlayClose, EventNotification, EventAgent,
	EventTapeStart, EventTapeFinish, EventKeys, EventPiP, EventAgentsSelect,
	EventAgentsTab, EventThemeEdit, EventShellStart, EventShellCommand, EventShellCwd,
	EventFiles, EventReviewMove, EventReviewNote, EventReviewSend, EventWorktree,
	EventCopyYank, EventTMLOpen, EventTMLInput, EventTMLPlace, EventTMLClose,
}

// OverlayNames are the overlay names of overlay.open, overlay.close and the
// snapshot's overlays list.
var OverlayNames = []string{
	OverlayHelp, OverlayWhichKey, OverlayCommandPalette, OverlayLauncher,
	OverlaySettings, OverlayThemePicker, OverlayCopyMode, OverlaySearch,
	OverlayScrollback, OverlayKeybinds, OverlayQuitMenu,
	OverlayLayoutPicker, OverlaySidebar, OverlayLogs, OverlayScreensaver,
	OverlayAgents, OverlayMail, OverlayPresets, OverlayGoto, OverlayHints,
	OverlaySpotlight, OverlayScratch, OverlayPaneLabels, OverlayPeek,
	OverlayNewAgent, OverlayCompare, OverlayReply, OverlayRailFocus,
	OverlayFileSearch, OverlayReview, OverlayWorktree, OverlayThemeEditor,
	OverlayGlyphPicker,
}

// SettingNames are the names a setting event carries.
func SettingNames() []string {
	out := []string{SettingGlyphs, SettingBorderStyle, SettingShowKeys}
	return append(out, lookPaths[:]...)
}
