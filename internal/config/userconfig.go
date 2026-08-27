package config

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// UserConfig represents the user's custom configuration
type UserConfig struct {
	Appearance    AppearanceConfig    `toml:"appearance"`
	Notifications NotificationsConfig `toml:"notifications"`
	Keybindings   KeybindingsConfig   `toml:"keybindings"`
	Daemon        DaemonConfig        `toml:"daemon"`
	Startup       StartupConfig       `toml:"startup"`
	Tape          TapeConfig          `toml:"tape"`
	Hooks         HooksConfig         `toml:"hooks"`
	Debug         DebugConfig         `toml:"debug"`
	// Screenshot is the [screenshot] table: how a capture renders and where
	// the file lands. See screenshot.go.
	Screenshot ScreenshotConfig `toml:"screenshot"`
	// Screensaver is the [screensaver] table: whether the screen animates
	// itself after a spell of quiet, and with what. See screensaver.go.
	Screensaver ScreensaverConfig `toml:"screensaver"`
	// Dock is the [dock] table: the bar as ordered lists of named components.
	// It sits outside the option registry for the same reason [hooks] and
	// [keybindings] do, being file-plane config rather than a settable option.
	Dock DockConfig `toml:"dock"`
	// Hosts is the [hosts.NAME] set: the other machines this daemon may ask for
	// listings. Outside the option registry for the same reason as the tables
	// above. See hosts.go.
	Hosts map[string]HostConfig `toml:"hosts,omitempty"`
}

// NotificationsConfig holds how long a dock message stays up.
//
// Durations are in seconds and are a floor, not a cap: a caller that asks for
// longer than the severity's default still gets what it asked for. Zero or
// absent means "use the built-in default", which is the value documented on the
// corresponding package var in constants.go.
type NotificationsConfig struct {
	// Duration is how long an info or success message stays up, in seconds.
	Duration int `toml:"duration"`

	// WarningDuration is how long a warning stays up, in seconds.
	WarningDuration int `toml:"warning_duration"`

	// ErrorDuration is how long an error stays up, in seconds, when
	// error_sticky is false.
	ErrorDuration int `toml:"error_duration"`

	// ErrorSticky makes errors wait for esc instead of expiring (default true).
	ErrorSticky *bool `toml:"error_sticky"`

	// Agent is the [notifications.agent] table: what happens when a pane's
	// agent state changes. See agent_alerts.go.
	Agent AgentAlertsConfig `toml:"agent"`
}

// DebugConfig holds diagnostic settings. These are off by default so a normal
// session is unaffected; they exist to diagnose input and rendering problems.
type DebugConfig struct {
	// ShowKeyEvents enables the on-screen showkeys overlay, a bottom-right
	// keycast that shows the last several keypresses as styled pills in both
	// window management and terminal mode. The same overlay is toggled by the
	// --show-keys flag, the settings entry, the command palette, and the
	// leader-D-k keybinding. Default false.
	ShowKeyEvents bool `toml:"show_key_events"`
}

// StartupConfig holds settings that only take effect when a session starts.
// Both default to false so a fresh install behaves exactly as before: the
// session comes up empty and floating, and the user opens the first window.
type StartupConfig struct {
	OpenDefaultWindow   bool `toml:"open_default_window"`    // Open one terminal window automatically when a session starts with none (default: false)
	Tiled               bool `toml:"tiled"`                  // Start a new session with tiling enabled instead of floating (default: false)
	StartInTerminalMode bool `toml:"start_in_terminal_mode"` // Start focused in terminal mode so typing goes straight to the shell, when a window is present (default: false)
	// Layout is the tiling scheme a new session arranges its panes with: bsp,
	// master-stack or scrolling. It only ever decides where a session starts.
	// Once one is running the scheme is the session's own and travels in its
	// state, so attaching to a session laid out one way never re-arranges it to
	// match the config of whoever attached.
	Layout string `toml:"layout"`
	// Daemon makes a bare "tuios" attach to a daemon-backed session instead of
	// running a standalone one, for a user who always wants the daemon and would
	// otherwise type "tuios attach" every time (default: false).
	//
	// It changes bare "tuios" and nothing else: every subcommand already says
	// which it wants, and a session already running is a separate process this
	// cannot reach. TUIOS_NO_DAEMON=1 and --standalone both override it, so a
	// daemon that will not start never leaves the user without a way in.
	Daemon bool `toml:"daemon"`
}

// TapeConfig holds settings for per-directory project tapes (.tuios.tape).
//
// Autorun is the master switch for detecting a project tape when the focused
// window's shell enters a directory that carries one:
//   - "off":  no detection, no indicator, the feature is invisible.
//   - "ask":  detection on; an encountered tape surfaces a passive indicator
//     (a dock badge and a non-focus-stealing notification) showing its trust
//     status. Nothing runs without the user's explicit action.
//   - "auto": a trusted, unedited tape runs automatically on entry; an untrusted
//     or changed tape falls back to the "ask" behavior and never auto-runs.
//
// In "ask" the passive indicator plus the review dialog (leader T t, or the
// command palette) are the only path to running a tape; nothing executes without
// the user opening the dialog and choosing Run. "auto" is the only mode that
// runs anything without a keypress, and only content the user already read and
// trusted. The default is "ask", which is safe by construction. A tape edited
// since it was trusted reverts to untrusted (hash mismatch) and re-prompts.
//
// AutoReview (default false) is an opt-in convenience: when true, entering a
// directory with a reviewable tape opens the review/trust dialog automatically
// instead of only surfacing the passive banner and badge, saving the keypress
// that opens it. It never weakens the trust boundary - the user still chooses Run
// once / Trust and run / Never / Not now - and it never auto-opens for a denied
// tape, an already-handled directory this session, or (in auto mode) a
// trusted-unedited tape that runs on its own.
type TapeConfig struct {
	Autorun    string `toml:"autorun"`     // off | ask | auto (default: ask)
	AutoReview bool   `toml:"auto_review"` // auto-open the review dialog on detection (default: false)
}

// DaemonConfig holds daemon-related settings
type DaemonConfig struct {
	LogLevel string `toml:"log_level"` // Debug log level: off, errors, basic, messages, verbose, trace (default: off)
	// AgentAutoDetect toggles automatic detection of a pane's foreground AI-agent
	// CLI (claude, codex, aider, ...), which sets the pane's agent-state glyph
	// without set-agent-state. Nil means enabled (the default); set to false to
	// turn it off.
	AgentAutoDetect *bool `toml:"agent_autodetect"`
	// AgentDetectSeconds overrides how often the auto-detector polls each pane, in
	// seconds. Zero uses the default (2s); a negative value disables detection.
	AgentDetectSeconds int `toml:"agent_detect_seconds"`
	// AgentBinaries lists extra binary names to treat as agents, merged with the
	// built-in defaults (not replacing them).
	AgentBinaries []string `toml:"agent_binaries"`
}

// AppearanceConfig holds appearance-related settings
type AppearanceConfig struct {
	BorderStyle          string  `toml:"border_style"`           // Border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block (borderless mode not yet implemented)
	ZenMode              string  `toml:"zen_mode"`               // Zen mode: disabled, always, mouse (default: disabled)
	Links                string  `toml:"links"`                  // Links tuios acts on: off, marked, all (default: all)
	HideWindowButtons    bool    `toml:"hide_window_buttons"`    // Hide window control buttons (minimize, maximize, close)
	WindowButtonStyle    string  `toml:"window_button_style"`    // Window control style: pill, dots (default: pill)
	WindowButtonPosition string  `toml:"window_button_position"` // Which end of the title bar the window controls sit on: right, left (default: right)
	HideScrollbar        bool    `toml:"hide_scrollbar"`         // Hide the window scrollbar thumb on the border
	ScrollbackLines      int     `toml:"scrollback_lines"`       // Number of lines to keep in scrollback buffer (default: 10000, min: 100, max: 1000000)
	ScrollLines          int     `toml:"scroll_lines"`           // Lines scrolled per mouse wheel notch (default: 3, min: 1, max: 50)
	CopyOnSelect         *bool   `toml:"copy_on_select"`         // Copy a mouse selection to the clipboard on release (default: true)
	FocusFollowsMouse    *bool   `toml:"focus_follows_mouse"`    // Focus the pane under the cursor as the mouse moves (default: false)
	AltDrag              *bool   `toml:"alt_drag"`               // Alt + left-drag moves a pane (default: true)
	ClickToType          string  `toml:"click_to_type"`          // What a click on a pane's content does in window-management mode: single, double, off (default: single)
	WordCharacters       *string `toml:"word_characters"`        // Punctuation that counts as part of a word for double-click selection (default: "@-./_~?&=%+#")
	DockbarPosition      string  `toml:"dockbar_position"`       // Dockbar position: bottom, top, hidden
	PreferredShell       string  `toml:"preferred_shell"`        // Preferred shell: if empty, auto-detect based on platform.
	AnimationsEnabled    *bool   `toml:"animations_enabled"`     // Enable UI animations (default: true). Set to false for instant transitions.
	ConfirmQuit          *bool   `toml:"confirm_quit"`           // Always show quit confirmation dialog (default: false). When false, only shown if foreground processes are running.
	WhichKeyEnabled      *bool   `toml:"whichkey_enabled"`       // Show which-key popup after pressing leader key (default: true)
	WhichKeyPosition     string  `toml:"whichkey_position"`      // Which-key popup position: bottom-right, bottom-left, top-right, top-left, center (default: bottom-right)
	WindowTitlePosition  string  `toml:"window_title_position"`  // Window title position: bottom, top, hidden (default: bottom). Shows CustomName if set, else terminal title.
	HideClock            bool    `toml:"hide_clock"`             // Hide the clock overlay (deprecated, use show_clock)
	ShowClock            bool    `toml:"show_clock"`             // Show the clock overlay (default: false)
	ShowCPU              bool    `toml:"show_cpu"`               // Show CPU graph in dock (default: false)
	ShowRAM              bool    `toml:"show_ram"`               // Show RAM usage in dock (default: false)
	Theme                string  `toml:"theme"`                  // Color theme name (e.g., dracula, nord, my-custom-theme)
	SharedBorders        *bool   `toml:"shared_borders"`         // Share borders between adjacent tiled windows (default: false)
	// Customization
	BorderFocusedColor     string `toml:"border_focused_color"`      // Hex color for focused pane border (e.g., "#89b4fa")
	BorderUnfocusedColor   string `toml:"border_unfocused_color"`    // Hex color for unfocused pane border (e.g., "#585b70")
	WindowTitleFormat      string `toml:"window_title_format"`       // Format string for window titles: {title}, {index}, {cwd}
	ZoomMaxWidth           int    `toml:"zoom_max_width"`            // Max width in cells for zoom mode (0 = fullscreen, e.g. 120 centers at 120 cols)
	NiriReverseScroll      bool   `toml:"niri_reverse_scroll"`       // Reverse mouse scroll direction in niri scrolling mode (default: false)
	MaxFPS                 int    `toml:"max_fps"`                   // Maximum render FPS (default: 60, max: 120)
	DockWorkspaceTabs      *bool  `toml:"dock_workspace_tabs"`       // Clickable workspace strip in the dock (default: true)
	DockWorkspaceTabFormat string `toml:"dock_workspace_tab_format"` // Format string for workspace tabs: {index}, {name} (default: "{name}")
	DockWorkspaceTooltip   *bool  `toml:"dock_workspace_tooltip"`    // Pop a truncated workspace name in full on hover (default: true)
	DockPillCaps           *bool  `toml:"dock_pill_caps"`            // Powerline caps on the dock's pills (default: false, flat)
	SessionColors          *bool  `toml:"session_colors"`            // Give each session its own colour on the rail and the switcher (default: true)
	Glyphs                 string `toml:"glyphs"`                    // Chrome glyph set: default, box, heavy, ascii, or one from ~/.config/tuios/glyphs
	Gap                    int    `toml:"gap"`                       // Cells of empty space kept between neighbouring tiled panes (default: 0)
	// MasterRatio and ScrollColumnWidth are percentages rather than fractions
	// because that is what a settings stepper and a CLI argument can carry: the
	// option registry holds ints, and "50" is a value a person types. The model
	// keeps the master ratio as the fraction the tilers want.
	MasterRatio       int    `toml:"master_ratio"`        // Master pane width in the master-stack layout, percent of the screen (default: 50)
	ScrollColumnWidth int    `toml:"scroll_column_width"` // New column width in the scrolling layout, percent of the screen (default: 55)
	PanelPadding      int    `toml:"panel_padding"`       // Columns of surface padding inside every overlay panel (default: 2)
	ClockFormat       string `toml:"clock_format"`        // Go time layout the clock overlay is drawn with (default: 15:04:05)
	DimUnfocused      int    `toml:"dim_unfocused"`       // Percent an unfocused pane's content is carried toward its own ground (default: 0)

	// Legacy flat sidebar keys, superseded by the [appearance.sidebar] table.
	// migrateLegacySidebar folds them into it and clears them, so they are read
	// from an old file but never written back to a new one.
	SidebarEnabled     *bool  `toml:"sidebar_enabled,omitempty"`
	SidebarPosition    string `toml:"sidebar_position,omitempty"`
	SidebarWidth       int    `toml:"sidebar_width,omitempty"`
	SidebarShowWindows *bool  `toml:"sidebar_show_windows,omitempty"`
	SidebarShowGlyphs  *bool  `toml:"sidebar_show_glyphs,omitempty"`
	SidebarShowCounts  *bool  `toml:"sidebar_show_counts,omitempty"`

	// The tables are last so the TOML encoder emits them after every scalar key
	// of [appearance]; a table written mid-section would swallow the keys that
	// follow it.
	Scrollbar ScrollbarConfig `toml:"scrollbar"`
	Sidebar   SidebarConfig   `toml:"sidebar"`
}

