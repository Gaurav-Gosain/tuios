package config

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
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
	// Percent marks an int option whose value is a share of something, with
	// Min and Max as the real ends of its travel rather than as a guard against
	// a silly number. Like Color and Theme it says what the value means rather
	// than what it is, and it is what earns the option a gauge on the settings
	// panel and a % after its number.
	//
	// The line it draws is between a proportion and a count. Both are ints with
	// a Max, but a gauge on a count says nothing: notifications.duration allows
	// up to an hour, so the usual four seconds would draw an empty bar, and
	// appearance.scrollback_lines allows a million, so ten thousand would too.
	// A proportion is the case
	// where the far end is a place you would actually put the value, which is
	// the only case where seeing how far along it sits tells you anything.
	Percent bool `json:"percent,omitempty"`
	// Follows names the option whose value this one takes while it is unset.
	// Such an option takes the empty string as a value, which clears it, so
	// it can go back to following once it has been set.
	Follows string `json:"follows,omitempty"`
	// DefaultClears marks a string option whose Default is a word rather than
	// a value. Writing that word clears the field back to unset, and an unset
	// field reads back as it. It is for an option where the empty string is a
	// value of its own: an empty mode icon hides the icon, so the empty string
	// cannot also be the way back to the built-in.
	DefaultClears bool `json:"default_clears,omitempty"`
	// Icon marks a string option whose value is drawn in the dock as an icon.
	// A value with a control character, or wider than DockModeIconMaxWidth
	// cells, is refused, because it would break the dock row's layout.
	Icon bool `json:"icon,omitempty"`
	// BoxSize marks a string option whose value is a size in cells (60) or
	// percent (80%), read by ParseBoxSize. An empty value means the default.
	BoxSize bool `json:"box_size,omitempty"`
	// Auto marks an int option that also takes the word "auto". Its config
	// field is a string type, such as FPSLimit, that can hold either.
	Auto bool `json:"auto,omitempty"`
}

