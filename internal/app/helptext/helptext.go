// Package helptext holds the prose hover help shows: one table of entries
// keyed by the component help mode explains, so the text a person reads is
// data rather than switch arms in the renderer. It is a package of its own
// because a second language is a second table beside this one, never an edit
// to the engine.
//
// An entry carries the static part of an explanation. What varies with the
// running session does not live here: the engine substitutes the pane's name,
// its position, the counts it reads, and the keys the configuration binds,
// because those are values of the screen and not of the sentence.
package helptext

// Entry is the text of one component's page.
type Entry struct {
	// Title is the page heading, such as "Dock" or "Session name". A
	// placeholder names a value the engine substitutes.
	Title string
	// Purpose is the paragraph under the heading.
	Purpose string
}

// The ids of the components this package holds text for. A dock cell's id is
// DockCell of the name its [dock] list uses.
const (
	IDDock = "dock"

	IDDockCustom        = "dock/custom"
	IDDockPlugin        = "dock/plugin"
	IDDockWorkspaces    = "dock/workspaces"
	IDDockWindows       = "dock/windows"
	IDDockSessionPanel  = "dock/session-controls"
	IDDockMessages      = "dock/messages"
	IDDockOverflow      = "dock/overflow"
	IDDockClock         = "dock/clock"
	IDDockCPU           = "dock/cpu"
	IDDockRAM           = "dock/ram"
	IDDockTransfers     = "dock/transfers"
	IDDockSession       = "dock/session"
	IDDockMode          = "dock/mode"
	IDDockTrail         = "dock/trail"
	IDDockTape          = "dock/tape"
	IDDockNotifications = "dock/notifications"
	IDDockCopyHelp      = "dock/copy-help"
)

// DockCell is the entry id for a dock cell, whose name comes from the [dock]
// list the configuration carries: "mode" reads dock/mode.
func DockCell(name string) string { return "dock/" + name }

// dock is the dock's own page and every cell of the bar.
var dock = map[string]Entry{
	IDDock: {
		Title: "Dock",
		Purpose: "The dock is the status bar: the mode pill, the workspace strip, " +
			"minimized panes, session controls, notifications and whatever cells the " +
			"configuration puts on it. Clicking a cell acts on the row it names.",
	},
	IDDockCustom: {
		Title: "Dock cell: {name}",
		Purpose: "A custom dock cell. Its command is run on the refresh rule set for it, " +
			"and clicking it runs the on-click command.",
	},
	IDDockPlugin: {
		Title: "Dock cell: plugin",
		Purpose: "A cell supplied by a plugin. The plugin owns what it draws and what a " +
			"click does; its manifest and cells are described in the plugin's own " +
			"documentation.",
	},
	IDDockWorkspaces: {
		Title: "Workspaces",
		Purpose: "The workspace strip. Each pill is a workspace and its panes; clicking " +
			"one switches to it, and the arrows step a strip too long for the dock.",
	},
	IDDockWindows: {
		Title: "Minimized panes",
		Purpose: "One entry per minimized pane. Clicking an entry restores that pane; the " +
			"overflow marker opens the list of entries the dock had no room for.",
	},
	IDDockSessionPanel: {
		Title: "Session controls",
		Purpose: "The client controls: open the session list, leave this client (detach), " +
			"close the session, or start a new one. They act on the daemon session, not " +
			"on one pane.",
	},
	IDDockMessages: {
		Title: "Messages",
		Purpose: "The newest notification. Enter or a click acts on it, esc dismisses it, " +
			"and a cut message can be held to read the rest. Notifications also carry " +
			"agent asks.",
	},
	IDDockOverflow: {
		Title: "Minimized overflow",
		Purpose: "The marker for minimized panes the dock could not fit. It opens the " +
			"aggregate list, which names every one of them.",
	},
	IDDockClock: {
		Title: "Clock",
		Purpose: "The current time, drawn by the dock. Its format and whether it appears " +
			"are both appearance settings.",
	},
	IDDockCPU: {
		Title: "CPU",
		Purpose: "The CPU readout. It samples the machine while the dock is shown; turn " +
			"it off when the dock is not the place you watch load.",
	},
	IDDockRAM: {
		Title: "RAM",
		Purpose: "The memory readout, in the same units the dock uses for the machine's " +
			"total. It is a dock cell, not a per-pane figure.",
	},
	IDDockTransfers: {
		Title: "Transfers",
		Purpose: "The copy and paste buffer strip: what is held, and whether a transfer is " +
			"in flight. Clicking it opens the buffer list.",
	},
	IDDockSession: {
		Title: "Session name",
		Purpose: "The daemon session this client is showing, its accent, and whether other " +
			"clients are attached. Clicking it opens the session switcher.",
	},
	IDDockMode: {
		Title: "Mode",
		Purpose: "The input mode: window management or terminal. It is the dock's answer to " +
			"where a key will go, and it changes as the prefix menu opens.",
	},
	IDDockTrail: {
		Title: "Trail",
		Purpose: "The trail cell records where this client has been: the panes and " +
			"workspaces it visited, newest first. It is built into the dock and is not a " +
			"command runner.",
	},
	IDDockTape: {
		Title: "Tape",
		Purpose: "The tape cell reports a tape playing in this session, with its status. " +
			"It is drawn only while a tape is running.",
	},
	IDDockNotifications: {
		Title: "Notifications",
		Purpose: "The notification cell carries the newest message and its level. Its " +
			"duration and whether errors stay until dismissed are notification settings.",
	},
	IDDockCopyHelp: {
		Title: "Copy help",
		Purpose: "The copy-help cell is the dock's reminder of the copy-mode chord while " +
			"copy mode is available. It is documentation, not a control.",
	},
}

// DockEntry returns the text of a dock cell or of the bar itself. A cell with
// no entry of its own is explained by the caller's fallback.
func DockEntry(id string) (Entry, bool) {
	e, ok := dock[id]
	return e, ok
}
