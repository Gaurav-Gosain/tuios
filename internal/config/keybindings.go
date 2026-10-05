package config

// Keybinding is one line of a which-key panel: a key and what it does.
type Keybinding struct {
	Key         string
	Description string
	// Submenu marks a key that opens another prefix menu rather than doing
	// something itself. The panel draws it with a leading + so nested menus
	// can be told from actions at a glance.
	Submenu bool
}

// KeybindingGroup is a titled section of a which-key panel. A panel with one
// untitled group is a plain list.
type KeybindingGroup struct {
	Title    string
	Bindings []Keybinding
}

func kb(key, desc string) Keybinding  { return Keybinding{Key: key, Description: desc} }
func sub(key, desc string) Keybinding { return Keybinding{Key: key, Description: desc, Submenu: true} }

// The prefix menu's agent lines. Named so IsAgentPrefixKeybinding can find
// them by what they say rather than by a key a config may have moved.
const (
	whichKeyInbox         = "Inbox"
	whichKeyOldestWaiting = "Oldest waiting"
	whichKeyInboxMail     = "Inbox: mail"
	whichKeyReview        = "Review changes"
	whichKeyNewestDone    = "Newest finished"
)

// IsAgentPrefixKeybinding reports whether a prefix menu line is one that only
// means something to a person running agents: the Inbox, the oldest waiting
// item, the Inbox on its mail, reviewing a pane's changes and the newest
// finished turn. The client leaves them out of the menu until an agent has
// been seen; the keys work either way.
func IsAgentPrefixKeybinding(k Keybinding) bool {
	switch k.Description {
	case whichKeyInbox, whichKeyOldestWaiting, whichKeyInboxMail, whichKeyReview, whichKeyNewestDone:
		return true
	}
	return false
}

// IsReviewPrefixKeybinding reports whether a prefix menu line is the review
// of the focused pane. The client leaves it out of the menu on a daemon that
// cannot review.
func IsReviewPrefixKeybinding(k Keybinding) bool {
	return k.Description == whichKeyReview
}

// WhichKeyCopyPasteKey is the leader menu's line for copy mode ([) and the
// image paste (V).
const WhichKeyCopyPasteKey = "[/V"

// GetPrefixKeybindings returns keybindings for the prefix overlay, every group
// in order. isDaemonSession indicates whether we're running in daemon mode
// (affects detach/quit descriptions).
func GetPrefixKeybindings(prefixType string, isDaemonSession ...bool) []Keybinding {
	var out []Keybinding
	for _, g := range GetPrefixKeybindingGroups(prefixType, isDaemonSession...) {
		out = append(out, g.Bindings...)
	}
	return out
}

