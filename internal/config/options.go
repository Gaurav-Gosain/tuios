package config

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Option describes one settable configuration path.
//
// The registry below is what lets a caller outside the process discover and
// change a setting. Before it, a runtime set-config knew six hardcoded paths,
// so the sidebar, the dock and everything else in the file were reachable only
// by editing the file and reloading.
type Option struct {
	Path        string   `json:"path"`                 // dotted toml path, e.g. "appearance.sidebar.position"
	Type        string   `json:"type"`                 // bool, int or string
	Section     string   `json:"section"`              // grouping for display
	Description string   `json:"description"`          // one line
	Accepted    []string `json:"accepted,omitempty"`   // closed value set, when there is one
	Default     string   `json:"default"`              // what DefaultConfig holds, rendered as a string
	Min         int      `json:"min,omitempty"`        // int options only
	Max         int      `json:"max,omitempty"`        // int options only, and only when a range is enforced
	Deprecated  string   `json:"deprecated,omitempty"` // why it is deprecated and what replaced it
	// Color marks a string option whose value is a colour literal. The type
	// stays string because that is what the field holds and what crosses the
	// protocol; this says what the string means, which is what lets the settings
	// panel offer a colour picker instead of a text field and what makes an
	// unparseable colour an error at the CLI rather than a broken border later.
	//
	// A colour option with Accepted set takes either one of those keywords or a
	// literal, which is the scrollbar tint's shape.
	Color bool `json:"color,omitempty"`
	// Theme marks the string option whose value is a registered theme id. Like
	// Color it says what the string means rather than what it is, and for the
	// same reason: the set is open (a user's own theme file joins it) and far
	// too long to publish as Accepted, so neither a closed set nor no check at
	// all is right. Without it a misspelled theme was recorded, reported as
	// applied, and drew the palette it already had.
	Theme bool `json:"theme,omitempty"`
	// GlyphSet marks the string option whose value is a glyph set id. Like
	// Theme it names an open set kept in a directory of its own, so neither an
	// Accepted list nor no check at all is right, and for the same reason:
	// without it a misspelled set is recorded, reported as applied, and draws
	// the glyphs it already had.
	GlyphSet bool `json:"glyph_set,omitempty"`
}

// The three types an option can carry. A config value crosses the protocol as a
// string, so these say how to parse it back.
const (
	OptionBool   = "bool"
	OptionInt    = "int"
	OptionString = "string"
)

// The enum sets shared by the registry, the validator and the settings page,
// so one spelling serves all three.
var (
	DockbarPositions     = []string{"bottom", "top", "hidden"}
	SidebarPositions     = []string{"left", "right", "hidden"}
	WhichKeyPositions    = []string{"bottom-right", "bottom-left", "top-right", "top-left", "center"}
	WindowTitlePositions = []string{"bottom", "top", "hidden"}
	daemonLogLevels      = []string{"off", "errors", "basic", "messages", "verbose", "trace"}
)

