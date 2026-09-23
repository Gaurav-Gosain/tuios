package learn

import (
	"sort"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Overlay names, as the page sees them in overlay.open and overlay.close and
// in the snapshot's overlays list.
const (
	OverlayHelp              = "help"
	OverlayWhichKey          = "whichkey"
	OverlayCommandPalette    = "commandPalette"
	OverlayLauncher          = "launcher"
	OverlaySettings          = "settings"
	OverlayThemePicker       = "themePicker"
	OverlayCopyMode          = "copyMode"
	OverlaySearch            = "search"
	OverlayScrollback        = "scrollback"
	OverlayKeybinds          = "keybinds"
	OverlayQuitMenu          = "quitMenu"
	OverlayWorkspaceSwitcher = "workspaceSwitcher"
	OverlayLayoutPicker      = "layoutPicker"
	OverlaySidebar           = "sidebar"
	OverlayLogs              = "logs"
)

// windowSnap is one window as the page sees it.
type windowSnap struct {
	ID        string
	Title     string
	Workspace int
	X, Y      int
	W, H      int
	Minimized bool
	Zoomed    bool
	Agent     string
	AgentNote string
	AgentKind string
	Harness   string
}

// Snapshot is the part of the app state a lesson checks steps against. It is
// taken after every Update and compared with the one before, so a change in
// any field is an event, whatever caused it: a key, the mouse, the palette,
// a tape or a command from the page.
type Snapshot struct {
	Mode       string // "window" or "terminal"
	Workspace  int
	Tiling     bool
	Layout     string // "bsp", "master-stack" or "scrolling"
	Prefix     string // "", "prefix", "workspace", "window", "minimize", "layout", "debug", "tape"
	Overlays   map[string]bool
	Theme      string
	Focused    string
	Tape       bool
	Cols, Rows int
	Windows    []windowSnap // every workspace, in the app's order
}

func take(o *app.OS) Snapshot {
	s := Snapshot{
		Mode:      "window",
		Workspace: o.CurrentWorkspace,
		Tiling:    o.AutoTiling,
		Layout:    o.LayoutModeName(),
		Theme:     theme.CurrentThemeID(),
		Focused:   o.GetFocusedWindowID(),
		Tape:      o.ScriptMode && o.ScriptFinishedTime.IsZero(),
		Cols:      o.Width,
		Rows:      o.Height,
		Overlays:  map[string]bool{},
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

	ov := s.Overlays
	ov[OverlayHelp] = o.ShowHelp
	ov[OverlayWhichKey] = o.PrefixActive && !o.ShowHelp && o.Settings.WhichKeyEnabled &&
		time.Since(o.LastPrefixTime) > config.WhichKeyDelay
	ov[OverlayCommandPalette] = o.ShowCommandPalette
	ov[OverlayLauncher] = o.ShowLauncher
	ov[OverlaySettings] = o.ShowSettings
	ov[OverlayThemePicker] = o.ShowThemePicker
	ov[OverlayKeybinds] = o.ShowKeybindManager
	ov[OverlayQuitMenu] = o.ShowQuitMenu
	ov[OverlayWorkspaceSwitcher] = o.ShowWorkspaceSwitcher
	ov[OverlayLayoutPicker] = o.ShowLayoutPicker
	ov[OverlaySidebar] = o.SidebarActive()
	ov[OverlayLogs] = o.ShowLogs
	ov[OverlayScrollback] = o.ShowScrollbackBrowser
	if b, ok := o.ScrollbackBrowser.(*scrollback.Browser); ok && o.ShowScrollbackBrowser && b != nil && b.SearchActive {
		ov[OverlaySearch] = true
	}
	if f := o.GetFocusedWindow(); f != nil && f.CopyMode != nil {
		ov[OverlayCopyMode] = true
		if f.CopyMode.State == terminal.CopyModeSearch {
			ov[OverlaySearch] = true
		}
	}

	for _, w := range o.Windows {
		if w == nil {
			continue
		}
		title := w.CustomName
		if title == "" {
			title = w.Title()
		}
		s.Windows = append(s.Windows, windowSnap{
			ID: w.ID, Title: title, Workspace: w.Workspace,
			X: w.X, Y: w.Y, W: w.Width, H: w.Height,
			Minimized: w.Minimized, Zoomed: w.Zoomed,
			Agent: w.AgentState, AgentNote: w.AgentMessage, AgentKind: w.AgentKind, Harness: w.AgentHarness,
		})
	}
	return s
}

func (s Snapshot) window(id string) (windowSnap, bool) {
	for _, w := range s.Windows {
		if w.ID == id {
			return w, true
		}
	}
	return windowSnap{}, false
}

// ToMap is the snapshot as the page reads it from tuios.state() and from the
// state field of an event.
func (s Snapshot) ToMap() map[string]any {
	var overlays []any
	names := make([]string, 0, len(s.Overlays))
	for name, on := range s.Overlays {
		if on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		overlays = append(overlays, n)
	}
	var wins []any
	used := map[int]bool{}
	onWorkspace, minimized := 0, 0
	focusedTitle, zoomed := "", false
	for _, w := range s.Windows {
		used[w.Workspace] = true
		if w.Workspace == s.Workspace {
			onWorkspace++
			if w.Minimized {
				minimized++
			}
		}
		if w.ID == s.Focused {
			focusedTitle, zoomed = w.Title, w.Zoomed
		}
		wins = append(wins, map[string]any{
			"id": w.ID, "title": w.Title, "workspace": w.Workspace,
			"x": w.X, "y": w.Y, "width": w.W, "height": w.H,
			"minimized": w.Minimized, "zoomed": w.Zoomed,
			"agent": w.Agent, "agentMessage": w.AgentNote,
		})
	}
	var usedList []any
	for i := 1; i <= 9; i++ {
		if used[i] {
			usedList = append(usedList, i)
		}
	}
	return map[string]any{
		"mode":           s.Mode,
		"workspace":      s.Workspace,
		"workspacesUsed": usedList,
		"tiling":         s.Tiling,
		"layout":         s.Layout,
		"prefix":         s.Prefix,
		"overlays":       overlays,
		"theme":          s.Theme,
		"focused":        s.Focused,
		"focusedTitle":   focusedTitle,
		"zoomed":         zoomed,
		"windows":        onWorkspace,
		"minimized":      minimized,
		"totalWindows":   len(s.Windows),
		"windowList":     wins,
		"tape":           s.Tape,
		"cols":           s.Cols,
		"rows":           s.Rows,
	}
}

// diff lists the events that take prev to now, in a fixed order.
func diff(prev, now Snapshot) []Event {
	var out []Event
	add := func(typ, windowID string, data map[string]any) {
		out = append(out, Event{Type: typ, WindowID: windowID, Data: data})
	}
	if prev.Mode != now.Mode {
		add(EventMode, "", map[string]any{"from": prev.Mode, "to": now.Mode})
	}
	if prev.Prefix != now.Prefix {
		add(EventPrefix, "", map[string]any{"from": prev.Prefix, "to": now.Prefix})
	}

	for _, w := range now.Windows {
		if _, ok := prev.window(w.ID); !ok {
			add(EventWindowOpen, w.ID, map[string]any{"id": w.ID, "title": w.Title, "workspace": w.Workspace, "count": len(now.Windows)})
		}
	}
	for _, w := range prev.Windows {
		if _, ok := now.window(w.ID); !ok {
			add(EventWindowClose, w.ID, map[string]any{"id": w.ID, "title": w.Title, "count": len(now.Windows)})
		}
	}
	if prev.Focused != now.Focused {
		nw, _ := now.window(now.Focused)
		add(EventWindowFocus, now.Focused, map[string]any{"from": prev.Focused, "to": now.Focused, "title": nw.Title})
	}
	for _, w := range now.Windows {
		p, ok := prev.window(w.ID)
		if !ok {
			continue
		}
		if p.Title != w.Title {
			add(EventWindowRename, w.ID, map[string]any{"id": w.ID, "from": p.Title, "to": w.Title})
		}
		if p.Minimized != w.Minimized {
			add(EventWindowMinimize, w.ID, map[string]any{"id": w.ID, "minimized": w.Minimized})
		}
		if p.Zoomed != w.Zoomed {
			add(EventWindowZoom, w.ID, map[string]any{"id": w.ID, "zoomed": w.Zoomed})
		}
		if p.Agent != w.Agent {
			add(EventAgent, w.ID, map[string]any{
				"id": w.ID, "from": p.Agent, "to": w.Agent,
				"message": w.AgentNote, "kind": w.AgentKind, "harness": w.Harness,
			})
		}
	}

	if prev.Workspace != now.Workspace {
		add(EventWorkspace, "", map[string]any{"from": prev.Workspace, "to": now.Workspace})
	}
	if prev.Tiling != now.Tiling {
		add(EventTiling, "", map[string]any{"from": prev.Tiling, "to": now.Tiling})
	}
	if prev.Layout != now.Layout {
		add(EventLayout, "", map[string]any{"from": prev.Layout, "to": now.Layout})
	}
	if prev.Theme != now.Theme {
		add(EventTheme, "", map[string]any{"from": prev.Theme, "to": now.Theme})
	}

	names := make([]string, 0, len(now.Overlays))
	for name := range now.Overlays {
		names = append(names, name)
	}
	for name := range prev.Overlays {
		if _, ok := now.Overlays[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		was, is := prev.Overlays[name], now.Overlays[name]
		switch {
		case is && !was:
			add(EventOverlayOpen, "", map[string]any{"name": name})
		case was && !is:
			add(EventOverlayClose, "", map[string]any{"name": name})
		}
	}

	if prev.Tape != now.Tape {
		if now.Tape {
			add(EventTapeStart, "", map[string]any{})
		} else {
			add(EventTapeFinish, "", map[string]any{})
		}
	}
	return out
}
