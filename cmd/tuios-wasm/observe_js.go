//go:build js && wasm

package main

import (
	"strconv"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// snapshot is the slice of app state a guided tour checks steps against. It
// is taken after every Update and compared with the previous one, so the app
// itself needs no hooks: a change in any field is an event.
type snapshot struct {
	Mode          string
	Windows       int // on the current workspace, minimized included
	Minimized     int
	TotalWindows  int
	Focused       string
	FocusedTitle  string
	Workspace     int
	Tiling        bool
	Prefix        string // "", "prefix", "workspace", "window", "minimize", "layout", "debug", "tape"
	Help          bool
	CommandPal    bool
	Launcher      bool
	Settings      bool
	CopyMode      bool
	Zoomed        bool
	Theme         string
	WorkspacesUse []int
	Cols, Rows    int
}

func (s snapshot) toJS() map[string]any {
	used := make([]any, len(s.WorkspacesUse))
	for i, w := range s.WorkspacesUse {
		used[i] = w
	}
	return map[string]any{
		"mode":           s.Mode,
		"windows":        s.Windows,
		"minimized":      s.Minimized,
		"totalWindows":   s.TotalWindows,
		"focused":        s.Focused,
		"focusedTitle":   s.FocusedTitle,
		"workspace":      s.Workspace,
		"tiling":         s.Tiling,
		"prefix":         s.Prefix,
		"help":           s.Help,
		"commandPalette": s.CommandPal,
		"launcher":       s.Launcher,
		"settings":       s.Settings,
		"copyMode":       s.CopyMode,
		"zoomed":         s.Zoomed,
		"theme":          s.Theme,
		"workspacesUsed": used,
		"cols":           s.Cols,
		"rows":           s.Rows,
	}
}

func take(o *app.OS) snapshot {
	s := snapshot{
		Mode:       "window",
		Workspace:  o.CurrentWorkspace,
		Tiling:     o.AutoTiling,
		Help:       o.ShowHelp,
		CommandPal: o.ShowCommandPalette,
		Launcher:   o.ShowLauncher,
		Settings:   o.ShowSettings,
		Theme:      theme.CurrentThemeID(),
		Cols:       o.Width,
		Rows:       o.Height,
	}
	if o.Mode == app.TerminalMode {
		s.Mode = "terminal"
	}
	switch {
	case o.WorkspacePrefixActive:
		s.Prefix = "workspace"
	case o.TilingPrefixActive:
		s.Prefix = "window"
	case o.MinimizePrefixActive:
		s.Prefix = "minimize"
	case o.LayoutPrefixActive:
		s.Prefix = "layout"
	case o.DebugPrefixActive:
		s.Prefix = "debug"
	case o.TapePrefixActive:
		s.Prefix = "tape"
	case o.PrefixActive:
		s.Prefix = "prefix"
	}
	used := map[int]bool{}
	for _, w := range o.Windows {
		s.TotalWindows++
		used[w.Workspace] = true
		if w.Workspace != o.CurrentWorkspace {
			continue
		}
		s.Windows++
		if w.Minimized {
			s.Minimized++
		}
	}
	for i := 1; i <= 9; i++ {
		if used[i] {
			s.WorkspacesUse = append(s.WorkspacesUse, i)
		}
	}
	if f := o.GetFocusedWindow(); f != nil {
		s.Focused = f.ID
		s.FocusedTitle = f.CustomName
		if s.FocusedTitle == "" {
			s.FocusedTitle = f.Title()
		}
		s.CopyMode = f.CopyMode != nil
		s.Zoomed = f.Zoomed
	}
	return s
}

// observed runs the tuios model and reports what changed to the page.
type observed struct {
	*app.OS
	emit func(map[string]any)

	mu   sync.Mutex
	last snapshot
}

func newObserved(o *app.OS, emit func(map[string]any)) *observed {
	return &observed{OS: o, emit: emit, last: take(o)}
}

func (o *observed) lastState() snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last
}

// commandMsg is a request from the page, run on the program's goroutine so it
// never races Update.
type commandMsg struct {
	name string
	args []string
}

func (o *observed) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var keyEvent map[string]any
	if k, ok := msg.(tea.KeyPressMsg); ok {
		keyEvent = o.describeKey(k)
	}
	var cmd tea.Cmd
	if c, ok := msg.(commandMsg); ok {
		cmd = o.runCommand(c)
	} else {
		next, c := o.OS.Update(msg)
		if n, ok := next.(*app.OS); ok {
			o.OS = n
		}
		cmd = c
	}
	if keyEvent != nil {
		o.emit(keyEvent)
	}
	o.diff()
	return o, cmd
}

