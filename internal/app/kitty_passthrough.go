package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func kittyPassthroughLog(format string, args ...any) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}
	f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] KITTY-PASSTHROUGH: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type KittyPassthrough struct {
	mu      sync.Mutex
	enabled bool
	hostOut *os.File

	placements    map[string]map[uint32]*PassthroughPlacement
	imageIDMap    map[string]map[uint32]uint32 // maps (windowID, guestImageID) -> hostImageID
	nextHostID    uint32
	pendingOutput []byte

	// Pending direct transmission data (for chunked transfers)
	pendingDirectData map[string]*pendingDirectTransmit // key: windowID
}

// pendingDirectTransmit holds accumulated data for chunked direct transmissions
type pendingDirectTransmit struct {
	Data        []byte
	Format      vt.KittyGraphicsFormat
	Compression vt.KittyGraphicsCompression
	Width       int
	Height      int
	ImageID     uint32
	Columns     int
	Rows        int
	SourceX     int
	SourceY     int
	SourceWidth int
	SourceHeight int
	XOffset     int
	YOffset     int
	ZIndex      int32
	Virtual     bool
	CursorMove  int
	// Position info from the first chunk (a=T command)
	WindowX        int
	WindowY        int
	WindowWidth    int
	WindowHeight   int
	ContentOffsetX int
	ContentOffsetY int
	CursorX        int
	CursorY        int
	ScrollbackLen  int
}

type PassthroughPlacement struct {
	GuestImageID uint32
	HostImageID  uint32
	PlacementID  uint32
	WindowID     string
	GuestX       int
	AbsoluteLine int  // Absolute line position (scrollbackLen + cursorY at placement time)
	HostX        int
	HostY        int
	Cols         int
	Rows         int // Original image rows (before any capping)
	DisplayRows  int // Capped rows for initial display
	Hidden       bool // True when placement is completely out of view

	// Source clipping parameters (pixels) - preserved for re-placement
	SourceX      int
	SourceY      int
	SourceWidth  int
	SourceHeight int
	XOffset      int
	YOffset      int
	ZIndex       int32
	Virtual      bool

	// Current clipping state (rows/cols to clip from each edge)
	ClipTop         int
	ClipBottom      int
	ClipLeft        int
	ClipRight       int
	MaxShowable     int // Max rows that can be shown in current viewport
	MaxShowableCols int // Max cols that can be shown in current viewport
}

type WindowPositionInfo struct {
	WindowX            int
	WindowY            int
	ContentOffsetX     int
	ContentOffsetY     int
	Width              int
	Height             int
	Visible            bool
	ScrollbackLen      int  // Total scrollback lines
	ScrollOffset       int  // Current scroll offset (0 = at bottom)
	IsBeingManipulated bool // True when window is being dragged/resized
	WindowZ            int  // Window z-index for occlusion detection
	IsAltScreen        bool // True when alternate screen is active (vim, less, etc.)
}

func NewKittyPassthrough() *KittyPassthrough {
	caps := GetHostCapabilities()
	kittyPassthroughLog("NewKittyPassthrough: KittyGraphics=%v, TerminalName=%s", caps.KittyGraphics, caps.TerminalName)
	return &KittyPassthrough{
		enabled:           caps.KittyGraphics,
		hostOut:           os.Stdout,
		placements:        make(map[string]map[uint32]*PassthroughPlacement),
		imageIDMap:        make(map[string]map[uint32]uint32),
		nextHostID:        1,
		pendingDirectData: make(map[string]*pendingDirectTransmit),
	}
}

// getOrAllocateHostID returns the host image ID for a given (windowID, guestImageID) pair.
// If no mapping exists, it allocates a new host ID and stores the mapping.
func (kp *KittyPassthrough) getOrAllocateHostID(windowID string, guestImageID uint32) uint32 {
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}
	if hostID, ok := kp.imageIDMap[windowID][guestImageID]; ok {
		return hostID
	}
	hostID := kp.allocateHostID()
	kp.imageIDMap[windowID][guestImageID] = hostID
	kittyPassthroughLog("getOrAllocateHostID: windowID=%s, guestID=%d -> hostID=%d", windowID[:8], guestImageID, hostID)
	return hostID
}

func (kp *KittyPassthrough) IsEnabled() bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	return kp.enabled
}

func (kp *KittyPassthrough) FlushPending() []byte {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if len(kp.pendingOutput) == 0 {
		return nil
	}
	out := kp.pendingOutput
	kp.pendingOutput = nil
	return out
}

func (kp *KittyPassthrough) allocateHostID() uint32 {
	id := kp.nextHostID
	kp.nextHostID++
	if kp.nextHostID == 0 {
		kp.nextHostID = 1
	}
	return id
}

