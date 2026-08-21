package app

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
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

// isKittyResponse checks if a graphics payload looks like an echoed kitty
// protocol response rather than real image data.
//
// It is matched against the RAW wire payload (the base64 text between ';' and
// the APC terminator), NOT the base64-decoded bytes. A real transmit payload
// is a long base64 string that decodes cleanly; an echoed response is a short
// status token: "OK", or a POSIX error name optionally followed by a ":message"
// (e.g. "ENOENT", "EINVAL:bad params"). Matching the decoded bytes instead let
// arbitrary binary chunks (a chafa/mpv direct stream) collide with the 'E'+A-Z
// shape ~0.04% of the time and silently drop a chunk, corrupting the image.
//
// The shape required is ^(OK|E[A-Z]+(:.*)?)$ with a hard length cap so that a
// legitimate (necessarily longer, mixed-case) base64 payload cannot match.
func isKittyResponse(payload string) bool {
	if len(payload) == 0 || len(payload) > 256 {
		return false
	}
	if payload == "OK" {
		return true
	}
	// POSIX error name: 'E' followed by one or more uppercase letters, then an
	// optional ":<message>". A base64 image payload is not all-uppercase.
	if payload[0] != 'E' {
		return false
	}
	i := 1
	for i < len(payload) && payload[i] >= 'A' && payload[i] <= 'Z' {
		i++
	}
	if i < 2 {
		// Need at least one uppercase letter after the leading 'E'.
		return false
	}
	if i == len(payload) {
		return true
	}
	return payload[i] == ':'
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
	// remoteClient is set when the host terminal is reached over the network
	// (SSH) and does not share the server's filesystem. See
	// KittyPassthroughOptions.RemoteClient.
	remoteClient bool
	hostOut      io.Writer
	hostMu       sync.Mutex // serializes writes to hostOut across render + async paths
	// hostScratch joins a multi-part sequence into one Write. Guarded by hostMu.
	hostScratch []byte
	// lastWriteNanos is how long the last host write took. Written under
	// hostMu, read from the VT callback without it, so it is atomic. See
	// hostBacklogged.
	lastWriteNanos atomic.Int64

	placements    map[string]map[uint32]*PassthroughPlacement
	imageIDMap    map[string]map[uint32]uint32 // maps (windowID, guestImageID) -> hostImageID
	nextHostID    uint32
	pendingOutput []byte

	// lastFrameHash is the CRC32 of the last bitmap sent per (windowID,
	// hostImageID) remote video stream. A browser re-sends identical frames
	// while idle (only a cursor blink changes), so skipping an unchanged one
	// avoids a compress + base64 + ssh write for nothing - the biggest idle-load
	// and lag win. A CRC collision at worst holds one stale frame until the next
	// differing one, which for a video stream is a few milliseconds.
	lastFrameHash map[string]map[uint32]uint32

	// overlayActive is true while a full-screen overlay (help, palette, etc.) is
	// showing. While set, self-placed remote video frames are dropped so a new
	// frame cannot redraw over the overlay; see SetOverlayActive.
	overlayActive bool

	// remoteVideo tracks self-placed remote video streams by (windowID ->
	// hostImageID -> state). These images are placed by the frame stream (a=T),
	// not by the normal placements map, but RefreshAllPlacements still needs
	// their geometry to follow a window drag/resize (re-emitting a=p from the
	// resident image, no re-transmit), to clear them when the pane leaves the
	// screen, and to re-show them after an overlay closes.
	remoteVideo map[string]map[uint32]*remoteVideoState

	// Async video frame writer. Video apps (mpv, youterm) send 30+ fps of
	// large image data. Processing synchronously inside the VT callback
	// blocks the bubbletea render loop and makes the entire UI unresponsive.
	// Instead we enqueue frames to this channel; a background goroutine
	// drains it and writes to hostOut. Channel capacity 1 means we always
	// keep at most one pending frame; newer frames replace older ones.
	asyncFrameCh chan asyncFrame

	// frozenThisPass is scratch space for RefreshAllPlacements: the set of
	// windows whose placements are held untouched this pass because an
	// interactive resize is in progress. Reused across frames to avoid a
	// per-frame map allocation.
	frozenThisPass map[string]bool

	// Pending direct transmission data (for chunked transfers)
	pendingDirectData map[string]*pendingDirectTransmit // key: windowID

	// lastBitmap and directFrames back the damage path: the bitmap the host
	// currently holds for each image, and the transmission being assembled
	// before it is compared with that bitmap. See kitty_damage.go.
	lastBitmap   map[string]map[uint32]*bitmapCache
	directFrames map[string]*directFrame

	// Screen dimensions (updated by RefreshAllPlacements)
	screenWidth  int
	screenHeight int

	// resizeFreezeSize records, per window, the size a placement was last laid
	// out at while that window is being manipulated. It exists to suppress the
	// per-tick re-placement churn during an interactive resize; see
	// RefreshAllPlacements.
	resizeFreezeSize map[string][2]int
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

// remoteVideoState is the geometry needed to re-place a self-placed video image
// with a=p (no re-transmit) when its window moves/resizes or an overlay closes.
//
// The desired on-screen geometry (hostX/hostY/showCols/showRows/hidden) is
// OWNED by RefreshAllPlacements: after the initial handoff it is the only
// writer, recomputing it from the live window layout every render pass. The
// async frame writer only reads it, at write time, so a queued frame always
// paints at the freshest position rather than the one current when it was
// enqueued (which trails the pointer during a drag).
type remoteVideoState struct {
	guestX, guestY int  // cursor position within the window at transmit time
	cols, rows     int  // full display size in cells (capped to the pane content)
	altScreen      bool // the screen the image was placed on
	// Native pixel dimensions of the transmitted bitmap (s/v params), for
	// source-rect cropping when the visible cell area is clamped.
	pxWidth, pxHeight int
	// Desired host geometry, owned by RefreshAllPlacements (see above).
	hostX, hostY       int
	showCols, showRows int  // cell area clamped to the screen; 0 = full cols/rows
	hidden             bool // offscreen/occluded; frames are dropped while set
}

// showGeometry returns the desired placement rectangle for a self-placed video
// image: display cols/rows (clamped to the screen) and the source-rect crop in
// pixels (0,0 when no crop is needed). Callers must hold kp.mu.
func (st *remoteVideoState) showGeometry() (cols, rows, srcW, srcH int) {
	cols, rows = st.cols, st.rows
	if st.showCols > 0 && st.showCols < cols {
		cols = st.showCols
	}
	if st.showRows > 0 && st.showRows < rows {
		rows = st.showRows
	}
	if (cols < st.cols || rows < st.rows) && st.pxWidth > 0 && st.pxHeight > 0 &&
		st.cols > 0 && st.rows > 0 {
		srcW = st.pxWidth * cols / st.cols
		srcH = st.pxHeight * rows / st.rows
	}
	return cols, rows, srcW, srcH
}

// asyncFrame is one unit of work for the async frame writer: either a fully
// pre-built byte sequence (the browser-overlay path, position-independent) or a
// self-placed remote video job whose placement geometry is resolved at write
// time so a queued frame cannot paint at a position the window has left.
type asyncFrame struct {
	data []byte
	job  *remoteVideoJob
}

// remoteVideoJob carries a remote-terminal video frame's payload and transmit
// metadata. Placement geometry deliberately lives in remoteVideoState, not
// here: it is read under kp.mu when the frame is written.
type remoteVideoJob struct {
	windowID    string
	hostID      uint32
	format      vt.KittyGraphicsFormat
	compression vt.KittyGraphicsCompression
	width       int    // pixel width (s param)
	height      int    // pixel height (v param)
	encoded     string // base64 payload, encoded at enqueue time
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
	// the same PTY as bubbletea's text output to the browser. SSH mode passes
	// the ssh.Session so APC sequences reach the client's terminal.
	Output io.Writer
	// RemoteClient marks the host terminal as one reached over a network
	// (SSH), so it does not share the server's filesystem. File-medium kitty
	// transmissions (t=f/t=t/t=s) name a server-local path the client cannot
	// read, so they are re-encoded as direct (t=d) data. Unlike the browser's
	// inline-graphics mode this keeps native placement, clipping, and delete
	// behavior, which a real remote terminal still needs.
	RemoteClient bool
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
		remoteClient:      opts.RemoteClient,
		hostOut:           hostOut,
		placements:        make(map[string]map[uint32]*PassthroughPlacement),
		imageIDMap:        make(map[string]map[uint32]uint32),
		remoteVideo:       make(map[string]map[uint32]*remoteVideoState),
		lastFrameHash:     make(map[string]map[uint32]uint32),
		nextHostID:        1,
		pendingDirectData: make(map[string]*pendingDirectTransmit),
		asyncFrameCh:      make(chan asyncFrame, 1),
		resizeFreezeSize:  make(map[string][2]int),
	}
	go kp.asyncFrameWriter()
	return kp
}