// describeKey names a key press and, best effort, the action it is bound to in
// the state the app is in before it handles the key. The real dispatcher also
// normalises some keys (see input.lookupAction); an exact answer wants a hook
// in the dispatcher, which this spike avoids.
func (o *observed) describeKey(k tea.KeyPressMsg) map[string]any {
	key := k.String()
	reg := o.OS.KeybindRegistry
	action := ""
	if reg != nil {
		switch {
		case o.OS.WorkspacePrefixActive:
			action = reg.GetWorkspacePrefixAction(key)
		case o.OS.TilingPrefixActive:
			action = reg.GetWindowPrefixAction(key)
		case o.OS.MinimizePrefixActive:
			action = reg.GetMinimizePrefixAction(key)
		case o.OS.LayoutPrefixActive:
			action = reg.GetLayoutPrefixAction(key)
		case o.OS.PrefixActive:
			action = reg.GetPrefixAction(key)
		case o.OS.Mode == app.WindowManagementMode:
			action = reg.GetAction(key)
		default:
			action = reg.GetTerminalModeAction(key)
		}
	}
	mode := "window"
	if o.OS.Mode == app.TerminalMode {
		mode = "terminal"
	}
	return map[string]any{"type": "key", "data": map[string]any{"key": key, "action": action, "mode": mode}}
}

// diff emits one event per field that changed since the last Update.
func (o *observed) diff() {
	now := take(o.OS)
	o.mu.Lock()
	prev := o.last
	o.last = now
	o.mu.Unlock()

	change := func(typ string, from, to any) {
		o.emit(map[string]any{"type": typ, "data": map[string]any{"from": from, "to": to}, "state": now.toJS()})
	}
	if prev.Mode != now.Mode {
		change("mode", prev.Mode, now.Mode)
	}
	if prev.TotalWindows < now.TotalWindows {
		change("window.open", prev.TotalWindows, now.TotalWindows)
	} else if prev.TotalWindows > now.TotalWindows {
		change("window.close", prev.TotalWindows, now.TotalWindows)
	}
	if prev.Focused != now.Focused {
		change("window.focus", prev.Focused, now.Focused)
	}
	if prev.FocusedTitle != now.FocusedTitle && prev.Focused == now.Focused && now.Focused != "" {
		change("window.rename", prev.FocusedTitle, now.FocusedTitle)
	}
	if prev.Minimized != now.Minimized {
		change("window.minimized", prev.Minimized, now.Minimized)
	}
	if prev.Zoomed != now.Zoomed {
		change("window.zoom", prev.Zoomed, now.Zoomed)
	}
	if prev.Workspace != now.Workspace {
		change("workspace", prev.Workspace, now.Workspace)
	}
	if prev.Tiling != now.Tiling {
		change("tiling", prev.Tiling, now.Tiling)
	}
	if prev.Prefix != now.Prefix {
		change("prefix", prev.Prefix, now.Prefix)
	}
	if prev.Help != now.Help {
		change("help", prev.Help, now.Help)
	}
	if prev.CommandPal != now.CommandPal {
		change("commandPalette", prev.CommandPal, now.CommandPal)
	}
	if prev.Launcher != now.Launcher {
		change("launcher", prev.Launcher, now.Launcher)
	}
	if prev.Settings != now.Settings {
		change("settings", prev.Settings, now.Settings)
	}
	if prev.CopyMode != now.CopyMode {
		change("copyMode", prev.CopyMode, now.CopyMode)
	}
	if prev.Theme != now.Theme {
		change("theme", prev.Theme, now.Theme)
	}
}

// runCommand lets the page set the scene for a step: open a window, reset
// tiling, show a hint. It uses the same methods tape scripts drive.
func (o *observed) runCommand(c commandMsg) tea.Cmd {
	arg := func(i int) string {
		if i < len(c.args) {
			return c.args[i]
		}
		return ""
	}
	switch c.name {
	case "notify":
		o.OS.ShowNotification(arg(0), "info", 3*time.Second)
	case "newWindow":
		_ = o.OS.CreateNewWindowWithName(arg(0))
	case "tiling":
		if arg(0) == "on" {
			_ = o.OS.EnableTiling()
		} else {
			_ = o.OS.DisableTiling()
		}
	case "workspace":
		if n, err := strconv.Atoi(arg(0)); err == nil {
			_ = o.OS.SwitchWorkspace(n)
		}
	case "mode":
		_ = o.OS.SetMode(arg(0))
	case "theme":
		_ = o.OS.SetTheme(arg(0))
	case "type":
		// Types into the focused pane, for a demo that plays itself.
		if id := o.OS.GetFocusedWindowID(); id != "" {
			_ = o.OS.SendToWindow(id, []byte(arg(0)))
		}
	}
	return nil
}