// calculateImageCells calculates the number of rows and columns the image will occupy.
// Uses cmd.Rows/Columns if specified, otherwise calculates from pixel dimensions and cell size.
func (kp *KittyPassthrough) calculateImageCells(cmd *vt.KittyCommand) (rows, cols int) {
	if cmd.Rows > 0 {
		rows = cmd.Rows
	}
	if cmd.Columns > 0 {
		cols = cmd.Columns
	}

	// If rows/cols not specified, calculate from image dimensions
	if rows == 0 || cols == 0 {
		caps := GetHostCapabilities()
		kittyPassthroughLog("calculateImageCells: imgPixels=(%d,%d), cmdRC=(%d,%d), cellSize=(%d,%d)",
			cmd.Width, cmd.Height, cmd.Columns, cmd.Rows, caps.CellWidth, caps.CellHeight)
		if caps.CellWidth > 0 && caps.CellHeight > 0 {
			if rows == 0 && cmd.Height > 0 {
				rows = (cmd.Height + caps.CellHeight - 1) / caps.CellHeight
			}
			if cols == 0 && cmd.Width > 0 {
				cols = (cmd.Width + caps.CellWidth - 1) / caps.CellWidth
			}
		}
	}

	kittyPassthroughLog("calculateImageCells: result rows=%d, cols=%d", rows, cols)
	return rows, cols
}

// PlacementResult contains info about an image placement for cursor positioning
type PlacementResult struct {
	Rows       int // Number of rows the image occupies
	Cols       int // Number of columns the image occupies
	CursorMove int // C parameter: 0=move cursor (default), 1=don't move
}

