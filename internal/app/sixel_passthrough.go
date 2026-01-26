package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func sixelPassthroughLog(format string, args ...any) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}
	f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] SIXEL-PASSTHROUGH: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

// SixelPassthrough handles forwarding sixel graphics to the host terminal.
// Unlike Kitty graphics, sixel images don't have IDs - they're placed inline
// at the cursor position and scroll with text.
type SixelPassthrough struct {
	mu      sync.Mutex
	enabled bool
	hostOut *os.File

	// Placements per window
	placements map[string][]*SixelPassthroughPlacement

	// Pending sixel output to be written
	pendingOutput []byte
}

// SixelPassthroughPlacement represents a sixel image placed in a guest window.
type SixelPassthroughPlacement struct {
	WindowID     string
	AbsoluteLine int // Absolute line in scrollback where image starts
	GuestX       int // Column position in guest terminal
	GuestY       int // Row position in guest terminal (at placement time)

	// Image dimensions
	Width  int // Pixel width
	Height int // Pixel height
	Rows   int // Number of terminal rows
	Cols   int // Number of terminal columns

	// Host terminal position (calculated during refresh)
	HostX int
	HostY int

	// Visibility state
	Hidden bool

	// Track if currently placed and at what position (to avoid re-rendering every frame)
	PlacedAtX int
	PlacedAtY int
	IsPlaced  bool

	// Clipping state
	ClipTop    int
	ClipBottom int
	ClipLeft   int
	ClipRight  int

	// The raw sixel data for re-rendering
	RawSequence []byte

	// Track which screen the image was placed on
	PlacedOnAltScreen bool

	// Sixel parameters
	AspectRatio    int
	BackgroundMode int
}

// NewSixelPassthrough creates a new SixelPassthrough.
func NewSixelPassthrough() *SixelPassthrough {
	caps := GetHostCapabilities()
	sixelPassthroughLog("NewSixelPassthrough: SixelGraphics=%v, TerminalName=%s", caps.SixelGraphics, caps.TerminalName)
	// TODO: Sixel passthrough is disabled - needs proper sixel raster clipping
	// to work correctly in a windowed environment.
	return &SixelPassthrough{
		enabled:    false,
		hostOut:    os.Stdout,
		placements: make(map[string][]*SixelPassthroughPlacement),
	}
}

// IsEnabled returns whether sixel passthrough is enabled.
func (sp *SixelPassthrough) IsEnabled() bool {
	return sp.enabled
}

// ForwardCommand handles a sixel command from a guest terminal.
// It stores the placement for later rendering during RefreshAllPlacements.
func (sp *SixelPassthrough) ForwardCommand(
	windowID string,
	cmd *vt.SixelCommand,
	cursorX, cursorY, absLine int,
	isAltScreen bool,
	cellWidth, cellHeight int,
) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if !sp.enabled {
		return
	}

	sixelPassthroughLog("ForwardCommand: windowID=%s, pos=(%d,%d), absLine=%d, size=%dx%d",
		windowID[:min(8, len(windowID))], cursorX, cursorY, absLine, cmd.Width, cmd.Height)

	// Calculate rows and columns
	rows := cmd.RowsForHeight(cellHeight)
	cols := cmd.ColsForWidth(cellWidth)

	placement := &SixelPassthroughPlacement{
		WindowID:          windowID,
		AbsoluteLine:      absLine,
		GuestX:            cursorX,
		GuestY:            cursorY,
		Width:             cmd.Width,
		Height:            cmd.Height,
		Rows:              rows,
		Cols:              cols,
		Hidden:            true, // Start hidden, RefreshAllPlacements will determine visibility
		RawSequence:       cmd.RawSequence,
		PlacedOnAltScreen: isAltScreen,
		AspectRatio:       cmd.AspectRatio,
		BackgroundMode:    cmd.BackgroundMode,
	}

	sp.placements[windowID] = append(sp.placements[windowID], placement)
}

// ClearWindow removes all placements for a window.
func (sp *SixelPassthrough) ClearWindow(windowID string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	delete(sp.placements, windowID)
	sixelPassthroughLog("ClearWindow: windowID=%s", windowID[:min(8, len(windowID))])
}

