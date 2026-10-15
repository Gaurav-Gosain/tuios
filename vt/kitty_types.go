package vt

// KittyGraphicsFormat is the pixel format of a kitty graphics upload, the
// f key.
type KittyGraphicsFormat uint8

const (
	KittyFormatRGB  KittyGraphicsFormat = 24  // f=24, raw 8-bit RGB
	KittyFormatRGBA KittyGraphicsFormat = 32  // f=32, raw 8-bit RGBA
	KittyFormatPNG  KittyGraphicsFormat = 100 // f=100, PNG data
)

// KittyGraphicsCompression is the compression of a kitty graphics payload,
// the o key.
type KittyGraphicsCompression uint8

const (
	KittyCompressionNone KittyGraphicsCompression = 0 // no o key
	KittyCompressionZlib KittyGraphicsCompression = 1 // o=z, zlib deflate
)

// KittyGraphicsAction is what a kitty graphics command asks for, the a key.
type KittyGraphicsAction byte

const (
	KittyActionQuery         KittyGraphicsAction = 'q' // probe support without storing an image
	KittyActionTransmit      KittyGraphicsAction = 't' // upload an image
	KittyActionTransmitPlace KittyGraphicsAction = 'T' // upload an image and place it
	KittyActionPlace         KittyGraphicsAction = 'p' // place an uploaded image
	KittyActionDelete        KittyGraphicsAction = 'd' // delete placements or images, see KittyDeleteTarget
	KittyActionFrame         KittyGraphicsAction = 'f' // send an animation frame
	KittyActionAnimation     KittyGraphicsAction = 'a' // control an animation
	KittyActionCompose       KittyGraphicsAction = 'c' // compose one frame onto another
)

// IsKittyAnimationAction reports whether a is one of the frame edits: a=f,
// a=a and a=c. A host either makes them or refuses them.
func IsKittyAnimationAction(a KittyGraphicsAction) bool {
	return a == KittyActionFrame || a == KittyActionAnimation || a == KittyActionCompose
}

// KittyAnimationRefusal is the answer to a frame edit the host cannot make.
// A guest that gets it falls back to sending whole frames.
const KittyAnimationRefusal = "ENOTSUPPORTED:host terminal does not support animation"

// KittyGraphicsMedium is how a kitty graphics upload carries its image, the
// t key.
type KittyGraphicsMedium byte

const (
	KittyMediumDirect       KittyGraphicsMedium = 'd' // image bytes in the payload
	KittyMediumFile         KittyGraphicsMedium = 'f' // payload names a file
	KittyMediumTempFile     KittyGraphicsMedium = 't' // payload names a temporary file to delete after reading
	KittyMediumSharedMemory KittyGraphicsMedium = 's' // payload names a shared memory object
)

// IsFile reports whether the medium names something on the machine the
// terminal runs on (a file, a temporary file or a shared memory object)
// instead of carrying the image bytes in the payload.
func (m KittyGraphicsMedium) IsFile() bool {
	return m == KittyMediumFile || m == KittyMediumTempFile || m == KittyMediumSharedMemory
}

// KittyDeleteTarget is what a kitty graphics delete takes down, the d key.
// A lower-case value removes placements only. Its upper-case form also
// frees the image data.
type KittyDeleteTarget byte

