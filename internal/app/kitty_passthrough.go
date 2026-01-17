package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
	defer f.Close()
	fmt.Fprintf(f, "[%s] KITTY-PASSTHROUGH: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type KittyPassthrough struct {
	mu      sync.Mutex
	enabled bool
	hostOut *os.File

	placements    map[string]map[uint32]*PassthroughPlacement
	nextHostID    uint32
	pendingOutput []byte
}

type PassthroughPlacement struct {
	GuestImageID uint32
	HostImageID  uint32
	PlacementID  uint32
	WindowID     string
	GuestX       int
	AbsoluteLine int  // Absolute line position (scrollbackLen + cursorY at placement time)
	HostX        int
	HostY        int
	Cols         int
	Rows         int
	Hidden       bool // True when placement is scrolled out of view
}

type WindowPositionInfo struct {
	WindowX        int
	WindowY        int
	ContentOffsetX int
	ContentOffsetY int
	Width          int
	Height         int
	Visible        bool
	ScrollbackLen  int // Total scrollback lines
	ScrollOffset   int // Current scroll offset (0 = at bottom)
}

func NewKittyPassthrough() *KittyPassthrough {
	caps := GetHostCapabilities()
	kittyPassthroughLog("NewKittyPassthrough: KittyGraphics=%v, TerminalName=%s", caps.KittyGraphics, caps.TerminalName)
	return &KittyPassthrough{
		enabled:    caps.KittyGraphics,
		hostOut:    os.Stdout,
		placements: make(map[string]map[uint32]*PassthroughPlacement),
		nextHostID: 1,
	}
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
		if caps.CellWidth > 0 && caps.CellHeight > 0 {
			if rows == 0 && cmd.Height > 0 {
				rows = (cmd.Height + caps.CellHeight - 1) / caps.CellHeight
			}
			if cols == 0 && cmd.Width > 0 {
				cols = (cmd.Width + caps.CellWidth - 1) / caps.CellWidth
			}
		}
	}

	return rows, cols
}

// PlacementResult contains info about an image placement for cursor positioning
type PlacementResult struct {
	Rows int // Number of rows the image occupies
	Cols int // Number of columns the image occupies
}

