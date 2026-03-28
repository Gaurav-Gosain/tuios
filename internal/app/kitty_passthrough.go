package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
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

// isKittyResponse checks if data looks like a kitty graphics protocol response
// rather than real image data. Responses are "OK" or POSIX error names like
// "ENOENT", "EINVAL", "EBADMSG" (start with 'E' followed by uppercase).
func isKittyResponse(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if string(data) == "OK" {
		return true
	}
	// POSIX error codes: start with 'E', second char is uppercase A-Z
	return len(data) >= 2 && data[0] == 'E' && data[1] >= 'A' && data[1] <= 'Z'
}

type KittyPassthrough struct {
	mu      sync.Mutex
	enabled bool
	hostOut *os.File

	placements    map[string]map[uint32]*PassthroughPlacement
	imageIDMap    map[string]map[uint32]uint32 // maps (windowID, guestImageID) -> hostImageID
	nextHostID    uint32
	pendingOutput []byte
	videoFrameBuf []byte // Reusable buffer for immediate video frame writes

	// Pending direct transmission data (for chunked transfers)
	pendingDirectData map[string]*pendingDirectTransmit // key: windowID

	// Screen dimensions (updated by RefreshAllPlacements)
	screenWidth  int
	screenHeight int
}

// pendingDirectTransmit holds accumulated data for chunked direct transmissions
type pendingDirectTransmit struct {
	Data         []byte
	RawPayload   string // Accumulated raw base64 payload (avoids decode→re-encode)
	Format       vt.KittyGraphicsFormat
	Compression  vt.KittyGraphicsCompression
	Width        int
	Height       int
	ImageID      uint32
	Columns      int
	Rows         int
	SourceX      int
	SourceY      int
	SourceWidth  int
	SourceHeight int
	XOffset      int
	YOffset      int
	ZIndex       int32
	Virtual      bool
	CursorMove   int
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
	IsAltScreen    bool
}

type PassthroughPlacement struct {
	GuestImageID uint32
	HostImageID  uint32
	PlacementID  uint32
	WindowID     string
	GuestX       int
	AbsoluteLine int  // Absolute line position (scrollbackLen + cursorY at placement time)
	Streaming    bool // True while chunks are still being received (don't re-place)
	HostX        int
	HostY        int
	Cols         int
	Rows         int  // Original image rows (before any capping)
	DisplayRows  int  // Capped rows for initial display
	Hidden       bool // True when placement is completely out of view
	DataDirty    bool // True when image data was re-transmitted (needs re-place for video)

	// Source clipping parameters (pixels) - preserved for re-placement
	SourceX      int
	SourceY      int
	SourceWidth  int
	SourceHeight int
	XOffset      int
	YOffset      int
	ZIndex       int32
	Virtual      bool

	// Track which screen the image was placed on
	PlacedOnAltScreen bool // True if placed while alternate screen was active

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
	ScreenWidth        int  // Host terminal width
	ScreenHeight       int  // Host terminal height
	WindowZ            int  // Window z-index for occlusion detection
	IsAltScreen        bool // True when alternate screen is active (vim, less, etc.)
}

func NewKittyPassthrough() *KittyPassthrough {
	caps := GetHostCapabilities()
	kittyPassthroughLog("NewKittyPassthrough: KittyGraphics=%v, TerminalName=%s", caps.KittyGraphics, caps.TerminalName)
	// Open /dev/tty once for the lifetime of the passthrough (avoids per-frame open/close)
	hostOut := os.Stdout
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		hostOut = tty
	}

	return &KittyPassthrough{
		enabled:           caps.KittyGraphics,
		hostOut:           hostOut,
		placements:        make(map[string]map[uint32]*PassthroughPlacement),
		imageIDMap:        make(map[string]map[uint32]uint32),
		nextHostID:        1,
		pendingDirectData: make(map[string]*pendingDirectTransmit),
	}
}

