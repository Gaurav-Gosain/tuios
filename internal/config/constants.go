// Package config provides configuration constants, keybinding management, and user settings.
package config

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/lrstanley/go-nf/glyphs/fa"
	"github.com/lrstanley/go-nf/glyphs/ple"
)

// =============================================================================
// Window Defaults
// =============================================================================

const (
	// DefaultWindowWidth is the default width for new terminal windows
	DefaultWindowWidth = 20

	// DefaultWindowHeight is the default height for new terminal windows
	DefaultWindowHeight = 5

	// MinWindowWidth is the minimum width a window can be resized to
	MinWindowWidth = 10
)

// =============================================================================
// Animation Durations
// =============================================================================

const (
	// DefaultAnimationDuration is the standard animation duration for minimize/restore operations
	DefaultAnimationDuration = 300 * time.Millisecond

	// FastAnimationDuration is the duration for snapping and window swapping animations
	FastAnimationDuration = 200 * time.Millisecond
)

// =============================================================================
// Notification Lifetimes
// =============================================================================

// =============================================================================
// Timeouts and Intervals
// =============================================================================

const (
	// CPUUpdateInterval is the interval between CPU usage updates
	CPUUpdateInterval = 500 * time.Millisecond

	// ProcessWaitDelay is the delay when waiting for process cleanup
	ProcessWaitDelay = 50 * time.Millisecond

	// WhichKeyDelay is the delay before showing which-key style overlay
	WhichKeyDelay = 500 * time.Millisecond
)

// =============================================================================
// FPS and Refresh Rates
// =============================================================================

var (

	// MaxFPSCap is the ceiling the renderer is allowed to reach. The tick loop
	// throttles actual work to NormalFPS; this is the upper bound so raising
	// NormalFPS at runtime (including the "unlimited" setting, which pins it to
	// this value) can take effect without a restart.
	//
	// It is the renderer's own ceiling and not a number of our choosing. Bubble
	// Tea clamps to 120 in NewProgram, so a larger value here was accepted by
	// the setting, offered by the settings row, handed to tea.WithFPS and then
	// quietly ignored: the tick loop really did run faster while the screen did
	// not, so the setting said one thing and the display did another. Matching
	// the renderer is the honest of the two fixes, because the other one is to
	// keep offering a number and explain in the row that part of it does
	// nothing.
	MaxFPSCap = 120

	// MinConfiguredFPS is the floor a configured max_fps is clamped to. Below it
	// the UI stops feeling like it is responding to the keyboard at all.
	MinConfiguredFPS = 10

	// IdleFPS is the refresh rate when the terminal is idle (no output for ~500ms).
	// Reduces CPU usage from ~10% to near-zero on idle.
	IdleFPS = 10
)

// =============================================================================
// UI Layout Dimensions
// =============================================================================

const (
	// DockHeight is the height of the dock area at the bottom
	DockHeight = 2

	// SidebarDefaultWidth is the preferred sidebar width on a wide screen.
	// Before v0.8.0 it was 28.
	SidebarDefaultWidth = 24

	// SidebarNarrowWidth is the width of the narrow rail (glyph + short name)
	// used on mid-width screens.
	SidebarNarrowWidth = 16

	// SidebarGlyphWidth is the width of the glyph-only rail used on small
	// screens: one glyph column plus a separator column.
	SidebarGlyphWidth = 3

	// SidebarMinPaneFloor is the fewest columns the content area is allowed to
	// keep for panes. The sidebar drops to a narrower variant before it would
	// squeeze panes below this.
	SidebarMinPaneFloor = 30

	// Sidebar breakpoints, measured against the render width. See
	// (*OS).GetSidebarWidth.
	SidebarBreakpointFull   = 90 // >= this: full sidebar at SidebarWidth
	SidebarBreakpointNarrow = 60 // >= this: narrow rail
	SidebarBreakpointGlyph  = 40 // >= this: glyph rail; below: auto-hidden

	// NotificationMaxWidth caps the dock's message block. Past about seventy
	// columns a message that keeps growing stops being a status line and starts
	// being a paragraph, so the rest is truncated instead.
	NotificationMaxWidth = 72

	// NotificationDockReserve is what the message block leaves the rest of the
	// dock: enough for the mode pill and the workspace counts, which are never
	// given up for a message.
	NotificationDockReserve = 18

	// NotificationMinWidth is the narrowest block worth reserving. Below it the
	// dock is too tight to split, and the message takes what the screen has
	// less a small margin instead.
	NotificationMinWidth = 14
)

// =============================================================================
// Dock Visual Characters: Nerd Font Icons (Default)
// Initialized from go-nf library in init()
// =============================================================================

var (
	// DockPillLeftChar is the left character for pill-style indicators
	DockPillLeftChar string

	// DockPillRightChar is the right character for pill-style indicators
	DockPillRightChar string

	// DockModeIconWindow is the icon for window mode (Nerd Font: nf-fa-window_restore)
	DockModeIconWindow string

	// DockModeIconTerminal is the icon for terminal mode (Nerd Font: nf-fa-terminal)
	DockModeIconTerminal string

	// DockModeIconTiling is the icon for tiling mode (Nerd Font: nf-fa-th, a 3x3 grid)
	DockModeIconTiling string

	// DockIconTerminalCount is the icon for terminal count (Nerd Font: nf-fa-terminal)
	DockIconTerminalCount string

	// DockIconWorkspaceCount is the icon for workspace count (Nerd Font: nf-fa-th_large, a 2x2 grid)
	DockIconWorkspaceCount string

	// DockIconLeaveRunning is the icon for the control that quits this client and
	// leaves the session up (Nerd Font: nf-fa-sign_out). The desktop metaphor is
	// carried by the glyph so the label next to it can stay plain.
	DockIconLeaveRunning string

	// DockIconCloseSession is the icon for the control that ends the session and
	// everything in it (Nerd Font: nf-fa-power_off).
	DockIconCloseSession string

	// DockSeparator is the separator between dock sections
	DockSeparator = "  " // Two spaces for breathing room

	// DockWorkspaceMoreLeft and DockWorkspaceMoreRight are the workspace strip's
	// overflow arrows. Single-angle quotes rather than triangles: they are one
	// cell in every font, so the strip's columns do not move with the glyph set,
	// and they read as "there is more this way" without the weight of a button.
	DockWorkspaceMoreLeft  = "‹"
	DockWorkspaceMoreRight = "›"

	// WindowPillLeft is the left pill-style character for window decorations.
	WindowPillLeft string
	// WindowPillRight is the right pill-style character for window decorations.
	WindowPillRight string
)

