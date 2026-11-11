package config

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
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
	Default     string   `json:"default"`              // derived from DefaultConfig at init; write it only for a field DefaultConfig leaves unset
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
	// up to an hour, so the usual two seconds would draw an empty bar, and
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
	// Fallback is the word a load warning gives for what a value outside
	// Accepted is read as. Empty means "default".
	// Zero says what 0 in the file means for an int option: a value
	// (ZeroIsValue, the default) or "not set" (ZeroIsDefault), which reads as
	// the option's default.
	Zero ZeroRule `json:"-"`
	// Flag is the name of the CLI flag that sets this option for one run of
	// tuios, tuios ssh and tuios-web, such as "border-style". Empty means no
	// flag. The flag takes its type, help and check from this entry.
	Flag string `json:"-"`
	// Env is the environment variable that sets this option for the
	// process, such as "TUIOS_PR_POLL_SECONDS". Empty means none. A set
	// variable is laid over the file by WithEnv, so it wins over the file.
	Env      string `json:"env,omitempty"`
	Fallback string `json:"-"`
	// Unit is what an int option's number counts, such as "ms", "s",
	// "cells" or "MiB", shown after the value on the settings page and in
	// list-options. An entry whose path ends in a unit (_ms, _seconds,
	// _hours, _mb ...) gets it from the path when it names none, and a
	// Percent option gets "%". Empty means a plain count.
	Unit string `json:"unit,omitempty"`
	// Normalize is what the option's reader does to a value before it
	// compares it with Accepted, such as trimming spaces or folding case. The
	// load check does the same, so it warns only about a value the reader
	// does not take. Nil compares the value as written.
	Normalize func(string) string `json:"-"`
	// Check rejects a value of an open string option that does not parse,
	// such as a duration or a field list, at set time rather than as a
	// warning on the next load. Nil checks nothing beyond the rest.
	Check func(string) error `json:"-"`
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
	// DockPrefixIndicators are where an armed leader shows: on the dock's
	// mode pill, or on the floating badge it had before the pill could.
	DockPrefixIndicators = []string{"pill", "badge"}
	// DockModePills are what the mode pill shows while tiling is on: the
	// tiling icon, or the icon of the input mode (terminal or window).
	DockModePills = []string{"tiling", "input"}
	// WindowTitleStyles are the shapes of a pane's title: pill has rounded
	// caps on a fill, tab is the fill alone, plain is the text on the line.
	WindowTitleStyles = []string{"pill", "tab", "plain"}
	// WindowTitleAligns are where the title sits on its row. auto is the end
	// away from the window buttons on the top row, and the middle on the
	// bottom row.
	WindowTitleAligns = []string{"auto", "left", "center", "right"}
	daemonLogLevels   = []string{"off", "errors", "basic", "messages", "verbose", "trace"}
)

