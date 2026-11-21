package app

import (
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"tuios.dev/vt"
)

func kittyPassthroughLog(format string, args ...any) {
	if !debuglog.Enabled() {
		return
	}
	f, err := debuglog.Open(debuglog.Path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] KITTY-PASSTHROUGH: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type KittyPassthrough struct {
	hostScreenErased atomic.Bool

	mu              sync.Mutex
	enabled         bool
	inlineGraphics  bool
	remoteClient    bool
	caps            *HostCapabilities
	hostOut         io.Writer
	hostMu          sync.Mutex
	hostScratch     []byte
	pacedUntilNanos atomic.Int64
	writeInFlight   atomic.Bool

	placements      map[string]map[placementKey]*PassthroughPlacement
	placeIdx        map[string]*placementIndex
	imageIDMap      map[string]map[uint32]uint32
	virtualImages   map[string]map[uint32]bool
	freeLowIDs      map[uint32]struct{}
	occluderScratch []cellRect
	regionScratch   []cellRect
	sliceScratch    []placementSlice
	nextHostID      uint32
	pendingOutput   []byte

	syncProbes    map[string]func() (open bool, serial uint64)
	held          map[string]*heldUpdate
	heldBytes     int
	heldFrames    map[string]heldStreamFrame
	renderPasses  uint64
	heldGeom      map[string]paneGeom
	guestCapture  bool
	captureStart  int
	syncHoldLimit time.Duration
	clock         func() time.Time

	lastFrameHash map[string]map[uint32]uint32

	imagePixels map[string]map[uint32][2]int

	frameHashMisses map[string]map[uint32]int

	frameHashBuf []byte

	overlayActive bool

	chromeOccluders []cellRect

	remoteVideo map[string]map[uint32]*remoteVideoState

	asyncFrameCh chan asyncFrame

	asyncStop     chan struct{}
	asyncStopOnce sync.Once

	frozenThisPass map[string]bool

	pendingDirectData map[string]*pendingDirectTransmit

	discardingChunks map[string]bool

	lastBitmap   map[string]map[uint32]*bitmapCache
	directFrames map[string]*directFrame

	kept         map[string]map[uint32]*keptImage
	keptBytes    int
	keptSeq      uint64
	virtualBoxes map[string]map[uint32]map[uint32]virtualBox

	rawChunks map[string]bool

	screenWidth  int
	screenHeight int

	resizeFreezeSize map[string][2]int
}

type pendingDirectTransmit struct {
	Data           []byte
	Format         vt.KittyGraphicsFormat
	Compression    vt.KittyGraphicsCompression
	Width          int
	Height         int
	ImageID        uint32
	PlacementID    uint32
	Columns        int
	Rows           int
	SourceX        int
	SourceY        int
	SourceWidth    int
	SourceHeight   int
	XOffset        int
	YOffset        int
	ZIndex         int32
	Virtual        bool
	CursorMove     int
	AndPlace       bool
	WindowX        int
	WindowY        int
	ContentCols    int
	ContentRows    int
	ContentOffsetX int
	ContentOffsetY int
	CursorX        int
	CursorY        int
	ScrollbackLen  int
	IsAltScreen    bool
}

type placementSlice struct {
	HostX, HostY      int
	ClipTop, ClipLeft int
	ClipBottom        int
	Cols, Rows        int
}

type PassthroughPlacement struct {
	GuestImageID uint32
	HostImageID  uint32
	// PlacementID is the first of the host placement ids this record puts
	// up, one per slice. See kitty_placement_keys.go.
	PlacementID uint32
	// GuestPlacementID is the p= the guest gave, or 0.
	GuestPlacementID uint32
	slot             uint32
	seq              uint64
	sentAsIs         bool
	WindowID         string
	GuestX           int
	AbsoluteLine     int // Absolute line position (scrollbackLen + cursorY at placement time)
	HostX            int
	HostY            int
	Cols             int
	Rows             int // Original image rows (before any capping)
	DisplayRows      int // Capped rows for initial display
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
	// Native is an image drawn at its own pixels: the guest gave no cell box,
	// and its size or offset does not fill whole cells. See nativeScale.
	Native bool

	// Source clipping parameters (pixels), preserved for re-placement.
	SourceX      int
	SourceY      int
	SourceWidth  int
	SourceHeight int
	XOffset      int
	YOffset      int
	ZIndex       int32
	Virtual      bool

	// Image's NATIVE pixel dimensions as transmitted (from s/v params).
	// Used to derive an accurate pixels-per-cell for source-region cropping.
	// This is critical when client and daemon have different cell sizes (web
	// mode).
	ImagePixelWidth  int
	ImagePixelHeight int

	// Track which screen the image was placed on
	PlacedOnAltScreen bool // True if placed while alternate screen was active

	// Current clipping state (rows/cols to clip from each edge)
	ClipTop         int
	ClipBottom      int
	ClipLeft        int
	MaxShowable     int // Max rows that can be shown in current viewport
	MaxShowableCols int // Max cols that can be shown in current viewport

	// Slices are the rectangles of this image that are actually clear, in
	// screen cells. One placement can show one rectangle, so an image with a
	// window over its corner is drawn as two, and the host is sent one a=p per
	// slice. Empty means the whole image, which is what every path that sets
	// HostX/HostY directly produces.
	Slices       []placementSlice
	placedSlices int
}

type remoteVideoState struct {
	guestX, guestY     int
	cols, rows         int
	altScreen          bool
	imgCols, imgRows   int
	pxWidth, pxHeight  int
	hostX, hostY       int
	showCols, showRows int
	hidden             bool
}

func (st *remoteVideoState) showGeometry() (cols, rows, srcW, srcH int) {
	cols, rows = st.cols, st.rows
	if st.showCols > 0 && st.showCols < cols {
		cols = st.showCols
	}
	if st.showRows > 0 && st.showRows < rows {
		rows = st.showRows
	}
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

type asyncFrame struct {
	data []byte
	job  *remoteVideoJob
}

type remoteVideoJob struct {
	windowID    string
	hostID      uint32
	format      vt.KittyGraphicsFormat
	compression vt.KittyGraphicsCompression
	width       int
	height      int
	encoded     string
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
	// reason it is here. A pane is allowed to hang past this box (a floating
	// pane is only clamped far enough to keep a strip of it reachable), and
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

const placementBoundsSlack = 1 << 30

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
	// Caps is the terminal at the far end of this session. Nil falls back to
	// this process's own terminal, which is what a local attach wants.
	Caps *HostCapabilities
}

// NewKittyPassthroughWithOptions creates a passthrough with custom options.
func NewKittyPassthroughWithOptions(opts KittyPassthroughOptions) *KittyPassthrough {
	caps := opts.Caps
	if caps == nil {
		caps = GetHostCapabilities()
	}
	enabled := caps.KittyGraphics || opts.ForceEnable
	kittyPassthroughLog("NewKittyPassthrough: KittyGraphics=%v Force=%v TerminalName=%s", caps.KittyGraphics, opts.ForceEnable, caps.TerminalName)
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
		caps:              caps,
		hostOut:           hostOut,
		placements:        make(map[string]map[placementKey]*PassthroughPlacement),
		imageIDMap:        make(map[string]map[uint32]uint32),
		virtualImages:     make(map[string]map[uint32]bool),
		remoteVideo:       make(map[string]map[uint32]*remoteVideoState),
		frameHashMisses:   make(map[string]map[uint32]int),
		imagePixels:       make(map[string]map[uint32][2]int),
		lastFrameHash:     make(map[string]map[uint32]uint32),
		nextHostID:        1,
		pendingDirectData: make(map[string]*pendingDirectTransmit),
		asyncFrameCh:      make(chan asyncFrame, 1),
		asyncStop:         make(chan struct{}),
		resizeFreezeSize:  make(map[string][2]int),
	}
	go kp.asyncFrameWriter()
	return kp
}

func (kp *KittyPassthrough) hostCaps() *HostCapabilities {
	if kp.caps != nil {
		return kp.caps
	}
	return GetHostCapabilities()
}

const scratchRetentionBytes = 4 * 1024 * 1024

func releaseScratch(buf []byte) []byte {
	if cap(buf) > scratchRetentionBytes {
		return nil
	}
	return buf[:0]
}

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
	kp.hostScratch = append(kp.hostScratch[:0], parts[0]...)
	for _, part := range parts[1:] {
		kp.hostScratch = append(kp.hostScratch, part...)
	}
	started := time.Now()
	kp.writeInFlight.Store(true)
	_, _ = kp.hostOut.Write(kp.hostScratch)
	kp.writeInFlight.Store(false)
	kp.hostScratch = releaseScratch(kp.hostScratch)
	kp.chargePacing(time.Since(started))
}

