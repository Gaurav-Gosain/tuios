package learn

// The names the page contract (cmd/tuios-wasm/README.md) promises, as lists.
// The lint in internal/lint checks every lesson against them, so a lesson
// cannot wait on an event, an overlay or a setting that never comes.

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
	OverlayScrollback, OverlayKeybinds, OverlayQuitMenu, OverlayWorkspaceSwitcher,
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