// The shipped edges for the dock, the rail and the window title. Named because
// DefaultConfig, DefaultSettings, the registry and the typo fallback in
// ApplyAppearanceConfig all have to agree on them. v0.8.0 moved all three: the
// dock and the title were at the bottom and the rail on the left before it.
const (
	DefaultDockbarPosition     = "top"
	DefaultSidebarPosition     = "right"
	DefaultWindowTitlePosition = "top"
	DefaultDockPrefixIndicator = "pill"
	DefaultDockModePill        = "tiling"
	// DefaultBorderGradientAngle runs the gradient from the top left corner
	// to the bottom right one.
	DefaultBorderGradientAngle = 45
	DefaultWindowTitleStyle    = "pill"
	DefaultWindowTitleAlign    = "auto"
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
		Accepted:    BorderStyles,
		Flag:        "border-style",
	},
	{
		Path: "appearance.frame_panels", Type: OptionBool, Section: "appearance",
		Description: "Frame the larger overlay panels, such as the command palette, in the border style",
	},
	{
		Path: "appearance.pane_overlays", Type: OptionBool, Section: "appearance",
		Description: "Draw dialogs and panels like pane windows: framed in the focused pane's border colour, on the pane's background",
	},
	{
		Path: "appearance.overlay_border_color", Type: OptionString, Section: "appearance",
		Description: "Colour of every overlay's frame: #89b4fa, a slot name such as bright_cyan, or a role such as accent. Empty keeps the overlay's own, or the focused pane's border under pane_overlays",
		Color:       true,
	},
	{
		Path: "appearance.zen_mode", Type: OptionString, Section: "appearance",
		Description: "When window borders are hidden: never, always, or while the mouse is idle",
		Accepted:    ZenModeModes,
	},
	{
		Path: "appearance.links", Type: OptionString, Section: "appearance",
		Description: "Links the pointer picks up: off, only marked links, or plain URLs too",
		Accepted:    LinkModes,
	},
	{
		Path: "appearance.link_click", Type: OptionString, Section: "appearance",
		Description: "The click that opens a link: ctrl or shift, ctrl only, shift only, or none",
		Accepted:    LinkClickModes,
	},
	{
		Path: "appearance.link_hover", Type: OptionString, Section: "appearance",
		Description: "The hover highlight: safe skips a pane whose guest tracks the mouse, always draws it everywhere",
		Accepted:    LinkHoverModes,
	},
	{
		Path: "appearance.drop_preview", Type: OptionString, Section: "appearance",
		Description: "What a BSP drag shows where the pane would land: a frame with a tint inside, the frame only, or nothing",
		Accepted:    DropPreviewModes,
	},
	{
		Path: "appearance.drop_preview_delay", Type: OptionString, Section: "appearance",
		Description: "How long the pointer rests on a drop zone before it shows: a duration such as 120ms, or 0 for at once",
		Check:       checkDropPreviewDelay,
	},
	{
		Path: "appearance.link_opener", Type: OptionString, Section: "appearance",
		Description: "The command that opens a web link. If this is empty, tuios uses $BROWSER, then the system opener.",
	},
	{
		Path: "appearance.hide_window_buttons", Type: OptionBool, Section: "appearance",
		Description: "Hide the minimize, maximize and close buttons",
		Flag:        "hide-window-buttons",
	},
	{
		Path: "appearance.window_button_style", Type: OptionString, Section: "appearance",
		Description: "Window controls as a filled pill or as macOS traffic lights",
		Accepted:    WindowButtonStyles,
		Flag:        "window-button-style",
	},
	{
		Path: "appearance.window_button_position", Type: OptionString, Section: "appearance",
		Description: "Which end of the title bar the window controls sit on",
		Accepted:    WindowButtonPositions,
		Flag:        "window-button-position",
	},
	{
		Path: "appearance.hide_scrollbar", Type: OptionBool, Section: "appearance",
		Description: "Hide the scrollbar thumb on the pane border",
		Flag:        "hide-scrollbar",
	},
	{
		Path: "appearance.scrollback_lines", Type: OptionInt, Section: "appearance",
		Description: "Lines each pane keeps in its scrollback. A pane takes the value when it is made. The daemon reads it when it starts",
		Min:         100, Max: 1000000,
		Flag: "scrollback-lines",
	},
	{
		Path: "appearance.scroll_lines", Type: OptionInt, Section: "appearance",
		Description: "Lines scrolled per mouse wheel notch",
		Min:         1, Max: 50,
	},
	{
		Path: "appearance.copy_on_select", Type: OptionBool, Section: "appearance",
		Description: "Copy a mouse selection to the clipboard on release",
		Default:     "true",
	},
	{
		Path: "appearance.nvim_navigation", Type: OptionBool, Section: "appearance",
		Description: "Let the Neovim navigator plugin hand terminal focus keys back to TUIOS",
	},
	{
		Path: "appearance.focus_wrap", Type: OptionBool, Section: "appearance",
		Description: "A focus key at the edge of a tiled layout moves to the pane on the far side",
	},
	{
		Path: "appearance.focus_follows_mouse", Type: OptionBool, Section: "appearance",
		Description: "Focus the pane under the cursor as the mouse moves",
		Default:     "false",
	},
	{
		Path: "appearance.focus_follows_mouse_pause", Type: OptionString, Section: "appearance",
		Description: "Hold this key to keep the focus on the current pane as the mouse moves: off, ctrl, alt",
		Accepted:    FocusFollowsMousePauseKeys,
		Fallback:    "off",
	},
	{
		Path: "appearance.paste_preview", Type: OptionBool, Section: "appearance",
		Description: "Show a pasted image in a card at the cursor for a few seconds",
		Default:     "true",
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
		Accepted:    KittyPlaceholderModes,
	},
	{
		Path: "appearance.image_symbols", Type: OptionString, Section: "appearance",
		Description: "Draw pane images as block glyphs when the terminal has no graphics: auto, octant, sextant, quadrant, half, off",
		Accepted:    ImageSymbolModes,
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
		Accepted:    ClickToTypeModes,
	},
	{
		Path: "appearance.auto_enter_terminal_on_focus", Type: OptionString, Section: "appearance",
		Description: "Type in a pane when a keyboard focus command moves to it: off, targeted (select and arrows), or all (including Tab)",
		Accepted:    AutoEnterTerminalModes,
	},
	{
		Path: "appearance.word_characters", Type: OptionString, Section: "appearance",
		Description: "Punctuation that counts as part of a word for double-click selection",
		Default:     `@-./_~?&=%+#`,
	},
	{
		Path: "appearance.preferred_shell", Type: OptionString, Section: "appearance",
		Description: "Shell that new panes run. Empty picks one for your platform.",
	},
	{
		Path: "appearance.motion", Type: OptionString, Section: "appearance",
		Description: "How much moves: none, basic (window slides, workspace slides and the copy sweep), or full (also overlay fades and the working-agent shimmer)",
		Accepted:    MotionLevels,
	},
	{
		Path: "appearance.calm", Type: OptionBool, Section: "appearance",
		Description: "Calm mode: nothing moves or blinks, and only messages that need you show in the dock",
		Default:     "false",
	},
	{
		Path: "appearance.modal_dim", Type: OptionInt, Section: "appearance",
		Description: "How much the screen behind a modal panel is darkened, as a percent. 0 is off.",
		Min:         0, Max: ModalDimMax,
		Percent: true,
	},
	{
		Path: "appearance.popup_dim", Type: OptionInt, Section: "appearance",
		Description: "How much the screen behind a focused plain popup is darkened, as a percent. 0 is off; popup --dim overrides it.",
		Min:         0, Max: PopupDimMax,
		Percent: true,
	},
	{
		Path: "appearance.animations_enabled", Type: OptionBool, Section: "appearance",
		Description: "Animate UI transitions instead of applying them instantly",
		Default:     "true",
		Deprecated:  "folded into appearance.motion on load (false is none); set appearance.motion",
	},
	{
		Path: "appearance.confirm_close", Type: OptionString, Section: "appearance",
		Description: "When closing a pane by hand (a key, its menu or its close control) asks first: never, busy (the pane runs something besides its shell) or always",
		Accepted:    ConfirmCloseLevels,
	},
	{
		Path: "appearance.confirm_quit", Type: OptionBool, Section: "appearance",
		Description: "Always confirm on quit, not only when processes are running",
		Default:     "false",
		Flag:        "confirm-quit",
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
		Accepted:    WhichKeyPositions,
	},
	{
		Path: "appearance.window_title_position", Type: OptionString, Section: "appearance",
		Description: "Edge of the pane the title is drawn on",
		Accepted:    WindowTitlePositions,
		Flag:        "window-title-position",
	},
	{
		Path: "appearance.window_title_style", Type: OptionString, Section: "appearance",
		Description: "Shape of a pane's title: pill has rounded caps, tab is a plain fill, plain is the text on the border",
		Accepted:    WindowTitleStyles,
	},
	{
		Path: "appearance.window_title_align", Type: OptionString, Section: "appearance",
		Description: "Where a pane's title sits on its row. auto keeps it away from the window buttons",
		Accepted:    WindowTitleAligns,
	},
	{
		Path: "appearance.theme", Type: OptionString, Section: "appearance",
		Description: "Colour theme name. Empty follows Omarchy when it is installed, else keeps your terminal's colours. none always keeps them.",
		Theme:       true,
	},
	{
		Path: "appearance.theme_light", Type: OptionString, Section: "appearance",
		Description: "Theme to use while the terminal is light. Empty uses theme for both. theme is then the dark theme",
		Theme:       true,
	},
	{
		Path: "appearance.shared_borders", Type: OptionBool, Section: "appearance",
		Description: "Draw one border between neighbouring tiled panes, in every layout",
		Default:     "false",
		Flag:        "shared-borders",
	},
	{
		Path: "appearance.border_focused_color", Type: OptionString, Section: "appearance",
		Description: "Colour overriding the focused pane's border: #89b4fa, a slot name such as bright_cyan, or a role such as accent",
		Color:       true,
	},
	{
		Path: "appearance.border_focused_color_window", Type: OptionString, Section: "appearance",
		Description: "Colour of the focused pane's border in window mode. It wins over border_focused_color",
		Color:       true,
	},
	{
		Path: "appearance.border_focused_color_terminal", Type: OptionString, Section: "appearance",
		Description: "Colour of the focused pane's border in terminal mode. It wins over border_focused_color",
		Color:       true,
	},
	{
		Path: "appearance.border_focused_gradient", Type: OptionString, Section: "appearance",
		Description: "Two or more colours, separated by spaces, that the focused pane's border steps through. Empty draws one colour",
	},
	{
		Path: "appearance.border_gradient_angle", Type: OptionInt, Section: "appearance",
		Unit:        "degrees",
		Description: "Direction of the focused border's gradient in degrees: 0 runs left to right, 90 top to bottom",
		Default:     "45", Min: 0, Max: 359,
	},
	{
		Path: "appearance.border_unfocused_color", Type: OptionString, Section: "appearance",
		Description: "Colour overriding an unfocused pane's border: #585b70, a slot name such as bright_black, or a role such as muted",
		Color:       true,
	},
	{
		Path: "appearance.window_title_format", Type: OptionString, Section: "appearance",
		Description: "Title template accepting {title}, {index} and {cwd}",
	},
	{
		Path: "appearance.host_title", Type: OptionString, Section: "appearance",
		Description: "Title of the terminal tuios runs in, from the focused pane: accepts {title}, {index}, {cwd}, {session} and {workspace}. Empty sets no title",
	},
	{
		Path: "appearance.zoom_max_width", Type: OptionInt, Section: "appearance",
		Unit:        "cells",
		Description: "Width in cells of a zoomed pane. 0 fills the screen.",
		Min:         0,
		Flag:        "zoom-max-width",
	},
	{
		Path: "appearance.niri_reverse_scroll", Type: OptionBool, Section: "appearance",
		Description: "Reverse the wheel direction in niri scrolling mode",
	},
	{
		Path: "appearance.niri_scroll_snap", Type: OptionBool, Section: "appearance",
		Description: "Snap the strip to the nearest column when a scroll stops",
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
		Path: "appearance.cell_frames", Type: OptionString, Section: "appearance",
		Description: "Which clients write their frames as diffed cells instead of through Bubble Tea's renderer, " +
			"which turns each frame into text and back. served: clients over SSH and the web. " +
			"all: this terminal as well. off: none. TUIOS_CELL_FRAME=1 means all and 0 means off",
		Accepted: CellFrameModes, Default: CellFramesAll,
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
		GlyphSet:    true,
	},
	{
		Path: "appearance.gap", Type: OptionInt, Section: "appearance",
		Unit:        "cells",
		Description: "Cells of empty ground kept between two neighbouring tiled panes",
		Min:         0, Max: PaneGapMax,
	},
	{
		Path: "appearance.tiling_scheme", Type: OptionString, Section: "appearance",
		Description: "BSP scheme a workspace inserts new windows with the first time it is tiled. A workspace already tiled keeps its own scheme.",
		Accepted:    TilingSchemes,
	},
	{
		Path: "appearance.master_ratio", Type: OptionInt, Section: "appearance",
		Description: "Width of the master pane in the master-stack layout, as a percent of the screen",
		Min:         MasterRatioMin, Max: MasterRatioMax,
		Percent: true,
		Zero:    ZeroIsDefault,
	},
	{
		Path: "appearance.master_position", Type: OptionString, Section: "appearance",
		Description: "Side the master panes take in the master-stack layout. Center puts the stack on both sides.",
		Accepted:    MasterPositions,
	},
	{
		Path: "appearance.master_count", Type: OptionInt, Section: "appearance",
		Description: "How many panes are master panes in the master-stack layout",
		Min:         MasterCountMin, Max: MasterCountMax,
	},
	{
		Path: "appearance.master_grid", Type: OptionBool, Section: "appearance",
		Description: "With one master on the left, show four or more panes as a grid",
		Default:     "true",
	},
	{
		Path: "appearance.scroll_column_width", Type: OptionInt, Section: "appearance",
		Description: "Width of a column in the scrolling layout, as a percent of the screen",
		Min:         ScrollColumnWidthMin, Max: ScrollColumnWidthCeiling,
		Percent: true,
	},
	{
		Path: "appearance.zoom_size", Type: OptionInt, Section: "appearance",
		Description: "How much of the screen a zoomed pane takes, as a percent. Below 100 the layout around it stays visible at the edges.",
		Min:         ZoomSizeMin, Max: ZoomSizeMax,
		Percent: true,
		Zero:    ZeroIsDefault,
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
	},
	{
		Path: "appearance.zoom_animation", Type: OptionBool, Section: "appearance",
		Description: "Old name of motion.zoom",
		Deprecated:  "folded into motion.zoom on load; set motion.zoom",
	},
	{
		Path: "appearance.animation_speed", Type: OptionInt, Section: "appearance",
		Description: "Percent window animations run at: 100 as shipped, 200 twice as fast, 50 half as fast",
		Min:         AnimationSpeedMin, Max: AnimationSpeedMax,
		Percent: true,
		Zero:    ZeroIsDefault,
	},
	{
		Path: "appearance.theme_transition", Type: OptionString, Section: "appearance",
		Description: "How tuios shows a theme change: none, circle (the new theme grows from the middle of the screen) or fade",
		Accepted:    ThemeTransitions,
	},
	{
		Path: "appearance.theme_transition_duration", Type: OptionInt, Section: "appearance",
		Unit:        "ms",
		Description: "Milliseconds a theme transition takes at animation speed 100",
		Min:         ThemeTransitionMsMin, Max: ThemeTransitionMsMax,
		Zero: ZeroIsDefault,
	},
	{
		Path: "appearance.workspace_transition", Type: OptionString, Section: "appearance",
		Description: "Old name of motion.workspace",
		Deprecated:  "folded into motion.workspace on load; set motion.workspace",
	},
	{
		Path: "appearance.workspace_transition_ms", Type: OptionInt, Section: "appearance",
		Description: "Old name of motion.workspace_ms",
		Min:         0, Max: MotionMsMax,
		Deprecated: "folded into motion.workspace_ms on load; set motion.workspace_ms",
	},
	{
		Path: "motion.workspace", Type: OptionString, Section: "motion",
		Description: "How tuios shows a workspace switch: slide, fade or none",
		Accepted:    WorkspaceTransitions,
	},
	{
		Path: "motion.workspace_ms", Type: OptionInt, Section: "motion",
		Description: "Milliseconds the workspace motion takes at animation speed 100. 0 is a cut.",
		Min:         0, Max: MotionMsMax,
	},
	{
		Path: "motion.layout", Type: OptionString, Section: "motion",
		Description: "Panes slide when they open, close, split, swap, snap, minimize or restore: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "motion.layout_ms", Type: OptionInt, Section: "motion",
		Description: "Milliseconds the layout motion takes at animation speed 100. 0 is a cut.",
		Min:         0, Max: MotionMsMax,
	},
	{
		Path: "motion.zoom", Type: OptionString, Section: "motion",
		Description: "A pane slides between its tile and the zoom box: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "motion.zoom_ms", Type: OptionInt, Section: "motion",
		Description: "Milliseconds the zoom motion takes at animation speed 100. 0 is a cut.",
		Min:         0, Max: MotionMsMax,
	},
	{
		Path: "motion.overlays", Type: OptionString, Section: "motion",
		Description: "A palette or dialog fades in as it opens: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "motion.overlays_ms", Type: OptionInt, Section: "motion",
		Description: "Milliseconds the overlays motion takes at animation speed 100. 0 is a cut.",
		Min:         0, Max: MotionMsMax,
	},
	{
		Path: "motion.sidebar", Type: OptionString, Section: "motion",
		Description: "The shimmer on a working agent row of the sidebar: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "motion.scroll", Type: OptionString, Section: "motion",
		Description: "The scrolling layout slides when the view moves: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "motion.scroll_ms", Type: OptionInt, Section: "motion",
		Description: "Milliseconds the scroll motion takes at animation speed 100. 0 is a cut.",
		Min:         0, Max: MotionMsMax,
	},
	{
		Path: "motion.copy", Type: OptionString, Section: "motion",
		Description: "A sweep crosses the cells a copy took: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "motion.confetti", Type: OptionString, Section: "motion",
		Description: "The confetti burst: on or off",
		Accepted:    MotionSwitches,
	},
	{
		Path: "appearance.scroll_column_max", Type: OptionInt, Section: "appearance",
		Description: "Highest a column's width may be set to in the scrolling layout, as a percent. 100 lets a column fill the screen, at the cost of the next one peeking in at the edge.",
		Min:         ScrollColumnWidthMin, Max: ScrollColumnWidthCeiling,
		Percent: true,
		Zero:    ZeroIsDefault,
	},
	{
		Path: "appearance.niri_scroll_cells", Type: OptionInt, Section: "appearance",
		Description: "Cells the scrolling layout's strip moves per mouse wheel event",
		Min:         NiriScrollCellsMin, Max: NiriScrollCellsMax,
		Zero: ZeroIsDefault,
	},
	{
		Path: "appearance.prefix_repeat_time", Type: OptionInt, Section: "appearance",
		Unit:        "ms",
		Description: "Milliseconds the prefix stays armed after a repeatable command. 0 turns it off.",
		Min:         PrefixRepeatTimeMin, Max: PrefixRepeatTimeMax,
	},
	{
		Path: "appearance.panel_padding", Type: OptionInt, Section: "appearance",
		Unit:        "columns",
		Description: "Columns of padding each side of an overlay panel's content",
		Min:         1, Max: overlay.MaxPanelPadding,
	},
	{
		Path: "appearance.dim_unfocused", Type: OptionInt, Section: "appearance",
		Description: "How much tuios fades a pane you are not in, as a percent. 0 is off.",
		Min:         0, Max: DimUnfocusedMax,
		Percent: true,
	},
	{
		Path: "appearance.dim_foreground", Type: OptionBool, Section: "appearance",
		Description: "Let dim_unfocused dim a pane's text too, not only its background",
		Default:     "true",
	},
	{
		Path: "appearance.dim_multifocus", Type: OptionBool, Section: "appearance",
		Description: "Dim the panes in the multifocus set like other panes you are not in",
	},
	// The backgrounds. appearance.background is the default for every surface;
	// each surface's own option overrides it, and empty follows it. See
	// ResolveBackground.
	{
		Path: "appearance.background", Type: OptionString, Section: "appearance",
		Description: "Background painted on every surface not set on its own (panes, desktop, window chrome, dock, rail): off (your terminal shows through), theme, or a colour (#rrggbb, 0-255, a slot name or a role name)",
		Accepted:    Backgrounds, Color: true,
	},
	{
		Path: "appearance.pane_background", Type: OptionString, Section: "appearance",
		Description: "Background behind pane content: off, theme, or a colour (#rrggbb, 0-255, a slot name or a role name); empty follows appearance.background",
		Accepted:    Backgrounds, Color: true,
	},
	{
		Path: "appearance.desktop_background", Type: OptionString, Section: "appearance",
		Description: "Background behind and between panes, including gaps and an empty workspace: off, theme, or a colour (#rrggbb, 0-255, a slot name or a role name); empty follows appearance.background",
		Accepted:    Backgrounds, Color: true,
	},
	{
		Path: "appearance.window_chrome_background", Type: OptionString, Section: "appearance",
		Description: "Background under pane borders, title bars and the lines between shared-border panes: off, theme, or a colour (#rrggbb, 0-255, a slot name or a role name); empty follows appearance.background",
		Accepted:    Backgrounds, Color: true,
	},
	{
		Path: "appearance.dock_background", Type: OptionString, Section: "dock",
		Description: "Background under the dock: off, theme, or a colour (#rrggbb, 0-255, a slot name or a role name); empty follows appearance.background",
		Accepted:    Backgrounds, Color: true,
	},
	{
		Path: "appearance.clock_format", Type: OptionString, Section: "dock",
		Description: "Go time layout the clock is drawn with, e.g. 15:04 or Mon 3:04PM",
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
		Accepted:    SidebarPositions,
		Deprecated:  "folded into [appearance.sidebar] on load; set appearance.sidebar.position",
	},
	{
		Path: "appearance.sidebar_width", Type: OptionInt, Section: "appearance",
		Unit:        "columns",
		Description: "Preferred rail width in columns",
		Min:         0,
		Deprecated:  "folded into [appearance.sidebar] on load; set appearance.sidebar.width",
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
		Accepted:    DockbarPositions,
		Flag:        "dockbar-position",
	},
	{
		Path: "appearance.dock_workspace_tabs", Type: OptionBool, Section: "dock",
		Description: "Show the clickable workspace strip in the dock",
		Default:     "true",
	},
	{
		Path: "appearance.dock_workspace_tab_format", Type: OptionString, Section: "dock",
		Description: "Workspace tab template accepting {index}, {name}, {icon} and {zoom} (empty: {name})",
	},
	{
		Path: "appearance.dock_workspace_tooltip", Type: OptionBool, Section: "dock",
		Description: "Show the workspace name on hover when the pill cuts the name or does not show it",
		Default:     "true",
	},
	{
		Path: "appearance.dock_workspace_label_max", Type: OptionInt, Section: "dock",
		Unit:        "cells",
		Description: "Cells a workspace pill's label may span before the pill cuts it (0 draws the whole name and scrolls the strip instead)",
		Default:     "12", Min: 0, Max: 200,
	},
	{
		Path: "appearance.dock_pill_caps", Type: OptionBool, Section: "dock",
		Description: "Draw rounded caps on every dock pill: the mode chip, the workspace tabs and the minimized windows. Off draws flat pills",
		Default:     "true",
	},
	{
		Path: "appearance.dock_prefix_indicator", Type: OptionString, Section: "dock",
		Description: "Where the dock shows an armed leader key: pill colours the mode pill, badge draws a separate badge",
		Accepted:    DockPrefixIndicators,
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
		Path: "appearance.dock_mode_pill", Type: OptionString, Section: "dock",
		Description: "What the mode pill shows while tiling is on: tiling shows the tiling icon, input shows the terminal or window icon",
		Accepted:    DockModePills,
	},
	{
		Path: "appearance.dock_mode_split_letter", Type: OptionBool, Section: "dock",
		Description: "Show V or H after the tiling icon in the mode pill. The letter is the direction of the next split",
		Default:     "true",
	},
	{
		Path: "appearance.dock_compact", Type: OptionBool, Section: "dock",
		Description: "Draw the dock as one row with no rule. The panes get one more row",
	},
	{
		Path: "appearance.show_clock", Type: OptionBool, Section: "dock",
		Description: "Show the clock overlay",
		Flag:        "show-clock",
	},
	{
		Path: "appearance.hide_clock", Type: OptionBool, Section: "dock",
		Description: "Hide the clock overlay",
		Deprecated:  "superseded by appearance.show_clock, which says the same thing the right way round",
		Flag:        "hide-clock",
	},
	{
		Path: "appearance.show_cpu", Type: OptionBool, Section: "dock",
		Description: "Show the CPU graph in the dock",
		Flag:        "show-cpu",
	},
	{
		Path: "appearance.show_ram", Type: OptionBool, Section: "dock",
		Description: "Show RAM usage in the dock",
		Flag:        "show-ram",
	},

	// [appearance.scrollbar]. The glyphs and the tint take values no closed set
	// covers, so they carry no Accepted and validation reports a bad one.
	{
		Path: "appearance.scrollbar.style", Type: OptionString, Section: "scrollbar",
		Description: "Hairline thumb over the content column, or a full-height track",
		Accepted:    ScrollbarStyles,
	},
	{
		Path: "appearance.scrollbar.thumb", Type: OptionString, Section: "scrollbar",
		Description: "One-cell glyph for the thumb. Empty uses the style's own.",
	},
	{
		Path: "appearance.scrollbar.track", Type: OptionString, Section: "scrollbar",
		Description: "One-cell glyph for the track, or none. Empty uses the style's own.",
	},
	{
		Path: "appearance.scrollbar.tint", Type: OptionString, Section: "scrollbar",
		Description: "Bar colour: quiet, border, muted, or a colour (#rrggbb, 0-255, a slot name or a role name)",
		Accepted:    ScrollbarTints, Color: true,
	},

	// [appearance.selection]. Every background is a colour literal; a
	// foreground may also be empty, which keeps the text the colour the
	// program wrote it in. No closed set covers a colour, so none carries
	// Accepted and validation reports a bad one.
	{
		Path: "appearance.selection.bg", Type: OptionString, Section: "selection",
		Description: "Background behind selected text",
		Color:       true,
	},
	{
		Path: "appearance.selection.fg", Type: OptionString, Section: "selection",
		Description: "Selected text colour. Empty keeps the colour it already has.",
		Color:       true,
	},
	{
		Path: "appearance.selection.bold", Type: OptionBool, Section: "selection",
		Description: "Draw selected text bold as well as tinted",
		Default:     "false",
	},
	{
		Path: "appearance.selection.search_bg", Type: OptionString, Section: "selection",
		Description: "Background behind every search match",
		Color:       true,
	},
	{
		Path: "appearance.selection.search_fg", Type: OptionString, Section: "selection",
		Description: "Text colour of a search match. Empty keeps the colour it has.",
		Color:       true,
	},
	{
		Path: "appearance.selection.match_bg", Type: OptionString, Section: "selection",
		Description: "Background behind the match the cursor is on",
		Color:       true,
	},
	{
		Path: "appearance.selection.match_fg", Type: OptionString, Section: "selection",
		Description: "Text colour of the match the cursor is on. Empty keeps it.",
		Color:       true,
	},
	{
		Path: "appearance.selection.cursor_bg", Type: OptionString, Section: "selection",
		Description: "Background of the copy mode cursor block",
		Color:       true,
	},
	{
		Path: "appearance.selection.cursor_fg", Type: OptionString, Section: "selection",
		Description: "Text colour under the copy mode cursor. Empty keeps it.",
		Color:       true,
	},

	{
		Path: "appearance.selection.flash", Type: OptionBool, Section: "selection",
		Description: "Sweep a band of light over text that was just copied. Set motion to none to turn it off.",
	},
	{
		Path: "appearance.selection.flash_ms", Type: OptionInt, Section: "selection",
		Description: "Milliseconds one copy sweep takes",
		Min:         CopyFlashMsMin, Max: CopyFlashMsMax,
	},
	{
		Path: "appearance.selection.flash_style", Type: OptionString, Section: "selection",
		Description: "The shape the copy sweep takes",
		Accepted:    CopyFlashStyles,
	},
	{
		Path: "appearance.selection.flash_color", Type: OptionString, Section: "selection",
		Description: "The light the copy sweep is made of",
		Color:       true,
	},
	{
		Path: "appearance.selection.multi_format", Type: OptionString, Section: "selection",
		Description: "The format multi copy mode yanks in: plain, markdown or json",
		Accepted:    MultiCopyFormats,
	},
	{
		Path: "appearance.selection.copy_entry", Type: OptionString, Section: "selection",
		Description: "Where copy mode puts its cursor: on the terminal cursor or in the center",
		Accepted:    CopyEntries,
	},
	{
		Path: "appearance.selection.osc52_write", Type: OptionString, Section: "selection",
		Description: "What happens when a program in a pane sets the clipboard: off, ask, focused (the focused pane only, others ask) or on",
		Accepted:    OSC52WriteModes,
	},
	{
		Path: "appearance.selection.copy_command", Type: OptionString, Section: "selection",
		Description: "A command that gets each copy mode yank on stdin. Its output goes to the clipboard. If this is empty, tuios copies the selection as it is.",
	},

	// [appearance.sidebar]
	{
		Path: "appearance.sidebar.enabled", Type: OptionBool, Section: "sidebar",
		Description: "Show the session rail",
	},
	{
		Path: "appearance.sidebar.per_session", Type: OptionBool, Section: "sidebar",
		Description: "Each session keeps its own shown or hidden rail. Off, showing or hiding the rail applies to every session",
		Default:     "false",
	},
	{
		Path: "appearance.sidebar.position", Type: OptionString, Section: "sidebar",
		Description: "Edge the rail sits on, or hidden",
		Accepted:    SidebarPositions,
	},
	{
		Path: "appearance.sidebar.width", Type: OptionInt, Section: "sidebar",
		Unit:        "columns",
		Description: "Preferred rail width in columns on a wide screen",
		Min:         0,
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
		Path: "appearance.sidebar.peek", Type: OptionBool, Section: "sidebar",
		Description: "Show a session's panes in the terminals section while the pointer or the rail cursor is on its row",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.tooltips", Type: OptionBool, Section: "sidebar",
		Description: "Label the collapsed strip on hover",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.background", Type: OptionString, Section: "sidebar",
		Description: "Background under the rail: off, theme, or a colour (#rrggbb, 0-255, a slot name or a role name); empty follows appearance.background",
		Accepted:    Backgrounds, Color: true,
	},
	{
		Path: "appearance.sidebar.cursor_background", Type: OptionString, Section: "sidebar",
		Description: "Background of the row under the cursor while the rail has the keyboard: a colour (#rrggbb, 0-255, a slot name or a role name); empty keeps the theme's",
		Color:       true,
	},
	{
		Path: "appearance.sidebar.sections", Type: OptionString, Section: "sidebar",
		Description: "Section names in the order the rail stacks them, each with an optional percent share. A name left out is a section the rail does not draw, and \"spacer\" is an empty block you may repeat. The files control can still open a files section that the list leaves out.",
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
		Accepted:    SidebarFolderClicks,
	},
	{
		Path: "appearance.sidebar.file_drag", Type: OptionBool, Section: "sidebar",
		Description: "Drag a row of the files section into a pane to paste its path",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.files_style", Type: OptionString, Section: "sidebar",
		Description: "How the files section draws: list shows one folder at a time, tree opens folders in place under one root",
		Accepted:    SidebarFilesStyles,
	},
	{
		Path: "appearance.sidebar.group_main", Type: OptionBool, Section: "sidebar",
		Description: "Give each repository a row of its own, with the main checkout under it. When off, the main checkout's row is the repository's row",
		Default:     "false",
	},
	{
		Path: "appearance.sidebar.header_case", Type: OptionString, Section: "sidebar",
		Description: "How the rail's section headers read: lowercase keeps the quiet furniture look, uppercase draws them as headings",
		Accepted:    RailHeaderCases,
	},
	{
		Path: "appearance.sidebar.row_style", Type: OptionString, Section: "sidebar",
		Description: "How the rail marks the row you are on: bar in the gutter, marker glyph, underline, or pill",
		Accepted:    RailRowStyles,
	},
	{
		Path: "appearance.sidebar.header_style", Type: OptionString, Section: "sidebar",
		Description: "How a rail section header is drawn: auto, plain, rule, pill, or icon with the section's icon",
		Accepted:    RailHeaderStyles,
	},
	{
		Path: "appearance.sidebar.density", Type: OptionString, Section: "sidebar",
		Description: "Space between the rail's sections: normal, or roomy for a blank row in front of each section after the first",
		Accepted:    RailDensities,
	},
	{
		Path: "appearance.sidebar.editor", Type: OptionString, Section: "sidebar",
		Description: "The terminal editor that opens a text file. If this is empty, tuios uses $EDITOR, then $VISUAL, then vi.",
	},
	{
		Path: "appearance.sidebar.file_actions", Type: OptionBool, Section: "sidebar",
		Description: "Let the files section create, rename, delete, copy and paste",
		Default:     "true",
	},
	{
		Path: "appearance.sidebar.file_delete", Type: OptionString, Section: "sidebar",
		Description: "Where a delete sends the file: the trash, or nowhere",
		Accepted:    SidebarFileDeletes,
	},
	{
		Path: "appearance.sidebar.agent_rest_fold", Type: OptionString, Section: "sidebar",
		Description: "How long an agent row rests (idle, unknown, or done and seen) before the rail folds it into one line: a duration such as 1h, or off",
		Check:       checkAgentRestFold,
	},
	{
		Path: "appearance.sidebar.subagent_rows", Type: OptionString, Section: "sidebar",
		Description: "How an agent row shows its subagents: a row for each under it, the rows on tree lines, or the count alone",
		Accepted:    SidebarSubagentRowsValues,
		Fallback:    SidebarSubagentRowsRows,
	},
	{
		Path: "appearance.sidebar.subagent_collapse", Type: OptionString, Section: "sidebar",
		Description: "A heading line over each agent's subagents that opens and shuts their rows: off, or on and expanded or collapsed at the start",
		Accepted:    SidebarSubagentCollapseValues,
		Fallback:    SidebarSubagentCollapseOff,
	},
	{
		Path: "appearance.sidebar.subagent_fields", Type: OptionString, Section: "sidebar",
		Description: "What each subagent row shows, a comma list of mark, name, type, time and note",
		Check:       CheckSubagentFields,
	},
	{
		Path: "appearance.sidebar.subagent_max_rows", Type: OptionInt, Section: "sidebar",
		Unit:        "lines",
		Description: "The most lines one agent's subagents take, the +N more line included. 0 is no limit.",
		Min:         0, Max: SidebarSubagentMaxRowsMax,
	},
	{
		Path: "appearance.sidebar.subagent_hide_finished", Type: OptionString, Section: "sidebar",
		Description: "How long a finished subagent's row stays after it ends: a duration such as 30s, or off to keep it until the daemon drops it",
		Check:       CheckSubagentHide,
	},
	{
		Path: "appearance.sidebar.workspaces", Type: OptionString, Section: "sidebar",
		Description: "Workspace chip band the rail used to draw",
		Deprecated:  "no longer used: panes name their own workspace, and switching lives on the dock and alt+1..9",
	},

	// [startup]
	{
		Path: "startup.open_default_window", Type: OptionBool, Section: "startup",
		Description: "Open one terminal automatically when a session starts empty",
	},
	{
		Path: "startup.tiled", Type: OptionBool, Section: "startup",
		Description: "Start a new session tiled instead of floating. False when the config file has no [startup] table.",
	},
	{
		Path: "startup.start_in_terminal_mode", Type: OptionBool, Section: "startup",
		Description: "Start focused in terminal mode, when a window is present",
	},
	{
		Path: "startup.layout", Type: OptionString, Section: "startup",
		Description: "Tiling scheme a new session starts in. An existing session keeps its own.",
		Accepted:    LayoutModes,
	},
	{
		Path: "startup.daemon", Type: OptionBool, Section: "startup",
		Description: "Make a bare \"tuios\" attach to a daemon-backed session instead of running standalone. TUIOS_NO_DAEMON=1 or --standalone overrides it. False when the config file has no [startup] table.",
	},
	{
		Path: "startup.session", Type: OptionString, Section: "startup",
		Description: "Session a bare \"tuios\" attaches to. tuios makes it when it is gone. Empty attaches to the most recent session.",
	},
	{
		Path: "startup.window", Type: OptionString, Section: "startup",
		Description: "Pane, by name or id, that a bare \"tuios\" focuses in startup.session. When no pane has that name, the focus does not change.",
	},

	// [agents]. Only enabled is a scalar; the rest of the table is lists and
	// tables, which stay in the file.
	{
		Path: "agents.enabled", Type: OptionBool, Section: "agents",
		Description: "Agents, the Inbox, mail and approvals. Turn off to keep only the multiplexer.",
		Default:     "true",
	},
	{
		Path: "agents.fold_working", Type: OptionBool, Section: "agents",
		Description: "Fold the Working group of the agents view. F folds or opens a group, and the view keeps that choice.",
		Default:     "false",
	},
	{
		Path: "agents.view_list", Type: OptionString, Section: "agents",
		Description: "How the agents view shows its list on a wide screen: a column beside the focus, a strip of chips above it, or a strip for two agents or fewer. The \\ key switches it, and the view keeps that choice.",
		Accepted:    AgentsViewLists,
		Fallback:    AgentsViewListColumn, Normalize: trimLower,
	},
	{
		Path: "agents.card_preview", Type: OptionString, Section: "agents",
		Description: "Where the task board shows the focused card's preview: beside the columns, over them, beside them on a wide screen and over them otherwise, or never. Space on a card opens and closes it.",
		Accepted:    CardPreviews,
		Fallback:    CardPreviewAuto, Normalize: trimLower,
	},
	{
		Path: "agents.card_preview_width", Type: OptionString, Section: "agents",
		Description: "The width of the task board's preview panel: a number of cells, such as 70, or a share of the view, such as 40%. Empty is 40%, at least 44 and at most 96 cells. A drag on the edge of the panel changes it, and the view keeps that choice.",
	},
	{
		Path: "agents.list_width", Type: OptionString, Section: "agents",
		Description: "The width of the agents view's list: a number of cells, such as 52, or a share of the view, such as 30%. Empty follows the screen. The < and > keys and a drag on the edge of the list change it, and the view keeps that choice.",
	},
	{
		Path: "agents.ask_wait_seconds", Type: OptionInt, Section: "agents",
		Description: "Seconds ask-human waits for your answer when the agent names no timeout. The question stays after the wait. 0 uses 120",
		Min:         0, Max: 3600, Zero: ZeroIsDefault,
	},
	{
		Path: "agents.undo_window_seconds", Type: OptionInt, Section: "agents",
		Description: "Seconds after a dismiss, a snooze or a queued reply that u takes it back. 0 uses 10",
		Min:         0, Max: 300, Zero: ZeroIsDefault,
	},
	{
		Path: "agents.reply_rows", Type: OptionInt, Section: "agents",
		Unit:        "rows",
		Description: "The most rows a multi-line editor grows to before it scrolls: a reply to an agent or to many, a mail draft and a new agent's task. 0 keeps each editor's own: 8 for a reply, 6 for mail, 3 for a task. An editor never takes more rows than its view has, and alt+m in it takes all of them.",
		Min:         0, Max: ReplyRowsMax,
	},

	// [tasks]. The daemon reads it when it starts and when the file changes.
	{
		Path: "tasks.auto_stage", Type: OptionBool, Section: "agents",
		Description: "Move task cards on their own when tuios sees their work move: a plan held and then approved, a fan check, a pull request. Off leaves every stage to you.",
		Default:     "true",
	},
	{
		Path: "tasks.machine_cap", Type: OptionInt, Section: "agents",
		Description: "How many agents may work on this machine before Any machine starts a new one on another machine. Agents that work, agents that start, and picks not yet started count. 0 is no cap.",
		Min:         0, Max: 1024,
	},
	{
		Path: "tasks.attachment_max_mib", Type: OptionInt, Section: "agents",
		Description: "The largest file that tuios task attach puts on a card. A larger file is refused with an error that names this option. 0 uses 8",
		Min:         0, Max: AttachmentMaxMiBMax, Zero: ZeroIsDefault, Unit: "MiB",
	},
	{
		Path: "tasks.attachments_per_card", Type: OptionInt, Section: "agents",
		Description: "The most attachments one task card holds. 0 uses 20",
		Min:         0, Max: AttachmentsPerCardMax, Zero: ZeroIsDefault,
	},
	{
		Path: "tasks.attachment_session_mib", Type: OptionInt, Section: "agents",
		Description: "The most that the files on one session's task cards take together. 0 uses 256",
		Min:         0, Max: AttachmentSessionMiBMax, Zero: ZeroIsDefault, Unit: "MiB",
	},

	// [daemon]. agent_binaries is absent: it is a list, and a value that arrives
	// as one string has no unambiguous spelling for a list. respond_from_shell
	// is absent too: it grants acting as the person, and set-option is a verb
	// any pane can call, so it is set in the file and nowhere else.
	{
		Path: "daemon.log_level", Type: OptionString, Section: "daemon",
		Description: "How much the daemon logs",
		Accepted:    daemonLogLevels,
	},
	{
		Path: "daemon.window_size", Type: OptionString, Section: "daemon",
		Description: "The size of a session with more than one client: the client that last had input, the smallest client, or the largest client",
		Accepted:    WindowSizeModes,
		Fallback:    WindowSizeLatest,
	},
	{
		Path: "daemon.agent_autodetect", Type: OptionBool, Section: "daemon",
		Description: "Detect a pane's foreground agent CLI and set its state glyph",
		Default:     "true",
		Env:         "TUIOS_AGENT_AUTODETECT",
	},
	{
		Path: "daemon.agent_detect_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds between checks. 0 uses 2. A negative number turns checks off.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_AGENT_DETECT_SECONDS",
	},
	{
		Path: "daemon.agent_stall_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds of silence after which a working agent shows as unknown. 0 uses 30. A negative number turns it off.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_AGENT_STALL_SECONDS",
	},
	{
		Path: "daemon.subagent_fade_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds a finished subagent stays in the list. 0 uses 60.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_SUBAGENT_FADE_SECONDS",
	},
	{
		Path: "daemon.cleanup_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds between releases of the memory that pane output leaves behind. 0 uses 30.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_CLEANUP_SECONDS",
	},
	{
		Path: "daemon.pr_poll_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds between checks of the pull requests the sidebar shows. 0 uses 60.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_PR_POLL_SECONDS",
	},
	{
		Path: "daemon.transfer_keep_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds a finished file transfer stays in the list. 0 uses 1800.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_TRANSFER_KEEP_SECONDS",
	},
	{
		Path: "daemon.save_seconds", Type: OptionInt, Section: "daemon",
		Description: "Most seconds a session whose panes printed waits to be saved, which keeps each pane's folder current for a restore",
		Min:         10, Max: 600, Zero: ZeroIsDefault,
	},
	{
		Path: "daemon.exec_timeout_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds a command run with tuios exec may take when the caller names no timeout. 0 uses 30",
		Min:         0, Max: 3600, Zero: ZeroIsDefault,
	},
	{
		Path: "daemon.host_heartbeat_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds a link to another machine may carry nothing before the daemon pings it. 0 uses 15",
		Min:         0, Max: 300, Zero: ZeroIsDefault,
	},
	{
		Path: "daemon.host_heartbeat_timeout_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds one ping to another machine waits for its reply. Two misses end the link. 0 uses 5",
		Min:         0, Max: 60, Zero: ZeroIsDefault,
	},
	{
		Path: "daemon.host_call_timeout_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds one listing over a live link may take before the link is dialed again. 0 uses 8",
		Min:         0, Max: 120, Zero: ZeroIsDefault,
	},
	{
		Path: "daemon.reconnect_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds a client waits for a restarted daemon before it gives up. 0 uses 15",
		Min:         0, Max: 300, Zero: ZeroIsDefault,
	},
	{
		Path: "daemon.dispatch_ready_seconds", Type: OptionInt, Section: "daemon",
		Description: "Seconds a dispatched task waits for its agent to be ready, at most 600. 0 uses the start-agent wait.",
		Zero:        ZeroIsDefault,
		Env:         "TUIOS_DISPATCH_READY_SECONDS",
	},
	{
		Path: "daemon.keep_awake", Type: OptionString, Section: "daemon",
		Description: "Keep the machine from sleeping: while an agent works on this machine, while the daemon runs, or never. The display can still lock.",
		Accepted:    KeepAwakeModes,
		Fallback:    KeepAwakeOff,
	},
	{
		Path: "daemon.resume_agents", Type: OptionString, Section: "daemon",
		Description: "After a daemon restart, ask to resume each pane's agent conversation, resume it, or do neither",
		Accepted:    ResumeAgentsModes,
		Fallback:    "ask",
	},
	{
		Path: "daemon.persist_scrollback", Type: OptionBool, Section: "daemon",
		Description: "Save each pane's history and show it again after a daemon restart. A change applies when the daemon next starts.",
		Default:     "true",
	},
	{
		Path: "daemon.client_follows_upgrade", Type: OptionBool, Section: "daemon",
		Description: "After a live upgrade of the daemon to a newer build, a client on this machine runs the new build too, on the same terminal and session.",
		Default:     "true",
	},
	{
		Path: "daemon.persist_scrollback_lines", Type: OptionInt, Section: "daemon",
		Description: "Most history lines one pane saves. 0 uses 1000. A change applies when the daemon next starts.",
		Min:         0, Max: 1000000,
	},
	{
		Path: "daemon.persist_scrollback_kb", Type: OptionInt, Section: "daemon",
		Description: "Most KiB one pane's saved history takes on disk. 0 uses 2048. A change applies when the daemon next starts.",
		Min:         0, Max: 1048576,
	},

	// [notifications]
	{
		Path: "notifications.duration", Type: OptionInt, Section: "notifications",
		Unit:        "s",
		Description: "Seconds an info message stays up. 0 uses the default, 2.",
		Min:         0, Max: 3600,
	},
	{
		Path: "notifications.warning_duration", Type: OptionInt, Section: "notifications",
		Unit:        "s",
		Description: "Seconds a warning stays up. 0 uses the default, 8.",
		Min:         0, Max: 3600,
	},
	{
		Path: "notifications.error_duration", Type: OptionInt, Section: "notifications",
		Unit:        "s",
		Description: "Seconds an error stays up when error_sticky is false. 0 uses the default, 15.",
		Min:         0, Max: 3600,
	},
	{
		Path: "notifications.error_sticky", Type: OptionBool, Section: "notifications",
		Description: "Make errors wait for esc instead of expiring",
		Default:     "true",
	},
	{
		Path: "notifications.bell", Type: OptionString, Section: "notifications",
		Description: "What a pane's bell does: a dock message, a bell in your terminal, both, or nothing",
		Accepted:    BellModes,
		Fallback:    BellToast,
	},
	{
		Path: "notifications.style", Type: OptionString, Section: "notifications",
		Description: "How a dock message looks: full shows a side bar and a countdown line, plain shows the icon and the text only",
		Accepted:    NotificationStyles,
		Fallback:    NotificationStyleFull,
	},
	{
		Path: "notifications.level", Type: OptionString, Section: "notifications",
		Description: "The least severe tuios message the dock shows: info shows every message, warning only warnings and errors, error only errors. Agent and pane alerts do not use this level",
		Accepted:    NotificationLevels,
		Fallback:    NotificationLevelInfo,
	},
	{
		Path: "notifications.log_hidden", Type: OptionBool, Section: "notifications",
		Description: "Put messages below notifications.level in the log viewer. When off, they go nowhere. Warnings and errors always go to the log",
		Default:     "true",
	},
	{
		Path: "notifications.machine_offer", Type: OptionBool, Section: "notifications",
		Description: "Offer to add a machine when a pane logs in to it with ssh or mosh and no [hosts] entry reaches it",
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
		Accepted:    AgentSoundModeNames,
		Fallback:    string(defaultAgentSoundMode), Normalize: strings.TrimSpace,
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

	{
		Path: "appearance.whichkey_delay_ms", Type: OptionInt, Section: "appearance",
		Description: "Milliseconds after the leader key before the which-key panel opens. 0 opens it at once.",
		Min:         WhichKeyDelayMSMin, Max: WhichKeyDelayMSMax,
	},
	{
		Path: "appearance.tooltip_delay_ms", Type: OptionInt, Section: "appearance",
		Description: "Milliseconds the pointer rests on a control before its tooltip shows. 0 shows it at once.",
		Min:         TooltipDelayMSMin, Max: TooltipDelayMSMax,
	},
	{
		Path: "appearance.zen_reveal_ms", Type: OptionInt, Section: "appearance",
		Description: "Milliseconds the pointer sits still before zen mode (mouse) hides the borders again",
		Min:         ZenRevealMSMin, Max: ZenRevealMSMax,
		Zero: ZeroIsDefault,
	},
	{
		Path: "appearance.paste_preview_ms", Type: OptionInt, Section: "appearance",
		Description: "Milliseconds the paste card stays up with nothing pressed",
		Min:         PastePreviewMSMin, Max: PastePreviewMSMax,
		Zero: ZeroIsDefault,
	},
	{
		Path: "appearance.chord_timeout_ms", Type: OptionInt, Section: "appearance",
		Description: "Milliseconds the first key of a two-key chord (gg in copy mode and the scrollback browser) waits for the second",
		Min:         ChordTimeoutMSMin, Max: ChordTimeoutMSMax,
		Zero: ZeroIsDefault,
	},
	// [mouse]
	{
		Path: "mouse.double_click_ms", Type: OptionInt, Section: "mouse",
		Description: "Milliseconds after a click in which a second one counts as a double click, in a pane, on the rail divider and in the Agents list",
		Min:         MouseDoubleClickMin, Max: MouseDoubleClickMax,
		Zero: ZeroIsDefault,
	},
	{
		Path: "mouse.drag_threshold", Type: OptionInt, Section: "mouse",
		Description: "Cells the pointer moves before a press is a drag and not a click",
		Min:         MouseDragThresholdMin, Max: MouseDragThresholdMax,
		Zero: ZeroIsDefault,
	},
	// [tape]
	{
		Path: "tape.autorun", Type: OptionString, Section: "tape",
		Description: "What happens on entering a directory with a project tape",
		Accepted:    TapeAutorunModes,
	},
	{
		Path: "tape.auto_review", Type: OptionBool, Section: "tape",
		Description: "Open the review dialog on detection instead of only badging it",
	},
	{
		Path: "tape.show_status", Type: OptionBool, Section: "tape",
		Description: "Show the RUNNING pill and a message while a tape plays",
		Default:     "true",
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
	},

	// [debug]
	{
		Path: "debug.show_key_events", Type: OptionBool, Section: "debug",
		Description: "Show the on-screen keycast of recent keypresses",
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
		Accepted:    ScreenshotFormats,
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
	},
	{
		Path: "screenshot.frame", Type: OptionString, Section: "screenshot",
		Description: "Dressing around the capture: a window card, a plain card, or nothing",
		Accepted:    ScreenshotFrames,
	},
	{
		Path: "screenshot.background", Type: OptionString, Section: "screenshot",
		Description: "Wash behind the card: auto derives it from the theme; none, a hex color, or hex..hex work too",
	},
	{
		Path: "screenshot.padding", Type: OptionInt, Section: "screenshot",
		Unit:        "px",
		Description: "Space around the card in pixels",
		Default:     "48", Min: 0, Max: ScreenshotMaxPadding,
	},
	{
		Path: "screenshot.radius", Type: OptionInt, Section: "screenshot",
		Unit:        "px",
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
		Accepted:    ScreenshotControlSet,
	},
	{
		Path: "screenshot.title_format", Type: OptionString, Section: "screenshot",
		Description: "Title bar text of a screenshot: {title} (the window title as its title bar shows it), {index} and {cwd}",
	},
	{
		Path: "screenshot.font_family", Type: OptionString, Section: "screenshot",
		// A capture on kitty is already drawn in the terminal's own font,
		// because kitty answers when asked which font that is. This is the
		// answer for every other terminal, and it names a font rather than a
		// file so it also names the SVG and HTML output.
		Description: "Font to draw the capture in when your terminal does not say which it uses",
	},
	{
		Path: "screenshot.font_file", Type: OptionString, Section: "screenshot",
		Description: "Font file to draw PNG with, also embedded in SVG and HTML. " +
			"It wins over every other font choice.",
	},
	{
		Path: "screenshot.scale", Type: OptionInt, Section: "screenshot",
		Description: "PNG size multiplier",
		Default:     "2", Min: 1, Max: ScreenshotMaxScale,
	},
	{
		Path: "screenshot.cursor", Type: OptionBool, Section: "screenshot",
		Description: "Draw the cursor cell in the capture",
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
		Min:         ScreensaverMinIdleMinutes, Max: ScreensaverMaxIdleMinutes,
	},
	{
		Path: "screensaver.effect", Type: OptionString, Section: "screensaver",
		Description: "Which effect runs, or random for a different one each time",
		Accepted:    ScreensaverEffects,
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
		Accepted:    SpotlightFollowModes,
	},
	{
		Path: "spotlight.radius", Type: OptionInt, Section: "spotlight",
		Unit:        "rows",
		Description: "Half the beam's height, in rows",
		Min:         SpotlightMinRadius, Max: SpotlightMaxRadius,
	},
	{
		Path: "spotlight.dim", Type: OptionInt, Section: "spotlight",
		Description: "Percent of its light an unlit cell loses",
		Min:         SpotlightMinDim, Max: SpotlightMaxDim,
		Percent: true,
	},
	{
		Path: "spotlight.edge", Type: OptionString, Section: "spotlight",
		Description: "Cut the beam at its edge, or fade it out. A fade sends about three times the bytes each time the beam moves",
		Accepted:    SpotlightEdges,
	},
	{
		Path: "spotlight.shake", Type: OptionBool, Section: "spotlight",
		Description: "Move the mouse left and right fast to turn the beam on and off. A shake does not move the beam. The follow setting decides where it goes",
	},

	// [pip]. Read each time the picture-in-picture view is drawn, and
	// client-local like the spotlight.
	{
		Path: "pip.width", Type: OptionInt, Section: "pip",
		Unit:        "cells",
		Description: "Width of the picture-in-picture view, border included, in cells",
		Min:         PiPMinWidth, Max: PiPMaxWidth,
	},
	{
		Path: "pip.height", Type: OptionInt, Section: "pip",
		Unit:        "cells",
		Description: "Height of the picture-in-picture view, border included, in cells",
		Min:         PiPMinHeight, Max: PiPMaxHeight,
	},
	{
		Path: "pip.corner", Type: OptionString, Section: "pip",
		Description: "Corner the picture-in-picture view goes to first. It moves to another corner when the cursor enters it",
		Accepted:    PiPCorners,
	},

	// [mermaid]. Read each time tuios mermaid runs.
	{
		Path: "mermaid.theme", Type: OptionString, Section: "mermaid",
		Description: "Colours of tuios mermaid: tuios for the colours of your theme, or a named theme such as flexoki, nord or rose-pine",
	},
	{
		Path: "mermaid.background", Type: OptionString, Section: "mermaid",
		Description: "Background of a tuios mermaid picture: theme for the theme's colour, or none for the terminal's own background",
		Accepted:    []string{"theme", "none"},
	},

	// [hints]. Read each time hints mode opens. hints.patterns is a list and
	// is set in the file only, like [keybindings].
	{
		Path: "hints.builtins", Type: OptionString, Section: "hints",
		Description: "Built-in patterns hints mode looks for: all, none, or names such as url,path,sha",
	},
	{
		Path: "hints.alphabet", Type: OptionString, Section: "hints",
		Description: "Letters the hint labels are made of, easiest first",
	},
	{
		Path: "hints.open", Type: OptionBool, Section: "hints",
		Description: "Ctrl and a label opens the URL or path. It does not open on a remote client",
		Default:     "true",
	},
	{
		Path: "hints.dim", Type: OptionInt, Section: "hints",
		Description: "Percent of its light the text around the hints loses",
		Min:         HintsMinDim, Max: HintsMaxDim,
		Percent: true,
	},
	{
		Path: "hints.all_panes", Type: OptionBool, Section: "hints",
		Description: "The hints key labels every pane on the workspace, not only the focused pane",
	},

	// [path_picker]. Read each time the picker opens.
	{
		Path: "path_picker.show_hidden", Type: OptionBool, Section: "panes",
		Description: "The path picker lists hidden files. Alt+h in the picker shows or hides them",
	},

	// [panes]. Read each time display_panes opens.
	{
		Path: "panes.label_keys", Type: OptionString, Section: "panes",
		Description: "Keys the pane labels are made of, first pane first. Letters a to z and digits",
	},
	{
		Path: "panes.navigator_layout", Type: OptionString, Section: "panes",
		Description: "How the pane navigator lists the panes when it opens",
		Accepted:    NavigatorLayouts,
		Fallback:    NavigatorLayoutTree, Normalize: trimLower,
	},
	{
		Path: "panes.navigator_search", Type: OptionBool, Section: "panes",
		Description: "Open the pane navigator ready to type a search",
	},
	{
		Path: "panes.drag_and_drop", Type: OptionBool, Section: "panes",
		Description: "Drag and drop between panes over the kitty drag and drop protocol (OSC 72)",
	},
	{
		Path: "panes.drag_and_drop_desktop", Type: OptionBool, Section: "panes",
		Description: "Bridge drags to and from the desktop through a terminal that speaks the protocol (kitty)",
	},

	// [scratch]. Read each time toggle_scratch creates or shows the scratch
	// terminal. A show resizes the popup to the size in force.
	{
		Path: "scratch.width", Type: OptionString, Section: "scratch",
		Description: "Width of the scratch terminal, in cells (60) or percent (80%)",
		BoxSize:     true,
	},
	{
		Path: "scratch.height", Type: OptionString, Section: "scratch",
		Description: "Height of the scratch terminal, in cells (20) or percent (80%)",
		BoxSize:     true,
	},
	// [launcher]. Read each time the launcher starts an entry.
	{
		Path: "launcher.gui_command", Type: OptionString, Section: "launcher",
		Description: "Command that starts a graphical desktop entry instead of a pane. The entry's argv is added to it. Use a command that takes an argv, such as \"tuios-wayland launch --\" or \"niri msg action spawn --\". Do not use one that joins its arguments into a shell line, such as \"swaymsg exec --\" or \"hyprctl dispatch exec\".",
	},
	{
		Path: "launcher.keep_open", Type: OptionBool, Section: "launcher",
		Description: "Keep the pane of a program the launcher started after the program exits. A line under the output shows the exit status. Needs a daemon session.",
	},
	// [workspaces]. The daemon reads it when it starts and when the file
	// changes.
	{
		Path: "workspaces.return_when_empty", Type: OptionBool, Section: "workspaces",
		Description: "When the last pane on the workspace on screen closes, show the workspace you came from. Off shows the splash screen",
		Default:     "true",
	},
	// [shell]. The daemon reads it each time it starts a pane.
	{
		Path: "shell.integration", Type: OptionString, Section: "appearance",
		Description: "Turn on prompt marks in the bash and zsh shells that new panes start. Your files do not change. off starts each shell as it is",
		Accepted:    ShellIntegrationModes,
		Fallback:    ShellIntegrationAuto,
	},
	{
		Path: "shell.login", Type: OptionBool, Section: "appearance",
		Description: "Start the shell of each new pane as a login shell, so it reads .zprofile, .bash_profile or .profile, as a terminal on macOS does",
	},
	// [paste_buffers]. The daemon reads it when it starts and when the file
	// changes, and so does a client with no daemon.
	{
		Path: "paste_buffers.limit", Type: OptionInt, Section: "selection",
		Description: "How many yanks to keep to paste again. When it is full, the oldest goes. 0 keeps none.",
		Min:         0, Max: 1000,
	},
	{
		Path: "paste_buffers.max_kb", Type: OptionInt, Section: "selection",
		Description: "Most KiB all paste buffers hold together, at most 262144 (256 MiB). When it is full, the oldest go. 0 uses 16384.",
		Min:         0, Max: 262144,
	},
	// [worktree]. The daemon reads it when it starts and when the file
	// changes.
	{
		Path: "worktree.path", Type: OptionString, Section: "worktree",
		Description: "Where new worktrees go, as a template of {{ repo_path }}, {{ repo }} and {{ branch }}, with | slug to make one folder name. A relative path starts at repo_path. Empty puts them under $XDG_DATA_HOME/tuios/worktrees",
		Check:       worktree.ValidatePathTemplate,
	},
	// The setup runs a command, so a file change applies only when it
	// clears the setup or leaves it as it is. A new command waits for tuios
	// config apply from outside tuios, or a daemon restart.
	{
		Path: "worktree.setup", Type: OptionString, Section: "worktree",
		Description: "A shell command to run in every new worktree before the agent starts, such as pnpm i. Empty runs nothing",
	},
	{
		Path: "worktree.setup_async", Type: OptionBool, Section: "worktree",
		Description: "Start the agent at once while the setup runs. A failed setup then keeps the worktree and the branch",
	},
	// [review]. Read each time a review opens.
	{
		Path: "review.focus", Type: OptionString, Section: "review",
		Description: "Which side of the review overlay has the keys when a review opens: the diff or the file list",
		Accepted:    ReviewFocuses,
		Fallback:    ReviewOpenOnDiff, Normalize: trimLower,
	},
	{
		Path: "review.scope", Type: OptionString, Section: "review",
		Description: "What a review shows when it opens: the changes since the base, or only the uncommitted changes. u switches between them",
		Accepted:    ReviewScopes,
		Fallback:    ReviewScopeBase, Normalize: trimLower,
	},
	// [files]: o on a file of another machine. Read each time o is pressed;
	// the daemon reads the keep at its start.
	{
		Path: "files.open_copy_ask_mb", Type: OptionInt, Section: "files",
		Description: "Above this size in MiB, o on a file of another machine shows the size and asks again before it copies the file here. 0 uses 200",
		Min:         0, Max: 1 << 20,
	},
	{
		Path: "files.open_copy_max_mb", Type: OptionInt, Section: "files",
		Description: "Above this size in MiB, o on a file of another machine does not copy it here. 0 uses 4096",
		Min:         0, Max: 1 << 20,
	},
	{
		Path: "files.drop_keep_hours", Type: OptionInt, Section: "files",
		Description: "Hours a file dropped into a pane of another machine stays in that machine's drop folder. 0 uses 24",
		Min:         0, Max: 8760, Zero: ZeroIsDefault,
	},
	{
		Path: "files.drop_keep_mb", Type: OptionInt, Section: "files",
		Description: "Most MiB the drop folder holds. A drop that does not fit is refused. 0 uses 256",
		Min:         0, Max: 1 << 16, Zero: ZeroIsDefault,
	},
	{
		Path: "files.open_copy_keep_days", Type: OptionInt, Section: "files",
		Description: "Days a copy of a far file stays when nobody opens it again. 0 uses 7, and -1 keeps every copy",
		Min:         -1, Max: 3650,
	},
	// Read by the client on each switch, so a change applies at once.
	{
		Path: "workspaces.new_window_when_empty", Type: OptionBool, Section: "workspaces",
		Description: "When you switch to a workspace with no panes, open a new pane there. It starts in the folder of the pane you came from",
	},
}