// writeHostSequence writes parts to hostOut as one unit that is mutually
// exclusive with every other host write. Each *os.File.Write is only
// per-syscall atomic, so without a shared lock a multi-part DEC 2026
// syncBegin/data/syncEnd triple emitted from one goroutine can interleave
// with a triple from another, breaking the synchronized-update pairing and
// mixing two APC sequences. Every writer to kp.hostOut MUST funnel through
// here so that never happens.
//
// The parts are joined and handed over in one Write, because hostMu only
// orders the writers that take it and the renderer is not one of them. What
// orders those two is sharing one *os.File (see PostRenderWriter): the runtime
// locks it per Write, so a whole sequence handed over in one call is delivered
// whole. Emitting it as three - the sync brackets and the payload they wrap -
// left two seams a frame could be written into.
//
// Lock ordering: hostMu is the innermost host-output lock. Callers may hold
// kp.mu when they call this (kp.mu outer, hostMu inner); this method never
// acquires kp.mu, so there is no lock-order cycle and no deadlock.
func (kp *KittyPassthrough) writeHostSequence(parts ...[]byte) {
	if kp.hostOut == nil {
		return
	}
	kp.hostMu.Lock()
	defer kp.hostMu.Unlock()
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	if total == 0 {
		return
	}
	// Reused across writes (it is only ever touched under hostMu) so a video
	// stream does not allocate a frame-sized buffer per frame.
	kp.hostScratch = append(kp.hostScratch[:0], parts[0]...)
	for _, part := range parts[1:] {
		kp.hostScratch = append(kp.hostScratch, part...)
	}
	started := time.Now()
	_, _ = kp.hostOut.Write(kp.hostScratch)
	kp.lastWriteNanos.Store(int64(time.Since(started)))
}

