package config

import (
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The values of appearance.confirm_close: when closing a pane by hand, with a
// key or the pane's menu, asks first. A script never gets the question.
const (
	// ConfirmCloseNever closes the pane at once, as tuios always did.
	ConfirmCloseNever = "never"
	// ConfirmCloseBusy asks when the pane runs something besides its shell,
	// the rule the quit dialog uses.
	ConfirmCloseBusy = "busy"
	// ConfirmCloseAlways asks for every pane.
	ConfirmCloseAlways = "always"
)

// ConfirmCloseLevels is what appearance.confirm_close accepts.
var ConfirmCloseLevels = []string{ConfirmCloseNever, ConfirmCloseBusy, ConfirmCloseAlways}

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

	// Bell is notifications.bell: what a pane's BEL does. Only the Update
	// goroutine reads it.
	Bell string

	// NotificationStyle is notifications.style: NotificationStyleFull draws a
	// dock message with its severity cap and the countdown on the rule above
	// it, NotificationStylePlain draws the mark and the text only. A mode
	// that wants a quieter screen sets it to NotificationStylePlain.
	NotificationStyle string

	// NotificationLevel is notifications.level: the least severe message the
	// dock shows. A message below it goes to the log viewer only.
	NotificationLevel string

	// NotificationLogHidden is notifications.log_hidden: whether an info or
	// success message below NotificationLevel still goes to the log viewer.
	NotificationLogHidden bool
	// NotificationMachineOffer is notifications.machine_offer: whether a
	// pane's ssh login to a machine with no [hosts] entry brings up the
	// offer to add it.
	NotificationMachineOffer bool

	// NormalFPS is the normal refresh rate during regular operation.
	// Set via appearance.max_fps config (default 60, up to MaxFPSCap).
	NormalFPS int

	// MaxFPSAuto is set when appearance.max_fps is "auto": NormalFPS then
	// follows DisplayFPS.
	MaxFPSAuto bool

	// CellFrames is appearance.cell_frames: which clients write their frames
	// as diffed cells rather than through Bubble Tea's renderer. See
	// CellFramesFor.
	CellFrames string

	// DisplayFPS is the refresh rate the client found for this machine's
	// displays, or 0 before it has looked or when it cannot tell. It is not
	// read from the config, so a reload keeps it.
	DisplayFPS int

	// UseASCIIOnly controls whether to use ASCII fallback characters instead
	// of Nerd Fonts. It is the effective answer: set by --ascii-only
	// (ASCIIRequested), or by a terminal whose locale is not UTF-8 when no
	// glyph set was chosen (see GlyphEnv).
	UseASCIIOnly bool

	// ASCIIRequested records --ascii-only, so re-applying the config at
	// runtime can recompute UseASCIIOnly without losing the flag.
	ASCIIRequested bool

	// GlyphEnv is what the terminal tuios draws on can show, read from its
	// locale and TERM when the client starts. See DetectGlyphEnv.
	GlyphEnv GlyphEnv

	// NoNerdFont is set when GlyphEnv is GlyphEnvUnicode and no glyph set was
	// chosen: the icons that are not glyph set roles (the dock's, the
	// notification marks) take their ASCII forms. Read it through
	// NerdFontsOff.
	NoNerdFont bool

	// Motion is how much the UI animates: MotionNone, MotionBasic or
	// MotionFull. Set via appearance.motion, or --no-animations for none.
	// Read it through MotionAllows, which also honours AnimationsSuppressed.
	Motion string

	// Calm is appearance.calm: nothing moves or blinks, and only messages
	// that need the person reach the dock. It sits over Motion rather than
	// replacing it, so turning calm off gives back the level the person had.
	// See internal/app/calm.go.
	Calm bool

	// NoAnimationsFlag records --no-animations, so re-applying the config at
	// runtime keeps the level at none until something sets the level itself
	// (OS.SetMotion).
	NoAnimationsFlag bool

	// AnimationsSuppressed is set to true temporarily to disable animations
	// (e.g., during remote command processing). This takes precedence over
	// Motion.
	AnimationsSuppressed bool

	// ModalDim is the percent the screen behind a modal overlay is darkened
	// by. Zero turns it off. Set via appearance.modal_dim.
	ModalDim int
	// PopupDim is the percent the screen behind a focused plain popup is darkened
	// by. Zero turns it off. Set via appearance.popup_dim.
	PopupDim int

	// ConfirmClose is when closing a pane by hand asks first: ConfirmCloseNever,
	// ConfirmCloseBusy or ConfirmCloseAlways. Set via appearance.confirm_close.
	ConfirmClose string

	// AlwaysConfirmQuit controls whether the quit confirmation dialog is shown
	// every time, regardless of whether there are active foreground processes.
	// Set via confirm_quit config option.
	AlwaysConfirmQuit bool `opt:"appearance.confirm_quit,keep"`

	// WhichKeyEnabled controls whether the which-key popup is shown after pressing leader key
	// Set via appearance.whichkey_enabled config
	WhichKeyEnabled bool `opt:"appearance.whichkey_enabled,keep"`

	// WhichKeyPosition controls where the which-key popup appears
	// Options: bottom-right, bottom-left, top-right, top-left, center
	// Set via appearance.whichkey_position config
	WhichKeyPosition string

	// WrapLists makes a single step off either end of a list land on the other
	// end: up on the first row goes to the last. Set via appearance.wrap_lists.
	// See internal/listnav.
	WrapLists bool `opt:"appearance.wrap_lists,keep"`

	// SharedBorders controls whether adjacent tiled windows share a single border
	// instead of having two separate borders side by side.
	// Set via --shared-borders flag or appearance.shared_borders config
	// Default: false (disabled, opt-in)
	SharedBorders bool `opt:"appearance.shared_borders,keep"`

	// BorderStyle controls which border style to use for windows
	// Set via --border-style flag or appearance.border_style config
	BorderStyle string `opt:"appearance.border_style,keep"`

	// FramePanels frames every overlay panel in OverlayBorder, not only the
	// panels at 16 colours that need a frame. Set via appearance.frame_panels.
	FramePanels bool `opt:"appearance.frame_panels"`

	// PaneOverlays draws dialogs and panels like pane windows: framed in the
	// focused pane's border colour, on the ground an empty pane cell shows.
	// Set via appearance.pane_overlays.
	PaneOverlays bool `opt:"appearance.pane_overlays"`

	// OverlayBorderColor is the colour every overlay frame is drawn in. Empty
	// keeps the overlay's own, or the focused pane's border under
	// PaneOverlays. Set via appearance.overlay_border_color.
	OverlayBorderColor string `opt:"appearance.overlay_border_color"`

	// TilingScheme is the BSP insertion scheme a workspace starts with the
	// first time it is tiled: one of the TilingScheme* constants (spiral,
	// longest_side, alternate, smart_split). Set via appearance.tiling_scheme.
	// GetOrCreateBSPTree reads this only when a workspace has no tree yet; a
	// workspace that is already tiled keeps its own scheme regardless of this
	// value.
	TilingScheme string `opt:"appearance.tiling_scheme,keep"`

	// ZenMode controls when window borders are hidden. Valid values are the
	// ZenMode* constants: disabled (always visible), always (always hidden) or
	// mouse (hidden while the pointer is idle). Set via appearance.zen_mode.
	ZenMode string `opt:"appearance.zen_mode,keep"`

	// Links controls what tuios treats as a link in pane content. Valid values are
	// the Links* constants: off, marked (OSC 8 only) or all (bare URLs too). Set
	// via appearance.links.
	Links string `opt:"appearance.links,keep"`

	// LinkClick is the click that opens a link: one of the LinkClick*
	// constants. Set via appearance.link_click.
	LinkClick string `opt:"appearance.link_click,keep"`

	// LinkHover is the hover highlight policy: one of the LinkHover*
	// constants. Set via appearance.link_hover.
	LinkHover string `opt:"appearance.link_hover,keep"`

	// LinkRules are the compiled [[links.rules]]: plain text that is turned
	// into a link with a target built from the match. Nil when there are
	// none, which is what keeps the frame from scanning lines for them.
	LinkRules *hints.LinkRules

	// DropPreview is what a BSP drag draws where the pane would land: one of
	// the DropPreview* constants. Set via appearance.drop_preview.
	DropPreview string

	// DropPreviewDelay is how long the pointer rests on one drop zone before
	// the preview shows. Zero shows it at once. Set via
	// appearance.drop_preview_delay.
	DropPreviewDelay time.Duration

	// LinkOpener is the command that opens a web link. Empty uses $BROWSER,
	// then the system opener. Set via appearance.link_opener.
	LinkOpener string

	// DockbarPosition controls the position of the dockbar
	// Set via --dockbar-position flag or appearance.dockbar_position config
	DockbarPosition string `opt:"appearance.dockbar_position,keep"`

	// SidebarEnabled turns the sidebar on. Default on since v0.8.0. It has
	// no opt tag: ApplyAppearanceConfig folds an explicit left or right edge
	// into it first, so it is not the file's appearance.sidebar.enabled.
	SidebarEnabled bool

	// SidebarPosition is which edge the sidebar reserves: "left", "right", or
	// "hidden" (reserves nothing even when enabled). It has no opt tag for the
	// same reason: under position = "hidden", an enabled explicit edge
	// becomes the rail's position.
	SidebarPosition string

	// SidebarWidth is the preferred sidebar width in columns for a wide screen.
	// GetSidebarWidth folds this together with the narrow-screen breakpoints.
	SidebarWidth int

	// SidebarShowGlyphs draws the agent-state glyph on each row.
	SidebarShowGlyphs bool `opt:"appearance.sidebar.show_glyphs,keep"`

	// SidebarShowCounts draws the window count on each session row.
	SidebarShowCounts bool `opt:"appearance.sidebar.show_counts,keep"`

	// SidebarShowNumbers draws the switch number ahead of each session name.
	// Off by default: the quiet rail is the default, and the people who want
	// the numbers set show_numbers in [appearance.sidebar].
	SidebarShowNumbers bool `opt:"appearance.sidebar.show_numbers,keep"`

	// SidebarMarquee scrolls a hovered row's title when it overflows its columns.
	SidebarMarquee bool `opt:"appearance.sidebar.marquee,keep"`

	// SidebarPerSession keeps the rail's shown or hidden state per session,
	// as before issue 589. Off (the default), showing or hiding the rail
	// applies to every session on the daemon. Set via
	// appearance.sidebar.per_session.
	SidebarPerSession bool

	// SidebarPeekEnabled lets hovering a session row, or moving the rail
	// cursor onto it, show that session's panes in the terminals section.
	// Off, the section shows the attached session's panes until a click
	// attaches another one (issue #1227).
	SidebarPeekEnabled bool

	// SidebarSections is the rail's layout: which sections it stacks, in what
	// order, and the share of the rail each one may claim. See
	// SidebarDefaultSections for the syntax.
	SidebarSections string

	// SidebarFileIcons draws a nerd font icon per file type in the files
	// section. Off, and on a terminal running in ASCII, the section falls back
	// to the glyph set's folder, parent and file marks.
	SidebarFileIcons bool `opt:"appearance.sidebar.file_icons,keep"`

	// SidebarFileIconColors draws each of those icons in its own file type's
	// colour, the way yeetui does. It needs the icons under it, so it draws
	// nothing when they are off or the terminal is running in ASCII.
	SidebarFileIconColors bool `opt:"appearance.sidebar.file_icon_colors,keep"`

	// SidebarFolderClick is what a click on a folder row does: walk the listing
	// into it, tell the pane to cd there, or both.
	SidebarFolderClick string `opt:"appearance.sidebar.folder_click,keep"`
	// SidebarFileDrag lets a row of the files section be dragged into a pane
	// to paste its path.
	SidebarFileDrag bool `opt:"appearance.sidebar.file_drag,keep"`
	// SidebarFilesStyle is how the files section draws: "list", one folder at
	// a time, or "tree", folders opened in place under one root.
	SidebarFilesStyle string
	// RailHeaderCase is how the rail's section headers read: "lowercase"
	// keeps the quiet furniture look they have always had, "uppercase" draws
	// the heading treatment (bold, secondary ink, the rule glyph and the
	// uppercase label). Set via appearance.sidebar.header_case.
	RailHeaderCase string `opt:"appearance.sidebar.header_case,keep"`
	// RailRowStyle is how the rail marks the row you are on. Set via
	// appearance.sidebar.row_style; read it through GetRailRowStyle.
	RailRowStyle string
	// RailHeaderStyle is how a rail section header is drawn. Set via
	// appearance.sidebar.header_style; read it through GetRailHeaderStyle.
	RailHeaderStyle string
	// RailRoomy puts a blank row in front of each rail section header after
	// the first. Set via appearance.sidebar.density = "roomy".
	RailRoomy bool
	// SidebarEditor is the terminal editor command; empty uses the environment.
	SidebarEditor string `opt:"appearance.sidebar.editor"`

	// SidebarFileActions lets the files section create, rename, delete, copy,
	// cut and paste. On leaves the listing exactly as it was until a key is
	// pressed or a menu row is picked; off makes those keys do nothing at all.
	//
	// It is a setting because the rail is beside a terminal rather than in front
	// of one. A file manager is a place somebody went; a rail is a place they
	// are, and not everybody wants the folder they are looking at to be one they
	// can delete from by mistake.
	SidebarFileActions bool `opt:"appearance.sidebar.file_actions,keep"`

	// SidebarAgentRestFold is how long an agent row rests (idle, unknown, or
	// done and seen) before the rail folds it into one line with the others.
	// Zero never folds. From appearance.sidebar.agent_rest_fold.
	SidebarAgentRestFold time.Duration

	// SidebarGroupMain gives each repository a row of its own, with the main
	// checkout under it. Off, the main checkout's row is the repository's row.
	// From appearance.sidebar.group_main.
	SidebarGroupMain bool `opt:"appearance.sidebar.group_main,keep"`

	// SidebarSubagents is how the rail draws the subagents under an agent
	// row: rows or the count, tree lines, the heading line that folds them,
	// the fields, the row cap and when a finished row goes. From the
	// appearance.sidebar.subagent_* keys and the rail's edge table. The rail
	// on the other edge resolves its own; see renderSecondarySidebar.
	SidebarSubagents SidebarSubagentView

	// SidebarFileDelete is where a deleted file goes: the trash, or nowhere.
	SidebarFileDelete string `opt:"appearance.sidebar.file_delete,keep"`

	// SidebarAgentRow is what an agent row draws and how each token is inked,
	// from [appearance.sidebar.agent_row]. See sidebar_agent_row.go.
	SidebarAgentRow SidebarAgentRowSpec

	// Tooltips pops a one-row label naming whatever icon-only control the
	// pointer is over: a row of the collapsed rail, or one of the dock's session
	// controls. A glyph is enough to steer by and not enough to read.
	Tooltips bool `opt:"appearance.sidebar.tooltips,keep"`

	// SessionColors gives every session a colour of its own and marks it on the
	// surfaces that show more than one session at once: the rail's sessions and
	// agents sections. Off leaves each of those
	// without a session colour.
	// SessionBorder carries the session's colour on every pane border, not only
	// on the rail. It is off by default because it changes the look of every
	// window, and it is separate from SessionColors so the rail's marks and the
	// borders can be turned on independently.
	//
	// It is for telling one machine from another at a glance. Once panes can be
	// attached on several machines, the session is the thing every pane in the
	// view has in common, and its colour is the cheapest way to say which one
	// you are looking at without reading the rail.
	// SidebarGitDirty adds the count of staged, changed and untracked paths to
	// the rail's git section.
	//
	// Separate from the section itself because it is the only part of it that
	// costs a walk of the working tree. A branch and a divergence are recorded
	// facts and come from a few file reads; dirtiness is recorded nowhere, so
	// the only way to know is to compare the tree against the index. On a large
	// repository that is the part worth being able to turn off.
	// NiriClickReveals brings a clicked column fully on screen in the scrolling
	// layout.
	//
	// A click is different from the other ways focus moves. A workspace switch
	// or a focus the daemon moved is not a statement about the viewport, and
	// revealing on those would throw away wherever the user had scrolled the
	// strip, so they follow the least-scroll rule.
	// Clicking a column that is half off the edge is a statement: you picked
	// that pane to work in, so the strip brings all of it to you.
	NiriClickReveals bool `opt:"appearance.niri_click_reveals,keep"`

	// NiriHoverReveals brings the column under the pointer fully on screen in
	// the scrolling layout, while focus-follows-mouse is on.
	//
	// Hovering a column with that setting on is the same statement clicking one
	// is: it is how you pick the pane to work in, and there is no other gesture
	// to make. Without it the focus moved to a column that stayed half off the
	// edge, so the pane you had just focused was the one you could not see.
	//
	// It does nothing unless appearance.focus_follows_mouse is on, since
	// nothing focuses on hover otherwise.
	NiriHoverReveals bool `opt:"appearance.niri_hover_reveals,keep"`

	SidebarGitDirty bool `opt:"appearance.git_dirty,keep"`

	SessionBorder bool `opt:"appearance.session_border,keep"`

	// GlobalSession offers a session that holds panes from more than one
	// machine, listed in the rail once a second machine is reachable.
	//
	// It is a session of its own rather than a thing any session can become.
	// An ordinary session is the machine it is on, and a new pane in it is a
	// pane there, with nothing to ask about. The global session is the one
	// place the question is worth putting, so it is the one place it is asked.
	GlobalSession bool `opt:"appearance.global_session,keep"`

	SessionColors bool `opt:"appearance.session_colors,keep"`

	// DockWorkspaceTabs draws the dock's clickable workspace strip. Off leaves the
	// dock exactly as it was before the strip existed.
	DockWorkspaceTabs bool `opt:"appearance.dock_workspace_tabs,keep"`

	// DockWorkspaceTabFormat is the format string for each workspace tab in the
	// dock strip. Placeholders: {index} (the workspace number) and {name} (the
	// workspace name, or its number when it has no name). Empty means "{name}",
	// the historic rendering.
	DockWorkspaceTabFormat string `opt:"appearance.dock_workspace_tab_format"`

	// DockWorkspaceTooltip pops the whole name of a workspace whose pill does not
	// show it: the pill had to cut it short, or the tab format has no {name}.
	// Off, the name stays cut or hidden with no way to read it from the dock.
	DockWorkspaceTooltip bool `opt:"appearance.dock_workspace_tooltip,keep"`

	// DockWorkspaceLabelMax caps a workspace pill's label in cells. 0 draws the
	// whole name and lets the strip's scroll arithmetic handle the width.
	DockWorkspaceLabelMax int

	// DockPillCaps puts powerline half-circle caps on the dock's mode chip,
	// workspace tabs and minimized-window pills. On by default. Off, each is a
	// flat filled cell, for anyone who reads a row of caps as a row of beads.
	DockPillCaps bool `opt:"appearance.dock_pill_caps,keep"`

	// DockModeSplitLetter shows the next split's direction, V or H, after the
	// tiling icon in the mode pill. On by default. Off, the pill is the icon
	// alone. With the default icons that is as wide as the leader's pill, so
	// pressing the leader no longer moves the workspace tabs.
	DockModeSplitLetter bool `opt:"appearance.dock_mode_split_letter,keep"`

	// DockPrefixIndicator is where an armed leader shows: "pill" colours the
	// dock's mode pill, "badge" draws the floating PREFIX badge. The badge is
	// drawn either way when the dock is hidden or has no mode pill.
	DockPrefixIndicator string

	// DockModePill is what the mode pill shows while tiling is on: "tiling"
	// draws the tiling icon in both input modes, "input" draws the terminal
	// or window icon, so the two modes differ by more than colour. The split
	// letter follows either icon.
	DockModePill string

	// BorderFocusedGradient and BorderGradientAngle are
	// appearance.border_focused_gradient and border_gradient_angle. Read the
	// stops through BorderGradientStops.
	BorderFocusedGradient string `opt:"appearance.border_focused_gradient"`
	BorderGradientAngle   int

	// DockModeIconWindow, DockModeIconTerminal and DockModeIconTiling are the
	// mode pill's icons from appearance.dock_mode_icon_*. Nil is unset, which
	// draws the built-in for the glyph set. An empty string draws no icon.
	// Read them through GetDockModeIconWindow and its siblings.
	DockModeIconWindow   *string
	DockModeIconTerminal *string
	DockModeIconTiling   *string

	// DockCompact draws the dock as one row: the pills without the rule above
	// or below them. The panes get the row back. Off by default.
	DockCompact bool `opt:"appearance.dock_compact"`

	// HideWindowButtons controls whether to hide window control buttons
	// Set via --hide-window-buttons flag or appearance.hide_window_buttons config
	HideWindowButtons bool `opt:"appearance.hide_window_buttons"`

	// WindowButtonStyle selects how the window controls are drawn. See
	// appearance.window_button_style.
	WindowButtonStyle string `opt:"appearance.window_button_style,keep"`

	// WindowButtonPosition selects which end of the title bar the window controls
	// sit on. See appearance.window_button_position.
	WindowButtonPosition string `opt:"appearance.window_button_position,keep"`

	// ScrollbarStyle selects how a scrolled-back pane draws its position. See
	// appearance.scrollbar.style.
	ScrollbarStyle string `opt:"appearance.scrollbar.style,keep"`

	ScrollbarThumb string `opt:"appearance.scrollbar.thumb"`

	ScrollbarTrack string `opt:"appearance.scrollbar.track"`

	ScrollbarTint string `opt:"appearance.scrollbar.tint"`

	// The colours a pane paints over its own output to mark text. See
	// SelectionConfig. An empty foreground leaves the text the colour the
	// program wrote it in and tints only the background.
	SelectionBg   string `opt:"appearance.selection.bg,keep"`
	SelectionFg   string `opt:"appearance.selection.fg"`
	SelectionBold bool   `opt:"appearance.selection.bold,keep"`
	SearchBg      string `opt:"appearance.selection.search_bg,keep"`
	SearchFg      string `opt:"appearance.selection.search_fg"`
	MatchBg       string `opt:"appearance.selection.match_bg,keep"`
	MatchFg       string `opt:"appearance.selection.match_fg"`
	CopyCursorBg  string `opt:"appearance.selection.cursor_bg,keep"`
	CopyCursorFg  string `opt:"appearance.selection.cursor_fg"`

	// CopyFlash sweeps a band of light over text that was just copied.
	// CopyFlashMs is how long one sweep takes and CopyFlashColor is the light.
	CopyFlash      bool `opt:"appearance.selection.flash,keep"`
	CopyFlashMs    int
	CopyFlashColor string `opt:"appearance.selection.flash_color,keep"`
	// CopyFlashStyle is the shape the sweep takes. One of CopyFlashStyles.
	CopyFlashStyle string `opt:"appearance.selection.flash_style,keep"`
	// MultiCopyFormat is the format multi copy mode starts in: one of
	// MultiCopyFormats.
	MultiCopyFormat string
	// CopyEntry is where copy mode puts its cursor on entry: one of
	// CopyEntries.
	CopyEntry string
	// OSC52Write says what happens when a program in a pane sets the
	// clipboard with OSC 52. One of OSC52WriteModes.
	OSC52Write string
	// CopyCommand is the command a copy-mode yank pipes the selection
	// through: appearance.selection.copy_command. Empty copies it as it is.
	CopyCommand string

	// HideScrollbar controls whether the window scrollbar is hidden.
	// Automatically treated as true when BorderStyle == "hidden" since there is
	// no border to draw the thumb on in that mode.
	// Set via --hide-scrollbar flag or appearance.hide_scrollbar config
	HideScrollbar bool `opt:"appearance.hide_scrollbar"`

	// WindowTitlePosition controls where window titles are displayed
	// Options: bottom, top, hidden
	// Set via --window-title-position flag or appearance.window_title_position config
	WindowTitlePosition string `opt:"appearance.window_title_position,keep"`

	// WindowTitleStyle and WindowTitleAlign are appearance.window_title_style
	// and window_title_align.
	WindowTitleStyle string
	WindowTitleAlign string

	// WindowTitleFormat is the template used to build a window's displayed title.
	// Empty (the default) means the title is shown as-is. See FormatWindowTitle for
	// the supported placeholders.
	// Set via appearance.window_title_format config
	WindowTitleFormat string `opt:"appearance.window_title_format"`

	// HostTitle is the template for the title of the terminal tuios runs in,
	// built from the focused pane. Empty (the default) sets none. See
	// FormatHostTitle for the supported placeholders.
	// Set via appearance.host_title config
	HostTitle string `opt:"appearance.host_title"`

	// HideClock controls whether the clock overlay is hidden
	// Set via --hide-clock flag or appearance.hide_clock config
	// Deprecated: Use ShowClock instead. HideClock takes precedence when true.
	HideClock bool `opt:"appearance.hide_clock"`

	// ShowClock controls whether the clock overlay is shown (default: hidden).
	// Set via --show-clock flag or appearance.show_clock config
	ShowClock bool `opt:"appearance.show_clock"`

	// ShowCPU controls whether the CPU graph is shown in the dock (default: hidden).
	// Set via --show-cpu flag or appearance.show_cpu config
	ShowCPU bool `opt:"appearance.show_cpu"`

	// ShowRAM controls whether RAM usage is shown in the dock (default: hidden).
	// Set via --show-ram flag or appearance.show_ram config
	ShowRAM bool `opt:"appearance.show_ram"`

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
	CopyOnSelect bool `opt:"appearance.copy_on_select,keep"`

	// NvimNavigation lets tuios and the optional tuios-nvim-navigator plugin
	// negotiate terminal focus keys through OSC 7777. It is off by default.
	// Set via appearance.nvim_navigation config.
	NvimNavigation bool `opt:"appearance.nvim_navigation,keep"`

	// FocusWrap lets a directional focus key in a tiled layout wrap to the far
	// side of the layout when no pane lies that way. It is off by default.
	// Set via appearance.focus_wrap config.
	FocusWrap bool `opt:"appearance.focus_wrap,keep"`

	// FocusFollowsMouse focuses the pane under the cursor as the mouse moves over
	// it, without a click and without entering terminal mode. It is a divisive
	// window-manager habit, so it defaults off and users opt in.
	// Set via appearance.focus_follows_mouse config.
	FocusFollowsMouse bool `opt:"appearance.focus_follows_mouse,keep"`
	// PastePreview shows the image an image paste turned into a path, in a
	// card at the pane's cursor for a few seconds.
	PastePreview bool `opt:"appearance.paste_preview,keep"`

	// FocusFollowsMousePause is the modifier, "ctrl" or "alt", that pauses
	// FocusFollowsMouse while it is held. Empty never pauses. tuios learns the
	// modifier from the motion report itself, and does not consume it: a pane
	// program that tracks the mouse still gets the motion with the modifier.
	// Set via appearance.focus_follows_mouse_pause config.
	FocusFollowsMousePause string

	// AltDrag makes alt + left-drag move a pane, the gesture nearly every desktop
	// window manager binds. It is on by default because the hands that know it
	// already outnumber the ones that do not. Turning it off hands alt-drag back to
	// the pane: selection while typing, and whatever a mouse-tracking app makes of
	// it. Alt + right-drag resizes either way, since that is the ordinary
	// right-press resize with alt only keeping the menu out of the way.
	// Set via appearance.alt_drag config.
	AltDrag bool `opt:"appearance.alt_drag,keep"`

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
	// release, which is what a newcomer expects a click to do (the default before
	// v0.8.0). "double" focuses on one click and enters on two, so arranging panes
	// with the mouse does not let a stray click take the window manager's keys
	// away; it is the default. "off" never changes mode from a click: the way in
	// stays the enter_terminal_mode binding.
	//
	// The mode decides who owns the mouse, here as everywhere else: a pane whose
	// app asked for mouse tracking is only forwarded to in terminal mode, so under
	// "off" the mouse alone cannot reach that app. "double" is the setting for
	// someone who lives in mouse-mode apps and still wants the mouse to be a
	// pointer first.
	// Set via appearance.click_to_type config.
	ClickToType string `opt:"appearance.click_to_type,keep"`

	// RightClickOpensMenu makes a plain right-click on a pane's content open the
	// pane menu while the keyboard is typing in it, instead of reserving the
	// unmodified right button.
	//
	// Off by default: a pointer reaches the menu with ctrl or shift held and a
	// plain right-click is consumed, so the button stays with the pane. A touch
	// client has no modifier and opens the menu with a long press regardless.
	// Turning this on is how someone makes the menu's Paste row one plain click
	// away, without selecting anything first.
	// Set via appearance.right_click_opens_menu config.
	RightClickOpensMenu bool `opt:"appearance.right_click_opens_menu,keep"`

	// KittyPlaceholders decides whether images an application positions with
	// kitty Unicode placeholders are drawn.
	//
	// "auto" draws them when the host terminal is one known to implement them,
	// which is read off the name and version it reports rather than from TERM;
	// there is no way to ask a terminal whether it has the feature. "on" and
	// "off" say so outright, for a terminal the table does not know or gets
	// wrong.
	//
	// Off means the placeholder cells are dropped, which is what tuios always
	// did and which leaves the blank space the application made room for.
	// Keeping them on a host that cannot draw them fills that space with
	// missing-glyph boxes instead.
	// Set via appearance.kitty_placeholders config.
	KittyPlaceholders string

	// ImageSymbols decides how a pane's sixel image is shown on a host
	// terminal that draws no graphics, such as kmscon or the Linux console.
	// A glyph set name draws the picture as block glyphs of that set:
	// octant (Unicode 16), sextant (Unicode 13), quadrant, half. "auto"
	// picks by TERM: octant on kmscon, half on the Linux console, quadrant
	// elsewhere (see imageSymbolKind in internal/app). "off" draws a box and
	// tells the pane there is no sixel, so programs use their own text.
	// Set via appearance.image_symbols config.
	ImageSymbols string

	// NewWindowInheritCwd starts a new window in the working directory of the
	// pane that was focused when it was asked for, rather than in the
	// directory the daemon itself was started in.
	//
	// On by default. A window opened from a pane deep in a project is almost
	// always wanted in that project, and a shell started elsewhere has to be
	// cd'd there again by hand. The directory is read from the focused pane's
	// live shell, so it follows the pane rather than where the pane started,
	// and anything that cannot be read falls back to the default directory
	// rather than failing to open a window.
	// Set via appearance.new_window_inherit_cwd config.
	NewWindowInheritCwd bool `opt:"appearance.new_window_inherit_cwd,keep"`

	// NewWindowFollowSSH makes the ordinary split and new-window actions
	// behave like their ssh versions: when the focused pane runs ssh, the new
	// pane runs the same ssh. Off by default, so a split is a local shell
	// unless the person asks for the ssh version by its own key.
	// Set via appearance.new_window_follow_ssh config.
	NewWindowFollowSSH bool `opt:"appearance.new_window_follow_ssh,keep"`

	// WordCharacters lists the punctuation that counts as part of a word when a
	// double-click selects one, on top of letters and digits, which always do.
	//
	// The default is kitty's select_by_word_characters, and it is chosen for what
	// terminal content actually looks like: it takes a path, a URL, a version
	// number, or a flag such as --no-vm as a single word instead of stopping at
	// every punctuation mark. A colon is deliberately absent, so host:port and
	// file:line select as their parts.
	// Set via appearance.word_characters config.
	WordCharacters string `opt:"appearance.word_characters,keep"`

	// NiriReverseScroll reverses mouse scroll direction in niri scrolling mode.
	// When true, scroll-up moves viewport right and scroll-down moves left.
	// Set via appearance.niri_reverse_scroll config
	NiriReverseScroll bool `opt:"appearance.niri_reverse_scroll"`

	// NiriScrollCells is how many cells one wheel event walks the strip in the
	// scrolling layout. See NiriScrollCellsDefault for why it is a flat count
	// and not a share of the screen.
	// Set via appearance.niri_scroll_cells config
	NiriScrollCells int `opt:"appearance.niri_scroll_cells"`

	// The timing options, in milliseconds. Set via appearance.whichkey_delay_ms,
	// tooltip_delay_ms, zen_reveal_ms, paste_preview_ms and
	// appearance.chord_timeout_ms.
	IdleFPS             int `opt:"appearance.idle_fps"`
	SidebarGitRefreshMS int `opt:"appearance.sidebar.git_refresh_ms"`
	ShowKeyCount        int `opt:"debug.show_key_count"`
	WhichKeyDelayMS     int `opt:"appearance.whichkey_delay_ms"`
	TooltipDelayMS      int `opt:"appearance.tooltip_delay_ms"`
	ZenRevealMS         int `opt:"appearance.zen_reveal_ms"`
	PastePreviewMS      int `opt:"appearance.paste_preview_ms"`
	ChordTimeoutMS      int `opt:"appearance.chord_timeout_ms"`

	// MouseDoubleClickMS is how long a second click may follow the first and
	// still be a double click. Set via mouse.double_click_ms.
	MouseDoubleClickMS int `opt:"mouse.double_click_ms"`
	// MouseDragThreshold is the summed cell movement that makes a press a
	// drag. Set via mouse.drag_threshold.
	MouseDragThreshold int `opt:"mouse.drag_threshold"`

	// NiriScrollSnap lines the strip up on the nearest column once a wheel,
	// a trackpad or a finger has stopped moving it, as niri does at the end
	// of a gesture. Set via appearance.niri_scroll_snap config
	NiriScrollSnap bool `opt:"appearance.niri_scroll_snap"`

	// PrefixRepeatTime is how long the prefix stays armed after a repeatable
	// prefix command, in milliseconds. Zero turns it off. See
	// PrefixRepeatTimeDefault.
	PrefixRepeatTime int `opt:"appearance.prefix_repeat_time,keep"`

	// LeaderKey is the prefix key for commands (default: ctrl+b)
	// Set via appearance.leader_key config
	LeaderKey string
	// KeyboardLayout and OptionGlyphs are keybindings.keyboard_layout and
	// keybindings.option_glyphs, for the leader check, which reads Settings
	// rather than the binding tables. Empty is the default of each.
	KeyboardLayout string
	OptionGlyphs   string
	// Lock is keybindings.lock: terminal mode gives every key but the leader
	// to the focused pane.
	Lock bool
	// NestExitKey is keybindings.nest_exit_key, the key that ends nest mode.
	// Empty is DefaultNestExitKey. See GetNestExitKey.
	NestExitKey string

	// PaneGap is the cells of empty space the tiler keeps between two neighbouring
	// panes: i3's inner gap, and about the only spacing a terminal window manager
	// can honestly offer.
	//
	// Inner only. An outer gap would have to inset the content region, which the
	// sidebar's width, the dock's height, every overlay's placement and every mouse
	// hit test are measured against, so a margin the sidebar already draws one of
	// would cost a move of the whole frame.
	PaneGap int `opt:"appearance.gap"`

	// UndoWindowSeconds is agents.undo_window_seconds: how long u takes back
	// an Inbox dismiss, a snooze or a queued reply. The daemon keeps the same
	// window.
	UndoWindowSeconds int `opt:"agents.undo_window_seconds"`

	// ReconnectSeconds is daemon.reconnect_seconds: how long the client waits
	// for a restarted daemon.
	ReconnectSeconds int `opt:"daemon.reconnect_seconds"`

	// MasterRatioPercent is how much of the screen the master pane takes in the
	// master-stack layout, as a percent. It is the value a new session starts at;
	// once a session is running the ratio is the session's, moved by the resize
	// keys and settled across every attached client, because it decides how many
	// columns a pane gets and a PTY has exactly one size.
	MasterRatioPercent int `opt:"appearance.master_ratio"`

	// MasterPosition, MasterCount and MasterNoGrid are the master-stack shape
	// a workspace starts with: the side the masters take (one of
	// MasterPositions), how many panes are masters, and whether one master on
	// the left stops turning four or more panes into a grid. A workspace
	// changed at run time keeps its own values in the session. Set via
	// appearance.master_position, master_count and master_grid. The zero value
	// of each is the layout as it was before they existed.
	MasterPosition string
	MasterCount    int
	MasterNoGrid   bool

	// ScrollColumnWidth is how wide a column is in the scrolling layout, as a
	// percent of the screen, before anything resizes it. Session state for the same
	// reason the master ratio is.
	//
	// The default is deliberately over half. The strip is meant to be wider than
	// the viewport (that is what makes it a strip rather than a grid), so a
	// default that let two columns sit side by side exactly would show the layout
	// as a two-pane split and never as something you scroll.
	ScrollColumnWidth int

	// ScrollColumnMax is the highest ScrollColumnWidth may be set to, as a
	// percent of the screen.
	//
	// The default, 90, is where the next column stops peeking in at the edge,
	// which is the only thing that says the strip has one. Raising it to 100
	// gives a column the whole screen: it is the ask from somebody who wanted a
	// pane at full width without zooming, because zooming costs them the fast
	// window switching the strip is for. The peek is what that trades away,
	// which is why it is a setting and not the new default.
	//
	// Read it through GetScrollColumnMax, never directly: a Settings built by
	// hand carries a zero here, and a clamp against zero pins every column to
	// the floor.
	ScrollColumnMax int `opt:"appearance.scroll_column_max"`

	// ScrollColumnPresets are the widths scroll_cycle_width steps through, as
	// ascending fractions of the screen, from appearance.scroll_column_presets.
	// Nil means the scrolling layout's built-in presets. Presets above
	// ScrollColumnMax are skipped by the cycle, not here, so raising the cap
	// live brings them back.
	ScrollColumnPresets []float64

	// ZoomSize is how much of the content region a zoomed pane takes, as a
	// percent. 100, the default, is the whole of it.
	//
	// Below 100 the layout around the pane stays on screen at the edges, which
	// is the scrolling layout's peek in both directions at once: a zoom that
	// still shows you what you are not looking at. The box is pulled toward the
	// pane's own corner rather than centred, so the neighbours that show are the
	// ones it actually has.
	//
	// Read it through GetZoomSize, never directly.
	ZoomSize int `opt:"appearance.zoom_size"`

	// WindowButtonZoom draws the third title bar control on a tiled pane, where
	// it toggles the zoom.
	//
	// On a tiled pane a maximize means zoom, since the tiler owns the
	// rectangle, and a tiled pane is exactly where a zoom is worth reaching
	// for. The green disc is the control everybody already knows.
	WindowButtonZoom bool `opt:"appearance.window_button_zoom,keep"`

	// ZoomFollowsFocus hands the zoom to the pane the focus lands on.
	//
	// A zoomed workspace shows one pane. If focus moved underneath it, the
	// next-pane key would focus the pane after it while the zoomed pane kept
	// the box, and keys would go to a pane nobody can see. The zoom is the
	// statement that you want one pane and the whole region for it; a focus
	// move is the statement of which pane.
	ZoomFollowsFocus bool `opt:"appearance.zoom_follows_focus,keep"`

	// AnimationSpeed is the percent window animations run at: 100 is as
	// shipped, 200 twice as fast, 50 half as fast.
	AnimationSpeed int `opt:"appearance.animation_speed"`

	// ThemeTransition is how a change of theme is shown: ThemeTransitionNone
	// swaps the colours in one frame, ThemeTransitionFade crosses the whole
	// screen over, and ThemeTransitionCircle grows the new theme from the
	// middle of the screen. Read it through ThemeTransitionStyle, which also
	// honours appearance.motion.
	ThemeTransition string
	// ThemeTransitionMs is how long the transition takes at animation speed
	// 100. Read it through ThemeTransitionDuration.
	ThemeTransitionMs int `opt:"appearance.theme_transition_duration"`

	// WorkspaceTransition is how a workspace switch is shown: slide, fade or
	// none. Read it through WorkspaceTransitionStyle, which also honours
	// appearance.motion.
	WorkspaceTransition string
	// Aspects holds each [motion] aspect. Read them through MotionOn and
	// MotionLength.
	Aspects [numAspects]AspectSetting

	// ZoomBorderless makes a zoom the whole pane region with no chrome: the
	// zoomed pane drops its border and title bar, and its guest is told the
	// full size of the region. zoom_size and zoom_max_width do not apply while
	// it is on, since a pane with no border has nothing to show around it.
	ZoomBorderless bool `opt:"appearance.zoom_borderless"`

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
	DimUnfocused int `opt:"appearance.dim_unfocused"`

	// DimForeground lets DimUnfocused carry a pane's text toward its ground,
	// the same as its background. True, the default, dims both; false keeps
	// the text at full strength and dims only the background, for a rice that
	// wants an unfocused pane to read as quieter without its text going dim
	// too.
	DimForeground bool

	// DimMultifocus dims the panes in the multifocus set like every other
	// unfocused pane. False, the default, leaves them undimmed: they take the
	// keys the user types, and full-strength content is what says so.
	DimMultifocus bool `opt:"appearance.dim_multifocus"`

	// Background is the ground painted on every surface that does not set
	// its own: "off", the default, leaves the default background transparent
	// so the host terminal shows through; "theme" paints the active theme's
	// background; a #RRGGBB literal paints that colour.
	//
	// PaneBackground, DesktopBackground, WindowChromeBackground,
	// DockBackground and SidebarBackground are each surface's own setting.
	// Each takes the same values, and empty follows Background. Only cells
	// left on the default background are painted, so a background a program
	// or a piece of chrome chose wins. See ResolveBackground and the
	// *BackgroundResolved methods.
	Background             string `opt:"appearance.background"`
	PaneBackground         string `opt:"appearance.pane_background"`
	DesktopBackground      string `opt:"appearance.desktop_background"`
	WindowChromeBackground string `opt:"appearance.window_chrome_background"`
	DockBackground         string `opt:"appearance.dock_background"`
	SidebarBackground      string `opt:"appearance.sidebar.background"`

	// SidebarCursorBackground is the ground of the rail's cursor row while
	// the rail has the keyboard. Empty keeps the palette's Surface. It is
	// a colour only, with no off or theme: the row is a band on the rail,
	// not a surface of its own. See sidebarRowBg.
	SidebarCursorBackground string `opt:"appearance.sidebar.cursor_background"`

	// ClockFormat is the Go time layout the clock overlay draws with. Empty takes
	// DefaultClockFormat.
	//
	// A layout rather than a set of toggles, for the reason window_title_format is
	// one: "seconds on or off" is two of the questions people actually have about a
	// clock, and the standard library already has a spelling for all of them.
	ClockFormat string `opt:"appearance.clock_format"`

	// ZoomMaxWidth is the maximum width in cells for zoom/zen mode.
	// 0 means fullscreen (no max width cap). When set (e.g., 120), the zoomed
	// window is centered horizontally and capped at this width.
	ZoomMaxWidth int

	// GlyphSet names the chrome glyph set, or "default" for the shipped one. It is
	// the shape half of a rice, beside Theme's colour half.
	GlyphSet string `opt:"appearance.glyphs"`
}

