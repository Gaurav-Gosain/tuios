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
}

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
	FilePath     string
}

type KittyPendingChunk struct {
	ImageID     uint32
	ImageNumber uint32
	Format      KittyGraphicsFormat
	Medium      KittyGraphicsMedium
	Compression KittyGraphicsCompression
	Width       int
	Height      int
	DataBuffer  []byte
}
