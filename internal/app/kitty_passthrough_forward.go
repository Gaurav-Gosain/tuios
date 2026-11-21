package app

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"math"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/kittyshm"
	"tuios.dev/vt"
)

func zlibCompress(data []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

func paneContentCells(info *WindowPositionInfo) (int, int) {
	if info.ContentWidth > 0 && info.ContentHeight > 0 {
		return info.ContentWidth, info.ContentHeight
	}
	return info.Width - 2*info.ContentOffsetX, info.Height - 2*info.ContentOffsetY
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
	contentCols, contentRows int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	isAltScreen bool,
	ptyInput func([]byte),
) *PlacementResult {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if debuglog.Enabled() {
		log.Printf("[KP] ForwardCommand action=%c enabled=%v inline=%v imageID=%d more=%v dataLen=%d",
			cmd.Action, kp.enabled, kp.inlineGraphics, cmd.ImageID, cmd.More, len(cmd.Data))
	}
	kp.beginGuestCapture()
	defer kp.endGuestCapture(windowID)

	kittyPassthroughLog("ForwardCommand: action=%c, enabled=%v, imageID=%d, windowID=%s, win=(%d,%d), size=(%d,%d), cursor=(%d,%d), scrollback=%d, altScreen=%v",
		cmd.Action, kp.enabled, cmd.ImageID, windowID[:min(8, len(windowID))], windowX, windowY, contentCols, contentRows, cursorX, cursorY, scrollbackLen, isAltScreen)

	if cmd.Action == vt.KittyActionTransmit && len(cmd.RawPayload) > 0 && vt.IsKittyEchoedResponse(cmd) {
		kittyPassthroughLog("ForwardCommand: DISCARDING echoed response: %q", cmd.RawPayload)
		return nil
	}

	if !kp.enabled {
		kittyPassthroughLog("ForwardCommand: DISABLED, returning early")
		return nil
	}

	isTransmit := cmd.Action == vt.KittyActionTransmit || cmd.Action == vt.KittyActionTransmitPlace
	if cmd.PayloadErr != nil {
		kittyPassthroughLog("ForwardCommand: dropping action=%c with an undecodable payload: %v", cmd.Action, cmd.PayloadErr)
		delete(kp.pendingDirectData, windowID)
		delete(kp.directFrames, windowID)
		if isTransmit && cmd.More {
			if kp.discardingChunks == nil {
				kp.discardingChunks = make(map[string]bool)
			}
			kp.discardingChunks[windowID] = true
		}
		return nil
	}
	if isTransmit && kp.discardingChunks[windowID] {
		if !cmd.More {
			delete(kp.discardingChunks, windowID)
		}
		kittyPassthroughLog("ForwardCommand: dropping a chunk of a transmission that already failed")
		return nil
	}

	rawData, ok := kp.resolveImageNumber(cmd, rawData, windowID)
	if !ok {
		kittyPassthroughLog("ForwardCommand: dropping action=%c naming image number %d, which no image was sent under", cmd.Action, cmd.ImageNumber)
		return nil
	}

	if cmd.Action == vt.KittyActionTransmit || cmd.Action == vt.KittyActionTransmitPlace {
		w, h := cmd.Width, cmd.Height
		if (w <= 0 || h <= 0) && cmd.Format == vt.KittyFormatPNG && cmd.Medium == vt.KittyMediumDirect {
			if pw, ph, ok := pngSize(cmd.Data, cmd.Compression == vt.KittyCompressionZlib); ok {
				w, h = pw, ph
			}
		}
		kp.rememberImagePixels(windowID, cmd.ImageID, w, h)
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
	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		result := kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, contentCols, contentRows, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		if result != nil {
			return result
		}
		if cmd.Virtual {
			break
		}
		if !cmd.More {
			imgRows, imgCols := kp.calculateImageCells(cmd)
			if imgRows == 0 && imgCols == 0 && !isFileBased {
				if placements := kp.placements[windowID]; placements != nil {
					for _, p := range placements {
						if p.Hidden {
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
		kp.forwardPlace(cmd, windowID, windowX, windowY, contentCols, contentRows, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		if cmd.Virtual {
			break
		}
		imgRows, imgCols := kp.calculateImageCells(cmd)
		if imgRows > 0 || imgCols > 0 {
			return &PlacementResult{Rows: imgRows, Cols: imgCols, CursorMove: cmd.CursorMove}
		}

	case vt.KittyActionDelete:
		kittyPassthroughLog("ForwardCommand: handling DELETE, d=%c, imageID=%d", cmd.Delete, cmd.ImageID)
		kp.forwardDelete(cmd, windowID)

	case vt.KittyActionFrame, vt.KittyActionAnimation, vt.KittyActionCompose:
		kittyPassthroughLog("ForwardCommand: handling ANIMATION action=%c", cmd.Action)
		kp.forwardAnimation(cmd, rawData, windowID, ptyInput)

	default:
		kittyPassthroughLog("ForwardCommand: UNKNOWN action %c", cmd.Action)
	}

	return nil
}

func (kp *KittyPassthrough) forwardQuery(cmd *vt.KittyCommand, _ []byte, ptyInput func([]byte)) {
	if ptyInput == nil {
		kittyPassthroughLog("forwardQuery: NOT sending response, no ptyInput")
		return
	}

	ok := true
	errMsg := ""
	switch {
	case cmd.Medium.IsFile() && !kp.hostReadsFiles():
		ok = false
		errMsg = "ENOTSUPPORTED:host terminal cannot read files from this machine"
	case cmd.Medium == vt.KittyMediumSharedMemory && !kittyShmReadable():
		ok = false
		errMsg = "ENOTSUPPORTED:shared memory is not supported here"
	}

	if cmd.Quiet >= 2 || (cmd.Quiet >= 1 && ok) {
		kittyPassthroughLog("forwardQuery: suppressed by quiet=%d (ok=%v)", cmd.Quiet, ok)
		return
	}

	response := vt.BuildKittyResponse(ok, cmd.ImageID, errMsg)
	kittyPassthroughLog("forwardQuery: imageID=%d medium=%c ok=%v response=%q", cmd.ImageID, cmd.Medium, ok, response)
	ptyInput(response)
}

func (kp *KittyPassthrough) hostReadsFiles() bool {
	if kp.inlineGraphics || kp.remoteClient {
		return false
	}
	return kp.hostCaps().KittyFileTransfer
}

func (kp *KittyPassthrough) forwardTransmit(cmd *vt.KittyCommand, rawData []byte, windowID string, andPlace bool, windowX, windowY, contentCols, contentRows, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) *PlacementResult {
	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile {
		kp.forwardFileTransmit(cmd, windowID, andPlace, windowX, windowY, contentCols, contentRows, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		return nil
	}

	hasPendingData := kp.pendingDirectData[windowID] != nil
	if !andPlace && !hasPendingData {
		hostID := uint32(0)
		if cmd.ImageID != 0 {
			hostID = kp.getOrAllocateHostID(windowID, cmd.ImageID)
			rawData = rewriteKittyImageID(rawData, hostID)
		}
		if kp.absorbDirectFrame(cmd, rawData, windowID, hostID) {
			return nil
		}
		switch {
		case cmd.More:
			if kp.rawChunks == nil {
				kp.rawChunks = make(map[string]bool)
			}
			kp.rawChunks[windowID] = true
			if hostID != 0 {
				kp.forgetKept(windowID, hostID)
			}
		case kp.rawChunks[windowID]:
			delete(kp.rawChunks, windowID)
		default:
			kp.keepImage(windowID, hostID, keptFromCommand(cmd, cmd.Data))
		}
		kp.pendingOutput = append(kp.pendingOutput, rawData...)
		return nil
	}

	pending := kp.pendingDirectData[windowID]
	if pending == nil {
		pending = &pendingDirectTransmit{
			Format:         cmd.Format,
			Compression:    cmd.Compression,
			Width:          cmd.Width,
			Height:         cmd.Height,
			ImageID:        cmd.ImageID,
			PlacementID:    cmd.PlacementID,
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
			ContentCols:    contentCols,
			ContentRows:    contentRows,
			ContentOffsetX: contentOffsetX,
			ContentOffsetY: contentOffsetY,
			CursorX:        cursorX,
			CursorY:        cursorY,
			ScrollbackLen:  scrollbackLen,
			IsAltScreen:    isAltScreen,
		}
		kp.pendingDirectData[windowID] = pending
	}

	if len(pending.Data)+len(cmd.Data) > maxPassthroughTransmitBytes {
		kittyPassthroughLog("forwardTransmit: aborting oversized transmission (%d + %d bytes)",
			len(pending.Data), len(cmd.Data))
		delete(kp.pendingDirectData, windowID)
		return nil
	}
	pending.Data = append(pending.Data, cmd.Data...)

	kittyPassthroughLog("forwardTransmit: accumulated %d bytes, total=%d, more=%v",
		len(cmd.Data), len(pending.Data), cmd.More)

	if cmd.More {
		return nil
	}

	defer delete(kp.pendingDirectData, windowID)

	if len(pending.Data) == 0 {
		kittyPassthroughLog("forwardTransmit: no data accumulated, skipping")
		return nil
	}

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

	hostX := pending.WindowX + pending.ContentOffsetX + pending.CursorX
	hostY := pending.WindowY + pending.ContentOffsetY + pending.CursorY

	contentWidth, contentHeight := pending.ContentCols, pending.ContentRows

	if (pending.Width <= 0 || pending.Height <= 0) && pending.Format == vt.KittyFormatPNG {
		if w, h, ok := pngSize(pending.Data, pending.Compression == vt.KittyCompressionZlib); ok {
			pending.Width, pending.Height = w, h
		}
	}

	box := vt.KittyCommand{
		Columns:      pending.Columns,
		Rows:         pending.Rows,
		Width:        pending.Width,
		Height:       pending.Height,
		SourceX:      pending.SourceX,
		SourceY:      pending.SourceY,
		SourceWidth:  pending.SourceWidth,
		SourceHeight: pending.SourceHeight,
		XOffset:      pending.XOffset,
		YOffset:      pending.YOffset,
		ZIndex:       pending.ZIndex,
	}
	imgRows, imgCols := kp.calculateImageCells(&box)
	native := kp.nativeScale(&box)

	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	update := kp.emitBitmap(windowID, hostID, pending.Format, pending.Compression,
		pending.Width, pending.Height, pending.Data, pending.ImageID != 0)
	kp.keepImage(windowID, hostID, &keptImage{
		guestID: pending.ImageID, format: pending.Format, compression: pending.Compression,
		width: pending.Width, height: pending.Height, data: pending.Data,
	})
	if update == bitmapFull {
		if kp.pendingGraphicsFull() {
			kittyPassthroughLog("forwardTransmit: dropping a %d byte bitmap, %d bytes already queued",
				len(pending.Data), len(kp.pendingOutput))
		} else {
			kp.pendingOutput = append(kp.pendingOutput, kp.buildInlineChunks(
				hostID, pending.Format, pending.Compression,
				pending.Width, pending.Height, pending.Data)...)
		}
	}

	kittyPassthroughLog("forwardTransmit: %d bitmap bytes, update=%d, hostID=%d, imgSize=(%d,%d) srcXYWH=(%d,%d,%d,%d) imgPixels=(%d,%d)",
		len(pending.Data), update, hostID, imgCols, imgRows,
		pending.SourceX, pending.SourceY, pending.SourceWidth, pending.SourceHeight,
		pending.Width, pending.Height)

	if pending.AndPlace && pending.Virtual {
		kp.declareVirtualPlacement(windowID, hostID, pending.PlacementID, pending.Columns, pending.Rows, pending.ZIndex)
		return nil
	}

	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[placementKey]*PassthroughPlacement)
	}
	if existing := kp.placements[windowID][imageKey(hostID)]; existing != nil && update != bitmapFull {
		existing.DataDirty = existing.DataDirty || existing.drawnDifferently(pending.XOffset, pending.YOffset, pending.ZIndex, native) ||
			existing.HostX != hostX || existing.HostY != hostY
		existing.XOffset, existing.YOffset, existing.ZIndex, existing.Native = pending.XOffset, pending.YOffset, pending.ZIndex, native
		existing.HostX, existing.HostY = hostX, hostY
		existing.AbsoluteLine = pending.ScrollbackLen + pending.CursorY
		existing.GuestX = pending.CursorX
		existing.Cols = displayCols
		existing.ImageCols = imgCols
		existing.Rows = imgRows
		existing.DisplayRows = displayRows
		existing.SourceX = pending.SourceX
		existing.SourceY = pending.SourceY
		existing.SourceWidth = pending.SourceWidth
		existing.SourceHeight = pending.SourceHeight
		existing.ImagePixelWidth = pending.Width
		existing.ImagePixelHeight = pending.Height
		existing.PlacedOnAltScreen = pending.IsAltScreen
		if pending.AndPlace {
			return &PlacementResult{
				Rows:       imgRows,
				Cols:       imgCols,
				CursorMove: pending.CursorMove,
			}
		}
		return nil
	}
	kp.putPlacement(windowID, imageKey(hostID), &PassthroughPlacement{
		GuestImageID:      pending.ImageID,
		GuestPlacementID:  pending.PlacementID,
		HostImageID:       hostID,
		WindowID:          windowID,
		GuestX:            pending.CursorX,
		AbsoluteLine:      pending.ScrollbackLen + pending.CursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
		ImageCols:         imgCols,
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
		Native:            native,
		Hidden:            true,
		PlacedOnAltScreen: pending.IsAltScreen,
		ImagePixelWidth:   pending.Width,
		ImagePixelHeight:  pending.Height,
	})

	if pending.AndPlace {
		return &PlacementResult{
			Rows:       imgRows,
			Cols:       imgCols,
			CursorMove: pending.CursorMove,
		}
	}
	return nil
}

func (kp *KittyPassthrough) forwardFileTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, contentCols, contentRows, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) {
	if cmd.FilePath == "" {
		return
	}

	filePath, ok := kp.kittyMediumPath(cmd)
	if !ok {
		kittyPassthroughLog("forwardFileTransmit: refusing medium=%c path=%q", cmd.Medium, cmd.FilePath)
		return
	}

	kittyPassthroughLog("forwardFileTransmit: file=%s, andPlace=%v, medium=%c", filePath, andPlace, cmd.Medium)

	if !kp.hostReadsFiles() {
		kp.forwardFileTransmitInline(cmd, filePath, windowID, andPlace,
			windowX, windowY, contentCols, contentRows,
			contentOffsetX, contentOffsetY,
			cursorX, cursorY, scrollbackLen, isAltScreen)
		return
	}

	sized := *cmd
	sized.Width, sized.Height = kp.mediumPixels(cmd, windowID, filePath)

	if kp.imageIDMap[windowID] == nil {
		kp.imageIDMap[windowID] = make(map[uint32]uint32)
	}

	hostID, reusingID := kp.imageIDMap[windowID][cmd.ImageID]
	if !reusingID || cmd.ImageID == 0 {
		hostID = kp.allocateHostID()
		if cmd.ImageID != 0 {
			kp.imageIDMap[windowID][cmd.ImageID] = hostID
		}
	} else if andPlace {
		if p := kp.placements[windowID][imageKey(hostID)]; p != nil {
			imgRows, imgCols := kp.calculateImageCells(&sized)
			if (p.Rows != imgRows || p.Cols != imgCols) && !p.Native {
				kp.deleteOnePlacement(p)
				kp.dropPlacement(windowID, imageKey(hostID))
			}
		}
	}
	kittyPassthroughLog("forwardFileTransmit: mapped guestID=%d -> hostID=%d for window=%s", cmd.ImageID, hostID, windowID[:min(8, len(windowID))])

	native := kp.nativeScale(&sized)
	if !kp.forwardFileFrameIsNew(filePath, windowID, hostID, cmd) {
		if existing := kp.placements[windowID][imageKey(hostID)]; existing != nil && andPlace {
			existing.DataDirty = existing.DataDirty || existing.drawnDifferently(cmd.XOffset, cmd.YOffset, cmd.ZIndex, native) ||
				existing.HostX != windowX+contentOffsetX+cursorX || existing.HostY != windowY+contentOffsetY+cursorY
			existing.XOffset, existing.YOffset, existing.ZIndex, existing.Native = cmd.XOffset, cmd.YOffset, cmd.ZIndex, native
			if native {
				existing.Rows, existing.ImageCols = kp.calculateImageCells(&sized)
				existing.Cols = min(existing.ImageCols, max(contentCols, 1))
				existing.DisplayRows = min(existing.Rows, max(contentRows, 1))
			}
			existing.GuestX = cursorX
			existing.AbsoluteLine = scrollbackLen + cursorY
			existing.HostX = windowX + contentOffsetX + cursorX
			existing.HostY = windowY + contentOffsetY + cursorY
			existing.PlacedOnAltScreen = isAltScreen
		}
		kittyPassthroughLog("forwardFileTransmit: identical frame for hostID=%d, sending nothing", hostID)
		releaseKittyMedium(cmd, filePath)
		return
	}

	if andPlace || cmd.ImageID != 0 {
		kp.keepFileMedium(cmd, windowID, hostID, filePath, !reusingID || cmd.ImageID == 0)
	}

	encodePath := cmd.FilePath
	if cmd.Medium != vt.KittyMediumSharedMemory {
		encodePath = filePath
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(encodePath))

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	contentWidth, contentHeight := contentCols, contentRows

	imgRows, imgCols := kp.calculateImageCells(&sized)

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

	var buf bytes.Buffer
	buf.WriteString("\x1b_G")

	medium := "f"
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		medium = "s"
	case vt.KittyMediumTempFile:
		medium = "t"
	}

	action := "t"

	fmt.Fprintf(&buf, "a=%s,t=%s,i=%d,f=%d,s=%d,v=%d,q=2",
		action, medium, hostID, cmd.Format, cmd.Width, cmd.Height)
	if cmd.Compression == vt.KittyCompressionZlib {
		buf.WriteString(",o=z")
	}
	if displayCols > 0 && !native {
		fmt.Fprintf(&buf, ",c=%d", displayCols)
	}
	if displayRows > 0 && !native {
		fmt.Fprintf(&buf, ",r=%d", displayRows)
	}
	if cmd.SourceX > 0 {
		fmt.Fprintf(&buf, ",x=%d", cmd.SourceX)
	}
	if cmd.SourceY > 0 {
		fmt.Fprintf(&buf, ",y=%d", cmd.SourceY)
	}
	sourceWidth, sourceHeight := cmd.SourceWidth, cmd.SourceHeight
	if sourceWidth == 0 && displayCols < imgCols && imgCols > 0 && sized.Width > 0 {
		sourceWidth = displayCols * (sized.Width / imgCols)
	}
	if sourceHeight == 0 && displayRows < imgRows && imgRows > 0 && sized.Height > 0 {
		sourceHeight = displayRows * (sized.Height / imgRows)
	}
	if sourceWidth > 0 {
		fmt.Fprintf(&buf, ",w=%d", sourceWidth)
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

	isVideoFrame := reusingID && andPlace && cmd.More

	if andPlace && cmd.Virtual {
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
		kp.declareVirtualPlacement(windowID, hostID, cmd.PlacementID, cmd.Columns, cmd.Rows, cmd.ZIndex)
		return
	}

	placedNow := false
	if !isVideoFrame && reusingID && andPlace && kittyMediumIsOneFrame(cmd.Medium) &&
		kp.hostOut != nil && kp.streamFrameFitsPlacement(windowID, hostID, hostX, hostY,
		displayCols, displayRows, imgCols, imgRows, isAltScreen) {
		guestOpen, guestSerial := false, uint64(0)
		if probe := kp.syncProbes[windowID]; probe != nil {
			guestOpen, guestSerial = probe()
		}
		h := kp.held[windowID]
		firstInUpdate := guestOpen && (h == nil || h.serial != guestSerial || (len(h.buf) == 0 && !h.bypass))
		if guestOpen || h == nil {
			p := kp.placements[windowID][imageKey(hostID)]
			frame := bytes.Replace(buf.Bytes(), []byte("a=t,"),
				fmt.Appendf(nil, "a=T,p=%d,C=1,", p.PlacementID), 1)
			start := len(kp.pendingOutput)
			kp.pendingOutput = append(kp.pendingOutput, "\x1b7"...)
			kp.pendingOutput = fmt.Appendf(kp.pendingOutput, "\x1b[%d;%dH", p.HostY+1, p.HostX+1)
			kp.pendingOutput = append(kp.pendingOutput, frame...)
			kp.pendingOutput = append(kp.pendingOutput, "\x1b8"...)
			switch {
			case firstInUpdate:
				kp.replaceHeldStreamFrame(windowID, hostID, cmd)
				kp.noteHeldStreamFrame(windowID, hostID, guestSerial, len(kp.pendingOutput)-start, cmd, filePath)
			case guestOpen:
			default:
				kp.flushToHost()
			}
			placedNow = true
		}
	} else if isVideoFrame && kp.hostOut != nil {
		bufBytes := bytes.Replace(buf.Bytes(), []byte("a=t,"), []byte("a=T,"), 1)

		visible := windowX >= 0 && windowY >= 0 && hostX >= 0 && hostY >= 0
		if visible && displayCols > 0 {
			visible = hostX+displayCols <= windowX+contentOffsetX+contentWidth
		}
		if visible && displayRows > 0 {
			visible = hostY+displayRows <= windowY+contentOffsetY+contentHeight
		}
		if visible && kp.screenWidth > 0 && kp.screenHeight > 0 {
			if hostX+displayCols > kp.screenWidth || hostY+displayRows > kp.screenHeight-1 {
				visible = false
			}
		}

		kp.unholdWindow(windowID)
		if visible {
			var posCmd []byte
			posCmd = append(posCmd, syncBegin...)
			posCmd = append(posCmd, fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1)...)
			posCmd = append(posCmd, bufBytes...)
			posCmd = append(posCmd, syncEnd...)
			kp.writeHostSequence(posCmd)
		} else {
			releaseKittyMedium(cmd, filePath)
			if hostID > 0 {
				var del []byte
				del = append(del, syncBegin...)
				del = append(del, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)...)
				del = append(del, syncEnd...)
				kp.writeHostSequence(del)
			}
		}
	} else {
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[placementKey]*PassthroughPlacement)
	}
	if existing := kp.placements[windowID][imageKey(hostID)]; existing != nil {
		existing.DataDirty = !placedNow
		if sized.Width > 0 && sized.Height > 0 {
			existing.ImagePixelWidth = sized.Width
			existing.ImagePixelHeight = sized.Height
		}
		if !andPlace {
			kittyPassthroughLog("forwardFileTransmit: replaced the data of hostID=%d, placement kept", hostID)
			return
		}
		existing.XOffset, existing.YOffset, existing.ZIndex, existing.Native = cmd.XOffset, cmd.YOffset, cmd.ZIndex, native
		existing.GuestX = cursorX
		existing.AbsoluteLine = scrollbackLen + cursorY
		existing.HostX = hostX
		existing.HostY = hostY
		existing.Cols = displayCols
		existing.ImageCols = imgCols
		existing.Rows = imgRows
		existing.DisplayRows = displayRows
		existing.SourceX = cmd.SourceX
		existing.SourceY = cmd.SourceY
		existing.SourceWidth = cmd.SourceWidth
		existing.SourceHeight = cmd.SourceHeight
		existing.PlacedOnAltScreen = isAltScreen
		kittyPassthroughLog("forwardFileTransmit: refreshed placement hostID=%d in place", hostID)
		return
	}
	if !andPlace {
		return
	}
	kp.putPlacement(windowID, imageKey(hostID), &PassthroughPlacement{
		GuestImageID:      cmd.ImageID,
		GuestPlacementID:  cmd.PlacementID,
		HostImageID:       hostID,
		WindowID:          windowID,
		GuestX:            cursorX,
		AbsoluteLine:      scrollbackLen + cursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
		ImageCols:         imgCols,
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
		Native:            native,
		Hidden:            true,
		PlacedOnAltScreen: isAltScreen,
		ImagePixelWidth:   sized.Width,
		ImagePixelHeight:  sized.Height,
	})
	kittyPassthroughLog("forwardFileTransmit: stored placement hostID=%d (hidden, waiting for refresh)", hostID)
}

func kittyMediumIsOneFrame(m vt.KittyGraphicsMedium) bool {
	return m == vt.KittyMediumSharedMemory || m == vt.KittyMediumTempFile
}

func (p *PassthroughPlacement) drawnDifferently(xOffset, yOffset int, z int32, native bool) bool {
	return p.XOffset != xOffset || p.YOffset != yOffset || p.ZIndex != z || p.Native != native
}

func (kp *KittyPassthrough) streamFrameFitsPlacement(
	windowID string, hostID uint32,
	hostX, hostY, displayCols, displayRows, imgCols, imgRows int,
	isAltScreen bool,
) bool {
	p := kp.placements[windowID][imageKey(hostID)]
	if p == nil || p.Hidden || p.DataDirty || p.PlacementID == 0 {
		return false
	}
	if p.PlacedOnAltScreen != isAltScreen {
		return false
	}
	if len(p.Slices) > 1 || p.placedSlices > 1 || p.ClipTop != 0 || p.ClipBottom != 0 || p.ClipLeft != 0 {
		return false
	}
	if displayCols != imgCols || displayRows != imgRows {
		return false
	}
	return p.HostX == hostX && p.HostY == hostY &&
		p.Cols == displayCols && p.Rows == imgRows &&
		p.MaxShowableCols == displayCols && p.MaxShowable == displayRows
}

func (kp *KittyPassthrough) forwardFileTransmitInline(
	cmd *vt.KittyCommand,
	filePath string,
	windowID string,
	andPlace bool,
	windowX, windowY, contentCols, contentRows int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	isAltScreen bool,
) {
	defer releaseKittyMedium(cmd, filePath)

	if kp.overlayActive && kp.remoteClient {
		return
	}

	if cmd.ImageID != 0 {
		if id, reusing := kp.imageIDMap[windowID][cmd.ImageID]; reusing && kp.hasLiveFrame(windowID, id) {
			if kp.hostBacklogged() {
				kittyPassthroughLog("forwardFileTransmitInline: early-drop reused frame (host still writing)")
				return
			}
			if len(kp.asyncFrameCh) > 0 {
				kittyPassthroughLog("forwardFileTransmitInline: early-drop reused frame (async busy)")
				return
			}
		}
	}

	prepStarted := time.Now()
	defer func() { kp.chargePacing(time.Since(prepStarted)) }()

	var info os.FileInfo
	var err error
	if name, isObject := kittyShmObject(filePath); isObject {
		info, err = kittyshm.Stat(name)
	} else {
		info, err = os.Stat(filePath)
	}
	if err != nil {
		kittyPassthroughLog("forwardFileTransmitInline: stat %s failed: %v", filePath, err)
		return
	}
	if !info.Mode().IsRegular() {
		kittyPassthroughLog("forwardFileTransmitInline: refusing non-regular file %s (mode %v)", filePath, info.Mode())
		return
	}
	if info.Size() > maxPassthroughTransmitBytes {
		kittyPassthroughLog("forwardFileTransmitInline: refusing oversized file %s (%d bytes)", filePath, info.Size())
		return
	}

	f, err := openKittyObject(filePath, 0)
	if err != nil {
		kittyPassthroughLog("forwardFileTransmitInline: open %s failed: %v", filePath, err)
		return
	}
	data, err := readFrameBytes(f, cmd)
	_ = f.Close()
	if err != nil {
		kittyPassthroughLog("forwardFileTransmitInline: read %s failed: %v", filePath, err)
		return
	}
	kittyPassthroughLog("forwardFileTransmitInline: read %d bytes from %s", len(data), filePath)

	if kp.remoteClient && cmd.ImageID != 0 {
		if existingID, reusing := kp.imageIDMap[windowID][cmd.ImageID]; reusing {
			h := crc32.ChecksumIEEE(data)
			if kp.lastFrameHash[windowID][existingID] == h {
				kittyPassthroughLog("forwardFileTransmitInline: skip unchanged frame (hostID=%d)", existingID)
				return
			}
			if kp.lastFrameHash[windowID] == nil {
				kp.lastFrameHash[windowID] = make(map[uint32]uint32)
			}
			kp.lastFrameHash[windowID][existingID] = h
		}
	}

	format := cmd.Format
	compression := cmd.Compression
	rawPixels := data
	if kp.remoteClient && compression == vt.KittyCompressionNone {
		if z := zlibCompress(data); z != nil && len(z) < len(data) {
			data = z
			compression = vt.KittyCompressionZlib
		}
	}

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
	kp.keepImage(windowID, hostID, keptFromCommand(cmd, rawPixels))

	imgRows, imgCols := kp.calculateImageCells(cmd)
	contentWidth, contentHeight := contentCols, contentRows
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

	if andPlace && cmd.Virtual {
		if kp.emitBitmap(windowID, hostID, format, cmd.Compression, cmd.Width, cmd.Height, rawPixels, cmd.ImageID != 0) == bitmapFull {
			if kp.pendingGraphicsFull() {
				kittyPassthroughLog("forwardFileTransmitInline: dropping a %d byte virtual frame, %d bytes already queued",
					len(data), kp.pendingGraphicsBytes())
				return
			}
			kp.pendingOutput = append(kp.pendingOutput, kp.buildInlineChunks(
				hostID, format, compression, cmd.Width, cmd.Height, data)...)
		}
		kp.declareVirtualPlacement(windowID, hostID, cmd.PlacementID, cmd.Columns, cmd.Rows, cmd.ZIndex)
		return
	}

	if reusingID && kp.remoteClient {
		if kp.remoteVideo[windowID] == nil {
			kp.remoteVideo[windowID] = make(map[uint32]*remoteVideoState)
		}
		st := kp.remoteVideo[windowID][hostID]
		if st == nil {
			st = &remoteVideoState{hostX: hostX, hostY: hostY}
			if p := kp.placements[windowID][imageKey(hostID)]; p != nil && (p.HostX != 0 || p.HostY != 0) {
				st.hostX, st.hostY = p.HostX, p.HostY
			}
			kp.remoteVideo[windowID][hostID] = st
		}
		kp.dropPlacement(windowID, imageKey(hostID))
		st.guestX, st.guestY = cursorX, cursorY
		st.cols, st.rows = displayCols, displayRows
		st.imgCols, st.imgRows = imgCols, imgRows
		st.altScreen = isAltScreen
		st.pxWidth, st.pxHeight = cmd.Width, cmd.Height

		kp.unholdWindow(windowID)
		job := &remoteVideoJob{
			windowID:    windowID,
			hostID:      hostID,
			format:      format,
			compression: compression,
			width:       cmd.Width,
			height:      cmd.Height,
			encoded:     base64.StdEncoding.EncodeToString(data),
		}
		select {
		case kp.asyncFrameCh <- asyncFrame{job: job}:
		default:
			kittyPassthroughLog("forwardFileTransmitInline: dropped frame (async channel full)")
		}
		return
	}

	if kp.canPatchBitmap(windowID, hostID) {
		switch kp.emitBitmap(windowID, hostID, format, cmd.Compression, cmd.Width, cmd.Height, rawPixels, true) {
		case bitmapUnchanged:
			kittyPassthroughLog("forwardFileTransmitInline: identical frame, sending nothing (hostID=%d)", hostID)
		case bitmapPatched:
			kittyPassthroughLog("forwardFileTransmitInline: patched hostID=%d instead of %d bytes", hostID, len(data))
		default:
			kp.pendingOutput = append(kp.pendingOutput, kp.buildInlineChunks(
				hostID, format, compression, cmd.Width, cmd.Height, data)...)
		}
		kp.trackInlinePlacement(cmd, windowID, hostID, hostX, hostY,
			displayCols, displayRows, imgCols, imgRows, cursorX, scrollbackLen, cursorY, isAltScreen)
		return
	}

	frameData := kp.buildInlineChunks(hostID, format, compression, cmd.Width, cmd.Height, data)

	kittyPassthroughLog("forwardFileTransmitInline: built %d bytes, hostID=%d, imgSize=(%d,%d), reusingID=%v",
		len(frameData), hostID, imgCols, imgRows, reusingID)

	if reusingID {
		kp.unholdWindow(windowID)
		select {
		case kp.asyncFrameCh <- asyncFrame{data: frameData}:
		default:
			kittyPassthroughLog("forwardFileTransmitInline: dropped frame (async channel full)")
		}
	} else if kp.pendingGraphicsFull() {
		kittyPassthroughLog("forwardFileTransmitInline: dropping a %d byte bitmap, %d bytes already queued",
			len(frameData), kp.pendingGraphicsBytes())
	} else {
		kp.pendingOutput = append(kp.pendingOutput, frameData...)
	}

	kp.trackInlinePlacement(cmd, windowID, hostID, hostX, hostY,
		displayCols, displayRows, imgCols, imgRows, cursorX, scrollbackLen, cursorY, isAltScreen)
	_ = andPlace
}

func (kp *KittyPassthrough) trackInlinePlacement(
	cmd *vt.KittyCommand,
	windowID string,
	hostID uint32,
	hostX, hostY, displayCols, displayRows, imgCols, imgRows, cursorX, scrollbackLen, cursorY int,
	isAltScreen bool,
) {
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[placementKey]*PassthroughPlacement)
	}
	if existing := kp.placements[windowID][imageKey(hostID)]; existing != nil {
		native := kp.nativeScale(cmd)
		existing.DataDirty = existing.DataDirty || existing.drawnDifferently(cmd.XOffset, cmd.YOffset, cmd.ZIndex, native)
		existing.XOffset, existing.YOffset, existing.ZIndex, existing.Native = cmd.XOffset, cmd.YOffset, cmd.ZIndex, native
		existing.Cols = displayCols
		existing.ImageCols = imgCols
		existing.Rows = imgRows
		existing.DisplayRows = displayRows
		existing.HostX = hostX
		existing.HostY = hostY
		existing.AbsoluteLine = scrollbackLen + cursorY
		existing.ImagePixelWidth = cmd.Width
		existing.ImagePixelHeight = cmd.Height
		return
	}
	kp.putPlacement(windowID, imageKey(hostID), &PassthroughPlacement{
		GuestImageID:      cmd.ImageID,
		GuestPlacementID:  cmd.PlacementID,
		HostImageID:       hostID,
		WindowID:          windowID,
		GuestX:            cursorX,
		AbsoluteLine:      scrollbackLen + cursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
		ImageCols:         imgCols,
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
		Native:            kp.nativeScale(cmd),
		Hidden:            true,
		PlacedOnAltScreen: isAltScreen,
		ImagePixelWidth:   cmd.Width,
		ImagePixelHeight:  cmd.Height,
	})
}

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

