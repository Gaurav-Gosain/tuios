package vt

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

// IsFile reports whether the medium names something on the machine the
// terminal runs on (a file, a temporary file or a shared memory object)
// instead of carrying the image bytes in the payload.
func (m KittyGraphicsMedium) IsFile() bool {
	return m == KittyMediumFile || m == KittyMediumTempFile || m == KittyMediumSharedMemory
}

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

	// PayloadErr is set when the payload could not be decoded as base64. Data
	// and FilePath are then empty: the undecoded text is never image data.
	PayloadErr error

	// BackgroundColor is the Y key read as a 32-bit RGBA colour, which is what
	// a=f means by it. YOffset holds the same key read as a placement offset;
	// the two never apply to the same command, and a colour overflows int on a
	// 32-bit build, so it needs its own width.
	BackgroundColor uint32

	// otherKeys is set when the control data named a key other than the
	// image, image number and placement ids, which are the only keys a reply
	// from a terminal carries. See IsKittyEchoedResponse.
	otherKeys bool
}
