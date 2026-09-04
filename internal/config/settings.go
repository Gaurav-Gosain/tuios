package config

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Settings is every appearance and behaviour value a running session reads.
//
// It is a struct rather than a wall of package variables because one tuios
// process is not one user. `tuios ssh` and tuios-web each run a goroutine per
// connection, so a package variable that the settings page writes on every
// keypress is a setting the person on the other connection did not choose. Each
// session holds its own copy; the settings page writes into that copy, and the
// panes beside it on somebody else's screen keep the border, the zen mode and
// the rail their own reader picked.
//
// A value, copied once per connection, so a read costs a field offset and no
// session can ever see another halfway through a write. Nothing here is a
// pointer or a map for the same reason.
//
// The theme is deliberately not in here yet. It is a package of its own with
// style caches keyed off it, and it is still process-wide under a server; see
// the note the settings page carries on that row.
type Settings struct {
	// NotificationDuration is how long an info or success message stays up. It
	// is also the floor for any duration a caller asks for; a caller wanting
	// longer still gets longer.
	NotificationDuration time.Duration

	// NotificationWarningDuration is how long a warning stays up.
	NotificationWarningDuration time.Duration

	// NotificationErrorDuration is how long an error stays up when
	// NotificationErrorSticky is off.
	NotificationErrorDuration time.Duration

	// NotificationErrorSticky makes errors wait for a dismissal instead of
	// expiring. The dock's rule stops burning down when this is what is on
	// screen, which is the affordance that it is waiting for you.
	NotificationErrorSticky bool

	// NormalFPS is the normal refresh rate during regular operation.
	// Set via appearance.max_fps config (default 60, up to MaxFPSCap).
	NormalFPS int

	// UseASCIIOnly controls whether to use ASCII fallback characters instead of Nerd Fonts
	// Set via --ascii-only command-line flag
	UseASCIIOnly bool

	// AnimationsEnabled controls whether UI animations are enabled
	// Set via --no-animations flag or appearance.animations_enabled config
	AnimationsEnabled bool

	// AnimationsSuppressed is set to true temporarily to disable animations
	// (e.g., during remote command processing). This takes precedence over AnimationsEnabled.
	AnimationsSuppressed bool

	// AlwaysConfirmQuit controls whether the quit confirmation dialog is shown
	// every time, regardless of whether there are active foreground processes.
	// Set via confirm_quit config option.
	AlwaysConfirmQuit bool

	// WhichKeyEnabled controls whether the which-key popup is shown after pressing leader key
	// Set via appearance.whichkey_enabled config
	WhichKeyEnabled bool

	// WhichKeyPosition controls where the which-key popup appears
	// Options: bottom-right, bottom-left, top-right, top-left, center
	// Set via appearance.whichkey_position config
	WhichKeyPosition string

	// SharedBorders controls whether adjacent tiled windows share a single border
	// instead of having two separate borders side by side.
	// Set via --shared-borders flag or appearance.shared_borders config
	// Default: false (disabled, opt-in)
	SharedBorders bool

	// BorderStyle controls which border style to use for windows
	// Set via --border-style flag or appearance.border_style config
	BorderStyle string

	// ZenMode controls when window borders are hidden. Valid values are the
	// ZenMode* constants: disabled (always visible), always (always hidden) or
	// mouse (hidden while the pointer is idle). Set via appearance.zen_mode.
	ZenMode string

	// Links controls what tuios treats as a link in pane content. Valid values are
	// the Links* constants: off, marked (OSC 8 only) or all (bare URLs too). Set
	// via appearance.links.
	Links string

	// DockbarPosition controls the position of the dockbar
	// Set via --dockbar-position flag or appearance.dockbar_position config
	DockbarPosition string

	// SidebarEnabled turns the sidebar on. Default off (opt-in).
	SidebarEnabled bool

	// SidebarPosition is which edge the sidebar reserves: "left", "right", or
	// "hidden" (reserves nothing even when enabled).
	SidebarPosition string

	// SidebarWidth is the preferred sidebar width in columns for a wide screen.
	// GetSidebarWidth folds this together with the narrow-screen breakpoints.
	SidebarWidth int

	// SidebarShowGlyphs draws the agent-state glyph on each row.
	SidebarShowGlyphs bool

	// SidebarShowCounts draws the window count on each session row.
	SidebarShowCounts bool

	// SidebarMarquee scrolls a hovered row's title when it overflows its columns.
	SidebarMarquee bool

	// SidebarSections is the rail's layout: which sections it stacks, in what
	// order, and the share of the rail each one may claim. See
	// SidebarDefaultSections for the syntax.
	SidebarSections string

	// SidebarFileIcons draws a nerd font icon per file type in the files
	// section. Off, and on a terminal running in ASCII, the section falls back
	// to the glyph set's folder, parent and file marks.
	SidebarFileIcons bool

	// SidebarFileIconColors draws each of those icons in its own file type's
	// colour, the way yeetui does. It needs the icons under it, so it draws
	// nothing when they are off or the terminal is running in ASCII.
	SidebarFileIconColors bool

	// SidebarFolderClick is what a click on a folder row does: walk the listing
	// into it, tell the pane to cd there, or both.
	SidebarFolderClick string

	// SidebarFileActions lets the files section create, rename, delete, copy,
	// cut and paste. On leaves the listing exactly as it was until a key is
	// pressed or a menu row is picked; off makes those keys do nothing at all.
	//
	// It is a setting because the rail is beside a terminal rather than in front
	// of one. A file manager is a place somebody went; a rail is a place they
	// are, and not everybody wants the folder they are looking at to be one they
	// can delete from by mistake.
	SidebarFileActions bool

	// SidebarFileDelete is where a deleted file goes: the trash, or nowhere.
	SidebarFileDelete string

	// Tooltips pops a one-row label naming whatever icon-only control the
	// pointer is over: a row of the collapsed rail, or one of the dock's session
	// controls. A glyph is enough to steer by and not enough to read.
	Tooltips bool

	// SessionColors gives every session a colour of its own and marks it on the
	// surfaces that show more than one session at once: the rail's sessions and
	// agents sections, and the session switcher. Off leaves each of those exactly
	// as it was before the colours existed.
	SessionColors bool

	// DockWorkspaceTabs draws the dock's clickable workspace strip. Off leaves the
	// dock exactly as it was before the strip existed.
	DockWorkspaceTabs bool

	// DockWorkspaceTabFormat is the format string for each workspace tab in the
	// dock strip. Placeholders: {index} (the workspace number) and {name} (the
	// workspace name, or its number when it has no name). Empty means "{name}",
	// the historic rendering.
	DockWorkspaceTabFormat string

	// DockWorkspaceTooltip pops the whole name of a workspace whose pill had to cut
	// it short. Off, a long name stays truncated with no way to read the rest.
	DockWorkspaceTooltip bool

	// DockPillCaps puts powerline half-circle caps back on the dock's mode pill,
	// workspace tabs and minimized-window pills. Off, each is a flat filled cell:
	// the caps repeated on every one of them, so a status line read as a row of
	// beads. The capped look is one key away for anyone who wants it.
	DockPillCaps bool

	// HideWindowButtons controls whether to hide window control buttons
	// Set via --hide-window-buttons flag or appearance.hide_window_buttons config
	HideWindowButtons bool

	// WindowButtonStyle selects how the window controls are drawn. See
	// appearance.window_button_style.
	WindowButtonStyle string

	// WindowButtonPosition selects which end of the title bar the window controls
	// sit on. See appearance.window_button_position.
	WindowButtonPosition string

	// ScrollbarStyle selects how a scrolled-back pane draws its position. See
	// appearance.scrollbar.style.
	ScrollbarStyle string

	ScrollbarThumb string

	ScrollbarTrack string

	ScrollbarTint string

	// HideScrollbar controls whether the window scrollbar is hidden.
	// Automatically treated as true when BorderStyle == "hidden" since there is
	// no border to draw the thumb on in that mode.
	// Set via --hide-scrollbar flag or appearance.hide_scrollbar config
	HideScrollbar bool

	// WindowTitlePosition controls where window titles are displayed
	// Options: bottom, top, hidden
	// Set via --window-title-position flag or appearance.window_title_position config
	WindowTitlePosition string

	// WindowTitleFormat is the template used to build a window's displayed title.
	// Empty (the default) means the title is shown as-is. See FormatWindowTitle for
	// the supported placeholders.
	// Set via appearance.window_title_format config
	WindowTitleFormat string

	// HideClock controls whether the clock overlay is hidden
	// Set via --hide-clock flag or appearance.hide_clock config
	// Deprecated: Use ShowClock instead. HideClock takes precedence when true.
	HideClock bool

	// ShowClock controls whether the clock overlay is shown (default: hidden).
	// Set via --show-clock flag or appearance.show_clock config
	ShowClock bool

	// ShowCPU controls whether the CPU graph is shown in the dock (default: hidden).
	// Set via --show-cpu flag or appearance.show_cpu config
	ShowCPU bool

	// ShowRAM controls whether RAM usage is shown in the dock (default: hidden).
	// Set via --show-ram flag or appearance.show_ram config
	ShowRAM bool

	// ScrollbackLines controls the number of lines to keep in scrollback buffer
	// Set via --scrollback-lines flag or appearance.scrollback_lines config
	ScrollbackLines int

	// ScrollLines is how many lines one mouse wheel notch scrolls in scrollback,
	// copy mode and the scrollback browser.
	// Set via appearance.scroll_lines config
	ScrollLines int

	// CopyOnSelect puts the text on the clipboard as soon as a mouse selection is
	// released, the way X11's primary selection and kitty's copy_on_select do.
	// Turn it off to keep the clipboard until an explicit yank.
	// Set via appearance.copy_on_select config.
	CopyOnSelect bool

	// FocusFollowsMouse focuses the pane under the cursor as the mouse moves over
	// it, without a click and without entering terminal mode. It is a divisive
	// window-manager habit, so it defaults off and users opt in.
	// Set via appearance.focus_follows_mouse config.
	FocusFollowsMouse bool

	// AltDrag makes alt + left-drag move a pane, the gesture nearly every desktop
	// window manager binds. It is on by default because the hands that know it
	// already outnumber the ones that do not. Turning it off hands alt-drag back to
	// the pane: selection while typing, and whatever a mouse-tracking app makes of
	// it. Alt + right-drag resizes either way, since that is the ordinary
	// right-press resize with alt only keeping the menu out of the way.
	// Set via appearance.alt_drag config.
	AltDrag bool

	// AutoEnterTerminalOnFocus enters terminal mode when a window-management
	// keyboard command actually moves focus to another pane. Hover-focus and
	// click-to-type keep their own policies; this is only those explicit focus
	// commands. A no-op that leaves the already-focused pane focused does not
	// change mode.
	//
	// off (the default): Tab keeps cycling in window-management mode.
	// targeted: numbered select and directional arrows enter terminal mode; Tab
	// does not.
	// all: every covered focus command that actually moves focus also enters
	// terminal mode, including next/prev window.
	// Set via appearance.auto_enter_terminal_on_focus config.
	AutoEnterTerminalOnFocus AutoEnterTerminalPolicy

	// ClickToType decides what a left click on a pane's content does while the
	// keyboard is driving the window manager. "single" enters terminal mode on the
	// release, which is what a newcomer expects a click to do and so the default.
	// "double" focuses on one click and enters on two, for a user who arranges
	// panes with the mouse and does not want a stray click to take the window
	// manager's keys away. "off" never changes mode from a click: the way in stays
	// the enter_terminal_mode binding.
	//
	// The mode decides who owns the mouse, here as everywhere else: a pane whose
	// app asked for mouse tracking is only forwarded to in terminal mode, so under
	// "off" the mouse alone cannot reach that app. "double" is the setting for
	// someone who lives in mouse-mode apps and still wants the mouse to be a
	// pointer first.
	// Set via appearance.click_to_type config.
	ClickToType string

	// WordCharacters lists the punctuation that counts as part of a word when a
	// double-click selects one, on top of letters and digits, which always do.
	//
	// The default is kitty's select_by_word_characters, and it is chosen for what
	// terminal content actually looks like: it takes a path, a URL, a version
	// number, or a flag such as --no-vm as a single word instead of stopping at
	// every punctuation mark. A colon is deliberately absent, so host:port and
	// file:line select as their parts.
	// Set via appearance.word_characters config.
	WordCharacters string

	// NiriReverseScroll reverses mouse scroll direction in niri scrolling mode.
	// When true, scroll-up moves viewport right and scroll-down moves left.
	// Set via appearance.niri_reverse_scroll config
	NiriReverseScroll bool

	// LeaderKey is the prefix key for commands (default: ctrl+b)
	// Set via appearance.leader_key config
	LeaderKey string

	// PaneGap is the cells of empty space the tiler keeps between two neighbouring
	// panes: i3's inner gap, and about the only spacing a terminal window manager
	// can honestly offer.
	//
	// Inner only. An outer gap would have to inset the content region, which the
	// sidebar's width, the dock's height, every overlay's placement and every mouse
	// hit test are measured against, so a margin the sidebar already draws one of
	// would cost a move of the whole frame.
	PaneGap int

	// MasterRatioPercent is how much of the screen the master pane takes in the
	// master-stack layout, as a percent. It is the value a new session starts at;
	// once a session is running the ratio is the session's, moved by the resize
	// keys and settled across every attached client, because it decides how many
	// columns a pane gets and a PTY has exactly one size.
	MasterRatioPercent int

	// ScrollColumnWidth is how wide a column is in the scrolling layout, as a
	// percent of the screen, before anything resizes it. Session state for the same
	// reason the master ratio is.
	//
	// The default is deliberately over half. The strip is meant to be wider than
	// the viewport - that is what makes it a strip rather than a grid - so a
	// default that let two columns sit side by side exactly would show the layout
	// as a two-pane split and never as something you scroll.
	ScrollColumnWidth int

	// DimUnfocused is how far an unfocused pane's content is carried toward the
	// pane's own ground, as a percentage. Zero, the default, draws every pane's
	// content the same.
	//
	// One number rather than wezterm's hue, saturation and brightness triple. A
	// blend toward the ground already moves saturation and brightness together,
	// because a pane's ground is both darker and flatter than the text on it, and
	// rotating the hue of somebody else's program output is a novelty rather than a
	// thing a rice wants. One number is also the only form that says the same thing
	// on a light theme as on a dark one.
	DimUnfocused int

	// ClockFormat is the Go time layout the clock overlay draws with. Empty takes
	// DefaultClockFormat.
	//
	// A layout rather than a set of toggles, for the reason window_title_format is
	// one: "seconds on or off" is two of the questions people actually have about a
	// clock, and the standard library already has a spelling for all of them.
	ClockFormat string

	// ZoomMaxWidth is the maximum width in cells for zoom/zen mode.
	// 0 means fullscreen (no max width cap). When set (e.g., 120), the zoomed
	// window is centered horizontally and capped at this width.
	ZoomMaxWidth int

	// GlyphSet names the chrome glyph set, or "default" for the shipped one. It is
	// the shape half of a rice, beside Theme's colour half.
	GlyphSet string
}