// DefaultSettings is tuios as it ships, before any config file, any flag and
// any settings page. It is the seed for Global and the value every unconfigured
// session starts from.
// The three answers appearance.kitty_placeholders takes.
const (
	KittyPlaceholdersAuto = "auto"
	KittyPlaceholdersOn   = "on"
	KittyPlaceholdersOff  = "off"
)

// KittyPlaceholderModes is what appearance.kitty_placeholders accepts.
var KittyPlaceholderModes = []string{KittyPlaceholdersAuto, KittyPlaceholdersOn, KittyPlaceholdersOff}

// ImageSymbolsAuto is the default of appearance.image_symbols.
const ImageSymbolsAuto = "auto"

// ImageSymbolModes is what appearance.image_symbols accepts. The names other
// than auto are mosaic.Kind names.
var ImageSymbolModes = []string{ImageSymbolsAuto, "octant", "sextant", "quadrant", "half", "off"}

// The values of notifications.bell.
const (
	// BellToast shows a message in the dock. It is the default, and what
	// tuios did before the option existed.
	BellToast = "toast"
	// BellForward writes the BEL to the terminal tuios runs in.
	BellForward = "forward"
	// BellBoth does both.
	BellBoth = "both"
	// BellOff does neither.
	BellOff = "off"
)

