package app

import (
	"fmt"
	"os"
	"sync"
	"time"

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
	kittyPassthroughLog("getOrAllocateHostID: windowID=%s, guestID=%d -> hostID=%d", windowID[:min(8, len(windowID))], guestImageID, hostID)
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

// maxPassthroughTransmitBytes caps the accumulated chunk data for a single
// direct passthrough transmission, mirroring the internal handler's limit.
const maxPassthroughTransmitBytes = 64 * 1024 * 1024

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
