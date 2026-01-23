package vt

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"time"
)

func kittyDebugLog(format string, args ...any) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}
	f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] KITTY: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

// KittyGraphicsHandler handles Kitty graphics protocol commands for a screen.
type KittyGraphicsHandler struct {
	screen *Screen
	state  *KittyState
	input  io.Writer // for sending responses back to PTY
}

// NewKittyGraphicsHandler creates a new handler for a screen.
func NewKittyGraphicsHandler(screen *Screen, state *KittyState, input io.Writer) *KittyGraphicsHandler {
	return &KittyGraphicsHandler{
		screen: screen,
		state:  state,
		input:  input,
	}
}

func (h *KittyGraphicsHandler) HandleCommand(cmd *KittyCommand) bool {
	if cmd == nil {
		return false
	}

	kittyDebugLog("HandleCommand: action=%c, imageID=%d, dataLen=%d, more=%v, quiet=%d",
		cmd.Action, cmd.ImageID, len(cmd.Data), cmd.More, cmd.Quiet)

	switch cmd.Action {
	case KittyActionQuery:
		return h.handleQuery(cmd)
	case KittyActionTransmit:
		return h.handleTransmit(cmd, false)
	case KittyActionTransmitPlace:
		return h.handleTransmit(cmd, true)
	case KittyActionPlace:
		return h.handlePlace(cmd)
	case KittyActionDelete:
		return h.handleDelete(cmd)
	case KittyActionFrame, KittyActionAnimation, KittyActionCompose:
		// Animation commands - not yet supported, but acknowledge them
		h.sendResponse(cmd, true, "")
		return true
	default:
		return false
	}
}

// handleQuery responds to capability queries.
func (h *KittyGraphicsHandler) handleQuery(cmd *KittyCommand) bool {
	// Respond with OK to indicate we support the graphics protocol
	h.sendResponse(cmd, true, "")
	return true
}

// handleTransmit stores an image, optionally placing it.
func (h *KittyGraphicsHandler) handleTransmit(cmd *KittyCommand, place bool) bool {
	// Handle chunked transmission
	if cmd.More {
		return h.handleChunkedTransmit(cmd)
	}

	// Check if this is a continuation of a chunked transmission
	pending := h.state.GetPending()
	if pending != nil {
		// Append final chunk and finalize
		h.state.AppendToPending(cmd.Data)
		img := h.state.FinalizePending()
		if img != nil {
			// Decompress if needed
			if pending.Compression == KittyCompressionZlib {
				decompressed, err := decompressZlib(img.Data)
				if err != nil {
					h.sendResponse(cmd, false, "EDECOMPRESS:zlib decompression failed")
					return true
				}
				img.Data = decompressed
			}
			h.state.AddImage(img)
			if place {
				h.placeImageAtCursor(img, cmd)
			}
			h.sendResponse(cmd, true, "")
		}
		return true
	}

	// Single transmission or first chunk
	data := cmd.Data

	// Handle file transmission modes
	switch cmd.Medium {
	case KittyMediumFile, KittyMediumTempFile:
		fileData, err := LoadFileData(cmd.FilePath)
		if err != nil {
			h.sendResponse(cmd, false, "ENOENT:file not found")
			return true
		}
		data = fileData
	case KittyMediumSharedMemory:
		shmData, err := loadSharedMemory(cmd.FilePath, cmd.Size)
		if err != nil {
			kittyDebugLog("Shared memory load failed: %v (path=%s, size=%d)", err, cmd.FilePath, cmd.Size)
			h.sendResponse(cmd, false, "ENOENT:shared memory not found")
			return true
		}
		kittyDebugLog("Loaded shared memory: path=%s, size=%d, got %d bytes", cmd.FilePath, cmd.Size, len(shmData))
		data = shmData
	}

	// Decompress if needed
	if cmd.Compression == KittyCompressionZlib {
		decompressed, err := decompressZlib(data)
		if err != nil {
			h.sendResponse(cmd, false, "EDECOMPRESS:zlib decompression failed")
			return true
		}
		data = decompressed
	}

	// Allocate or use provided ID
	imageID := cmd.ImageID
	if imageID == 0 {
		imageID = h.state.AllocateID()
	}

	img := &KittyImage{
		ID:           imageID,
		Number:       cmd.ImageNumber,
		Width:        cmd.Width,
		Height:       cmd.Height,
		Format:       cmd.Format,
		Compression:  KittyCompressionNone, // Already decompressed
		Data:         data,
		TransmitTime: time.Now(),
	}

	h.state.AddImage(img)
	kittyDebugLog("Image stored: id=%d, size=%dx%d, format=%c, dataLen=%d",
		img.ID, img.Width, img.Height, img.Format, len(img.Data))

	if place {
		h.placeImageAtCursor(img, cmd)
		kittyDebugLog("Image placed at cursor")
	}

	h.sendResponse(cmd, true, "")
	return true
}

