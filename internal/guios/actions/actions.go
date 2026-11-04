// Package actions names what kero's app does, for its menus, its command
// palette, the shortcuts the user rebinds in Settings, and `kero do`,
// which runs them from a shell or an agent (Remote Control).
package actions

import (
	"runtime"
	"strings"
)

// Action is something the app does.
type Action struct {
	ID    string
	Title string
	Menu  string // the menu it shows in
	Keys  string // the default accelerator, as "CmdOrCtrl+Shift+D"; "" none
	// Remote tells that `kero do` may run it: what needs no dialog or
	// focus of the user's.
	Remote bool
	// Global tells that its shortcut works in every app, as the system's
	// global shortcut, rather than as a key of the app's menus.
	Global bool
}

// All are the app's actions, in their menus' order.
var All = []Action{
	{ID: "settings.open", Title: "Settings…", Menu: "Kero", Keys: "CmdOrCtrl+,", Remote: true},
	{ID: "cli.install", Title: "Install command line tool…", Menu: "Kero"},
	{ID: "app.quitAll", Title: "Quit and end all sessions…", Menu: "Kero", Keys: "CmdOrCtrl+Alt+Q"},

	{ID: "session.new", Title: "New session", Menu: "Session", Keys: "CmdOrCtrl+T", Remote: true},
	{ID: "session.newHome", Title: "New session in home", Menu: "Session", Keys: "CmdOrCtrl+Shift+T", Remote: true},
	{ID: "session.newWorktree", Title: "New session in worktree…", Menu: "Session", Keys: "CmdOrCtrl+Alt+T"},
	{ID: "project.open", Title: "Open project…", Menu: "Session", Keys: "CmdOrCtrl+O"},
	{ID: "palette.open", Title: "Go to session…", Menu: "Session", Keys: "CmdOrCtrl+P", Remote: true},
	{ID: "conversation.resume", Title: "Resume conversation…", Menu: "Session", Keys: "CmdOrCtrl+Shift+O", Remote: true},
	{ID: "session.attention", Title: "Jump to session needing you", Menu: "Session", Keys: "CmdOrCtrl+J", Remote: true},
	{ID: "agents.toggle", Title: "Agents", Menu: "Session", Keys: "CmdOrCtrl+Shift+A", Remote: true},
	{ID: "agents.new", Title: "New agent…", Menu: "Session", Keys: "CmdOrCtrl+Shift+N"},
	{ID: "session.next", Title: "Next session", Menu: "Session", Keys: "CmdOrCtrl+Shift+]", Remote: true},
	{ID: "session.previous", Title: "Previous session", Menu: "Session", Keys: "CmdOrCtrl+Shift+[", Remote: true},
	{ID: "session.rename", Title: "Rename session", Menu: "Session", Keys: "CmdOrCtrl+R"},
	{ID: "pane.restart", Title: "Restart pane", Menu: "Session", Keys: "CmdOrCtrl+Shift+R", Remote: true},
	{ID: "pane.close", Title: "Close", Menu: "Session", Keys: "CmdOrCtrl+W"},
	{ID: "session.close", Title: "Close session", Menu: "Session", Keys: "CmdOrCtrl+Shift+W"},

	{ID: "find.open", Title: "Find…", Menu: "Edit", Keys: "CmdOrCtrl+F"},
	{ID: "find.next", Title: "Find next", Menu: "Edit", Keys: "CmdOrCtrl+G"},
	{ID: "find.previous", Title: "Find previous", Menu: "Edit", Keys: "CmdOrCtrl+Shift+G"},

	{ID: "pane.new", Title: "New pane", Menu: "Pane", Keys: "CmdOrCtrl+N", Remote: true},
	{ID: "pane.split.right", Title: "Split right", Menu: "Pane", Keys: "CmdOrCtrl+D", Remote: true},
	{ID: "pane.split.down", Title: "Split down", Menu: "Pane", Keys: "CmdOrCtrl+Shift+D", Remote: true},
	{ID: "pane.browser", Title: "New browser pane", Menu: "Pane", Keys: "CmdOrCtrl+Shift+B", Remote: true},
	{ID: "browser.address", Title: "Open location…", Menu: "Pane", Keys: "CmdOrCtrl+L"},
	{ID: "pane.zoom", Title: "Zoom pane", Menu: "Pane", Keys: "CmdOrCtrl+Shift+Enter", Remote: true},
	{ID: "pane.next", Title: "Next pane", Menu: "Pane", Keys: "CmdOrCtrl+]", Remote: true},
	{ID: "pane.previous", Title: "Previous pane", Menu: "Pane", Keys: "CmdOrCtrl+[", Remote: true},
	{ID: "pane.left", Title: "Pane left", Menu: "Pane", Keys: "CmdOrCtrl+Alt+Left", Remote: true},
	{ID: "pane.right", Title: "Pane right", Menu: "Pane", Keys: "CmdOrCtrl+Alt+Right", Remote: true},
	{ID: "pane.up", Title: "Pane above", Menu: "Pane", Keys: "CmdOrCtrl+Alt+Up", Remote: true},
	{ID: "pane.down", Title: "Pane below", Menu: "Pane", Keys: "CmdOrCtrl+Alt+Down", Remote: true},

	{ID: "layout.bsp", Title: "Split tree layout", Menu: "Layout", Remote: true},
	{ID: "layout.master", Title: "Master and stack layout", Menu: "Layout", Remote: true},
	{ID: "layout.scrolling", Title: "Scrolling layout", Menu: "Layout", Remote: true},
	{ID: "layout.floating", Title: "Floating layout", Menu: "Layout", Remote: true},
	{ID: "layout.next", Title: "Next layout", Menu: "Layout", Remote: true},
	{ID: "layout.tiling", Title: "Toggle tiling", Menu: "Layout", Remote: true},
	{ID: "layout.rotate", Title: "Rotate split", Menu: "Layout", Remote: true},
	{ID: "layout.equalize", Title: "Even out splits", Menu: "Layout", Remote: true},
	{ID: "layout.float", Title: "Float pane", Menu: "Layout", Remote: true},
	{ID: "layout.master.position", Title: "Move masters", Menu: "Layout", Remote: true},
	{ID: "layout.master.add", Title: "Add a master", Menu: "Layout", Remote: true},
	{ID: "layout.master.remove", Title: "Remove a master", Menu: "Layout", Remote: true},
	{ID: "layout.master.swap", Title: "Swap with master", Menu: "Layout", Remote: true},
	{ID: "layout.master.focus", Title: "Focus master", Menu: "Layout", Remote: true},
	{ID: "layout.scroll.width", Title: "Next column width", Menu: "Layout", Remote: true},
	{ID: "layout.scroll.max", Title: "Widest column", Menu: "Layout", Remote: true},
	{ID: "layout.scroll.consume", Title: "Join next column", Menu: "Layout", Remote: true},
	{ID: "layout.scroll.expel", Title: "Split off column", Menu: "Layout", Remote: true},
	{ID: "layout.scroll.moveLeft", Title: "Move column left", Menu: "Layout", Remote: true},
	{ID: "layout.scroll.moveRight", Title: "Move column right", Menu: "Layout", Remote: true},
	{ID: "layout.snap.left", Title: "Snap left", Menu: "Layout", Remote: true},
	{ID: "layout.snap.right", Title: "Snap right", Menu: "Layout", Remote: true},
	{ID: "layout.snap.full", Title: "Snap to fill", Menu: "Layout", Remote: true},
	{ID: "layout.snap.unsnap", Title: "Unsnap", Menu: "Layout", Remote: true},

	{ID: "workspace.next", Title: "Next workspace", Menu: "Workspace", Keys: "CmdOrCtrl+Alt+]", Remote: true},
	{ID: "workspace.previous", Title: "Previous workspace", Menu: "Workspace", Keys: "CmdOrCtrl+Alt+[", Remote: true},
	{ID: "workspace.new", Title: "New workspace", Menu: "Workspace", Remote: true},
	{ID: "workspace.1", Title: "Go to workspace 1", Menu: "Workspace", Keys: "Ctrl+1", Remote: true},
	{ID: "workspace.2", Title: "Go to workspace 2", Menu: "Workspace", Keys: "Ctrl+2", Remote: true},
	{ID: "workspace.3", Title: "Go to workspace 3", Menu: "Workspace", Keys: "Ctrl+3", Remote: true},
	{ID: "workspace.4", Title: "Go to workspace 4", Menu: "Workspace", Keys: "Ctrl+4", Remote: true},
	{ID: "workspace.5", Title: "Go to workspace 5", Menu: "Workspace", Keys: "Ctrl+5", Remote: true},
	{ID: "workspace.6", Title: "Go to workspace 6", Menu: "Workspace", Keys: "Ctrl+6", Remote: true},
	{ID: "workspace.7", Title: "Go to workspace 7", Menu: "Workspace", Keys: "Ctrl+7", Remote: true},
	{ID: "workspace.8", Title: "Go to workspace 8", Menu: "Workspace", Keys: "Ctrl+8", Remote: true},
	{ID: "workspace.9", Title: "Go to workspace 9", Menu: "Workspace", Keys: "Ctrl+9", Remote: true},
	{ID: "pane.workspace.1", Title: "Move pane to workspace 1", Menu: "Workspace", Keys: "Ctrl+Shift+1", Remote: true},
	{ID: "pane.workspace.2", Title: "Move pane to workspace 2", Menu: "Workspace", Keys: "Ctrl+Shift+2", Remote: true},
	{ID: "pane.workspace.3", Title: "Move pane to workspace 3", Menu: "Workspace", Keys: "Ctrl+Shift+3", Remote: true},
	{ID: "pane.workspace.4", Title: "Move pane to workspace 4", Menu: "Workspace", Keys: "Ctrl+Shift+4", Remote: true},
	{ID: "pane.workspace.5", Title: "Move pane to workspace 5", Menu: "Workspace", Keys: "Ctrl+Shift+5", Remote: true},
	{ID: "pane.workspace.6", Title: "Move pane to workspace 6", Menu: "Workspace", Keys: "Ctrl+Shift+6", Remote: true},
	{ID: "pane.workspace.7", Title: "Move pane to workspace 7", Menu: "Workspace", Keys: "Ctrl+Shift+7", Remote: true},
	{ID: "pane.workspace.8", Title: "Move pane to workspace 8", Menu: "Workspace", Keys: "Ctrl+Shift+8", Remote: true},
	{ID: "pane.workspace.9", Title: "Move pane to workspace 9", Menu: "Workspace", Keys: "Ctrl+Shift+9", Remote: true},

	{ID: "sidebar.toggle", Title: "Toggle sidebar", Menu: "View", Keys: "CmdOrCtrl+B", Remote: true},
	{ID: "changes.toggle", Title: "Toggle changes", Menu: "View", Keys: "CmdOrCtrl+Alt+G", Remote: true},
	{ID: "files.toggle", Title: "Toggle files", Menu: "View", Keys: "CmdOrCtrl+Alt+E", Remote: true},
	{ID: "rightSidebar.toggle", Title: "Toggle right sidebar", Menu: "View", Keys: "CmdOrCtrl+Alt+B", Remote: true},
	{ID: "text.bigger", Title: "Bigger", Menu: "View", Keys: "CmdOrCtrl+=", Remote: true},
	{ID: "text.smaller", Title: "Smaller", Menu: "View", Keys: "CmdOrCtrl+-", Remote: true},
	{ID: "text.reset", Title: "Actual size", Menu: "View", Keys: "CmdOrCtrl+0", Remote: true},
	// No shortcut until the user gives it one: a global shortcut takes its
	// keys from every other app, as Control+` from VS Code's terminal.
	{ID: "quickTerminal.toggle", Title: "Quick terminal", Menu: "View", Remote: true, Global: true},
}

