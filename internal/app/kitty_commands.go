package app

import (
	"bytes"
	"encoding/base64"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

const (
	kittyChunkSize = 4096
)

type KittyCommandBuilder struct {
	buf bytes.Buffer
}

func NewKittyCommandBuilder() *KittyCommandBuilder {
	return &KittyCommandBuilder{}
}

func (b *KittyCommandBuilder) Reset() {
	b.buf.Reset()
	// Save cursor position at the start of graphics rendering
	b.buf.WriteString("\x1b7") // DECSC - Save cursor
}

func (b *KittyCommandBuilder) Finalize() {
	// Restore cursor position after graphics rendering
	b.buf.WriteString("\x1b8") // DECRC - Restore cursor
}

func (b *KittyCommandBuilder) Bytes() []byte {
	return b.buf.Bytes()
}

func (b *KittyCommandBuilder) String() string {
	return b.buf.String()
}

func (b *KittyCommandBuilder) TransmitImage(hostID uint32, img *vt.KittyImage) {
	if img == nil || len(img.Data) == 0 {
		return
	}

	encoded := base64.StdEncoding.EncodeToString(img.Data)

	if len(encoded) <= kittyChunkSize {
		b.writeAPC(buildTransmitParams(hostID, img, false), encoded)
		return
	}

	for offset := 0; offset < len(encoded); offset += kittyChunkSize {
		end := min(offset+kittyChunkSize, len(encoded))

		chunk := encoded[offset:end]
		more := end < len(encoded)

		if offset == 0 {
			b.writeAPC(buildTransmitParams(hostID, img, more), chunk)
		} else {
			b.writeAPC(buildContinueParams(more), chunk)
		}
	}
}

func (b *KittyCommandBuilder) PlaceImage(
	hostID uint32,
	placement *vt.KittyPlacement,
	hostX, hostY int,
	clipLeft, clipTop, clipRight, clipBottom int,
	cellWidth, cellHeight int,
) {
	if placement == nil {
		return
	}

	// Get original dimensions (0 means auto-size in Kitty protocol)
	originalCols := placement.Columns
	originalRows := placement.Rows

	// Calculate effective display dimensions after clipping
	// Only apply clipping if we have explicit dimensions
	cols := originalCols
	rows := originalRows

	if originalCols > 0 {
		cols = originalCols - clipLeft - clipRight
		if cols <= 0 {
			return // Completely clipped horizontally
		}
	}
	if originalRows > 0 {
		rows = originalRows - clipTop - clipBottom
		if rows <= 0 {
			return // Completely clipped vertically
		}
	}

	// Calculate effective source rectangle (source clipping in pixels)
	// Only apply source clipping if we have explicit dimensions
	effectiveSourceX := placement.SourceX
	effectiveSourceY := placement.SourceY
	effectiveSourceWidth := placement.SourceWidth
	effectiveSourceHeight := placement.SourceHeight

	if originalCols > 0 && cellWidth > 0 {
		effectiveSourceX = placement.SourceX + (clipLeft * cellWidth)
		if placement.SourceWidth > 0 {
			effectiveSourceWidth = max(0, placement.SourceWidth-((clipLeft+clipRight)*cellWidth))
		}
	}
	if originalRows > 0 && cellHeight > 0 {
		effectiveSourceY = placement.SourceY + (clipTop * cellHeight)
		if placement.SourceHeight > 0 {
			effectiveSourceHeight = max(0, placement.SourceHeight-((clipTop+clipBottom)*cellHeight))
		}
	}

	rendererDebugLog("PlaceImage: hostID=%d, hostPos=(%d,%d), cols=%d, rows=%d, clip=(%d,%d,%d,%d), cellSize=(%d,%d)",
		hostID, hostX, hostY, cols, rows, clipLeft, clipTop, clipRight, clipBottom, cellWidth, cellHeight)
	rendererDebugLog("PlaceImage: effectiveSource: x=%d, y=%d, w=%d, h=%d (original: x=%d, y=%d, w=%d, h=%d)",
		effectiveSourceX, effectiveSourceY, effectiveSourceWidth, effectiveSourceHeight,
		placement.SourceX, placement.SourceY, placement.SourceWidth, placement.SourceHeight)

	var params bytes.Buffer
	params.WriteString("a=p,i=")
	params.WriteString(strconv.FormatUint(uint64(hostID), 10))

	if placement.PlacementID > 0 {
		params.WriteString(",p=")
		params.WriteString(strconv.FormatUint(uint64(placement.PlacementID), 10))
	}

	if cols > 0 {
		params.WriteString(",c=")
		params.WriteString(strconv.Itoa(cols))
	}

	if rows > 0 {
		params.WriteString(",r=")
		params.WriteString(strconv.Itoa(rows))
	}

	if placement.XOffset > 0 {
		params.WriteString(",X=")
		params.WriteString(strconv.Itoa(placement.XOffset))
	}

	if placement.YOffset > 0 {
		params.WriteString(",Y=")
		params.WriteString(strconv.Itoa(placement.YOffset))
	}

	// Use effective source coordinates with clipping applied
	if effectiveSourceX > 0 {
		params.WriteString(",x=")
		params.WriteString(strconv.Itoa(effectiveSourceX))
	}

	if effectiveSourceY > 0 {
		params.WriteString(",y=")
		params.WriteString(strconv.Itoa(effectiveSourceY))
	}

	if effectiveSourceWidth > 0 {
		params.WriteString(",w=")
		params.WriteString(strconv.Itoa(effectiveSourceWidth))
	}

	if effectiveSourceHeight > 0 {
		params.WriteString(",h=")
		params.WriteString(strconv.Itoa(effectiveSourceHeight))
	}

	if placement.ZIndex != 0 {
		params.WriteString(",z=")
		params.WriteString(strconv.FormatInt(int64(placement.ZIndex), 10))
	}

	if placement.Virtual {
		params.WriteString(",U=1")
	}

	params.WriteString(",q=2")

	b.writeCursorMove(hostX, hostY)
	b.writeAPC(params.String(), "")
}

func (b *KittyCommandBuilder) DeleteAll() {
	b.writeAPC("a=d,d=a,q=2", "")
}

func (b *KittyCommandBuilder) DeleteByID(hostID uint32) {
	var params bytes.Buffer
	params.WriteString("a=d,d=i,i=")
	params.WriteString(strconv.FormatUint(uint64(hostID), 10))
	params.WriteString(",q=2")
	b.writeAPC(params.String(), "")
}

func (b *KittyCommandBuilder) DeleteVisible() {
	b.writeAPC("a=d,d=p,q=2", "")
}

func (b *KittyCommandBuilder) writeCursorMove(x, y int) {
	b.buf.WriteString("\x1b[")
	b.buf.WriteString(strconv.Itoa(y + 1))
	b.buf.WriteByte(';')
	b.buf.WriteString(strconv.Itoa(x + 1))
	b.buf.WriteByte('H')
}

func (b *KittyCommandBuilder) writeAPC(params, data string) {
	b.buf.WriteString("\x1b_G")
	b.buf.WriteString(params)
	if data != "" {
		b.buf.WriteByte(';')
		b.buf.WriteString(data)
	}
	b.buf.WriteString("\x1b\\")
}

func buildTransmitParams(hostID uint32, img *vt.KittyImage, more bool) string {
	var params bytes.Buffer
	params.WriteString("a=t,i=")
	params.WriteString(strconv.FormatUint(uint64(hostID), 10))

	switch img.Format {
	case vt.KittyFormatRGB:
		params.WriteString(",f=24")
	case vt.KittyFormatRGBA:
		params.WriteString(",f=32")
	case vt.KittyFormatPNG:
		params.WriteString(",f=100")
	default:
		params.WriteString(",f=32")
	}

	if img.Width > 0 {
		params.WriteString(",s=")
		params.WriteString(strconv.Itoa(img.Width))
	}

	if img.Height > 0 {
		params.WriteString(",v=")
		params.WriteString(strconv.Itoa(img.Height))
	}

	if more {
		params.WriteString(",m=1")
	}

	params.WriteString(",q=2")

	return params.String()
}

func buildContinueParams(more bool) string {
	if more {
		return "m=1,q=2"
	}
	return "m=0,q=2"
}
