package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

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
	windowWidth, windowHeight int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	isAltScreen bool,
	ptyInput func([]byte),
) *PlacementResult {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		log.Printf("[KP] ForwardCommand action=%c enabled=%v inline=%v imageID=%d more=%v dataLen=%d",
			cmd.Action, kp.enabled, kp.inlineGraphics, cmd.ImageID, cmd.More, len(cmd.Data))
	}
	kittyPassthroughLog("ForwardCommand: action=%c, enabled=%v, imageID=%d, windowID=%s, win=(%d,%d), size=(%d,%d), cursor=(%d,%d), scrollback=%d, altScreen=%v",
		cmd.Action, kp.enabled, cmd.ImageID, windowID[:min(8, len(windowID))], windowX, windowY, windowWidth, windowHeight, cursorX, cursorY, scrollbackLen, isAltScreen)

	// Detect and discard echoed responses to prevent feedback loops.
	// Responses have format "i=N;OK" or "i=N;ERROR_MSG" or just "OK"/"ERROR_MSG"
	// When parsed, they appear as transmit commands with Data="OK" or error message.
	// Real transmit commands have binary/base64 image data, not status strings.
	if cmd.Action == vt.KittyActionTransmit && len(cmd.Data) > 0 && isKittyResponse(cmd.Data) {
		kittyPassthroughLog("ForwardCommand: DISCARDING echoed response: %q", cmd.Data)
		return nil
	}

	if !kp.enabled {
		kittyPassthroughLog("ForwardCommand: DISABLED, returning early")
		return nil
	}

	// Clear virtual placements on any new image activity for this window
	// Virtual placements are inherently transient - they should be re-sent by the app if still needed
	if placements := kp.placements[windowID]; placements != nil {
		var virtualIDs []uint32
		for hostID, p := range placements {
			if p.Virtual {
				virtualIDs = append(virtualIDs, hostID)
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
			}
		}
		for _, id := range virtualIDs {
			delete(placements, id)
			kittyPassthroughLog("ForwardCommand: cleared stale virtual placement hostID=%d", id)
		}
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
		// On the final chunk of a chunked transmission that was part of a
		// previous TransmitPlace (chafa: T ... t ... t m=0), return the image
		// dimensions from the tracked placement so the guest terminal reserves
		// whitespace. Without this, the image appears but the cursor doesn't
		// advance below it, causing text to overdraw.
		if !cmd.More {
			if placements := kp.placements[windowID]; placements != nil {
				for _, p := range placements {
					if p.Streaming {
						return &PlacementResult{
							Rows:       p.Rows,
							Cols:       p.Cols,
							CursorMove: cmd.CursorMove,
						}
					}
				}
			}
		}

	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		result := kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Return PlacementResult from direct transmit if available
		if result != nil {
			return result
		}
		// On the final chunk (m=0), return image dimensions so the guest
		// terminal reserves whitespace for the image. This applies to BOTH
		// file-based AND direct transmissions (chafa uses direct with chunks).
		if !cmd.More {
			imgRows, imgCols := kp.calculateImageCells(cmd)
			// For direct mode where the final chunk doesn't have s/v params,
			// look up the stored placement from the first chunk.
			if imgRows == 0 && imgCols == 0 && !isFileBased {
				if placements := kp.placements[windowID]; placements != nil {
					for _, p := range placements {
						if p.Streaming || p.Hidden {
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
		kp.forwardPlace(cmd, windowID, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Return ORIGINAL image dimensions for whitespace reservation
		imgRows, imgCols := kp.calculateImageCells(cmd)
		if imgRows > 0 || imgCols > 0 {
			return &PlacementResult{Rows: imgRows, Cols: imgCols, CursorMove: cmd.CursorMove}
		}

	case vt.KittyActionDelete:
		kittyPassthroughLog("ForwardCommand: handling DELETE, d=%c, imageID=%d", cmd.Delete, cmd.ImageID)
		kp.forwardDelete(cmd, windowID)

	case vt.KittyActionFrame, vt.KittyActionAnimation, vt.KittyActionCompose:
		// Animation protocol (a=f, a=a, a=c) is not yet supported in passthrough.
		// These commands require consistent image ID management between the guest
		// app and host terminal which conflicts with tuios's ID remapping.
		// Apps like kitty-doom that use animation should be run directly in the
		// terminal instead of inside tuios.
		kittyPassthroughLog("ForwardCommand: DROPPING unsupported animation action=%c", cmd.Action)

	default:
		kittyPassthroughLog("ForwardCommand: UNKNOWN action %c", cmd.Action)
	}

	return nil
}

func (kp *KittyPassthrough) forwardQuery(cmd *vt.KittyCommand, _ []byte, ptyInput func([]byte)) {
	if ptyInput != nil && cmd.Quiet < 2 {
		response := vt.BuildKittyResponse(true, cmd.ImageID, "")
		kittyPassthroughLog("forwardQuery: sending response for imageID=%d, response=%q, ptyInput=%v", cmd.ImageID, response, ptyInput != nil)
		ptyInput(response)
	} else {
		kittyPassthroughLog("forwardQuery: NOT sending response, ptyInput=%v, quiet=%d", ptyInput != nil, cmd.Quiet)
	}
}

func (kp *KittyPassthrough) forwardTransmit(cmd *vt.KittyCommand, rawData []byte, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) *PlacementResult {
	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile {
		kp.forwardFileTransmit(cmd, windowID, andPlace, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen, isAltScreen)
		// Don't flush immediately  - accumulate in pendingOutput.
		// Flushed during render cycle (GetKittyGraphicsCmd) so graphics
		// and text arrive in the same frame, preventing tearing.
		return nil
	}

	// rawData includes the full APC framing: \x1b_G<params>;<data>\x1b\\
	// Strip the framing to get just the inner content for rewriting.
	innerData := rawData
	if len(innerData) >= 3 && innerData[0] == '\x1b' && innerData[1] == '_' {
		innerData = innerData[2:] // skip \x1b_
		if innerData[0] == 'G' {
			innerData = innerData[1:] // skip G
		}
	}
	if len(innerData) >= 2 && innerData[len(innerData)-2] == '\x1b' && innerData[len(innerData)-1] == '\\' {
		innerData = innerData[:len(innerData)-2] // strip \x1b\\
	}

	_ = innerData // innerData unused in this v0.6.0-style implementation

	hasPendingData := kp.pendingDirectData[windowID] != nil
	if !andPlace && !hasPendingData {
		// Pass through raw (already has framing)
		kp.pendingOutput = append(kp.pendingOutput, rawData...)
		return nil
	}

	// v0.6.0-style direct transmit: accumulate raw decoded bytes across chunks,
	// then on the final chunk re-encode and emit as properly-formatted kitty
	// APC chunks of our own. This avoids the mess of trying to splice chafa's
	// non-standard chunk format (params-only first chunk + data-only continuations).

	// Get or create pending transmission state
	pending := kp.pendingDirectData[windowID]
	if pending == nil {
		pending = &pendingDirectTransmit{
			Format:         cmd.Format,
			Compression:    cmd.Compression,
			Width:          cmd.Width,
			Height:         cmd.Height,
			ImageID:        cmd.ImageID,
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
			WindowWidth:    windowWidth,
			WindowHeight:   windowHeight,
			ContentOffsetX: contentOffsetX,
			ContentOffsetY: contentOffsetY,
			CursorX:        cursorX,
			CursorY:        cursorY,
			ScrollbackLen:  scrollbackLen,
			IsAltScreen:    isAltScreen,
		}
		kp.pendingDirectData[windowID] = pending
	}

	// Abort a runaway transmission before it exhausts memory. A guest can
	// stream endless m=1 chunks without ever sending m=0; kitty's own limit
	// is on this order (tens of MB).
	if len(pending.Data)+len(cmd.Data) > maxPassthroughTransmitBytes {
		kittyPassthroughLog("forwardTransmit: aborting oversized transmission (%d + %d bytes)",
			len(pending.Data), len(cmd.Data))
		delete(kp.pendingDirectData, windowID)
		return nil
	}
	pending.Data = append(pending.Data, cmd.Data...)

	kittyPassthroughLog("forwardTransmit: accumulated %d bytes, total=%d, more=%v",
		len(cmd.Data), len(pending.Data), cmd.More)

	// If more chunks coming, wait for them
	if cmd.More {
		return nil
	}

	// Final chunk  - process complete image
	defer delete(kp.pendingDirectData, windowID)

	if len(pending.Data) == 0 {
		kittyPassthroughLog("forwardTransmit: no data accumulated, skipping")
		return nil
	}

	// Get/allocate host ID.
	// - Guest image ID == 0 is kitty's "auto-assign" sentinel; each transmit
	//   with ID 0 is a DISTINCT image (chafa uses 0 for every invocation).
	//   Always allocate a fresh host ID so multiple chafa images coexist in
	//   scrollback without overwriting each other.
	// - For non-zero guest IDs, reuse the same host ID on re-transmit so the
	//   image data is replaced in place.
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

	// Re-encode to base64 and emit as properly-formatted kitty chunks
	encoded := base64.StdEncoding.EncodeToString(pending.Data)

	hostX := pending.WindowX + pending.ContentOffsetX + pending.CursorX
	hostY := pending.WindowY + pending.ContentOffsetY + pending.CursorY

	contentWidth := pending.WindowWidth - 2
	contentHeight := pending.WindowHeight - 2

	// Calculate image cell dimensions
	imgRows := pending.Rows
	imgCols := pending.Columns
	if imgRows == 0 || imgCols == 0 {
		caps := GetHostCapabilities()
		if caps.CellWidth > 0 && caps.CellHeight > 0 {
			if imgRows == 0 && pending.Height > 0 {
				imgRows = (pending.Height + caps.CellHeight - 1) / caps.CellHeight
			}
			if imgCols == 0 && pending.Width > 0 {
				imgCols = (pending.Width + caps.CellWidth - 1) / caps.CellWidth
			}
		}
	}

	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	// Emit transmit-only command in proper 4096-byte kitty chunks.
	// Placement is handled by RefreshAllPlacements.
	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		chunk := encoded[i:end]
		more := end < len(encoded)

		var buf bytes.Buffer
		buf.WriteString("\x1b_G")
		if i == 0 {
			// First chunk: full header
			fmt.Fprintf(&buf, "a=t,i=%d,f=%d,s=%d,v=%d,q=2",
				hostID, pending.Format, pending.Width, pending.Height)
			if pending.Compression == vt.KittyCompressionZlib {
				buf.WriteString(",o=z")
			}
		} else {
			// Continuation chunks: just image ID (no placement params for a=t)
			fmt.Fprintf(&buf, "i=%d,q=2", hostID)
		}
		if more {
			buf.WriteString(",m=1")
		}
		buf.WriteByte(';')
		buf.WriteString(chunk)
		buf.WriteString("\x1b\\")
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	kittyPassthroughLog("forwardTransmit: emitted %d bytes as %d-byte chunks, hostID=%d, imgSize=(%d,%d) srcXYWH=(%d,%d,%d,%d) imgPixels=(%d,%d)",
		len(encoded), chunkSize, hostID, imgCols, imgRows,
		pending.SourceX, pending.SourceY, pending.SourceWidth, pending.SourceHeight,
		pending.Width, pending.Height)

	// Track placement for RefreshAllPlacements
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	kp.placements[windowID][hostID] = &PassthroughPlacement{
		GuestImageID:      pending.ImageID,
		HostImageID:       hostID,
		WindowID:          windowID,
		GuestX:            pending.CursorX,
		AbsoluteLine:      pending.ScrollbackLen + pending.CursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
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
		Hidden:            true, // RefreshAllPlacements places it
		PlacedOnAltScreen: pending.IsAltScreen,
		// The image's native pixel dimensions from the s/v params. These are
		// what the image ACTUALLY has on disk/in kitty  - independent of the
		// client's notion of cell size. placeOne uses these to derive accurate
		// pixels-per-row for source-region cropping, which is critical in
		// web/daemon mode where the client and daemon may have different
		// terminal cell sizes.
		ImagePixelWidth:  pending.Width,
		ImagePixelHeight: pending.Height,
	}

	// Return PlacementResult if the original transmission was a TransmitPlace.
	// This triggers whitespace reservation in the guest terminal so the cursor
	// advances past where the image will be placed.
	if pending.AndPlace {
		return &PlacementResult{
			Rows:       imgRows,
			Cols:       imgCols,
			CursorMove: pending.CursorMove,
		}
	}
	return nil
}

func (kp *KittyPassthrough) forwardFileTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, windowWidth, windowHeight, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int, isAltScreen bool) {
	if cmd.FilePath == "" {
		return
	}

	filePath := cmd.FilePath
	if cmd.Medium == vt.KittyMediumSharedMemory {
		filePath = "/dev/shm/" + cmd.FilePath
	}

	kittyPassthroughLog("forwardFileTransmit: file=%s, andPlace=%v, medium=%c", filePath, andPlace, cmd.Medium)

	// In inline-graphics mode (tuios-web) the host terminal cannot read
	// files on the server. Read the file ourselves and divert into the
	// direct-transmission path so the bytes reach the browser over the
	// sip PTY. This is critical for apps like youterm / mpv that use
	// shared-memory frames (t=s, /dev/shm/...) and for any t=f / t=t
	// transmission.
	if kp.inlineGraphics {
		kp.forwardFileTransmitInline(cmd, filePath, windowID, andPlace,
			windowX, windowY, windowWidth, windowHeight,
			contentOffsetX, contentOffsetY,
			cursorX, cursorY, scrollbackLen, isAltScreen)
		return
	}

	// Reuse existing host ID if this window already has a placement for this
	// guest image ID. This eliminates delete+re-place flicker for video playback:
	// transmitting with the same ID replaces the image data in-place, and the
	// existing placement automatically shows the new frame.
	//
	// Guest image ID == 0 is kitty's "auto-assign" sentinel; each transmit with
	// ID 0 is a DISTINCT image (youterm's thumbnails, chafa, etc. all use 0).
	// Always allocate a fresh host ID so they coexist instead of overwriting.
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
		// Reusing ID  - check if dimensions changed (e.g., window resize).
		// If so, delete old placement so it gets recreated at the new size.
		if placements := kp.placements[windowID]; placements != nil {
			for _, p := range placements {
				imgRows, imgCols := kp.calculateImageCells(cmd)
				if p.HostImageID == hostID && (p.Rows != imgRows || p.Cols != imgCols) {
					kp.deleteOnePlacement(p)
					delete(placements, hostID)
					break
				}
			}
		}
	}
	kittyPassthroughLog("forwardFileTransmit: mapped guestID=%d -> hostID=%d for window=%s", cmd.ImageID, hostID, windowID[:min(8, len(windowID))])

	// PERFORMANCE: Forward the file path directly to the host terminal.
	// The host (Ghostty/Kitty) reads the file itself  - no need to read the
	// entire file into memory, base64 encode it, and chunk it.
	// For t=s (shm), send the original shm name (NOT /dev/shm/ prefixed path).
	// The host terminal prepends /dev/shm/ itself.
	// For t=f/t=t, send the full file path.
	encodePath := cmd.FilePath // Original name from the guest
	if cmd.Medium != vt.KittyMediumSharedMemory {
		encodePath = filePath // Use potentially modified path for non-shm
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(encodePath))

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	// Calculate content area dimensions (accounting for borders)
	contentWidth := windowWidth - 2   // -2 for left/right borders
	contentHeight := windowHeight - 2 // -2 for top/bottom borders

	// Calculate image dimensions in cells
	// Note: calculateImageCells returns (rows, cols) in that order
	imgRows, imgCols := kp.calculateImageCells(cmd)

	// Cap to content area (not cursor position) - allow full-height images
	// The image will be repositioned by RefreshAllPlacements after scrolling
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

	// Build a single transmit command with the correct medium type.
	// The host terminal reads the file/shm directly  - no chunking needed.
	//
	// For video playback (reusing ID + andPlace), use a=T (transmit+place)
	// to avoid race conditions where RefreshAllPlacements runs before the
	// new transmit arrives. For first frames (new ID), use a=t and let
	// RefreshAllPlacements handle placement.
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")

	// Use the original medium type: f=file, s=shared memory, t=temp file
	medium := "f"
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		medium = "s"
	case vt.KittyMediumTempFile:
		medium = "t"
	}

	// Always transmit-only here. Placement is handled either by:
	// - Video immediate path (isVideoFrame) which uses a=T with positioning
	// - RefreshAllPlacements which sends a=p for non-video images
	action := "t"

	fmt.Fprintf(&buf, "a=%s,t=%s,i=%d,f=%d,s=%d,v=%d,q=2",
		action, medium, hostID, cmd.Format, cmd.Width, cmd.Height)
	if cmd.Compression == vt.KittyCompressionZlib {
		buf.WriteString(",o=z")
	}
	if displayCols > 0 {
		fmt.Fprintf(&buf, ",c=%d", displayCols)
	}
	if displayRows > 0 {
		fmt.Fprintf(&buf, ",r=%d", displayRows)
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
	sourceHeight := cmd.SourceHeight
	if sourceHeight == 0 && displayRows < imgRows {
		caps := GetHostCapabilities()
		cellH := caps.CellHeight
		if cellH <= 0 {
			cellH = 20
		}
		sourceHeight = displayRows * cellH
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

	// For video (reusing ID + shm), write IMMEDIATELY to host terminal.
	// File/shm-based video is time-critical: mpv overwrites the shm/file
	// with the next frame almost instantly.
	// For non-video (first image, icat), always transmit via pendingOutput
	// and let RefreshAllPlacements handle placement with proper clipping.
	// Video: reusing ID + chunked (more=true on first chunk).
	// icat/youterm: may reuse ID but sends single unchunked command (more=false).
	isVideoFrame := reusingID && andPlace && cmd.More

	if isVideoFrame && kp.hostOut != nil {
		// Override to a=T for video immediate flush (buf was built with a=t)
		bufBytes := bytes.Replace(buf.Bytes(), []byte("a=t,"), []byte("a=T,"), 1)

		// Bounds check for video
		visible := windowX >= 0 && windowY >= 0 && hostX >= 0 && hostY >= 0
		if visible && displayCols > 0 {
			visible = hostX+displayCols <= windowX+1+contentWidth
		}
		if visible && displayRows > 0 {
			visible = hostY+displayRows <= windowY+1+contentHeight
		}
		if visible && kp.screenWidth > 0 && kp.screenHeight > 0 {
			if hostX+displayCols > kp.screenWidth || hostY+displayRows >= kp.screenHeight-1 {
				visible = false
			}
		}

		if visible {
			var posCmd []byte
			posCmd = append(posCmd, syncBegin...)
			posCmd = append(posCmd, fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1)...)
			posCmd = append(posCmd, bufBytes...)
			posCmd = append(posCmd, syncEnd...)
			_, _ = kp.hostOut.Write(posCmd)
		} else if hostID > 0 {
			var del []byte
			del = append(del, syncBegin...)
			del = append(del, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)...)
			del = append(del, syncEnd...)
			_, _ = kp.hostOut.Write(del)
		}
	} else {
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	// Don't clean up files here  - for shared memory (t=s), the guest app
	// manages the lifecycle. For temp files (t=t), the host terminal deletes
	// them after reading. For regular files (t=f), they persist.

	// Store placement using hostID as key (cmd.ImageID is often 0 for new images)
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	kp.placements[windowID][hostID] = &PassthroughPlacement{
		GuestImageID:      cmd.ImageID,
		HostImageID:       hostID,
		WindowID:          windowID,
		GuestX:            cursorX,
		AbsoluteLine:      scrollbackLen + cursorY,
		HostX:             hostX,
		HostY:             hostY,
		Cols:              displayCols,
		Rows:              imgRows,     // Original image rows (for scroll clipping)
		DisplayRows:       displayRows, // Capped rows for initial display
		SourceX:           cmd.SourceX,
		SourceY:           cmd.SourceY,
		SourceWidth:       cmd.SourceWidth,
		SourceHeight:      cmd.SourceHeight,
		XOffset:           cmd.XOffset,
		YOffset:           cmd.YOffset,
		ZIndex:            cmd.ZIndex,
		Virtual:           cmd.Virtual,
		Hidden:            true, // Start hidden, RefreshAllPlacements will place it
		PlacedOnAltScreen: isAltScreen,
	}
	kittyPassthroughLog("forwardFileTransmit: stored placement hostID=%d (hidden, waiting for refresh)", hostID)
}

// forwardFileTransmitInline handles file / shm / temp-file kitty transmits
// when the host terminal cannot read server-local files (tuios-web's browser
// target). We read the file ourselves, base64 encode it, and emit a normal
// direct (t=d) transmission so the bytes reach the browser through the sip
// PTY. A placement entry is created in the standard hidden-until-refresh
// state so RefreshAllPlacements will emit the matching a=p on the next
// render cycle, identical to the native-mode flow.
func (kp *KittyPassthrough) forwardFileTransmitInline(
	cmd *vt.KittyCommand,
	filePath string,
	windowID string,
	andPlace bool,
	windowX, windowY, windowWidth, windowHeight int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	isAltScreen bool,
) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		kittyPassthroughLog("forwardFileTransmitInline: read %s failed: %v", filePath, err)
		return
	}
	kittyPassthroughLog("forwardFileTransmitInline: read %d bytes from %s", len(data), filePath)

	// Get or allocate a host id. Video frames reuse the same guest id per
	// stream, so the second frame onward finds an existing placement and
	// just replaces the bitmap bytes (our overlay re-renders live
	// placements when their image id gets re-transmitted).
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

	// Cell dimensions. Match forwardFileTransmit semantics.
	imgRows, imgCols := kp.calculateImageCells(cmd)
	contentWidth := windowWidth - 2
	contentHeight := windowHeight - 2
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

	// Build the image as 4096-byte kitty chunks (t=d). Pass through
	// format / compression / size so the overlay knows how to decode.
	frameData := kp.buildInlineChunks(hostID, cmd.Format, cmd.Compression, cmd.Width, cmd.Height, data)

	kittyPassthroughLog("forwardFileTransmitInline: built %d bytes, hostID=%d, imgSize=(%d,%d), reusingID=%v",
		len(frameData), hostID, imgCols, imgRows, reusingID)

	if reusingID {
		// Video frame: send asynchronously so the VT callback and render
		// loop stay responsive. Drop frames if the writer is backed up
		// (channel full) to prevent unbounded lag.
		select {
		case kp.asyncFrameCh <- frameData:
		default:
			// Previous frame still in flight, drop this one.
			kittyPassthroughLog("forwardFileTransmitInline: dropped frame (async channel full)")
		}
	} else {
		// First frame / static image: go through pendingOutput so
		// RefreshAllPlacements can attach the a=p in the same flush.
		kp.pendingOutput = append(kp.pendingOutput, frameData...)
	}

	// Track placement. Reuse an existing entry on retransmit so the
	// previously emitted a=p does not get resent (we want the browser
	// to keep the same canvas and just pick up the new bitmap).
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	existing, hasExisting := kp.placements[windowID][hostID]
	if hasExisting {
		// Retransmit path: update dims in case they changed, but keep
		// Hidden state as-is so we do not re-emit a=p.
		existing.Cols = displayCols
		existing.Rows = imgRows
		existing.DisplayRows = displayRows
		existing.HostX = hostX
		existing.HostY = hostY
		existing.AbsoluteLine = scrollbackLen + cursorY
		existing.ImagePixelWidth = cmd.Width
		existing.ImagePixelHeight = cmd.Height
	} else {
		kp.placements[windowID][hostID] = &PassthroughPlacement{
			GuestImageID:      cmd.ImageID,
			HostImageID:       hostID,
			WindowID:          windowID,
			GuestX:            cursorX,
			AbsoluteLine:      scrollbackLen + cursorY,
			HostX:             hostX,
			HostY:             hostY,
			Cols:              displayCols,
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
			Hidden:            true, // RefreshAllPlacements emits a=p
			PlacedOnAltScreen: isAltScreen,
			ImagePixelWidth:   cmd.Width,
			ImagePixelHeight:  cmd.Height,
		}
	}
	_ = andPlace // placement is always driven by RefreshAllPlacements in inline mode
}

// buildInlineChunks encodes raw image bytes as a kitty direct-transmission
// (t=d) sequence split into 4096-byte base64 chunks. Returns the complete
// byte sequence ready to write to the host.
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

func (kp *KittyPassthrough) forwardPlace(
	cmd *vt.KittyCommand,
	windowID string,
	windowX, windowY int,
	windowWidth, windowHeight int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	_ bool, // isAltScreen - currently unused
) {
	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	// Get or allocate a unique host ID for this (window, guestImageID) pair
	// This prevents conflicts when multiple windows use the same guest image ID
	hostID := kp.getOrAllocateHostID(windowID, cmd.ImageID)

	// Calculate content area dimensions (accounting for borders)
	contentWidth := windowWidth - 2
	contentHeight := windowHeight - 2

	// Calculate image dimensions and cap to content area
	// Note: calculateImageCells returns (rows, cols) in that order
	imgRows, imgCols := kp.calculateImageCells(cmd)
	displayCols := imgCols
	displayRows := imgRows
	if displayCols > contentWidth && contentWidth > 0 {
		displayCols = contentWidth
	}
	if displayRows > contentHeight && contentHeight > 0 {
		displayRows = contentHeight
	}

	var buf bytes.Buffer
	buf.WriteString("\x1b7") // Save cursor position
	fmt.Fprintf(&buf, "\x1b[%d;%dH", hostY+1, hostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d", hostID)

	if cmd.PlacementID > 0 {
		fmt.Fprintf(&buf, ",p=%d", cmd.PlacementID)
	}
	// Always set display dimensions to control size
	if displayCols > 0 {
		fmt.Fprintf(&buf, ",c=%d", displayCols)
	}
	if displayRows > 0 {
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
	// Note: Don't send U=1 to host - TUIOS renders guest content itself
	buf.WriteString(",q=2")
	buf.WriteString("\x1b\\")
	buf.WriteString("\x1b8") // Restore cursor position

	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)

	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}

	// Store placement with both original and capped dimensions
	placement := &PassthroughPlacement{
		GuestImageID: cmd.ImageID,
		HostImageID:  hostID,
		PlacementID:  cmd.PlacementID,
		WindowID:     windowID,
		GuestX:       cursorX,
		AbsoluteLine: scrollbackLen + cursorY,
		HostX:        hostX,
		HostY:        hostY,
		Cols:         displayCols,
		Rows:         imgRows,     // Original image rows
		DisplayRows:  displayRows, // Capped for initial display
		SourceX:      cmd.SourceX,
		SourceY:      cmd.SourceY,
		SourceWidth:  cmd.SourceWidth,
		SourceHeight: cmd.SourceHeight,
		XOffset:      cmd.XOffset,
		YOffset:      cmd.YOffset,
		ZIndex:       cmd.ZIndex,
		Virtual:      cmd.Virtual,
	}
	kp.placements[windowID][cmd.ImageID] = placement
}

// deleteAllWindowPlacements removes all placements for a window from the host terminal
// and clears the placement tracking. If clearImageMap is true, also clears the imageIDMap.
func (kp *KittyPassthrough) deleteAllWindowPlacements(windowID string, clearImageMap bool) {
	for _, p := range kp.placements[windowID] {
		kp.deleteOnePlacement(p)
	}
	kp.placements[windowID] = nil
	if clearImageMap {
		kp.imageIDMap[windowID] = nil
	}
}

func (kp *KittyPassthrough) forwardDelete(cmd *vt.KittyCommand, windowID string) {
	kittyPassthroughLog("forwardDelete: delete=%c, imageID=%d, windowID=%s", cmd.Delete, cmd.ImageID, windowID[:min(8, len(windowID))])

	switch cmd.Delete {
	case vt.KittyDeleteAll, 0:
		kp.deleteAllWindowPlacements(windowID, false)

	case vt.KittyDeleteByID:
		if windowMap := kp.imageIDMap[windowID]; windowMap != nil {
			if hostID, ok := windowMap[cmd.ImageID]; ok {
				kp.deleteOnePlacement(&PassthroughPlacement{HostImageID: hostID})
				if placements := kp.placements[windowID]; placements != nil {
					delete(placements, hostID)
				}
				delete(windowMap, cmd.ImageID)
				kittyPassthroughLog("forwardDelete: deleted guestID=%d (hostID=%d)", cmd.ImageID, hostID)
			}
		}

	case vt.KittyDeleteByIDAndPlacement:
		if windowMap := kp.imageIDMap[windowID]; windowMap != nil {
			if hostID, ok := windowMap[cmd.ImageID]; ok {
				var buf bytes.Buffer
				buf.WriteString("\x1b_G")
				fmt.Fprintf(&buf, "a=d,d=I,i=%d", hostID)
				if cmd.PlacementID > 0 {
					fmt.Fprintf(&buf, ",p=%d", cmd.PlacementID)
				}
				buf.WriteString(",q=2\x1b\\")
				kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
				if placements := kp.placements[windowID]; placements != nil {
					delete(placements, hostID)
				}
				delete(windowMap, cmd.ImageID)
				kittyPassthroughLog("forwardDelete: deleted guestID=%d (hostID=%d) with placement", cmd.ImageID, hostID)
			}
		}

	default:
		// Handles DeleteOnScreen, DeleteAtCursor, DeleteAtCursorCell, and unknown types.
		// For simplicity, all of these clear all placements and the imageID map.
		if cmd.Delete != vt.KittyDeleteOnScreen &&
			cmd.Delete != vt.KittyDeleteAtCursor &&
			cmd.Delete != vt.KittyDeleteAtCursorCell {
			kittyPassthroughLog("forwardDelete: UNHANDLED delete type=%c (%d), clearing all as fallback", cmd.Delete, cmd.Delete)
		}
		kp.deleteAllWindowPlacements(windowID, true)
	}
}