// optionIndex indexes the registry for lookup, and fills each option's
// derived Default first (see deriveOptionDefaults). It is built on first use,
// not at init: deriving reads DefaultConfig and walks every path by
// reflection, about 1 ms and 680 allocations, and most runs of tuios are short
// commands that never read an option.
var optionIndex = sync.OnceValue(func() map[string]Option {
	deriveOptionDefaults(optionSpecs, DefaultConfig())
	byPath := make(map[string]Option, len(optionSpecs))
	for _, opt := range optionSpecs {
		byPath[opt.Path] = opt
	}
	return byPath
})

// deriveOptionDefaults fills each option's Default from the field DefaultConfig
// sets, so the default is written once, in DefaultConfig. A field DefaultConfig
// leaves unset (a nil pointer, an empty auto-or-int field) keeps the Default
// the entry spells, because that entry is then the only place the default is
// written.
func deriveOptionDefaults(specs []Option, defaults *UserConfig) {
	for i := range specs {
		if specs[i].Unit == "" {
			specs[i].Unit = unitFromPath(specs[i])
		}
		field, ok := resolveOptionField(defaults, specs[i].Path)
		if !ok {
			continue
		}
		if text, set, ok := optionFieldText(field, specs[i]); ok && set {
			specs[i].Default = text
		}
	}
}