func (kp *KittyPassthrough) ForwardCommand(
	cmd *vt.KittyCommand,
	rawData []byte,
	windowID string,
	windowX, windowY int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
	ptyInput func([]byte),
) *PlacementResult {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kittyPassthroughLog("ForwardCommand: action=%c, enabled=%v, imageID=%d, windowID=%s, win=(%d,%d), cursor=(%d,%d), scrollback=%d",
		cmd.Action, kp.enabled, cmd.ImageID, windowID[:8], windowX, windowY, cursorX, cursorY, scrollbackLen)

	if !kp.enabled {
		kittyPassthroughLog("ForwardCommand: DISABLED, returning early")
		return nil
	}

	switch cmd.Action {
	case vt.KittyActionQuery:
		kittyPassthroughLog("ForwardCommand: handling QUERY")
		kp.forwardQuery(cmd, rawData, ptyInput)

	case vt.KittyActionTransmit:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT")
		kp.forwardTransmit(cmd, rawData, windowID, false, 0, 0, 0, 0, 0, 0, 0)

	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		if !cmd.More && !isFileBased {
			kp.forwardPlace(cmd, windowID, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		}
		// Return placement dimensions for cursor positioning
		if !cmd.More {
			rows, cols := kp.calculateImageCells(cmd)
			if rows > 0 || cols > 0 {
				return &PlacementResult{Rows: rows, Cols: cols}
			}
		}

	case vt.KittyActionPlace:
		kittyPassthroughLog("ForwardCommand: handling PLACE")
		kp.forwardPlace(cmd, windowID, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		rows, cols := kp.calculateImageCells(cmd)
		if rows > 0 || cols > 0 {
			return &PlacementResult{Rows: rows, Cols: cols}
		}

	case vt.KittyActionDelete:
		kittyPassthroughLog("ForwardCommand: handling DELETE, d=%c, imageID=%d", cmd.Delete, cmd.ImageID)
		kp.forwardDelete(cmd, windowID)

	default:
		kittyPassthroughLog("ForwardCommand: UNKNOWN action %c", cmd.Action)
	}

	return nil
}

func (kp *KittyPassthrough) forwardQuery(cmd *vt.KittyCommand, rawData []byte, ptyInput func([]byte)) {
	if ptyInput != nil && cmd.Quiet < 2 {
		response := vt.BuildKittyResponse(true, cmd.ImageID, "")
		ptyInput(response)
	}
}

func (kp *KittyPassthrough) forwardTransmit(cmd *vt.KittyCommand, rawData []byte, windowID string, andPlace bool, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int) {
	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile {
		kp.forwardFileTransmit(cmd, windowID, andPlace, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen)
		return
	}
	kp.pendingOutput = append(kp.pendingOutput, rawData...)
}

func (kp *KittyPassthrough) forwardFileTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen int) {
	if cmd.FilePath == "" {
		return
	}

	filePath := cmd.FilePath
	if cmd.Medium == vt.KittyMediumSharedMemory {
		filePath = "/dev/shm/" + cmd.FilePath
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		kittyPassthroughLog("forwardFileTransmit: failed to read %s: %v", filePath, err)
		return
	}

	kittyPassthroughLog("forwardFileTransmit: read %d bytes from %s, andPlace=%v", len(data), filePath, andPlace)

	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile {
		os.Remove(filePath)
	}

	hostID := kp.allocateHostID()
	encoded := base64.StdEncoding.EncodeToString(data)

	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	kittyPassthroughLog("forwardFileTransmit: hostID=%d, hostPos=(%d,%d) = win(%d,%d) + offset(%d,%d) + cursor(%d,%d), absLine=%d",
		hostID, hostX, hostY, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY, scrollbackLen+cursorY)

	kp.pendingOutput = append(kp.pendingOutput, "\x1b7"...)

	if andPlace {
		kp.pendingOutput = append(kp.pendingOutput, fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1)...)
	}

	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]
		more := end < len(encoded)

		var buf bytes.Buffer
		buf.WriteString("\x1b_G")

		if i == 0 {
			action := "t"
			if andPlace {
				action = "T"
			}
			buf.WriteString(fmt.Sprintf("a=%s,i=%d,f=%d,s=%d,v=%d,q=2",
				action, hostID, cmd.Format, cmd.Width, cmd.Height))
			if cmd.Compression == vt.KittyCompressionZlib {
				buf.WriteString(",o=z")
			}
		} else {
			buf.WriteString(fmt.Sprintf("i=%d,q=2", hostID))
		}

		if more {
			buf.WriteString(",m=1")
		}

		buf.WriteByte(';')
		buf.WriteString(chunk)
		buf.WriteString("\x1b\\")

		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	}

	kp.pendingOutput = append(kp.pendingOutput, "\x1b8"...)

	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}
	kp.placements[windowID][cmd.ImageID] = &PassthroughPlacement{
		GuestImageID: cmd.ImageID,
		HostImageID:  hostID,
		WindowID:     windowID,
		GuestX:       cursorX,
		AbsoluteLine: scrollbackLen + cursorY,
		HostX:        hostX,
		HostY:        hostY,
	}
}