// ClearAltScreenPlacements removes placements that were made on the alt screen.
// Called when transitioning from alt screen to normal screen.
func (sp *SixelPassthrough) ClearAltScreenPlacements(windowID string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	placements := sp.placements[windowID]
	if len(placements) == 0 {
		return
	}

	var remaining []*SixelPassthroughPlacement
	for _, p := range placements {
		if !p.PlacedOnAltScreen {
			remaining = append(remaining, p)
		}
	}

	sp.placements[windowID] = remaining
	sixelPassthroughLog("ClearAltScreenPlacements: windowID=%s, removed=%d",
		windowID[:min(8, len(windowID))], len(placements)-len(remaining))
}

// RefreshAllPlacements updates visibility and positions for all placements.
// This is called during each render cycle.
func (sp *SixelPassthrough) RefreshAllPlacements(getWindowInfo func(windowID string) *WindowPositionInfo) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if !sp.enabled {
		sixelPassthroughLog("RefreshAllPlacements: sixel disabled")
		return
	}

	caps := GetHostCapabilities()
	cellWidth := caps.CellWidth
	cellHeight := caps.CellHeight

	if cellWidth == 0 {
		cellWidth = 9
	}
	if cellHeight == 0 {
		cellHeight = 20
	}

	hostHeight := caps.Rows
	hostWidth := caps.Cols

	for windowID, placements := range sp.placements {
		info := getWindowInfo(windowID)
		if info == nil {
			for _, p := range placements {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
			}
			continue
		}
		if !info.Visible {
			for _, p := range placements {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
			}
			continue
		}

		sixelPassthroughLog("Window %s: pos=(%d,%d) size=%dx%d scrollback=%d offset=%d",
			windowID[:min(8, len(windowID))], info.WindowX, info.WindowY, info.Width, info.Height,
			info.ScrollbackLen, info.ScrollOffset)

		// During window manipulation (drag/resize), hide all images
		if info.IsBeingManipulated {
			for _, p := range placements {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
			}
			continue
		}

		// Calculate viewport boundaries
		// When scrollbackLen < height, content starts at top of window (viewportTop = 0)
		viewportTop := 0
		if info.ScrollbackLen > info.Height {
			viewportTop = info.ScrollbackLen - info.ScrollOffset - info.Height
		}
		viewportBottom := info.ScrollbackLen - info.ScrollOffset

		for _, p := range placements {
			// Check if placement matches current screen mode
			if p.PlacedOnAltScreen != info.IsAltScreen {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Calculate visibility
			placementBottom := p.AbsoluteLine + p.Rows

			// Check if any part is visible
			anyPartVisible := placementBottom > viewportTop && p.AbsoluteLine < viewportBottom

			if !anyPartVisible {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Calculate clipping
			clipTop := 0
			clipBottom := 0

			if p.AbsoluteLine < viewportTop {
				clipTop = viewportTop - p.AbsoluteLine
			}
			if placementBottom > viewportBottom {
				clipBottom = placementBottom - viewportBottom
			}

			visibleRows := p.Rows - clipTop - clipBottom
			if visibleRows <= 0 {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Calculate host position
			relativeY := max(0, p.AbsoluteLine-viewportTop)

			hostX := info.WindowX + info.ContentOffsetX + p.GuestX
			hostY := info.WindowY + info.ContentOffsetY + relativeY

			sixelPassthroughLog("Placement: absLine=%d viewportTop=%d relativeY=%d guestX=%d -> hostPos=(%d,%d)",
				p.AbsoluteLine, viewportTop, relativeY, p.GuestX, hostX, hostY)

			// Check horizontal bounds
			if hostX < 0 || hostX >= hostWidth {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Check if we're within host terminal bounds
			if hostY < 0 || hostY >= hostHeight {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Check if position changed - only re-render if needed
			positionChanged := !p.IsPlaced || p.PlacedAtX != hostX || p.PlacedAtY != hostY

			// Update placement state
			p.HostX = hostX
			p.HostY = hostY
			p.ClipTop = clipTop
			p.ClipBottom = clipBottom

			// Only place the sixel image if position changed or not yet placed
			if positionChanged {
				sp.placeSixel(p, cellWidth, cellHeight)
				p.PlacedAtX = hostX
				p.PlacedAtY = hostY
				p.IsPlaced = true
			}
			p.Hidden = false
		}
	}
}

// hidePlacement hides a sixel placement.
// Since sixels don't have delete commands like Kitty, we rely on
// the terminal to naturally overwrite the area or redraw.
func (sp *SixelPassthrough) hidePlacement(p *SixelPassthroughPlacement) {
	p.Hidden = true
	p.IsPlaced = false // Force re-render when it becomes visible again
	// Sixel doesn't have a delete command - the area will be
	// naturally cleared when the terminal redraws
}

// placeSixel writes a sixel image to the host terminal at the specified position.
// cellWidth and cellHeight are provided for potential future clipping support.
func (sp *SixelPassthrough) placeSixel(p *SixelPassthroughPlacement, _, _ int) {
	if len(p.RawSequence) == 0 {
		return
	}

	// Build the sixel output
	var buf []byte

	// Save cursor position
	buf = append(buf, "\x1b7"...)

	// Move to target position (1-indexed)
	buf = append(buf, fmt.Sprintf("\x1b[%d;%dH", p.HostY+1, p.HostX+1)...)

	// Write the DCS sixel sequence
	// Format: ESC P <params> q <data> ESC \
	buf = append(buf, "\x1bP"...)
	buf = append(buf, p.RawSequence...)
	buf = append(buf, "\x1b\\"...)

	// Restore cursor position
	buf = append(buf, "\x1b8"...)

	sp.pendingOutput = append(sp.pendingOutput, buf...)

}

// FlushOutput writes any pending output to the host terminal.
func (sp *SixelPassthrough) FlushOutput() {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if len(sp.pendingOutput) > 0 {
		_, _ = sp.hostOut.Write(sp.pendingOutput)
		sp.pendingOutput = sp.pendingOutput[:0]
	}
}

// FlushPending returns pending sixel output and clears the buffer.
func (sp *SixelPassthrough) FlushPending() []byte {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if len(sp.pendingOutput) == 0 {
		return nil
	}

	result := make([]byte, len(sp.pendingOutput))
	copy(result, sp.pendingOutput)
	sp.pendingOutput = sp.pendingOutput[:0]
	return result
}

// GetSixelGraphicsCmd returns pending sixel output and clears the buffer.
func (sp *SixelPassthrough) GetSixelGraphicsCmd() string {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if len(sp.pendingOutput) == 0 {
		return ""
	}

	result := string(sp.pendingOutput)
	sp.pendingOutput = sp.pendingOutput[:0]
	return result
}

// PlacementCount returns the total number of placements across all windows.
func (sp *SixelPassthrough) PlacementCount() int {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	count := 0
	for _, placements := range sp.placements {
		count += len(placements)
	}
	return count
}

// ClearScrolledOut removes placements that have scrolled past a certain line.
func (sp *SixelPassthrough) ClearScrolledOut(windowID string, minLine int) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	placements := sp.placements[windowID]
	if len(placements) == 0 {
		return
	}

	var remaining []*SixelPassthroughPlacement
	for _, p := range placements {
		if p.AbsoluteLine+p.Rows > minLine {
			remaining = append(remaining, p)
		}
	}

	sp.placements[windowID] = remaining
}

// setupSixelPassthrough configures sixel passthrough for a window.
func (m *OS) setupSixelPassthrough(window *terminal.Window) {
	if m.SixelPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}

	caps := GetHostCapabilities()
	cellWidth := caps.CellWidth
	cellHeight := caps.CellHeight

	if cellWidth == 0 {
		cellWidth = 9
	}
	if cellHeight == 0 {
		cellHeight = 20
	}

	window.Terminal.SetSixelPassthroughFunc(func(cmd *vt.SixelCommand, cursorX, cursorY, absLine int) {
		isAltScreen := window.Terminal.IsAltScreen()
		m.SixelPassthrough.ForwardCommand(
			window.ID,
			cmd,
			cursorX, cursorY, absLine,
			isAltScreen,
			cellWidth, cellHeight,
		)
	})

	sixelPassthroughLog("setupSixelPassthrough: configured for window %s", window.ID[:min(8, len(window.ID))])
}