func init() {
	DockPillLeftChar = ple.LeftHalfCircleThick.String()
	DockPillRightChar = ple.RightHalfCircleThick.String()
	DockModeIconWindow = " " + fa.WindowRestore.String() + " "
	DockModeIconTerminal = " " + fa.Terminal.String() + " "
	DockModeIconTiling = " " + fa.Th.String() + " "
	DockIconTerminalCount = fa.Terminal.String()
	DockIconWorkspaceCount = fa.ThLarge.String()
	DockIconLeaveRunning = fa.SignOut.String()
	DockIconCloseSession = fa.PowerOff.String()
	WindowPillLeft = ple.LeftHalfCircleThick.String()
	WindowPillRight = ple.RightHalfCircleThick.String()
	NotificationGlyphError = fa.TimesCircle.String()
	NotificationGlyphWarning = fa.ExclamationTriangle.String()
	NotificationGlyphSuccess = fa.CheckCircle.String()
	NotificationGlyphInfo = fa.InfoCircle.String()
}

// =============================================================================
// Dock Visual Characters: ASCII Fallback
// =============================================================================

const (
	// ASCII fallback characters (used when --ascii-only flag is set)

	// DockPillLeftCharASCII is the ASCII fallback for pill left
	DockPillLeftCharASCII = "["

	// DockPillRightCharASCII is the ASCII fallback for pill right
	DockPillRightCharASCII = "]"

	// DockModeIconWindowASCII is the ASCII fallback for window mode
	DockModeIconWindowASCII = " W "

	// DockModeIconTerminalASCII is the ASCII fallback for terminal mode
	DockModeIconTerminalASCII = " T "

	// DockModeIconTilingASCII is the ASCII fallback for tiling mode
	DockModeIconTilingASCII = " # "

	// DockIconTerminalCountASCII is the ASCII fallback for terminal count
	DockIconTerminalCountASCII = "win"

	// DockIconWorkspaceCountASCII is the ASCII fallback for workspace count
	DockIconWorkspaceCountASCII = "ws"

	// DockIconLeaveRunningASCII is the ASCII fallback for the leave-running
	// control. One cell, like the glyph it stands in for, so the strip's columns
	// do not move with the font.
	//
	// The keybind's own letter. "<" was an angle bracket that meant nothing on
	// its own, and the workspace strip's overflow arrow on the same row is also
	// "<"; with the words gone the fallback has to carry the control by itself,
	// and prefix-d is the thing it does.
	DockIconLeaveRunningASCII = "d"

	// DockIconCloseSessionASCII is the ASCII fallback for the close-session
	// control, which is also the letter prefix-X ends a session with.
	DockIconCloseSessionASCII = "X"

	// DockSeparatorASCII is the ASCII fallback separator
	DockSeparatorASCII = " | "

	// DockWorkspaceMoreLeftASCII and DockWorkspaceMoreRightASCII are the ASCII
	// fallbacks for the workspace strip's overflow arrows.
	DockWorkspaceMoreLeftASCII  = "<"
	DockWorkspaceMoreRightASCII = ">"
)

// =============================================================================
// Tape Manager Icons
// =============================================================================

const (
	// TapeRecordingIndicator is the recording indicator
	TapeRecordingIndicator = "[REC]"
)

// =============================================================================
// Notification Icons (ASCII-safe)
// =============================================================================

const (
	// NotificationIconError is the error notification icon
	NotificationIconError = "[X]"

	// NotificationIconWarning is the warning notification icon
	NotificationIconWarning = "[!]"

	// NotificationIconSuccess is the success notification icon
	NotificationIconSuccess = "[OK]"

	// NotificationIconInfo is the info notification icon
	NotificationIconInfo = "[i]"
)

// Nerd Font severity marks, from the same FontAwesome block the dock already
// draws its terminal and workspace counts from, so a terminal that renders the
// dock at all renders these. They come from go-nf rather than being written out
// as literals for the same reason the dock icons do: a private-use codepoint
// pasted into source is invisible to review and is silently lost by any tool
// that touches the file, which is exactly how the first version of this shipped
// four empty strings and drew a message with no mark at all.
var (
	// NotificationGlyphError is the error mark (nf-fa-times_circle).
	NotificationGlyphError string

	// NotificationGlyphWarning is the warning mark (nf-fa-exclamation_triangle).
	NotificationGlyphWarning string

	// NotificationGlyphSuccess is the success mark (nf-fa-check_circle).
	NotificationGlyphSuccess string

	// NotificationGlyphInfo is the info mark (nf-fa-info_circle).
	NotificationGlyphInfo string
)

// The message block's opening edge. It is the severity rail in one cell: a
// freestanding partial block on the bare bar, inked two eighths for info and
// success, four for a warning and six for an error.
//
// Two eighths apart rather than one because a single eighth is not a difference
// you can see without the two of them side by side, and they never are. The
// weight is what carries severity into a greyscale screenshot or a theme with
// no contrast to spare.
//
// The ground behind the cap is the bar itself, never a fill. A sliver only
// reads as a weight against something that is not the same colour; against
// a solid severity field all it changes is where the field starts, with nothing
// to compare that against, and the severities become indistinguishable. That is
// why this design carries less colour than a filled pill would.
const (
	// NotificationCapLight is the info and success cap (U+258E, two eighths).
	NotificationCapLight = "▎"

	// NotificationCapMedium is the warning cap (U+258C, four eighths).
	NotificationCapMedium = "▌"

	// NotificationCapHeavy is the error cap (U+258A, six eighths).
	NotificationCapHeavy = "▊"

	// NotificationCapASCII is the cap with Nerd Fonts off. Weight cannot be
	// encoded in one ASCII cell, so severity falls back to the mark and the
	// colour alone.
	NotificationCapASCII = "|"
)

// The dock hairline's stroke while a message is burning down over it. A warning
// or an error is drawn heavy, so an escalating message is a heavier line as
// well as a different colour.
const (
	// NotificationRuleLight is the burn stroke for info and success (U+2500).
	NotificationRuleLight = "─"

	// NotificationRuleHeavy is the burn stroke for warnings and errors (U+2501).
	NotificationRuleHeavy = "━"
)

// GetNotificationCap returns the weighted cap for a severity, or the ASCII
// fallback when Nerd Fonts are off.
func (s *Settings) GetNotificationCap(cap string) string {
	if s.UseASCIIOnly {
		return NotificationCapASCII
	}
	return cap
}

// GetNotificationRule returns the burn stroke for the dock's hairline, matching
// the separator character the rest of the row is drawn with when Nerd Fonts are
// off so the lit run and the unlit run stay the same shape.
func (s *Settings) GetNotificationRule(stroke string) string {
	if s.UseASCIIOnly {
		return WindowSeparatorCharASCII
	}
	return stroke
}