func (kp *KittyPassthrough) ForwardCommand(
	cmd *vt.KittyCommand,
	rawData []byte,
	windowID string,
	windowX, windowY int,
	windowWidth, windowHeight int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	ptyInput func([]byte),
) *PlacementResult {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kittyPassthroughLog("ForwardCommand: action=%c, enabled=%v, imageID=%d, windowID=%s, win=(%d,%d), size=(%d,%d), cursor=(%d,%d), scrollback=%d",
		cmd.Action, kp.enabled, cmd.ImageID, windowID[:8], windowX, windowY, windowWidth, windowHeight, cursorX, cursorY, scrollbackLen)

	if !kp.enabled {
		kittyPassthroughLog("ForwardCommand: DISABLED, returning early")
		return nil
	}

	switch cmd.Action {
	case vt.KittyActionQuery:
		kittyPassthroughLog("ForwardCommand: handling QUERY")
		kp.forwardQuery(cmd, rawData, ptyInput)

	case vt.KittyActionTransmit:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT")
		result := kp.forwardTransmit(cmd, rawData, windowID, false, 0, 0, 0, 0, 0, 0, 0, 0, 0)
		if result != nil {
			return result
		}

	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		result := kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		if !cmd.More && !isFileBased {
			kp.forwardPlace(cmd, windowID, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		}
		// Return PlacementResult from direct transmit if available
		if result != nil {
			return result
		}
		// Return ORIGINAL image dimensions for whitespace reservation (file-based)
		// The terminal needs to reserve space for the full image so it can scroll properly
		if !cmd.More && isFileBased {
			imgRows, imgCols := kp.calculateImageCells(cmd)
			if imgRows > 0 || imgCols > 0 {
				return &PlacementResult{Rows: imgRows, Cols: imgCols, CursorMove: cmd.CursorMove}
			}
		}

	case vt.KittyActionPlace:
		kittyPassthroughLog("ForwardCommand: handling PLACE")
		kp.forwardPlace(cmd, windowID, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		// Return ORIGINAL image dimensions for whitespace reservation
		imgRows, imgCols := kp.calculateImageCells(cmd)
		if imgRows > 0 || imgCols > 0 {
			return &PlacementResult{Rows: imgRows, Cols: imgCols, CursorMove: cmd.CursorMove}
		}

	case vt.KittyActionDelete:
		kittyPassthroughLog("ForwardCommand: handling DELETE, d=%c, imageID=%d", cmd.Delete, cmd.ImageID)
		kp.forwardDelete(cmd, windowID)

	default:
		kittyPassthroughLog("ForwardCommand: UNKNOWN action %c", cmd.Action)
	}

	return nil
}

func (kp *KittyPassthrough) forwardQuery(cmd *vt.KittyCommand, _ []byte, ptyInput func([]byte)) {
	if ptyInput != nil && cmd.Quiet < 2 {
		response := vt.BuildKittyResponse(true, cmd.ImageID, "")
		ptyInput(response)
	}
}

func (kp *KittyPassthrough) forwardTransmit(cmd *vt.KittyCommand, rawData []byte, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int) *PlacementResult {
	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile {
		kp.forwardFileTransmit(cmd, windowID, andPlace, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		return nil
	}

	// Check if we have pending data from a previous transmit+place command
	// Chafa sends first chunk as a=T,m=1 but continuation chunks as a=t
	// We need to continue accumulating if there's pending data
	hasPendingData := kp.pendingDirectData[windowID] != nil

	// For transmit-only (no placement) AND no pending accumulation, pass through raw data
	if !andPlace && !hasPendingData {
		kp.pendingOutput = append(kp.pendingOutput, rawData...)
		return nil
	}

	// Handle direct data transmission with placement - accumulate chunks and track placements
	// Also handle continuation of a pending transmit+place
	return kp.forwardDirectTransmit(cmd, windowID, andPlace || hasPendingData, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
}

func (kp *KittyPassthrough) forwardDirectTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int) *PlacementResult {
	// Get or create pending data buffer for this window
	pending := kp.pendingDirectData[windowID]
	if pending == nil {
		pending = &pendingDirectTransmit{
			Format:         cmd.Format,
			Compression:   cmd.Compression,
			Width:         cmd.Width,
			Height:        cmd.Height,
			ImageID:       cmd.ImageID,
			Columns:       cmd.Columns,
			Rows:          cmd.Rows,
			SourceX:       cmd.SourceX,
			SourceY:       cmd.SourceY,
			SourceWidth:   cmd.SourceWidth,
			SourceHeight:  cmd.SourceHeight,
			XOffset:       cmd.XOffset,
			YOffset:       cmd.YOffset,
			ZIndex:        cmd.ZIndex,
			Virtual:       cmd.Virtual,
			CursorMove:    cmd.CursorMove,
			// Store position info from the first chunk (a=T command has valid positions)
			WindowX:        windowX,
			WindowY:        windowY,
			WindowWidth:    windowWidth,
			WindowHeight:   windowHeight,
			ContentOffsetX: contentOffsetX,
			ContentOffsetY: contentOffsetY,
			CursorX:        cursorX,
			CursorY:        cursorY,
			ScrollbackLen:  scrollbackLen,
		}
		kp.pendingDirectData[windowID] = pending
	}

	// Accumulate data
	pending.Data = append(pending.Data, cmd.Data...)

	kittyPassthroughLog("forwardDirectTransmit: accumulated %d bytes, total=%d, more=%v, andPlace=%v, storedPos=(%d,%d)",
		len(cmd.Data), len(pending.Data), cmd.More, andPlace, pending.WindowX, pending.WindowY)

	// If more chunks coming, wait
	if cmd.More {
		return nil
	}

	// Final chunk - process the complete image
	defer func() {
		delete(kp.pendingDirectData, windowID)
	}()

	if len(pending.Data) == 0 {
		kittyPassthroughLog("forwardDirectTransmit: no data accumulated, skipping")
		return nil
	}

	// Clear existing placements for this window before placing new image
	if andPlace {
		if existing := kp.placements[windowID]; existing != nil {
			for _, p := range existing {
				kp.deleteOnePlacement(p)
			}
			kp.placements[windowID] = nil
			kittyPassthroughLog("forwardDirectTransmit: cleared %d existing placements", len(existing))
		}
	}

	// Allocate a unique host ID
	hostID := kp.allocateHostID()
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}
	kp.imageIDMap[windowID][pending.ImageID] = hostID
	kittyPassthroughLog("forwardDirectTransmit: mapped guestID=%d -> hostID=%d for window=%s", pending.ImageID, hostID, windowID[:8])

	// Data is already base64 encoded from the guest
	encoded := base64.StdEncoding.EncodeToString(pending.Data)

	// Use stored position info from the first chunk (not the zeros from continuation chunks)
	hostX := pending.WindowX + pending.ContentOffsetX + pending.CursorX
	hostY := pending.WindowY + pending.ContentOffsetY + pending.CursorY

	// Calculate content area dimensions
	contentWidth := pending.WindowWidth - 2
	contentHeight := pending.WindowHeight - 2

	// Calculate image dimensions in cells
	imgRows := pending.Rows
	imgCols := pending.Columns
	if imgRows == 0 || imgCols == 0 {
		caps := GetHostCapabilities()
		if caps.CellWidth > 0 && caps.CellHeight > 0 {
			if imgRows == 0 && pending.Height > 0 {
				imgRows = (pending.Height + caps.CellHeight - 1) / caps.CellHeight
			}
			if imgCols == 0 && pending.Width > 0 {
				imgCols = (pending.Width + caps.CellWidth - 1) / caps.CellWidth
			}
		}
	}

	// Cap to content area
	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	kittyPassthroughLog("forwardDirectTransmit: hostID=%d, hostPos=(%d,%d), imgSize=(%d,%d), displaySize=(%d,%d)",
		hostID, hostX, hostY, imgCols, imgRows, displayCols, displayRows)

	// Don't place initially - just transmit the image data
	// RefreshAllPlacements will handle the actual placement at the correct position
	// This avoids placing at wrong position when cursor moves during chunk accumulation

	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		chunk := encoded[i:end]
		more := end < len(encoded)

		var buf bytes.Buffer
		buf.WriteString("\x1b_G")

		if i == 0 {
			// Always use 't' (transmit only) - placement will be done by RefreshAllPlacements
			buf.WriteString(fmt.Sprintf("a=t,i=%d,f=%d,s=%d,v=%d,q=2",
				hostID, pending.Format, pending.Width, pending.Height))
			if pending.Compression == vt.KittyCompressionZlib {
				buf.WriteString(",o=z")
			}
			if displayCols > 0 {
				buf.WriteString(fmt.Sprintf(",c=%d", displayCols))
			}
			if displayRows > 0 {
				buf.WriteString(fmt.Sprintf(",r=%d", displayRows))
			}
			if pending.SourceX > 0 {
				buf.WriteString(fmt.Sprintf(",x=%d", pending.SourceX))
			}
			if pending.SourceY > 0 {
				buf.WriteString(fmt.Sprintf(",y=%d", pending.SourceY))
			}
			if pending.SourceWidth > 0 {
				buf.WriteString(fmt.Sprintf(",w=%d", pending.SourceWidth))
			}
			if pending.SourceHeight > 0 {
				buf.WriteString(fmt.Sprintf(",h=%d", pending.SourceHeight))
			}
			if pending.XOffset > 0 {
				buf.WriteString(fmt.Sprintf(",X=%d", pending.XOffset))
			}
			if pending.YOffset > 0 {
				buf.WriteString(fmt.Sprintf(",Y=%d", pending.YOffset))
			}
			if pending.ZIndex != 0 {
				buf.WriteString(fmt.Sprintf(",z=%d", pending.ZIndex))
			}
			if pending.Virtual {
				buf.WriteString(",U=1")
			}
		} else {
			buf.WriteString(fmt.Sprintf("i=%d,q=2", hostID))
		}

		if more {
			buf.WriteString(",m=1")
		}

		buf.WriteByte(';')
		buf.WriteString(chunk)
		buf.WriteString("\x1b\\")

		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	// Store placement for tracking (placement will be done by RefreshAllPlacements)
	if andPlace {
		if kp.placements[windowID] == nil {
			kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
		}
		kp.placements[windowID][hostID] = &PassthroughPlacement{
			GuestImageID: pending.ImageID,
			HostImageID:  hostID,
			WindowID:     windowID,
			GuestX:       pending.CursorX,
			AbsoluteLine: pending.ScrollbackLen + pending.CursorY,
			HostX:        hostX,
			HostY:        hostY,
			Cols:         displayCols,
			Rows:         imgRows,
			DisplayRows:  displayRows,
			SourceX:      pending.SourceX,
			SourceY:      pending.SourceY,
			SourceWidth:  pending.SourceWidth,
			SourceHeight: pending.SourceHeight,
			XOffset:      pending.XOffset,
			YOffset:      pending.YOffset,
			ZIndex:       pending.ZIndex,
			Virtual:      pending.Virtual,
			Hidden:       true, // Start hidden, RefreshAllPlacements will place it
		}
		kittyPassthroughLog("forwardDirectTransmit: stored placement hostID=%d (hidden, waiting for refresh)", hostID)

		// Return PlacementResult for whitespace reservation
		return &PlacementResult{
			Rows:       imgRows,
			Cols:       imgCols,
			CursorMove: pending.CursorMove,
		}
	}

	return nil
}

func (kp *KittyPassthrough) forwardFileTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int) {
	if cmd.FilePath == "" {
		return
	}

	filePath := cmd.FilePath
	if cmd.Medium == vt.KittyMediumSharedMemory {
		filePath = "/dev/shm/" + cmd.FilePath
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		kittyPassthroughLog("forwardFileTransmit: failed to read %s: %v", filePath, err)
		return
	}

	kittyPassthroughLog("forwardFileTransmit: read %d bytes from %s, andPlace=%v", len(data), filePath, andPlace)

	// Clear existing placements for this window before placing new image
	// This prevents old images from lingering when icat is called multiple times
	if andPlace {
		if existing := kp.placements[windowID]; existing != nil {
			for _, p := range existing {
				kp.deleteOnePlacement(p)
			}
			kp.placements[windowID] = nil
			kittyPassthroughLog("forwardFileTransmit: cleared %d existing placements", len(existing))
		}
	}

	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile {
		_ = os.Remove(filePath)
	}

	// Allocate a unique host ID and store the mapping
	hostID := kp.allocateHostID()
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}
	kp.imageIDMap[windowID][cmd.ImageID] = hostID
	kittyPassthroughLog("forwardFileTransmit: mapped guestID=%d -> hostID=%d for window=%s", cmd.ImageID, hostID, windowID[:8])

	encoded := base64.StdEncoding.EncodeToString(data)

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	// Calculate content area dimensions (accounting for borders)
	contentWidth := windowWidth - 2  // -2 for left/right borders
	contentHeight := windowHeight - 2 // -2 for top/bottom borders

	// Calculate image dimensions in cells
	// Note: calculateImageCells returns (rows, cols) in that order
	imgRows, imgCols := kp.calculateImageCells(cmd)

	// Cap to content area (not cursor position) - allow full-height images
	// The image will be repositioned by RefreshAllPlacements after scrolling
	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	kittyPassthroughLog("forwardFileTransmit: hostID=%d, hostPos=(%d,%d), imgSize=(%d,%d), displaySize=(%d,%d), contentArea=(%d,%d)",
		hostID, hostX, hostY, imgCols, imgRows, displayCols, displayRows, contentWidth, contentHeight)

	// Don't place initially - just transmit the image data
	// RefreshAllPlacements will handle the actual placement at the correct position
	// This avoids ANSI leaks when icat is called multiple times

	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		chunk := encoded[i:end]
		more := end < len(encoded)

		var buf bytes.Buffer
		buf.WriteString("\x1b_G")

		if i == 0 {
			// Always use 't' (transmit only) - placement will be done by RefreshAllPlacements
			buf.WriteString(fmt.Sprintf("a=t,i=%d,f=%d,s=%d,v=%d,q=2",
				hostID, cmd.Format, cmd.Width, cmd.Height))
			if cmd.Compression == vt.KittyCompressionZlib {
				buf.WriteString(",o=z")
			}

			// Always set c and r to control display size
			if displayCols > 0 {
				buf.WriteString(fmt.Sprintf(",c=%d", displayCols))
			}
			if displayRows > 0 {
				buf.WriteString(fmt.Sprintf(",r=%d", displayRows))
			}
			// Include source rectangle if specified
			if cmd.SourceX > 0 {
				buf.WriteString(fmt.Sprintf(",x=%d", cmd.SourceX))
			}
			if cmd.SourceY > 0 {
				buf.WriteString(fmt.Sprintf(",y=%d", cmd.SourceY))
			}
			if cmd.SourceWidth > 0 {
				buf.WriteString(fmt.Sprintf(",w=%d", cmd.SourceWidth))
			}
			// Calculate source height for clipping (not scaling)
			// If displayRows < imgRows, we need to clip the image
			sourceHeight := cmd.SourceHeight
			if sourceHeight == 0 && displayRows < imgRows {
				caps := GetHostCapabilities()
				cellH := caps.CellHeight
				if cellH <= 0 {
					cellH = 20
				}
				sourceHeight = displayRows * cellH
			}
			if sourceHeight > 0 {
				buf.WriteString(fmt.Sprintf(",h=%d", sourceHeight))
			}
			if cmd.XOffset > 0 {
				buf.WriteString(fmt.Sprintf(",X=%d", cmd.XOffset))
			}
			if cmd.YOffset > 0 {
				buf.WriteString(fmt.Sprintf(",Y=%d", cmd.YOffset))
			}
			if cmd.ZIndex != 0 {
				buf.WriteString(fmt.Sprintf(",z=%d", cmd.ZIndex))
			}
			if cmd.Virtual {
				buf.WriteString(",U=1")
			}
		} else {
			buf.WriteString(fmt.Sprintf("i=%d,q=2", hostID))
		}

		if more {
			buf.WriteString(",m=1")
		}

		buf.WriteByte(';')
		buf.WriteString(chunk)
		buf.WriteString("\x1b\\")

		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	// Store placement using hostID as key (cmd.ImageID is often 0 for new images)
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	kp.placements[windowID][hostID] = &PassthroughPlacement{
		GuestImageID: cmd.ImageID,
		HostImageID:  hostID,
		WindowID:     windowID,
		GuestX:       cursorX,
		AbsoluteLine: scrollbackLen + cursorY,
		HostX:        hostX,
		HostY:        hostY,
		Cols:         displayCols,
		Rows:         imgRows,     // Original image rows (for scroll clipping)
		DisplayRows:  displayRows, // Capped rows for initial display
		SourceX:      cmd.SourceX,
		SourceY:      cmd.SourceY,
		SourceWidth:  cmd.SourceWidth,
		SourceHeight: cmd.SourceHeight,
		XOffset:      cmd.XOffset,
		YOffset:      cmd.YOffset,
		ZIndex:       cmd.ZIndex,
		Virtual:      cmd.Virtual,
		Hidden:       true, // Start hidden, RefreshAllPlacements will place it
	}
	kittyPassthroughLog("forwardFileTransmit: stored placement hostID=%d (hidden, waiting for refresh)", hostID)
}