// optionSpecs is the registry, hand-written so each entry can say what the
// setting is for in the words the struct already uses.
//
// Default is what DefaultConfig writes, which for several keys is the zero
// value meaning "use the built-in default"; where the two differ the
// description names the effective one. A nil pointer field reads back as
// Default, since nil is exactly the unset state.
//
// [keybindings] and [hooks] are absent: both are maps of name to value rather
// than fixed scalar paths, so a set-by-path verb is the wrong shape for them.
var optionSpecs = []Option{
	// [appearance]
	{
		Path: "appearance.border_style", Type: OptionString, Section: "appearance",
		Description: "Border style drawn around every pane",
		Accepted:    BorderStyles, Default: "rounded",
	},
	{
		Path: "appearance.zen_mode", Type: OptionString, Section: "appearance",
		Description: "When window borders are hidden: never, always, or while the mouse is idle",
		Accepted:    ZenModeModes, Default: ZenModeDisabled,
	},
	{
		Path: "appearance.hide_window_buttons", Type: OptionBool, Section: "appearance",
		Description: "Hide the minimize, maximize and close buttons",
		Default:     "false",
	},
	{
		Path: "appearance.window_button_style", Type: OptionString, Section: "appearance",
		Description: "Window controls as a filled pill or as macOS traffic lights",
		Accepted:    WindowButtonStyles, Default: WindowButtonStylePill,
	},
	{
		Path: "appearance.window_button_position", Type: OptionString, Section: "appearance",
		Description: "Which end of the title bar the window controls sit on",
		Accepted:    WindowButtonPositions, Default: WindowButtonPositionRight,
	},
	{
		Path: "appearance.hide_scrollbar", Type: OptionBool, Section: "appearance",
		Description: "Hide the scrollbar thumb on the pane border",
		Default:     "false",
	},
	{
		Path: "appearance.scrollback_lines", Type: OptionInt, Section: "appearance",
		Description: "Lines kept in each pane's scrollback buffer",
		Default:     "10000", Min: 100, Max: 1000000,
	},
	{
		Path: "appearance.scroll_lines", Type: OptionInt, Section: "appearance",
		Description: "Lines scrolled per mouse wheel notch",
		Default:     "3", Min: 1, Max: 50,
	},
	{
		Path: "appearance.copy_on_select", Type: OptionBool, Section: "appearance",
		Description: "Copy a mouse selection to the clipboard on release",
		Default:     "true",
	},
	{
		Path: "appearance.focus_follows_mouse", Type: OptionBool, Section: "appearance",
		Description: "Focus the pane under the cursor as the mouse moves",
		Default:     "false",
	},
	{
		Path: "appearance.alt_drag", Type: OptionBool, Section: "appearance",
		Description: "Alt plus left-drag moves a pane",
		Default:     "true",
	},
	{
		Path: "appearance.click_to_type", Type: OptionString, Section: "appearance",
		Description: "What a click on a pane's content does in window-management mode",
		Accepted:    ClickToTypeModes, Default: ClickToTypeSingle,
	},
	{
		Path: "appearance.word_characters", Type: OptionString, Section: "appearance",
		Description: "Punctuation that counts as part of a word for double-click selection",
		Default:     `@-./_~?&=%+#`,
	},
	{
		Path: "appearance.preferred_shell", Type: OptionString, Section: "appearance",
		Description: "Shell new panes run; empty auto-detects for the platform",
		Default:     "",
	},
	{
		Path: "appearance.animations_enabled", Type: OptionBool, Section: "appearance",
		Description: "Animate UI transitions instead of applying them instantly",
		Default:     "true",
	},
	{
		Path: "appearance.confirm_quit", Type: OptionBool, Section: "appearance",
		Description: "Always confirm on quit, not only when processes are running",
		Default:     "false",
	},
	{
		Path: "appearance.whichkey_enabled", Type: OptionBool, Section: "appearance",
		Description: "Show the which-key popup after the leader key",
		Default:     "true",
	},
	{
		Path: "appearance.whichkey_position", Type: OptionString, Section: "appearance",
		Description: "Corner the which-key popup opens in (empty: bottom-right)",
		Accepted:    WhichKeyPositions, Default: "",
	},
	{
		Path: "appearance.window_title_position", Type: OptionString, Section: "appearance",
		Description: "Edge of the pane the title is drawn on (empty: bottom)",
		Accepted:    WindowTitlePositions, Default: "",
	},
	{
		Path: "appearance.theme", Type: OptionString, Section: "appearance",
		Description: "Colour theme name; empty keeps the terminal's own colours",
		Default:     "", Theme: true,
	},
	{
		Path: "appearance.shared_borders", Type: OptionBool, Section: "appearance",
		Description: "Share one border between adjacent tiled panes",
		Default:     "false",
	},
	{
		Path: "appearance.border_focused_color", Type: OptionString, Section: "appearance",
		Description: "Hex colour overriding the focused pane's border, e.g. #89b4fa",
		Default:     "", Color: true,
	},
	{
		Path: "appearance.border_unfocused_color", Type: OptionString, Section: "appearance",
		Description: "Hex colour overriding an unfocused pane's border, e.g. #585b70",
		Default:     "", Color: true,
	},
	{
		Path: "appearance.window_title_format", Type: OptionString, Section: "appearance",
		Description: "Title template accepting {title}, {index} and {cwd}",
		Default:     "",
	},
	{
		Path: "appearance.zoom_max_width", Type: OptionInt, Section: "appearance",
		Description: "Width in cells a zoomed pane is centred at; 0 is fullscreen",
		Default:     "0", Min: 0,
	},
	{
		Path: "appearance.niri_reverse_scroll", Type: OptionBool, Section: "appearance",
		Description: "Reverse the wheel direction in niri scrolling mode",
		Default:     "false",
	},
	{
		Path: "appearance.max_fps", Type: OptionInt, Section: "appearance",
		Description: "Frame cap for the render loop; 0 uses 60, and the value is clamped to 10..240",
		Default:     "0", Min: 0, Max: MaxFPSCap,
	},
	{
		Path: "appearance.session_colors", Type: OptionBool, Section: "appearance",
		Description: "Give each session its own colour on the rail and in the switcher",
		Default:     "true",
	},
	{
		Path: "appearance.glyphs", Type: OptionString, Section: "appearance",
		Description: "Chrome glyph set: the characters the border, controls, rules and rail marks are drawn with",
		Default:     theme.GlyphSetNone, GlyphSet: true,
	},
	{
		Path: "appearance.gap", Type: OptionInt, Section: "appearance",
		Description: "Cells of empty ground kept between two neighbouring tiled panes",
		Default:     "0", Min: 0, Max: PaneGapMax,
	},
	{
		Path: "appearance.panel_padding", Type: OptionInt, Section: "appearance",
		Description: "Columns of padding each side of an overlay panel's content",
		Default:     strconv.Itoa(overlay.DefaultPanelPadding), Min: 1, Max: overlay.MaxPanelPadding,
	},
	{
		Path: "appearance.clock_format", Type: OptionString, Section: "dock",
		Description: "Go time layout the clock is drawn with, e.g. 15:04 or Mon 3:04PM",
		Default:     DefaultClockFormat,
	},

	// The flat sidebar keys a config written before [appearance.sidebar] used.
	// Still read, and folded into the table on load, so they are listed for a
	// caller inspecting an old file rather than for setting anything new.
	{
		Path: "appearance.sidebar_enabled", Type: OptionBool, Section: "appearance",
		Description: "Show the session rail",
		Default:     "false",
		Deprecated:  "folded into [appearance.sidebar] on load; set appearance.sidebar.enabled",
	},
	{
		Path: "appearance.sidebar_position", Type: OptionString, Section: "appearance",
		Description: "Edge the session rail sits on",
		Accepted:    SidebarPositions, Default: "",
		Deprecated: "folded into [appearance.sidebar] on load; set appearance.sidebar.position",
	},
	{
		Path: "appearance.sidebar_width", Type: OptionInt, Section: "appearance",
		Description: "Preferred rail width in columns",
		Default:     "0", Min: 0,
		Deprecated: "folded into [appearance.sidebar] on load; set appearance.sidebar.width",
	},
	{
		Path: "appearance.sidebar_show_windows", Type: OptionBool, Section: "appearance",
		Description: "Show the terminals section on the rail",
		Default:     "true",
		Deprecated:  "folded into [appearance.sidebar] on load; set appearance.sidebar.show_windows",
	},
	{
		Path: "appearance.sidebar_show_glyphs", Type: OptionBool, Section: "appearance",
		Description: "Show the agent-state glyph on each rail row",
		Default:     "true",
		Deprecated:  "folded into [appearance.sidebar] on load; set appearance.sidebar.show_glyphs",
	},
	{
		Path: "appearance.sidebar_show_counts", Type: OptionBool, Section: "appearance",
		Description: "Show the window count on each session row",
		Default:     "true",
		Deprecated:  "folded into [appearance.sidebar] on load; set appearance.sidebar.show_counts",
	},

	// The dock and the readouts on it.
	{
		Path: "appearance.dockbar_position", Type: OptionString, Section: "dock",
		Description: "Edge the dock sits on, or hidden",
		Accepted:    DockbarPositions, Default: "bottom",
	},
	{
		Path: "appearance.dock_workspace_tabs", Type: OptionBool, Section: "dock",
		Description: "Show the clickable workspace strip in the dock",
		Default:     "true",
	},
	{
		Path: "appearance.dock_workspace_tab_format", Type: OptionString, Section: "dock",
		Description: "Workspace tab template accepting {index} and {name} (empty: {name})",
		Default:     "",
	},
	{
		Path: "appearance.dock_workspace_tooltip", Type: OptionBool, Section: "dock",
		Description: "Pop a truncated workspace name in full on hover",
		Default:     "true",
	},
	{
		Path: "appearance.dock_pill_caps", Type: OptionBool, Section: "dock",
		Description: "Draw powerline caps on the dock's pills instead of flat ends",
		Default:     "false",
	},
	{
		Path: "appearance.show_clock", Type: OptionBool, Section: "dock",
		Description: "Show the clock overlay",
		Default:     "false",
	},
	{
		Path: "appearance.hide_clock", Type: OptionBool, Section: "dock",
		Description: "Hide the clock overlay",
		Default:     "false",
		Deprecated:  "superseded by appearance.show_clock, which says the same thing the right way round",
	},
	{
		Path: "appearance.show_cpu", Type: OptionBool, Section: "dock",
		Description: "Show the CPU graph in the dock",
		Default:     "false",
	},
	{
		Path: "appearance.show_ram", Type: OptionBool, Section: "dock",
		Description: "Show RAM usage in the dock",
		Default:     "false",
	},

	// [appearance.scrollbar]. The glyphs and the tint take values no closed set
	// covers, so they carry no Accepted and validation reports a bad one.
	{
		Path: "appearance.scrollbar.style", Type: OptionString, Section: "scrollbar",
		Description: "Hairline thumb over the content column, or a full-height track",
		Accepted:    ScrollbarStyles, Default: ScrollbarStyleThin,
	},
	{
		Path: "appearance.scrollbar.thumb", Type: OptionString, Section: "scrollbar",
		Description: "One-cell thumb glyph; empty takes the style's own",
		Default:     "",
	},
	{
		Path: "appearance.scrollbar.track", Type: OptionString, Section: "scrollbar",
		Description: "One-cell track glyph, or none; empty takes the style's own",
		Default:     "",
	},
	{
		Path: "appearance.scrollbar.tint", Type: OptionString, Section: "scrollbar",
		Description: "Bar colour: quiet, border, muted, or a #RRGGBB literal",
		Accepted:    ScrollbarTints, Default: ScrollbarTintQuiet, Color: true,
	},

	// [appearance.sidebar]
	{
		Path: "appearance.sidebar.enabled", Type: OptionBool, Section: "sidebar",
		Description: "Show the session rail",
		Default:     "false",
	},
	{
		Path: "appearance.sidebar.position", Type: OptionString, Section: "sidebar",
		Description: "Edge the rail sits on, or hidden",
		Accepted:    SidebarPositions, Default: "left",
	},
	{
		Path: "appearance.sidebar.width", Type: OptionInt, Section: "sidebar",
		Description: "Preferred rail width in columns on a wide screen",
		Default:     strconv.Itoa(SidebarDefaultWidth), Min: 0,
	},
	{
		Path: "appearance.sidebar.show_windows", Type: OptionBool, Section: "sidebar",
		Description: "Show the terminals section",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.show_glyphs", Type: OptionBool, Section: "sidebar",
		Description: "Show the agent-state glyph on each row",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.show_counts", Type: OptionBool, Section: "sidebar",
		Description: "Show the window count on each session row",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.show_agents", Type: OptionBool, Section: "sidebar",
		Description: "Show the agents section at the rail's bottom",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.marquee", Type: OptionBool, Section: "sidebar",
		Description: "Scroll a hovered row's overflowing title",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.tooltips", Type: OptionBool, Section: "sidebar",
		Description: "Label the collapsed strip on hover",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.workspaces", Type: OptionString, Section: "sidebar",
		Description: "Workspace chip band the rail used to draw",
		Default:     "",
		Deprecated:  "no longer used: panes name their own workspace, and switching lives on the dock and alt+1..9",
	},

	// [startup]
	{
		Path: "startup.open_default_window", Type: OptionBool, Section: "startup",
		Description: "Open one terminal automatically when a session starts empty",
		Default:     "false",
	},
	{
		Path: "startup.tiled", Type: OptionBool, Section: "startup",
		Description: "Start a new session tiled instead of floating",
		Default:     "false",
	},
	{
		Path: "startup.start_in_terminal_mode", Type: OptionBool, Section: "startup",
		Description: "Start focused in terminal mode, when a window is present",
		Default:     "false",
	},
	{
		Path: "startup.daemon", Type: OptionBool, Section: "startup",
		Description: "Make a bare \"tuios\" attach to a daemon-backed session instead of running standalone. TUIOS_NO_DAEMON=1 or --standalone overrides it",
		Default:     "false",
	},

	// [daemon]. agent_binaries is absent: it is a list, and a value that arrives
	// as one string has no unambiguous spelling for a list.
	{
		Path: "daemon.log_level", Type: OptionString, Section: "daemon",
		Description: "How much the daemon logs",
		Accepted:    daemonLogLevels, Default: "off",
	},
	{
		Path: "daemon.agent_autodetect", Type: OptionBool, Section: "daemon",
		Description: "Detect a pane's foreground agent CLI and set its state glyph",
		Default:     "true",
	},
	{
		Path: "daemon.agent_detect_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds between detector polls; 0 uses 2, and a negative value turns detection off",
		Default:     "0",
	},

	// [notifications]
	{
		Path: "notifications.duration", Type: OptionInt, Section: "notifications",
		Description: "Seconds an info or success message stays up; 0 uses the built-in default",
		Default:     "0", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.warning_duration", Type: OptionInt, Section: "notifications",
		Description: "Seconds a warning stays up; 0 uses the built-in default",
		Default:     "0", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.error_duration", Type: OptionInt, Section: "notifications",
		Description: "Seconds an error stays up when error_sticky is false",
		Default:     "0", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.error_sticky", Type: OptionBool, Section: "notifications",
		Description: "Make errors wait for esc instead of expiring",
		Default:     "true",
	},

	// [notifications.agent]. The sounds table is absent: its two keys are paths
	// to files, which no accepted set or range can check.
	{
		Path: "notifications.agent.enabled", Type: OptionBool, Section: "notifications",
		Description: "Master switch for agent-state alerts",
		Default:     "true",
	},
	{
		Path: "notifications.agent.notify", Type: OptionBool, Section: "notifications",
		Description: "Send a desktop notification to the attached terminal",
		Default:     "true",
	},
	{
		Path: "notifications.agent.sound", Type: OptionBool, Section: "notifications",
		Description: "Make an alert audible",
		Default:     "false",
	},
	{
		Path: "notifications.agent.sound_mode", Type: OptionString, Section: "notifications",
		Description: "How an audible alert sounds: a played cue, a BEL, or both",
		Accepted:    AgentSoundModeNames, Default: "",
	},
	{
		Path: "notifications.agent.sound_cooldown_seconds", Type: OptionInt, Section: "notifications",
		Description: "Shortest gap between two cues across every pane; 0 uses 3",
		Default:     "3", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.agent.dock", Type: OptionBool, Section: "notifications",
		Description: "Show the alert in the dock, where it jumps to the pane that raised it",
		Default:     "true",
	},
	{
		Path: "notifications.agent.command", Type: OptionString, Section: "notifications",
		Description: "Shell command run on an alert; empty runs nothing",
		Default:     "",
	},
	{
		Path: "notifications.agent.settle_seconds", Type: OptionInt, Section: "notifications",
		Description: "Hold an alert this long and drop it if the pane leaves the state",
		Default:     "2", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.agent.suppress_focused", Type: OptionBool, Section: "notifications",
		Description: "Drop alerts for the pane the user is already looking at",
		Default:     "true",
	},
	{
		Path: "notifications.agent.quiet_hours", Type: OptionString, Section: "notifications",
		Description: "Local-time window written HH:MM-HH:MM in which nothing alerts",
		Default:     "",
	},

	// [notifications.agent.states]
	{
		Path: "notifications.agent.states.needs_input", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent blocks on the user",
		Default:     "true",
	},
	{
		Path: "notifications.agent.states.errored", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent stops on an error",
		Default:     "true",
	},
	{
		Path: "notifications.agent.states.done", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent reports it finished",
		Default:     "true",
	},
	{
		Path: "notifications.agent.states.idle", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent goes quiet, which the stall timer guesses from silence",
		Default:     "false",
	},
	{
		Path: "notifications.agent.states.working", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent starts work",
		Default:     "false",
	},

	// [tape]
	{
		Path: "tape.autorun", Type: OptionString, Section: "tape",
		Description: "What happens on entering a directory with a project tape",
		Accepted:    TapeAutorunModes, Default: TapeAutorunAsk,
	},
	{
		Path: "tape.auto_review", Type: OptionBool, Section: "tape",
		Description: "Open the review dialog on detection instead of only badging it",
		Default:     "false",
	},

	// [debug]
	{
		Path: "debug.show_key_events", Type: OptionBool, Section: "debug",
		Description: "Show the on-screen keycast of recent keypresses",
		Default:     "false",
	},
}

