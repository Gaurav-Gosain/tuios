package vt

import (
	"time"
)

type KittyGraphicsFormat uint8

const (
	KittyFormatRGB  KittyGraphicsFormat = 24
	KittyFormatRGBA KittyGraphicsFormat = 32
	KittyFormatPNG  KittyGraphicsFormat = 100
)

type KittyGraphicsCompression uint8

const (
	KittyCompressionNone KittyGraphicsCompression = 0
	KittyCompressionZlib KittyGraphicsCompression = 1
)

type KittyGraphicsAction byte

const (
	KittyActionQuery         KittyGraphicsAction = 'q'
	KittyActionTransmit      KittyGraphicsAction = 't'
	KittyActionTransmitPlace KittyGraphicsAction = 'T'
	KittyActionPlace         KittyGraphicsAction = 'p'
	KittyActionDelete        KittyGraphicsAction = 'd'
	KittyActionFrame         KittyGraphicsAction = 'f'
	KittyActionAnimation     KittyGraphicsAction = 'a'
	KittyActionCompose       KittyGraphicsAction = 'c'
)

type KittyGraphicsMedium byte

const (
	KittyMediumDirect       KittyGraphicsMedium = 'd'
	KittyMediumFile         KittyGraphicsMedium = 'f'
	KittyMediumTempFile     KittyGraphicsMedium = 't'
	KittyMediumSharedMemory KittyGraphicsMedium = 's'
)

type KittyDeleteTarget byte

const (
	KittyDeleteAll               KittyDeleteTarget = 'a'
	KittyDeleteByID              KittyDeleteTarget = 'i'
	KittyDeleteByIDAndPlacement  KittyDeleteTarget = 'I'
	KittyDeleteByNumber          KittyDeleteTarget = 'n'
	KittyDeleteByNumberPlacement KittyDeleteTarget = 'N'
	KittyDeleteAtCursor          KittyDeleteTarget = 'c'
	KittyDeleteAtCursorCell      KittyDeleteTarget = 'C'
	KittyDeleteAtColumn          KittyDeleteTarget = 'x'
	KittyDeleteAtRow             KittyDeleteTarget = 'y'
	KittyDeleteAtZIndex          KittyDeleteTarget = 'z'
	KittyDeleteOnScreen          KittyDeleteTarget = 'p'
	KittyDeleteByPlacementID     KittyDeleteTarget = 'P'
	KittyDeleteIntersectCursor   KittyDeleteTarget = 'q'
	KittyDeleteIntersectColumn   KittyDeleteTarget = 'X'
	KittyDeleteIntersectRow      KittyDeleteTarget = 'Y'
	KittyDeleteIntersectCell     KittyDeleteTarget = 'Q'
)

type KittyImage struct {
	ID           uint32
	Number       uint32
	Width        int
	Height       int
	Format       KittyGraphicsFormat
	Compression  KittyGraphicsCompression
	Data         []byte
	TransmitTime time.Time

	// Animation. Data holds the root frame (frame 1) and Frames holds every
	// frame after it, so a still image costs nothing extra. RootGap is frame
	// 1's own gap. Reach for these through frameData/frameGap rather than
	// indexing, which keeps the off-by-one in one place.
	RootGap int
	Frames  []KittyFrame
	Anim    KittyAnimation
}

// KittyFrame is one frame of an animated image, after the root.
type KittyFrame struct {
	Data []byte
	Gap  int // milliseconds to hold this frame; <= 0 means the default gap
}

// KittyAnimationState is what a=a asked playback to do.
type KittyAnimationState uint8

const (
	// KittyAnimStopped is s=1: hold the current frame.
	KittyAnimStopped KittyAnimationState = iota
	// KittyAnimWaiting is s=2: play, then stop on the last frame and wait for
	// more frames to arrive.
	KittyAnimWaiting
	// KittyAnimRunning is s=3: play, looping as Loops says.
	KittyAnimRunning
)

// KittyAnimation is the playback state a=a controls. There is deliberately no
// timer behind it: the current frame is derived from Started whenever someone
// asks (see CurrentFrame), because a standing tick to advance an animation
// nobody is looking at is exactly the idle cost tuios refuses to pay.
type KittyAnimation struct {
	State   KittyAnimationState
	Current int // 1-based frame the animation was at when Started was set
	Loops   int // 0 = loop forever, n = stop after n plays
	Started time.Time
}

// defaultKittyGap is the gap kitty assumes for a frame that never declared
// one.
const defaultKittyGap = 40

type KittyPlacement struct {
	ImageID      uint32
	PlacementID  uint32
	ScreenX      int
	ScreenY      int
	AbsoluteLine int
	XOffset      int
	YOffset      int
	SourceX      int
	SourceY      int
	SourceWidth  int
	SourceHeight int
	Columns      int
	Rows         int
	ZIndex       int32
	CursorMove   int
	Virtual      bool
}

type KittyCommand struct {
	Action       KittyGraphicsAction
	Quiet        int
	ImageID      uint32
	ImageNumber  uint32
	PlacementID  uint32
	Format       KittyGraphicsFormat
	Medium       KittyGraphicsMedium
	Compression  KittyGraphicsCompression
	Width        int
	Height       int
	Size         int
	Offset       int
	More         bool
	Delete       KittyDeleteTarget
	XOffset      int
	YOffset      int
	SourceX      int
	SourceY      int
	SourceWidth  int
	SourceHeight int
	Columns      int
	Rows         int
	ZIndex       int32
	CursorMove   int
	Virtual      bool
	Data         []byte
	RawPayload   string // Original base64 payload (preserved for passthrough without re-encoding)
	FilePath     string

	// BackgroundColor is the Y key read as a 32-bit RGBA colour, which is what
	// a=f means by it. YOffset holds the same key read as a placement offset;
	// the two never apply to the same command, and a colour overflows int on a
	// 32-bit build, so it needs its own width.
	BackgroundColor uint32
}

type KittyPendingChunk struct {
	// Frame is set when the chunks belong to an a=f animation frame rather
	// than an image transmission; Command then carries the frame parameters
	// from the first chunk, which the later ones do not repeat.
	Frame       bool
	Command     KittyCommand
	ImageID     uint32
	ImageNumber uint32
	Format      KittyGraphicsFormat
	Medium      KittyGraphicsMedium
	Compression KittyGraphicsCompression
	Width       int
	Height      int
	DataBuffer  []byte
}