func (kp *KittyPassthrough) forwardPlace(
	cmd *vt.KittyCommand,
	windowID string,
	windowX, windowY int,
	windowWidth, windowHeight int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
) {
	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	// Get or allocate a unique host ID for this (window, guestImageID) pair
	// This prevents conflicts when multiple windows use the same guest image ID
	hostID := kp.getOrAllocateHostID(windowID, cmd.ImageID)

	// Calculate content area dimensions (accounting for borders)
	contentWidth := windowWidth - 2
	contentHeight := windowHeight - 2

	// Calculate image dimensions and cap to content area
	// Note: calculateImageCells returns (rows, cols) in that order
	imgRows, imgCols := kp.calculateImageCells(cmd)
	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	var buf bytes.Buffer
	buf.WriteString("\x1b7") // Save cursor position
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1))
	buf.WriteString("\x1b_G")
	buf.WriteString(fmt.Sprintf("a=p,i=%d", hostID))

	if cmd.PlacementID > 0 {
		buf.WriteString(fmt.Sprintf(",p=%d", cmd.PlacementID))
	}
	// Always set display dimensions to control size
	if displayCols > 0 {
		buf.WriteString(fmt.Sprintf(",c=%d", displayCols))
	}
	if displayRows > 0 {
		buf.WriteString(fmt.Sprintf(",r=%d", displayRows))
	}
	if cmd.XOffset > 0 {
		buf.WriteString(fmt.Sprintf(",X=%d", cmd.XOffset))
	}
	if cmd.YOffset > 0 {
		buf.WriteString(fmt.Sprintf(",Y=%d", cmd.YOffset))
	}
	if cmd.SourceX > 0 {
		buf.WriteString(fmt.Sprintf(",x=%d", cmd.SourceX))
	}
	if cmd.SourceY > 0 {
		buf.WriteString(fmt.Sprintf(",y=%d", cmd.SourceY))
	}
	if cmd.SourceWidth > 0 {
		buf.WriteString(fmt.Sprintf(",w=%d", cmd.SourceWidth))
	}
	if cmd.SourceHeight > 0 {
		buf.WriteString(fmt.Sprintf(",h=%d", cmd.SourceHeight))
	}
	if cmd.ZIndex != 0 {
		buf.WriteString(fmt.Sprintf(",z=%d", cmd.ZIndex))
	}
	if cmd.Virtual {
		buf.WriteString(",U=1")
	}
	buf.WriteString(",q=2")
	buf.WriteString("\x1b\\")
	buf.WriteString("\x1b8") // Restore cursor position

	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)

	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}

	// Store placement with both original and capped dimensions
	placement := &PassthroughPlacement{
		GuestImageID: cmd.ImageID,
		HostImageID:  hostID,
		PlacementID:  cmd.PlacementID,
		WindowID:     windowID,
		GuestX:       cursorX,
		AbsoluteLine: scrollbackLen + cursorY,
		HostX:        hostX,
		HostY:        hostY,
		Cols:         displayCols,
		Rows:         imgRows,     // Original image rows
		DisplayRows:  displayRows, // Capped for initial display
		SourceX:      cmd.SourceX,
		SourceY:      cmd.SourceY,
		SourceWidth:  cmd.SourceWidth,
		SourceHeight: cmd.SourceHeight,
		XOffset:      cmd.XOffset,
		YOffset:      cmd.YOffset,
		ZIndex:       cmd.ZIndex,
		Virtual:      cmd.Virtual,
	}
	kp.placements[windowID][cmd.ImageID] = placement
}

