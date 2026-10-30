package learn

import (
	"sort"
	"strconv"
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
	OverlayScreensaver       = "screensaver"
	OverlayAgents            = "agents"
	OverlayMail              = "mail"
	OverlayPresets           = "presets"
	OverlayGoto              = "goto"
	OverlayHints             = "hints"
	OverlaySpotlight         = "spotlight"
	OverlayScratch           = "scratch"
	OverlayPaneLabels        = "paneLabels"
	OverlayPeek              = "peek"
	OverlayNewAgent          = "newAgent"
	OverlayCompare           = "compare"
	OverlayReply             = "reply"
	OverlayRailFocus         = "railFocus"
	OverlayFileSearch        = "fileSearch"
	OverlayReview            = "review"
	OverlayWorktree          = "worktree"
	OverlayThemeEditor       = "themeEditor"
	OverlayGlyphPicker       = "glyphPicker"
)

// Setting names in the setting event: the looks the settings page changes
// that a lesson checks. The looks in lookPaths are named by their config path.
const (
	SettingGlyphs      = "glyphs"
	SettingBorderStyle = "borderStyle"
	// SettingShowKeys is the showkeys overlay (ctrl+b D k), "true" or
	// "false".
	SettingShowKeys = "showkeys"
)

// lookPaths are the config options a lesson can check for a change, beyond
// the glyph set and the border style: the looks the settings page and a look
// change. Each one is a setting event named by its path when its value moves.
// Reading one walks the config by reflection, so the list stays short and is
// read only after input (see Model.looksDue).
var lookPaths = [...]string{
	"appearance.window_title_style",
	"appearance.window_title_position",
	"appearance.window_title_align",
	"appearance.border_focused_color",
	"appearance.border_focused_color_window",
	"appearance.border_focused_color_terminal",
	"appearance.border_unfocused_color",
	"appearance.theme_light",
	"appearance.frame_panels",
	"appearance.popup_dim",
	"appearance.modal_dim",
	"appearance.gap",
	"appearance.sidebar.row_style",
	"appearance.sidebar.header_style",
	"appearance.dock_pill_caps",
	"appearance.dock_mode_pill",
}

// looks holds the value of each of lookPaths, in that order. An array, so a
// snapshot still compares and copies as a value.
type looks [len(lookPaths)]string

// readLooks reads lookPaths from the config, each as the value in force: an
// enum left empty reads as its default, as the settings page shows it.
func readLooks(cfg *config.UserConfig) looks {
	var out looks
	if cfg == nil {
		return out
	}
	for i, path := range lookPaths {
		opt, _ := config.LookupOption(path)
		v, _ := config.GetOptionValue(cfg, path)
		if v == "" && len(opt.Accepted) > 0 {
			v = opt.Default
			if v == "" {
				v = opt.Accepted[0]
			}
		}
		out[i] = v
	}
	return out
}

// keysSnap is the keymap in use: the preset, the leader and lock mode.
type keysSnap struct {
	Preset string // "tuios", "tmux", "screen" or "zellij"
	Leader string // such as "ctrl+b"
	Lock   bool   // keybindings.lock: every key but the leader goes to the pane
}

func (k keysSnap) toMap() map[string]any {
	return map[string]any{"preset": k.Preset, "leader": k.Leader, "lock": k.Lock}
}

// agentsSnap is the agents view's cursor: the pane of the agent under it and
// the focus tab, both "" while the view is closed.
type agentsSnap struct {
	Selected string
	Tab      string // "thread", "changes", "checkpoints", "artifacts" or "pane"
}

func (a agentsSnap) toMap() map[string]any {
	return map[string]any{"selected": a.Selected, "tab": a.Tab}
}

// filesSnap is the rail's files section: the folder it lists and the path
// of the row under the rail's cursor, "" for none.
type filesSnap struct{ Dir, Cursor string }

func (f filesSnap) toMap() map[string]any {
	return map[string]any{"dir": f.Dir, "cursor": f.Cursor}
}

// reviewSnap is the review overlay: the file it shows and the hunk the
// cursor is in, -1 for none.
type reviewSnap struct {
	File string
	Hunk int
}

func (r reviewSnap) toMap() map[string]any {
	return map[string]any{"file": r.File, "hunk": r.Hunk}
}

// rect is a window's place and size in cells.
type rect struct{ X, Y, W, H int }

func (r rect) toMap() map[string]any {
	return map[string]any{"x": r.X, "y": r.Y, "width": r.W, "height": r.H}
}