func buildPlacedFrame(job *remoteVideoJob, hostX, hostY, cols, rows, srcW, srcH int) []byte {
	const chunkSize = 4096
	var out bytes.Buffer
	out.WriteString("\x1b7")
	fmt.Fprintf(&out, "\x1b[%d;%dH", hostY+1, hostX+1)
	for i := 0; i < len(job.encoded); i += chunkSize {
		end := min(i+chunkSize, len(job.encoded))
		chunk := job.encoded[i:end]
		more := end < len(job.encoded)

		out.WriteString("\x1b_G")
		if i == 0 {
			fmt.Fprintf(&out, "a=T,i=%d,p=1,f=%d,s=%d,v=%d,q=2", job.hostID, job.format, job.width, job.height)
			if cols > 0 {
				fmt.Fprintf(&out, ",c=%d", cols)
			}
			if rows > 0 {
				fmt.Fprintf(&out, ",r=%d", rows)
			}
			if srcW > 0 && srcH > 0 {
				fmt.Fprintf(&out, ",x=0,y=0,w=%d,h=%d", srcW, srcH)
			}
			if job.compression == vt.KittyCompressionZlib {
				out.WriteString(",o=z")
			}
		} else {
			fmt.Fprintf(&out, "i=%d,q=2", job.hostID)
		}
		if more {
			out.WriteString(",m=1")
		}
		out.WriteByte(';')
		out.WriteString(chunk)
		out.WriteString("\x1b\\")
	}
	out.WriteString("\x1b8")
	return out.Bytes()
}