// WriteToHost writes graphics data directly to the host terminal,
// wrapped in synchronized update sequences to prevent tearing.
func (kp *KittyPassthrough) WriteToHost(data []byte) {
	if kp.hostOut != nil && len(data) > 0 {
		_, _ = kp.hostOut.Write(syncBegin)
		_, _ = kp.hostOut.Write(data)
		_, _ = kp.hostOut.Write(syncEnd)
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

// Synchronized output mode 2026 (supported by Kitty, Ghostty, WezTerm, etc.)
// This prevents screen tearing by telling the terminal to buffer output
// until the end sequence is received.
var (
	syncBegin = []byte("\x1b[?2026h") // Begin Synchronized Update
	syncEnd   = []byte("\x1b[?2026l") // End Synchronized Update
)

// flushToHost writes any pending output immediately to the host terminal,
// wrapped in synchronized update sequences to prevent tearing/flickering.
// Must be called while kp.mu is already held.
func (kp *KittyPassthrough) flushToHost() {
	if len(kp.pendingOutput) > 0 && kp.hostOut != nil {
		_, _ = kp.hostOut.Write(syncBegin)
		_, _ = kp.hostOut.Write(kp.pendingOutput)
		_, _ = kp.hostOut.Write(syncEnd)
		kp.pendingOutput = kp.pendingOutput[:0]
	}
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
	isAltScreen bool,
	ptyInput func([]byte),
) *PlacementResult {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kittyPassthroughLog("ForwardCommand: action=%c, enabled=%v, imageID=%d, windowID=%s, win=(%d,%d), size=(%d,%d), cursor=(%d,%d), scrollback=%d, altScreen=%v",
		cmd.Action, kp.enabled, cmd.ImageID, windowID[:8], windowX, windowY, windowWidth, windowHeight, cursorX, cursorY, scrollbackLen, isAltScreen)

	// Detect and discard echoed responses to prevent feedback loops.
	// Responses have format "i=N;OK" or "i=N;ERROR_MSG" or just "OK"/"ERROR_MSG"
	// When parsed, they appear as transmit commands with Data="OK" or error message.
	// Real transmit commands have binary/base64 image data, not status strings.
	if cmd.Action == vt.KittyActionTransmit && len(cmd.Data) > 0 && isKittyResponse(cmd.Data) {
		kittyPassthroughLog("ForwardCommand: DISCARDING echoed response: %q", cmd.Data)
		return nil
	}

	if !kp.enabled {
		kittyPassthroughLog("ForwardCommand: DISABLED, returning early")
		return nil
	}

	// Clear virtual placements on any new image activity for this window
	// Virtual placements are inherently transient - they should be re-sent by the app if still needed
	if placements := kp.placements[windowID]; placements != nil {
		var virtualIDs []uint32
		for hostID, p := range placements {
			if p.Virtual {
				virtualIDs = append(virtualIDs, hostID)
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
			}
		}
		for _, id := range virtualIDs {
			delete(placements, id)
			kittyPassthroughLog("ForwardCommand: cleared stale virtual placement hostID=%d", id)
		}
	}

	switch cmd.Action {
	case vt.KittyActionQuery:
		kittyPassthroughLog("ForwardCommand: handling QUERY")
		kp.forwardQuery(cmd, rawData, ptyInput)

	case vt.KittyActionTransmit:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT, more=%v", cmd.More)
		result := kp.forwardTransmit(cmd, rawData, windowID, false, 0, 0, 0, 0, 0, 0, 0, 0, 0, isAltScreen)
		if result != nil {
			return result
		}

	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		result := kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Return PlacementResult from direct transmit if available
		// Don't call forwardPlace since forwardDirectTransmit already handled placement
		if result != nil {
			return result
		}
		// For file-based transmissions, forwardFileTransmit handles placement
		// Return ORIGINAL image dimensions for whitespace reservation
		if !cmd.More && isFileBased {
			imgRows, imgCols := kp.calculateImageCells(cmd)
			if imgRows > 0 || imgCols > 0 {
				return &PlacementResult{Rows: imgRows, Cols: imgCols, CursorMove: cmd.CursorMove}
			}
		}

	case vt.KittyActionPlace:
		kittyPassthroughLog("ForwardCommand: handling PLACE")
		kp.forwardPlace(cmd, windowID, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Return ORIGINAL image dimensions for whitespace reservation
		imgRows, imgCols := kp.calculateImageCells(cmd)
		if imgRows > 0 || imgCols > 0 {
			return &PlacementResult{Rows: imgRows, Cols: imgCols, CursorMove: cmd.CursorMove}
		}

	case vt.KittyActionDelete:
		kittyPassthroughLog("ForwardCommand: handling DELETE, d=%c, imageID=%d", cmd.Delete, cmd.ImageID)
		kp.forwardDelete(cmd, windowID)

	case vt.KittyActionFrame, vt.KittyActionAnimation, vt.KittyActionCompose:
		// Animation protocol (a=f, a=a, a=c) is not yet supported in passthrough.
		// These commands require consistent image ID management between the guest
		// app and host terminal which conflicts with tuios's ID remapping.
		// Apps like kitty-doom that use animation should be run directly in the
		// terminal instead of inside tuios.
		kittyPassthroughLog("ForwardCommand: DROPPING unsupported animation action=%c", cmd.Action)

	default:
		kittyPassthroughLog("ForwardCommand: UNKNOWN action %c", cmd.Action)
	}

	return nil
}

func (kp *KittyPassthrough) forwardQuery(cmd *vt.KittyCommand, _ []byte, ptyInput func([]byte)) {
	if ptyInput != nil && cmd.Quiet < 2 {
		response := vt.BuildKittyResponse(true, cmd.ImageID, "")
		kittyPassthroughLog("forwardQuery: sending response for imageID=%d, response=%q, ptyInput=%v", cmd.ImageID, response, ptyInput != nil)
		ptyInput(response)
	} else {
		kittyPassthroughLog("forwardQuery: NOT sending response, ptyInput=%v, quiet=%d", ptyInput != nil, cmd.Quiet)
	}
}

func (kp *KittyPassthrough) forwardTransmit(cmd *vt.KittyCommand, rawData []byte, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) *PlacementResult {
	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile {
		kp.forwardFileTransmit(cmd, windowID, andPlace, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Don't flush immediately — accumulate in pendingOutput.
		// Flushed during render cycle (GetKittyGraphicsCmd) so graphics
		// and text arrive in the same frame, preventing tearing.
		return nil
	}

	// rawData includes the full APC framing: \x1b_G<params>;<data>\x1b\\
	// Strip the framing to get just the inner content for rewriting.
	innerData := rawData
	if len(innerData) >= 3 && innerData[0] == '\x1b' && innerData[1] == '_' {
		innerData = innerData[2:] // skip \x1b_
		if innerData[0] == 'G' {
			innerData = innerData[1:] // skip G
		}
	}
	if len(innerData) >= 2 && innerData[len(innerData)-2] == '\x1b' && innerData[len(innerData)-1] == '\\' {
		innerData = innerData[:len(innerData)-2] // strip \x1b\\
	}

	hasPendingData := kp.pendingDirectData[windowID] != nil
	if !andPlace && !hasPendingData {
		// Pass through raw (already has framing)
		kp.pendingOutput = append(kp.pendingOutput, rawData...)
		return nil
	}

	isFirstChunk := cmd.Width > 0 && cmd.Height > 0

	if isFirstChunk {
		delete(kp.pendingDirectData, windowID)

		// Get/allocate host ID
		if kp.imageIDMap[windowID] == nil {
			kp.imageIDMap[windowID] = make(map[uint32]uint32)
		}
		hostID, reusingID := kp.imageIDMap[windowID][cmd.ImageID]
		if !reusingID {
			hostID = kp.allocateHostID()
			kp.imageIDMap[windowID][cmd.ImageID] = hostID
			kp.deleteAllWindowPlacements(windowID, false)
		}

		hostX := windowX + contentOffsetX + cursorX
		hostY := windowY + contentOffsetY + cursorY

		// Track placement for RefreshAllPlacements
		imgRows, imgCols := kp.calculateImageCells(cmd)
		contentWidth := windowWidth - 2
		contentHeight := windowHeight - 2
		if kp.placements[windowID] == nil {
			kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
		}
		kp.placements[windowID][hostID] = &PassthroughPlacement{
			GuestImageID:      cmd.ImageID,
			HostImageID:       hostID,
			WindowID:          windowID,
			GuestX:            cursorX,
			AbsoluteLine:      scrollbackLen + cursorY,
			HostX:             hostX,
			HostY:             hostY,
			Cols:              min(imgCols, contentWidth),
			Rows:              imgRows,
			DisplayRows:       min(imgRows, contentHeight),
			Hidden:            true, // Will be placed by a=p after transfer completes
				Streaming:         true, // Don't let RefreshAllPlacements touch this
			PlacedOnAltScreen: isAltScreen,
		}

		// Accumulate ALL chunks in videoFrameBuf, write to host in ONE call
		// when m=0 arrives. This prevents interleaving with bubbletea's output.
		kp.videoFrameBuf = kp.videoFrameBuf[:0]
		kp.videoFrameBuf = append(kp.videoFrameBuf, "\x1b_G"...)
		kp.videoFrameBuf = append(kp.videoFrameBuf, fmt.Sprintf("a=T,i=%d,q=2,", hostID)...)
		paramEnd := bytes.IndexByte(innerData, ';')
		if paramEnd >= 0 {
			var filteredParams []string
			for _, p := range strings.Split(string(innerData[:paramEnd]), ",") {
				if !strings.HasPrefix(p, "a=") && !strings.HasPrefix(p, "i=") && !strings.HasPrefix(p, "q=") {
					filteredParams = append(filteredParams, p)
				}
			}
			if len(filteredParams) > 0 {
				kp.videoFrameBuf = append(kp.videoFrameBuf, strings.Join(filteredParams, ",")...)
			}
			kp.videoFrameBuf = append(kp.videoFrameBuf, innerData[paramEnd:]...)
		}
		kp.videoFrameBuf = append(kp.videoFrameBuf, "\x1b\\"...)
		kp.pendingDirectData[windowID] = &pendingDirectTransmit{
			ImageID:        cmd.ImageID,
			WindowX:        windowX,
			WindowY:        windowY,
			WindowWidth:    windowWidth,
			WindowHeight:   windowHeight,
			ContentOffsetX: contentOffsetX,
			ContentOffsetY: contentOffsetY,
			CursorX:        cursorX,
			CursorY:        cursorY,
		}
	} else {
		// Continuation chunk: accumulate in videoFrameBuf
		kp.videoFrameBuf = append(kp.videoFrameBuf, "\x1b_G"...)
		if cmd.More {
			kp.videoFrameBuf = append(kp.videoFrameBuf, "m=1"...)
		}
		paramEnd := bytes.IndexByte(innerData, ';')
		if paramEnd >= 0 {
			kp.videoFrameBuf = append(kp.videoFrameBuf, innerData[paramEnd:]...)
		}
		kp.videoFrameBuf = append(kp.videoFrameBuf, "\x1b\\"...)
	}

	// When transfer completes (m=0), write ENTIRE frame to host in one call
	if !cmd.More && kp.hostOut != nil {
		hostID := kp.imageIDMap[windowID][cmd.ImageID]

		// Get stored position from first chunk (m=0 arrives via KittyActionTransmit
		// with all position params as 0, so we use the saved values)
		pending := kp.pendingDirectData[windowID]
		delete(kp.pendingDirectData, windowID)

		var hostX, hostY int
		winX, winY, winW, winH := windowX, windowY, windowWidth, windowHeight
		if pending != nil {
			hostX = pending.WindowX + pending.ContentOffsetX + pending.CursorX
			hostY = pending.WindowY + pending.ContentOffsetY + pending.CursorY
			winX = pending.WindowX
			winY = pending.WindowY
			winW = pending.WindowWidth
			winH = pending.WindowHeight
		}

		contentW := winW - 2
		contentH := winH - 2
		imgCols, imgRows := 0, 0
		if placements := kp.placements[windowID]; placements != nil {
			if p := placements[hostID]; p != nil {
				imgCols = p.Cols
				imgRows = p.DisplayRows
			}
		}

		// Bounds check: window must be onscreen, image must fit in content area AND screen
		visible := winX >= 0 && winY >= 0 && hostX >= 0 && hostY >= 0
		if visible && imgCols > 0 {
			visible = hostX+imgCols <= winX+1+contentW
		}
		if visible && imgRows > 0 {
			visible = hostY+imgRows <= winY+1+contentH
		}
		if visible && kp.screenWidth > 0 && kp.screenHeight > 0 {
			if hostX+imgCols > kp.screenWidth || hostY+imgRows >= kp.screenHeight-1 {
				visible = false
			}
		}

		if visible {
			var frame []byte
			frame = append(frame, syncBegin...)
			frame = append(frame, fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1)...)
			frame = append(frame, kp.videoFrameBuf...)
			frame = append(frame, syncEnd...)
			_, _ = kp.hostOut.Write(frame)
		} else if hostID > 0 {
			// Delete the image from host when not visible to prevent ghost rendering
			var del []byte
			del = append(del, syncBegin...)
			del = append(del, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)...)
			del = append(del, syncEnd...)
			_, _ = kp.hostOut.Write(del)
		}
		kp.videoFrameBuf = kp.videoFrameBuf[:0]

		// Mark placement
		if placements := kp.placements[windowID]; placements != nil {
			if p := placements[hostID]; p != nil {
				p.Hidden = !visible
				p.Streaming = false
				p.HostX = hostX
				p.HostY = hostY
			}
		}
	}

	return nil
}

func (kp *KittyPassthrough) forwardDirectTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) *PlacementResult {
	// Get or create pending data buffer for this window
	pending := kp.pendingDirectData[windowID]

	// If a new frame starts (has Width/Height) while previous is still pending,
	// the previous frame was implicitly complete. Discard it (the new frame
	// supersedes it) and start fresh.
	if pending != nil && cmd.Width > 0 && cmd.Height > 0 {
		kittyPassthroughLog("forwardDirectTransmit: new frame supersedes pending (%d bytes), starting fresh", len(pending.Data))
		delete(kp.pendingDirectData, windowID)
		pending = nil
	}

	if pending == nil {
		pending = &pendingDirectTransmit{
			Format:       cmd.Format,
			Compression:  cmd.Compression,
			Width:        cmd.Width,
			Height:       cmd.Height,
			ImageID:      cmd.ImageID,
			Columns:      cmd.Columns,
			Rows:         cmd.Rows,
			SourceX:      cmd.SourceX,
			SourceY:      cmd.SourceY,
			SourceWidth:  cmd.SourceWidth,
			SourceHeight: cmd.SourceHeight,
			XOffset:      cmd.XOffset,
			YOffset:      cmd.YOffset,
			ZIndex:       cmd.ZIndex,
			Virtual:      cmd.Virtual,
			CursorMove:   cmd.CursorMove,
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
			IsAltScreen:    isAltScreen,
		}
		kp.pendingDirectData[windowID] = pending
	}

	// Accumulate raw base64 payload (avoids decode→re-encode cycle)
	pending.RawPayload += cmd.RawPayload
	// Also accumulate decoded data for dimension calculations
	pending.Data = append(pending.Data, cmd.Data...)

	kittyPassthroughLog("forwardDirectTransmit: accumulated %d bytes, total=%d, more=%v, andPlace=%v, storedPos=(%d,%d)",
		len(cmd.Data), len(pending.Data), cmd.More, andPlace, pending.WindowX, pending.WindowY)

	// If more chunks coming, wait for finalization.
	// Note: a new a=T with dimensions arriving while accumulating will be
	// detected at the top of this function and reset the pending state.
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

	// Note: Virtual placements (U=1) use unicode placeholder characters in the terminal content.
	// Since TUIOS renders the guest terminal content itself (not passthrough), those placeholders
	// don't exist in the host terminal. So we convert virtual placements to regular deferred
	// placements that RefreshAllPlacements will handle with proper cursor positioning.
	if pending.Virtual {
		kittyPassthroughLog("forwardDirectTransmit: virtual placement detected, converting to regular deferred placement")
	}

	// Reuse existing host ID to avoid delete+re-place flicker
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}

	hostID, reusingID := kp.imageIDMap[windowID][pending.ImageID]
	if !reusingID {
		hostID = kp.allocateHostID()
		kp.imageIDMap[windowID][pending.ImageID] = hostID
		if andPlace {
			kp.deleteAllWindowPlacements(windowID, false)
		}
	}
	kittyPassthroughLog("forwardDirectTransmit: mapped guestID=%d -> hostID=%d for window=%s", pending.ImageID, hostID, windowID[:8])

	// Use the accumulated raw base64 payload directly (no decode→re-encode cycle)
	encoded := pending.RawPayload

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

	// Use 'T' when placement is requested (including video frame updates)
	directAction := "t"
	if andPlace {
		directAction = "T"
	}

	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		chunk := encoded[i:end]
		more := end < len(encoded)

		var buf bytes.Buffer
		buf.WriteString("\x1b_G")

		if i == 0 {
			fmt.Fprintf(&buf, "a=%s,i=%d,f=%d,s=%d,v=%d,q=2",
				directAction, hostID, pending.Format, pending.Width, pending.Height)
			if pending.Compression == vt.KittyCompressionZlib {
				buf.WriteString(",o=z")
			}
			if displayCols > 0 {
				fmt.Fprintf(&buf, ",c=%d", displayCols)
			}
			if displayRows > 0 {
				fmt.Fprintf(&buf, ",r=%d", displayRows)
			}
			if pending.SourceX > 0 {
				fmt.Fprintf(&buf, ",x=%d", pending.SourceX)
			}
			if pending.SourceY > 0 {
				fmt.Fprintf(&buf, ",y=%d", pending.SourceY)
			}
			if pending.SourceWidth > 0 {
				fmt.Fprintf(&buf, ",w=%d", pending.SourceWidth)
			}
			if pending.SourceHeight > 0 {
				fmt.Fprintf(&buf, ",h=%d", pending.SourceHeight)
			}
			if pending.XOffset > 0 {
				fmt.Fprintf(&buf, ",X=%d", pending.XOffset)
			}
			if pending.YOffset > 0 {
				fmt.Fprintf(&buf, ",Y=%d", pending.YOffset)
			}
			if pending.ZIndex != 0 {
				fmt.Fprintf(&buf, ",z=%d", pending.ZIndex)
			}
			// Note: We don't send U=1 to host even for virtual placements
			// because TUIOS renders guest content itself, so placeholder
			// characters don't exist in the host terminal
		} else {
			fmt.Fprintf(&buf, "i=%d,q=2", hostID)
		}

		if more {
			buf.WriteString(",m=1")
		}

		buf.WriteByte(';')
		buf.WriteString(chunk)
		buf.WriteString("\x1b\\")

		// For video frame updates (a=T), accumulate ALL chunks then flush
		// immediately to host. For first frames (a=t), queue in pendingOutput.
		if directAction == "T" {
			if i == 0 {
				// First chunk: save cursor + position
				kp.videoFrameBuf = kp.videoFrameBuf[:0]
				kp.videoFrameBuf = append(kp.videoFrameBuf, syncBegin...)
				kp.videoFrameBuf = append(kp.videoFrameBuf, "\x1b7"...)
				kp.videoFrameBuf = append(kp.videoFrameBuf, fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1)...)
			}
			kp.videoFrameBuf = append(kp.videoFrameBuf, buf.Bytes()...)
			if !more {
				// Last chunk: restore cursor + flush immediately
				kp.videoFrameBuf = append(kp.videoFrameBuf, "\x1b8"...)
				kp.videoFrameBuf = append(kp.videoFrameBuf, syncEnd...)
				if kp.hostOut != nil {
					_, _ = kp.hostOut.Write(kp.videoFrameBuf)
				}
				kp.videoFrameBuf = kp.videoFrameBuf[:0]
			}
		} else {
			kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
		}
	}

	// Store placement for tracking (placement will be done by RefreshAllPlacements)
	if andPlace {
		if kp.placements[windowID] == nil {
			kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
		}
		kp.placements[windowID][hostID] = &PassthroughPlacement{
			GuestImageID:      pending.ImageID,
			HostImageID:       hostID,
			WindowID:          windowID,
			GuestX:            pending.CursorX,
			AbsoluteLine:      pending.ScrollbackLen + pending.CursorY,
			HostX:             hostX,
			HostY:             hostY,
			Cols:              displayCols,
			Rows:              imgRows,
			DisplayRows:       displayRows,
			SourceX:           pending.SourceX,
			SourceY:           pending.SourceY,
			SourceWidth:       pending.SourceWidth,
			SourceHeight:      pending.SourceHeight,
			XOffset:           pending.XOffset,
			YOffset:           pending.YOffset,
			ZIndex:            pending.ZIndex,
			Virtual:           pending.Virtual,
			Hidden:            true, // Start hidden, RefreshAllPlacements will place it
			PlacedOnAltScreen: pending.IsAltScreen,
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

func (kp *KittyPassthrough) forwardFileTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) {
	if cmd.FilePath == "" {
		return
	}

	filePath := cmd.FilePath
	if cmd.Medium == vt.KittyMediumSharedMemory {
		filePath = "/dev/shm/" + cmd.FilePath
	}

	kittyPassthroughLog("forwardFileTransmit: file=%s, andPlace=%v, medium=%c", filePath, andPlace, cmd.Medium)

	// Reuse existing host ID if this window already has a placement for this
	// guest image ID. This eliminates delete+re-place flicker for video playback:
	// transmitting with the same ID replaces the image data in-place, and the
	// existing placement automatically shows the new frame.
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}

	hostID, reusingID := kp.imageIDMap[windowID][cmd.ImageID]
	if !reusingID {
		// First frame for this image — allocate a new ID
		hostID = kp.allocateHostID()
		kp.imageIDMap[windowID][cmd.ImageID] = hostID
		if andPlace {
			kp.deleteAllWindowPlacements(windowID, false)
		}
	} else if andPlace {
		// Reusing ID — check if dimensions changed (e.g., window resize).
		// If so, delete old placement so it gets recreated at the new size.
		if placements := kp.placements[windowID]; placements != nil {
			for _, p := range placements {
				imgRows, imgCols := kp.calculateImageCells(cmd)
			if p.HostImageID == hostID && (p.Rows != imgRows || p.Cols != imgCols) {
					kp.deleteOnePlacement(p)
					delete(placements, hostID)
					break
				}
			}
		}
	}
	kittyPassthroughLog("forwardFileTransmit: mapped guestID=%d -> hostID=%d for window=%s", cmd.ImageID, hostID, windowID[:8])

	// PERFORMANCE: Forward the file path directly to the host terminal.
	// The host (Ghostty/Kitty) reads the file itself — no need to read the
	// entire file into memory, base64 encode it, and chunk it.
	// For t=s (shm), send the original shm name (NOT /dev/shm/ prefixed path).
	// The host terminal prepends /dev/shm/ itself.
	// For t=f/t=t, send the full file path.
	encodePath := cmd.FilePath // Original name from the guest
	if cmd.Medium != vt.KittyMediumSharedMemory {
		encodePath = filePath // Use potentially modified path for non-shm
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(encodePath))

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	// Calculate content area dimensions (accounting for borders)
	contentWidth := windowWidth - 2   // -2 for left/right borders
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

	// Build a single transmit command with the correct medium type.
	// The host terminal reads the file/shm directly — no chunking needed.
	//
	// For video playback (reusing ID + andPlace), use a=T (transmit+place)
	// to avoid race conditions where RefreshAllPlacements runs before the
	// new transmit arrives. For first frames (new ID), use a=t and let
	// RefreshAllPlacements handle placement.
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")

	// Use the original medium type: f=file, s=shared memory, t=temp file
	medium := "f"
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		medium = "s"
	case vt.KittyMediumTempFile:
		medium = "t"
	}

	action := "t" // transmit only (default)
	if andPlace {
		action = "T" // transmit+place (always when placement is requested)
	}

	fmt.Fprintf(&buf, "a=%s,t=%s,i=%d,f=%d,s=%d,v=%d,q=2",
		action, medium, hostID, cmd.Format, cmd.Width, cmd.Height)
	if cmd.Compression == vt.KittyCompressionZlib {
		buf.WriteString(",o=z")
	}
	if displayCols > 0 {
		fmt.Fprintf(&buf, ",c=%d", displayCols)
	}
	if displayRows > 0 {
		fmt.Fprintf(&buf, ",r=%d", displayRows)
	}
	if cmd.SourceX > 0 {
		fmt.Fprintf(&buf, ",x=%d", cmd.SourceX)
	}
	if cmd.SourceY > 0 {
		fmt.Fprintf(&buf, ",y=%d", cmd.SourceY)
	}
	if cmd.SourceWidth > 0 {
		fmt.Fprintf(&buf, ",w=%d", cmd.SourceWidth)
	}
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
		fmt.Fprintf(&buf, ",h=%d", sourceHeight)
	}
	if cmd.XOffset > 0 {
		fmt.Fprintf(&buf, ",X=%d", cmd.XOffset)
	}
	if cmd.YOffset > 0 {
		fmt.Fprintf(&buf, ",Y=%d", cmd.YOffset)
	}
	if cmd.ZIndex != 0 {
		fmt.Fprintf(&buf, ",z=%d", cmd.ZIndex)
	}
	buf.WriteByte(';')
	buf.WriteString(encoded)
	buf.WriteString("\x1b\\")

	// For video (reusing ID + shm), write IMMEDIATELY to host terminal.
	// File/shm-based video is time-critical: mpv overwrites the shm/file
	// with the next frame almost instantly.
	// For non-video (first image, icat), always transmit via pendingOutput
	// and let RefreshAllPlacements handle placement with proper clipping.
	isVideoFrame := reusingID && action == "T"

	if isVideoFrame && kp.hostOut != nil {
		// Bounds check for video
		visible := windowX >= 0 && windowY >= 0 && hostX >= 0 && hostY >= 0
		if visible && displayCols > 0 {
			visible = hostX+displayCols <= windowX+1+contentWidth
		}
		if visible && displayRows > 0 {
			visible = hostY+displayRows <= windowY+1+contentHeight
		}
		if visible && kp.screenWidth > 0 && kp.screenHeight > 0 {
			if hostX+displayCols > kp.screenWidth || hostY+displayRows >= kp.screenHeight-1 {
				visible = false
			}
		}

		if visible {
			var posCmd []byte
			posCmd = append(posCmd, syncBegin...)
			posCmd = append(posCmd, fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1)...)
			posCmd = append(posCmd, buf.Bytes()...)
			posCmd = append(posCmd, syncEnd...)
			_, _ = kp.hostOut.Write(posCmd)
		} else if hostID > 0 {
			var del []byte
			del = append(del, syncBegin...)
			del = append(del, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)...)
			del = append(del, syncEnd...)
			_, _ = kp.hostOut.Write(del)
		}
	} else {
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	// Don't clean up files here — for shared memory (t=s), the guest app
	// manages the lifecycle. For temp files (t=t), the host terminal deletes
	// them after reading. For regular files (t=f), they persist.

	// Store placement using hostID as key (cmd.ImageID is often 0 for new images)
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	kp.placements[windowID][hostID] = &PassthroughPlacement{
		GuestImageID:      cmd.ImageID,
		HostImageID:       hostID,
		WindowID:          windowID,
		GuestX:            cursorX,
		AbsoluteLine:      scrollbackLen + cursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
		Rows:              imgRows,     // Original image rows (for scroll clipping)
		DisplayRows:       displayRows, // Capped rows for initial display
		SourceX:           cmd.SourceX,
		SourceY:           cmd.SourceY,
		SourceWidth:       cmd.SourceWidth,
		SourceHeight:      cmd.SourceHeight,
		XOffset:           cmd.XOffset,
		YOffset:           cmd.YOffset,
		ZIndex:            cmd.ZIndex,
		Virtual:           cmd.Virtual,
		Hidden:            true, // Start hidden, RefreshAllPlacements will place it
		PlacedOnAltScreen: isAltScreen,
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
	_ bool, // isAltScreen - currently unused
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
	fmt.Fprintf(&buf, "\x1b[%d;%dH", hostY+1, hostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d", hostID)

	if cmd.PlacementID > 0 {
		fmt.Fprintf(&buf, ",p=%d", cmd.PlacementID)
	}
	// Always set display dimensions to control size
	if displayCols > 0 {
		fmt.Fprintf(&buf, ",c=%d", displayCols)
	}
	if displayRows > 0 {
		fmt.Fprintf(&buf, ",r=%d", displayRows)
	}
	if cmd.XOffset > 0 {
		fmt.Fprintf(&buf, ",X=%d", cmd.XOffset)
	}
	if cmd.YOffset > 0 {
		fmt.Fprintf(&buf, ",Y=%d", cmd.YOffset)
	}
	if cmd.SourceX > 0 {
		fmt.Fprintf(&buf, ",x=%d", cmd.SourceX)
	}
	if cmd.SourceY > 0 {
		fmt.Fprintf(&buf, ",y=%d", cmd.SourceY)
	}
	if cmd.SourceWidth > 0 {
		fmt.Fprintf(&buf, ",w=%d", cmd.SourceWidth)
	}
	if cmd.SourceHeight > 0 {
		fmt.Fprintf(&buf, ",h=%d", cmd.SourceHeight)
	}
	if cmd.ZIndex != 0 {
		fmt.Fprintf(&buf, ",z=%d", cmd.ZIndex)
	}
	// Note: Don't send U=1 to host - TUIOS renders guest content itself
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

// deleteAllWindowPlacements removes all placements for a window from the host terminal
// and clears the placement tracking. If clearImageMap is true, also clears the imageIDMap.
func (kp *KittyPassthrough) deleteAllWindowPlacements(windowID string, clearImageMap bool) {
	for _, p := range kp.placements[windowID] {
		kp.deleteOnePlacement(p)
	}
	kp.placements[windowID] = nil
	if clearImageMap {
		kp.imageIDMap[windowID] = nil
	}
}

func (kp *KittyPassthrough) forwardDelete(cmd *vt.KittyCommand, windowID string) {
	kittyPassthroughLog("forwardDelete: delete=%c, imageID=%d, windowID=%s", cmd.Delete, cmd.ImageID, windowID[:8])

	switch cmd.Delete {
	case vt.KittyDeleteAll, 0:
		kp.deleteAllWindowPlacements(windowID, false)

	case vt.KittyDeleteByID:
		if windowMap := kp.imageIDMap[windowID]; windowMap != nil {
			if hostID, ok := windowMap[cmd.ImageID]; ok {
				kp.deleteOnePlacement(&PassthroughPlacement{HostImageID: hostID})
				if placements := kp.placements[windowID]; placements != nil {
					delete(placements, hostID)
				}
				delete(windowMap, cmd.ImageID)
				kittyPassthroughLog("forwardDelete: deleted guestID=%d (hostID=%d)", cmd.ImageID, hostID)
			}
		}

	case vt.KittyDeleteByIDAndPlacement:
		if windowMap := kp.imageIDMap[windowID]; windowMap != nil {
			if hostID, ok := windowMap[cmd.ImageID]; ok {
				var buf bytes.Buffer
				buf.WriteString("\x1b_G")
				fmt.Fprintf(&buf, "a=d,d=I,i=%d", hostID)
				if cmd.PlacementID > 0 {
					fmt.Fprintf(&buf, ",p=%d", cmd.PlacementID)
				}
				buf.WriteString(",q=2\x1b\\")
				kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
				if placements := kp.placements[windowID]; placements != nil {
					delete(placements, hostID)
				}
				delete(windowMap, cmd.ImageID)
				kittyPassthroughLog("forwardDelete: deleted guestID=%d (hostID=%d) with placement", cmd.ImageID, hostID)
			}
		}

	default:
		// Handles DeleteOnScreen, DeleteAtCursor, DeleteAtCursorCell, and unknown types.
		// For simplicity, all of these clear all placements and the imageID map.
		if cmd.Delete != vt.KittyDeleteOnScreen &&
			cmd.Delete != vt.KittyDeleteAtCursor &&
			cmd.Delete != vt.KittyDeleteAtCursorCell {
			kittyPassthroughLog("forwardDelete: UNHANDLED delete type=%c (%d), clearing all as fallback", cmd.Delete, cmd.Delete)
		}
		kp.deleteAllWindowPlacements(windowID, true)
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
	kp.OnWindowMove(windowID, windowX, windowY, contentOffsetX, contentOffsetY, scrollbackLen, scrollOffset, viewportHeight)
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

	// Update screen dimensions from any window info
	for _, info := range allWindows {
		if info.ScreenWidth > 0 && info.ScreenHeight > 0 {
			kp.screenWidth = info.ScreenWidth
			kp.screenHeight = info.ScreenHeight
			break
		}
	}

	for windowID, placements := range kp.placements {
		if len(placements) == 0 {
			continue
		}

		info := allWindows[windowID]
		kittyPassthroughLog("RefreshAllPlacements: windowID=%s, info=%v, numPlacements=%d", windowID[:8], info != nil, len(placements))
		if info == nil {
			for _, p := range placements {
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
			}
			delete(kp.placements, windowID)
			continue
		}

		kittyPassthroughLog("RefreshAllPlacements: windowID=%s, IsAltScreen=%v, visible=%v", windowID[:8], info.IsAltScreen, info.Visible)

		// During window manipulation (drag/resize), let images reposition
		// with the window. The change detection below (posChanged check)
		// ensures we only re-place if the position actually changed.

		// Calculate viewport dimensions (accounting for window borders)
		viewportTop := info.ScrollbackLen - info.ScrollOffset
		viewportHeight := info.Height - 2
		viewportWidth := info.Width - 2

		// Collect IDs to delete (for altscreen cleanup)
		var idsToDelete []uint32

		for hostID, p := range placements {
			// Skip placements that are still receiving chunked data
			if p.Streaming {
				continue
			}

			// Handle screen mode mismatch:
			// - Images placed on normal screen should be hidden when altscreen is active
			// - Images placed on altscreen should be DELETED when back to normal screen
			//   (cleanup after TUI apps like yazi exit)
			if info.IsAltScreen != p.PlacedOnAltScreen {
				kittyPassthroughLog("RefreshPlacement: altscreen mismatch (info=%v, placed=%v)",
					info.IsAltScreen, p.PlacedOnAltScreen)
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
				// When exiting altscreen (now on normal screen), delete altscreen placements entirely
				// This cleans up images from TUI apps like yazi when they exit
				if !info.IsAltScreen && p.PlacedOnAltScreen {
					kittyPassthroughLog("RefreshPlacement: cleaning up altscreen placement hostID=%d", hostID)
					idsToDelete = append(idsToDelete, hostID)
				}
				continue
			}

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

			// Hide images when host position is out of bounds.
			if anyPartVisible && (newHostX < 0 || newHostY < 0) {
				anyPartVisible = false
			}
			if anyPartVisible && (info.WindowX < 0 || info.WindowY < 0) {
				anyPartVisible = false
			}
			// Hide if image would extend past or touch the host terminal screen edge.
			// Placing an image near the bottom causes the terminal to scroll to
			// make room, creating a feedback loop of duplicate frames scrolling up.
			// Use a 1-row margin to prevent this.
			if anyPartVisible && info.ScreenWidth > 0 && info.ScreenHeight > 0 {
				if newHostX+imageCellWidth > info.ScreenWidth || newHostY+imageCellHeight >= info.ScreenHeight-1 {
					anyPartVisible = false
				}
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
				// Only re-place if position/clipping actually changed
				posChanged := p.Hidden || p.HostX != newHostX || p.HostY != newHostY ||
					p.ClipTop != clipTop || p.ClipBottom != clipBottom || p.MaxShowable != maxShowableRows
				if posChanged {
					if !p.Hidden {
						kp.deleteOnePlacement(p)
					}
					p.HostX = newHostX
					p.HostY = newHostY
					p.ClipTop = clipTop
					p.ClipBottom = clipBottom
					p.MaxShowable = maxShowableRows
					kp.placeOne(p)
				}
				p.Hidden = false
			}
		}

		// Clean up altscreen placements that are no longer needed
		for _, id := range idsToDelete {
			delete(placements, id)
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
// HideAllPlacements hides all visible image placements. Used during resize
// to prevent stale positions. RefreshAllPlacements will re-place them.
func (kp *KittyPassthrough) HideAllPlacements() {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, placements := range kp.placements {
		for _, p := range placements {
			if !p.Hidden {
				kp.deleteOnePlacement(p)
				p.Hidden = true
			}
		}
	}
	kp.flushToHost()
}

func (kp *KittyPassthrough) deleteOnePlacement(p *PassthroughPlacement) {
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=d,d=i,i=%d", p.HostImageID)
	if p.PlacementID > 0 {
		fmt.Fprintf(&buf, ",p=%d", p.PlacementID)
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
	fmt.Fprintf(&buf, "\x1b[%d;%dH", p.HostY+1, p.HostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d", p.HostImageID)
	if p.PlacementID > 0 {
		fmt.Fprintf(&buf, ",p=%d", p.PlacementID)
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
		fmt.Fprintf(&buf, ",c=%d", p.Cols)
	}
	if visibleRows > 0 {
		fmt.Fprintf(&buf, ",r=%d", visibleRows)
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
		fmt.Fprintf(&buf, ",x=%d", p.SourceX)
	}
	if sourceY > 0 {
		fmt.Fprintf(&buf, ",y=%d", sourceY)
	}
	// Use original source width if specified (no horizontal clipping for now)
	if p.SourceWidth > 0 {
		fmt.Fprintf(&buf, ",w=%d", p.SourceWidth)
	}
	// Always set h for vertical clipping
	if sourceHeight > 0 {
		fmt.Fprintf(&buf, ",h=%d", sourceHeight)
	}
	if p.XOffset > 0 {
		fmt.Fprintf(&buf, ",X=%d", p.XOffset)
	}
	if p.YOffset > 0 {
		fmt.Fprintf(&buf, ",Y=%d", p.YOffset)
	}
	if p.ZIndex != 0 {
		fmt.Fprintf(&buf, ",z=%d", p.ZIndex)
	}
	// Note: Don't send U=1 to host - TUIOS renders guest content itself
	buf.WriteString(",q=2\x1b\\")
	buf.WriteString("\x1b8") // Restore cursor position
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

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
		// In daemon mode, the daemon's VT emulator responds to queries directly
		// with low latency. Skip here to avoid sending a duplicate response.
		if win.DaemonMode && cmd.Action == vt.KittyActionQuery {
			return
		}

		cursorPos := win.Terminal.CursorPosition()
		scrollbackLen := win.Terminal.ScrollbackLen()
		result := kp.ForwardCommand(
			cmd, rawData, win.ID,
			win.X, win.Y,
			win.Width, win.Height,
			1, 1,
			cursorPos.X, cursorPos.Y,
			scrollbackLen,
			win.IsAltScreen,
			func(response []byte) {
				kittyPassthroughLog("ptyInput callback: Pty=%v, DaemonWriteFunc=%v, response=%q", win.Pty != nil, win.DaemonWriteFunc != nil, response)
				if win.Pty != nil {
					_, _ = win.Pty.Write(response)
				} else if win.DaemonWriteFunc != nil {
					_ = win.DaemonWriteFunc(response)
				} else {
					kittyPassthroughLog("ptyInput callback: WARNING - both Pty and DaemonWriteFunc are nil, response dropped!")
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