// Click-to-type policies. See AppearanceConfig.ClickToType.
const (
	// ClickToTypeSingle enters terminal mode when a click on a pane's content
	// is released without a drag.
	ClickToTypeSingle = "single"
	// ClickToTypeDouble focuses on a single click and enters terminal mode only
	// on the second click of a double click.
	ClickToTypeDouble = "double"
	// ClickToTypeOff never changes mode from a click: the pane takes focus and
	// the keyboard stays with the window manager.
	ClickToTypeOff = "off"
)

// ClickToTypeModes lists the valid values for appearance.click_to_type.
var ClickToTypeModes = []string{ClickToTypeSingle, ClickToTypeDouble, ClickToTypeOff}

// Zen-mode policies. See AppearanceConfig.ZenMode.
const (
	// ZenModeDisabled keeps window borders always visible (default).
	ZenModeDisabled = "disabled"
	// ZenModeAlways hides window borders at all times.
	ZenModeAlways = "always"
	// ZenModeMouse hides window borders while the mouse is idle, revealing
	// them while the pointer is moving or any mouse button is held.
	ZenModeMouse = "mouse"
)

// ZenModeModes lists the valid values for appearance.zen_mode.
var ZenModeModes = []string{ZenModeDisabled, ZenModeAlways, ZenModeMouse}

// Link policies. See AppearanceConfig.Links.
//
// The three values answer one question: what counts as a link the pointer may
// pick up. A program that emits OSC 8 has said outright that a run of cells is
// a link and where it points, so "marked" trusts only that. Almost no program
// does, though, and the links a person actually reads in a pane are plain text,
// so "all" also finds bare http, https and file URLs. "off" is for anyone who
// wants the pointer to leave pane content alone.
const (
	// LinksOff finds no links at all.
	LinksOff = "off"
	// LinksMarked finds only OSC 8 hyperlinks.
	LinksMarked = "marked"
	// LinksAll also finds bare http, https and file URLs in plain text.
	LinksAll = "all"
)

// LinkModes lists the valid values for appearance.links.
var LinkModes = []string{LinksOff, LinksMarked, LinksAll}

// Window control styles. See AppearanceConfig.WindowButtonStyle.
const (
	// WindowButtonStylePill draws the controls as black glyphs on a filled pill
	// in the border's colour, capped with powerline half circles.
	WindowButtonStylePill = "pill"
	// WindowButtonStyleDots draws them as macOS traffic lights: three unlabelled
	// discs in red, yellow and green, sitting straight on the border, showing
	// their symbols while the pointer is on them.
	WindowButtonStyleDots = "dots"
)

// WindowButtonStyles lists the valid values for appearance.window_button_style.
var WindowButtonStyles = []string{WindowButtonStylePill, WindowButtonStyleDots}

// Which end of the title bar the controls sit on. See
// AppearanceConfig.WindowButtonPosition.
const (
	// WindowButtonPositionRight puts them against the border's trailing corner,
	// which is where Windows and most Linux desktops put them.
	WindowButtonPositionRight = "right"
	// WindowButtonPositionLeft puts them against the leading corner, the way
	// macOS does.
	WindowButtonPositionLeft = "left"
)

// WindowButtonPositions lists the valid values for
// appearance.window_button_position.
var WindowButtonPositions = []string{WindowButtonPositionRight, WindowButtonPositionLeft}

// Scrollbar styles. See ScrollbarConfig.Style.
const (
	// ScrollbarStyleThin floats a hairline thumb over the pane's last content
	// column and draws nothing else.
	ScrollbarStyleThin = "thin"
	// ScrollbarStyleTrack draws a full-height track behind a block thumb
	// positioned to the half cell, after opentui's ScrollBar.
	ScrollbarStyleTrack = "track"
)

// ScrollbarStyles lists the valid values for appearance.scrollbar.style.
var ScrollbarStyles = []string{ScrollbarStyleThin, ScrollbarStyleTrack}

// Scrollbar tints. See ScrollbarConfig.Tint.
const (
	// ScrollbarTintQuiet draws the bar in the pane's own ink dimmed toward the
	// pane's own background: a mid-grey thumb on a dark theme, a mid-grey thumb
	// on a light one, and no hue of its own either way.
	ScrollbarTintQuiet = "quiet"
	// ScrollbarTintBorder draws the focused pane's bar in the colour its border
	// is drawn in, or in its accent when it has one.
	ScrollbarTintBorder = "border"
	// ScrollbarTintMuted draws every bar in the unfocused border grey.
	ScrollbarTintMuted = "muted"
)

// ScrollbarTints lists the keyword values for appearance.scrollbar.tint; a
// #RRGGBB literal is also accepted.
var ScrollbarTints = []string{ScrollbarTintQuiet, ScrollbarTintBorder, ScrollbarTintMuted}

// ScrollbarTrackNone is the track value that draws no track at all, which is
// what the thin style looked like before it grew one.
const ScrollbarTrackNone = "none"

// ScrollbarConfig is the pane scrollbar's own table. hide_scrollbar predates it
// and stays where it is, so existing files keep working. Every key here is
// optional and an absent one takes the style's own default, so a file written
// before the table grew renders exactly what the release documents.
type ScrollbarConfig struct {
	Style string `toml:"style"` // thin, track (default: thin)
	Thumb string `toml:"thumb"` // one-cell glyph (default: thin ▐, track █, ASCII |)
	Track string `toml:"track"` // one-cell glyph or none (default: thin ▕, track the surface fill, ASCII none)
	Tint  string `toml:"tint"`  // border, muted, #RRGGBB (default: border)
}

// SidebarConfig holds the [appearance.sidebar] table: everything about the
// vertical session rail. Each toggle is a pointer so nil can mean "unset, use
// the default" and an explicit false survives a reload.
type SidebarConfig struct {
	Enabled     *bool  `toml:"enabled"`      // Show the rail (default: false)
	Position    string `toml:"position"`     // Edge: left, right, hidden (default: left)
	Width       int    `toml:"width"`        // Preferred width in columns for a wide screen (default: 28)
	ShowWindows *bool  `toml:"show_windows"` // The terminals section (default: true)
	ShowGlyphs  *bool  `toml:"show_glyphs"`  // Agent-state glyph on each row (default: true)
	ShowCounts  *bool  `toml:"show_counts"`  // Window count on each session row (default: true)
	ShowAgents  *bool  `toml:"show_agents"`  // Agents section at the rail's bottom (default: true)
	// Workspaces named the workspace chip band, which the rail no longer draws:
	// panes say which workspace they are on with a tag of their own, and
	// switching stays on the dock and alt+1..9. Still parsed so an existing
	// config file loads unchanged; validation warns once and nothing reads it.
	Workspaces string `toml:"workspaces"`
	Marquee    *bool  `toml:"marquee"`  // Scroll a hovered row's overflowing title (default: true)
	Tooltips   *bool  `toml:"tooltips"` // Label the collapsed strip on hover (default: true)
	// Sections is the rail's layout: which sections it stacks, in what order,
	// and the share of it each may claim. See SidebarDefaultSections.
	Sections string `toml:"sections"`
	// FileIcons draws a nerd font icon per file type in the files section
	// (default: true).
	FileIcons *bool `toml:"file_icons"`
	// FileIconColors draws each of those icons in its file type's own colour
	// (default: true).
	FileIconColors *bool `toml:"file_icon_colors"`
	// FolderClick is what a click on a folder row does: navigate, cd or both
	// (default: navigate).
	FolderClick string `toml:"folder_click"`
	// FileActions lets the files section create, rename, delete, copy, cut and
	// paste (default: true).
	FileActions *bool `toml:"file_actions"`
	// FileDelete is where a delete sends the file: trash or permanent
	// (default: trash).
	FileDelete string `toml:"file_delete"`
}