// pathUnits are the path endings that name an int option's unit.
var pathUnits = []struct{ suffix, unit string }{
	{"_ms", "ms"}, {"_seconds", "s"}, {"_minutes", "min"}, {"_hours", "h"},
	{"_days", "days"}, {"_mb", "MiB"}, {"_kb", "KiB"}, {"_lines", "lines"},
	{"_cells", "cells"},
}

// unitFromPath is the unit an int option's path or Percent flag names, or "".
func unitFromPath(o Option) string {
	if o.Type != OptionInt {
		return ""
	}
	if o.Percent {
		return "%"
	}
	for _, pu := range pathUnits {
		if strings.HasSuffix(o.Path, pu.suffix) {
			return pu.unit
		}
	}
	return ""
}

// WithUnit is text, an int option's value, followed by its unit: "2000 ms",
// "45%", "1 cell". text is returned as it is when the option has no unit or text is not
// a number, such as "auto".
func (o Option) WithUnit(text string) string {
	if o.Unit == "" {
		return text
	}
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return text
	}
	unit := o.Unit
	switch {
	case unit == "%":
		return text + "%"
	case (n == 1 || n == -1) && len(unit) > 2 && strings.HasSuffix(unit, "s"):
		// A word unit reads in the singular for one: "1 cell", "1 day".
		unit = strings.TrimSuffix(unit, "s")
	}
	return text + " " + unit
}

