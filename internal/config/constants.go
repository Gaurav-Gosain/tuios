package config

import "time"

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

	// MinWindowHeight is the minimum height a window can be resized to
	MinWindowHeight = 3
)

// =============================================================================
// Animation Durations
// =============================================================================

const (
	// DefaultAnimationDuration is the standard animation duration for minimize/restore operations
	DefaultAnimationDuration = 300 * time.Millisecond

	// FastAnimationDuration is the duration for snapping and window swapping animations
	FastAnimationDuration = 200 * time.Millisecond

	// NotificationFadeOutDuration is the fade out duration for notifications
	NotificationFadeOutDuration = 500 * time.Millisecond

	// NotificationDuration is the default duration notifications remain visible
	NotificationDuration = 1500 * time.Millisecond
)

// =============================================================================
// Timeouts and Intervals
// =============================================================================

const (
	// PrefixCommandTimeout is the timeout for prefix command mode
	PrefixCommandTimeout = 2 * time.Second

	// CPUUpdateInterval is the interval between CPU usage updates
	CPUUpdateInterval = 500 * time.Millisecond

	// ProcessWaitDelay is the delay when waiting for process cleanup
	ProcessWaitDelay = 50 * time.Millisecond

	// WhichKeyDelay is the delay before showing which-key style overlay
	WhichKeyDelay = 500 * time.Millisecond

	// ProcessShutdownTimeout is the timeout for graceful process shutdown
	ProcessShutdownTimeout = 500 * time.Millisecond
)

// =============================================================================
// FPS and Refresh Rates
// =============================================================================

const (
	// NormalFPS is the normal refresh rate during regular operation
	NormalFPS = 60

	// InteractionFPS is the refresh rate during user interactions (drag/resize)
	// Lower FPS during interactions improves mouse responsiveness
	InteractionFPS = 30

	// BackgroundWindowUpdateCycle is the number of update cycles to skip for background windows
	BackgroundWindowUpdateCycle = 3
)

// =============================================================================
// UI Layout Dimensions
// =============================================================================

const (
	// DockHeight is the height of the dock area at the bottom
	DockHeight = 2

	// StatusBarLeftWidth is the width of the left section of status bar
	StatusBarLeftWidth = 30

	// LogViewerWidth is the width of the log viewer overlay
	LogViewerWidth = 80

	// CPUGraphWidth is the width of the CPU graph including label
	CPUGraphWidth = 19

	// CPUGraphBars is the number of bars in the CPU graph
	CPUGraphBars = 10

	// CPUGraphScale is the scale factor for CPU graph bars (100/8 blocks)
	CPUGraphScale = 12.5

	// LeftInfoWidth is the width of the left info area in dock
	LeftInfoWidth = 30

	// RightInfoWidth is the width of the right info area in dock
	RightInfoWidth = 20

	// DockItemWidth is the base width of a dock item
	DockItemWidth = 6

	// MaxNotificationWidth is the maximum width of notification messages
	MaxNotificationWidth = 60

	// MinNotificationWidth is the minimum width of notification messages
	MinNotificationWidth = 20

	// NotificationMargin is the margin from screen edge for notifications
	NotificationMargin = 8

	// NotificationSpacing is the vertical spacing between notifications
	NotificationSpacing = 4

	// MaxVisibleNotifications is the maximum number of notifications shown at once
	MaxVisibleNotifications = 3

	// AnimationMargin is the margin for culling animated windows
	AnimationMargin = 20

	// VisibilityMargin is the margin for culling static windows
	VisibilityMargin = 5

	// MaxNameLengthDock is the maximum length of window name in dock
	MaxNameLengthDock = 12

	// MinimizedDockWindowSize is the size of minimized window visual
	MinimizedDockWidth  = 5
	MinimizedDockHeight = 3
)

// =============================================================================
// Dock Visual Characters - Nerd Font Icons (Default)
// =============================================================================