func (kp *KittyPassthrough) forwardPlace(
	cmd *vt.KittyCommand,
	windowID string,
	windowX, windowY int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	scrollbackLen int,
) {
	hostX := windowX + contentOffsetX + cursorX
	hostY := windowY + contentOffsetY + cursorY

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", hostY+1, hostX+1))
	buf.WriteString("\x1b_G")
	buf.WriteString(fmt.Sprintf("a=p,i=%d", cmd.ImageID))

	if cmd.PlacementID > 0 {
		buf.WriteString(fmt.Sprintf(",p=%d", cmd.PlacementID))
	}
	if cmd.Columns > 0 {
		buf.WriteString(fmt.Sprintf(",c=%d", cmd.Columns))
	}
	if cmd.Rows > 0 {
		buf.WriteString(fmt.Sprintf(",r=%d", cmd.Rows))
	}
	if cmd.XOffset > 0 {
		buf.WriteString(fmt.Sprintf(",X=%d", cmd.XOffset))
	}
	if cmd.YOffset > 0 {
		buf.WriteString(fmt.Sprintf(",Y=%d", cmd.YOffset))
	}
	if cmd.SourceX > 0 {
		buf.WriteString(fmt.Sprintf(",x=%d", cmd.SourceX))
	}
	if cmd.SourceY > 0 {
		buf.WriteString(fmt.Sprintf(",y=%d", cmd.SourceY))
	}
	if cmd.SourceWidth > 0 {
		buf.WriteString(fmt.Sprintf(",w=%d", cmd.SourceWidth))
	}
	if cmd.SourceHeight > 0 {
		buf.WriteString(fmt.Sprintf(",h=%d", cmd.SourceHeight))
	}
	if cmd.ZIndex != 0 {
		buf.WriteString(fmt.Sprintf(",z=%d", cmd.ZIndex))
	}
	if cmd.Virtual {
		buf.WriteString(",U=1")
	}
	buf.WriteString(",q=2")
	buf.WriteString("\x1b\\")

	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)

	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[uint32]*PassthroughPlacement)
	}

	placement := &PassthroughPlacement{
		GuestImageID: cmd.ImageID,
		HostImageID:  cmd.ImageID,
		PlacementID:  cmd.PlacementID,
		WindowID:     windowID,
		GuestX:       cursorX,
		AbsoluteLine: scrollbackLen + cursorY,
		HostX:        hostX,
		HostY:        hostY,
		Cols:         cmd.Columns,
		Rows:         cmd.Rows,
	}
	kp.placements[windowID][cmd.ImageID] = placement
}

func (kp *KittyPassthrough) forwardDelete(cmd *vt.KittyCommand, windowID string) {
	switch cmd.Delete {
	case vt.KittyDeleteAll, 0:
		// Delete all images for this window
		placements := kp.placements[windowID]
		for _, p := range placements {
			var buf bytes.Buffer
			buf.WriteString("\x1b_G")
			buf.WriteString(fmt.Sprintf("a=d,d=i,i=%d,q=2", p.HostImageID))
			buf.WriteString("\x1b\\")
			kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
		}
		kp.placements[windowID] = nil

	case vt.KittyDeleteByID:
		// Translate guest image ID to host image ID
		if placements := kp.placements[windowID]; placements != nil {
			if p, ok := placements[cmd.ImageID]; ok {
				var buf bytes.Buffer
				buf.WriteString("\x1b_G")
				buf.WriteString(fmt.Sprintf("a=d,d=i,i=%d,q=2", p.HostImageID))
				buf.WriteString("\x1b\\")
				kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
				delete(placements, cmd.ImageID)
			}
		}

	case vt.KittyDeleteByIDAndPlacement:
		// Translate guest image ID to host image ID
		if placements := kp.placements[windowID]; placements != nil {
			if p, ok := placements[cmd.ImageID]; ok {
				var buf bytes.Buffer
				buf.WriteString("\x1b_G")
				buf.WriteString(fmt.Sprintf("a=d,d=I,i=%d", p.HostImageID))
				if cmd.PlacementID > 0 {
					buf.WriteString(fmt.Sprintf(",p=%d", cmd.PlacementID))
				}
				buf.WriteString(",q=2\x1b\\")
				kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
				delete(placements, cmd.ImageID)
			}
		}
	}
}

func (kp *KittyPassthrough) OnWindowMove(windowID string, newX, newY, contentOffsetX, contentOffsetY int, scrollbackLen, scrollOffset, viewportHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	viewportTop := scrollbackLen - scrollOffset

	for _, p := range placements {
		if !p.Hidden {
			kp.deleteOnePlacement(p)
		}

		relativeY := p.AbsoluteLine - viewportTop
		p.HostX = newX + contentOffsetX + p.GuestX
		p.HostY = newY + contentOffsetY + relativeY

		// Check if in viewport
		if relativeY >= 0 && relativeY < viewportHeight {
			kp.placeOne(p)
			p.Hidden = false
		} else {
			p.Hidden = true
		}
	}
}