func buildVideoReplace(hostID uint32, st *remoteVideoState) []byte {
	cols, rows, srcW, srcH := st.showGeometry()
	var out bytes.Buffer
	out.WriteString("\x1b7")
	fmt.Fprintf(&out, "\x1b[%d;%dH", st.hostY+1, st.hostX+1)
	fmt.Fprintf(&out, "\x1b_Ga=p,i=%d,p=1,C=1,q=2", hostID)
	if cols > 0 {
		fmt.Fprintf(&out, ",c=%d", cols)
	}
	if rows > 0 {
		fmt.Fprintf(&out, ",r=%d", rows)
	}
	if srcW > 0 && srcH > 0 {
		fmt.Fprintf(&out, ",x=0,y=0,w=%d,h=%d", srcW, srcH)
	}
	out.WriteString("\x1b\\\x1b8")
	return out.Bytes()
}

func (kp *KittyPassthrough) forwardVirtualPlace(cmd *vt.KittyCommand, windowID string) {
	hostID := kp.getOrAllocateHostID(windowID, cmd.ImageID)
	kp.declareVirtualPlacement(windowID, hostID, cmd.PlacementID, cmd.Columns, cmd.Rows, cmd.ZIndex)
}

func (kp *KittyPassthrough) declareVirtualPlacement(windowID string, hostID, placementID uint32, cols, rows int, z int32) {
	kp.pendingOutput = append(kp.pendingOutput, buildVirtualPlace(hostID, placementID, cols, rows, z)...)

	if kp.virtualImages[windowID] == nil {
		kp.virtualImages[windowID] = make(map[uint32]bool)
	}
	kp.virtualImages[windowID][hostID] = true
	kp.keepVirtualBox(windowID, hostID, placementID, cols, rows, z)

	kittyPassthroughLog("declareVirtualPlacement: hostID=%d cols=%d rows=%d winID=%s",
		hostID, cols, rows, windowID[:min(8, len(windowID))])
}