const (
	// DockPillLeftChar is the left character for pill-style indicators
	// Set to "" to disable, or use custom characters
	DockPillLeftChar = string(rune(0xe0b6)) // Powerline left semicircle

	// DockPillRightChar is the right character for pill-style indicators
	// Set to "" to disable, or use custom characters
	DockPillRightChar = string(rune(0xe0b4)) // Powerline right semicircle

	// DockModeIconWindow is the icon for window mode (Nerd Font: nf-fa-window_restore)
	DockModeIconWindow = " " + string(rune(0xf2d2)) + " " //

	// DockModeIconTerminal is the icon for terminal mode (Nerd Font: nf-fa-terminal)
	DockModeIconTerminal = " " + string(rune(0xf120)) + " " //

	// DockModeIconTiling is the icon for tiling mode (Nerd Font: nf-fa-th - 3x3 grid)
	DockModeIconTiling = " " + string(rune(0xf00a)) + " " //

	// DockIconTerminalCount is the icon for terminal count (Nerd Font: nf-fa-terminal)
	DockIconTerminalCount = string(rune(0xf120)) //

	// DockIconWorkspaceCount is the icon for workspace count (Nerd Font: nf-fa-th_large - 2x2 grid)
	DockIconWorkspaceCount = string(rune(0xf009)) //

	// DockSeparator is the separator between dock sections
	DockSeparator = "  " // Two spaces for breathing room
)

// =============================================================================
// Dock Visual Characters - ASCII Fallback
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

	// DockSeparatorASCII is the ASCII fallback separator
	DockSeparatorASCII = " | "
)

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

// UseASCIIOnly controls whether to use ASCII fallback characters instead of Nerd Fonts
// Set via --ascii-only command-line flag
var UseASCIIOnly = false

// GetDockPillLeftChar returns the appropriate pill left character based on UseASCIIOnly
func GetDockPillLeftChar() string {
	if UseASCIIOnly {
		return DockPillLeftCharASCII
	}
	return DockPillLeftChar
}

// GetDockPillRightChar returns the appropriate pill right character based on UseASCIIOnly
func GetDockPillRightChar() string {
	if UseASCIIOnly {
		return DockPillRightCharASCII
	}
	return DockPillRightChar
}

// GetDockModeIconWindow returns the appropriate window mode icon based on UseASCIIOnly
func GetDockModeIconWindow() string {
	if UseASCIIOnly {
		return DockModeIconWindowASCII
	}
	return DockModeIconWindow
}

// GetDockModeIconTerminal returns the appropriate terminal mode icon based on UseASCIIOnly
func GetDockModeIconTerminal() string {
	if UseASCIIOnly {
		return DockModeIconTerminalASCII
	}
	return DockModeIconTerminal
}

// GetDockModeIconTiling returns the appropriate tiling mode icon based on UseASCIIOnly
func GetDockModeIconTiling() string {
	if UseASCIIOnly {
		return DockModeIconTilingASCII
	}
	return DockModeIconTiling
}

// GetDockIconTerminalCount returns the appropriate terminal count icon based on UseASCIIOnly
func GetDockIconTerminalCount() string {
	if UseASCIIOnly {
		return DockIconTerminalCountASCII
	}
	return DockIconTerminalCount
}

// GetDockIconWorkspaceCount returns the appropriate workspace count icon based on UseASCIIOnly
func GetDockIconWorkspaceCount() string {
	if UseASCIIOnly {
		return DockIconWorkspaceCountASCII
	}
	return DockIconWorkspaceCount
}

// GetDockSeparator returns the appropriate separator based on UseASCIIOnly
func GetDockSeparator() string {
	if UseASCIIOnly {
		return DockSeparatorASCII
	}
	return DockSeparator
}

// =============================================================================
// Window Decoration Characters
// =============================================================================

const (
	// Window border characters (Nerd Font / Unicode)
	WindowBorderTopLeft     = "╭" // U+256D
	WindowBorderTopRight    = "╮" // U+256E
	WindowBorderBottomLeft  = "╰" // U+2570
	WindowBorderBottomRight = "╯" // U+256F
	WindowBorderHorizontal  = "─" // U+2500
	WindowBorderVertical    = "│" // U+2502

	// Window button characters
	WindowButtonClose     = " ⤫ " // Close/kill window
	WindowPillLeft        = string(rune(0xe0b6))
	WindowPillRight       = string(rune(0xe0b4))
	WindowSeparatorChar   = "─" // U+2500
)

const (
	// Window border characters (ASCII fallback)
	WindowBorderTopLeftASCII     = "+"
	WindowBorderTopRightASCII    = "+"
	WindowBorderBottomLeftASCII  = "+"
	WindowBorderBottomRightASCII = "+"
	WindowBorderHorizontalASCII  = "-"
	WindowBorderVerticalASCII    = "|"

	// Window button characters (ASCII fallback)
	WindowButtonCloseASCII   = " X "
	WindowPillLeftASCII      = "["
	WindowPillRightASCII     = "]"
	WindowSeparatorCharASCII = "-"
)