// pcKeys are the defaults on Windows and Linux, where Control is the
// terminal's: Control+T, D, W or R belong to the shell, so the shortcuts add
// Shift, as Windows Terminal's do, or Alt; "" leaves an action without one.
var pcKeys = map[string]string{
	"settings.open":       "Ctrl+,",
	"app.quitAll":         "",
	"session.new":         "Ctrl+Shift+T",
	"session.newHome":     "",
	"session.newWorktree": "",
	"project.open":        "Ctrl+Shift+O",
	"palette.open":        "Ctrl+Shift+P",
	"conversation.resume": "",
	"session.attention":   "Ctrl+Shift+J",
	"agents.toggle":       "Ctrl+Shift+A",
	"agents.new":          "Ctrl+Shift+N",
	"session.next":        "Ctrl+PageDown",
	"session.previous":    "Ctrl+PageUp",
	"session.rename":      "",
	"pane.restart":        "Ctrl+Shift+R",
	"pane.close":          "Ctrl+Shift+W",
	"session.close":       "",
	"find.open":           "Ctrl+Shift+F",
	"find.next":           "",
	"find.previous":       "",
	"pane.new":            "Alt+Shift+N",
	"pane.split.right":    "Alt+Shift+=",
	"pane.split.down":     "Alt+Shift+-",
	"pane.browser":        "Alt+Shift+B",
	"browser.address":     "",
	"pane.zoom":           "Ctrl+Shift+Enter",
	"pane.next":           "Ctrl+Shift+]",
	"pane.previous":       "Ctrl+Shift+[",
	"pane.left":           "Alt+Left",
	"pane.right":          "Alt+Right",
	"pane.up":             "Alt+Up",
	"pane.down":           "Alt+Down",
	"workspace.next":      "Alt+PageDown",
	"workspace.previous":  "Alt+PageUp",
	"workspace.1":         "Alt+1",
	"workspace.2":         "Alt+2",
	"workspace.3":         "Alt+3",
	"workspace.4":         "Alt+4",
	"workspace.5":         "Alt+5",
	"workspace.6":         "Alt+6",
	"workspace.7":         "Alt+7",
	"workspace.8":         "Alt+8",
	"workspace.9":         "Alt+9",
	"pane.workspace.1":    "Alt+Shift+1",
	"pane.workspace.2":    "Alt+Shift+2",
	"pane.workspace.3":    "Alt+Shift+3",
	"pane.workspace.4":    "Alt+Shift+4",
	"pane.workspace.5":    "Alt+Shift+5",
	"pane.workspace.6":    "Alt+Shift+6",
	"pane.workspace.7":    "Alt+Shift+7",
	"pane.workspace.8":    "Alt+Shift+8",
	"pane.workspace.9":    "Alt+Shift+9",
	"sidebar.toggle":      "Ctrl+Shift+B",
	"changes.toggle":      "Ctrl+Shift+G",
	"files.toggle":        "Ctrl+Shift+E",
	"rightSidebar.toggle": "",
	"text.bigger":         "Ctrl+=",
	"text.smaller":        "Ctrl+-",
	"text.reset":          "Ctrl+0",
}