const (
	pacingHoldFactor = 4
	minPacedCost     = time.Millisecond
	maxPacingHold    = 250 * time.Millisecond
)

func (kp *KittyPassthrough) hostBacklogged() bool {
	if kp.writeInFlight.Load() {
		return true
	}
	until := kp.pacedUntilNanos.Load()
	return until != 0 && time.Now().UnixNano() < until
}

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

func (kp *KittyPassthrough) asyncFrameWriter() {
	for {
		var frame asyncFrame
		select {
		case <-kp.asyncStop:
			return
		case frame = <-kp.asyncFrameCh:
		}
		if kp.hostOut == nil {
			continue
		}
		kp.writeFrameSafely(frame)
	}
}

func (kp *KittyPassthrough) stopAsyncWriter() {
	if kp == nil || kp.asyncStop == nil {
		return
	}
	kp.asyncStopOnce.Do(func() { close(kp.asyncStop) })
}

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

func (kp *KittyPassthrough) writeRemoteVideoFrame(job *remoteVideoJob) {
	kp.mu.Lock()
	st := kp.remoteVideo[job.windowID][job.hostID]
	if st == nil || st.hidden || (kp.overlayActive && kp.remoteClient) {
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

// WriteToHost writes graphics data directly to the host terminal,
// wrapped in synchronized update sequences to prevent tearing.
func (kp *KittyPassthrough) WriteToHost(data []byte) {
	if kp.hostOut == nil || len(data) == 0 {
		return
	}
	kp.writeHostSequence(syncBegin, data, syncEnd)
}

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
	return kp.takePending()
}

var (
	syncBegin = []byte("\x1b[?2026h")
	syncEnd   = []byte("\x1b[?2026l")
)

const maxPassthroughTransmitBytes = 64 * 1024 * 1024

const maxPendingGraphicsBytes = 8 * 1024 * 1024

func (kp *KittyPassthrough) pendingGraphicsFull() bool {
	return kp.pendingGraphicsBytes() > maxPendingGraphicsBytes
}

func (kp *KittyPassthrough) flushToHost() {
	if kp.hostOut == nil {
		return
	}
	kp.releaseDueHeld(false)
	if len(kp.pendingOutput) == 0 {
		return
	}
	kp.writeHostSequence(syncBegin, kp.pendingOutput, syncEnd)
	kp.pendingOutput = releaseScratch(kp.pendingOutput)
	kp.captureStart = 0
}

// HasQueued reports whether commands wait for the next frame to be written
// to the host.
func (kp *KittyPassthrough) HasQueued() bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	return kp.pendingGraphicsBytes() > 0
}