func buildVirtualPlace(hostID, placementID uint32, cols, rows int, z int32) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x1b_Ga=p,U=1")
	fmt.Fprintf(&buf, ",i=%d", hostID)
	if placementID > 0 {
		fmt.Fprintf(&buf, ",p=%d", placementID)
	}
	if cols > 0 {
		fmt.Fprintf(&buf, ",c=%d", cols)
	}
	if rows > 0 {
		fmt.Fprintf(&buf, ",r=%d", rows)
	}
	if z != 0 {
		fmt.Fprintf(&buf, ",z=%d", z)
	}
	buf.WriteString(",q=2")
	buf.WriteString("\x1b\\")
	return buf.Bytes()
}

func (kp *KittyPassthrough) forwardPlace(
	cmd *vt.KittyCommand,
	windowID string,
	windowX, windowY int,
	contentCols, contentRows int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	isAltScreen bool,
) {
	if cmd.Virtual {
		kp.forwardVirtualPlace(cmd, windowID)
		return
	}

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	hostID := kp.getOrAllocateHostID(windowID, cmd.ImageID)

	contentWidth, contentHeight := contentCols, contentRows

	pixelW, pixelH := kp.imagePixelsFor(windowID, cmd.ImageID, cmd.Width, cmd.Height)
	imgRows, imgCols := kp.placeCells(cmd, pixelW, pixelH)
	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	var key placementKey
	var base uint32
	var oldSlices int
	if cmd.PlacementID != 0 {
		if slot, ok := kp.guestSlot(windowID, hostID, cmd.PlacementID); ok {
			key = placementKey{hostID, slot}
			if old := kp.placements[windowID][key]; old != nil {
				base, oldSlices = old.PlacementID, old.placedSlices
			}
		}
	}
	if base == 0 {
		key = placementKey{hostID, kp.freshSlot(windowID, hostID)}
		base = kp.allocBase(windowID, hostID)
	}

	if oldSlices > 1 {
		for i := 1; i < oldSlices; i++ {
			kp.pendingOutput = fmt.Appendf(kp.pendingOutput,
				"\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", hostID, base+uint32(i))
		}
	}
	var buf bytes.Buffer
	buf.WriteString("\x1b7")
	fmt.Fprintf(&buf, "\x1b[%d;%dH", hostY+1, hostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d,p=%d,C=1", hostID, base)
	sized := *cmd
	sized.Width, sized.Height = pixelW, pixelH
	native := kp.nativeScale(&sized)
	if displayCols > 0 && !native {
		fmt.Fprintf(&buf, ",c=%d", displayCols)
	}
	if displayRows > 0 && !native {
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
	buf.WriteString(",q=2")
	buf.WriteString("\x1b\\")
	buf.WriteString("\x1b8")

	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)

	placement := &PassthroughPlacement{
		GuestImageID:      cmd.ImageID,
		HostImageID:       hostID,
		PlacementID:       base,
		GuestPlacementID:  cmd.PlacementID,
		placedSlices:      1,
		sentAsIs:          true,
		WindowID:          windowID,
		GuestX:            cursorX,
		AbsoluteLine:      scrollbackLen + cursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
		ImageCols:         imgCols,
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
		Native:            native,
		ImagePixelWidth:   pixelW,
		ImagePixelHeight:  pixelH,
		PlacedOnAltScreen: isAltScreen,
	}
	kp.putPlacement(windowID, key, placement)
}

func (kp *KittyPassthrough) placeCells(cmd *vt.KittyCommand, pixelW, pixelH int) (rows, cols int) {
	if cmd.Width > 0 && cmd.Height > 0 {
		return kp.calculateImageCells(cmd)
	}
	sized := *cmd
	sized.Width, sized.Height = pixelW, pixelH
	return kp.calculateImageCells(&sized)
}

func (kp *KittyPassthrough) deleteAllWindowPlacements(windowID string) {
	kp.deleteWindowImages(windowID, false)
	kp.dropWindowPlacements(windowID)
	kp.forgetBitmaps(windowID, 0)
}

func (kp *KittyPassthrough) forwardDelete(cmd *vt.KittyCommand, windowID string) {
	kittyPassthroughLog("forwardDelete: delete=%c, imageID=%d, windowID=%s", cmd.Delete, cmd.ImageID, windowID[:min(8, len(windowID))])

	kp.forgetBitmaps(windowID, 0)

	switch cmd.Delete {
	case vt.KittyDeleteAll, 0:
		kp.deleteAllWindowPlacements(windowID)

	case vt.KittyDeleteByID:
		if hostID, ok := kp.imageIDMap[windowID][cmd.ImageID]; ok {
			kp.deleteImagePlacements(windowID, hostID, cmd.PlacementID)
			kp.forgetFrameHashes(windowID)
			kittyPassthroughLog("forwardDelete: deleted guestID=%d (hostID=%d)", cmd.ImageID, hostID)
		}

	case vt.KittyDeleteByIDFree:
		if hostID, ok := kp.imageIDMap[windowID][cmd.ImageID]; ok {
			left := cmd.PlacementID != 0 && kp.deleteImagePlacements(windowID, hostID, cmd.PlacementID)
			if !left {
				for k := range kp.placements[windowID] {
					if k.host == hostID {
						kp.dropPlacement(windowID, k)
					}
				}
				kp.pendingOutput = fmt.Appendf(kp.pendingOutput, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)
			}
			if cmd.PlacementID == 0 {
				delete(kp.virtualImages[windowID], hostID)
			}
			if !left {
				kp.forgetImagePixels(windowID, cmd.ImageID)
				kp.forgetKept(windowID, hostID)
			}
			kp.forgetFrameHashes(windowID)
			kittyPassthroughLog("forwardDelete: deleted guestID=%d (hostID=%d) with placement", cmd.ImageID, hostID)
		}

	case 'A':
		kp.freeWindowImages(windowID)

	default:
		if cmd.Delete != vt.KittyDeleteAtCell &&
			cmd.Delete != vt.KittyDeleteAtCursor &&
			cmd.Delete != vt.KittyDeleteAtCursorFree {
			kittyPassthroughLog("forwardDelete: delete type=%c (%d) takes every placement down", cmd.Delete, cmd.Delete)
		}
		kp.deleteAllWindowPlacements(windowID)
	}
}

func (kp *KittyPassthrough) freeWindowImages(windowID string) {
	kp.deleteAllWindowPlacements(windowID)
	for _, hostID := range kp.imageIDMap[windowID] {
		kp.releaseHostID(windowID, hostID)
	}
	kp.imageIDMap[windowID] = nil
	kp.forgetKept(windowID, 0)
	kp.forgetImagePixels(windowID, 0)
	kp.forgetFrameHashes(windowID)
	kp.deleteRemoteVideoImages(windowID)
	kp.deleteVirtualImages(windowID)
}

func (kp *KittyPassthrough) rememberImagePixels(windowID string, guestImageID uint32, w, h int) {
	if guestImageID == 0 || w <= 0 || h <= 0 {
		return
	}
	if kp.imagePixels == nil {
		kp.imagePixels = make(map[string]map[uint32][2]int)
	}
	if kp.imagePixels[windowID] == nil {
		kp.imagePixels[windowID] = make(map[uint32][2]int)
	}
	kp.imagePixels[windowID][guestImageID] = [2]int{w, h}
}

func (kp *KittyPassthrough) mediumPixels(cmd *vt.KittyCommand, windowID, path string) (w, h int) {
	w, h = cmd.Width, cmd.Height
	if (w <= 0 || h <= 0) && cmd.Format == vt.KittyFormatPNG && path != "" {
		if pw, ph, ok := kittyMediumPNGSize(path, cmd.Offset, cmd.Size, cmd.Compression == vt.KittyCompressionZlib); ok {
			w, h = pw, ph
		}
	}
	kp.rememberImagePixels(windowID, cmd.ImageID, w, h)
	return w, h
}

func pngSize(data []byte, zlibbed bool) (w, h int, ok bool) {
	head := data
	if zlibbed {
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return 0, 0, false
		}
		defer func() { _ = zr.Close() }()
		buf := make([]byte, 24)
		if _, err := io.ReadFull(zr, buf); err != nil {
			return 0, 0, false
		}
		head = buf
	}
	if len(head) < 24 || string(head[:8]) != "\x89PNG\r\n\x1a\n" || string(head[12:16]) != "IHDR" {
		return 0, 0, false
	}
	pw := binary.BigEndian.Uint32(head[16:20])
	ph := binary.BigEndian.Uint32(head[20:24])
	if pw == 0 || ph == 0 || pw > math.MaxInt32 || ph > math.MaxInt32 {
		return 0, 0, false
	}
	return int(pw), int(ph), true
}