func init() {
	if runtime.GOOS == "darwin" {
		return
	}
	for i, a := range All {
		if k, ok := pcKeys[a.ID]; ok {
			All[i].Keys = k
		}
	}
}

// Find returns an action by its id.
func Find(id string) (Action, bool) {
	for _, a := range All {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// Keys returns an action's accelerator: the user's, over the default.
func Keys(a Action, overrides map[string]string) string {
	if k, ok := overrides[a.ID]; ok {
		return k
	}
	return a.Keys
}

// Glyphs writes an accelerator as the system's menus show it: "⇧⌘D" on
// macOS, "Ctrl+Shift+D" elsewhere.
func Glyphs(accel string) string {
	if accel == "" {
		return ""
	}
	parts := strings.Split(accel, "+")
	key := parts[len(parts)-1]
	if key == "" && len(parts) > 1 { // "CmdOrCtrl++"
		key = "+"
	}
	if runtime.GOOS != "darwin" {
		return words(parts[:len(parts)-1], key)
	}
	var ctrl, alt, shift, cmd bool
	for _, m := range parts[:len(parts)-1] {
		switch strings.ToLower(m) {
		case "cmdorctrl", "commandorcontrol", "cmd", "command", "super", "meta":
			cmd = true
		case "ctrl", "control":
			ctrl = true
		case "alt", "option":
			alt = true
		case "shift":
			shift = true
		}
	}
	var b strings.Builder
	if ctrl {
		b.WriteString("⌃")
	}
	if alt {
		b.WriteString("⌥")
	}
	if shift {
		b.WriteString("⇧")
	}
	if cmd {
		b.WriteString("⌘")
	}
	names := map[string]string{
		"enter": "↵", "return": "↵", "tab": "⇥", "space": "Space", "backspace": "⌫", "delete": "⌦",
		"escape": "⎋", "esc": "⎋", "up": "↑", "down": "↓", "left": "←", "right": "→",
		"home": "⇱", "end": "⇲", "pageup": "⇞", "pagedown": "⇟", "plus": "+",
	}
	if n, ok := names[strings.ToLower(key)]; ok {
		b.WriteString(n)
	} else {
		b.WriteString(strings.ToUpper(key))
	}
	return b.String()
}

// words writes an accelerator as Windows and Linux menus show it:
// "Ctrl+Shift+D".
func words(mods []string, key string) string {
	var ctrl, alt, shift, super bool
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "cmdorctrl", "commandorcontrol", "ctrl", "control":
			ctrl = true
		case "cmd", "command", "super", "meta":
			super = true
		case "alt", "option":
			alt = true
		case "shift":
			shift = true
		}
	}
	var out []string
	if super && runtime.GOOS == "windows" {
		out = append(out, "Win")
	} else if super {
		out = append(out, "Super")
	}
	if ctrl {
		out = append(out, "Ctrl")
	}
	if alt {
		out = append(out, "Alt")
	}
	if shift {
		out = append(out, "Shift")
	}
	names := map[string]string{
		"enter": "Enter", "return": "Enter", "tab": "Tab", "space": "Space", "backspace": "Backspace", "delete": "Del",
		"escape": "Esc", "esc": "Esc", "up": "↑", "down": "↓", "left": "←", "right": "→",
		"home": "Home", "end": "End", "pageup": "PgUp", "pagedown": "PgDn", "plus": "+",
	}
	if n, ok := names[strings.ToLower(key)]; ok {
		key = n
	} else {
		key = strings.ToUpper(key)
	}
	return strings.Join(append(out, key), "+")
}