const (
	KittyDeleteAll          KittyDeleteTarget = 'a' // d=a, every placement on the screen
	KittyDeleteAllFree      KittyDeleteTarget = 'A' // d=A, as d=a and also frees the image data
	KittyDeleteByID         KittyDeleteTarget = 'i' // d=i, the placements of image i, or placement p of it
	KittyDeleteByIDFree     KittyDeleteTarget = 'I' // d=I, as d=i and also frees the image data
	KittyDeleteByNumber     KittyDeleteTarget = 'n' // d=n, as d=i for the newest image with number I
	KittyDeleteByNumberFree KittyDeleteTarget = 'N' // d=N, as d=n and also frees the image data
	KittyDeleteAtCursor     KittyDeleteTarget = 'c' // d=c, placements over the cursor cell
	KittyDeleteAtCursorFree KittyDeleteTarget = 'C' // d=C, as d=c and also frees the image data
	KittyDeleteAtCell       KittyDeleteTarget = 'p' // d=p, placements over the cell at x, y
	KittyDeleteAtCellFree   KittyDeleteTarget = 'P' // d=P, as d=p and also frees the image data
	KittyDeleteAtCellZ      KittyDeleteTarget = 'q' // d=q, placements over the cell at x, y with z-index z
	KittyDeleteAtCellZFree  KittyDeleteTarget = 'Q' // d=Q, as d=q and also frees the image data
	KittyDeleteAtColumn     KittyDeleteTarget = 'x' // d=x, placements over column x
	KittyDeleteAtColumnFree KittyDeleteTarget = 'X' // d=X, as d=x and also frees the image data
	KittyDeleteAtRow        KittyDeleteTarget = 'y' // d=y, placements over row y
	KittyDeleteAtRowFree    KittyDeleteTarget = 'Y' // d=Y, as d=y and also frees the image data
	KittyDeleteAtZIndex     KittyDeleteTarget = 'z' // d=z, placements at z-index z
	KittyDeleteAtZIndexFree KittyDeleteTarget = 'Z' // d=Z, as d=z and also frees the image data
	KittyDeleteIDRange      KittyDeleteTarget = 'r' // d=r, the placements of images with ids from x to y
	KittyDeleteIDRangeFree  KittyDeleteTarget = 'R' // d=R, as d=r and also frees the image data
	KittyDeleteFrames       KittyDeleteTarget = 'f' // d=f, the animation frames of image i
	KittyDeleteFramesFree   KittyDeleteTarget = 'F' // d=F, as d=f and also frees the image data
)

// KittyCommand is one parsed kitty graphics command (APC G). Each field
// holds one control key. A key the command does not carry leaves its zero
// value.
type KittyCommand struct {
	Action       KittyGraphicsAction      // a, what the command asks for
	Quiet        int                      // q, 1 drops OK replies and 2 drops every reply
	ImageID      uint32                   // i, the image id
	ImageNumber  uint32                   // I, the image number the terminal maps to an id
	PlacementID  uint32                   // p, the placement id
	Format       KittyGraphicsFormat      // f, the pixel format
	Medium       KittyGraphicsMedium      // t, how the image is carried
	Compression  KittyGraphicsCompression // o, the payload compression
	Width        int                      // s, the image width in pixels
	Height       int                      // v, the image height in pixels
	Size         int                      // S, how many bytes to read from a file
	Offset       int                      // O, where in a file to start reading
	More         bool                     // m=1, more chunks of this upload follow
	Delete       KittyDeleteTarget        // d, what a delete takes down
	XOffset      int                      // X, pixel offset inside the first cell
	YOffset      int                      // Y, pixel offset inside the first cell
	SourceX      int                      // x, left edge of the source rectangle in pixels
	SourceY      int                      // y, top edge of the source rectangle in pixels
	SourceWidth  int                      // w, width of the source rectangle in pixels
	SourceHeight int                      // h, height of the source rectangle in pixels
	Columns      int                      // c, columns the placement spans
	Rows         int                      // r, rows the placement spans
	ZIndex       int32                    // z, stacking order against text and other images
	CursorMove   int                      // C=1 leaves the cursor where it is
	Virtual      bool                     // U=1, a virtual placement for Unicode placeholders
	Data         []byte                   // decoded payload when the medium is direct
	RawPayload   string                   // Original base64 payload (preserved for passthrough without re-encoding)
	FilePath     string                   // decoded payload when the medium names a file

	// PayloadErr is set when the payload could not be decoded as base64. Data
	// and FilePath are then empty: the undecoded text is never image data.
	PayloadErr error

	// BackgroundColor is the Y key read as a 32-bit RGBA colour, which is what
	// a=f means by it. YOffset holds the same key read as a placement offset;
	// the two never apply to the same command, and a colour overflows int on a
	// 32-bit build, so it needs its own width.
	BackgroundColor uint32

	// pngHead is the start of a PNG upload's first chunk, decoded, so the
	// emulator can answer a corrupt one. See kittyPNGHeadError.
	pngHead []byte

	// otherKeys is set when the control data named a key other than the
	// image, image number and placement ids, which are the only keys a reply
	// from a terminal carries. See IsKittyEchoedResponse.
	otherKeys bool
}