// Tape autorun modes. See TapeConfig.Autorun.
const (
	TapeAutorunOff  = "off"
	TapeAutorunAsk  = "ask"
	TapeAutorunAuto = "auto"
)

// TapeAutorunModes lists the valid values for tape.autorun.
var TapeAutorunModes = []string{TapeAutorunOff, TapeAutorunAsk, TapeAutorunAuto}

// HooksConfig holds shell command hooks for events.
type HooksConfig map[string]any

// KeybindingsConfig holds all keybinding configurations
type KeybindingsConfig struct {
	LeaderKey        string              `toml:"leader_key"` // Leader key for prefix commands (default: ctrl+b)
	WindowManagement map[string][]string `toml:"window_management"`
	Workspaces       map[string][]string `toml:"workspaces"`
	Layout           map[string][]string `toml:"layout"`
	ModeControl      map[string][]string `toml:"mode_control"`
	System           map[string][]string `toml:"system"`
	Navigation       map[string][]string `toml:"navigation"`
	RestoreMinimized map[string][]string `toml:"restore_minimized"`
	PrefixMode       map[string][]string `toml:"prefix_mode"`
	WindowPrefix     map[string][]string `toml:"window_prefix"`
	MinimizePrefix   map[string][]string `toml:"minimize_prefix"`
	WorkspacePrefix  map[string][]string `toml:"workspace_prefix"`
	DebugPrefix      map[string][]string `toml:"debug_prefix"`
	TapePrefix       map[string][]string `toml:"tape_prefix"`
	LayoutPrefix     map[string][]string `toml:"layout_prefix"`
	TerminalMode     map[string][]string `toml:"terminal_mode"` // Direct keybinds in terminal mode (no prefix required)
	// Global binds are consulted in window mode and in terminal mode alike, so
	// the palette and the launcher answer to one key wherever the user is. They
	// are a section of their own rather than entries in terminal_mode because
	// they are not terminal-mode-only, and rather than literals in the input
	// path because a key nobody can rebind is a key nobody can inspect.
	Global map[string][]string `toml:"global"`
	// Script binds are live only while a .tape is playing back. Its own section
	// because it is its own keyboard context: sharing ctrl+p with the palette by
	// default is not a conflict, since only one of the two contexts is ever
	// active.
	Script map[string][]string `toml:"script"`
	// Sidebar binds are looked up only while the rail owns the keyboard
	// (SidebarFocused), through GetSidebarAction. They are deliberately kept out
	// of buildMappings: that flattens sections into the global keymap, which would
	// leak rail keys (j/k/h/l/enter) onto panes.
	Sidebar map[string][]string `toml:"sidebar"`
	// SidebarFiles binds are looked up before Sidebar, and only while the rail's
	// cursor is on a row of the files section. See
	// getDefaultSidebarFilesKeybinds for why they are not in Sidebar.
	SidebarFiles map[string][]string `toml:"sidebar_files"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *UserConfig {
	cfg := &UserConfig{
		Appearance: AppearanceConfig{
			BorderStyle:          "rounded",
			ZenMode:              ZenModeDisabled,
			Links:                LinksAll,
			HideWindowButtons:    false,
			WindowButtonStyle:    WindowButtonStylePill,
			WindowButtonPosition: WindowButtonPositionRight,
			ScrollbackLines:      10000,
			ScrollLines:          3,
			DockbarPosition:      "bottom",
			PreferredShell:       "",
			ClickToType:          ClickToTypeSingle,
			Glyphs:               theme.GlyphSetNone,
			PanelPadding:         overlay.DefaultPanelPadding,
			ClockFormat:          DefaultClockFormat,
			MasterRatio:          MasterRatioDefault,
			ScrollColumnWidth:    ScrollColumnWidthDefault,
			Scrollbar:            ScrollbarConfig{Style: ScrollbarStyleThin, Tint: ScrollbarTintQuiet},
			Sidebar: SidebarConfig{
				Position:    "left",
				Width:       SidebarDefaultWidth,
				Sections:    SidebarDefaultSections,
				FolderClick: SidebarFolderClickNavigate,
				FileDelete:  SidebarFileDeleteTrash,
			},
		},
		Daemon: DaemonConfig{
			LogLevel: "off",
		},
		Startup: StartupConfig{
			OpenDefaultWindow:   false,
			Tiled:               false,
			StartInTerminalMode: false,
			Layout:              LayoutModeBSP,
		},
		Tape: TapeConfig{
			Autorun:    TapeAutorunAsk,
			AutoReview: false,
		},
		Screenshot:  defaultScreenshotConfig(),
		Screensaver: defaultScreensaverConfig(),
		Keybindings: KeybindingsConfig{
			LeaderKey: "ctrl+b",
			WindowManagement: map[string][]string{
				"new_window":        {"n"},
				"close_window":      {"w", "x"},
				"rename_window":     {"r"},
				"minimize_window":   {"m"},
				"restore_all":       {"M"},
				"toggle_zoom":       {"z"},
				"start_screensaver": {"S"},
				// Finishing a mouse selection has always told the user to press
				// 'c' to copy it. Until this binding existed, nothing was
				// listening.
				"copy_selection":  {"c"},
				"next_window":     {"tab"},
				"prev_window":     {"shift+tab"},
				"select_window_1": {"1"},
				"select_window_2": {"2"},
				"select_window_3": {"3"},
				"select_window_4": {"4"},
				"select_window_5": {"5"},
				"select_window_6": {"6"},
				"select_window_7": {"7"},
				"select_window_8": {"8"},
				"select_window_9": {"9"},
				// Enter the sidebar rail's keyboard scope; s is free in window mode.
				"focus_sidebar": {"s"},
				// Walking sessions is a chord, not a letter, so it also works while
				// typing in a shell (see isTerminalSafeAction). Spell the shift out:
				// "alt+N" normalizes to alt+n, which is already next-window.
				"next_session": {"alt+shift+n"},
				"prev_session": {"alt+shift+p"},
			},
			Workspaces: getDefaultWorkspaceKeybinds(),
			Layout:     getDefaultLayoutKeybinds(),
			ModeControl: map[string][]string{
				"enter_terminal_mode": {"i", "enter"},
				"enter_window_mode":   {"esc"},
				"toggle_help":         {"?"},
				"quit":                {"q"},
				// "," is resize_master_shrink_left by default too. mode_control is
				// consulted after layout, so settings wins and the doctor reports
				// the resize bind as dead, which is the honest description of what
				// the input path was already doing behind a literal.
				"open_settings": {","},
				// hold_window_mode is deliberately absent rather than present and
				// empty. An empty list is how a user says they took a key away
				// (see keybind_unbind.go), and a default must not say that on
				// their behalf: it would show up in the keybind manager as a
				// binding they had removed. Holding a modifier key is only
				// reportable under the
				// Kitty protocol's report-all-keys mode, which turns every
				// keystroke in the session into an escape code, so it is not
				// something everyone should pay for. The config header says how
				// to turn it on.
			},
			System: map[string][]string{
				// Debug commands (logs, cache stats) are accessed via Ctrl+B D submenu
				// and are not directly configurable as keybindings
			},
			Navigation: map[string][]string{
				"nav_up":    {"up"},
				"nav_down":  {"down"},
				"nav_left":  {"left"},
				"nav_right": {"right"},
			},
			RestoreMinimized: map[string][]string{
				"restore_minimized_1": {"shift+1", "!"},
				"restore_minimized_2": {"shift+2", "@"},
				"restore_minimized_3": {"shift+3", "#"},
				"restore_minimized_4": {"shift+4", "$"},
				"restore_minimized_5": {"shift+5", "%"},
				"restore_minimized_6": {"shift+6", "^"},
				"restore_minimized_7": {"shift+7", "&"},
				"restore_minimized_8": {"shift+8", "*"},
				"restore_minimized_9": {"shift+9", "("},
			},
			PrefixMode: map[string][]string{
				"prefix_new_window":    {"c"},
				"prefix_close_window":  {"x"},
				"prefix_rename_window": {"r"},
				"prefix_settings":      {","},
				"prefix_keybinds":      {"k"},
				"prefix_next_window":   {"n", "tab"},
				"prefix_prev_window":   {"p", "shift+tab"},
				"prefix_select_0":      {"0"},
				"prefix_select_1":      {"1"},
				"prefix_select_2":      {"2"},
				"prefix_select_3":      {"3"},
				"prefix_select_4":      {"4"},
				"prefix_select_5":      {"5"},
				"prefix_select_6":      {"6"},
				"prefix_select_7":      {"7"},
				"prefix_select_8":      {"8"},
				"prefix_select_9":      {"9"},
				"prefix_toggle_tiling": {"space"},
				"prefix_workspace":     {"w"},
				"prefix_minimize":      {"m"},
				"prefix_window":        {"t"},
				"prefix_detach":        {"d"},
				// Capital X, one shift away from the x that closes a single pane.
				// The two verbs sound alike and only one of them is recoverable, so
				// the slip that matters costs a pane and never the session.
				"prefix_close_session":    {"X"},
				"prefix_exit_mode":        {"esc"},
				"prefix_selection":        {"["},
				"prefix_help":             {"?"},
				"prefix_debug":            {"D"},
				"prefix_tape":             {"T"},
				"prefix_quit":             {"q"},
				"prefix_fullscreen":       {"z"},
				"prefix_split_horizontal": {"-"},
				"prefix_split_vertical":   {"|", "\\"},
				"prefix_rotate_split":     {"R"},
				"prefix_equalize_splits":  {"="},
				"prefix_scrollback":       {"s"},
				// s is the scrollback browser, so the capture takes capital C,
				// one shift away from the c that creates a window. Nothing here
				// is destructive either way.
				"prefix_screenshot":         {"C"},
				"prefix_command_palette":    {"P"},
				"prefix_toggle_sidebar":     {"b"},
				"prefix_session_switcher":   {"S"},
				"prefix_workspace_switcher": {"W"},
				"prefix_layout":             {"L"},
				"prefix_explore":            {"e"}, // the same key goes to the rail and comes back
				"prefix_jump_notif":         {"j"}, // the keyboard twin of clicking a message
			},
			WindowPrefix: map[string][]string{
				"window_prefix_new":    {"n"},
				"window_prefix_close":  {"x"},
				"window_prefix_rename": {"r"},
				"window_prefix_next":   {"tab"},
				"window_prefix_prev":   {"shift+tab"},
				"window_prefix_tiling": {"t"},
				"window_prefix_cancel": {"esc"},
			},
			MinimizePrefix: map[string][]string{
				"minimize_prefix_focused":     {"m"},
				"minimize_prefix_restore_1":   {"1"},
				"minimize_prefix_restore_2":   {"2"},
				"minimize_prefix_restore_3":   {"3"},
				"minimize_prefix_restore_4":   {"4"},
				"minimize_prefix_restore_5":   {"5"},
				"minimize_prefix_restore_6":   {"6"},
				"minimize_prefix_restore_7":   {"7"},
				"minimize_prefix_restore_8":   {"8"},
				"minimize_prefix_restore_9":   {"9"},
				"minimize_prefix_restore_all": {"M"},
				"minimize_prefix_cancel":      {"esc"},
			},
			WorkspacePrefix: map[string][]string{
				"workspace_prefix_switch_1": {"1"},
				"workspace_prefix_switch_2": {"2"},
				"workspace_prefix_switch_3": {"3"},
				"workspace_prefix_switch_4": {"4"},
				"workspace_prefix_switch_5": {"5"},
				"workspace_prefix_switch_6": {"6"},
				"workspace_prefix_switch_7": {"7"},
				"workspace_prefix_switch_8": {"8"},
				"workspace_prefix_switch_9": {"9"},
				"workspace_prefix_move_1":   {"!"},
				"workspace_prefix_move_2":   {"@"},
				"workspace_prefix_move_3":   {"#"},
				"workspace_prefix_move_4":   {"$"},
				"workspace_prefix_move_5":   {"%"},
				"workspace_prefix_move_6":   {"^"},
				"workspace_prefix_move_7":   {"&"},
				"workspace_prefix_move_8":   {"*"},
				"workspace_prefix_move_9":   {"("},
				"workspace_prefix_rename":   {"r"},
				"workspace_prefix_cancel":   {"esc"},
			},
			DebugPrefix: map[string][]string{
				"debug_prefix_logs":       {"l"},
				"debug_prefix_cache":      {"c"},
				"debug_prefix_animations": {"a"},
				"debug_prefix_showkeys":   {"k"},
				"debug_prefix_cancel":     {"esc"},
			},
			TapePrefix: map[string][]string{
				"tape_prefix_manager": {"m"},
				"tape_prefix_review":  {"t"},
				"tape_prefix_record":  {"r"},
				"tape_prefix_stop":    {"s"},
				"tape_prefix_cancel":  {"esc"},
			},
			LayoutPrefix: map[string][]string{
				"layout_prefix_load":   {"l"},
				"layout_prefix_save":   {"s"},
				"layout_prefix_cancel": {"esc"},
				// Corner snapping lives here rather than on the bare digits in
				// window mode, where it used to shadow select_window_1 through
				// _4. See getDefaultLayoutKeybinds for why it lost that contest.
				"snap_corner_1": {"1"},
				"snap_corner_2": {"2"},
				"snap_corner_3": {"3"},
				"snap_corner_4": {"4"},
			},
			TerminalMode: getDefaultTerminalModeKeybinds(),
			Sidebar:      getDefaultSidebarKeybinds(),
			SidebarFiles: getDefaultSidebarFilesKeybinds(),
			Global: map[string][]string{
				// ctrl+p is fish's history-back and vim's keyword completion, and
				// alt+space is readline's set-mark. Both are taken on purpose and
				// both live here rather than in the input path so a user who wants
				// them back can have them: set the action to [] and the key is
				// forwarded again.
				"command_palette": {"ctrl+p"},
				"launcher":        {"alt+space"},
			},
			Script: map[string][]string{
				"script_pause": {"ctrl+p"},
			},
		},
	}
	return cfg
}

// getDefaultSidebarKeybinds returns the rail's scope-local keybindings, active
// only while the rail owns the keyboard (SidebarFocused). Each mirrors a mouse
// affordance so the two devices reach the same OS mutation. Case matters: J/K
// reorder, j/k move the cursor.
func getDefaultSidebarKeybinds() map[string][]string {
	return map[string][]string{
		"cursor_down":  {"j", "down"},
		"cursor_up":    {"k", "up"},
		"first":        {"g", "home"},
		"last":         {"G", "end"},
		"expand":       {"l", "right"},
		"collapse":     {"h", "left"},
		"activate":     {"enter"},
		"reorder_down": {"J", "shift+down"},
		"reorder_up":   {"K", "shift+up"},
		"section":      {"tab", "shift+tab"},
		// The agents section's own two controls, which is why they are single
		// letters rather than a chord: they are the section's shape, and changing
		// it is a browse, not a command.
		"agents_filter": {"f"},
		"agents_sort":   {"o"},
		// The rail lists; the palette searches. "/" is what searches everywhere
		// else, and the rail is the one scope with nothing else to spend it on.
		"palette":     {"/"},
		"narrow":      {"<"},
		"widen":       {">"},
		"jump_1":      {"1"},
		"jump_2":      {"2"},
		"jump_3":      {"3"},
		"jump_4":      {"4"},
		"jump_5":      {"5"},
		"jump_6":      {"6"},
		"jump_7":      {"7"},
		"jump_8":      {"8"},
		"jump_9":      {"9"},
		"new_session": {"n"},
		"new_window":  {"t"},
		"rename":      {"r"},
		"accent":      {"c"},
		"kill":        {"x"},
		"menu":        {"m"},
		"help":        {"?"},
		"exit":        {"esc", "s"},
	}
}

// getDefaultSidebarFilesKeybinds returns the files section's own keys, live
// only while the rail owns the keyboard and the cursor is on a row of the
// listing.
//
// They are neo-tree's keys and yeetui's: a adds, r renames, d deletes, y
// copies, x cuts, p pastes. Both tools agree on five of the six and the
// maintainer wrote one of them, so this is the set his hands already know.
//
// A section of their own, and not entries in [keybindings.sidebar], because
// three of them collide with a rail key that already exists: r renames a pane
// or a session, x opens a row's destructive menu, and a makes nothing yet. One
// map cannot hold two actions on one key, so the two maps are consulted in
// order instead, and the row under the cursor decides which one answers. That
// is the same rule the rail's own rename already follows, where r means the
// pane on a pane row and the session on a session row.
//
// D is the permanent delete. It is a key of its own rather than a second answer
// inside the dialog, because a file on another disk cannot go to the home trash
// at all, and somebody who means it should not have to edit a config file.
func getDefaultSidebarFilesKeybinds() map[string][]string {
	return map[string][]string{
		"file_create":         {"a"},
		"file_rename":         {"r"},
		"file_delete":         {"d"},
		"file_delete_forever": {"D"},
		"file_copy":           {"y"},
		"file_cut":            {"x"},
		"file_paste":          {"p"},
	}
}

// getDefaultTerminalModeKeybinds returns platform-specific terminal mode keybindings
// These are direct keybinds that work in terminal mode without the prefix key
func getDefaultTerminalModeKeybinds() map[string][]string {
	if isMacOS() {
		return map[string][]string{
			"terminal_next_window": {"opt+tab", "alt+n"},
			"terminal_prev_window": {"opt+shift+tab", "alt+p"},
			"terminal_exit_mode":   {"opt+esc"},
			"terminal_focus_left":  {"alt+left"},
			"terminal_focus_right": {"alt+right"},
			"terminal_focus_up":    {"alt+up"},
			"terminal_focus_down":  {"alt+down"},
			"terminal_scroll_up":   {"shift+up"},
			"terminal_scroll_down": {"shift+down"},
			"terminal_paste_host":  {"ctrl+shift+v", "super+v", "super+shift+v"},
		}
	}
	return map[string][]string{
		"terminal_next_window": {"alt+n"},
		"terminal_prev_window": {"alt+p"},
		"terminal_exit_mode":   {"alt+esc"},
		// alt+left and alt+right are word-wise cursor movement in readline, fish
		// and zsh, and taking them costs a user that. They are bound because
		// directional focus on the arrows is what zellij and most tiling window
		// managers do, and because each of the four is a separate action a user
		// can set to [] to hand back. See docs/KEYBINDINGS.md.
		"terminal_focus_left":  {"alt+left"},
		"terminal_focus_right": {"alt+right"},
		"terminal_focus_up":    {"alt+up"},
		"terminal_focus_down":  {"alt+down"},
		"terminal_scroll_up":   {"shift+up"},
		"terminal_scroll_down": {"shift+down"},
		// Plain ctrl+v is deliberately not here: it has to reach the pane as 0x16
		// for vim's visual block, which is the tmux and zellij convention.
		"terminal_paste_host": {"ctrl+shift+v", "super+v", "super+shift+v"},
	}
}

// getDefaultWorkspaceKeybinds returns platform-specific workspace keybindings
func getDefaultWorkspaceKeybinds() map[string][]string {
	// On macOS, use opt+N (which expands to alt+N and unicode via normalization)
	// On Linux/other, use alt+N
	var base map[string][]string

	if isMacOS() {
		// macOS users think in terms of Option key
		// The KeyNormalizer will expand opt+1 → [opt+1, alt+1, ¡]
		base = map[string][]string{
			"switch_workspace_1": {"opt+1"},
			"switch_workspace_2": {"opt+2"},
			"switch_workspace_3": {"opt+3"},
			"switch_workspace_4": {"opt+4"},
			"switch_workspace_5": {"opt+5"},
			"switch_workspace_6": {"opt+6"},
			"switch_workspace_7": {"opt+7"},
			"switch_workspace_8": {"opt+8"},
			"switch_workspace_9": {"opt+9"},
			"move_and_follow_1":  {"opt+shift+1"},
			"move_and_follow_2":  {"opt+shift+2"},
			"move_and_follow_3":  {"opt+shift+3"},
			"move_and_follow_4":  {"opt+shift+4"},
			"move_and_follow_5":  {"opt+shift+5"},
			"move_and_follow_6":  {"opt+shift+6"},
			"move_and_follow_7":  {"opt+shift+7"},
			"move_and_follow_8":  {"opt+shift+8"},
			"move_and_follow_9":  {"opt+shift+9"},
		}
	} else {
		// Linux and other platforms use alt
		base = map[string][]string{
			"switch_workspace_1": {"alt+1"},
			"switch_workspace_2": {"alt+2"},
			"switch_workspace_3": {"alt+3"},
			"switch_workspace_4": {"alt+4"},
			"switch_workspace_5": {"alt+5"},
			"switch_workspace_6": {"alt+6"},
			"switch_workspace_7": {"alt+7"},
			"switch_workspace_8": {"alt+8"},
			"switch_workspace_9": {"alt+9"},
			"move_and_follow_1":  {"alt+shift+1"},
			"move_and_follow_2":  {"alt+shift+2"},
			"move_and_follow_3":  {"alt+shift+3"},
			"move_and_follow_4":  {"alt+shift+4"},
			"move_and_follow_5":  {"alt+shift+5"},
			"move_and_follow_6":  {"alt+shift+6"},
			"move_and_follow_7":  {"alt+shift+7"},
			"move_and_follow_8":  {"alt+shift+8"},
			"move_and_follow_9":  {"alt+shift+9"},
		}
	}

	return base
}

// getDefaultLayoutKeybinds returns platform-specific layout keybindings
func getDefaultLayoutKeybinds() map[string][]string {
	// Base layout keybindings (common to all platforms)
	layout := map[string][]string{
		"snap_left":       {"h"},
		"snap_right":      {"l"},
		"snap_fullscreen": {"f"},
		"unsnap":          {"u"},
		// snap_corner_1 through _4 are deliberately absent here. They used to
		// hold "1" through "4", and because the layout table is merged into the
		// window-mode keymap after window_management, they won those keys
		// outright: select_window_1 through _4 never fired in a default
		// install, while 5 through 9 did, because corner snapping stops at 4.
		//
		// Window mode has room for four ordinal digit rows and it already has
		// four: plain digits select a window, shift+digits restore a minimized
		// one, alt+digits switch workspace, alt+shift+digits move a window
		// there. Corner snapping was the fifth claimant, and it is the one of
		// the five whose meaning is not ordinal (a 2x2 grid, not a list) and
		// the only one that does nothing at all in half the modes:
		// makeSnapCornerHandler returns early when tiling is on. So it is the
		// one that gives the digits up.
		//
		// It keeps its action, its handler and this table; it is bound under
		// the layout prefix (leader, L, then 1-4), which is where a layout
		// operation belongs and where the which-key panel can show it.
		"toggle_tiling":        {"t"},
		"swap_left":            {"H", "ctrl+left"},
		"swap_right":           {"L", "ctrl+right"},
		"swap_up":              {"K", "ctrl+up"},
		"swap_down":            {"J", "ctrl+down"},
		"resize_master_shrink": {"<", "shift+,"},
		"resize_master_grow":   {">", "shift+."},
		"resize_height_shrink": {"{", "shift+["},
		"resize_height_grow":   {"}", "shift+]"},
		// resize_master_shrink_left is deliberately absent rather than present and
		// empty: "," is open_settings, and has been since the settings shortcut
		// was a literal checked ahead of the registry, so this binding was
		// already dead. Leaving it in the defaults would put a conflict warning
		// on every fresh config for a key that never worked. Bind it to a free
		// key to get it back.
		"resize_master_grow_left":  {"."},
		"resize_height_shrink_top": {"["},
		"resize_height_grow_top":   {"]"},
		// BSP tiling
		"split_horizontal": {"-"},
		"split_vertical":   {"|", "\\"},
		"rotate_split":     {"R"},
		"equalize_splits":  {"="},
	}

	// Add platform-specific BSP preselect bindings
	if isMacOS() {
		layout["preselect_left"] = []string{"opt+h"}
		layout["preselect_right"] = []string{"opt+l"}
		layout["preselect_up"] = []string{"opt+k"}
		layout["preselect_down"] = []string{"opt+j"}
	} else {
		layout["preselect_left"] = []string{"alt+h"}
		layout["preselect_right"] = []string{"alt+l"}
		layout["preselect_up"] = []string{"alt+k"}
		layout["preselect_down"] = []string{"alt+j"}
	}

	return layout
}

// isMacOS detects if the current platform is macOS
func isMacOS() bool {
	// Check runtime.GOOS first (most reliable)
	if runtime.GOOS == "darwin" {
		return true
	}
	// Fallback to environment variables
	return strings.Contains(strings.ToLower(os.Getenv("GOOS")), "darwin") ||
		strings.Contains(strings.ToLower(os.Getenv("OSTYPE")), "darwin")
}

// clampPercent folds a configured percentage into its range, treating zero as
// "not written" and answering with the default. A percentage option's floor is
// well above zero, so clamping an unwritten value would silently pin it to the
// floor and there would be no way to spell "whatever the default is".
func clampPercent(v, lo, hi, fallback int) int {
	if v == 0 {
		return fallback
	}
	return min(max(v, lo), hi)
}

// LoadUserConfig loads the user configuration from XDG config directory
func LoadUserConfig() (*UserConfig, error) {
	// Try to find existing config file
	configPath, err := xdg.SearchConfigFile("tuios/config.toml")
	if err != nil {
		// Config doesn't exist, create default
		return createDefaultConfig()
	}

	// Read and parse config file
	// #nosec G304 - configPath is from XDG search, reading user config is intentional
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg UserConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate and fill in missing sections with defaults
	defaultCfg := DefaultConfig()
	fillMissingAppearance(&cfg, defaultCfg)
	fillMissingDaemon(&cfg, defaultCfg)
	fillMissingTape(&cfg, defaultCfg)
	fillMissingKeybinds(&cfg, defaultCfg)
	fillMissingScreenshot(&cfg, defaultCfg)
	fillMissingScreensaver(&cfg, defaultCfg)

	// Validate configuration
	validation := ValidateConfig(&cfg)
	if validation.HasErrors() {
		// Log all errors
		for _, err := range validation.Errors {
			fmt.Fprintf(os.Stderr, "Config error in [%s]: %s - %s\n", err.Field, err.Key, err.Message)
		}
		return nil, fmt.Errorf("configuration has %d error(s), please fix and restart", len(validation.Errors))
	}

	// Warnings are deliberately not printed here. Loading happens before the
	// alternate screen is entered, so anything written to stdout or stderr at
	// this point is wiped by the first frame; the previous tea.Println call was
	// worse than that, since its return value (a command) was discarded and it
	// never printed anything at all. The warnings are surfaced inside the TUI
	// instead, by ConfigWarnings below.

	// Loading is pure: it never mutates package globals. Callers apply the
	// appearance globals exactly once, on the Bubble Tea goroutine, via
	// ApplyOverrides (which lets CLI flags win) and/or ApplyAppearanceConfig.
	// This keeps a second load (e.g. inside NewOS) from clobbering CLI flags and
	// stops the per-connection server paths from racing other sessions' globals.
	return &cfg, nil
}

// createDefaultConfig creates a default config file in the user's config directory
func createDefaultConfig() (*UserConfig, error) {
	cfg := DefaultConfig()

	configPath, err := xdg.ConfigFile("tuios/config.toml")
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}

	if err := WriteConfigFile(cfg, configPath); err != nil {
		return nil, err
	}

	return cfg, nil
}

// fillMissingAppearance fills in any missing appearance settings with defaults.
// It only mutates cfg and has no package-global side effects, so it is safe to
// call off the Bubble Tea goroutine (e.g. from the config file watcher).
// Applying the parsed values to package globals is done separately by
// ApplyAppearanceConfig, which must run on the Bubble Tea goroutine.
func fillMissingAppearance(cfg, defaultCfg *UserConfig) {
	migrateLegacySidebar(cfg)

	if cfg.Appearance.BorderStyle == "" {
		cfg.Appearance.BorderStyle = defaultCfg.Appearance.BorderStyle
	}

	if cfg.Appearance.ZenMode == "" {
		cfg.Appearance.ZenMode = defaultCfg.Appearance.ZenMode
	}

	if cfg.Appearance.Links == "" {
		cfg.Appearance.Links = defaultCfg.Appearance.Links
	}

	if cfg.Appearance.DockbarPosition == "" {
		cfg.Appearance.DockbarPosition = defaultCfg.Appearance.DockbarPosition
	}

	if cfg.Appearance.WindowButtonStyle == "" {
		cfg.Appearance.WindowButtonStyle = defaultCfg.Appearance.WindowButtonStyle
	}

	if cfg.Appearance.WindowButtonPosition == "" {
		cfg.Appearance.WindowButtonPosition = defaultCfg.Appearance.WindowButtonPosition
	}

	if cfg.Appearance.Sidebar.Position == "" {
		cfg.Appearance.Sidebar.Position = defaultCfg.Appearance.Sidebar.Position
	}
	if cfg.Appearance.Sidebar.Width <= 0 {
		cfg.Appearance.Sidebar.Width = defaultCfg.Appearance.Sidebar.Width
	}
	if cfg.Appearance.ClickToType == "" {
		cfg.Appearance.ClickToType = defaultCfg.Appearance.ClickToType
	}
	if cfg.Appearance.Glyphs == "" {
		cfg.Appearance.Glyphs = defaultCfg.Appearance.Glyphs
	}
	if cfg.Appearance.Gap < 0 {
		cfg.Appearance.Gap = 0
	} else if cfg.Appearance.Gap > PaneGapMax {
		cfg.Appearance.Gap = PaneGapMax
	}
	// Zero is "not written", which is the common case, so it falls back to the
	// default rather than being clamped up to the floor: a config that never
	// mentioned the master ratio must not read as one that asked for the
	// narrowest master there is.
	if cfg.Appearance.MasterRatio == 0 {
		cfg.Appearance.MasterRatio = MasterRatioDefault
	} else {
		cfg.Appearance.MasterRatio = min(max(cfg.Appearance.MasterRatio, MasterRatioMin), MasterRatioMax)
	}
	if cfg.Appearance.ScrollColumnWidth == 0 {
		cfg.Appearance.ScrollColumnWidth = ScrollColumnWidthDefault
	} else {
		cfg.Appearance.ScrollColumnWidth = min(max(cfg.Appearance.ScrollColumnWidth, ScrollColumnWidthMin), ScrollColumnWidthMax)
	}
	if cfg.Appearance.DimUnfocused < 0 {
		cfg.Appearance.DimUnfocused = 0
	} else if cfg.Appearance.DimUnfocused > DimUnfocusedMax {
		cfg.Appearance.DimUnfocused = DimUnfocusedMax
	}
	if cfg.Appearance.PanelPadding <= 0 {
		cfg.Appearance.PanelPadding = defaultCfg.Appearance.PanelPadding
	} else if cfg.Appearance.PanelPadding > overlay.MaxPanelPadding {
		cfg.Appearance.PanelPadding = overlay.MaxPanelPadding
	}
	if cfg.Appearance.ClockFormat == "" {
		cfg.Appearance.ClockFormat = defaultCfg.Appearance.ClockFormat
	}
	if cfg.Appearance.Scrollbar.Style == "" {
		cfg.Appearance.Scrollbar.Style = defaultCfg.Appearance.Scrollbar.Style
	}
	if cfg.Appearance.Scrollbar.Tint == "" {
		cfg.Appearance.Scrollbar.Tint = defaultCfg.Appearance.Scrollbar.Tint
	}

	// Note: HideWindowButtons defaults to false (zero value)
	// In borderless mode, buttons are hidden automatically regardless of this setting

	// Validate and set scrollback lines (min: 100, max: 1000000)
	if cfg.Appearance.ScrollbackLines <= 0 {
		cfg.Appearance.ScrollbackLines = defaultCfg.Appearance.ScrollbackLines
	} else if cfg.Appearance.ScrollbackLines < 100 {
		cfg.Appearance.ScrollbackLines = 100
	} else if cfg.Appearance.ScrollbackLines > 1000000 {
		cfg.Appearance.ScrollbackLines = 1000000
	}

	// Validate and set wheel scroll speed (min: 1, max: 50)
	if cfg.Appearance.ScrollLines <= 0 {
		cfg.Appearance.ScrollLines = defaultCfg.Appearance.ScrollLines
	} else if cfg.Appearance.ScrollLines > 50 {
		cfg.Appearance.ScrollLines = 50
	}
}

// ApplyAppearanceConfig applies a parsed config file to the package globals the
// render loop and the input handler read. It must be called on the Bubble Tea
// goroutine (from Update or at startup before the program runs), never from the
// file-watcher goroutine, because the globals are read concurrently on the
// render path.
//
// This is the whole of the config-file-to-globals mapping, deliberately: an
// entrypoint that loads a config and calls this gets every setting the settings
// page can write, with nothing left needing a second call. It used to cover
// only part of the [appearance] section and the rest lived in ApplyOverrides,
// so border style, dock position, the dock meters, the scrollbar, the window
// buttons, scrollback, scroll direction, the frame cap and the theme were
// applied by cmd/tuios (which calls both) and silently dropped everywhere else:
// `tuios tape`, the pkg/tuios embed, and every live config reload through
// ConfigReloadedMsg. ApplyOverrides still layers CLI flags on top, so flags
// keep winning where they are set.
func ApplyAppearanceConfig(cfg *UserConfig, s *Settings) {
	// BorderStyle defaults to rounded. Empty means "not configured", so the
	// current value (a flag, or the default) stands.
	if cfg.Appearance.BorderStyle != "" {
		s.BorderStyle = cfg.Appearance.BorderStyle
	}

	// ZenMode only takes one of its three values, so a typo in the config lands
	// on the default the validator warned it would fall back to.
	if slices.Contains(ZenModeModes, cfg.Appearance.ZenMode) {
		s.ZenMode = cfg.Appearance.ZenMode
	} else if cfg.Appearance.ZenMode != "" {
		s.ZenMode = ZenModeDisabled
	}

	// Links takes the same shape: a typo falls back to the default rather than
	// leaving the pointer with no policy at all.
	if slices.Contains(LinkModes, cfg.Appearance.Links) {
		s.Links = cfg.Appearance.Links
	} else if cfg.Appearance.Links != "" {
		s.Links = LinksAll
	}

	// DockbarPosition defaults to bottom.
	if cfg.Appearance.DockbarPosition != "" {
		s.DockbarPosition = cfg.Appearance.DockbarPosition
	}

	// Sidebar. Also runs for a config that never went through LoadUserConfig (the
	// settings page builds one in memory), so an old flat key reaches the globals
	// whichever way the config got here.
	migrateLegacySidebar(cfg)

	// Position, width and the band mode fall back to their defaults when unset;
	// the toggles are pointer bools so turning one off in the settings page
	// survives a reload just as turning it on does.
	sb := cfg.Appearance.Sidebar
	if sb.Enabled != nil {
		s.SidebarEnabled = *sb.Enabled
	}
	if sb.Position != "" {
		s.SidebarPosition = sb.Position
	}
	if sb.Width > 0 {
		s.SidebarWidth = sb.Width
	}
	if sb.ShowGlyphs != nil {
		s.SidebarShowGlyphs = *sb.ShowGlyphs
	}
	if sb.ShowCounts != nil {
		s.SidebarShowCounts = *sb.ShowCounts
	}
	if sb.Marquee != nil {
		s.SidebarMarquee = *sb.Marquee
	}
	// The layout falls back to the shipped one when unset, so a config file
	// written before the files section existed lays the rail out the way it
	// always did plus the new section, rather than drawing nothing at all.
	//
	// Assigned rather than left alone, so an empty value puts the rail back on
	// the shipped layout instead of leaving it on whatever was set last. That
	// is what lets the editor's undo hand a layout it never wrote back to the
	// default, rather than writing today's four sections into a file that named
	// none and pinning that user to them for good.
	s.SidebarSections = SidebarDefaultSections
	if sb.Sections != "" {
		s.SidebarSections = sb.Sections
	}
	if sb.FileIcons != nil {
		s.SidebarFileIcons = *sb.FileIcons
	}
	if sb.FileIconColors != nil {
		s.SidebarFileIconColors = *sb.FileIconColors
	}
	if sb.FolderClick != "" {
		s.SidebarFolderClick = sb.FolderClick
	}
	if sb.FileActions != nil {
		s.SidebarFileActions = *sb.FileActions
	}
	if sb.FileDelete != "" {
		s.SidebarFileDelete = sb.FileDelete
	}
	if sb.Tooltips != nil {
		s.Tooltips = *sb.Tooltips
	}
	if cfg.Appearance.DockWorkspaceTabs != nil {
		s.DockWorkspaceTabs = *cfg.Appearance.DockWorkspaceTabs
	}
	// DockWorkspaceTabFormat is assigned unconditionally: an empty string is the
	// "{name}" default, and clearing it on reload has to be possible too.
	s.DockWorkspaceTabFormat = cfg.Appearance.DockWorkspaceTabFormat
	if cfg.Appearance.DockWorkspaceTooltip != nil {
		s.DockWorkspaceTooltip = *cfg.Appearance.DockWorkspaceTooltip
	}
	if cfg.Appearance.DockPillCaps != nil {
		s.DockPillCaps = *cfg.Appearance.DockPillCaps
	}
	if cfg.Appearance.SessionColors != nil {
		s.SessionColors = *cfg.Appearance.SessionColors
	}
	if cfg.Appearance.Scrollbar.Style != "" {
		s.ScrollbarStyle = cfg.Appearance.Scrollbar.Style
	}
	// The glyph and tint keys are assigned as written, empty included: empty is
	// the "use the style's default" state, and the getters below resolve it. A
	// value that does not measure one cell is rejected there too, so an edit
	// made live in the settings page cannot slip past the file's validation.
	s.ScrollbarThumb = cfg.Appearance.Scrollbar.Thumb
	s.ScrollbarTrack = cfg.Appearance.Scrollbar.Track
	s.ScrollbarTint = cfg.Appearance.Scrollbar.Tint

	// The hide/show toggles are plain bools with no "unset" state, so they are
	// assigned unconditionally: turning one off in the settings page has to
	// survive a reload just as turning it on does.
	s.HideWindowButtons = cfg.Appearance.HideWindowButtons
	if cfg.Appearance.WindowButtonStyle != "" {
		s.WindowButtonStyle = cfg.Appearance.WindowButtonStyle
	}
	if cfg.Appearance.WindowButtonPosition != "" {
		s.WindowButtonPosition = cfg.Appearance.WindowButtonPosition
	}
	s.HideScrollbar = cfg.Appearance.HideScrollbar
	s.ShowClock = cfg.Appearance.ShowClock
	s.ClockFormat = cfg.Appearance.ClockFormat
	s.PaneGap = min(max(cfg.Appearance.Gap, 0), PaneGapMax)
	s.MasterRatioPercent = clampPercent(cfg.Appearance.MasterRatio, MasterRatioMin, MasterRatioMax, MasterRatioDefault)
	s.ScrollColumnWidth = clampPercent(cfg.Appearance.ScrollColumnWidth, ScrollColumnWidthMin, ScrollColumnWidthMax, ScrollColumnWidthDefault)
	s.DimUnfocused = min(max(cfg.Appearance.DimUnfocused, 0), DimUnfocusedMax)
	overlay.SetPanelPadding(cfg.Appearance.PanelPadding)
	// The glyph set is selected here rather than beside the theme, because it
	// is read through the config globals the render path already goes to and
	// this is the one funnel those come through. Re-selecting an unchanged id
	// costs a resolve of a handful of strings.
	s.GlyphSet = cfg.Appearance.Glyphs
	if s.GlyphSet == "" {
		s.GlyphSet = theme.GlyphSetNone
	}
	theme.SetActiveGlyphs(s.GlyphSet)
	s.ShowCPU = cfg.Appearance.ShowCPU
	s.ShowRAM = cfg.Appearance.ShowRAM
	s.NiriReverseScroll = cfg.Appearance.NiriReverseScroll

	if cfg.Appearance.ScrollbackLines > 0 {
		s.ScrollbackLines = cfg.Appearance.ScrollbackLines
	}

	if cfg.Appearance.MaxFPS > 0 {
		s.NormalFPS = clampMaxFPS(cfg.Appearance.MaxFPS)
	}

	// LeaderKey lives in [keybindings] rather than [appearance], but it is a
	// package global fed by the same file and it had the same gap.
	if cfg.Keybindings.LeaderKey != "" {
		s.LeaderKey = cfg.Keybindings.LeaderKey
	}

	// AnimationsEnabled defaults to true (nil means use default)
	// Only set global if explicitly configured
	if cfg.Appearance.AnimationsEnabled != nil {
		s.AnimationsEnabled = *cfg.Appearance.AnimationsEnabled
	}

	// ConfirmQuit defaults to false (nil means use default)
	if cfg.Appearance.ConfirmQuit != nil {
		s.AlwaysConfirmQuit = *cfg.Appearance.ConfirmQuit
	}

	// SharedBorders defaults to true (nil means use default)
	if cfg.Appearance.SharedBorders != nil {
		s.SharedBorders = *cfg.Appearance.SharedBorders
	}

	// WhichKeyEnabled defaults to true (nil means use default)
	if cfg.Appearance.WhichKeyEnabled != nil {
		s.WhichKeyEnabled = *cfg.Appearance.WhichKeyEnabled
	}

	// WhichKeyPosition defaults to bottom-right
	if cfg.Appearance.WhichKeyPosition != "" {
		s.WhichKeyPosition = cfg.Appearance.WhichKeyPosition
	}

	// WindowTitlePosition defaults to bottom
	if cfg.Appearance.WindowTitlePosition != "" {
		s.WindowTitlePosition = cfg.Appearance.WindowTitlePosition
	}

	// HideClock defaults to false
	s.HideClock = cfg.Appearance.HideClock

	// WindowTitleFormat defaults to empty, meaning the title is shown as-is.
	// An empty string in the config also clears a previously set format on
	// reload, which is why it is assigned unconditionally.
	s.WindowTitleFormat = cfg.Appearance.WindowTitleFormat

	// ScrollLines (lines per wheel notch)
	if cfg.Appearance.ScrollLines > 0 {
		s.ScrollLines = cfg.Appearance.ScrollLines
	}

	// CopyOnSelect defaults to true (nil means use default)
	if cfg.Appearance.CopyOnSelect != nil {
		s.CopyOnSelect = *cfg.Appearance.CopyOnSelect
	}

	// FocusFollowsMouse defaults to false; a pointer so turning it off in the
	// settings page survives a reload just as turning it on does.
	if cfg.Appearance.FocusFollowsMouse != nil {
		s.FocusFollowsMouse = *cfg.Appearance.FocusFollowsMouse
	}

	// AltDrag defaults to true; a pointer so an explicit false in the config
	// survives the default being on.
	if cfg.Appearance.AltDrag != nil {
		s.AltDrag = *cfg.Appearance.AltDrag
	}

	// ClickToType only takes one of its three values, so a typo in the config
	// lands on the default the validator warned it would fall back to.
	if slices.Contains(ClickToTypeModes, cfg.Appearance.ClickToType) {
		s.ClickToType = cfg.Appearance.ClickToType
	} else if cfg.Appearance.ClickToType != "" {
		s.ClickToType = ClickToTypeSingle
	}

	// WordCharacters is a pointer so an explicitly empty string can mean "no
	// punctuation is part of a word", which is different from "unset".
	if cfg.Appearance.WordCharacters != nil {
		s.WordCharacters = *cfg.Appearance.WordCharacters
	}

	// ZoomMaxWidth (0 = fullscreen)
	if cfg.Appearance.ZoomMaxWidth > 0 {
		s.ZoomMaxWidth = cfg.Appearance.ZoomMaxWidth
	}

	// Theme. An empty name means "no theme, use the terminal's own colors",
	// which is also the startup state, so there is nothing to undo and the
	// initialize is skipped. This matches ApplyOverrides, which then re-applies
	// with the --theme flag's name when one was given.
	if cfg.Appearance.Theme != "" {
		if err := theme.Initialize(cfg.Appearance.Theme); err != nil {
			log.Printf("Warning: Failed to load theme '%s': %v", cfg.Appearance.Theme, err)
		}
	}

	// Custom border colors override the theme-derived colors. Empty strings
	// clear any override and restore theme colors. This runs after the theme
	// switch above, which resets the derived colors.
	theme.SetBorderOverrides(cfg.Appearance.BorderFocusedColor, cfg.Appearance.BorderUnfocusedColor)

	// [notifications] rides along here rather than in its own funnel because
	// this is the one function every path that loads a config file already
	// calls: the tape runner, the SSH server, the command palette's save, and
	// the live reload. A second entry point is a second thing to forget.
	ApplyNotificationConfig(cfg, s)
}

// clampMaxFPS folds a configured max_fps into the range the tick loop can
// actually drive. Both apply passes call it, so a config file cannot mean two
// different frame rates depending on which one ran: ApplyOverrides runs alone
// for the entrypoints that have no ApplyAppearanceConfig, and both run (in that
// order) for cmd/tuios.
func clampMaxFPS(fps int) int {
	return max(min(fps, MaxFPSCap), MinConfiguredFPS)
}

// ApplyNotificationConfig applies the [notifications] section to the package
// globals the renderer reads. Absent or non-positive values leave the built-in
// default in place rather than collapsing a message to zero seconds, which is
// the failure mode the old 1500ms default was already close enough to.
func ApplyNotificationConfig(cfg *UserConfig, s *Settings) {
	if cfg.Notifications.Duration > 0 {
		s.NotificationDuration = time.Duration(cfg.Notifications.Duration) * time.Second
	}
	if cfg.Notifications.WarningDuration > 0 {
		s.NotificationWarningDuration = time.Duration(cfg.Notifications.WarningDuration) * time.Second
	}
	if cfg.Notifications.ErrorDuration > 0 {
		s.NotificationErrorDuration = time.Duration(cfg.Notifications.ErrorDuration) * time.Second
	}
	// ErrorSticky defaults to true, so nil means "keep the default" and only an
	// explicit false turns it off.
	if cfg.Notifications.ErrorSticky != nil {
		s.NotificationErrorSticky = *cfg.Notifications.ErrorSticky
	}
}

// fillMissingTape fills in any missing tape settings with defaults. An unset or
// unrecognized autorun mode falls back to the safe default ("ask"); validation
// reports the unrecognized value separately as a warning.
func fillMissingTape(cfg, defaultCfg *UserConfig) {
	if !slices.Contains(TapeAutorunModes, cfg.Tape.Autorun) {
		cfg.Tape.Autorun = defaultCfg.Tape.Autorun
	}
}

// fillMissingDaemon fills in any missing daemon settings with defaults
func fillMissingDaemon(cfg, defaultCfg *UserConfig) {
	if cfg.Daemon.LogLevel == "" {
		cfg.Daemon.LogLevel = defaultCfg.Daemon.LogLevel
	}
}

// migrateLegacySidebar folds the flat appearance.sidebar_* keys into the
// [appearance.sidebar] table, so a config written before the table existed keeps
// behaving identically. The table wins where both are present, and each legacy
// field is cleared once read: that makes the migration idempotent and keeps the
// next save from writing the old spelling back out.
func migrateLegacySidebar(cfg *UserConfig) {
	a := &cfg.Appearance
	s := &a.Sidebar
	if s.Enabled == nil {
		s.Enabled = a.SidebarEnabled
	}
	if s.Position == "" {
		s.Position = a.SidebarPosition
	}
	if s.Width <= 0 {
		s.Width = a.SidebarWidth
	}
	if s.ShowWindows == nil {
		s.ShowWindows = a.SidebarShowWindows
	}
	if s.ShowGlyphs == nil {
		s.ShowGlyphs = a.SidebarShowGlyphs
	}
	if s.ShowCounts == nil {
		s.ShowCounts = a.SidebarShowCounts
	}
	a.SidebarEnabled, a.SidebarPosition, a.SidebarWidth = nil, "", 0
	a.SidebarShowWindows, a.SidebarShowGlyphs, a.SidebarShowCounts = nil, nil, nil

	migrateSidebarSectionToggles(s)
}

// migrateSidebarSectionToggles folds show_windows and show_agents into the
// layout string, and clears them.
//
// There used to be two ways to turn a section off: leave its name out of
// sections, or set the boolean named after it. Neither could express the other.
// The layout knows where a section goes and the boolean does not, and a layout
// may carry two spacers while a boolean per section has nowhere to put the
// second one, so the layout is the one that survives. A file still carrying a
// boolean keeps its meaning here, once, and the next save writes only sections.
//
// Only false is folded. A boolean that says true is saying nothing the layout
// does not already say, and adding a section back on the strength of it would
// put a section on the rail of somebody who had taken it off the layout.
func migrateSidebarSectionToggles(s *SidebarConfig) {
	for _, legacy := range []struct {
		flag    *bool
		section string
	}{
		{s.ShowWindows, "terminals"},
		{s.ShowAgents, "agents"},
	} {
		if legacy.flag == nil || *legacy.flag {
			continue
		}
		source := s.Sections
		if source == "" {
			source = SidebarDefaultSections
		}
		s.Sections = SidebarSectionsWithout(source, legacy.section)
	}
	s.ShowWindows, s.ShowAgents = nil, nil
}

// migrateLegacyKeybinds rewrites bindings whose meaning changed, so a config
// written by an older version keeps behaving the way its author expects.
//
// Older defaults bound prefix_detach to both "d" and "esc" while the prefix
// handler hard-coded esc to leave terminal mode and never detach. Now that the
// binding is what actually runs, leaving esc on prefix_detach would start
// detaching the session on a key that used to just switch modes, so it moves to
// the prefix_exit_mode action that carries the old behaviour.
// It must run before the defaults are filled in, so that the presence of
// prefix_exit_mode still distinguishes a config written by a version that knew
// about the split (leave it alone) from an older one (migrate it).
func migrateLegacyKeybinds(cfg *UserConfig) {
	if _, ok := cfg.Keybindings.PrefixMode["prefix_exit_mode"]; ok {
		return
	}
	detach := cfg.Keybindings.PrefixMode["prefix_detach"]
	if !slices.Contains(detach, "esc") {
		return
	}
	remaining := slices.DeleteFunc(slices.Clone(detach), func(key string) bool {
		return key == "esc"
	})
	cfg.Keybindings.PrefixMode["prefix_detach"] = remaining
	cfg.Keybindings.PrefixMode["prefix_exit_mode"] = []string{"esc"}
}

// migrateSettingsComma takes "," off resize_master_shrink_left in a config
// written while the settings shortcut was a literal in the input path.
//
// That literal was checked ahead of the registry, so the layout binding it
// shadowed had not run since the shortcut landed. Now that settings is
// open_settings in mode_control the two are a visible clash, and a user who
// never chose either would get a startup warning about a key that had already
// stopped working. Removing the dead claim leaves the config saying what the
// program was already doing.
//
// Only the exact stale default is touched. A user who put "," on the resize
// action themselves has it alongside their own keys, and taking someone's
// deliberate binding away to quiet a warning would be the worse bug.
func migrateSettingsComma(cfg *UserConfig) {
	if _, ok := cfg.Keybindings.ModeControl["open_settings"]; ok {
		return // written by a version that knew about open_settings
	}
	if keys := cfg.Keybindings.Layout["resize_master_shrink_left"]; len(keys) == 1 && keys[0] == "," {
		delete(cfg.Keybindings.Layout, "resize_master_shrink_left")
	}
}

// migrateCornerSnapDigits takes the bare digits off snap_corner_1 through _4 in
// a config written before corner snapping moved to the layout prefix.
//
// The old defaults put snap_corner_N on "1" through "4" in the layout table.
// That table is merged into the window-mode keymap after window_management, so
// it won those keys outright and select_window_1 through _4 never fired. Every
// config ever written carries those four lines, and fillMapDefaults only adds
// actions that are missing, so without this the fix would reach new installs
// only and everyone else would go on seeing four conflicts they never caused.
//
// It must run before the defaults are filled in, so that the presence of a
// corner binding under layout_prefix still distinguishes a config written by a
// version that knew about the move (leave it alone) from an older one.
//
// Only the exact stale default is removed. A user who put snap_corner_1
// somewhere of their own, or who kept the digit alongside another key, has made
// a choice, and taking it away to quiet a warning would be the worse bug.
func migrateCornerSnapDigits(cfg *UserConfig) {
	for i := 1; i <= 4; i++ {
		action := "snap_corner_" + strconv.Itoa(i)
		if _, ok := cfg.Keybindings.LayoutPrefix[action]; ok {
			return // written by a version that knew about the move
		}
	}
	for i := 1; i <= 4; i++ {
		action := "snap_corner_" + strconv.Itoa(i)
		if keys := cfg.Keybindings.Layout[action]; len(keys) == 1 && keys[0] == strconv.Itoa(i) {
			delete(cfg.Keybindings.Layout, action)
		}
	}
}

// fillMissingKeybinds fills in any missing keybindings with defaults
func fillMissingKeybinds(cfg, defaultCfg *UserConfig) {
	// Initialize nil maps
	if cfg.Keybindings.WindowManagement == nil {
		cfg.Keybindings.WindowManagement = make(map[string][]string)
	}
	if cfg.Keybindings.Workspaces == nil {
		cfg.Keybindings.Workspaces = make(map[string][]string)
	}
	if cfg.Keybindings.Layout == nil {
		cfg.Keybindings.Layout = make(map[string][]string)
	}
	if cfg.Keybindings.ModeControl == nil {
		cfg.Keybindings.ModeControl = make(map[string][]string)
	}
	if cfg.Keybindings.System == nil {
		cfg.Keybindings.System = make(map[string][]string)
	}
	if cfg.Keybindings.Navigation == nil {
		cfg.Keybindings.Navigation = make(map[string][]string)
	}
	if cfg.Keybindings.RestoreMinimized == nil {
		cfg.Keybindings.RestoreMinimized = make(map[string][]string)
	}
	if cfg.Keybindings.PrefixMode == nil {
		cfg.Keybindings.PrefixMode = make(map[string][]string)
	}
	if cfg.Keybindings.WindowPrefix == nil {
		cfg.Keybindings.WindowPrefix = make(map[string][]string)
	}
	if cfg.Keybindings.MinimizePrefix == nil {
		cfg.Keybindings.MinimizePrefix = make(map[string][]string)
	}
	if cfg.Keybindings.WorkspacePrefix == nil {
		cfg.Keybindings.WorkspacePrefix = make(map[string][]string)
	}
	if cfg.Keybindings.DebugPrefix == nil {
		cfg.Keybindings.DebugPrefix = make(map[string][]string)
	}
	if cfg.Keybindings.TapePrefix == nil {
		cfg.Keybindings.TapePrefix = make(map[string][]string)
	}
	if cfg.Keybindings.LayoutPrefix == nil {
		cfg.Keybindings.LayoutPrefix = make(map[string][]string)
	}
	if cfg.Keybindings.TerminalMode == nil {
		cfg.Keybindings.TerminalMode = make(map[string][]string)
	}
	if cfg.Keybindings.Global == nil {
		cfg.Keybindings.Global = make(map[string][]string)
	}
	if cfg.Keybindings.Script == nil {
		cfg.Keybindings.Script = make(map[string][]string)
	}
	if cfg.Keybindings.Sidebar == nil {
		cfg.Keybindings.Sidebar = make(map[string][]string)
	}
	if cfg.Keybindings.SidebarFiles == nil {
		cfg.Keybindings.SidebarFiles = make(map[string][]string)
	}

	migrateLegacyKeybinds(cfg)
	migrateSettingsComma(cfg)
	migrateCornerSnapDigits(cfg)

	// Set default leader key if not specified
	if cfg.Keybindings.LeaderKey == "" {
		cfg.Keybindings.LeaderKey = defaultCfg.Keybindings.LeaderKey
	}

	// Fill in missing keys with defaults
	fillMapDefaults(cfg.Keybindings.WindowManagement, defaultCfg.Keybindings.WindowManagement)
	fillMapDefaults(cfg.Keybindings.Workspaces, defaultCfg.Keybindings.Workspaces)
	fillMapDefaults(cfg.Keybindings.Layout, defaultCfg.Keybindings.Layout)
	fillMapDefaults(cfg.Keybindings.ModeControl, defaultCfg.Keybindings.ModeControl)
	fillMapDefaults(cfg.Keybindings.System, defaultCfg.Keybindings.System)
	fillMapDefaults(cfg.Keybindings.Navigation, defaultCfg.Keybindings.Navigation)
	fillMapDefaults(cfg.Keybindings.RestoreMinimized, defaultCfg.Keybindings.RestoreMinimized)
	fillMapDefaults(cfg.Keybindings.PrefixMode, defaultCfg.Keybindings.PrefixMode)
	fillMapDefaults(cfg.Keybindings.WindowPrefix, defaultCfg.Keybindings.WindowPrefix)
	fillMapDefaults(cfg.Keybindings.MinimizePrefix, defaultCfg.Keybindings.MinimizePrefix)
	fillMapDefaults(cfg.Keybindings.WorkspacePrefix, defaultCfg.Keybindings.WorkspacePrefix)
	fillMapDefaults(cfg.Keybindings.DebugPrefix, defaultCfg.Keybindings.DebugPrefix)
	fillMapDefaults(cfg.Keybindings.TapePrefix, defaultCfg.Keybindings.TapePrefix)
	fillMapDefaults(cfg.Keybindings.LayoutPrefix, defaultCfg.Keybindings.LayoutPrefix)
	fillMapDefaults(cfg.Keybindings.TerminalMode, defaultCfg.Keybindings.TerminalMode)
	// A config written before the global and script sections existed has neither,
	// so without this the palette and the launcher would come back unbound for
	// every existing user rather than keeping the keys they already had.
	fillMapDefaults(cfg.Keybindings.Global, defaultCfg.Keybindings.Global)
	fillMapDefaults(cfg.Keybindings.Script, defaultCfg.Keybindings.Script)
	// A config written before the rail scope existed has no sidebar section. Left
	// unfilled it resolves every rail key to nothing, and since the scope swallows
	// unbound keys that traps the keyboard in the rail with no way out.
	fillMapDefaults(cfg.Keybindings.Sidebar, defaultCfg.Keybindings.Sidebar)
	// And the files section's own keys, for the same reason: a config written
	// before they existed has no sidebar_files section, and left unfilled the
	// listing would have no way to create, rename or delete anything.
	fillMapDefaults(cfg.Keybindings.SidebarFiles, defaultCfg.Keybindings.SidebarFiles)

	for _, section := range keybindSectionPairs(cfg, defaultCfg) {
		dropStaleDuplicateKeys(section.target, section.defaults)
	}
}

func fillMapDefaults(target, defaults map[string][]string) {
	for k, v := range defaults {
		if _, exists := target[k]; !exists {
			target[k] = v
		}
	}
}

// keybindSection pairs a config section with the defaults for that section.
type keybindSection struct{ target, defaults map[string][]string }

func keybindSectionPairs(cfg, defaultCfg *UserConfig) []keybindSection {
	c, d := &cfg.Keybindings, &defaultCfg.Keybindings
	return []keybindSection{
		{c.WindowManagement, d.WindowManagement},
		{c.Workspaces, d.Workspaces},
		{c.Layout, d.Layout},
		{c.ModeControl, d.ModeControl},
		{c.System, d.System},
		{c.Navigation, d.Navigation},
		{c.RestoreMinimized, d.RestoreMinimized},
		{c.PrefixMode, d.PrefixMode},
		{c.WindowPrefix, d.WindowPrefix},
		{c.MinimizePrefix, d.MinimizePrefix},
		{c.WorkspacePrefix, d.WorkspacePrefix},
		{c.DebugPrefix, d.DebugPrefix},
		{c.TapePrefix, d.TapePrefix},
		{c.LayoutPrefix, d.LayoutPrefix},
		{c.TerminalMode, d.TerminalMode},
		{c.Sidebar, d.Sidebar},
		{c.SidebarFiles, d.SidebarFiles},
		{c.Global, d.Global},
		{c.Script, d.Script},
	}
}

// dropStaleDuplicateKeys resolves a key that two actions in the same section
// both claim, in favour of the action that owns it by default.
//
// A default binding that moves from one action to another leaves the old key
// behind in every config written before the move: the file already names the
// old action with the key, and fillMapDefaults only adds the new action, so
// both end up on it. That is how "," ended up on prefix_rename_window and
// prefix_settings at once, which made the leader chord a coin flip between
// renaming a pane and opening settings. Dropping the key from the action whose
// own default no longer lists it leaves the binding a fresh config would have.
//
// A key no default claims, or one two defaults claim, is left alone: the first
// is the user's own arrangement to resolve, the second is a defaults bug that
// silently picking a winner would hide. Nor is an action's last key ever taken,
// however the defaults read: a sole binding is a deliberate choice, and only a
// redundant extra one can be residue. ValidateConfig warns about what is left.
func dropStaleDuplicateKeys(section, defaults map[string][]string) {
	claimants := make(map[string][]string)
	for action, keys := range section {
		for _, key := range keys {
			claimants[key] = append(claimants[key], action)
		}
	}

	for key, actions := range claimants {
		if len(actions) < 2 {
			continue
		}
		owner := ""
		for _, action := range actions {
			if !slices.Contains(defaults[action], key) {
				continue
			}
			if owner != "" {
				owner = ""
				break
			}
			owner = action
		}
		if owner == "" {
			continue
		}
		for _, action := range actions {
			if action == owner || len(section[action]) < 2 {
				continue
			}
			section[action] = slices.DeleteFunc(slices.Clone(section[action]), func(k string) bool {
				return k == key
			})
		}
	}
}

// ConfigWarnings returns the non-fatal problems in cfg as human-readable lines,
// for surfacing inside the running TUI. Config problems used to be reported
// only to a stream nobody sees, so a typo in a keybinding looked like the
// feature was broken rather than like the config was.
func ConfigWarnings(cfg *UserConfig) []string {
	if cfg == nil {
		return nil
	}
	validation := ValidateConfig(cfg)
	lines := make([]string, 0, len(validation.Errors)+len(validation.Warnings))
	for _, issue := range validation.Errors {
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", issue.Field, issue.Key, issue.Message))
	}
	for _, issue := range validation.Warnings {
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", issue.Field, issue.Key, issue.Message))
	}
	return lines
}

// GetConfigPath returns the path to the config file
func GetConfigPath() (string, error) {
	path, err := xdg.SearchConfigFile("tuios/config.toml")
	if err != nil {
		// Return where it would be created
		return xdg.ConfigFile("tuios/config.toml")
	}
	return path, nil
}
