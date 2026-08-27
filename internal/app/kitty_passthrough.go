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
	// inlineGraphics indicates the host terminal is the browser client, whose
	// vendored webterm bundle carries a kitty overlay that renders placements
	// as absolutely-positioned DOM canvases. In this mode, file-based
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
	// pacedUntilNanos is the wall time before which no further frame should be
	// sent, and writeInFlight says a write is happening now. Written under
	// hostMu, read from the VT callback without it, so both are atomic. See
	// hostBacklogged.
	pacedUntilNanos atomic.Int64
	writeInFlight   atomic.Bool

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

	// imagePixels is the pixel size a guest declared for each image it
	// transmitted, keyed by window and by the guest's own image id.
	//
	// A placement command carries no pixel dimensions: a=p says which image and
	// how many cells, and everything about how big that image is has to come
	// from the transmission that preceded it. Without it the only figure left
	// to divide by is the host's cell size, which assumes the guest drew one
	// cell's worth of pixels per cell. A guest that draws at any other scale -
	// a browser at a device pixel ratio above one is the ordinary case - then
	// has its source rectangle computed at the wrong scale, and the picture is
	// magnified by exactly the ratio between the two.
	//
	// Keyed by the guest's id rather than the host's because that is the id the
	// later a=p names, and it is known on every transmission path without
	// having to reproduce each path's id mapping.
	imagePixels map[string]map[uint32][2]int

	// frameHashMisses counts, per image, how many frames in a row have differed
	// from the one before them. A stream that never repeats itself gains nothing
	// from the comparison, so past a threshold it is only sampled; see
	// forwardFileFrameIsNew.
	frameHashMisses map[string]map[uint32]int

	// frameHashBuf is the scratch the frame comparison copies through. One
	// buffer for the life of the passthrough rather than one per frame: this
	// runs on every frame of every file-backed stream, and kp.mu is held
	// throughout, so there is only ever one reader of it.
	frameHashBuf []byte

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
	// AndPlace tracks whether the original chunk that created this pending
	// was a TransmitPlace (action T). Chafa sends first chunk as T (andPlace=true)
	// then subsequent chunks as t (andPlace=false). We track this so the final
	// chunk's PlacementResult is returned correctly for whitespace reservation.
	AndPlace bool
	// Position info from the first chunk (a=T command)
	WindowX int
	WindowY int
	// ContentCols/ContentRows are the cells the guest had been told it has when
	// the first chunk arrived, which is the box the bitmap being assembled was
	// drawn for.
	ContentCols    int
	ContentRows    int
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
	AbsoluteLine int // Absolute line position (scrollbackLen + cursorY at placement time)
	HostX        int
	HostY        int
	Cols         int
	Rows         int // Original image rows (before any capping)
	DisplayRows  int // Capped rows for initial display
	// ImageCols is the image's own width in cells, BEFORE it was capped to the
	// pane. Cols is the capped one, and the two are not interchangeable: the
	// image's pixels were divided into ImageCols, so that is the number a
	// pixels-per-cell has to be derived from, and the difference between them
	// is exactly how much of the image does not fit and must be cropped away
	// rather than squeezed in. Rows is already the uncapped count (DisplayRows
	// is its capped twin), so there is no ImageRows to go with this.
	ImageCols int
	Hidden    bool // True when placement is completely out of view
	DataDirty bool // Image data was re-transmitted, so the placement must be re-sent

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
	// imgCols/imgRows are the image's OWN size in cells, before that cap. The
	// pixels below were divided into these, so these are what a fraction of the
	// image has to be measured against; cols/rows above are what fits, which is
	// a different number the moment the image is bigger than its pane.
	imgCols, imgRows int
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
	// Measured against the image's own cell footprint, not against the capped
	// one. Capping is where a frame first stops fitting, and comparing with the
	// capped number cannot see that: an image wider than its pane has
	// cols == st.cols, so no crop was emitted and the whole bitmap was scaled
	// into the pane instead - a squeeze on one axis, which is a stretched
	// picture. The fraction shown is exact rather than a cell-size estimate,
	// because the pixel count and the cell count both came off the same
	// transmit.
	imgCols, imgRows := st.imgCols, st.imgRows
	if imgCols <= 0 {
		imgCols = st.cols
	}
	if imgRows <= 0 {
		imgRows = st.rows
	}
	if (cols < imgCols || rows < imgRows) && st.pxWidth > 0 && st.pxHeight > 0 &&
		imgCols > 0 && imgRows > 0 {
		srcW = st.pxWidth * cols / imgCols
		srcH = st.pxHeight * rows / imgRows
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
	WindowX        int
	WindowY        int
	ContentOffsetX int
	ContentOffsetY int
	Width          int
	Height         int
	// ContentWidth/ContentHeight are the cells the guest has been told it has.
	// Every measurement of an image against its pane uses these rather than
	// Width and Height less the border allowance, because the guest draws to
	// what it was told and a bitmap can only match that. See
	// terminal.GeometrySnapshot.
	ContentWidth       int
	ContentHeight      int
	Visible            bool
	ScrollbackLen      int  // Total scrollback lines
	ScrollOffset       int  // Current scroll offset (0 = at bottom)
	IsBeingManipulated bool // True when window is being dragged/resized
	ScreenWidth        int  // Host terminal width
	ScreenHeight       int  // Host terminal height
	WindowZ            int  // Window z-index for occlusion detection
	IsAltScreen        bool // True when alternate screen is active (vim, less, etc.)
	// LayoutX/LayoutY/LayoutW/LayoutH are the screen cells the panes are laid
	// out in: the render area less the chrome the session reserves, which is
	// the sidebar rail's columns and the dock's rows.
	//
	// It is not the same box as the screen, and the difference is the whole
	// reason it is here. A pane is allowed to hang past this box - a floating
	// pane is only clamped far enough to keep a strip of it reachable - and
	// every cell tuios composes for such a pane still stops at the boundary,
	// because the rail and the dock are drawn over the pane layer. A kitty
	// placement is the one thing on screen tuios does not draw: the host paints
	// it over the finished frame, so unless the reserve reaches the placement
	// arithmetic the image runs straight over the rail.
	//
	// A zero W or H means the caller did not fill the box in, and the whole
	// screen is used instead.
	LayoutX int
	LayoutY int
	LayoutW int
	LayoutH int
}