// Options returns every settable option, sorted by path.
func Options() []Option {
	optionIndex()
	out := slices.Clone(optionSpecs)
	sortx.SortFunc(out, func(a, b Option) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// LookupOption returns the option a dotted path names.
//
//go:noinline
func LookupOption(path string) (Option, bool) {
	opt, ok := optionIndex()[path]
	return opt, ok
}

// OptionFlags returns the options that have a CLI flag, sorted by flag name.
// It reads the registry without filling the derived defaults (see
// optionIndex), because every run of tuios registers the flags, and most runs
// read no option: Default is left out of the result for that reason.
func OptionFlags() []Option {
	var out []Option
	for _, opt := range optionSpecs {
		if opt.Flag != "" {
			opt.Default = ""
			out = append(out, opt)
		}
	}
	sortx.SortFunc(out, func(a, b Option) int { return strings.Compare(a.Flag, b.Flag) })
	return out
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
// A colour option is why this is more than the Accepted membership test. The
// two border colours take any literal and so carry no Accepted set: unchecked,
// set-option appearance.border_focused_color notacolour would be accepted,
// written, and turn up later as a border drawn in nothing. The tint has a keyword set and a literal form at once, which a closed
// set on its own cannot say.
func (o Option) checkValue(value string) error {
	if o.Color {
		// Empty is how a colour option says unset: the border falls back to the
		// theme's, the tint to its own default. Clearing has to stay reachable.
		if value == "" || slices.Contains(o.Accepted, value) || theme.IsColor(value) {
			return nil
		}
		return errfmt.Errorf("%s: %s. Use an empty value to unset it", o.Path, ColourProblem(value, o.Accepted...))
	}
	if o.Theme && !theme.Exists(value) {
		// Exists re-reads the custom themes directory before it says no, so a
		// theme file written a moment ago resolves here rather than on the next
		// restart.
		return errfmt.Errorf("%s: no theme named %q; call list-themes for the ones there are, "+
			"or write %s.json in the themes directory first", o.Path, value, value)
	}
	if o.GlyphSet && !theme.GlyphSetExists(value) {
		// GlyphSetExists re-reads the glyphs directory before it says no, for
		// the reason Exists does: a set written a moment ago has to resolve on
		// this call rather than on the next restart.
		return errfmt.Errorf("%s: no glyph set named %q; call list-glyphs for the ones there are, "+
			"or write %s.json in the glyphs directory first", o.Path, value, value)
	}
	if o.Icon && value != o.Default && !DockModeIconUsable(value) {
		return errfmt.Errorf("%s: %q is not a usable icon; use at most %d cells and no control characters",
			o.Path, value, DockModeIconMaxWidth)
	}
	if o.BoxSize && strings.TrimSpace(value) != "" {
		if _, _, err := ParseBoxSize(value); err != nil {
			return errfmt.Errorf("%s: %w", o.Path, err)
		}
	}
	if len(o.Accepted) > 0 && !slices.Contains(o.Accepted, value) {
		return errfmt.Errorf("%s: %q is not one of %s", o.Path, value, strings.Join(o.Accepted, ", "))
	}
	if o.Check != nil {
		if err := o.Check(value); err != nil {
			return errfmt.Errorf("%s: %w", o.Path, err)
		}
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
		return errfmt.Errorf("unknown config option %q", path)
	}
	if err := opt.checkValue(value); err != nil {
		return err
	}
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return errfmt.Errorf("unknown config option %q", path)
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
			return errfmt.Errorf("%s: %w", path, err)
		}
		if optionKind(field) != reflect.Bool {
			return errfmt.Errorf("%s: registry says bool, config field is %s", path, optionKind(field))
		}
		optionTarget(field).SetBool(parsed)
	case OptionInt:
		if opt.Auto && strings.EqualFold(strings.TrimSpace(value), FPSAuto) {
			if optionKind(field) != reflect.String {
				return errfmt.Errorf("%s: registry says int or auto, config field is %s", path, optionKind(field))
			}
			optionTarget(field).SetString(FPSAuto)
			return nil
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			if opt.Auto {
				return errfmt.Errorf("%s: %q is not a whole number or auto", path, value)
			}
			return errfmt.Errorf("%s: %q is not a whole number", path, value)
		}
		if opt.Max > 0 && (parsed < opt.Min || parsed > opt.Max) {
			return errfmt.Errorf("%s: %d is outside %d..%d", path, parsed, opt.Min, opt.Max)
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
			return errfmt.Errorf("%s: registry says int, config field is %s", path, optionKind(field))
		}
		optionTarget(field).SetInt(int64(parsed))
	case OptionString:
		if optionKind(field) != reflect.String {
			return errfmt.Errorf("%s: registry says string, config field is %s", path, optionKind(field))
		}
		optionTarget(field).SetString(value)
	default:
		return errfmt.Errorf("%s: registry carries no type", path)
	}
	return nil
}

// GetOptionValue reads the current value of a path as a string. A field that
// is unset (a nil pointer, or an empty auto-or-int field) reads back as the
// option's default, since that is what the app will act on. ok is false only
// for a path the registry does not carry.
func GetOptionValue(cfg *UserConfig, path string) (string, bool) {
	opt, ok := LookupOption(path)
	if !ok {
		return "", false
	}
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return "", false
	}
	text, set, ok := optionFieldText(field, opt)
	if !ok {
		return "", false
	}
	if !set {
		return opt.Default, true
	}
	return text, true
}