// GetPrefixKeybindingGroups returns the prefix overlay's lines in the sections
// the panel draws them under. The sub-prefixes are one untitled group each:
// they are a handful of lines and a heading over them would say what the
// panel's title already does.
func GetPrefixKeybindingGroups(prefixType string, isDaemonSession ...bool) []KeybindingGroup {
	daemonMode := len(isDaemonSession) > 0 && isDaemonSession[0]
	one := func(b ...Keybinding) []KeybindingGroup { return []KeybindingGroup{{Bindings: b}} }
	switch prefixType {
	case "workspace":
		return one(
			kb("1-9", "Switch to workspace"),
			kb("Shift+1-9", "Move window to workspace"),
			kb("r", "Rename workspace"),
			kb("Esc", "Cancel"),
		)
	case "minimize":
		return one(
			kb("m", "Minimize focused window"),
			kb("1-9", "Restore window"),
			kb("Shift+M", "Restore all"),
			kb("Esc", "Cancel"),
		)
	case "window":
		return one(
			kb("n", "New window"),
			kb("x", "Close window"),
			kb("r", "Rename window"),
			kb("Tab", "Next window"),
			kb("Shift+Tab", "Previous window"),
			kb("t", "Toggle tiling mode"),
			kb("Esc", "Cancel"),
		)
	case "debug":
		return one(
			kb("l", "Toggle log viewer"),
			kb("c", "Toggle cache statistics"),
			kb("k", "Toggle showkeys overlay"),
			kb("a", "Toggle animations"),
			kb("Esc", "Cancel"),
		)
	case "tape":
		return one(
			kb("m", "Open tape manager"),
			kb("t", "Review project tape"),
			kb("r", "Start recording"),
			kb("s", "Stop recording"),
			kb("Esc", "Cancel"),
		)
	case "layout":
		return one(
			kb("l", "Load layout"),
			kb("s", "Save layout"),
			kb("1-4", "Snap window to a corner"),
			kb("5-9", "Resize focused window width (%)"),
			kb("Shift+5-9", "Resize focused window height (%)"),
			kb("o", "Move master to next side"),
			kb("Enter", "Swap with master"),
			kb("m", "Focus master"),
			kb("i/d", "Add/remove master"),
			kb("Esc", "Cancel"),
		)
	}

	// The leader's menu, in the sections the panel flows into columns. Each
	// section is a few lines, so a column holds a section or two whole and the
	// panel stays short enough to read without scrolling the eye down a list.
	windows := KeybindingGroup{Title: "Windows", Bindings: []Keybinding{
		kb("c", "Create window"),
		kb("x", "Close window"),
		kb("r", "Rename window"),
		kb("n/p", "Next/prev window"),
		kb("0-9", "Jump to window"),
		kb("z", "Toggle zoom"),
	}}
	panes := KeybindingGroup{Title: "Panes", Bindings: []Keybinding{
		// The arrows walk panes, and the prefix stays armed for a moment
		// so a run of them costs one prefix press. See the repeat window
		// in internal/input/prefix_repeat.go.
		kb("←↑↓→", "Focus pane"),
		kb("space", "Toggle tiling"),
		kb("-", "Split horizontal"),
		kb("|/\\", "Split vertical"),
		kb("R", "Rotate split"),
		kb("=", "Equalize splits"),
		// Hints label the focused pane's text. The row is here rather than
		// under Tools because Tools shares a column with Menus in the narrow
		// layout, and one row more there made the panel the tallest column
		// at 80x24, where it reached the pane's bottom border.
		kb("F", "Hints"),
	}}
	sessions := KeybindingGroup{Title: "Sessions", Bindings: []Keybinding{
		kb("(/)", "Prev/next session"),
		kb("S", "Sessions"),
		kb("W", "Workspaces"),
		kb("X", "Close session"),
		kb("g", "Scratch session"),
	}}
	modes := KeybindingGroup{Title: "Modes"}
	// In daemon mode d detaches and Esc leaves for window mode; in local mode
	// both leave for window mode.
	if daemonMode {
		sessions.Bindings = append(sessions.Bindings, kb("d", "Detach session"), kb("q", "Quit menu"))
		modes.Bindings = append(modes.Bindings, kb("Esc", "Window mode"))
	} else {
		sessions.Bindings = append(sessions.Bindings, kb("q", "Quit application"))
		modes.Bindings = append(modes.Bindings, kb("d/Esc", "Window mode"))
	}
	modes.Bindings = append(modes.Bindings,
		// Copy mode and the image paste share a line: the leader's menu
		// fills an 80x24 screen, and a line more pushes it off the bottom.
		kb(WhichKeyCopyPasteKey, "Copy mode/paste image"),
		kb("s", "Scrollback browser"),
		kb("b", "Toggle sidebar"),
		kb("e", "Focus sidebar"),
		kb("f", "Search files"),
	)
	menus := KeybindingGroup{Title: "Menus", Bindings: []Keybinding{
		sub("w", "Workspace"),
		sub("m", "Minimize"),
		sub("t", "Window"),
		sub("L", "Layout"),
		sub("T", "Tape"),
		sub("D", "Debug"),
	}}
	tools := KeybindingGroup{Title: "Tools", Bindings: []Keybinding{
		kb("P", "Command palette"),
		kb("a", "Launcher"),
		kb(",", "Settings"),
		kb("k", "Keybindings"),
		kb("C", "Screenshot"),
		kb("j", "Newest message"),
		kb("u", "Back to before the last jump"),
		kb("l", "Jump-link demo pane"),
		kb("?", "Help"),
	}}
	agents := KeybindingGroup{Title: "Agents", Bindings: []Keybinding{
		kb("i", whichKeyInbox),
		kb("o", whichKeyOldestWaiting),
		kb("M", whichKeyInboxMail),
		kb("O", whichKeyNewestDone),
		kb("v", whichKeyReview),
	}}
	return []KeybindingGroup{windows, panes, sessions, modes, menus, tools, agents}
}