func (kp *KittyPassthrough) forwardDelete(cmd *vt.KittyCommand, windowID string) {
	switch cmd.Delete {
	case vt.KittyDeleteAll, 0:
		// Delete all images for this window
		placements := kp.placements[windowID]
		for _, p := range placements {
			var buf bytes.Buffer
			buf.WriteString("\x1b_G")
			buf.WriteString(fmt.Sprintf("a=d,d=i,i=%d,q=2", p.HostImageID))
			buf.WriteString("\x1b\\")
			kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
		}
		kp.placements[windowID] = nil

	case vt.KittyDeleteByID:
		// Translate guest image ID to host image ID
		if placements := kp.placements[windowID]; placements != nil {
			if p, ok := placements[cmd.ImageID]; ok {
				var buf bytes.Buffer
				buf.WriteString("\x1b_G")
				buf.WriteString(fmt.Sprintf("a=d,d=i,i=%d,q=2", p.HostImageID))
				buf.WriteString("\x1b\\")
				kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
				delete(placements, cmd.ImageID)
			}
		}

	case vt.KittyDeleteByIDAndPlacement:
		// Translate guest image ID to host image ID
		if placements := kp.placements[windowID]; placements != nil {
			if p, ok := placements[cmd.ImageID]; ok {
				var buf bytes.Buffer
				buf.WriteString("\x1b_G")
				buf.WriteString(fmt.Sprintf("a=d,d=I,i=%d", p.HostImageID))
				if cmd.PlacementID > 0 {
					buf.WriteString(fmt.Sprintf(",p=%d", cmd.PlacementID))
				}
				buf.WriteString(",q=2\x1b\\")
				kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
				delete(placements, cmd.ImageID)
			}
		}
	}
}