// hostWriteBudget is how long a write to the host may take before the host is
// treated as not keeping up. Half a frame at 60fps: past that, the guest is
// producing faster than the terminal can consume, and every writer behind this
// one, including the render loop that carries keystrokes, waits for the
// difference.
const hostWriteBudget = 8 * time.Millisecond

// hostBacklogged reports whether the last write to the host ran long enough to
// mean it is not draining.
//
// It is the only backpressure signal there is. A pty write returns as soon as
// the kernel buffer takes the bytes, so a fast write says nothing, but a slow
// one says the buffer is full and the terminal is behind. Pacing on it is what
// keeps a guest that renders faster than the host can paint from queueing
// frames the user will never see in front of the input they are waiting on.
func (kp *KittyPassthrough) hostBacklogged() bool {
	return time.Duration(kp.lastWriteNanos.Load()) > hostWriteBudget
}

// WriteToHost writes graphics data directly to the host terminal,
// wrapped in synchronized update sequences to prevent tearing.
// asyncFrameWriter drains asyncFrameCh and writes video frames to hostOut
// in a background goroutine so the VT callback and render loop stay
// responsive during high-fps video playback.
func (kp *KittyPassthrough) asyncFrameWriter() {
	for frame := range kp.asyncFrameCh {
		if kp.hostOut == nil {
			continue
		}
		// Contain a panic from the host write to this one frame. This goroutine
		// is spawned by us, not by bubbletea, so a panic here is recovered by
		// nothing: it would crash the entire process (every SSH session on the
		// server), not just the pane. A dropped video frame is the correct
		// degradation; the drain loop keeps running for the next one.
		kp.writeFrameSafely(frame)
	}
}

