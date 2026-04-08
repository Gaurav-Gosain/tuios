package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"runtime"
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
	// inlineGraphics indicates the host terminal is xterm.js with a custom
	// kitty overlay (xterm-kitty-overlay.js) that renders placements as
	// absolutely-positioned DOM canvases. In this mode, file-based
	// transmissions (t=f, t=s) are read server-side and re-encoded as
	// direct (t=d) chunks because the browser cannot read local files.
	inlineGraphics bool
	hostOut        *os.File
	hostMu         sync.Mutex // serializes writes to hostOut across render + async paths

	placements    map[string]map[uint32]*PassthroughPlacement
	imageIDMap    map[string]map[uint32]uint32 // maps (windowID, guestImageID) -> hostImageID
	nextHostID    uint32
	pendingOutput []byte
	videoFrameBuf []byte // Reusable buffer for immediate video frame writes

	// Async video frame writer. Video apps (mpv, youterm) send 30+ fps of
	// large image data. Processing synchronously inside the VT callback
	// blocks the bubbletea render loop and makes the entire UI unresponsive.
	// Instead we enqueue frames to this channel; a background goroutine
	// drains it and writes to hostOut. Channel capacity 1 means we always
	// keep at most one pending frame; newer frames replace older ones.
	asyncFrameCh chan []byte

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
	// HeaderParams stores filtered params from the first (params-only) chunk,
	// to be merged into the first data-carrying chunk. Needed because chafa
	// sends params and data in separate APC sequences.
	HeaderParams string
	HeaderSent   bool
	// AndPlace tracks whether the original chunk that created this pending
	// was a TransmitPlace (action T). Chafa sends first chunk as T (andPlace=true)
	// then subsequent chunks as t (andPlace=false). We track this so the final
	// chunk's PlacementResult is returned correctly for whitespace reservation.
	AndPlace bool
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

	// Image's NATIVE pixel dimensions as transmitted (from s/v params).
	// Used to derive an accurate pixels-per-cell for source-region cropping
	//  - critical when client and daemon have different cell sizes (web mode).
	ImagePixelWidth  int
	ImagePixelHeight int

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

// KittyPassthroughOptions configures a KittyPassthrough instance.
type KittyPassthroughOptions struct {
	// ForceEnable skips capability detection and enables kitty graphics
	// unconditionally. Used in web mode where stdin isn't a real TTY so
	// GetHostCapabilities() can't detect kitty support, but the browser
	// terminal (xterm.js with kitty addon) supports it.
	ForceEnable bool
	// Output is the writer for kitty graphics APC sequences. If nil, the
	// passthrough opens /dev/tty (or falls back to os.Stdout). Web mode
	// should pass the sip session's PtySlave so graphics bytes flow through
	// the same PTY as bubbletea's text output to the browser.
	Output *os.File
}

// NewKittyPassthrough creates a passthrough using auto-detected capabilities
// and /dev/tty for output. Use NewKittyPassthroughWithOptions for finer
// control (web mode, custom writers).
func NewKittyPassthrough() *KittyPassthrough {
	return NewKittyPassthroughWithOptions(KittyPassthroughOptions{})
}

// NewKittyPassthroughWithOptions creates a passthrough with custom options.
func NewKittyPassthroughWithOptions(opts KittyPassthroughOptions) *KittyPassthrough {
	caps := GetHostCapabilities()
	enabled := caps.KittyGraphics || opts.ForceEnable
	kittyPassthroughLog("NewKittyPassthrough: KittyGraphics=%v Force=%v TerminalName=%s", caps.KittyGraphics, opts.ForceEnable, caps.TerminalName)
	// Open /dev/tty once for the lifetime of the passthrough (avoids per-frame open/close)
	hostOut := opts.Output
	if hostOut == nil {
		hostOut = os.Stdout
		if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
			hostOut = tty
		}
	}

	kp := &KittyPassthrough{
		enabled:           enabled,
		inlineGraphics:    opts.ForceEnable,
		hostOut:           hostOut,
		placements:        make(map[string]map[uint32]*PassthroughPlacement),
		imageIDMap:        make(map[string]map[uint32]uint32),
		nextHostID:        1,
		pendingDirectData: make(map[string]*pendingDirectTransmit),
		asyncFrameCh:      make(chan []byte, 1),
	}
	go kp.asyncFrameWriter()
	return kp
}