// UnsetText is how a reader is told an option is unset and what it follows,
// for an option with Follows. Empty for any other option.
func (o Option) UnsetText() string {
	if o.Follows == "" {
		return ""
	}
	return "(follows " + o.Follows + ")"
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

// The shipped edges for the dock, the rail and the window title. Named because
// DefaultConfig, DefaultSettings, the registry and the typo fallback in
// ApplyAppearanceConfig all have to agree on them. v0.8.0 moved all three: the
// dock and the title were at the bottom and the rail on the left before it.
const (
	DefaultDockbarPosition     = "top"
	DefaultSidebarPosition     = "right"
	DefaultWindowTitlePosition = "top"
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
		Path: "appearance.links", Type: OptionString, Section: "appearance",
		Description: "Links the pointer picks up: off, only marked links, or plain URLs too",
		Accepted:    LinkModes, Default: LinksAll,
	},
	{
		Path: "appearance.link_click", Type: OptionString, Section: "appearance",
		Description: "The click that opens a link: ctrl or shift, ctrl only, shift only, or none",
		Accepted:    LinkClickModes, Default: LinkClickBoth,
	},
	{
		Path: "appearance.link_hover", Type: OptionString, Section: "appearance",
		Description: "The hover highlight: safe skips a pane whose guest tracks the mouse, always draws it everywhere",
		Accepted:    LinkHoverModes, Default: LinkHoverSafe,
	},
	{
		Path: "appearance.link_opener", Type: OptionString, Section: "appearance",
		Description: "The command that opens a web link. If this is empty, tuios uses $BROWSER, then the system opener.",
		Default:     "",
	},
	{
		Path: "appearance.hide_window_buttons", Type: OptionBool, Section: "appearance",
		Description: "Hide the minimize, maximize and close buttons",
		Default:     "false",
	},
	{
		Path: "appearance.window_button_style", Type: OptionString, Section: "appearance",
		Description: "Window controls as a filled pill or as macOS traffic lights",
		Accepted:    WindowButtonStyles, Default: WindowButtonStyleDots,
	},
	{
		Path: "appearance.window_button_position", Type: OptionString, Section: "appearance",
		Description: "Which end of the title bar the window controls sit on",
		Accepted:    WindowButtonPositions, Default: WindowButtonPositionLeft,
	},
	{
		Path: "appearance.hide_scrollbar", Type: OptionBool, Section: "appearance",
		Description: "Hide the scrollbar thumb on the pane border",
		Default:     "false",
	},
	{
		Path: "appearance.scrollback_lines", Type: OptionInt, Section: "appearance",
		Description: "Lines each pane keeps in its scrollback. A pane takes the value when it is made. The daemon reads it when it starts",
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
		Path: "appearance.nvim_navigation", Type: OptionBool, Section: "appearance",
		Description: "Let the Neovim navigator plugin hand terminal focus keys back to TUIOS",
		Default:     "false",
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
		Path: "appearance.right_click_opens_menu", Type: OptionBool, Section: "appearance",
		Description: "A plain right-click on a pane in terminal mode opens the pane menu, for pasting without selecting first",
		Default:     "false",
	},
	{
		Path: "appearance.kitty_placeholders", Type: OptionString, Section: "appearance",
		Description: "Draw images an app positions with kitty Unicode placeholders: auto, on, off",
		Accepted:    KittyPlaceholderModes, Default: KittyPlaceholdersAuto,
	},
	{
		Path: "appearance.image_symbols", Type: OptionString, Section: "appearance",
		Description: "Draw pane images as block glyphs when the terminal has no graphics: auto, octant, sextant, quadrant, half, off",
		Accepted:    ImageSymbolModes, Default: ImageSymbolsAuto,
	},
	{
		Path: "appearance.new_window_inherit_cwd", Type: OptionBool, Section: "appearance",
		Description: "A new window starts in the focused pane's working directory",
		Default:     "true",
	},
	{
		Path: "appearance.new_window_follow_ssh", Type: OptionBool, Section: "appearance",
		Description: "A split or new window of a pane that runs ssh runs the same ssh",
		Default:     "false",
	},
	{
		Path: "appearance.click_to_type", Type: OptionString, Section: "appearance",
		Description: "What a click on a pane's content does in window-management mode",
		Accepted:    ClickToTypeModes, Default: ClickToTypeDouble,
	},
	{
		Path: "appearance.auto_enter_terminal_on_focus", Type: OptionString, Section: "appearance",
		Description: "Type in a pane when a keyboard focus command moves to it: off, targeted (select and arrows), or all (including Tab)",
		Accepted:    AutoEnterTerminalModes, Default: string(AutoEnterTerminalOff),
	},
	{
		Path: "appearance.word_characters", Type: OptionString, Section: "appearance",
		Description: "Punctuation that counts as part of a word for double-click selection",
		Default:     `@-./_~?&=%+#`,
	},
	{
		Path: "appearance.preferred_shell", Type: OptionString, Section: "appearance",
		Description: "Shell that new panes run. Empty picks one for your platform.",
		Default:     "",
	},
	{
		Path: "appearance.motion", Type: OptionString, Section: "appearance",
		Description: "How much moves: none, basic (window slides and the copy sweep), or full (also overlay fades and the working-agent shimmer)",
		Accepted:    MotionLevels, Default: MotionFull,
	},
	{
		Path: "appearance.modal_dim", Type: OptionInt, Section: "appearance",
		Description: "How much the screen behind a modal panel is darkened, as a percent. 0 is off.",
		Default:     strconv.Itoa(ModalDimDefault), Min: 0, Max: ModalDimMax,
		Percent: true,
	},
	{
		Path: "appearance.animations_enabled", Type: OptionBool, Section: "appearance",
		Description: "Animate UI transitions instead of applying them instantly",
		Default:     "true",
		Deprecated:  "folded into appearance.motion on load (false is none); set appearance.motion",
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
		Path: "appearance.wrap_lists", Type: OptionBool, Section: "appearance",
		Description: "Up on a list's first row goes to its last, and down on the last goes to the first",
		Default:     "true",
	},
	{
		Path: "appearance.whichkey_position", Type: OptionString, Section: "appearance",
		Description: "Corner the which-key popup opens in (empty: bottom-right)",
		Accepted:    WhichKeyPositions, Default: "",
	},
	{
		Path: "appearance.window_title_position", Type: OptionString, Section: "appearance",
		Description: "Edge of the pane the title is drawn on",
		Accepted:    WindowTitlePositions, Default: DefaultWindowTitlePosition,
	},
	{
		Path: "appearance.theme", Type: OptionString, Section: "appearance",
		Description: "Colour theme name. Empty keeps your terminal's own colours.",
		Default:     "", Theme: true,
	},
	{
		Path: "appearance.shared_borders", Type: OptionBool, Section: "appearance",
		Description: "Draw one border between neighbouring tiled panes, in every layout",
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
		Description: "Width in cells of a zoomed pane. 0 fills the screen.",
		Default:     "0", Min: 0,
	},
	{
		Path: "appearance.niri_reverse_scroll", Type: OptionBool, Section: "appearance",
		Description: "Reverse the wheel direction in niri scrolling mode",
		Default:     "false",
	},
	{
		Path: "appearance.max_fps", Type: OptionInt, Section: "appearance",
		Description: fmt.Sprintf("Highest frame rate tuios draws at. Your terminal and monitor can show fewer frames. "+
			"A value above what they can show does not make tuios smoother. "+
			"0 uses %d. auto uses the refresh rate of your display. The range is %d to %d.",
			DefaultFPS, MinConfiguredFPS, MaxFPSCap),
		Default: "0", Min: 0, Max: MaxFPSCap, Auto: true,
	},
	{
		Path: "appearance.session_colors", Type: OptionBool, Section: "appearance",
		Description: "Give each session its own colour on the rail and in the switcher",
		Default:     "true",
	},
	{
		Path: "appearance.session_border", Type: OptionBool, Section: "appearance",
		Description: "Carry the session's colour on every pane border, not only on the rail",
		Default:     "false",
	},
	{
		Path: "appearance.global_session", Type: OptionBool, Section: "appearance",
		Description: "Offer a session in the rail that holds panes from several machines, once a second one is reachable",
		Default:     "true",
	},
	{
		Path: "appearance.niri_click_reveals", Type: OptionBool, Section: "appearance",
		Description: "In the scrolling layout, clicking a column that is partly off the edge brings all of it on screen",
		Default:     "true",
	},
	{
		Path: "appearance.niri_hover_reveals", Type: OptionBool, Section: "appearance",
		Description: "In the scrolling layout with focus-follows-mouse on, hovering a column that is partly off the edge brings all of it on screen",
		Default:     "true",
	},
	{
		Path: "appearance.git_dirty", Type: OptionBool, Section: "sidebar",
		Description: "Count staged, changed and untracked paths in the rail's git section. The only part of it that costs a walk of the working tree.",
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
		Path: "appearance.tiling_scheme", Type: OptionString, Section: "appearance",
		Description: "BSP scheme a workspace inserts new windows with the first time it is tiled. A workspace already tiled keeps its own scheme.",
		Accepted:    TilingSchemes, Default: TilingSchemeSpiral,
	},
	{
		Path: "appearance.master_ratio", Type: OptionInt, Section: "appearance",
		Description: "Width of the master pane in the master-stack layout, as a percent of the screen",
		Default:     strconv.Itoa(MasterRatioDefault), Min: MasterRatioMin, Max: MasterRatioMax,
		Percent: true,
	},
	{
		Path: "appearance.master_position", Type: OptionString, Section: "appearance",
		Description: "Side the master panes take in the master-stack layout. Center puts the stack on both sides.",
		Accepted:    MasterPositions, Default: MasterPositionLeft,
	},
	{
		Path: "appearance.master_count", Type: OptionInt, Section: "appearance",
		Description: "How many panes are master panes in the master-stack layout",
		Default:     strconv.Itoa(MasterCountDefault), Min: MasterCountMin, Max: MasterCountMax,
	},
	{
		Path: "appearance.master_grid", Type: OptionBool, Section: "appearance",
		Description: "With one master on the left, show four or more panes as a grid",
		Default:     "true",
	},
	{
		Path: "appearance.scroll_column_width", Type: OptionInt, Section: "appearance",
		Description: "Width of a column in the scrolling layout, as a percent of the screen",
		Default:     strconv.Itoa(ScrollColumnWidthDefault), Min: ScrollColumnWidthMin, Max: ScrollColumnWidthCeiling,
		Percent: true,
	},
	{
		Path: "appearance.zoom_size", Type: OptionInt, Section: "appearance",
		Description: "How much of the screen a zoomed pane takes, as a percent. Below 100 the layout around it stays visible at the edges.",
		Default:     strconv.Itoa(ZoomSizeDefault),
		Min:         ZoomSizeMin, Max: ZoomSizeMax,
		Percent: true,
	},
	{
		Path: "appearance.window_button_zoom", Type: OptionBool, Section: "appearance",
		Description: "Carry the third title bar control on a tiled pane, where it toggles the zoom",
		Default:     "true",
	},
	{
		Path: "appearance.zoom_follows_focus", Type: OptionBool, Section: "appearance",
		Description: "Hand the zoom to the pane the focus lands on, so moving focus while zoomed shows the pane you moved to",
		Default:     "true",
	},
	{
		Path: "appearance.zoom_borderless", Type: OptionBool, Section: "appearance",
		Description: "Show a zoomed pane on the whole pane region with no border or title bar. Zoom size and zoom max width do not apply.",
		Default:     "false",
	},
	{
		Path: "appearance.zoom_animation", Type: OptionBool, Section: "appearance",
		Description: "Slide a pane between its tile and the zoom box instead of swapping the two in one frame",
		Default:     "true",
	},
	{
		Path: "appearance.scroll_column_max", Type: OptionInt, Section: "appearance",
		Description: "Highest a column's width may be set to in the scrolling layout, as a percent. 100 lets a column fill the screen, at the cost of the next one peeking in at the edge.",
		Default:     strconv.Itoa(ScrollColumnWidthMax),
		Min:         ScrollColumnWidthMin, Max: ScrollColumnWidthCeiling,
		Percent: true,
	},
	{
		Path: "appearance.niri_scroll_cells", Type: OptionInt, Section: "appearance",
		Description: "Cells the scrolling layout's strip moves per mouse wheel event",
		Default:     strconv.Itoa(NiriScrollCellsDefault), Min: NiriScrollCellsMin, Max: NiriScrollCellsMax,
	},
	{
		Path: "appearance.prefix_repeat_time", Type: OptionInt, Section: "appearance",
		Description: "Milliseconds the prefix stays armed after a repeatable command. 0 turns it off.",
		Default:     strconv.Itoa(PrefixRepeatTimeDefault),
		Min:         PrefixRepeatTimeMin, Max: PrefixRepeatTimeMax,
	},
	{
		Path: "appearance.panel_padding", Type: OptionInt, Section: "appearance",
		Description: "Columns of padding each side of an overlay panel's content",
		Default:     strconv.Itoa(overlay.DefaultPanelPadding), Min: 1, Max: overlay.MaxPanelPadding,
	},
	{
		Path: "appearance.dim_unfocused", Type: OptionInt, Section: "appearance",
		Description: "How much tuios fades a pane you are not in, as a percent. 0 is off.",
		Default:     "0", Min: 0, Max: DimUnfocusedMax,
		Percent: true,
	},
	{
		Path: "appearance.dim_multifocus", Type: OptionBool, Section: "appearance",
		Description: "Dim the panes in the multifocus set like other panes you are not in",
		Default:     "false",
	},
	// The backgrounds. appearance.background is the default for every surface;
	// each surface's own option overrides it, and empty follows it. See
	// ResolveBackground.
	{
		Path: "appearance.background", Type: OptionString, Section: "appearance",
		Description: "Background painted on every surface not set on its own (panes, desktop, window chrome, dock, rail): off (your terminal shows through), theme, or a #RRGGBB literal",
		Accepted:    Backgrounds, Default: BackgroundOff, Color: true,
	},
	{
		Path: "appearance.pane_background", Type: OptionString, Section: "appearance",
		Description: "Background behind pane content: off, theme, or a #RRGGBB literal; empty follows appearance.background",
		Accepted:    Backgrounds, Default: "", Color: true,
	},
	{
		Path: "appearance.desktop_background", Type: OptionString, Section: "appearance",
		Description: "Background behind and between panes, including gaps and an empty workspace: off, theme, or a #RRGGBB literal; empty follows appearance.background",
		Accepted:    Backgrounds, Default: "", Color: true,
	},
	{
		Path: "appearance.window_chrome_background", Type: OptionString, Section: "appearance",
		Description: "Background under pane borders, title bars and the lines between shared-border panes: off, theme, or a #RRGGBB literal; empty follows appearance.background",
		Accepted:    Backgrounds, Default: "", Color: true,
	},
	{
		Path: "appearance.dock_background", Type: OptionString, Section: "dock",
		Description: "Background under the dock: off, theme, or a #RRGGBB literal; empty follows appearance.background",
		Accepted:    Backgrounds, Default: "", Color: true,
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
		Accepted:    DockbarPositions, Default: DefaultDockbarPosition,
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
		Path: "appearance.dock_workspace_label_max", Type: OptionInt, Section: "dock",
		Description: "Cells a workspace pill's label may span before the pill cuts it (0 draws the whole name and scrolls the strip instead)",
		Default:     "12", Min: 0, Max: 200,
	},
	{
		Path: "appearance.dock_pill_caps", Type: OptionBool, Section: "dock",
		Description: "Draw rounded caps on every dock pill: the mode chip, the workspace tabs and the minimized windows. Off draws flat pills",
		Default:     "true",
	},
	{
		Path: "appearance.dock_mode_icon_window", Type: OptionString, Section: "dock",
		Description: "Icon in the mode pill in window mode. An empty value shows no icon. default uses the icon of the glyph set",
		Default:     DockModeIconDefault, DefaultClears: true, Icon: true,
	},
	{
		Path: "appearance.dock_mode_icon_terminal", Type: OptionString, Section: "dock",
		Description: "Icon in the mode pill in terminal mode. An empty value shows no icon. default uses the icon of the glyph set",
		Default:     DockModeIconDefault, DefaultClears: true, Icon: true,
	},
	{
		Path: "appearance.dock_mode_icon_tiling", Type: OptionString, Section: "dock",
		Description: "Icon in the mode pill while tiling is on. An empty value shows no icon. default uses the icon of the glyph set",
		Default:     DockModeIconDefault, DefaultClears: true, Icon: true,
	},
	{
		Path: "appearance.dock_compact", Type: OptionBool, Section: "dock",
		Description: "Draw the dock as one row with no rule. The panes get one more row",
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
		Accepted:    ScrollbarStyles, Default: ScrollbarStyleTrack,
	},
	{
		Path: "appearance.scrollbar.thumb", Type: OptionString, Section: "scrollbar",
		Description: "One-cell glyph for the thumb. Empty uses the style's own.",
		Default:     "",
	},
	{
		Path: "appearance.scrollbar.track", Type: OptionString, Section: "scrollbar",
		Description: "One-cell glyph for the track, or none. Empty uses the style's own.",
		Default:     "",
	},
	{
		Path: "appearance.scrollbar.tint", Type: OptionString, Section: "scrollbar",
		Description: "Bar colour: quiet, border, muted, or a #RRGGBB literal",
		Accepted:    ScrollbarTints, Default: ScrollbarTintQuiet, Color: true,
	},

	// [appearance.selection]. Every background is a colour literal; a
	// foreground may also be empty, which keeps the text the colour the
	// program wrote it in. No closed set covers a colour, so none carries
	// Accepted and validation reports a bad one.
	{
		Path: "appearance.selection.bg", Type: OptionString, Section: "selection",
		Description: "Background behind selected text",
		Default:     DefaultSelectionBg, Color: true,
	},
	{
		Path: "appearance.selection.fg", Type: OptionString, Section: "selection",
		Description: "Selected text colour. Empty keeps the colour it already has.",
		Default:     DefaultSelectionFg, Color: true,
	},
	{
		Path: "appearance.selection.bold", Type: OptionBool, Section: "selection",
		Description: "Draw selected text bold as well as tinted",
		Default:     "false",
	},
	{
		Path: "appearance.selection.search_bg", Type: OptionString, Section: "selection",
		Description: "Background behind every search match",
		Default:     DefaultSearchBg, Color: true,
	},
	{
		Path: "appearance.selection.search_fg", Type: OptionString, Section: "selection",
		Description: "Text colour of a search match. Empty keeps the colour it has.",
		Default:     DefaultSearchFg, Color: true,
	},
	{
		Path: "appearance.selection.match_bg", Type: OptionString, Section: "selection",
		Description: "Background behind the match the cursor is on",
		Default:     DefaultMatchBg, Color: true,
	},
	{
		Path: "appearance.selection.match_fg", Type: OptionString, Section: "selection",
		Description: "Text colour of the match the cursor is on. Empty keeps it.",
		Default:     DefaultMatchFg, Color: true,
	},
	{
		Path: "appearance.selection.cursor_bg", Type: OptionString, Section: "selection",
		Description: "Background of the copy mode cursor block",
		Default:     DefaultCopyCursorBg, Color: true,
	},
	{
		Path: "appearance.selection.cursor_fg", Type: OptionString, Section: "selection",
		Description: "Text colour under the copy mode cursor. Empty keeps it.",
		Default:     DefaultCopyCursorFg, Color: true,
	},

	{
		Path: "appearance.selection.flash", Type: OptionBool, Section: "selection",
		Description: "Sweep a band of light over text that was just copied. Set motion to none to turn it off.",
		Default:     "true",
	},
	{
		Path: "appearance.selection.flash_ms", Type: OptionInt, Section: "selection",
		Description: "Milliseconds one copy sweep takes",
		Default:     strconv.Itoa(CopyFlashMsDefault),
		Min:         CopyFlashMsMin, Max: CopyFlashMsMax,
	},
	{
		Path: "appearance.selection.flash_style", Type: OptionString, Section: "selection",
		Description: "The shape the copy sweep takes",
		Accepted:    CopyFlashStyles, Default: DefaultCopyFlashStyle,
	},
	{
		Path: "appearance.selection.flash_color", Type: OptionString, Section: "selection",
		Description: "The light the copy sweep is made of",
		Default:     DefaultCopyFlashColor, Color: true,
	},
	{
		Path: "appearance.selection.multi_format", Type: OptionString, Section: "selection",
		Description: "The format multi copy mode yanks in: plain, markdown or json",
		Accepted:    MultiCopyFormats, Default: MultiCopyFormatPlain,
	},
	{
		Path: "appearance.selection.copy_entry", Type: OptionString, Section: "selection",
		Description: "Where copy mode puts its cursor: on the terminal cursor or in the center",
		Accepted:    CopyEntries, Default: CopyEntryCursor,
	},
	{
		Path: "appearance.selection.osc52_write", Type: OptionString, Section: "selection",
		Description: "What happens when a program in a pane sets the clipboard: off, ask, focused (the focused pane only, others ask) or on",
		Accepted:    OSC52WriteModes, Default: OSC52WriteFocused,
	},
	{
		Path: "appearance.selection.copy_command", Type: OptionString, Section: "selection",
		Description: "A command that gets each copy mode yank on stdin. Its output goes to the clipboard. If this is empty, tuios copies the selection as it is.",
		Default:     "",
	},

	// [appearance.sidebar]
	{
		Path: "appearance.sidebar.enabled", Type: OptionBool, Section: "sidebar",
		Description: "Show the session rail",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.position", Type: OptionString, Section: "sidebar",
		Description: "Edge the rail sits on, or hidden",
		Accepted:    SidebarPositions, Default: DefaultSidebarPosition,
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
		Deprecated:  "folded into appearance.sidebar.sections on load; leave terminals out of the layout",
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
		Path: "appearance.sidebar.show_numbers", Type: OptionBool, Section: "sidebar",
		Description: "Show the switch number ahead of each session name",
		Default:     "false",
	},
	{
		Path: "appearance.sidebar.show_agents", Type: OptionBool, Section: "sidebar",
		Description: "Show the agents section at the rail's bottom",
		Default:     "true",
		Deprecated:  "folded into appearance.sidebar.sections on load; leave agents out of the layout",
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
		Path: "appearance.sidebar.background", Type: OptionString, Section: "sidebar",
		Description: "Background under the rail: off, theme, or a #RRGGBB literal; empty follows appearance.background",
		Accepted:    Backgrounds, Default: "", Color: true,
	},
	{
		Path: "appearance.sidebar.sections", Type: OptionString, Section: "sidebar",
		Description: "Section names in the order the rail stacks them, each with an optional percent share. A name left out is a section the rail does not draw, and \"spacer\" is an empty block you may repeat.",
		Default:     SidebarDefaultSections,
	},
	{
		Path: "appearance.sidebar.file_icons", Type: OptionBool, Section: "sidebar",
		Description: "Draw a nerd font icon per file type in the files section",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.file_icon_colors", Type: OptionBool, Section: "sidebar",
		Description: "Draw each file icon in its file type's own colour",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.folder_click", Type: OptionString, Section: "sidebar",
		Description: "What a click on a folder row does",
		Accepted:    SidebarFolderClicks, Default: SidebarFolderClickNavigate,
	},
	{
		Path: "appearance.sidebar.header_case", Type: OptionString, Section: "sidebar",
		Description: "How the rail's section headers read: lowercase keeps the quiet furniture look, uppercase draws them as headings",
		Accepted:    RailHeaderCases, Default: RailHeaderLowercase,
	},
	{
		Path: "appearance.sidebar.editor", Type: OptionString, Section: "sidebar",
		Description: "The terminal editor that opens a text file. If this is empty, tuios uses $EDITOR, then $VISUAL, then vi.",
		Default:     "",
	},
	{
		Path: "appearance.sidebar.file_actions", Type: OptionBool, Section: "sidebar",
		Description: "Let the files section create, rename, delete, copy and paste",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.file_delete", Type: OptionString, Section: "sidebar",
		Description: "Where a delete sends the file: the trash, or nowhere",
		Accepted:    SidebarFileDeletes, Default: SidebarFileDeleteTrash,
	},
	{
		Path: "appearance.sidebar.agent_rest_fold", Type: OptionString, Section: "sidebar",
		Description: "How long an agent row rests (idle, unknown, or done and seen) before the rail folds it into one line: a duration such as 1h, or off",
		Default:     "1h",
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
		Description: "Start a new session tiled instead of floating. False when the config file has no [startup] table.",
		Default:     "true",
	},
	{
		Path: "startup.start_in_terminal_mode", Type: OptionBool, Section: "startup",
		Description: "Start focused in terminal mode, when a window is present",
		Default:     "false",
	},
	{
		Path: "startup.layout", Type: OptionString, Section: "startup",
		Description: "Tiling scheme a new session starts in. An existing session keeps its own.",
		Accepted:    LayoutModes, Default: LayoutModeBSP,
	},
	{
		Path: "startup.daemon", Type: OptionBool, Section: "startup",
		Description: "Make a bare \"tuios\" attach to a daemon-backed session instead of running standalone. TUIOS_NO_DAEMON=1 or --standalone overrides it. False when the config file has no [startup] table.",
		Default:     "true",
	},

	// [agents]. Only enabled is a scalar; the rest of the table is lists and
	// tables, which stay in the file.
	{
		Path: "agents.enabled", Type: OptionBool, Section: "agents",
		Description: "Agents, the Inbox, mail and approvals. Turn off to keep only the multiplexer.",
		Default:     "true",
	},

	// [daemon]. agent_binaries is absent: it is a list, and a value that arrives
	// as one string has no unambiguous spelling for a list. respond_from_shell
	// is absent too: it grants acting as the person, and set-option is a verb
	// any pane can call, so it is set in the file and nowhere else.
	{
		Path: "daemon.log_level", Type: OptionString, Section: "daemon",
		Description: "How much the daemon logs",
		Accepted:    daemonLogLevels, Default: "off",
	},
	{
		Path: "daemon.window_size", Type: OptionString, Section: "daemon",
		Description: "The size of a session with more than one client: the smallest client, the largest client, or the client that last had input",
		Accepted:    WindowSizeModes, Default: WindowSizeSmallest,
	},
	{
		Path: "daemon.agent_autodetect", Type: OptionBool, Section: "daemon",
		Description: "Detect a pane's foreground agent CLI and set its state glyph",
		Default:     "true",
	},
	{
		Path: "daemon.agent_detect_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds between checks. 0 uses 2. A negative number turns checks off.",
		Default:     "0",
	},
	{
		Path: "daemon.resume_agents", Type: OptionString, Section: "daemon",
		Description: "After a daemon restart, ask to resume each pane's agent conversation, resume it, or do neither",
		Accepted:    ResumeAgentsModes, Default: ResumeAgentsAsk,
	},
	{
		Path: "daemon.persist_scrollback", Type: OptionBool, Section: "daemon",
		Description: "Save each pane's history and show it again after a daemon restart. A change applies when the daemon next starts.",
		Default:     "true",
	},
	{
		Path: "daemon.persist_scrollback_lines", Type: OptionInt, Section: "daemon",
		Description: "Most history lines one pane saves. 0 uses 1000. A change applies when the daemon next starts.",
		Default:     "0", Min: 0, Max: 1000000,
	},
	{
		Path: "daemon.persist_scrollback_kb", Type: OptionInt, Section: "daemon",
		Description: "Most KiB one pane's saved history takes on disk. 0 uses 2048. A change applies when the daemon next starts.",
		Default:     "0", Min: 0, Max: 1048576,
	},

	// [notifications]
	{
		Path: "notifications.duration", Type: OptionInt, Section: "notifications",
		Description: "Seconds an info message stays up. 0 uses the default.",
		Default:     "0", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.warning_duration", Type: OptionInt, Section: "notifications",
		Description: "Seconds a warning stays up. 0 uses the default.",
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
		Description: "Turn every agent alert on or off",
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
		Description: "How an alert sounds: a cue, a BEL, or both",
		Accepted:    AgentSoundModeNames, Default: "",
	},
	{
		Path: "notifications.agent.sound_cooldown_seconds", Type: OptionInt, Section: "notifications",
		Description: "Shortest gap between two sounds, in seconds. 0 uses 3.",
		Default:     "3", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.agent.dock", Type: OptionBool, Section: "notifications",
		Description: "Show the alert in the dock. Click it to go to the pane.",
		Default:     "true",
	},
	{
		Path: "notifications.agent.command", Type: OptionString, Section: "notifications",
		Description: "Shell command to run on an alert. Empty runs nothing.",
		Default:     "",
	},
	{
		Path: "notifications.agent.settle_seconds", Type: OptionInt, Section: "notifications",
		Description: "Seconds to wait. tuios drops the alert if the pane changes state.",
		Default:     "2", Min: 0, Max: 3600,
	},
	{
		Path: "notifications.agent.suppress_focused", Type: OptionBool, Section: "notifications",
		Description: "No alert for the focused pane while the terminal has focus",
		Default:     "true",
	},
	{
		Path: "notifications.agent.quiet_hours", Type: OptionString, Section: "notifications",
		Description: "Hours when nothing alerts. Write it as HH:MM-HH:MM.",
		Default:     "",
	},

	// [notifications.agent.states]
	{
		Path: "notifications.agent.states.needs_input", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent waits for you",
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
		Description: "Alert when an agent goes quiet",
		Default:     "false",
	},
	{
		Path: "notifications.agent.states.working", Type: OptionBool, Section: "notifications",
		Description: "Alert when an agent starts work",
		Default:     "false",
	},

	// [notifications.mail]. Default is empty for the four keys that follow
	// [notifications.agent] when unset: the value in force is that table's.
	{
		Path: "notifications.mail.enabled", Type: OptionBool, Section: "notifications",
		Description: "Turn mail alerts on or off. Unset follows notifications.agent.enabled.",
		Default:     "", Follows: "notifications.agent.enabled",
	},
	{
		Path: "notifications.mail.notify", Type: OptionBool, Section: "notifications",
		Description: "Send a desktop notification for mail. Unset follows notifications.agent.notify.",
		Default:     "", Follows: "notifications.agent.notify",
	},
	{
		Path: "notifications.mail.sound", Type: OptionBool, Section: "notifications",
		Description: "Make a mail alert audible. Unset follows notifications.agent.sound.",
		Default:     "", Follows: "notifications.agent.sound",
	},
	{
		Path: "notifications.mail.dock", Type: OptionBool, Section: "notifications",
		Description: "Show a mail alert in the dock. Unset follows notifications.agent.dock.",
		Default:     "", Follows: "notifications.agent.dock",
	},
	{
		Path: "notifications.mail.between_agents", Type: OptionBool, Section: "notifications",
		Description: "Alert on a message from one agent to another agent too.",
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

	// [dock]
	// The three component lists and the [dock.custom] tables are absent for the
	// reason [keybindings] and [hooks] are: they are ordered lists and free-form
	// tables, which a set-by-path verb cannot spell. The clock's format is a
	// plain scalar, so it belongs here.
	{
		Path: "dock.clock.format", Type: OptionString, Section: "dock",
		Description: "Go time layout the clock renders in (empty means " + DefaultClockFormat +
			"); a layout without seconds refreshes once a minute",
		Default: "",
	},

	// [debug]
	{
		Path: "debug.show_key_events", Type: OptionBool, Section: "debug",
		Description: "Show the on-screen keycast of recent keypresses",
		Default:     "false",
	},

	// [screenshot]
	// The two path options (directory, font_file) are registered as plain
	// strings: no accepted set can check a path, but a registry entry keeps
	// them settable through set-option, which agents need. They are excluded
	// from the settings panel instead (see settingsUIExcluded), for the
	// reason notifications.agent.command is: an SSH client authenticates
	// nobody and should not redirect server-side writes.
	{
		Path: "screenshot.format", Type: OptionString, Section: "screenshot",
		Description: "Default output format for captures",
		Accepted:    ScreenshotFormats, Default: ScreenshotDefaultFormat,
	},
	{
		Path: "screenshot.copy", Type: OptionBool, Section: "screenshot",
		Description: "Try to copy the capture to the clipboard",
		Default:     "true",
	},
	{
		Path: "screenshot.preview", Type: OptionBool, Section: "screenshot",
		Description: "Open the preview panel after a capture",
		Default:     "true",
	},
	{
		Path: "screenshot.directory", Type: OptionString, Section: "screenshot",
		Description: "Folder where capture files are saved",
		Default:     ScreenshotDefaultDirectory,
	},
	{
		Path: "screenshot.frame", Type: OptionString, Section: "screenshot",
		Description: "Dressing around the capture: a window card, a plain card, or nothing",
		Accepted:    ScreenshotFrames, Default: ScreenshotDefaultFrame,
	},
	{
		Path: "screenshot.background", Type: OptionString, Section: "screenshot",
		Description: "Wash behind the card: auto derives it from the theme; none, a hex color, or hex..hex work too",
		Default:     ScreenshotDefaultBackground,
	},
	{
		Path: "screenshot.padding", Type: OptionInt, Section: "screenshot",
		Description: "Space around the card in pixels",
		Default:     "48", Min: 0, Max: ScreenshotMaxPadding,
	},
	{
		Path: "screenshot.radius", Type: OptionInt, Section: "screenshot",
		Description: "Card corner radius in pixels",
		Default:     "10", Min: 0, Max: ScreenshotMaxRadius,
	},
	{
		Path: "screenshot.shadow", Type: OptionBool, Section: "screenshot",
		Description: "Draw a soft shadow under the card",
		Default:     "true",
	},
	{
		Path: "screenshot.controls", Type: OptionString, Section: "screenshot",
		Description: "Window control marks: the macOS lights, your glyph set, or none",
		Accepted:    ScreenshotControlSet, Default: ScreenshotDefaultControls,
	},
	{
		Path: "screenshot.title_format", Type: OptionString, Section: "screenshot",
		Description: "Title bar text, with {title}, {index} and {cwd} tokens",
		Default:     ScreenshotDefaultTitleFormat,
	},
	{
		Path: "screenshot.font_family", Type: OptionString, Section: "screenshot",
		// A capture on kitty is already drawn in the terminal's own font,
		// because kitty answers when asked which font that is. This is the
		// answer for every other terminal, and it names a font rather than a
		// file so it also names the SVG and HTML output.
		Description: "Font to draw the capture in when your terminal does not say which it uses",
		Default:     ScreenshotDefaultFontFamily,
	},
	{
		Path: "screenshot.font_file", Type: OptionString, Section: "screenshot",
		Description: "Font file to draw PNG with, also embedded in SVG and HTML. " +
			"It wins over every other font choice.",
		Default: "",
	},
	{
		Path: "screenshot.scale", Type: OptionInt, Section: "screenshot",
		Description: "PNG size multiplier",
		Default:     "2", Min: 1, Max: ScreenshotMaxScale,
	},
	{
		Path: "screenshot.cursor", Type: OptionBool, Section: "screenshot",
		Description: "Draw the cursor cell in the capture",
		Default:     "false",
	},

	// [screensaver]. Off by default: a screen that starts animating on its own
	// is a surprise, and the setting to stop it is the one nobody can find
	// while it is running.
	{
		Path: "screensaver.enabled", Type: OptionBool, Section: "screensaver",
		Description: "Animate the screen after a spell with no input",
		Default:     "false",
	},
	{
		Path: "screensaver.idle_minutes", Type: OptionInt, Section: "screensaver",
		Description: "Minutes of quiet before the screen saver starts",
		Default:     "10", Min: ScreensaverMinIdleMinutes, Max: ScreensaverMaxIdleMinutes,
	},
	{
		Path: "screensaver.effect", Type: OptionString, Section: "screensaver",
		Description: "Which effect runs, or random for a different one each time",
		Accepted:    ScreensaverEffects, Default: ScreensaverRandomEffect,
	},
	{
		Path: "screensaver.while_busy", Type: OptionBool, Section: "screensaver",
		Description: "Start even when a pane is running a command or an agent",
		Default:     "false",
	},

	// [spotlight]. Off by default, and client-local while it runs: the beam is
	// what this client's screen looks like, not what the session holds, so a
	// peer attached to the same panes sees nothing.
	{
		Path: "spotlight.enabled", Type: OptionBool, Section: "spotlight",
		Description: "Light one part of the screen and dim the rest",
		Default:     "false",
	},
	{
		Path: "spotlight.follow", Type: OptionString, Section: "spotlight",
		Description: "What the beam follows: the mouse, or the focused pane's cursor. The cursor sends fewer bytes to a remote client",
		Accepted:    SpotlightFollowModes, Default: SpotlightFollowMouse,
	},
	{
		Path: "spotlight.radius", Type: OptionInt, Section: "spotlight",
		Description: "Half the beam's height, in rows",
		Default:     "10", Min: SpotlightMinRadius, Max: SpotlightMaxRadius,
	},
	{
		Path: "spotlight.dim", Type: OptionInt, Section: "spotlight",
		Description: "Percent of its light an unlit cell loses",
		Default:     "75", Min: SpotlightMinDim, Max: SpotlightMaxDim,
		Percent: true,
	},
	{
		Path: "spotlight.edge", Type: OptionString, Section: "spotlight",
		Description: "Cut the beam at its edge, or fade it out. A fade sends about three times the bytes each time the beam moves",
		Accepted:    SpotlightEdges, Default: SpotlightEdgeHard,
	},
	{
		Path: "spotlight.shake", Type: OptionBool, Section: "spotlight",
		Description: "Move the mouse left and right fast to turn the beam on and off. A shake does not move the beam. The follow setting decides where it goes",
		Default:     "false",
	},

	// [pip]. Read each time the picture-in-picture view is drawn, and
	// client-local like the spotlight.
	{
		Path: "pip.width", Type: OptionInt, Section: "pip",
		Description: "Width of the picture-in-picture view, border included, in cells",
		Default:     "40", Min: PiPMinWidth, Max: PiPMaxWidth,
	},
	{
		Path: "pip.height", Type: OptionInt, Section: "pip",
		Description: "Height of the picture-in-picture view, border included, in cells",
		Default:     "12", Min: PiPMinHeight, Max: PiPMaxHeight,
	},
	{
		Path: "pip.corner", Type: OptionString, Section: "pip",
		Description: "Corner the picture-in-picture view goes to first. It moves to another corner when the cursor enters it",
		Accepted:    PiPCorners, Default: PiPCornerBottomRight,
	},

	// [hints]. Read each time hints mode opens. hints.patterns is a list and
	// is set in the file only, like [keybindings].
	{
		Path: "hints.builtins", Type: OptionString, Section: "hints",
		Description: "Built-in patterns hints mode looks for: all, none, or names such as url,path,sha",
		Default:     HintsDefaultBuiltins,
	},
	{
		Path: "hints.alphabet", Type: OptionString, Section: "hints",
		Description: "Letters the hint labels are made of, easiest first",
		Default:     hints.DefaultAlphabet,
	},
	{
		Path: "hints.open", Type: OptionBool, Section: "hints",
		Description: "Ctrl and a label opens the URL or path. It does not open on a remote client",
		Default:     "true",
	},
	{
		Path: "hints.dim", Type: OptionInt, Section: "hints",
		Description: "Percent of its light the text around the hints loses",
		Default:     "60", Min: HintsMinDim, Max: HintsMaxDim,
		Percent: true,
	},
	{
		Path: "hints.all_panes", Type: OptionBool, Section: "hints",
		Description: "The hints key labels every pane on the workspace, not only the focused pane",
		Default:     "false",
	},

	// [panes]. Read each time display_panes opens.
	{
		Path: "panes.label_keys", Type: OptionString, Section: "panes",
		Description: "Keys the pane labels are made of, first pane first. Letters a to z and digits",
		Default:     PanesDefaultLabelKeys,
	},
	{
		Path: "panes.navigator_layout", Type: OptionString, Section: "panes",
		Description: "How the pane navigator lists the panes when it opens",
		Accepted:    NavigatorLayouts,
		Default:     NavigatorLayoutTree,
	},

	// [scratch]. Read each time toggle_scratch creates or shows the scratch
	// terminal. A show resizes the popup to the size in force.
	{
		Path: "scratch.width", Type: OptionString, Section: "scratch",
		Description: "Width of the scratch terminal, in cells (60) or percent (80%)",
		Default:     ScratchDefaultWidth, BoxSize: true,
	},
	{
		Path: "scratch.height", Type: OptionString, Section: "scratch",
		Description: "Height of the scratch terminal, in cells (20) or percent (80%)",
		Default:     ScratchDefaultHeight, BoxSize: true,
	},
	// [launcher]. Read each time the launcher starts an entry.
	{
		Path: "launcher.gui_command", Type: OptionString, Section: "launcher",
		Description: "Command that starts a graphical desktop entry instead of a pane. The entry's argv is added to it. Use a command that takes an argv, such as \"tuios-wayland launch --\" or \"niri msg action spawn --\". Do not use one that joins its arguments into a shell line, such as \"swaymsg exec --\" or \"hyprctl dispatch exec\".",
		Default:     "",
	},
	// [workspaces]. The daemon reads it when it starts and when the file
	// changes.
	{
		Path: "workspaces.return_when_empty", Type: OptionBool, Section: "workspaces",
		Description: "When the last pane on the workspace on screen closes, show the workspace you came from. Off shows the splash screen",
		Default:     "true",
	},
	// [paste_buffers]. The daemon reads it when it starts and when the file
	// changes, and so does a client with no daemon.
	{
		Path: "paste_buffers.limit", Type: OptionInt, Section: "selection",
		Description: "How many yanks to keep to paste again. When it is full, the oldest goes. 0 keeps none.",
		Default:     "20", Min: 0, Max: 1000,
	},
	{
		Path: "paste_buffers.max_kb", Type: OptionInt, Section: "selection",
		Description: "Most KiB all paste buffers hold together, at most 262144 (256 MiB). When it is full, the oldest go. 0 uses 16384.",
		Default:     "16384", Min: 0, Max: 262144,
	},
	// Read by the client on each switch, so a change applies at once.
	{
		Path: "workspaces.new_window_when_empty", Type: OptionBool, Section: "workspaces",
		Description: "When you switch to a workspace with no panes, open a new pane there. It starts in the folder of the pane you came from",
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
	if o.Icon && value != o.Default && !DockModeIconUsable(value) {
		return fmt.Errorf("%s: %q is not a usable icon; use at most %d cells and no control characters",
			o.Path, value, DockModeIconMaxWidth)
	}
	if o.BoxSize && strings.TrimSpace(value) != "" {
		if _, _, err := ParseBoxSize(value); err != nil {
			return fmt.Errorf("%s: %w", o.Path, err)
		}
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
		if msg, retired := RetiredOption(path); retired {
			return errors.New(msg)
		}
		return fmt.Errorf("unknown config option %q", path)
	}
	if err := opt.checkValue(value); err != nil {
		return err
	}
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return fmt.Errorf("unknown config option %q", path)
	}
	// An option that follows another clears on the empty string, back to
	// following it.
	if opt.Follows != "" && strings.TrimSpace(value) == "" && field.Kind() == reflect.Pointer {
		field.SetZero()
		return nil
	}

	// An option whose default is a word clears on that word, back to unset.
	if opt.DefaultClears && strings.TrimSpace(value) == opt.Default && field.Kind() == reflect.Pointer {
		field.SetZero()
		return nil
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
		if opt.Auto && strings.EqualFold(strings.TrimSpace(value), FPSAuto) {
			if optionKind(field) != reflect.String {
				return fmt.Errorf("%s: registry says int or auto, config field is %s", path, optionKind(field))
			}
			optionTarget(field).SetString(FPSAuto)
			return nil
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			if opt.Auto {
				return fmt.Errorf("%s: %q is not a whole number or auto", path, value)
			}
			return fmt.Errorf("%s: %q is not a whole number", path, value)
		}
		if opt.Max > 0 && (parsed < opt.Min || parsed > opt.Max) {
			return fmt.Errorf("%s: %d is outside %d..%d", path, parsed, opt.Min, opt.Max)
		}
		if opt.Auto && optionKind(field) == reflect.String {
			// 0 is stored as the empty value, the one spelling a reload gives.
			stored := ""
			if parsed != 0 {
				stored = strconv.Itoa(parsed)
			}
			optionTarget(field).SetString(stored)
			return nil
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
		if opt.Auto {
			// An auto-or-int option reads back as the int or "auto", never
			// as the empty string an unset field holds.
			if v := strings.TrimSpace(field.String()); v != "" {
				if strings.EqualFold(v, FPSAuto) {
					return FPSAuto, true
				}
				return v, true
			}
			return opt.Default, true
		}
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
	for segment := range strings.SplitSeq(path, ".") {
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