// Dock Mode Colors
const (
	// DockColorWindow is the color for window mode indicator
	DockColorWindow = "#4865f2" // Blue

	// DockColorTerminal is the color for terminal mode indicator
	DockColorTerminal = "#4ade80" // Green

	// DockColorCopy is the color for copy mode indicator
	DockColorCopy = "#fb923c" // Orange
)

// =============================================================================
// Runtime Configuration
// =============================================================================

// The zoom box's size as a percent of the content region, and its range.
//
// 100 is the whole region, which is what zoom was before v0.8.0. Below it the
// pane keeps the middle and the layout around it stays on screen at the edges,
// the way the scrolling layout leaves the next column peeking in, except in
// both directions at once: you can see what you are not looking at. The
// default leaves a thin band of that layout showing.
const (
	ZoomSizeMin     = 50
	ZoomSizeMax     = 100
	ZoomSizeDefault = 95
)

// GetZoomSize is the zoom box's size as a percent of the content region.
//
// A value outside the range, including the zero a Settings built by hand
// carries, reads as the default. Nothing that computes the box may read the
// field directly, or a hand-built model would zoom to half a screen.
func (s *Settings) GetZoomSize() int {
	if s.ZoomSize < ZoomSizeMin || s.ZoomSize > ZoomSizeMax {
		return ZoomSizeDefault
	}
	return s.ZoomSize
}

// GetScrollColumnMax is the highest a scrolling column's width may be set to,
// as a percent of the screen.
//
// A value outside the allowed range, including the zero a Settings built by
// hand carries, reads as the default ceiling. Nothing that clamps a width may
// read the constant directly, or the setting would be ignored on whichever
// path forgot.
func (s *Settings) GetScrollColumnMax() int {
	if s.ScrollColumnMax < ScrollColumnWidthMin || s.ScrollColumnMax > ScrollColumnWidthCeiling {
		return ScrollColumnWidthMax
	}
	return s.ScrollColumnMax
}

// GetAnimationDuration returns the animation duration for standard operations.
// Returns 0 if animations are disabled or suppressed, causing instant transitions.
func (s *Settings) GetAnimationDuration() time.Duration {
	if !s.AnimationsEnabled || s.AnimationsSuppressed {
		return 0
	}
	return DefaultAnimationDuration
}

// GetFastAnimationDuration returns the animation duration for fast operations.
// Returns 0 if animations are disabled or suppressed, causing instant transitions.
func (s *Settings) GetFastAnimationDuration() time.Duration {
	if !s.AnimationsEnabled || s.AnimationsSuppressed {
		return 0
	}
	return FastAnimationDuration
}

// SidebarDefaultSections is the rail's layout as it ships.
//
// The syntax is one section per comma, in the order they are stacked from the
// top, each optionally followed by ":" and the percent of the rail's content
// lines it may claim. A section with no percent is flexible: it takes whatever
// the others leave. A name left out of the list is a section the rail does not
// draw.
//
// The percent is a ceiling, not a reservation. A section only ever claims the
// lines its own rows can fill, so the three quarters this default spends on
// sessions, files and agents are spent only when there are that many sessions,
// files and agents; the terminals list gets the rest, which on a normal rail is
// nearly all of it.
const SidebarDefaultSections = "sessions:25,terminals,files:25,agents:34"

// SidebarSectionNames are the sections appearance.sidebar.sections may name.
//
// This is membership as well as order. A section left out of the layout is a
// section the rail does not draw, and that is the only way to turn one off:
// there is no second switch per section, because a switch cannot say where a
// thing goes and two spacers would have no switch to share.
var SidebarSectionNames = []string{"sessions", "terminals", "files", "agents", "git"}

// SidebarSectionSpacer is the layout's empty block. It draws nothing and takes
// lines, which is how a person puts a gap between two sections or pushes what
// follows it to the bottom of the rail.
//
// It is the one name the layout may carry more than once, because it names a
// place rather than a section. Two spacers in a layout are two gaps, and the
// parser keeps both; every other name is dropped the second time it appears,
// since a rail cannot draw one list in two places.
//
// A spacer with a share takes that percent of the rail and keeps it. A spacer
// with no share takes the lines nothing else wants, which is what "push the
// rest to the bottom" means.
const SidebarSectionSpacer = "spacer"

// SidebarLayoutNames are every name the layout may carry: the sections, and the
// spacer. Used where a person is told what they may type.
func SidebarLayoutNames() []string {
	return append(append([]string{}, SidebarSectionNames...), SidebarSectionSpacer)
}

// What a click on a folder row in the files section does.
//
// Navigate is the default because it is the only one of the three that touches
// nothing outside the rail: the listing moves and no key is typed into anybody's
// program. tuios does not know that a pane is at a shell prompt until it looks,
// and a cd that reached vim or a REPL would be a series of edits or a syntax
// error. A user who wants the pane to follow can say so once, here.
const (
	SidebarFolderClickNavigate = "navigate"
	SidebarFolderClickCd       = "cd"
	SidebarFolderClickBoth     = "both"
)

// SidebarFolderClicks lists the valid values for appearance.sidebar.folder_click.
var SidebarFolderClicks = []string{
	SidebarFolderClickNavigate, SidebarFolderClickCd, SidebarFolderClickBoth,
}

// Where a delete from the files section sends the file.
//
// Trash is the default. The rail is an incidental place for a keystroke to land
// in a way a file manager is not, and a delete that cannot be undone is the
// wrong default for an incidental place. Permanent is for somebody who has
// decided they mean it, and it is on a key of its own as well, because a file
// on another disk cannot go to the home trash at all.
const (
	SidebarFileDeleteTrash     = "trash"
	SidebarFileDeletePermanent = "permanent"
)

// SidebarFileDeletes lists the valid values for appearance.sidebar.file_delete.
var SidebarFileDeletes = []string{SidebarFileDeleteTrash, SidebarFileDeletePermanent}

// FormatWindowTitle expands WindowTitleFormat for one window. The placeholders
// are {title} (the custom or terminal-reported title), {index} (the window's
// 1-based position in its workspace, the same number the leader-digit shortcuts
// use) and {cwd} (the shell's working directory, empty where it cannot be
// read).
//
// An empty format returns the title unchanged, which is what keeps the default
// rendering free of any formatting work.
func (s *Settings) FormatWindowTitle(title string, index int, cwd string) string {
	if s.WindowTitleFormat == "" {
		return title
	}
	return strings.NewReplacer(
		"{title}", title,
		"{index}", strconv.Itoa(index),
		"{cwd}", cwd,
	).Replace(s.WindowTitleFormat)
}