func (kp *KittyPassthrough) pendingGraphicsBytes() int {
	return len(kp.pendingOutput) + kp.heldBytes
}

func (kp *KittyPassthrough) releaseHostID(windowID string, hostID uint32) {
	if hostID == 0 {
		return
	}
	kp.pendingOutput = fmt.Appendf(kp.pendingOutput, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)
	delete(kp.virtualImages[windowID], hostID)
	kp.forgetKept(windowID, hostID)
	if hostID < lowHostIDs {
		if kp.freeLowIDs == nil {
			kp.freeLowIDs = make(map[uint32]struct{})
		}
		kp.freeLowIDs[hostID] = struct{}{}
	}
}

// HostImageID reports the id the host knows a window's guest image by.
func (kp *KittyPassthrough) HostImageID(windowID string, guestID uint32) (uint32, bool) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	hostID, ok := kp.imageIDMap[windowID][guestID]
	return hostID, ok
}

const placeholderImageIDLimit = 4096

// HostImageIDForPlaceholder is the id a placeholder cell is rewritten with.
// An image not transmitted yet gets its host id now: the protocol lets the
// cells be printed before the image, and a cell is rewritten once, as it is
// stored.
func (kp *KittyPassthrough) HostImageIDForPlaceholder(windowID string, guestID uint32) (uint32, bool) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if hostID, ok := kp.imageIDMap[windowID][guestID]; ok {
		return hostID, true
	}
	if guestID == 0 || len(kp.imageIDMap[windowID]) >= placeholderImageIDLimit {
		return 0, false
	}
	return kp.getOrAllocateHostID(windowID, guestID), true
}