// writeFrameSafely writes one async video frame, recovering from any panic so a
// single bad frame degrades to a drop instead of taking the process down.
func (kp *KittyPassthrough) writeFrameSafely(frame asyncFrame) {
	defer func() {
		if r := recover(); r != nil {
			kittyPassthroughLog("asyncFrameWriter: recovered panic writing frame: %v", r)
		}
	}()
	if frame.job != nil {
		kp.writeRemoteVideoFrame(frame.job)
		return
	}
	if len(frame.data) == 0 {
		return
	}
	kp.writeHostSequence(syncBegin, frame.data, syncEnd)
}

// writeRemoteVideoFrame writes one self-placed remote video frame. The
// placement geometry is read from remoteVideoState under kp.mu at WRITE time,
// not enqueue time, so a frame that sat in the channel while the window was
// dragged still paints on the pane's current content rectangle.
//
// After the write it re-reads the desired geometry: if RefreshAllPlacements
// moved the window while this frame was in flight, the render loop's a=p may
// have reached the host BEFORE this a=T (host writes are serialized but not
// ordered against the render flush), leaving the image at the stale position
// with no later correction. Emitting a follow-up a=p at the new geometry makes
// the two writers converge instead of fight.
func (kp *KittyPassthrough) writeRemoteVideoFrame(job *remoteVideoJob) {
	kp.mu.Lock()
	st := kp.remoteVideo[job.windowID][job.hostID]
	if st == nil || st.hidden || (kp.overlayActive && kp.remoteClient) {
		// The image is hidden (offscreen/occluded/overlay): drop the frame.
		// RefreshAllPlacements re-shows the resident image when it becomes
		// visible again, and the next frame after that paints fresh pixels.
		kp.mu.Unlock()
		return
	}
	x, y := st.hostX, st.hostY
	cols, rows, srcW, srcH := st.showGeometry()
	kp.mu.Unlock()

	frame := buildPlacedFrame(job, x, y, cols, rows, srcW, srcH)
	kp.writeHostSequence(syncBegin, frame, syncEnd)

	kp.mu.Lock()
	st = kp.remoteVideo[job.windowID][job.hostID]
	if st == nil || st.hidden || (kp.overlayActive && kp.remoteClient) {
		kp.mu.Unlock()
		return
	}
	nx, ny := st.hostX, st.hostY
	ncols, nrows, nsrcW, nsrcH := st.showGeometry()
	if nx == x && ny == y && ncols == cols && nrows == rows && nsrcW == srcW && nsrcH == srcH {
		kp.mu.Unlock()
		return
	}
	fix := buildVideoReplace(job.hostID, st)
	kp.mu.Unlock()
	kp.writeHostSequence(syncBegin, fix, syncEnd)
}

func (kp *KittyPassthrough) WriteToHost(data []byte) {
	if kp.hostOut == nil || len(data) == 0 {
		return
	}
	kp.writeHostSequence(syncBegin, data, syncEnd)
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
// Must be called while kp.mu is already held; the host write funnels through
// writeHostSequence, which takes hostMu (kp.mu outer, hostMu inner).
func (kp *KittyPassthrough) flushToHost() {
	if len(kp.pendingOutput) > 0 && kp.hostOut != nil {
		kp.writeHostSequence(syncBegin, kp.pendingOutput, syncEnd)
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