// FormatWorkspaceTab renders a dock workspace tab label from the configured
// DockWorkspaceTabFormat. Placeholders are {index} (the workspace number) and
// {name} (the workspace's name, or its number when unnamed). An empty format
// returns the name unchanged, which is the historic rendering.
func (s *Settings) FormatWorkspaceTab(name string, index int) string {
	if s.DockWorkspaceTabFormat == "" {
		return name
	}
	return strings.NewReplacer(
		"{name}", name,
		"{index}", strconv.Itoa(index),
	).Replace(s.DockWorkspaceTabFormat)
}

// PaneGapMax caps it. Past this the panes on a small terminal are further apart
// than they are wide, and spacing that swallows what it spaces has stopped
// being spacing.
const PaneGapMax = 8

// The tiling schemes, under the names they travel by: in [startup] layout, in
// session state, and in the command palette. They live here rather than in the
// package that implements them because the option registry has to publish the
// accepted set and cannot import that package.
const (
	LayoutModeBSP         = "bsp"
	LayoutModeMasterStack = "master-stack"
	LayoutModeScrolling   = "scrolling"
)

// LayoutModes is the accepted set, for the registry and the validator.
var LayoutModes = []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling}

// The master ratio's range and its default. The master-stack tiler clamps to
// the same range (layout.MinSplitRatio and MaxSplitRatio are read from here),
// and it matches the resize_width_N actions, which go from 10 to 90 percent, so
// a percentage resize the tiler keeps is one the settings row can show.
const (
	MasterRatioMin     = 10
	MasterRatioMax     = 90
	MasterRatioDefault = 50
)

// MasterRatioFraction is the configured master ratio as the fraction the tilers
// take. One conversion in one place, so a percentage in the file and a fraction
// in the layout cannot drift apart.
func (s *Settings) MasterRatioFraction() float64 {
	return float64(clampPercent(s.MasterRatioPercent, MasterRatioMin, MasterRatioMax, MasterRatioDefault)) / 100
}

// The column width's range and its default. The floor is the narrowest column
// a shell is usable in.
//
// ScrollColumnWidthMax is the default ceiling, and it is where the strip's next
// column stops peeking in at the edge, which is the only thing that says there
// is one. appearance.scroll_column_max raises it, up to
// ScrollColumnWidthCeiling: a full-width column gives the pane the whole screen
// without zooming, so the strip keeps its columns and its fast switching, and
// the peek is what you spend for it.
const (
	ScrollColumnWidthMin     = 20
	ScrollColumnWidthMax     = 90
	ScrollColumnWidthCeiling = 100
	ScrollColumnWidthDefault = 55
)

// How far one wheel event walks the scrolling layout's strip, in cells.
//
// It is a flat number of cells rather than a fraction of the visible width
// because a terminal reports trackpad scrolling as one wheel event per cell the
// fingers cross. A single flick and its momentum tail are tens of events, so a
// step of a fifth of the screen sends the strip to its clamp before the fingers
// leave the glass. A flat number is the same distance whatever the screen is,
// small enough that a flick lands where the user aimed, and large enough that a
// few notches of a wheel still get somewhere. Someone who only ever scrolls the
// strip with a wheel can raise it.
const (
	NiriScrollCellsMin     = 1
	NiriScrollCellsMax     = 200
	NiriScrollCellsDefault = 8
)

// DimUnfocusedMax caps it. The cap is not a legibility floor (content is the
// user's own text and they may quiet it as far as they like). It only stops a
// pane from being erased outright, where there is nothing left to show the
// setting worked and no way to tell a dimmed pane from a crashed one.
const DimUnfocusedMax = 90

// DefaultClockFormat is what the clock has always drawn.
const DefaultClockFormat = "15:04:05"

// GetClockFormat returns the layout in effect.
func (s *Settings) GetClockFormat() string {
	if s.ClockFormat == "" {
		return DefaultClockFormat
	}
	return s.ClockFormat
}

// GetSidebarPillLeftChar returns the rail's left pill cap. The rail keeps its
// own accessor so the dock's flat/capped setting cannot reshape its rows.
func (s *Settings) GetSidebarPillLeftChar() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.PillLeft }, DockPillLeftChar, DockPillLeftCharASCII)
}

// GetSidebarPillRightChar returns the rail's right pill cap.
func (s *Settings) GetSidebarPillRightChar() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.PillRight }, DockPillRightChar, DockPillRightCharASCII)
}

// GetDockPillLeftChar returns the pill's left cap, empty when pills are flat.
func (s *Settings) GetDockPillLeftChar() string {
	if !s.DockPillCaps {
		return ""
	}
	return s.GetSidebarPillLeftChar()
}

// GetDockPillRightChar returns the pill's right cap, empty when pills are flat.
func (s *Settings) GetDockPillRightChar() string {
	if !s.DockPillCaps {
		return ""
	}
	return s.GetSidebarPillRightChar()
}

// GetDockModeCapLeft returns the mode chip's left cap.
//
// The chip sits at the head of a row of capped workspace pills, so a square
// chip beside them reads as an unfinished pill rather than a different kind of
// thing. It caps regardless of DockPillCaps, which is really about the
// minimized run, where a cap on every entry turned the row into beads.
func (s *Settings) GetDockModeCapLeft() string { return s.GetSidebarPillLeftChar() }

// GetDockModeCapRight returns the mode chip's right cap.
func (s *Settings) GetDockModeCapRight() string { return s.GetSidebarPillRightChar() }

// GetDockWorkspaceCapLeft returns the workspace pill's left cap.
//
// The strip keeps its own accessor for the reason the rail does: DockPillCaps
// is about the mode chip and the minimized run, where a cap on every entry
// turned the row into beads. A workspace pill is a tab, and a tab wants the
// rounded end that says where it starts and stops.
//
// Empty under ASCII. A half circle has no 7-bit stand-in: "[" is a bracket
// drawn beside the pill rather than the pill's own edge, and it reads as
// punctuation in a row that has none.
func (s *Settings) GetDockWorkspaceCapLeft() string {
	if s.UseASCIIOnly {
		return ""
	}
	return s.GetSidebarPillLeftChar()
}

// GetDockWorkspaceCapRight returns the workspace pill's right cap.
func (s *Settings) GetDockWorkspaceCapRight() string {
	if s.UseASCIIOnly {
		return ""
	}
	return s.GetSidebarPillRightChar()
}

// GetDockModeIconWindow returns the appropriate window mode icon based on UseASCIIOnly
func (s *Settings) GetDockModeIconWindow() string {
	if s.UseASCIIOnly {
		return DockModeIconWindowASCII
	}
	return DockModeIconWindow
}

// GetDockModeIconTerminal returns the appropriate terminal mode icon based on UseASCIIOnly
func (s *Settings) GetDockModeIconTerminal() string {
	if s.UseASCIIOnly {
		return DockModeIconTerminalASCII
	}
	return DockModeIconTerminal
}