const lowHostIDs = 256

func (kp *KittyPassthrough) allocateHostID() uint32 {
	if len(kp.freeLowIDs) > 0 {
		id := uint32(lowHostIDs)
		for free := range kp.freeLowIDs {
			id = min(id, free)
		}
		delete(kp.freeLowIDs, id)
		return id
	}
	id := kp.nextHostID
	kp.nextHostID++
	if kp.nextHostID == 0 {
		kp.nextHostID = 1
	}
	return id
}

func (kp *KittyPassthrough) calculateImageCells(cmd *vt.KittyCommand) (rows, cols int) {
	rows, cols = clampCells(cmd.Rows), clampCells(cmd.Columns)
	if rows > 0 && cols > 0 {
		return rows, cols
	}

	caps := kp.hostCaps()
	kittyPassthroughLog("calculateImageCells: imgPixels=(%d,%d), cmdRC=(%d,%d), cellSize=(%d,%d)",
		cmd.Width, cmd.Height, cmd.Columns, cmd.Rows, caps.CellWidth, caps.CellHeight)
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		srcW := sourceSpan(cmd.Width, cmd.SourceX, cmd.SourceWidth)
		srcH := sourceSpan(cmd.Height, cmd.SourceY, cmd.SourceHeight)
		switch {
		case cols > 0 && srcW > 0 && srcH > 0:
			rows = fitCells(float64(cols)*float64(caps.CellWidth)*float64(srcH),
				float64(srcW)*float64(caps.CellHeight))
		case rows > 0 && srcW > 0 && srcH > 0:
			cols = fitCells(float64(rows)*float64(caps.CellHeight)*float64(srcW),
				float64(srcH)*float64(caps.CellWidth))
		default:
			if rows == 0 {
				rows = fitCells(float64(cmd.Height+max(cmd.YOffset, 0)), float64(caps.CellHeight))
			}
			if cols == 0 {
				cols = fitCells(float64(cmd.Width+max(cmd.XOffset, 0)), float64(caps.CellWidth))
			}
		}
	}

	kittyPassthroughLog("calculateImageCells: result rows=%d, cols=%d", rows, cols)
	return rows, cols
}

func (kp *KittyPassthrough) nativeScale(cmd *vt.KittyCommand) bool {
	if kp.inlineGraphics || cmd.Columns > 0 || cmd.Rows > 0 {
		return false
	}
	if cmd.XOffset <= 0 && cmd.YOffset <= 0 && cmd.ZIndex == 0 {
		return false
	}
	caps := kp.hostCaps()
	if caps.CellWidth <= 0 || caps.CellHeight <= 0 {
		return false
	}
	w := sourceSpan(cmd.Width, cmd.SourceX, cmd.SourceWidth)
	h := sourceSpan(cmd.Height, cmd.SourceY, cmd.SourceHeight)
	if w <= 0 || h <= 0 {
		return false
	}
	return cmd.XOffset > 0 || cmd.YOffset > 0 || w%caps.CellWidth != 0 || h%caps.CellHeight != 0
}

const maxBoxCells = 1 << 16

func clampCells(n int) int {
	return min(max(n, 0), maxBoxCells)
}

func fitCells(pixels, cell float64) int {
	if pixels <= 0 || cell <= 0 {
		return 0
	}
	return int(min(math.Ceil(pixels/cell), maxBoxCells))
}

func sourceSpan(full, off, span int) int {
	off = max(off, 0)
	if full > 0 {
		rest := max(full-off, 0)
		if span <= 0 || span > rest {
			return rest
		}
	}
	return max(span, 0)
}