func (kp *KittyPassthrough) OnWindowMove(windowID string, newX, newY, contentOffsetX, contentOffsetY int, scrollbackLen, scrollOffset, viewportHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	viewportTop := scrollbackLen - scrollOffset

	for _, p := range placements {
		if !p.Hidden {
			kp.deleteOnePlacement(p)
		}

		relativeY := p.AbsoluteLine - viewportTop
		p.HostX = newX + contentOffsetX + p.GuestX
		p.HostY = newY + contentOffsetY + relativeY

		// Check if in viewport
		if relativeY >= 0 && relativeY < viewportHeight {
			kp.placeOne(p)
			p.Hidden = false
		} else {
			p.Hidden = true
		}
	}
}

func (kp *KittyPassthrough) OnWindowClose(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	delete(kp.placements, windowID)
	delete(kp.imageIDMap, windowID)
}

func (kp *KittyPassthrough) OnWindowScroll(windowID string, windowX, windowY, contentOffsetX, contentOffsetY, scrollbackLen, scrollOffset, viewportHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	viewportTop := scrollbackLen - scrollOffset

	for _, p := range placements {
		if !p.Hidden {
			kp.deleteOnePlacement(p)
		}

		relativeY := p.AbsoluteLine - viewportTop
		p.HostX = windowX + contentOffsetX + p.GuestX
		p.HostY = windowY + contentOffsetY + relativeY

		// Check if in viewport
		if relativeY >= 0 && relativeY < viewportHeight {
			kp.placeOne(p)
			p.Hidden = false
		} else {
			p.Hidden = true
		}
	}
}

func (kp *KittyPassthrough) ClearWindow(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kittyPassthroughLog("ClearWindow called for windowID=%s, enabled=%v", windowID[:8], kp.enabled)

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	kittyPassthroughLog("ClearWindow: found %d placements to clear", len(placements))
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	kp.placements[windowID] = nil
}

// rectsOverlap checks if two rectangles overlap
func rectsOverlap(x1, y1, w1, h1, x2, y2, w2, h2 int) bool {
	return x1 < x2+w2 && x1+w1 > x2 && y1 < y2+h2 && y1+h1 > y2
}