// GetDockModeIconTiling returns the appropriate tiling mode icon based on UseASCIIOnly
func (s *Settings) GetDockModeIconTiling() string {
	if s.UseASCIIOnly {
		return DockModeIconTilingASCII
	}
	return DockModeIconTiling
}

// GetDockIconTerminalCount returns the appropriate terminal count icon based on UseASCIIOnly
func (s *Settings) GetDockIconTerminalCount() string {
	if s.UseASCIIOnly {
		return DockIconTerminalCountASCII
	}
	return DockIconTerminalCount
}

// GetDockIconWorkspaceCount returns the appropriate workspace count icon based on UseASCIIOnly
func (s *Settings) GetDockIconWorkspaceCount() string {
	if s.UseASCIIOnly {
		return DockIconWorkspaceCountASCII
	}
	return DockIconWorkspaceCount
}

// GetDockIconLeaveRunning returns the leave-running icon for the current glyph set.
func (s *Settings) GetDockIconLeaveRunning() string {
	if s.UseASCIIOnly {
		return DockIconLeaveRunningASCII
	}
	return DockIconLeaveRunning
}

// GetDockIconCloseSession returns the close-session icon for the current glyph set.
func (s *Settings) GetDockIconCloseSession() string {
	if s.UseASCIIOnly {
		return DockIconCloseSessionASCII
	}
	return DockIconCloseSession
}

// GetDockSeparator returns the appropriate separator based on UseASCIIOnly
func (s *Settings) GetDockSeparator() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.Separator }, DockSeparator, DockSeparatorASCII)
}

// GetDockWorkspaceMoreLeft returns the strip's left overflow arrow.
func (s *Settings) GetDockWorkspaceMoreLeft() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.ArrowLeft }, DockWorkspaceMoreLeft, DockWorkspaceMoreLeftASCII)
}

// GetDockWorkspaceMoreRight returns the strip's right overflow arrow.
func (s *Settings) GetDockWorkspaceMoreRight() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.ArrowRight }, DockWorkspaceMoreRight, DockWorkspaceMoreRightASCII)
}

// =============================================================================
// Window Decoration Characters
// =============================================================================

const (
	// WindowBorderTopLeft is the top-left corner character for window borders (Nerd Font / Unicode).
	WindowBorderTopLeft = "╭" // U+256D
	// WindowBorderTopRight is the top-right corner character for window borders.
	WindowBorderTopRight = "╮" // U+256E
	// WindowBorderBottomLeft is the bottom-left corner character for window borders.
	WindowBorderBottomLeft = "╰" // U+2570
	// WindowBorderBottomRight is the bottom-right corner character for window borders.
	WindowBorderBottomRight = "╯" // U+256F
	// WindowBorderHorizontal is the horizontal line character for window borders.
	WindowBorderHorizontal = "─" // U+2500
	// WindowBorderVertical is the vertical line character for window borders.
	WindowBorderVertical = "│" // U+2502

	// WindowButtonClose is the close/kill window button character.
	//
	// U+2715 MULTIPLICATION X, not the U+292B RISING DIAGONAL CROSSING FALLING
	// DIAGONAL it used to be. U+292B lives in Miscellaneous Mathematical
	// Symbols-B, which JetBrainsMono Nerd Font does not cover at all, so a
	// terminal running it falls back to whatever proportional system font
	// happens to have the codepoint. That substitute's advance is wider than
	// one cell, so the glyph draws past the column the layout budgeted for it
	// and the falling diagonal is clipped by whatever is painted next.
	// U+2715 is in the font, has an advance of exactly one cell, and keeps its
	// ink well inside it. It carries the same East Asian Width class "N" as
	// U+292B, so it still measures 1 cell and the button pill keeps its old
	// width. See the hit-test offsets below, which depend on that width.
	WindowButtonCloseMark = "✕"
	// WindowButtonClose is that mark padded into the three-cell button.
	WindowButtonClose = " " + WindowButtonCloseMark + " "
	// WindowButtonMaximizeMark is the maximize mark.
	WindowButtonMaximizeMark = "□" // U+25A1
	// WindowButtonMaximize is that mark padded into the three-cell button.
	WindowButtonMaximize = " " + WindowButtonMaximizeMark + " "
	// WindowButtonMinimizeMark is the minimize mark. It leads the pill, so its
	// button carries an extra cell of lead-in rather than a symmetric pad.
	WindowButtonMinimizeMark = "-"
	// WindowButtonMinimize is the four-cell minimize button.
	WindowButtonMinimize = "  " + WindowButtonMinimizeMark + " "
	// WindowButtonDot is the disc the dots style draws each control as.
	//
	// U+25CF BLACK CIRCLE, which JetBrainsMono Nerd Font covers and draws at an
	// advance of exactly one cell. U+23FA BLACK CIRCLE FOR RECORD would have
	// been the closer shape and is not in the font at all, which is the defect
	// the close button's comment above records.
	WindowButtonDot = "●" // U+25CF
	// WindowSeparatorChar is the separator character for window elements.
	WindowSeparatorChar = "─" // U+2500
)

const (
	// WindowButtonCloseASCII is the close/kill window button character (ASCII fallback).
	WindowButtonCloseMarkASCII = "X"
	// WindowButtonCloseASCII is that mark padded into the three-cell button.
	WindowButtonCloseASCII = " " + WindowButtonCloseMarkASCII + " "
	// WindowButtonMaximizeMarkASCII is the maximize mark (ASCII fallback). One
	// cell like the Unicode mark, so the padded button keeps its three cells and
	// the hit-test offsets below still hold.
	WindowButtonMaximizeMarkASCII = "O"
	// WindowButtonDotASCII is the dots style's disc in ASCII. One cell like the
	// disc it stands in for, so the traffic light keeps its layout and its
	// colours, and only loses the roundness.
	WindowButtonDotASCII = "o"
	// WindowPillLeftASCII is the left pill-style character for window decorations (ASCII fallback).
	WindowPillLeftASCII = "["
	// WindowPillRightASCII is the right pill-style character for window decorations (ASCII fallback).
	WindowPillRightASCII = "]"
	// WindowSeparatorCharASCII is the separator character for window elements (ASCII fallback).
	WindowSeparatorCharASCII = "-"
)

// BorderStyles is every border style the app offers, in the order the settings
// page cycles them. One list so a style added here is offered, validated and
// covered by the border tests at once.
var BorderStyles = []string{
	"rounded", "normal", "thick", "double",
	"block", "outer-half-block", "inner-half-block",
	"ascii", "hidden", BorderStyleGlyphs,
}