// BellModes is what notifications.bell accepts.
var BellModes = []string{BellToast, BellForward, BellBoth, BellOff}

// The values of notifications.style.
const (
	// NotificationStyleFull draws the severity cap on the message's left and
	// burns the rule above it down as the message ages. It is the default.
	NotificationStyleFull = "full"
	// NotificationStylePlain draws the severity mark and the text, with no cap
	// and no countdown.
	NotificationStylePlain = "plain"
)

// NotificationStyles is what notifications.style accepts.
var NotificationStyles = []string{NotificationStyleFull, NotificationStylePlain}

// The notifications.level values, least severe first. info shows every
// message, success included.
const (
	NotificationLevelInfo    = "info"
	NotificationLevelWarning = "warning"
	NotificationLevelError   = "error"
)

// NotificationLevels is every notifications.level value.
var NotificationLevels = []string{NotificationLevelInfo, NotificationLevelWarning, NotificationLevelError}

// NotificationsPlain reports whether a dock message is drawn without its cap
// and countdown.
func (s *Settings) NotificationsPlain() bool {
	return s.NotificationStyle == NotificationStylePlain
}

func DefaultSettings() Settings {
	return Settings{
		NotificationDuration:        2 * time.Second,
		NotificationWarningDuration: 8 * time.Second,
		NotificationErrorDuration:   15 * time.Second,
		NotificationErrorSticky:     true,
		Bell:                        BellToast,
		NotificationStyle:           NotificationStyleFull,
		NotificationLevel:           NotificationLevelInfo,
		NotificationLogHidden:       true,
		NotificationMachineOffer:    true,
		NormalFPS:                   DefaultFPS,
		UseASCIIOnly:                false,
		Motion:                      MotionFull,
		ModalDim:                    ModalDimDefault,
		PopupDim:                    PopupDimDefault,
		AnimationsSuppressed:        false,
		AlwaysConfirmQuit:           false,
		ConfirmClose:                ConfirmCloseNever,
		WhichKeyEnabled:             true,
		WhichKeyPosition:            "bottom-right",
		WrapLists:                   true,
		SharedBorders:               false,
		BorderStyle:                 "rounded",
		TilingScheme:                TilingSchemeSpiral,
		ZenMode:                     ZenModeDisabled,
		Links:                       LinksAll,
		LinkClick:                   LinkClickBoth,
		DropPreview:                 DropPreviewOutline,
		DropPreviewDelay:            DefaultDropPreviewDelay,
		DockbarPosition:             DefaultDockbarPosition,
		SidebarEnabled:              true,
		SidebarPosition:             DefaultSidebarPosition,
		SidebarWidth:                SidebarDefaultWidth,
		SidebarShowGlyphs:           true,
		SidebarShowCounts:           true,
		SidebarShowNumbers:          false,
		SidebarMarquee:              true,
		SidebarPeekEnabled:          true,
		SidebarSections:             SidebarDefaultSections,
		SidebarFileIcons:            true,
		SidebarFileIconColors:       true,
		SidebarFolderClick:          SidebarFolderClickNavigate,
		SidebarFileDrag:             true,
		SidebarFilesStyle:           SidebarFilesList,
		SidebarFileActions:          true,
		SidebarFileDelete:           SidebarFileDeleteTrash,
		SidebarAgentRow:             DefaultSidebarAgentRow(),
		SidebarAgentRestFold:        DefaultAgentRestFold,
		SidebarSubagents:            DefaultSidebarSubagentView(),
		Tooltips:                    true,
		SessionColors:               true,
		SessionBorder:               false,
		GlobalSession:               true,
		SidebarGitDirty:             true,
		NiriClickReveals:            true,
		NiriHoverReveals:            true,
		DockWorkspaceTabs:           true,
		DockWorkspaceTabFormat:      "",
		DockWorkspaceTooltip:        true,
		DockWorkspaceLabelMax:       12,
		DockPillCaps:                true,
		DockModeSplitLetter:         true,
		DockPrefixIndicator:         DefaultDockPrefixIndicator,
		DockModePill:                DefaultDockModePill,
		BorderGradientAngle:         DefaultBorderGradientAngle,
		HideWindowButtons:           false,
		WindowButtonStyle:           WindowButtonStyleDots,
		WindowButtonPosition:        WindowButtonPositionLeft,
		ScrollbarStyle:              ScrollbarStyleTrack,
		ScrollbarThumb:              "",
		ScrollbarTrack:              "",
		ScrollbarTint:               ScrollbarTintQuiet,
		SelectionBg:                 DefaultSelectionBg,
		SelectionFg:                 DefaultSelectionFg,
		SelectionBold:               false,
		SearchBg:                    DefaultSearchBg,
		SearchFg:                    DefaultSearchFg,
		MatchBg:                     DefaultMatchBg,
		MatchFg:                     DefaultMatchFg,
		CopyCursorBg:                DefaultCopyCursorBg,
		CopyCursorFg:                DefaultCopyCursorFg,
		CopyFlash:                   true,
		CopyFlashMs:                 CopyFlashMsDefault,
		CopyFlashColor:              DefaultCopyFlashColor,
		CopyFlashStyle:              DefaultCopyFlashStyle,
		MultiCopyFormat:             MultiCopyFormatPlain,
		CopyEntry:                   CopyEntryCursor,
		OSC52Write:                  OSC52WriteFocused,
		HideScrollbar:               false,
		WindowTitlePosition:         DefaultWindowTitlePosition,
		WindowTitleStyle:            DefaultWindowTitleStyle,
		WindowTitleAlign:            DefaultWindowTitleAlign,
		WindowTitleFormat:           "",
		HostTitle:                   "",
		HideClock:                   false,
		ShowClock:                   false,
		ShowCPU:                     false,
		ShowRAM:                     false,
		ScrollbackLines:             DefaultScrollbackLines,
		ScrollLines:                 3,
		CopyOnSelect:                true,
		NvimNavigation:              false,
		FocusFollowsMouse:           false,
		PastePreview:                true,
		AltDrag:                     true,
		ClickToType:                 ClickToTypeDouble,
		NewWindowInheritCwd:         true,
		KittyPlaceholders:           KittyPlaceholdersAuto,
		ImageSymbols:                ImageSymbolsAuto,
		RightClickOpensMenu:         false,
		AutoEnterTerminalOnFocus:    AutoEnterTerminalOff,
		WordCharacters:              `@-./_~?&=%+#`,
		NiriReverseScroll:           false,
		NiriScrollCells:             NiriScrollCellsDefault,
		MouseDoubleClickMS:          MouseDoubleClickDefault,
		IdleFPS:                     IdleFPSDefault,
		SidebarGitRefreshMS:         SidebarGitRefreshMSDefault,
		ShowKeyCount:                ShowKeyCountDefault,
		WhichKeyDelayMS:             WhichKeyDelayMSDefault,
		TooltipDelayMS:              TooltipDelayMSDefault,
		ZenRevealMS:                 ZenRevealMSDefault,
		PastePreviewMS:              PastePreviewMSDefault,
		ChordTimeoutMS:              ChordTimeoutMSDefault,
		MouseDragThreshold:          MouseDragThresholdDefault,
		PrefixRepeatTime:            PrefixRepeatTimeDefault,
		LeaderKey:                   DefaultLeaderKey,
		PaneGap:                     0,
		MasterRatioPercent:          MasterRatioDefault,
		MasterPosition:              MasterPositionLeft,
		MasterCount:                 MasterCountDefault,
		ScrollColumnWidth:           ScrollColumnWidthDefault,
		ScrollColumnMax:             ScrollColumnWidthMax,
		ZoomSize:                    ZoomSizeDefault,
		AnimationSpeed:              AnimationSpeedDefault,
		ThemeTransition:             ThemeTransitionNone,
		ThemeTransitionMs:           ThemeTransitionMsDefault,
		WorkspaceTransition:         WorkspaceTransitionSlide,
		ZoomFollowsFocus:            true,
		WindowButtonZoom:            true,
		DimUnfocused:                0,
		DimForeground:               true,
		Background:                  BackgroundOff,
		ClockFormat:                 "",
		ZoomMaxWidth:                0,
		GlyphSet:                    theme.GlyphSetNone,
	}
}