func (kp *KittyPassthrough) OnWindowClose(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	delete(kp.placements, windowID)
}

func (kp *KittyPassthrough) OnWindowScroll(windowID string, windowX, windowY, contentOffsetX, contentOffsetY, scrollbackLen, scrollOffset, viewportHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	viewportTop := scrollbackLen - scrollOffset

	for _, p := range placements {
		if !p.Hidden {
			kp.deleteOnePlacement(p)
		}

		relativeY := p.AbsoluteLine - viewportTop
		p.HostX = windowX + contentOffsetX + p.GuestX
		p.HostY = windowY + contentOffsetY + relativeY

		// Check if in viewport
		if relativeY >= 0 && relativeY < viewportHeight {
			kp.placeOne(p)
			p.Hidden = false
		} else {
			p.Hidden = true
		}
	}
}

func (kp *KittyPassthrough) ClearWindow(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	kp.placements[windowID] = nil
}

func (kp *KittyPassthrough) RefreshAllPlacements(getWindowInfo func(windowID string) *WindowPositionInfo) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	for windowID, placements := range kp.placements {
		if len(placements) == 0 {
			continue
		}

		info := getWindowInfo(windowID)
		if info == nil {
			for _, p := range placements {
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
			}
			delete(kp.placements, windowID)
			continue
		}

		// Calculate viewport top line (absolute line number)
		viewportTop := info.ScrollbackLen - info.ScrollOffset
		viewportHeight := info.Height - 2 // Account for window borders

		for _, p := range placements {
			// Delete current placement if visible
			if !p.Hidden {
				kp.deleteOnePlacement(p)
			}

			if !info.Visible {
				p.Hidden = true
				continue
			}

			// Calculate relative Y from AbsoluteLine
			relativeY := p.AbsoluteLine - viewportTop

			newHostX := info.WindowX + info.ContentOffsetX + p.GuestX
			newHostY := info.WindowY + info.ContentOffsetY + relativeY

			p.HostX = newHostX
			p.HostY = newHostY

			// Check if placement is within viewport
			if relativeY >= 0 && relativeY < viewportHeight {
				kp.placeOne(p)
				p.Hidden = false
			} else {
				p.Hidden = true
			}
		}
	}
}

func (kp *KittyPassthrough) HasPlacements() bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, placements := range kp.placements {
		if len(placements) > 0 {
			return true
		}
	}
	return false
}