// BorderJoinsChromeRules reports whether a divider drawn in the active style can
// meet the rule that closes the content region. Only a style drawn with strokes
// can: its junction glyph carries the rule's own stroke through the cell it
// takes over. A style drawn with fills would cover the rule instead, having
// inked its last cell up to the boundary already, and the hidden style would rub
// a cell of the rule out.
func (s *Settings) BorderJoinsChromeRules() bool {
	if s.UseASCIIOnly {
		return true
	}
	switch s.BorderStyle {
	case "block", "outer-half-block", "inner-half-block", "hidden":
		return false
	case BorderStyleGlyphs:
		// A set's border is answered for by the set. Reported as joining
		// because a set naming no junctions still gets the rounded border's,
		// and one that names them means them to be used.
		return true
	}
	return true
}

// GetBorderForStyle returns the lipgloss Border for the current style
func (s *Settings) GetBorderForStyle() lipgloss.Border {
	if s.UseASCIIOnly || s.BorderStyle == "ascii" {
		return lipgloss.ASCIIBorder()
	}
	if s.BorderStyle == BorderStyleGlyphs {
		return s.glyphSetBorder()
	}
	switch s.BorderStyle {
	case "normal":
		return lipgloss.NormalBorder()
	case "thick":
		return lipgloss.ThickBorder()
	case "double":
		return lipgloss.DoubleBorder()
	case "hidden":
		return lipgloss.HiddenBorder()
	case "block":
		return lipgloss.BlockBorder()
	case "outer-half-block":
		return lipgloss.OuterHalfBlockBorder()
	case "inner-half-block":
		return lipgloss.InnerHalfBlockBorder()
	case "rounded":
		fallthrough
	default:
		return lipgloss.RoundedBorder()
	}
}

// glyphSetBorder is the border the active glyph set draws, with any rune it
// leaves unnamed taken from the rounded border.
//
// Rounded rather than nothing, because a half-specified border is the likely
// case: a set that wants square corners says four runes and means "the rest as
// usual". Falling back per rune also means a set naming no border at all under
// border_style = "glyphs" draws the default frame rather than a hole.
//
// In ASCII mode the fallback is the ASCII border instead. GetBorderForStyle
// short-circuits to the ASCII border before it reaches here, so what draws was
// never wrong; what was wrong is what this reports. The rune check below
// rejects a set's non-ASCII runes in ASCII mode and every rejected rune was
// then replaced by a rounded one, so ResolvedGlyphs answered "what would this
// set draw" with ╭ corners that ASCII mode would never put on screen. That is
// the answer list-glyphs prints and the glyph picker previews.
func (s *Settings) glyphSetBorder() lipgloss.Border {
	b := lipgloss.RoundedBorder()
	if s.UseASCIIOnly {
		b = lipgloss.ASCIIBorder()
	}
	g := theme.Glyphs().Border
	if g == nil {
		return b
	}
	pick := func(dst *string, src string) {
		if src != "" && (!s.UseASCIIOnly || overlay.IsASCII(src)) {
			*dst = src
		}
	}
	pick(&b.Top, g.Top)
	pick(&b.Bottom, g.Bottom)
	pick(&b.Left, g.Left)
	pick(&b.Right, g.Right)
	pick(&b.TopLeft, g.TopLeft)
	pick(&b.TopRight, g.TopRight)
	pick(&b.BottomLeft, g.BottomLeft)
	pick(&b.BottomRight, g.BottomRight)
	pick(&b.Middle, g.Middle)
	pick(&b.MiddleTop, g.MiddleTop)
	pick(&b.MiddleBottom, g.MiddleBottom)
	pick(&b.MiddleLeft, g.MiddleLeft)
	pick(&b.MiddleRight, g.MiddleRight)
	return b
}

// Per-style scrollbar glyphs. The thin style's pair is one stroke at two
// weights: the same box-drawing vertical, light for the track and heavy for the
// thumb, so the bar reads as a single line that thickens where the viewport is
// rather than as two different shapes stacked in a column. Box-drawing
// verticals are drawn cell-height, so the track is an unbroken hairline, and
// they sit centred in the cell, which keeps the bar clear of the pane border
// instead of thickening it. Half and eighth blocks would hug the right edge and
// read as part of the frame.
//
// The track style fills its column instead, so its thumb is a whole block and
// its track is the surface fill behind it rather than a glyph.
const (
	scrollbarThinThumb  = "┃" // U+2503 BOX DRAWINGS HEAVY VERTICAL
	scrollbarThinTrack  = "│" // U+2502 BOX DRAWINGS LIGHT VERTICAL
	scrollbarTrackThumb = "█"
	scrollbarASCIIThumb = "|"
)

// scrollbarASCII reports whether the bar has to stay inside ASCII.
func (s *Settings) scrollbarASCII() bool {
	return s.UseASCIIOnly || s.BorderStyle == "ascii"
}

// scrollbarGlyphOverride returns a configured glyph if it can be drawn: exactly
// one cell wide, and plain ASCII when the rest of the frame is. Anything else
// falls back to the default, matching the warning validation raises for it.
func (s *Settings) scrollbarGlyphOverride(glyph string) (string, bool) {
	if glyph == "" || lipgloss.Width(glyph) != 1 {
		return "", false
	}
	if s.scrollbarASCII() {
		for _, r := range glyph {
			if r > 127 {
				return "", false
			}
		}
	}
	return glyph, true
}

// GetScrollbarThumbChar returns the glyph the thumb is drawn with: the
// configured one when it is usable, else the active style's default.
func (s *Settings) GetScrollbarThumbChar() string {
	if glyph, ok := s.scrollbarGlyphOverride(s.ScrollbarThumb); ok {
		return glyph
	}
	// The set is consulted under the explicit option and over the style's own
	// default: appearance.scrollbar.thumb is the narrower statement and keeps
	// winning, and a set the user chose is still a statement about the bar.
	if g, ok := s.scrollbarGlyphOverride(theme.Glyphs().ScrollbarThumb); ok {
		return g
	}
	if s.scrollbarASCII() {
		return scrollbarASCIIThumb
	}
	if s.ScrollbarStyle == ScrollbarStyleTrack {
		return scrollbarTrackThumb
	}
	return scrollbarThinThumb
}

// ScrollbarTintHex returns the configured tint when it is a colour literal
// rather than a keyword. A malformed one is not a colour, so it is refused here
// as well as warned about at load: a bar drawn in nothing is invisible.
func (s *Settings) ScrollbarTintHex() (string, bool) {
	if IsHexColor(s.ScrollbarTint) {
		return s.ScrollbarTint, true
	}
	return "", false
}

// ScrollbarTintResolved is the keyword the tint is behaving as, with unset
// resolved to the documented default.
//
// Unset used to reach the renderer as the empty string and match none of its
// cases, so it fell through to the border rule: clearing the tint gave the one
// tint that is not the default. The registry, the validator and the config
// header all say empty means quiet, and now so does the bar.
func (s *Settings) ScrollbarTintResolved() string {
	if s.ScrollbarTint == "" {
		return ScrollbarTintQuiet
	}
	return s.ScrollbarTint
}