// Window decoration getter functions

// GetWindowBorderTopLeft returns the appropriate top-left border character
func GetWindowBorderTopLeft() string {
	if UseASCIIOnly {
		return WindowBorderTopLeftASCII
	}
	return WindowBorderTopLeft
}

// GetWindowBorderTopRight returns the appropriate top-right border character
func GetWindowBorderTopRight() string {
	if UseASCIIOnly {
		return WindowBorderTopRightASCII
	}
	return WindowBorderTopRight
}

// GetWindowBorderBottomLeft returns the appropriate bottom-left border character
func GetWindowBorderBottomLeft() string {
	if UseASCIIOnly {
		return WindowBorderBottomLeftASCII
	}
	return WindowBorderBottomLeft
}

// GetWindowBorderBottomRight returns the appropriate bottom-right border character
func GetWindowBorderBottomRight() string {
	if UseASCIIOnly {
		return WindowBorderBottomRightASCII
	}
	return WindowBorderBottomRight
}

// GetWindowBorderHorizontal returns the appropriate horizontal border character
func GetWindowBorderHorizontal() string {
	if UseASCIIOnly {
		return WindowBorderHorizontalASCII
	}
	return WindowBorderHorizontal
}

// GetWindowBorderVertical returns the appropriate vertical border character
func GetWindowBorderVertical() string {
	if UseASCIIOnly {
		return WindowBorderVerticalASCII
	}
	return WindowBorderVertical
}

// GetWindowButtonClose returns the appropriate close button character
func GetWindowButtonClose() string {
	if UseASCIIOnly {
		return WindowButtonCloseASCII
	}
	return WindowButtonClose
}

// GetWindowPillLeft returns the appropriate pill left character
func GetWindowPillLeft() string {
	if UseASCIIOnly {
		return WindowPillLeftASCII
	}
	return WindowPillLeft
}

// GetWindowPillRight returns the appropriate pill right character
func GetWindowPillRight() string {
	if UseASCIIOnly {
		return WindowPillRightASCII
	}
	return WindowPillRight
}

// GetWindowSeparatorChar returns the appropriate separator character
func GetWindowSeparatorChar() string {
	if UseASCIIOnly {
		return WindowSeparatorCharASCII
	}
	return WindowSeparatorChar
}

// =============================================================================
// Button Positions (relative offsets)
// =============================================================================

const (
	// Button positions for non-tiling mode (3 buttons: minimize, maximize, close)
	MinimizeButtonLeftNonTiling  = -11
	MinimizeButtonRightNonTiling = -9
	MaximizeButtonLeft           = -8
	MaximizeButtonRight          = -6

	// Button positions for tiling mode (2 buttons: minimize, close)
	MinimizeButtonLeftTiling  = -8
	MinimizeButtonRightTiling = -6

	// Close button position (same for both modes)
	CloseButtonLeft  = -5
	CloseButtonRight = -3
)

// =============================================================================
// Buffer and Pool Sizes
// =============================================================================

const (
	// ByteSliceBufferSize is the size of byte slices in the pool
	ByteSliceBufferSize = 32 * 1024 // 32KB

	// WindowExitChannelBuffer is the buffer size for window exit channel
	WindowExitChannelBuffer = 10

	// LayerPoolInitialCapacity is the initial capacity for layer pool slices
	LayerPoolInitialCapacity = 16

	// StringBuilderInitialCapacity is estimated size for terminal content
	StringBuilderInitialCapacity = 1000 // Will be adjusted based on window size
)

// =============================================================================
// Limits
// =============================================================================

const (
	// MaxLogMessages is the maximum number of log messages to keep in memory
	MaxLogMessages = 100

	// MaxWorkspaces is the maximum number of workspaces supported
	MaxWorkspaces = 9

	// CPUHistorySize is the number of CPU usage samples to keep
	CPUHistorySize = 10

	// MaxDockItems is the maximum number of minimized windows shown in dock
	MaxDockItems = 9

	// MaxGridColumns is the maximum number of columns in window grid layout
	MaxGridColumns = 3

	// MaxTwoColumnGridWindows is the threshold for switching to 2-column grid
	MaxTwoColumnGridWindows = 6

	// MaxHelpLines is the estimated maximum number of help lines
	MaxHelpLines = 50

	// MaxSwapDistance is the threshold for directional window swapping
	MaxSwapDistance = 5
)