// isOccludedByHigherWindow checks if an image region is fully occluded by a window with higher z-index
func (kp *KittyPassthrough) isOccludedByHigherWindow(
	screenX, screenY, width, height, windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) bool {
	for id, info := range allWindows {
		if id == excludeWindowID || info.WindowZ <= windowZ {
			continue
		}
		// Check if higher-z window overlaps the image region
		if rectsOverlap(screenX, screenY, width, height,
			info.WindowX, info.WindowY, info.Width, info.Height) {
			return true
		}
	}
	return false
}

func (kp *KittyPassthrough) RefreshAllPlacements(getAllWindows func() map[string]*WindowPositionInfo) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	// Get all windows upfront for occlusion detection
	allWindows := getAllWindows()

	// First pass: check if ANY window is being manipulated
	anyBeingManipulated := false
	for windowID := range kp.placements {
		if info := allWindows[windowID]; info != nil && info.IsBeingManipulated {
			anyBeingManipulated = true
			break
		}
	}

	// If any window is being dragged/resized, hide ALL images to prevent ANSI leaks
	if anyBeingManipulated {
		for _, placements := range kp.placements {
			for _, p := range placements {
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
			}
		}
		return
	}

	for windowID, placements := range kp.placements {
		if len(placements) == 0 {
			continue
		}

		info := allWindows[windowID]
		if info == nil {
			for _, p := range placements {
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
			}
			delete(kp.placements, windowID)
			continue
		}

		// Hide all images when alternate screen is active (vim, less, etc.)
		if info.IsAltScreen {
			for _, p := range placements {
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
			}
			continue
		}

		// Calculate viewport dimensions (accounting for window borders)
		viewportTop := info.ScrollbackLen - info.ScrollOffset
		viewportHeight := info.Height - 2
		viewportWidth := info.Width - 2

		for _, p := range placements {
			// Calculate new position (where top-left of image would be)
			relativeY := p.AbsoluteLine - viewportTop

			// Calculate where the FULL image would end (for visibility check)
			fullImageBottom := relativeY + p.Rows
			fullImageRight := p.GuestX + p.Cols

			// Check if ANY part of the image is visible in the viewport
			// Image is visible if: top < viewportHeight AND bottom > 0 AND left < viewportWidth AND right > 0
			anyPartVisible := info.Visible &&
				relativeY < viewportHeight && fullImageBottom > 0 &&
				p.GuestX < viewportWidth && fullImageRight > 0

			// Calculate vertical clipping based on FULL image dimensions
			clipTop := 0
			clipBottom := 0
			if anyPartVisible {
				if relativeY < 0 {
					clipTop = -relativeY // Clip rows above viewport
				}
				if fullImageBottom > viewportHeight {
					clipBottom = fullImageBottom - viewportHeight // Clip rows below viewport
				}
			}

			// For horizontal: if image is wider than viewport, hide it (simpler approach for now)
			// TODO: implement proper horizontal clipping later
			if fullImageRight > viewportWidth {
				anyPartVisible = false
			}

			// Calculate how many rows we CAN show after clipping
			maxShowableRows := min(p.Rows-clipTop-clipBottom, viewportHeight)
			if maxShowableRows <= 0 {
				maxShowableRows = 1
			}

			// Calculate actual host position (after clipping adjustment)
			actualRelativeY := relativeY
			if clipTop > 0 {
				actualRelativeY = 0 // Start at top of viewport
			}
			newHostX := info.WindowX + info.ContentOffsetX + p.GuestX
			newHostY := info.WindowY + info.ContentOffsetY + actualRelativeY

			// Calculate image dimensions in cells for occlusion check (same units as window dimensions)
			imageCellWidth := p.Cols
			imageCellHeight := maxShowableRows

			// Check if image is occluded by a higher-z window
			if anyPartVisible && kp.isOccludedByHigherWindow(
				newHostX, newHostY, imageCellWidth, imageCellHeight,
				info.WindowZ, allWindows, windowID,
			) {
				kittyPassthroughLog("RefreshPlacement: image occluded by higher-z window, hiding")
				anyPartVisible = false
			}

			// Hide images when host position would be negative or outside screen bounds
			// This prevents issues when windows are partially off-screen
			if anyPartVisible && (newHostX < 0 || newHostY < 0) {
				kittyPassthroughLog("RefreshPlacement: image at negative position (%d,%d), hiding", newHostX, newHostY)
				anyPartVisible = false
			}

			// Also hide if the window itself is partially off the left or top edge
			if anyPartVisible && (info.WindowX < 0 || info.WindowY < 0) {
				kittyPassthroughLog("RefreshPlacement: window at negative position (%d,%d), hiding image", info.WindowX, info.WindowY)
				anyPartVisible = false
			}

			kittyPassthroughLog("RefreshPlacement: relY=%d, origRows=%d, origCols=%d, vpH=%d, vpW=%d, clipTop=%d, clipBot=%d, maxRows=%d, visible=%v",
				relativeY, p.Rows, p.Cols, viewportHeight, viewportWidth, clipTop, clipBottom, maxShowableRows, anyPartVisible)

			if !anyPartVisible {
				// Completely hidden
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
			} else {
				// Always re-place visible images to ensure correct rendering
				// This fixes issues where one window's image operations affect another
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
				p.HostX = newHostX
				p.HostY = newHostY
				p.ClipTop = clipTop
				p.ClipBottom = clipBottom
				p.MaxShowable = maxShowableRows
				kp.placeOne(p)
				p.Hidden = false
			}
		}
	}
}