// PaneBackgroundHex returns the configured pane background when it is a colour
// literal rather than a keyword.
func (s *Settings) PaneBackgroundHex() (string, bool) {
	if IsHexColor(s.PaneBackground) {
		return s.PaneBackground, true
	}
	return "", false
}

// PaneBackgroundResolved is what the pane background is behaving as: off,
// theme, or a #RRGGBB literal. Empty and anything unrecognised resolve to off,
// the documented default, so a typo paints nothing rather than a guess.
func (s *Settings) PaneBackgroundResolved() string {
	switch v := s.PaneBackground; {
	case v == PaneBackgroundTheme, IsHexColor(v):
		return v
	default:
		return PaneBackgroundOff
	}
}

// GetScrollbarTrackChar returns the glyph drawn on the track's uncovered cells.
// An empty string is a blank cell, which in the track style is its surface fill
// and in the thin style is no track at all. That is also what ASCII
// gets since it has no hairline to draw one with.
func (s *Settings) GetScrollbarTrackChar() string {
	if s.ScrollbarTrack == ScrollbarTrackNone {
		return ""
	}
	if glyph, ok := s.scrollbarGlyphOverride(s.ScrollbarTrack); ok {
		return glyph
	}
	if g, ok := s.scrollbarGlyphOverride(theme.Glyphs().ScrollbarTrack); ok {
		return g
	}
	if s.scrollbarASCII() || s.ScrollbarStyle == ScrollbarStyleTrack {
		return ""
	}
	return scrollbarThinTrack
}

// Window decoration getter functions

// GetWindowBorderTopLeft returns the appropriate top-left border character
func (s *Settings) GetWindowBorderTopLeft() string {
	return s.GetBorderForStyle().TopLeft
}

// GetWindowBorderTopRight returns the appropriate top-right border character
func (s *Settings) GetWindowBorderTopRight() string {
	return s.GetBorderForStyle().TopRight
}

// GetWindowBorderBottomLeft returns the appropriate bottom-left border character
func (s *Settings) GetWindowBorderBottomLeft() string {
	return s.GetBorderForStyle().BottomLeft
}

// GetWindowBorderBottomRight returns the appropriate bottom-right border character
func (s *Settings) GetWindowBorderBottomRight() string {
	return s.GetBorderForStyle().BottomRight
}

// GetWindowBorderTop returns the appropriate top border character
func (s *Settings) GetWindowBorderTop() string {
	return s.GetBorderForStyle().Top
}

// GetWindowBorderBottom returns the appropriate bottom border character
func (s *Settings) GetWindowBorderBottom() string {
	return s.GetBorderForStyle().Bottom
}

// GetWindowBorderLeft returns the appropriate left border character
func (s *Settings) GetWindowBorderLeft() string {
	return s.GetBorderForStyle().Left
}

// The window controls resolve in two steps: a one-cell mark, which is what a
// glyph set names and what a user means by "the close button", and the padded
// button the title bar draws.
//
// Splitting them is what makes a set safe here. The press rectangles below are
// fixed offsets from the border's trailing corner, measured against buttons of
// exactly three and four cells, so a set able to name the padded string could
// move a button out from under the pointer with one two-cell glyph. Naming the
// mark leaves the padding to the renderer and the widths cannot drift.

// GetWindowButtonCloseMark returns the one-cell close mark.
func (s *Settings) GetWindowButtonCloseMark() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.Close },
		WindowButtonCloseMark, WindowButtonCloseMarkASCII)
}

// GetWindowButtonMaximizeMark returns the one-cell maximize mark.
func (s *Settings) GetWindowButtonMaximizeMark() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.Maximize },
		WindowButtonMaximizeMark, WindowButtonMaximizeMarkASCII)
}

// GetWindowButtonMinimizeMark returns the one-cell minimize mark. It has no
// separate ASCII form because the mark is already 7-bit, so --ascii-only needs
// nothing different here.
func (s *Settings) GetWindowButtonMinimizeMark() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.Minimize },
		WindowButtonMinimizeMark, WindowButtonMinimizeMark)
}

// GetWindowButtonClose returns the three-cell close button.
func (s *Settings) GetWindowButtonClose() string { return " " + s.GetWindowButtonCloseMark() + " " }

// GetWindowButtonMaximize returns the three-cell maximize button.
func (s *Settings) GetWindowButtonMaximize() string {
	return " " + s.GetWindowButtonMaximizeMark() + " "
}

// GetWindowButtonMinimize returns the four-cell minimize button, which leads the
// pill and so carries the extra cell of lead-in.
func (s *Settings) GetWindowButtonMinimize() string {
	return "  " + s.GetWindowButtonMinimizeMark() + " "
}

// GetWindowButtonDot returns the appropriate dots-style disc character
func (s *Settings) GetWindowButtonDot() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.Dot }, WindowButtonDot, WindowButtonDotASCII)
}

// GetWindowPillLeft returns the appropriate pill left character
func (s *Settings) GetWindowPillLeft() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.PillLeft }, WindowPillLeft, WindowPillLeftASCII)
}

// GetWindowPillRight returns the appropriate pill right character
func (s *Settings) GetWindowPillRight() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.PillRight }, WindowPillRight, WindowPillRightASCII)
}

// GetWindowSeparatorChar returns the appropriate separator character
func (s *Settings) GetWindowSeparatorChar() string {
	return s.glyphOr(func(g *theme.GlyphSet) string { return g.Rule }, WindowSeparatorChar, WindowSeparatorCharASCII)
}

// =============================================================================
// Button Positions (relative offsets)
// =============================================================================

const (
	// MinimizeButtonLeftNonTiling is the left position offset for minimize button in non-tiling mode.
	MinimizeButtonLeftNonTiling = -11
	// MinimizeButtonRightNonTiling is the right position offset for minimize button in non-tiling mode.
	MinimizeButtonRightNonTiling = -9
	// MaximizeButtonLeft is the left position offset for maximize button.
	MaximizeButtonLeft = -8
	// MaximizeButtonRight is the right position offset for maximize button.
	MaximizeButtonRight = -6

	// MinimizeButtonLeftTiling is the left position offset for minimize button in tiling mode.
	MinimizeButtonLeftTiling = -8
	// MinimizeButtonRightTiling is the right position offset for minimize button in tiling mode.
	MinimizeButtonRightTiling = -6

	// CloseButtonLeft is the left position offset for close button (same for both modes).
	CloseButtonLeft = -5
	// CloseButtonRight is the right position offset for close button (same for both modes).
	CloseButtonRight = -3
)