// optionFieldText renders field as the option's string form. set is false when
// the field holds no value of its own: a nil pointer, or an auto-or-int field
// left empty. ok is false for a field kind the registry cannot carry.
func optionFieldText(field reflect.Value, opt Option) (text string, set, ok bool) {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return "", false, true
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(field.Bool()), true, true
	case reflect.Int:
		return strconv.FormatInt(field.Int(), 10), true, true
	case reflect.String:
		if opt.Auto {
			// An auto-or-int option reads back as the int or "auto", never
			// as the empty string an unset field holds.
			v := strings.TrimSpace(field.String())
			if v == "" {
				return "", false, true
			}
			if strings.EqualFold(v, FPSAuto) {
				return FPSAuto, true, true
			}
			return v, true, true
		}
		return field.String(), true, true
	default:
		return "", false, false
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
	i, ok := tomlFieldIndex(value.Type())[name]
	if !ok {
		return reflect.Value{}, false
	}
	return value.Field(i), true
}

// tomlFieldIndexes caches, per struct type, each toml key's field index.
// Reading the tags of a section on every path segment cost about 3 us a
// lookup, and 1 ms for the whole registry when its defaults are derived.
var tomlFieldIndexes sync.Map // reflect.Type -> map[string]int

// tomlFieldIndex is the toml key to field index map of structType.
func tomlFieldIndex(structType reflect.Type) map[string]int {
	if m, ok := tomlFieldIndexes.Load(structType); ok {
		return m.(map[string]int)
	}
	m := make(map[string]int, structType.NumField())
	for i := range structType.NumField() {
		if name := tomlFieldName(structType.Field(i)); name != "" {
			if _, dup := m[name]; !dup {
				m[name] = i
			}
		}
	}
	tomlFieldIndexes.Store(structType, m)
	return m
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

// optionTarget is the value to write into. A pointer field gets a new value
// of its own: the pointer is what tells an explicitly set value from an unset
// one across a reload, and writing through the old pointer would also change
// every config that shares it, such as a copy of this one (see
// Overrides.Config).
func optionTarget(field reflect.Value) reflect.Value {
	if field.Kind() != reflect.Pointer {
		return field
	}
	fresh := reflect.New(field.Type().Elem())
	field.Set(fresh)
	return fresh.Elem()
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
		return false, errfmt.Errorf("%q is not a true or false value", value)
	}
	return parsed, nil
}

// ZeroRule is what 0 means for an int option. See Option.Zero.
type ZeroRule uint8

const (
	// ZeroIsValue: 0 is a value like any other, such as a gap of 0.
	ZeroIsValue ZeroRule = iota
	// ZeroIsDefault: 0 is what an option the file leaves out holds, so it
	// reads as the default, such as a master ratio of 0.
	ZeroIsDefault
)

// trimLower is the Normalize of an option whose reader trims spaces and
// ignores case.
func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