// handleChunkedTransmit handles the start or continuation of a chunked transmission.
func (h *KittyGraphicsHandler) handleChunkedTransmit(cmd *KittyCommand) bool {
	pending := h.state.GetPending()

	if pending == nil {
		// Start new chunked transmission
		imageID := cmd.ImageID
		if imageID == 0 {
			imageID = h.state.AllocateID()
		}

		pending = &KittyPendingChunk{
			ImageID:     imageID,
			ImageNumber: cmd.ImageNumber,
			Format:      cmd.Format,
			Medium:      cmd.Medium,
			Compression: cmd.Compression,
			Width:       cmd.Width,
			Height:      cmd.Height,
			DataBuffer:  make([]byte, 0, len(cmd.Data)*4), // Pre-allocate
		}
		h.state.SetPending(pending)
	}

	// Append data
	h.state.AppendToPending(cmd.Data)

	// Don't send response for intermediate chunks (quiet mode implied)
	return true
}

// handlePlace creates a placement for an existing image.
func (h *KittyGraphicsHandler) handlePlace(cmd *KittyCommand) bool {
	// Find the image
	var img *KittyImage
	if cmd.ImageID > 0 {
		img = h.state.GetImage(cmd.ImageID)
	} else if cmd.ImageNumber > 0 {
		img = h.state.GetImageByNumber(cmd.ImageNumber)
	}

	if img == nil {
		h.sendResponse(cmd, false, "ENOENT:image not found")
		return true
	}

	h.placeImageAtCursor(img, cmd)
	h.sendResponse(cmd, true, "")
	return true
}

// handleDelete removes images or placements.
func (h *KittyGraphicsHandler) handleDelete(cmd *KittyCommand) bool {
	switch cmd.Delete {
	case KittyDeleteAll, 0:
		// Delete all images and placements
		h.state.Clear()

	case KittyDeleteByID:
		// Delete image by ID (and its placements)
		if cmd.ImageID > 0 {
			h.state.DeleteImage(cmd.ImageID)
		}

	case KittyDeleteByIDAndPlacement:
		// Delete specific placement for image
		if cmd.ImageID > 0 && cmd.PlacementID > 0 {
			h.state.DeletePlacement(cmd.ImageID, cmd.PlacementID)
		} else if cmd.ImageID > 0 {
			// Delete image and all its placements
			h.state.DeleteImage(cmd.ImageID)
		}

	case KittyDeleteByNumber:
		// Delete image by number
		if cmd.ImageNumber > 0 {
			h.state.DeleteImageByNumber(cmd.ImageNumber)
		}

	case KittyDeleteByNumberPlacement:
		// Delete specific placement by number
		img := h.state.GetImageByNumber(cmd.ImageNumber)
		if img != nil {
			if cmd.PlacementID > 0 {
				h.state.DeletePlacement(img.ID, cmd.PlacementID)
			} else {
				h.state.DeleteImage(img.ID)
			}
		}

	case KittyDeleteAtCursor, KittyDeleteAtCursorCell:
		// Delete placements at cursor position
		x, y := h.screen.CursorPosition()
		h.state.DeletePlacementsAtCursor(x, y)

	case KittyDeleteAtColumn:
		// Delete placements in column
		x, _ := h.screen.CursorPosition()
		h.state.DeletePlacementsInColumn(x)

	case KittyDeleteAtRow:
		// Delete placements in row
		_, y := h.screen.CursorPosition()
		h.state.DeletePlacementsInRow(y)

	case KittyDeleteAtZIndex:
		// Delete placements at specific z-index
		h.state.DeletePlacementsByZIndex(cmd.ZIndex)

	case KittyDeleteOnScreen:
		// Delete all visible placements (clear screen area)
		// For now, treat same as delete all
		h.state.Clear()

	case KittyDeleteByPlacementID:
		// Delete all placements with this ID (across all images)
		// Iterate through all images and remove placements
		for _, img := range h.state.GetImages() {
			h.state.DeletePlacement(img.ID, cmd.PlacementID)
		}

	case KittyDeleteIntersectCursor:
		// Delete placements intersecting cursor
		x, y := h.screen.CursorPosition()
		h.deleteIntersecting(x, y, 1, 1)

	case KittyDeleteIntersectColumn:
		// Delete placements intersecting column
		x, _ := h.screen.CursorPosition()
		h.deleteIntersecting(x, 0, 1, h.screen.Height())

	case KittyDeleteIntersectRow:
		// Delete placements intersecting row
		_, y := h.screen.CursorPosition()
		h.deleteIntersecting(0, y, h.screen.Width(), 1)

	case KittyDeleteIntersectCell:
		// Delete placements intersecting cell at cursor
		x, y := h.screen.CursorPosition()
		h.deleteIntersecting(x, y, 1, 1)
	}

	h.sendResponse(cmd, true, "")
	return true
}