// optionsByPath indexes the registry for lookup. Built once at init so a caller
// resolving a path per keystroke does not walk the table.
var optionsByPath = func() map[string]Option {
	byPath := make(map[string]Option, len(optionSpecs))
	for _, opt := range optionSpecs {
		byPath[opt.Path] = opt
	}
	return byPath
}()

// Options returns every settable option, sorted by path.
func Options() []Option {
	out := slices.Clone(optionSpecs)
	slices.SortFunc(out, func(a, b Option) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// LookupOption returns the option a dotted path names.
func LookupOption(path string) (Option, bool) {
	opt, ok := optionsByPath[path]
	return opt, ok
}

// OptionPaths returns every path, sorted, for a did-you-mean hint on a typo.
func OptionPaths() []string {
	paths := make([]string, 0, len(optionSpecs))
	for _, opt := range optionSpecs {
		paths = append(paths, opt.Path)
	}
	slices.Sort(paths)
	return paths
}

// checkValue rejects a value the option cannot hold, before any of it reaches
// the config struct.
//
// A colour option is why this is no longer just the Accepted membership test.
// The two border colours take any literal and so carry no Accepted set, which
// meant nothing checked them at all: set-option appearance.border_focused_color
// notacolour was accepted, written, and turned up later as a border drawn in
// nothing. The tint has a keyword set and a literal form at once, which a closed
// set on its own cannot say.
func (o Option) checkValue(value string) error {
	if o.Color {
		// Empty is how a colour option says unset: the border falls back to the
		// theme's, the tint to its own default. Clearing has to stay reachable.
		if value == "" || slices.Contains(o.Accepted, value) || IsHexColor(value) {
			return nil
		}
		if len(o.Accepted) > 0 {
			return fmt.Errorf("%s: %q is not a colour; expected #RRGGBB, one of %s, or empty",
				o.Path, value, strings.Join(o.Accepted, ", "))
		}
		return fmt.Errorf("%s: %q is not a colour; expected #RRGGBB or empty", o.Path, value)
	}
	if o.Theme && !theme.Exists(value) {
		// Exists re-reads the custom themes directory before it says no, so a
		// theme file written a moment ago resolves here rather than on the next
		// restart.
		return fmt.Errorf("%s: no theme named %q; call list-themes for the ones there are, "+
			"or write %s.json in the themes directory first", o.Path, value, value)
	}
	if o.GlyphSet && !theme.GlyphSetExists(value) {
		// GlyphSetExists re-reads the glyphs directory before it says no, for
		// the reason Exists does: a set written a moment ago has to resolve on
		// this call rather than on the next restart.
		return fmt.Errorf("%s: no glyph set named %q; call list-glyphs for the ones there are, "+
			"or write %s.json in the glyphs directory first", o.Path, value, value)
	}
	if len(o.Accepted) > 0 && !slices.Contains(o.Accepted, value) {
		return fmt.Errorf("%s: %q is not one of %s", o.Path, value, strings.Join(o.Accepted, ", "))
	}
	return nil
}

// SetOptionValue validates value against the option's type and accepted set,
// then writes it to cfg.
func SetOptionValue(cfg *UserConfig, path, value string) error {
	opt, ok := LookupOption(path)
	if !ok {
		return fmt.Errorf("unknown config option %q", path)
	}
	if err := opt.checkValue(value); err != nil {
		return err
	}
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return fmt.Errorf("unknown config option %q", path)
	}

	switch opt.Type {
	case OptionBool:
		parsed, err := parseOptionBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if optionKind(field) != reflect.Bool {
			return fmt.Errorf("%s: registry says bool, config field is %s", path, optionKind(field))
		}
		optionTarget(field).SetBool(parsed)
	case OptionInt:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s: %q is not a whole number", path, value)
		}
		if opt.Max > 0 && (parsed < opt.Min || parsed > opt.Max) {
			return fmt.Errorf("%s: %d is outside %d..%d", path, parsed, opt.Min, opt.Max)
		}
		if optionKind(field) != reflect.Int {
			return fmt.Errorf("%s: registry says int, config field is %s", path, optionKind(field))
		}
		optionTarget(field).SetInt(int64(parsed))
	case OptionString:
		if optionKind(field) != reflect.String {
			return fmt.Errorf("%s: registry says string, config field is %s", path, optionKind(field))
		}
		optionTarget(field).SetString(value)
	default:
		return fmt.Errorf("%s: registry carries no type", path)
	}
	return nil
}