func (kp *KittyPassthrough) imagePixelsFor(windowID string, guestImageID uint32, cmdW, cmdH int) (int, int) {
	if cmdW > 0 && cmdH > 0 {
		return cmdW, cmdH
	}
	if wh, ok := kp.imagePixels[windowID][guestImageID]; ok {
		return wh[0], wh[1]
	}
	return 0, 0
}

func (kp *KittyPassthrough) forgetImagePixels(windowID string, guestImageID uint32) {
	if guestImageID == 0 {
		delete(kp.imagePixels, windowID)
		return
	}
	if byID := kp.imagePixels[windowID]; byID != nil {
		delete(byID, guestImageID)
		if len(byID) == 0 {
			delete(kp.imagePixels, windowID)
		}
	}
}

const frameHashSampleEvery = 16

const frameHashMissesBeforeBackoff = 8

func (kp *KittyPassthrough) forwardFileFrameIsNew(
	filePath, windowID string, hostID uint32, cmd *vt.KittyCommand,
) bool {
	if cmd.ImageID == 0 {
		return true
	}
	if filePath == "" {
		return true
	}
	if kp.frameHashMisses == nil {
		kp.frameHashMisses = make(map[string]map[uint32]int)
	}
	if kp.frameHashMisses[windowID] == nil {
		kp.frameHashMisses[windowID] = make(map[uint32]int)
	}
	misses := kp.frameHashMisses[windowID][hostID]
	if misses >= frameHashMissesBeforeBackoff && misses%frameHashSampleEvery != 0 {
		kp.frameHashMisses[windowID][hostID] = misses + 1
		return true
	}

	f, err := openKittyObject(filePath, 0)
	if err != nil {
		return true
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPassthroughTransmitBytes {
		_ = f.Close()
		return true
	}
	if kp.frameHashBuf == nil {
		kp.frameHashBuf = make([]byte, 64*1024)
	}
	sum, err := hashFileFrame(f, cmd, kp.frameHashBuf)
	_ = f.Close()
	if err != nil {
		return true
	}

	if kp.lastFrameHash[windowID] == nil {
		kp.lastFrameHash[windowID] = make(map[uint32]uint32)
	}
	if prev, seen := kp.lastFrameHash[windowID][hostID]; seen && prev == sum {
		kp.frameHashMisses[windowID][hostID] = 0
		return false
	}
	kp.lastFrameHash[windowID][hostID] = sum
	kp.frameHashMisses[windowID][hostID] = misses + 1
	return true
}

func hashFileFrame(r io.Reader, cmd *vt.KittyCommand, buf []byte) (uint32, error) {
	h := crc32.New(frameHashTable())
	var header [16]byte
	binary.LittleEndian.PutUint32(header[0:], uint32(cmd.Width))
	binary.LittleEndian.PutUint32(header[4:], uint32(cmd.Height))
	binary.LittleEndian.PutUint32(header[8:], uint32(cmd.Format))
	binary.LittleEndian.PutUint32(header[12:], uint32(cmd.Compression))
	_, _ = h.Write(header[:])
	if _, err := io.CopyBuffer(h, io.LimitReader(r, maxPassthroughTransmitBytes), buf); err != nil {
		return 0, err
	}
	return h.Sum32(), nil
}

func (kp *KittyPassthrough) forgetFrameHashes(windowID string) {
	delete(kp.lastFrameHash, windowID)
	delete(kp.frameHashMisses, windowID)
}

func readFrameBytes(f io.Reader, cmd *vt.KittyCommand) ([]byte, error) {
	limit := int64(maxPassthroughTransmitBytes)
	if _, isObject := f.(shmKittyObject); isObject && cmd.Medium == vt.KittyMediumSharedMemory {
		if n, ok := kittyshm.FrameBytes(cmd); ok && n < limit {
			limit = n
		}
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

var frameHashTable = sync.OnceValue(func() *crc32.Table { return crc32.MakeTable(crc32.Castagnoli) })