func (h *KittyGraphicsHandler) placeImageAtCursor(img *KittyImage, cmd *KittyCommand) {
	x, y := h.screen.CursorPosition()
	scrollbackLen := h.screen.ScrollbackLen()
	absoluteLine := scrollbackLen + y

	placement := &KittyPlacement{
		ImageID:      img.ID,
		PlacementID:  cmd.PlacementID,
		ScreenX:      x,
		ScreenY:      y,
		AbsoluteLine: absoluteLine,
		XOffset:      cmd.XOffset,
		YOffset:      cmd.YOffset,
		SourceX:      cmd.SourceX,
		SourceY:      cmd.SourceY,
		SourceWidth:  cmd.SourceWidth,
		SourceHeight: cmd.SourceHeight,
		Columns:      cmd.Columns,
		Rows:         cmd.Rows,
		ZIndex:       cmd.ZIndex,
		CursorMove:   cmd.CursorMove,
		Virtual:      cmd.Virtual,
	}

	h.state.AddPlacement(placement)
	kittyDebugLog("Placement created: imgID=%d, pos=(%d,%d), absLine=%d, cols=%d, rows=%d",
		img.ID, x, y, absoluteLine, placement.Columns, placement.Rows)

	// Cursor movement: C=0 (or unset) = move cursor (DEFAULT), C=1 = don't move
	// Per Kitty graphics protocol, the default behavior is to move cursor after image placement
	if cmd.CursorMove == 0 {
		if placement.Rows > 0 {
			// Multi-row image: move cursor to line below image
			newY := y + placement.Rows
			if newY >= h.screen.Height() {
				newY = h.screen.Height() - 1
			}
			h.screen.setCursor(0, newY, false)
		} else if placement.Columns > 0 {
			// Single-row inline image: move cursor right
			newX := x + placement.Columns
			if newX >= h.screen.Width() {
				// Wrap to next line
				if y+1 < h.screen.Height() {
					h.screen.setCursor(0, y+1, false)
				}
			} else {
				h.screen.setCursorX(newX, false)
			}
		}
	}
}

// deleteIntersecting removes placements that intersect the given rectangle.
func (h *KittyGraphicsHandler) deleteIntersecting(x, y, w, he int) {
	placements := h.state.GetPlacements()
	for _, p := range placements {
		// Check if placement intersects rectangle
		pRight := p.ScreenX + p.Columns
		pBottom := p.ScreenY + p.Rows
		rectRight := x + w
		rectBottom := y + he

		if p.ScreenX < rectRight && pRight > x &&
			p.ScreenY < rectBottom && pBottom > y {
			h.state.DeletePlacement(p.ImageID, p.PlacementID)
		}
	}
}

// sendResponse sends a response back through the PTY input pipe.
func (h *KittyGraphicsHandler) sendResponse(cmd *KittyCommand, ok bool, errMsg string) {
	// Check quiet mode
	if cmd.Quiet == 2 {
		// q=2: suppress all responses
		return
	}
	if cmd.Quiet == 1 && ok {
		// q=1: suppress OK responses, only send errors
		return
	}

	// Get the image ID to include in response
	imageID := cmd.ImageID
	if imageID == 0 {
		// Check if we have a pending chunk with an allocated ID
		pending := h.state.GetPending()
		if pending != nil {
			imageID = pending.ImageID
		}
	}

	response := BuildKittyResponse(ok, imageID, errMsg)
	if h.input != nil {
		_, _ = h.input.Write(response)
	}
}

// decompressZlib decompresses zlib-compressed data.
func decompressZlib(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	return io.ReadAll(reader)
}