// GetOptionValue reads the current value of a path as a string. A nil pointer
// field reads back as the option's default, since nil is the unset state and
// the default is what the app will act on. ok is false only for a path the
// registry does not carry.
func GetOptionValue(cfg *UserConfig, path string) (string, bool) {
	opt, ok := LookupOption(path)
	if !ok {
		return "", false
	}
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return "", false
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return opt.Default, true
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(field.Bool()), true
	case reflect.Int:
		return strconv.FormatInt(field.Int(), 10), true
	case reflect.String:
		return field.String(), true
	default:
		return "", false
	}
}

// resolveOptionField walks cfg one path segment at a time, matching each
// segment against a field's toml tag, and returns the settable field the last
// segment names.
func resolveOptionField(cfg *UserConfig, path string) (reflect.Value, bool) {
	if cfg == nil || path == "" {
		return reflect.Value{}, false
	}
	value := reflect.ValueOf(cfg).Elem()
	for _, segment := range strings.Split(path, ".") {
		if value.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		field, ok := fieldByTOMLName(value, segment)
		if !ok {
			return reflect.Value{}, false
		}
		value = field
	}
	return value, true
}

// fieldByTOMLName finds the field of a struct whose toml tag is name.
func fieldByTOMLName(value reflect.Value, name string) (reflect.Value, bool) {
	structType := value.Type()
	for i := range structType.NumField() {
		if tomlFieldName(structType.Field(i)) == name {
			return value.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// tomlFieldName is a field's toml key with any option such as omitempty
// stripped. A field with no tag returns empty, which matches no path segment.
func tomlFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
	return name
}

// optionKind is the kind a field holds, seeing through a pointer.
func optionKind(field reflect.Value) reflect.Kind {
	fieldType := field.Type()
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	return fieldType.Kind()
}

// optionTarget is the value to write into, allocating a nil pointer first.
// Those fields are pointers so an explicitly set value survives a reload that a
// zero value would be indistinguishable from, and writing through the pointer
// is what preserves that.
func optionTarget(field reflect.Value) reflect.Value {
	if field.Kind() != reflect.Pointer {
		return field
	}
	if field.IsNil() {
		field.Set(reflect.New(field.Type().Elem()))
	}
	return field.Elem()
}

// optionBoolWords are the spellings of true and false a caller may send. A
// control surface is typed by hand as often as by a program, so "on" and
// "enabled" have to mean what they look like.
var optionBoolWords = map[string]bool{
	"true": true, "on": true, "1": true, "yes": true, "enabled": true,
	"false": false, "off": false, "0": false, "no": false, "disabled": false,
}

func parseOptionBool(value string) (bool, error) {
	parsed, ok := optionBoolWords[strings.ToLower(strings.TrimSpace(value))]
	if !ok {
		return false, fmt.Errorf("%q is not a true or false value", value)
	}
	return parsed, nil
}