// windowSnap is one window as the page sees it.
type windowSnap struct {
	ID        string
	Title     string
	Workspace int
	X, Y      int
	W, H      int
	Minimized bool
	Zoomed    bool
	Floating  bool
	Agent     string
	AgentNote string
	AgentKind string
	Harness   string
	// Kind is "tml" for a window that draws a TML surface, "" for a pane
	// with a program in it. Popup says the window floats as a popup.
	Kind  string
	Popup bool
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
	Glyphs     string       // the glyph set, "default" for the shipped one
	Border     string       // the border style, such as "rounded"
	ShowKeys   bool         // the showkeys overlay is on
	Keys       keysSnap
	PiP        string // the window picture-in-picture shows, "" for none
	// Agents is the agents view's cursor while it is open.
	Agents agentsSnap
	Files  filesSnap
	Review reviewSnap // zero while the review is closed
	// Looks is the value of each of lookPaths. take leaves it empty: the
	// Model fills it, after input only. See Model.looksDue.
	Looks looks
	// ThemeEdited is true while the theme editor holds a change not saved.
	ThemeEdited bool
	// Moving is true while an animation is running, when window rects are
	// still on their way. Moves are reported once it is false again.
	Moving bool
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
		Glyphs:    o.Settings.GlyphSet,
		Border:    o.Settings.BorderStyle,
		ShowKeys:  o.ShowKeys,
		Moving:    len(o.Animations) > 0,
		Keys:      keysSnap{Preset: config.PresetTuios, Leader: o.Settings.LeaderKey, Lock: o.Settings.Lock},
		PiP:       o.PiPWindowID(),
	}
	s.Agents.Selected, s.Agents.Tab = o.CockpitSelection()
	if o.UserConfig != nil && o.UserConfig.Keybindings.Preset != "" {
		s.Keys.Preset = o.UserConfig.Keybindings.Preset
	}
	if s.Glyphs == "" {
		s.Glyphs = "default"
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
	case o.Menu != "":
		s.Prefix = "menu"
	case o.TuiosPrefixActive:
		s.Prefix = "tuios"
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
	ov[OverlayScreensaver] = o.ScreensaverActive()
	ov[OverlayAgents] = o.ShowInbox
	ov[OverlayMail] = o.ShowAgentMail
	ov[OverlayPresets] = o.PresetPickerOpen()
	ov[OverlayGoto] = o.GotoPickerOpen()
	ov[OverlayHints] = o.HintsOpen()
	ov[OverlaySpotlight] = o.SpotlightOn()
	ov[OverlayScratch] = o.InScratchView()
	ov[OverlayPaneLabels] = o.PaneLabelsOpen()
	ov[OverlayPeek] = o.ShowInbox && o.InboxPeeking()
	ov[OverlayNewAgent] = o.CockpitSpawnOpen()
	ov[OverlayCompare] = o.CockpitCompareOpen()
	ov[OverlayReply] = o.CockpitReplyOpen()
	ov[OverlayRailFocus] = o.SidebarActive() && o.SidebarFocused
	ov[OverlayFileSearch] = o.FileSearchOpen()
	ov[OverlayWorktree] = o.WorktreePromptOpen()
	if open, file, hunk := o.LearnReview(); open {
		ov[OverlayReview] = true
		s.Review = reviewSnap{File: file, Hunk: hunk}
	}
	s.Files.Dir, s.Files.Cursor = o.LearnFiles()
	ov[OverlayThemeEditor] = o.ThemeEditorOpen()
	ov[OverlayGlyphPicker] = o.ShowGlyphPicker
	s.ThemeEdited = o.ThemeEditorDirty()
	if b, ok := o.ScrollbackBrowser.(*scrollback.Browser); ok && o.ShowScrollbackBrowser && b != nil && b.SearchActive {
		ov[OverlaySearch] = true
	}
	// Leaving copy mode keeps the CopyMode struct and clears Active.
	if f := o.GetFocusedWindow(); f != nil && f.CopyMode != nil && f.CopyMode.Active {
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
			Minimized: w.Minimized, Zoomed: w.Zoomed, Floating: w.IsFloating,
			Agent: w.AgentState, AgentNote: w.AgentMessage, AgentKind: w.AgentKind, Harness: w.AgentHarness,
			Kind: w.Kind, Popup: w.IsPopup,
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
			"minimized": w.Minimized, "zoomed": w.Zoomed, "floating": w.Floating,
			"agent": w.Agent, "agentMessage": w.AgentNote,
			"agentKind": w.AgentKind, "harness": w.Harness,
			"kind": w.Kind, "popup": w.Popup,
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
		"glyphs":         s.Glyphs,
		"borderStyle":    s.Border,
		"keys":           s.Keys.toMap(),
		"pip":            s.PiP,
		"agentsView":     s.Agents.toMap(),
		"files":          s.Files.toMap(),
		"review":         s.reviewMap(),
		"cols":           s.Cols,
		"rows":           s.Rows,
	}
}