// DefaultSettings is tuios as it ships, before any config file, any flag and
// any settings page. It is the seed for Global and the value every unconfigured
// session starts from.
func DefaultSettings() Settings {
	return Settings{
		NotificationDuration:        6 * time.Second,
		NotificationWarningDuration: 8 * time.Second,
		NotificationErrorDuration:   15 * time.Second,
		NotificationErrorSticky:     true,
		NormalFPS:                   60,
		UseASCIIOnly:                false,
		AnimationsEnabled:           true,
		AnimationsSuppressed:        false,
		AlwaysConfirmQuit:           false,
		WhichKeyEnabled:             true,
		WhichKeyPosition:            "bottom-right",
		SharedBorders:               false,
		BorderStyle:                 "rounded",
		ZenMode:                     ZenModeDisabled,
		Links:                       LinksAll,
		DockbarPosition:             "bottom",
		SidebarEnabled:              false,
		SidebarPosition:             "left",
		SidebarWidth:                SidebarDefaultWidth,
		SidebarShowGlyphs:           true,
		SidebarShowCounts:           true,
		SidebarMarquee:              true,
		SidebarSections:             SidebarDefaultSections,
		SidebarFileIcons:            true,
		SidebarFileIconColors:       true,
		SidebarFolderClick:          SidebarFolderClickNavigate,
		SidebarFileActions:          true,
		SidebarFileDelete:           SidebarFileDeleteTrash,
		Tooltips:                    true,
		SessionColors:               true,
		DockWorkspaceTabs:           true,
		DockWorkspaceTabFormat:      "",
		DockWorkspaceTooltip:        true,
		DockPillCaps:                false,
		HideWindowButtons:           false,
		WindowButtonStyle:           WindowButtonStylePill,
		WindowButtonPosition:        WindowButtonPositionRight,
		ScrollbarStyle:              ScrollbarStyleThin,
		ScrollbarThumb:              "",
		ScrollbarTrack:              "",
		ScrollbarTint:               ScrollbarTintQuiet,
		HideScrollbar:               false,
		WindowTitlePosition:         "bottom",
		WindowTitleFormat:           "",
		HideClock:                   false,
		ShowClock:                   false,
		ShowCPU:                     false,
		ShowRAM:                     false,
		ScrollbackLines:             DefaultScrollbackLines,
		ScrollLines:                 3,
		CopyOnSelect:                true,
		FocusFollowsMouse:           false,
		AltDrag:                     true,
		ClickToType:                 ClickToTypeSingle,
		AutoEnterTerminalOnFocus:    AutoEnterTerminalOff,
		WordCharacters:              `@-./_~?&=%+#`,
		NiriReverseScroll:           false,
		LeaderKey:                   DefaultLeaderKey,
		PaneGap:                     0,
		MasterRatioPercent:          MasterRatioDefault,
		ScrollColumnWidth:           ScrollColumnWidthDefault,
		DimUnfocused:                0,
		ClockFormat:                 "",
		ZoomMaxWidth:                0,
		GlyphSet:                    theme.GlyphSetNone,
	}
}

// DefaultScrollbackLines is how many lines a pane keeps behind it as it ships.
// Named because a caller with no session in reach - a window built in a test,
// or a harness - still has to say how deep the scrollback is, and the number is
// better said once here than repeated at every one of them.
const DefaultScrollbackLines = 10000

// DefaultLeaderKey is the prefix key tuios ships with. It is a constant rather
// than a read of Settings.LeaderKey because the places that fall back to it are
// asking "what does tuios bind when nobody said otherwise", which is one answer
// for the whole program and not one per session.
const DefaultLeaderKey = "ctrl+b"

// Global is the process seed: the config file and the CLI flags are applied to
// it once at startup, single-threaded, and every session copies it at
// construction. Nothing that serves a client writes to it after that, which is
// what stops one client's settings page reaching another client's frame.
//
// Read it directly only where there is no session in reach: an entrypoint doing
// startup, or a harness with no OS.
var Global = DefaultSettings()