// DefaultScrollbackLines is how many lines a pane keeps behind it as it ships.
// Named because a caller with no session in reach (a window built in a test,
// or a harness) still has to say how deep the scrollback is, and the number is
// better said once here than repeated at every one of them.
const DefaultScrollbackLines = 10000

// DefaultLeaderKey is the prefix key tuios ships with. It is a constant rather
// than a read of Settings.LeaderKey because the places that fall back to it are
// asking "what does tuios bind when nobody said otherwise", which is one answer
// for the whole program and not one per session.
const DefaultLeaderKey = "ctrl+b"

// DefaultNestExitKey is the key that ends nest mode when
// keybindings.nest_exit_key is unset: F12, tmux-matryoshka's.
const DefaultNestExitKey = "f12"

// GetNestExitKey returns the key that ends nest mode.
func (s *Settings) GetNestExitKey() string {
	if s.NestExitKey == "" {
		return DefaultNestExitKey
	}
	return s.NestExitKey
}

// Global is the seed app.NewOS uses when it is given no Settings: what tests
// set before they build a model. Production code builds its Settings with
// Resolve and passes them in; internal/lint rejects a read of Global outside
// test files and app.NewOS. The tests move to passing Settings with the app
// test fixture, and Global goes then.
var Global = DefaultSettings()

// The values of appearance.cell_frames.
const (
	CellFramesServed = "served"
	CellFramesAll    = "all"
	CellFramesOff    = "off"
)

// CellFrameModes are the accepted values of appearance.cell_frames.
var CellFrameModes = []string{CellFramesServed, CellFramesAll, CellFramesOff}

// CellFramesFor reports whether a client writes its frames as diffed cells:
// a served one (SSH, web, the browser build) or this terminal's. The
// environment's TUIOS_CELL_FRAME, 1 or 0, overrides the option; an empty or
// unknown value reads as the default, all.
func (s *Settings) CellFramesFor(served bool) bool {
	mode := s.CellFrames
	switch os.Getenv("TUIOS_CELL_FRAME") {
	case "1":
		mode = CellFramesAll
	case "0":
		mode = CellFramesOff
	}
	switch mode {
	case CellFramesServed:
		return served
	case CellFramesOff:
		return false
	}
	return true
}