// WriteToHost writes graphics data directly to the host terminal,
// wrapped in synchronized update sequences to prevent tearing.
// asyncFrameWriter drains asyncFrameCh and writes video frames to hostOut
// in a background goroutine so the VT callback and render loop stay
// responsive during high-fps video playback.
func (kp *KittyPassthrough) asyncFrameWriter() {
	for data := range kp.asyncFrameCh {
		if kp.hostOut == nil || len(data) == 0 {
			continue
		}
		kp.hostMu.Lock()
		_, _ = kp.hostOut.Write(syncBegin)
		_, _ = kp.hostOut.Write(data)
		_, _ = kp.hostOut.Write(syncEnd)
		kp.hostMu.Unlock()
	}
}

func (kp *KittyPassthrough) WriteToHost(data []byte) {
	if kp.hostOut == nil || len(data) == 0 {
		return
	}
	kp.hostMu.Lock()
	_, _ = kp.hostOut.Write(syncBegin)
	_, _ = kp.hostOut.Write(data)
	_, _ = kp.hostOut.Write(syncEnd)
	kp.hostMu.Unlock()
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

func (kp *KittyPassthrough) IsInlineGraphics() bool {
	return kp.inlineGraphics
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

	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		log.Printf("[KP] ForwardCommand action=%c enabled=%v inline=%v imageID=%d more=%v dataLen=%d",
			cmd.Action, kp.enabled, kp.inlineGraphics, cmd.ImageID, cmd.More, len(cmd.Data))
	}
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
		// On the final chunk of a chunked transmission that was part of a
		// previous TransmitPlace (chafa: T ... t ... t m=0), return the image
		// dimensions from the tracked placement so the guest terminal reserves
		// whitespace. Without this, the image appears but the cursor doesn't
		// advance below it, causing text to overdraw.
		if !cmd.More {
			if placements := kp.placements[windowID]; placements != nil {
				for _, p := range placements {
					if p.Streaming {
						return &PlacementResult{
							Rows:       p.Rows,
							Cols:       p.Cols,
							CursorMove: cmd.CursorMove,
						}
					}
				}
			}
		}

	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		result := kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Return PlacementResult from direct transmit if available
		if result != nil {
			return result
		}
		// On the final chunk (m=0), return image dimensions so the guest
		// terminal reserves whitespace for the image. This applies to BOTH
		// file-based AND direct transmissions (chafa uses direct with chunks).
		if !cmd.More {
			imgRows, imgCols := kp.calculateImageCells(cmd)
			// For direct mode where the final chunk doesn't have s/v params,
			// look up the stored placement from the first chunk.
			if imgRows == 0 && imgCols == 0 && !isFileBased {
				if placements := kp.placements[windowID]; placements != nil {
					for _, p := range placements {
						if p.Streaming || p.Hidden {
							imgRows = p.Rows
							imgCols = p.Cols
							break
						}
					}
				}
			}
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
		// Don't flush immediately  - accumulate in pendingOutput.
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

	_ = innerData // innerData unused in this v0.6.0-style implementation

	hasPendingData := kp.pendingDirectData[windowID] != nil
	if !andPlace && !hasPendingData {
		// Pass through raw (already has framing)
		kp.pendingOutput = append(kp.pendingOutput, rawData...)
		return nil
	}

	// v0.6.0-style direct transmit: accumulate raw decoded bytes across chunks,
	// then on the final chunk re-encode and emit as properly-formatted kitty
	// APC chunks of our own. This avoids the mess of trying to splice chafa's
	// non-standard chunk format (params-only first chunk + data-only continuations).

	// Get or create pending transmission state
	pending := kp.pendingDirectData[windowID]
	if pending == nil {
		pending = &pendingDirectTransmit{
			Format:         cmd.Format,
			Compression:    cmd.Compression,
			Width:          cmd.Width,
			Height:         cmd.Height,
			ImageID:        cmd.ImageID,
			Columns:        cmd.Columns,
			Rows:           cmd.Rows,
			SourceX:        cmd.SourceX,
			SourceY:        cmd.SourceY,
			SourceWidth:    cmd.SourceWidth,
			SourceHeight:   cmd.SourceHeight,
			XOffset:        cmd.XOffset,
			YOffset:        cmd.YOffset,
			ZIndex:         cmd.ZIndex,
			Virtual:        cmd.Virtual,
			CursorMove:     cmd.CursorMove,
			AndPlace:       andPlace,
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

	// Accumulate DECODED data bytes from this chunk.
	// Cap at expected size to handle double-delivery (reliable kittyChan +
	// lossy broadcast both feed the same VT, producing duplicate chunks).
	expectedSize := pending.Width * pending.Height * 4 // RGBA
	if pending.Format == vt.KittyFormatRGB {
		expectedSize = pending.Width * pending.Height * 3
	}
	if expectedSize > 0 && len(pending.Data)+len(cmd.Data) > expectedSize {
		remaining := expectedSize - len(pending.Data)
		if remaining > 0 {
			pending.Data = append(pending.Data, cmd.Data[:remaining]...)
		}
	} else {
		pending.Data = append(pending.Data, cmd.Data...)
	}

	kittyPassthroughLog("forwardTransmit: accumulated %d bytes, total=%d, more=%v",
		len(cmd.Data), len(pending.Data), cmd.More)

	// If more chunks coming, wait for them
	if cmd.More {
		return nil
	}

	// Final chunk  - process complete image
	defer delete(kp.pendingDirectData, windowID)

	if len(pending.Data) == 0 {
		kittyPassthroughLog("forwardTransmit: no data accumulated, skipping")
		return nil
	}

	// Get/allocate host ID.
	// - Guest image ID == 0 is kitty's "auto-assign" sentinel; each transmit
	//   with ID 0 is a DISTINCT image (chafa uses 0 for every invocation).
	//   Always allocate a fresh host ID so multiple chafa images coexist in
	//   scrollback without overwriting each other.
	// - For non-zero guest IDs, reuse the same host ID on re-transmit so the
	//   image data is replaced in place.
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}
	var hostID uint32
	if pending.ImageID == 0 {
		hostID = kp.allocateHostID()
	} else {
		var reusingID bool
		hostID, reusingID = kp.imageIDMap[windowID][pending.ImageID]
		if !reusingID {
			hostID = kp.allocateHostID()
			kp.imageIDMap[windowID][pending.ImageID] = hostID
		}
	}

	// Re-encode to base64 and emit as properly-formatted kitty chunks
	encoded := base64.StdEncoding.EncodeToString(pending.Data)

	hostX := pending.WindowX + pending.ContentOffsetX + pending.CursorX
	hostY := pending.WindowY + pending.ContentOffsetY + pending.CursorY

	contentWidth := pending.WindowWidth - 2
	contentHeight := pending.WindowHeight - 2

	// Calculate image cell dimensions
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

	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	// Emit transmit-only command in proper 4096-byte kitty chunks.
	// Placement is handled by RefreshAllPlacements.
	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		chunk := encoded[i:end]
		more := end < len(encoded)

		var buf bytes.Buffer
		buf.WriteString("\x1b_G")
		if i == 0 {
			// First chunk: full header
			fmt.Fprintf(&buf, "a=t,i=%d,f=%d,s=%d,v=%d,q=2",
				hostID, pending.Format, pending.Width, pending.Height)
			if pending.Compression == vt.KittyCompressionZlib {
				buf.WriteString(",o=z")
			}
		} else {
			// Continuation chunks: just image ID (no placement params for a=t)
			fmt.Fprintf(&buf, "i=%d,q=2", hostID)
		}
		if more {
			buf.WriteString(",m=1")
		}
		buf.WriteByte(';')
		buf.WriteString(chunk)
		buf.WriteString("\x1b\\")
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	kittyPassthroughLog("forwardTransmit: emitted %d bytes as %d-byte chunks, hostID=%d, imgSize=(%d,%d) srcXYWH=(%d,%d,%d,%d) imgPixels=(%d,%d)",
		len(encoded), chunkSize, hostID, imgCols, imgRows,
		pending.SourceX, pending.SourceY, pending.SourceWidth, pending.SourceHeight,
		pending.Width, pending.Height)

	// Track placement for RefreshAllPlacements
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
		Hidden:            true, // RefreshAllPlacements places it
		PlacedOnAltScreen: pending.IsAltScreen,
		// The image's native pixel dimensions from the s/v params. These are
		// what the image ACTUALLY has on disk/in kitty  - independent of the
		// client's notion of cell size. placeOne uses these to derive accurate
		// pixels-per-row for source-region cropping, which is critical in
		// web/daemon mode where the client and daemon may have different
		// terminal cell sizes.
		ImagePixelWidth:  pending.Width,
		ImagePixelHeight: pending.Height,
	}

	// Return PlacementResult if the original transmission was a TransmitPlace.
	// This triggers whitespace reservation in the guest terminal so the cursor
	// advances past where the image will be placed.
	if pending.AndPlace {
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

	// In inline-graphics mode (tuios-web) the host terminal cannot read
	// files on the server. Read the file ourselves and divert into the
	// direct-transmission path so the bytes reach the browser over the
	// sip PTY. This is critical for apps like youterm / mpv that use
	// shared-memory frames (t=s, /dev/shm/...) and for any t=f / t=t
	// transmission.
	if kp.inlineGraphics {
		kp.forwardFileTransmitInline(cmd, filePath, windowID, andPlace,
			windowX, windowY, windowWidth, windowHeight,
			contentOffsetX, contentOffsetY,
			cursorX, cursorY, scrollbackLen, isAltScreen)
		return
	}

	// Reuse existing host ID if this window already has a placement for this
	// guest image ID. This eliminates delete+re-place flicker for video playback:
	// transmitting with the same ID replaces the image data in-place, and the
	// existing placement automatically shows the new frame.
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}

	hostID, reusingID := kp.imageIDMap[windowID][cmd.ImageID]
	if !reusingID || cmd.ImageID == 0 {
		// New image, or imageID=0 (default - no explicit ID). Apps like
		// youterm use imageID=0 for ALL thumbnails. Each transmit is a
		// separate image that should get its own host ID and placement.
		// Only reuse IDs when the app explicitly sets a non-zero imageID
		// (e.g., video playback with a fixed stream ID).
		hostID = kp.allocateHostID()
		if cmd.ImageID != 0 {
			kp.imageIDMap[windowID][cmd.ImageID] = hostID
		}
	} else if andPlace {
		// Reusing ID  - check if dimensions changed (e.g., window resize).
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
	// The host (Ghostty/Kitty) reads the file itself  - no need to read the
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
	// The host terminal reads the file/shm directly  - no chunking needed.
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

	// Always transmit-only here. Placement is handled either by:
	// - Video immediate path (isVideoFrame) which uses a=T with positioning
	// - RefreshAllPlacements which sends a=p for non-video images
	action := "t"

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
	// Video: reusing ID + chunked (more=true on first chunk).
	// icat/youterm: may reuse ID but sends single unchunked command (more=false).
	isVideoFrame := reusingID && andPlace && cmd.More

	if isVideoFrame && kp.hostOut != nil {
		// Override to a=T for video immediate flush (buf was built with a=t)
		bufBytes := bytes.Replace(buf.Bytes(), []byte("a=t,"), []byte("a=T,"), 1)

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
			posCmd = append(posCmd, bufBytes...)
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

	// Don't clean up files here  - for shared memory (t=s), the guest app
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

// forwardFileTransmitInline handles file / shm / temp-file kitty transmits
// when the host terminal cannot read server-local files (tuios-web's browser
// target). We read the file ourselves, base64 encode it, and emit a normal
// direct (t=d) transmission so the bytes reach the browser through the sip
// PTY. A placement entry is created in the standard hidden-until-refresh
// state so RefreshAllPlacements will emit the matching a=p on the next
// render cycle, identical to the native-mode flow.
func (kp *KittyPassthrough) forwardFileTransmitInline(
	cmd *vt.KittyCommand,
	filePath string,
	windowID string,
	andPlace bool,
	windowX, windowY, windowWidth, windowHeight int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	isAltScreen bool,
) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		kittyPassthroughLog("forwardFileTransmitInline: read %s failed: %v", filePath, err)
		return
	}
	kittyPassthroughLog("forwardFileTransmitInline: read %d bytes from %s", len(data), filePath)

	// Get or allocate a host id. Video frames reuse the same guest id per
	// stream, so the second frame onward finds an existing placement and
	// just replaces the bitmap bytes (our overlay re-renders live
	// placements when their image id gets re-transmitted).
	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}
	hostID, reusingID := kp.imageIDMap[windowID][cmd.ImageID]
	if !reusingID || cmd.ImageID == 0 {
		hostID = kp.allocateHostID()
		if cmd.ImageID != 0 {
			kp.imageIDMap[windowID][cmd.ImageID] = hostID
		}
	}

	// Cell dimensions. Match forwardFileTransmit semantics.
	imgRows, imgCols := kp.calculateImageCells(cmd)
	contentWidth := windowWidth - 2
	contentHeight := windowHeight - 2
	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	// Build the image as 4096-byte kitty chunks (t=d). Pass through
	// format / compression / size so the overlay knows how to decode.
	frameData := kp.buildInlineChunks(hostID, cmd.Format, cmd.Compression, cmd.Width, cmd.Height, data)

	kittyPassthroughLog("forwardFileTransmitInline: built %d bytes, hostID=%d, imgSize=(%d,%d), reusingID=%v",
		len(frameData), hostID, imgCols, imgRows, reusingID)

	if reusingID {
		// Video frame: send asynchronously so the VT callback and render
		// loop stay responsive. Drop frames if the writer is backed up
		// (channel full) to prevent unbounded lag.
		select {
		case kp.asyncFrameCh <- frameData:
		default:
			// Previous frame still in flight, drop this one.
			kittyPassthroughLog("forwardFileTransmitInline: dropped frame (async channel full)")
		}
	} else {
		// First frame / static image: go through pendingOutput so
		// RefreshAllPlacements can attach the a=p in the same flush.
		kp.pendingOutput = append(kp.pendingOutput, frameData...)
	}

	// Track placement. Reuse an existing entry on retransmit so the
	// previously emitted a=p does not get resent (we want the browser
	// to keep the same canvas and just pick up the new bitmap).
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	existing, hasExisting := kp.placements[windowID][hostID]
	if hasExisting {
		// Retransmit path: update dims in case they changed, but keep
		// Hidden state as-is so we do not re-emit a=p.
		existing.Cols = displayCols
		existing.Rows = imgRows
		existing.DisplayRows = displayRows
		existing.HostX = hostX
		existing.HostY = hostY
		existing.AbsoluteLine = scrollbackLen + cursorY
		existing.ImagePixelWidth = cmd.Width
		existing.ImagePixelHeight = cmd.Height
	} else {
		kp.placements[windowID][hostID] = &PassthroughPlacement{
			GuestImageID:      cmd.ImageID,
			HostImageID:       hostID,
			WindowID:          windowID,
			GuestX:            cursorX,
			AbsoluteLine:      scrollbackLen + cursorY,
			HostX:             hostX,
			HostY:             hostY,
			Cols:              displayCols,
			Rows:              imgRows,
			DisplayRows:       displayRows,
			SourceX:           cmd.SourceX,
			SourceY:           cmd.SourceY,
			SourceWidth:       cmd.SourceWidth,
			SourceHeight:      cmd.SourceHeight,
			XOffset:           cmd.XOffset,
			YOffset:           cmd.YOffset,
			ZIndex:            cmd.ZIndex,
			Virtual:           cmd.Virtual,
			Hidden:            true, // RefreshAllPlacements emits a=p
			PlacedOnAltScreen: isAltScreen,
			ImagePixelWidth:   cmd.Width,
			ImagePixelHeight:  cmd.Height,
		}
	}
	_ = andPlace // placement is always driven by RefreshAllPlacements in inline mode
}

// buildInlineChunks encodes raw image bytes as a kitty direct-transmission
// (t=d) sequence split into 4096-byte base64 chunks. Returns the complete
// byte sequence ready to write to the host.
func (kp *KittyPassthrough) buildInlineChunks(hostID uint32, format vt.KittyGraphicsFormat, compression vt.KittyGraphicsCompression, width, height int, raw []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(raw)
	const chunkSize = 4096
	var out bytes.Buffer
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		chunk := encoded[i:end]
		more := end < len(encoded)

		out.WriteString("\x1b_G")
		if i == 0 {
			fmt.Fprintf(&out, "a=t,i=%d,f=%d,s=%d,v=%d,q=2", hostID, format, width, height)
			if compression == vt.KittyCompressionZlib {
				out.WriteString(",o=z")
			}
		} else {
			fmt.Fprintf(&out, "i=%d,q=2", hostID)
		}
		if more {
			out.WriteString(",m=1")
		}
		out.WriteByte(';')
		out.WriteString(chunk)
		out.WriteString("\x1b\\")
	}
	return out.Bytes()
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
	// In web mode, RefreshAllPlacements handles repositioning via the
	// overlay. OnWindowMove's delete-then-reposition pattern sends d=i
	// which wipes image data from the overlay's storage.
	if kp.inlineGraphics {
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

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
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

	// Note: prior versions short-circuited this loop in web mode because
	// xterm-addon-image could not update placements in place. sip now
	// ships a custom kitty overlay (xterm-kitty-overlay.js) that renders
	// placements as absolutely-positioned DOM canvases with proper
	// update/delete semantics, so the standard refresh path works in
	// both native and web modes.

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

		// Calculate viewport dimensions (accounting for window borders).
		// For tiled/borderless windows BorderOffset=0, so content area is full
		// Width×Height. For floating windows with a border, it's 1, so content
		// is (Width-2)×(Height-2).
		viewportTop := info.ScrollbackLen - info.ScrollOffset
		viewportHeight := info.Height - 2*info.ContentOffsetY
		viewportWidth := info.Width - 2*info.ContentOffsetX

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

			// Clamp to viewport: rows vertically, cols horizontally
			maxShowableRows := min(p.Rows-clipTop-clipBottom, viewportHeight)
			if maxShowableRows <= 0 {
				maxShowableRows = 1
			}
			maxShowableCols := p.Cols
			if fullImageRight > viewportWidth {
				maxShowableCols = viewportWidth - p.GuestX
				if maxShowableCols <= 0 {
					anyPartVisible = false
				}
			}

			actualRelativeY := relativeY
			if clipTop > 0 {
				actualRelativeY = 0
			}
			newHostX := info.WindowX + info.ContentOffsetX + p.GuestX
			newHostY := info.WindowY + info.ContentOffsetY + actualRelativeY

			imageCellWidth := maxShowableCols
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
			// In native mode, hide if image extends past the host terminal edge
			// to prevent the terminal from scrolling to make room (feedback loop).
			// In inline-graphics mode (web), the browser overlay clips via CSS
			// overflow:hidden, so this check is unnecessary and causes images to
			// disappear at certain terminal sizes.
			if !kp.inlineGraphics && anyPartVisible && info.ScreenWidth > 0 && info.ScreenHeight > 0 {
				if newHostX+imageCellWidth > info.ScreenWidth || newHostY+imageCellHeight >= info.ScreenHeight-1 {
					anyPartVisible = false
				}
			}

			kittyPassthroughLog("RefreshPlacement: winXY=(%d,%d) size=(%d,%d) off=(%d,%d) relY=%d, origRows=%d, origCols=%d, vpH=%d, vpW=%d, clipTop=%d, clipBot=%d, maxRows=%d, newHost=(%d,%d), visible=%v",
				info.WindowX, info.WindowY, info.Width, info.Height, info.ContentOffsetX, info.ContentOffsetY,
				relativeY, p.Rows, p.Cols, viewportHeight, viewportWidth, clipTop, clipBottom, maxShowableRows, newHostX, newHostY, anyPartVisible)

			if !anyPartVisible {
				// Send a delete only if the image was currently visible.
				// deleteOnePlacement sends d=p (placement id, image id) so
				// the image bytes stay in storage and a subsequent scroll
				// back into view can re-place without retransmitting.
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
			} else {
				// Re-place only if position/clipping changed. Real kitty
				// and our sip overlay both treat a=p with the same (i, p)
				// as an in-place update of the existing placement.
				posChanged := p.Hidden || p.HostX != newHostX || p.HostY != newHostY ||
					p.ClipTop != clipTop || p.ClipBottom != clipBottom ||
					p.MaxShowable != maxShowableRows || p.MaxShowableCols != maxShowableCols
				if posChanged {
					p.HostX = newHostX
					p.HostY = newHostY
					p.ClipTop = clipTop
					p.ClipBottom = clipBottom
					p.MaxShowable = maxShowableRows
					p.MaxShowableCols = maxShowableCols
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
	// In inline-graphics mode (web), the browser overlay manages
	// placement visibility via CSS. Don't send delete commands that
	// would wipe image data from the overlay's storage.
	if kp.inlineGraphics {
		return
	}
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
	fmt.Fprintf(&buf, "a=d,d=i,i=%d,q=2\x1b\\", p.HostImageID)
	// Trace caller for debugging
	var caller string
	if pc, _, line, ok := runtime.Caller(1); ok {
		caller = fmt.Sprintf("%s:%d", runtime.FuncForPC(pc).Name(), line)
	}
	kittyPassthroughLog("deleteOnePlacement: hostID=%d caller=%s", p.HostImageID, caller)
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func (kp *KittyPassthrough) placeOne(p *PassthroughPlacement) {
	caps := GetHostCapabilities()
	cellHeight := caps.CellHeight
	if cellHeight <= 0 {
		cellHeight = 20 // Fallback
	}

	// Use a stable, non-zero placement ID so we can delete the previous
	// placement unambiguously before creating a new one. Kitty's a=p with
	// the same (i, p) replaces  - without p, kitty can stack placements.
	if p.PlacementID == 0 {
		p.PlacementID = 1
	}

	var buf bytes.Buffer
	buf.WriteString("\x1b7") // Save cursor position
	fmt.Fprintf(&buf, "\x1b[%d;%dH", p.HostY+1, p.HostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d,p=%d", p.HostImageID, p.PlacementID)

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

	kittyPassthroughLog("placeOne: hostID=%d, pos=(%d,%d), origRows=%d, origCols=%d, clipTop=%d, clipBot=%d, visibleRows=%d, srcXYWH=(%d,%d,%d,%d), cellH=%d",
		p.HostImageID, p.HostX, p.HostY, p.Rows, p.Cols, p.ClipTop, p.ClipBottom, visibleRows,
		p.SourceX, p.SourceY, p.SourceWidth, p.SourceHeight, cellHeight)

	// Use clamped cols if the image extends past the viewport
	visibleCols := p.Cols
	if p.MaxShowableCols > 0 && p.MaxShowableCols < visibleCols {
		visibleCols = p.MaxShowableCols
	}
	if visibleCols > 0 {
		fmt.Fprintf(&buf, ",c=%d", visibleCols)
	}
	if visibleRows > 0 {
		fmt.Fprintf(&buf, ",r=%d", visibleRows)
	}

	// Source clipping parameters. Emit the full x,y,w,h rectangle when
	// clipping is needed so kitty crops the source to exactly the visible
	// slice. When combined with c,r, kitty maps that source pixel rect 1:1
	// onto the cell area, avoiding vertical squash.
	//
	// Derive pixels-per-row from the image's ACTUAL native pixel dimensions
	// (from the s/v transmit params) divided by its native cell rows. This is
	// critical in web/daemon mode where the client's host cell height may
	// differ from the daemon's (e.g. client cellH=22 but image was generated
	// at daemon cellH=20 → 380/19=20). Using the client's cellHeight would
	// produce source regions that overflow the image and xterm-addon-image
	// rejects them.
	isClipping := p.ClipTop > 0 || p.ClipBottom > 0 || visibleCols < p.Cols
	pixelsPerRow := cellHeight
	switch {
	case p.Rows > 0 && p.ImagePixelHeight > 0:
		pixelsPerRow = p.ImagePixelHeight / p.Rows
	case p.Rows > 0 && p.SourceHeight > 0:
		pixelsPerRow = p.SourceHeight / p.Rows
	}
	pixelsPerCol := caps.CellWidth
	switch {
	case p.Cols > 0 && p.ImagePixelWidth > 0:
		pixelsPerCol = p.ImagePixelWidth / p.Cols
	case p.Cols > 0 && p.SourceWidth > 0:
		pixelsPerCol = p.SourceWidth / p.Cols
	}
	switch {
	case isClipping:
		srcX := p.SourceX
		srcY := p.SourceY + p.ClipTop*pixelsPerRow
		srcW := p.SourceWidth
		if srcW == 0 && pixelsPerCol > 0 {
			srcW = p.Cols * pixelsPerCol
		}
		// Horizontal crop: if columns were clamped, crop source width
		if visibleCols < p.Cols && pixelsPerCol > 0 {
			srcW = visibleCols * pixelsPerCol
		}
		srcH := visibleRows * pixelsPerRow
		// Clamp against the image's native pixel height so we never request
		// a source region that overflows the image  - xterm-addon-image rejects
		// such requests (real kitty silently clamps).
		if p.ImagePixelHeight > 0 && srcY+srcH > p.ImagePixelHeight {
			srcH = max(p.ImagePixelHeight-srcY, 0)
		}
		if p.ImagePixelWidth > 0 && srcX+srcW > p.ImagePixelWidth {
			srcW = max(p.ImagePixelWidth-srcX, 0)
		}
		fmt.Fprintf(&buf, ",x=%d,y=%d,w=%d,h=%d", srcX, srcY, srcW, srcH)
	case p.SourceWidth > 0 || p.SourceHeight > 0:
		if p.SourceX > 0 {
			fmt.Fprintf(&buf, ",x=%d", p.SourceX)
		}
		if p.SourceY > 0 {
			fmt.Fprintf(&buf, ",y=%d", p.SourceY)
		}
		if p.SourceWidth > 0 {
			fmt.Fprintf(&buf, ",w=%d", p.SourceWidth)
		}
		if p.SourceHeight > 0 {
			fmt.Fprintf(&buf, ",h=%d", p.SourceHeight)
		}
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
	kittyPassthroughLog("placeOne: emitted kitty cmd: %q", buf.String())
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
		borderOff := win.BorderOffset()
		result := kp.ForwardCommand(
			cmd, rawData, win.ID,
			win.X, win.Y,
			win.Width, win.Height,
			borderOff, borderOff,
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