func (kp *KittyPassthrough) deleteOnePlacement(p *PassthroughPlacement) {
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")
	buf.WriteString(fmt.Sprintf("a=d,d=i,i=%d", p.HostImageID))
	if p.PlacementID > 0 {
		buf.WriteString(fmt.Sprintf(",p=%d", p.PlacementID))
	}
	buf.WriteString(",q=2\x1b\\")
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func (kp *KittyPassthrough) placeOne(p *PassthroughPlacement) {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", p.HostY+1, p.HostX+1))
	buf.WriteString("\x1b_G")
	buf.WriteString(fmt.Sprintf("a=p,i=%d", p.HostImageID))
	if p.PlacementID > 0 {
		buf.WriteString(fmt.Sprintf(",p=%d", p.PlacementID))
	}
	if p.Cols > 0 {
		buf.WriteString(fmt.Sprintf(",c=%d", p.Cols))
	}
	if p.Rows > 0 {
		buf.WriteString(fmt.Sprintf(",r=%d", p.Rows))
	}
	buf.WriteString(",q=2\x1b\\")
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func rebuildKittyCommand(cmd *vt.KittyCommand) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")

	params := make([]string, 0)

	if cmd.Action != 0 {
		params = append(params, fmt.Sprintf("a=%c", cmd.Action))
	}
	if cmd.ImageID > 0 {
		params = append(params, fmt.Sprintf("i=%d", cmd.ImageID))
	}
	if cmd.ImageNumber > 0 {
		params = append(params, fmt.Sprintf("I=%d", cmd.ImageNumber))
	}
	if cmd.PlacementID > 0 {
		params = append(params, fmt.Sprintf("p=%d", cmd.PlacementID))
	}
	if cmd.Format != 0 {
		params = append(params, fmt.Sprintf("f=%d", cmd.Format))
	}
	if cmd.Medium != 0 {
		params = append(params, fmt.Sprintf("t=%c", cmd.Medium))
	}
	if cmd.Compression == vt.KittyCompressionZlib {
		params = append(params, "o=z")
	}
	if cmd.Width > 0 {
		params = append(params, fmt.Sprintf("s=%d", cmd.Width))
	}
	if cmd.Height > 0 {
		params = append(params, fmt.Sprintf("v=%d", cmd.Height))
	}
	if cmd.Size > 0 {
		params = append(params, fmt.Sprintf("S=%d", cmd.Size))
	}
	if cmd.Offset > 0 {
		params = append(params, fmt.Sprintf("O=%d", cmd.Offset))
	}
	if cmd.More {
		params = append(params, "m=1")
	}
	if cmd.Delete != 0 {
		params = append(params, fmt.Sprintf("d=%c", cmd.Delete))
	}
	if cmd.Columns > 0 {
		params = append(params, fmt.Sprintf("c=%d", cmd.Columns))
	}
	if cmd.Rows > 0 {
		params = append(params, fmt.Sprintf("r=%d", cmd.Rows))
	}
	if cmd.XOffset > 0 {
		params = append(params, fmt.Sprintf("X=%d", cmd.XOffset))
	}
	if cmd.YOffset > 0 {
		params = append(params, fmt.Sprintf("Y=%d", cmd.YOffset))
	}
	if cmd.SourceX > 0 {
		params = append(params, fmt.Sprintf("x=%d", cmd.SourceX))
	}
	if cmd.SourceY > 0 {
		params = append(params, fmt.Sprintf("y=%d", cmd.SourceY))
	}
	if cmd.SourceWidth > 0 {
		params = append(params, fmt.Sprintf("w=%d", cmd.SourceWidth))
	}
	if cmd.SourceHeight > 0 {
		params = append(params, fmt.Sprintf("h=%d", cmd.SourceHeight))
	}
	if cmd.ZIndex != 0 {
		params = append(params, fmt.Sprintf("z=%d", cmd.ZIndex))
	}
	if cmd.CursorMove > 0 {
		params = append(params, fmt.Sprintf("C=%d", cmd.CursorMove))
	}
	if cmd.Virtual {
		params = append(params, "U=1")
	}
	if cmd.Quiet > 0 {
		params = append(params, fmt.Sprintf("q=%d", cmd.Quiet))
	}

	buf.WriteString(strings.Join(params, ","))

	if len(cmd.Data) > 0 {
		buf.WriteByte(';')
		buf.WriteString(base64.StdEncoding.EncodeToString(cmd.Data))
	} else if cmd.FilePath != "" {
		buf.WriteByte(';')
		buf.WriteString(base64.StdEncoding.EncodeToString([]byte(cmd.FilePath)))
	}

	buf.WriteString("\x1b\\")
	return buf.Bytes()
}

var _ = strconv.Itoa

func (m *OS) setupKittyPassthrough(window *terminal.Window) {
	if m.KittyPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}

	win := window
	window.Terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cursorPos := win.Terminal.CursorPosition()
		scrollbackLen := win.Terminal.ScrollbackLen()
		result := m.KittyPassthrough.ForwardCommand(
			cmd, rawData, win.ID,
			win.X, win.Y,
			1, 1,
			cursorPos.X, cursorPos.Y,
			scrollbackLen,
			func(response []byte) {
				if win.Pty != nil {
					_, _ = win.Pty.Write(response)
				} else if win.DaemonWriteFunc != nil {
					_ = win.DaemonWriteFunc(response)
				}
			},
		)
		// Reserve space in guest terminal for the image placement
		if result != nil && result.Rows > 0 {
			win.Terminal.ReserveImageSpace(result.Rows, result.Cols)
		}
	})
}