// =============================================================================
// Z-Index Layers
// =============================================================================

const (
	// ZIndexBase is the base z-index for regular windows
	ZIndexBase = 0

	// ZIndexAnimating is the z-index for windows currently animating
	ZIndexAnimating = 999

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

	// ZIndexNotifications is the z-index for notifications
	ZIndexNotifications = 2000
)

// =============================================================================
// Default Values
// =============================================================================

const (
	// DefaultSSHPort is the default SSH server port
	DefaultSSHPort = "2222"

	// DefaultSSHHost is the default SSH server host
	DefaultSSHHost = "localhost"

	// DefaultTerminalWidth is the fallback terminal width when screen size unknown
	DefaultTerminalWidth = 80

	// DefaultTerminalHeight is the fallback terminal height when screen size unknown
	DefaultTerminalHeight = 24

	// MinTerminalWidth is the minimum terminal width (accounting for borders)
	MinTerminalWidth = 1

	// MinTerminalHeight is the minimum terminal height (accounting for borders)
	MinTerminalHeight = 1
)

// =============================================================================
// Fractional Sizes
// =============================================================================

const (
	// HalfDivisor is used for calculating half of a dimension
	HalfDivisor = 2

	// QuarterDivisor is used for calculating quarter of a dimension
	QuarterDivisor = 4
)

// =============================================================================
// Character Constants
// =============================================================================

const (
	// CtrlB is the control code for Ctrl+B
	CtrlB = 0x02

	// DEL is the delete character code
	DEL = 0x7f

	// ESC is the escape character code
	ESC = 0x1b

	// NUL is the null character code
	NUL = 0x00

	// Tab is the tab character code
	Tab = 0x09

	// CarriageReturn is the carriage return character code
	CarriageReturn = '\r'

	// LineFeed is the line feed character code
	LineFeed = '\n'

	// Space is the space character code
	Space = ' '

	// PrintableCharMin is the minimum printable ASCII character
	PrintableCharMin = 32

	// PrintableCharMax is the maximum printable ASCII character
	PrintableCharMax = 126

	// ASCIICharMax is the maximum single-byte ASCII character
	ASCIICharMax = 127
)

// =============================================================================
// Terminal Size Adjustments
// =============================================================================

const (
	// BorderWidth is the width of window borders (2 for left and right)
	BorderWidth = 2

	// BorderHeight is the height of window borders (2 for top and bottom)
	BorderHeight = 2

	// MaxLineLength is the maximum length for display lines
	MaxLineLength = 2000
)

// =============================================================================
// Modifier Parameters (ANSI sequences)
// =============================================================================

const (
	// ModParamBase is the base value for modifier parameters
	ModParamBase = 1

	// ModParamShift is the shift key modifier parameter
	ModParamShift = 2

	// ModParamAlt is the alt key modifier parameter
	ModParamAlt = 2

	// ModParamCtrl is the ctrl key modifier parameter
	ModParamCtrl = 4
)

// =============================================================================
// VT Attribute Flags
// =============================================================================

const (
	// VTAttrBold is the bit flag for bold text
	VTAttrBold = 1

	// VTAttrFaint is the bit flag for faint text
	VTAttrFaint = 2

	// VTAttrItalic is the bit flag for italic text
	VTAttrItalic = 4

	// VTAttrReverse is the bit flag for reverse video
	VTAttrReverse = 32

	// VTAttrStrikethrough is the bit flag for strikethrough text
	VTAttrStrikethrough = 128
)

// =============================================================================
// Tiling Layout
// =============================================================================

const (
	// TilingModeEnabledWorkspaces is the number of workspaces that support tiling
	TilingModeEnabledWorkspaces = MaxWorkspaces

	// GridLayoutThreshold is the number of windows before using grid layout
	GridLayoutThreshold = 4
)

// =============================================================================
// Helper Offsets and Counts
// =============================================================================

const (
	// IDPrefixLength is the length of ID prefix used in display (8 chars from UUID)
	IDPrefixLength = 8

	// MaxNameTruncateLength is the max length before truncating with ellipsis
	MaxNameTruncateLength = 12

	// EllipsisLength is the length of the ellipsis string
	EllipsisLength = 3

	// MaxNameLengthBeforeEllipsis is max length before needing ellipsis
	MaxNameLengthBeforeEllipsis = MaxNameTruncateLength - EllipsisLength
)