// reviewMap is the review in the state: nil while it is closed.
func (s Snapshot) reviewMap() any {
	if !s.Overlays[OverlayReview] {
		return nil
	}
	return s.Review.toMap()
}

// diff lists the events that take prev to now, in a fixed order.
func diff(prev, now Snapshot) []Event {
	var out []Event
	add := func(typ, windowID string, data map[string]any) { appendEvent(&out, typ, windowID, data) }
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
		if p.Floating != w.Floating {
			add(EventWindowFloat, w.ID, map[string]any{"id": w.ID, "floating": w.Floating})
		}
		if p.Workspace != w.Workspace {
			add(EventWindowWorkspace, w.ID, map[string]any{"id": w.ID, "from": p.Workspace, "to": w.Workspace})
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
	if prev.Glyphs != now.Glyphs {
		add(EventSetting, "", map[string]any{"name": SettingGlyphs, "from": prev.Glyphs, "to": now.Glyphs})
	}
	if prev.Border != now.Border {
		add(EventSetting, "", map[string]any{"name": SettingBorderStyle, "from": prev.Border, "to": now.Border})
	}
	if prev.ShowKeys != now.ShowKeys {
		add(EventSetting, "", map[string]any{"name": SettingShowKeys, "from": strconv.FormatBool(prev.ShowKeys), "to": strconv.FormatBool(now.ShowKeys)})
	}
	for i, path := range lookPaths {
		if prev.Looks[i] != now.Looks[i] {
			add(EventSetting, "", map[string]any{"name": path, "from": prev.Looks[i], "to": now.Looks[i]})
		}
	}
	if prev.ThemeEdited != now.ThemeEdited {
		add(EventThemeEdit, "", map[string]any{"changed": now.ThemeEdited})
	}
	if prev.Keys != now.Keys {
		add(EventKeys, "", map[string]any{"from": prev.Keys.toMap(), "to": now.Keys.toMap()})
	}
	if prev.PiP != now.PiP {
		add(EventPiP, now.PiP, map[string]any{"from": prev.PiP, "to": now.PiP})
	}
	if prev.Agents.Selected != now.Agents.Selected {
		w, _ := now.window(now.Agents.Selected)
		add(EventAgentsSelect, now.Agents.Selected, map[string]any{
			"from": prev.Agents.Selected, "to": now.Agents.Selected,
			"title": w.Title, "agent": w.Agent, "kind": w.AgentKind, "harness": w.Harness,
		})
	}
	if prev.Agents.Tab != now.Agents.Tab {
		add(EventAgentsTab, "", map[string]any{"from": prev.Agents.Tab, "to": now.Agents.Tab})
	}
	if prev.Files != now.Files {
		add(EventFiles, "", map[string]any{"from": prev.Files.toMap(), "to": now.Files.toMap()})
	}
	if prev.Overlays[OverlayReview] && now.Overlays[OverlayReview] && prev.Review != now.Review {
		data := now.Review.toMap()
		data["from"] = prev.Review.toMap()
		add(EventReviewMove, "", data)
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

// moves lists a window.move event for every window whose place or size
// changed between two settled snapshots. A window that opened or closed in
// between is left out: window.open and window.close already cover it.
func moves(rest, now Snapshot) []Event {
	var out []Event
	for _, w := range now.Windows {
		p, ok := rest.window(w.ID)
		if !ok {
			continue
		}
		from, to := rect{p.X, p.Y, p.W, p.H}, rect{w.X, w.Y, w.W, w.H}
		if from == to {
			continue
		}
		data := to.toMap()
		data["id"] = w.ID
		data["from"] = from.toMap()
		out = append(out, Event{Type: EventWindowMove, WindowID: w.ID, Data: data})
	}
	return out
}

// appendEvent is out of line, because diff adds an event at dozens of places.
//
//go:noinline
func appendEvent(out *[]Event, typ, windowID string, data map[string]any) {
	*out = append(*out, Event{Type: typ, WindowID: windowID, Data: data})
}