func (kp *KittyPassthrough) HasPlacements() bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, placements := range kp.placements {
		if len(placements) > 0 {
			return true
		}
	}
	return false
}

// deleteOnePlacement removes the image and all its placements from graphics memory.
func (kp *KittyPassthrough) deleteOnePlacement(p *PassthroughPlacement) {
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")
	buf.WriteString(fmt.Sprintf("a=d,d=i,i=%d", p.HostImageID))
	if p.PlacementID > 0 {
		buf.WriteString(fmt.Sprintf(",p=%d", p.PlacementID))
	}
	buf.WriteString(",q=2\x1b\\")
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func (kp *KittyPassthrough) placeOne(p *PassthroughPlacement) {
	caps := GetHostCapabilities()
	cellHeight := caps.CellHeight
	if cellHeight <= 0 {
		cellHeight = 20 // Fallback
	}

	var buf bytes.Buffer
	buf.WriteString("\x1b7") // Save cursor position
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", p.HostY+1, p.HostX+1))
	buf.WriteString("\x1b_G")
	buf.WriteString(fmt.Sprintf("a=p,i=%d", p.HostImageID))
	if p.PlacementID > 0 {
		buf.WriteString(fmt.Sprintf(",p=%d", p.PlacementID))
	}

	// MaxShowable is already calculated as: p.Rows - clipTop - clipBottom
	// So it already accounts for clipping and is the number of rows to display
	visibleRows := p.MaxShowable
	if visibleRows <= 0 {
		visibleRows = p.DisplayRows
	}
	if visibleRows <= 0 {
		visibleRows = p.Rows
	}
	if visibleRows <= 0 {
		visibleRows = 1 // Minimum 1 row to avoid issues
	}

	kittyPassthroughLog("placeOne: hostID=%d, pos=(%d,%d), origRows=%d, origCols=%d, clipTop=%d, clipBot=%d, visibleRows=%d",
		p.HostImageID, p.HostX, p.HostY, p.Rows, p.Cols, p.ClipTop, p.ClipBottom, visibleRows)

	// Use original cols (no horizontal clipping for now)
	if p.Cols > 0 {
		buf.WriteString(fmt.Sprintf(",c=%d", p.Cols))
	}
	if visibleRows > 0 {
		buf.WriteString(fmt.Sprintf(",r=%d", visibleRows))
	}

	// Calculate source Y offset (in pixels) - includes original SourceY plus clipping
	sourceY := p.SourceY
	if p.ClipTop > 0 {
		sourceY += p.ClipTop * cellHeight
	}

	// Calculate source height (in pixels) for proper vertical clipping
	// This is critical: without setting h, Kitty will SCALE the image to fit r rows
	// With h set, Kitty will CLIP to show only h pixels of height
	sourceHeight := visibleRows * cellHeight

	// Include source clipping parameters
	if p.SourceX > 0 {
		buf.WriteString(fmt.Sprintf(",x=%d", p.SourceX))
	}
	if sourceY > 0 {
		buf.WriteString(fmt.Sprintf(",y=%d", sourceY))
	}
	// Use original source width if specified (no horizontal clipping for now)
	if p.SourceWidth > 0 {
		buf.WriteString(fmt.Sprintf(",w=%d", p.SourceWidth))
	}
	// Always set h for vertical clipping
	if sourceHeight > 0 {
		buf.WriteString(fmt.Sprintf(",h=%d", sourceHeight))
	}
	if p.XOffset > 0 {
		buf.WriteString(fmt.Sprintf(",X=%d", p.XOffset))
	}
	if p.YOffset > 0 {
		buf.WriteString(fmt.Sprintf(",Y=%d", p.YOffset))
	}
	if p.ZIndex != 0 {
		buf.WriteString(fmt.Sprintf(",z=%d", p.ZIndex))
	}
	if p.Virtual {
		buf.WriteString(",U=1")
	}
	buf.WriteString(",q=2\x1b\\")
	buf.WriteString("\x1b8") // Restore cursor position
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

var _ = strconv.Itoa

func (m *OS) setupKittyPassthrough(window *terminal.Window) {
	if m.KittyPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}

	win := window
	kp := m.KittyPassthrough

	// Set up callback for when placements are cleared (e.g., clear screen, ED sequences)
	window.Terminal.KittyState().SetClearCallback(func() {
		kp.ClearWindow(win.ID)
	})

	window.Terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cursorPos := win.Terminal.CursorPosition()
		scrollbackLen := win.Terminal.ScrollbackLen()
		result := kp.ForwardCommand(
			cmd, rawData, win.ID,
			win.X, win.Y,
			win.Width, win.Height,
			1, 1,
			cursorPos.X, cursorPos.Y,
			scrollbackLen,
			func(response []byte) {
				if win.Pty != nil {
					_, _ = win.Pty.Write(response)
				} else if win.DaemonWriteFunc != nil {
					_ = win.DaemonWriteFunc(response)
				}
			},
		)
		// Reserve space in guest terminal for the image placement
		// Only move cursor when C=0 (default behavior), not when C=1 (no cursor move)
		if result != nil && result.Rows > 0 && result.CursorMove == 0 {
			win.Terminal.ReserveImageSpace(result.Rows, result.Cols)
		}
	})
}
