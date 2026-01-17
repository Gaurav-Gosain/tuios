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
	GuestY       int
	HostX        int
	HostY        int
	Cols         int
	Rows         int
}

type WindowPositionInfo struct {
	WindowX        int
	WindowY        int
	ContentOffsetX int
	ContentOffsetY int
	Width          int
	Height         int
	Visible        bool
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

func (kp *KittyPassthrough) ForwardCommand(
	cmd *vt.KittyCommand,
	rawData []byte,
	windowID string,
	windowX, windowY int,
	contentOffsetX, contentOffsetY int,
	cursorX, cursorY int,
	ptyInput func([]byte),
) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kittyPassthroughLog("ForwardCommand: action=%c, enabled=%v, imageID=%d, windowID=%s, win=(%d,%d), cursor=(%d,%d)",
		cmd.Action, kp.enabled, cmd.ImageID, windowID[:8], windowX, windowY, cursorX, cursorY)

	if !kp.enabled {
		kittyPassthroughLog("ForwardCommand: DISABLED, returning early")
		return
	}

	switch cmd.Action {
	case vt.KittyActionQuery:
		kittyPassthroughLog("ForwardCommand: handling QUERY")
		kp.forwardQuery(cmd, rawData, ptyInput)

	case vt.KittyActionTransmit:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT")
		kp.forwardTransmit(cmd, rawData, windowID, false, 0, 0, 0, 0, 0, 0)

	case vt.KittyActionTransmitPlace:
		kittyPassthroughLog("ForwardCommand: handling TRANSMIT+PLACE, more=%v", cmd.More)
		isFileBased := cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile
		kp.forwardTransmit(cmd, rawData, windowID, true, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY)
		if !cmd.More && !isFileBased {
			kp.forwardPlace(cmd, windowID, windowX, windowY, contentOffsetX, contentOffsetY)
		}

	case vt.KittyActionPlace:
		kittyPassthroughLog("ForwardCommand: handling PLACE")
		kp.forwardPlace(cmd, windowID, windowX, windowY, contentOffsetX, contentOffsetY)

	case vt.KittyActionDelete:
		kittyPassthroughLog("ForwardCommand: handling DELETE")
		kp.forwardDelete(cmd, rawData, windowID)

	default:
		kittyPassthroughLog("ForwardCommand: UNKNOWN action %c", cmd.Action)
	}
}

func (kp *KittyPassthrough) forwardQuery(cmd *vt.KittyCommand, rawData []byte, ptyInput func([]byte)) {
	if ptyInput != nil && cmd.Quiet < 2 {
		response := vt.BuildKittyResponse(true, cmd.ImageID, "")
		ptyInput(response)
	}
}

func (kp *KittyPassthrough) forwardTransmit(cmd *vt.KittyCommand, rawData []byte, windowID string, andPlace bool, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY int) {
	if cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile || cmd.Medium == vt.KittyMediumFile {
		kp.forwardFileTransmit(cmd, windowID, andPlace, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY)
		return
	}
	kp.pendingOutput = append(kp.pendingOutput, rawData...)
}

func (kp *KittyPassthrough) forwardFileTransmit(cmd *vt.KittyCommand, windowID string, andPlace bool, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY int) {
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

	kittyPassthroughLog("forwardFileTransmit: hostID=%d, hostPos=(%d,%d) = win(%d,%d) + offset(%d,%d) + cursor(%d,%d)",
		hostID, hostX, hostY, windowX, windowY, contentOffsetX, contentOffsetY, cursorX, cursorY)

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
		GuestY:       cursorY,
		HostX:        hostX,
		HostY:        hostY,
	}
}

func (kp *KittyPassthrough) forwardPlace(
	cmd *vt.KittyCommand,
	windowID string,
	windowX, windowY int,
	contentOffsetX, contentOffsetY int,
) {
	hostX := windowX + contentOffsetX + cmd.SourceX
	hostY := windowY + contentOffsetY + cmd.SourceY

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
		GuestX:       cmd.SourceX,
		GuestY:       cmd.SourceY,
		HostX:        hostX,
		HostY:        hostY,
		Cols:         cmd.Columns,
		Rows:         cmd.Rows,
	}
	kp.placements[windowID][cmd.ImageID] = placement
}

func (kp *KittyPassthrough) forwardDelete(cmd *vt.KittyCommand, rawData []byte, windowID string) {
	kp.pendingOutput = append(kp.pendingOutput, rawData...)

	switch cmd.Delete {
	case vt.KittyDeleteAll:
		kp.placements[windowID] = nil
	case vt.KittyDeleteByID:
		if placements := kp.placements[windowID]; placements != nil {
			delete(placements, cmd.ImageID)
		}
	}
}

func (kp *KittyPassthrough) OnWindowMove(windowID string, newX, newY, contentOffsetX, contentOffsetY int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	for _, p := range placements {
		kp.deleteOnePlacement(p)

		p.HostX = newX + contentOffsetX + p.GuestX
		p.HostY = newY + contentOffsetY + p.GuestY

		kp.placeOne(p)
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

func (kp *KittyPassthrough) OnWindowScroll(windowID string, scrollDelta int, windowX, windowY, contentOffsetX, contentOffsetY, windowHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	toDelete := make([]uint32, 0)

	for imgID, p := range placements {
		kp.deleteOnePlacement(p)

		p.GuestY -= scrollDelta
		p.HostY = windowY + contentOffsetY + p.GuestY

		if p.HostY < windowY || p.HostY >= windowY+windowHeight {
			toDelete = append(toDelete, imgID)
		} else {
			kp.placeOne(p)
		}
	}

	for _, imgID := range toDelete {
		delete(placements, imgID)
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
				kp.deleteOnePlacement(p)
			}
			delete(kp.placements, windowID)
			continue
		}

		for _, p := range placements {
			kp.deleteOnePlacement(p)

			if !info.Visible {
				continue
			}

			newHostX := info.WindowX + info.ContentOffsetX + p.GuestX
			newHostY := info.WindowY + info.ContentOffsetY + p.GuestY

			if newHostX != p.HostX || newHostY != p.HostY {
				p.HostX = newHostX
				p.HostY = newHostY
			}

			if p.HostY >= info.WindowY && p.HostY < info.WindowY+info.Height {
				kp.placeOne(p)
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
		m.KittyPassthrough.ForwardCommand(
			cmd, rawData, win.ID,
			win.X, win.Y,
			1, 1,
			cursorPos.X, cursorPos.Y,
			func(response []byte) {
				if win.Pty != nil {
					_, _ = win.Pty.Write(response)
				} else if win.DaemonWriteFunc != nil {
					_ = win.DaemonWriteFunc(response)
				}
			},
		)
	})
}