// =============================================================================
// Limits
// =============================================================================

const (
	// MaxLogMessages is the maximum number of log messages to keep in memory
	MaxLogMessages = 100

	// MaxWorkspaces is the maximum number of workspaces supported
	MaxWorkspaces = 9
)

// =============================================================================
// Z-Index Layers
// =============================================================================

const (
	// ZIndexSeparators is the z-index for shared border separator lines. It is
	// above every tiled window, whose Z counts up from zero, and below
	// the floating band.
	ZIndexSeparators = 500

	// ZIndexAnimating is the z-index for windows currently animating.
	ZIndexAnimating = 501

	// ZIndexFloating is where the floating band starts: a floating window is
	// drawn at ZIndexFloating plus its Z, capped at ZIndexFloatingTop. The band
	// used to start one above the separators at 998, which put a floating
	// window with Z >= 2 over the dock, the clock, the log viewer, the which-key
	// overlay and the scrollback browser: every overlay between 1000 and 1003
	// was reachable with a handful of panes open.
	ZIndexFloating = 502

	// ZIndexFloatingTop is the highest z-index a floating window can be drawn
	// at. Every overlay and the dock sit above it.
	ZIndexFloatingTop = 999

	// ZIndexHelp is the z-index for help overlay
	ZIndexHelp = 1000

	// ZIndexDock is the z-index for the dock
	ZIndexDock = 1000

	// ZIndexTime is the z-index for the time display
	ZIndexTime = 1001

	// ZIndexLogs is the z-index for log viewer overlay
	ZIndexLogs = 1001

	// ZIndexWhichKey is the z-index for which-key overlay
	ZIndexWhichKey = 1002

	// ZIndexScrollbackBrowser is the z-index for the scrollback browser overlay
	ZIndexScrollbackBrowser = 1003

	// ZIndexCommandPalette is the z-index for command palette overlay
	ZIndexCommandPalette = 1004

	// ZIndexSessionSwitcher is the z-index for session switcher overlay
	ZIndexSessionSwitcher = 1005

	// ZIndexLayoutPicker is the z-index for layout picker overlay
	ZIndexLayoutPicker = 1006

	// ZIndexOverlayBase is the base z-index for the draggable floating overlay
	// panels (settings, theme picker, palette, etc.). Each open panel is stacked
	// at this base plus its position in the click-to-raise order, so clicking a
	// panel brings it above the others.
	ZIndexOverlayBase = 1100

	// ZIndexContextMenu is the z-index for the shift+right-click context menu. It
	// sits above every floating panel because it is opened on top of whatever is
	// already on screen and is dismissed by the next click either way, so nothing
	// is served by letting another panel cover it.
	ZIndexContextMenu = 1500

	// ZIndexScreensaver is the z-index for the idle screen saver. It is above
	// everything, notifications included: the saver covers the screen, and a
	// message drawn over an animation of that same screen would only look like
	// part of the animation.
	ZIndexScreensaver = 3000

	// ZIndexNotifications is the z-index for notifications
	ZIndexNotifications = 2000
)

// =============================================================================
// Character Constants
// =============================================================================

const (
	// DEL is the delete character code
	DEL = 0x7f

	// ESC is the escape character code
	ESC = 0x1b

	// NUL is the null character code
	NUL = 0x00

	// Tab is the tab character code
	Tab = 0x09

	// Space is the space character code
	Space = ' '
)

// =============================================================================
// Helper Offsets and Counts
// =============================================================================

const (
	// MaxNameTruncateLength is the max length before truncating with ellipsis
	MaxNameTruncateLength = 12

	// EllipsisLength is the length of the ellipsis string
	EllipsisLength = 3
)

// The colours a pane paints over its own output to mark text. See
// SelectionConfig for why they are settings rather than literals.
//
// The selection is a neutral grey rather than the violet it used to be. A
// selection is not a status and should not introduce a hue the rest of the
// screen does not use: grey reads as "this is marked" against output of any
// colour, which is what every editor and browser settled on. The text keeps
// the colour the program wrote it in, so a selection over syntax-highlighted
// output stays readable as the same code.
const (
	DefaultSelectionBg = "#45475A"
	DefaultSelectionFg = ""
	// Search is amber and the match under the cursor is a brighter one. They
	// are two steps of one colour rather than two colours, because they answer
	// two parts of the same question.
	DefaultSearchBg = "#8A6D2F"
	DefaultSearchFg = "#F5E7C8"
	DefaultMatchBg  = "#E5A93D"
	DefaultMatchFg  = "#1C1B19"
	// The copy mode cursor. Cyan, which is the one hue not used by the
	// selection or the search, so the three never read as each other.
	DefaultCopyCursorBg = "#39C5CF"
	DefaultCopyCursorFg = "#08222B"
)

// The prefix repeat window, in milliseconds.
//
// After a prefix command that is worth pressing twice, the prefix stays armed
// for this long, so ctrl+b then left left left walks three columns instead of
// one. It is tmux's repeat-time and the same default: long enough to press
// again without hurrying, short enough that a key struck afterwards for any
// other reason goes where it was meant to.
//
// Zero turns it off, and every prefix command then takes its own prefix.
const (
	PrefixRepeatTimeDefault = 500
	PrefixRepeatTimeMin     = 0
	PrefixRepeatTimeMax     = 5000
)

// The copy sweep: a band of light that crosses what was copied, once.
//
// Copying is the one gesture in a terminal with no result to look at, so the
// feedback has to be put where the text was. See internal/app/copy_flash.go.
const (
	CopyFlashMsDefault = 420
	CopyFlashMsMin     = 80
	CopyFlashMsMax     = 3000
	// Empty, meaning the colour is derived from the pane's own background.
	//
	// It was a pale warm white, and one literal cannot serve both ends of the
	// theme range: that colour measures fourteen to one against a dark ground
	// and one point oh three against a light one. A value set here is still
	// honoured, which is what the setting is for.
	DefaultCopyFlashColor = ""
	// The shape the sweep takes. A diagonal falls across a paragraph, which is
	// what most copies are.
	DefaultCopyFlashStyle = "diagonal"
)

// CopyFlashStyles is every value appearance.selection.flash_style takes. It
// lives here rather than beside the shapes themselves so the option registry
// can name the set without importing the app.
const (
	CopyFlashDiagonal        = "diagonal"
	CopyFlashDiagonalReverse = "diagonal-reverse"
	CopyFlashHorizontal      = "horizontal"
	CopyFlashVertical        = "vertical"
)

var CopyFlashStyles = []string{
	CopyFlashDiagonal, CopyFlashDiagonalReverse,
	CopyFlashHorizontal, CopyFlashVertical,
}