// placementBoundsSlack stands in for a screen dimension the caller left unset,
// so an unbounded axis clamps nothing rather than clamping to zero.
const placementBoundsSlack = 1 << 30

// placementBounds is the half-open screen rectangle a placement may draw in:
// the pane layout box, further bounded by the screen itself.
func (info *WindowPositionInfo) placementBounds() (x0, y0, x1, y1 int) {
	x1, y1 = info.ScreenWidth, info.ScreenHeight
	if x1 <= 0 {
		x1 = placementBoundsSlack
	}
	if y1 <= 0 {
		y1 = placementBoundsSlack
	}
	if info.LayoutW > 0 {
		x0 = max(info.LayoutX, 0)
		x1 = min(x1, info.LayoutX+info.LayoutW)
	}
	if info.LayoutH > 0 {
		y0 = max(info.LayoutY, 0)
		y1 = min(y1, info.LayoutY+info.LayoutH)
	}
	return x0, y0, x1, y1
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
		frameHashMisses:   make(map[string]map[uint32]int),
		imagePixels:       make(map[string]map[uint32][2]int),
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
	kp.writeInFlight.Store(true)
	_, _ = kp.hostOut.Write(kp.hostScratch)
	kp.writeInFlight.Store(false)
	// The host earns a rest as long as the write it just took. See
	// hostBacklogged.
	kp.chargePacing(time.Since(started))
}

const (
	// pacingHoldFactor is how many times its own duration a write holds the
	// next frame back, which is the same as saying graphics may use at most
	// one part in five of the host's write capacity. The other four are what
	// the render loop needs to stay responsive, and it is the render loop that
	// carries the user's keystrokes.
	//
	// It scales itself. A damage patch writes in a millisecond and is held for
	// four, which no frame rate notices; a whole 900 KB bitmap writes in thirty
	// and is held for a hundred and twenty, which is exactly the stream that
	// has to slow down.
	pacingHoldFactor = 4
	// minPacedCost is the cost below which work is free. A placement, a delete
	// or a small damage patch costs tens of microseconds and holding the next
	// frame back for a multiple of that would be noise, so nothing is charged
	// for it. Only work that a frame at 60fps would actually notice paces the
	// stream behind it.
	minPacedCost = time.Millisecond
	// maxPacingHold caps the hold so one pathological write cannot freeze a
	// stream for seconds.
	maxPacingHold = 250 * time.Millisecond
)

// hostBacklogged reports whether a frame sent right now would be piling onto a
// host that has not finished with the last one.
//
// It is the only backpressure signal there is. A pty write returns as soon as
// the kernel buffer takes the bytes, so a fast write says nothing, but a slow
// one says the buffer is full and the terminal is behind.
//
// Two things make it true. A write in flight is the exact case: whatever this
// frame adds goes behind it. Past that, each write holds the next frame back
// for a multiple of its own cost, so an expensive stream is throttled in
// proportion to how expensive it is. A fixed threshold was tried first and
// behaved badly in both directions: it does not decay, so one slow write
// suppressed every frame until some unrelated graphics write reset it.
// chargePacing books the cost of one piece of graphics work against the
// stream, holding the next frame back for a multiple of it.
//
// Both kinds of cost are charged here, because both are paid out of the same
// budget. The write is the obvious one. The other is the work of preparing a
// frame at all, reading it off disk and base64 encoding a megabyte, which
// happens inside the lock the render loop needs and so delays a keystroke
// exactly as a slow write would, while being invisible to any measurement of
// the write itself.
func (kp *KittyPassthrough) chargePacing(cost time.Duration) {
	if cost < minPacedCost {
		return
	}
	hold := min(cost*pacingHoldFactor, maxPacingHold)
	until := time.Now().Add(hold).UnixNano()
	for {
		prev := kp.pacedUntilNanos.Load()
		if prev >= until || kp.pacedUntilNanos.CompareAndSwap(prev, until) {
			return
		}
	}
}

func (kp *KittyPassthrough) hostBacklogged() bool {
	if kp.writeInFlight.Load() {
		return true
	}
	until := kp.pacedUntilNanos.Load()
	return until != 0 && time.Now().UnixNano() < until
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
