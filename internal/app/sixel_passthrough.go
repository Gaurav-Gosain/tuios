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

// SixelPassthroughOptions configures a SixelPassthrough instance.
type SixelPassthroughOptions struct {
	// ForceEnable skips capability detection (for web mode).
	ForceEnable bool
	// Output is the writer for sixel output. If nil, uses os.Stdout.
	Output *os.File
}

// NewSixelPassthrough creates a new SixelPassthrough using auto-detected
// capabilities and os.Stdout for output.
func NewSixelPassthrough() *SixelPassthrough {
	return NewSixelPassthroughWithOptions(SixelPassthroughOptions{})
}

// NewSixelPassthroughWithOptions creates a new SixelPassthrough with custom
// options. Use this in web mode to pass the sip session's PtySlave() so
// sixel bytes flow through the same PTY as the browser's text output.
func NewSixelPassthroughWithOptions(opts SixelPassthroughOptions) *SixelPassthrough {
	caps := GetHostCapabilities()
	enabled := caps.SixelGraphics || opts.ForceEnable
	sixelPassthroughLog("NewSixelPassthrough: SixelGraphics=%v Force=%v TerminalName=%s", caps.SixelGraphics, opts.ForceEnable, caps.TerminalName)
	hostOut := opts.Output
	if hostOut == nil {
		hostOut = os.Stdout
	}
	return &SixelPassthrough{
		enabled:    enabled,
		hostOut:    hostOut,
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

	// Check for existing placement at the same position with same dimensions
	// (shell redraws can re-emit the same sixel)
	for _, existing := range sp.placements[windowID] {
		if existing.AbsoluteLine == absLine && existing.GuestX == cursorX &&
			existing.Width == cmd.Width && existing.Height == cmd.Height {
			// Update in place
			existing.RawSequence = cmd.RawSequence
			existing.IsPlaced = false // Force re-render
			sixelPassthroughLog("ForwardCommand: updated existing placement at absLine=%d", absLine)
			return
		}
	}

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

		// During window manipulation (drag/resize), hide only this window's images
		if info.IsBeingManipulated {
			for _, p := range placements {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
			}
			continue
		}

		// Calculate viewport boundaries using content height (exclude borders)
		contentHeight := info.Height - 2*info.ContentOffsetY
		if contentHeight <= 0 {
			contentHeight = info.Height
		}
		viewportTop := 0
		if info.ScrollbackLen > contentHeight {
			viewportTop = info.ScrollbackLen - info.ScrollOffset - contentHeight
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

			// When not scrolled back, also consider images that extend beyond
			// current scrollback (the scrollback may not have caught up with
			// ReserveImageSpace yet)
			if !anyPartVisible && info.ScrollOffset == 0 && p.AbsoluteLine >= viewportTop {
				anyPartVisible = true
			}

			if !anyPartVisible {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Calculate host position
			relativeY := max(0, p.AbsoluteLine-viewportTop)

			hostX := info.WindowX + info.ContentOffsetX + p.GuestX
			hostY := info.WindowY + info.ContentOffsetY + relativeY

			// Window content area bounds (in host coordinates)
			windowContentBottom := info.WindowY + info.Height - info.ContentOffsetY

			// Hide if image extends past window content bottom
			// (sixel can't be pixel-cropped without palette re-quantization)
			if hostY+p.Rows > windowContentBottom {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Hide if image extends past screen bottom (causes scroll feedback)
			if hostY+p.Rows >= hostHeight-1 {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Hide if top is clipped (scrolled partially out of view)
			if p.AbsoluteLine < viewportTop {
				if !p.Hidden {
					sp.hidePlacement(p)
				}
				continue
			}

			// Check if position changed - only re-render if needed
			positionChanged := !p.IsPlaced ||
				p.PlacedAtX != hostX || p.PlacedAtY != hostY

			// Update placement state
			p.HostX = hostX
			p.HostY = hostY

			// Only place the sixel image if position changed
			if positionChanged {
				sixelPassthroughLog("placeSixel: rendering at (%d,%d) imgSize=%dx%d rows=%d",
					hostX, hostY, p.Width, p.Height, p.Rows)
				sp.placeSixel(p, cellWidth, cellHeight)
				p.PlacedAtX = hostX
				p.PlacedAtY = hostY
				p.IsPlaced = true
			}
			p.Hidden = false
		}
	}
}

// hidePlacement hides a sixel placement by overwriting the image area with
// spaces. Unlike Kitty graphics, sixel has no delete command, so we must
// actively clear the area.
func (sp *SixelPassthrough) hidePlacement(p *SixelPassthroughPlacement) {
	if p.IsPlaced && p.Rows > 0 {
		// Clear the image area by writing spaces over it
		var buf []byte
		buf = append(buf, "\x1b7"...) // Save cursor
		for row := range p.Rows {
			buf = append(buf, fmt.Sprintf("\x1b[%d;%dH", p.PlacedAtY+row+1, p.PlacedAtX+1)...)
			buf = append(buf, fmt.Sprintf("\x1b[%dX", p.Cols)...) // Erase N characters
		}
		buf = append(buf, "\x1b8"...) // Restore cursor
		sp.pendingOutput = append(sp.pendingOutput, buf...)
	}
	p.Hidden = true
	p.IsPlaced = false
}

// placeSixel writes a sixel image to the host terminal at the specified position.
// The raw sixel data is passed through without re-encoding to preserve the
// original palette and image quality. Clipping is handled by hiding images
// that don't fit within window boundaries.
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

	// Write the DCS sixel sequence with raw data passthrough
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

// HideAllPlacements hides all sixel placements and queues clear commands.
func (sp *SixelPassthrough) HideAllPlacements() {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	for _, placements := range sp.placements {
		for _, p := range placements {
			if !p.Hidden {
				sp.hidePlacement(p)
			}
		}
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

	win := window
	var lastSixelLen int
	var lastSixelTime time.Time
	window.Terminal.SetSixelPassthroughFunc(func(cmd *vt.SixelCommand, cursorX, cursorY, absLine int) {
		if !m.SixelPassthrough.IsEnabled() || len(cmd.RawSequence) == 0 {
			return
		}

		// Deduplicate: skip if same-sized sixel arrives within 1 second
		// (shell prompt redraws can re-trigger the DCS handler)
		now := time.Now()
		if len(cmd.RawSequence) == lastSixelLen && now.Sub(lastSixelTime) < time.Second {
			return
		}
		lastSixelLen = len(cmd.RawSequence)
		lastSixelTime = now

		// Get fresh cell dimensions (may change on resize)
		caps := GetHostCapabilities()
		cw := caps.CellWidth
		ch := caps.CellHeight
		if cw == 0 {
			cw = 9
		}
		if ch == 0 {
			ch = 20
		}

		sixelPassthroughLog("CALLBACK: rawLen=%d cursorX=%d cursorY=%d absLine=%d winX=%d winY=%d winW=%d winH=%d cell=%dx%d",
			len(cmd.RawSequence), cursorX, cursorY, absLine, win.X, win.Y, win.Width, win.Height, cw, ch)

		// Route through the placement system for proper position tracking and clipping
		m.SixelPassthrough.ForwardCommand(
			win.ID,
			cmd,
			cursorX, cursorY, absLine,
			win.IsAltScreen,
			cw, ch,
		)
	})

	sixelPassthroughLog("setupSixelPassthrough: configured for window %s", window.ID[:min(8, len(window.ID))])
}
