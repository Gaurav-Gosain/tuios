package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func rendererDebugLog(format string, args ...any) {
	if os.Getenv("TUIOS_DEBUG_INTERNAL") != "1" {
		return
	}
	f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] RENDERER: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type KittyRenderer struct {
	mu             sync.Mutex
	enabled        bool
	hostIDCounter  uint32
	guestToHostID  map[guestImageKey]uint32
	transmittedIDs map[uint32]bool
	commandBuilder *KittyCommandBuilder
}

type guestImageKey struct {
	windowID string
	imageID  uint32
}

func NewKittyRenderer() *KittyRenderer {
	caps := GetHostCapabilities()
	return &KittyRenderer{
		enabled:        caps.KittyGraphics,
		hostIDCounter:  1,
		guestToHostID:  make(map[guestImageKey]uint32),
		transmittedIDs: make(map[uint32]bool),
		commandBuilder: NewKittyCommandBuilder(),
	}
}

func (r *KittyRenderer) IsEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

func (r *KittyRenderer) SetEnabled(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = enabled
}

func (r *KittyRenderer) GetRenderOutput(
	windows []*terminal.Window,
	screenWidth, screenHeight int,
) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return nil
	}

	r.commandBuilder.Reset()
	r.commandBuilder.DeleteVisible()

	for _, w := range windows {
		if w == nil || w.Minimized {
			continue
		}

		if w.Terminal == nil {
			continue
		}

		state := w.Terminal.KittyState()
		if state == nil {
			continue
		}

		images := state.GetImages()
		placements := state.GetPlacements()

		rendererDebugLog("Window %s: %d images, %d placements", w.ID[:8], len(images), len(placements))

		if len(placements) == 0 {
			continue
		}

		scrollbackLen := w.ScrollbackLen()
		visible := TransformPlacements(
			placements,
			w.X, w.Y,
			w.Width, w.Height,
			1, 1,
			scrollbackLen,
			w.ScrollbackOffset,
			screenWidth, screenHeight,
		)

		rendererDebugLog("Window pos=(%d,%d) size=(%dx%d) scrollback=%d offset=%d",
			w.X, w.Y, w.Width, w.Height, scrollbackLen, w.ScrollbackOffset)
		for _, p := range placements {
			rendererDebugLog("  Guest placement: imgID=%d pos=(%d,%d) absLine=%d cols=%d rows=%d",
				p.ImageID, p.ScreenX, p.ScreenY, p.AbsoluteLine, p.Columns, p.Rows)
		}
		for _, vp := range visible {
			rendererDebugLog("  Host placement: imgID=%d hostPos=(%d,%d)", vp.GuestImageID, vp.HostX, vp.HostY)
		}

		for _, vp := range visible {
			img := findImage(images, vp.GuestImageID)
			if img == nil {
				continue
			}

			key := guestImageKey{
				windowID: w.ID,
				imageID:  img.ID,
			}

			hostID, exists := r.guestToHostID[key]
			if !exists {
				hostID = r.allocateHostID()
				r.guestToHostID[key] = hostID
			}

			if !r.transmittedIDs[hostID] {
				r.commandBuilder.TransmitImage(hostID, img)
				r.transmittedIDs[hostID] = true
			}

			caps := GetHostCapabilities()
			r.commandBuilder.PlaceImage(
				hostID, vp.Placement, vp.HostX, vp.HostY,
				vp.ClipLeft, vp.ClipTop, vp.ClipRight, vp.ClipBottom,
				caps.CellWidth, caps.CellHeight,
			)
		}
	}

	r.commandBuilder.Finalize()
	return r.commandBuilder.Bytes()
}

func (r *KittyRenderer) GetClearOutput() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return nil
	}

	r.commandBuilder.Reset()
	r.commandBuilder.DeleteAll()

	r.guestToHostID = make(map[guestImageKey]uint32)
	r.transmittedIDs = make(map[uint32]bool)

	r.commandBuilder.Finalize()
	return r.commandBuilder.Bytes()
}

func (r *KittyRenderer) InvalidateWindow(windowID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for key := range r.guestToHostID {
		if key.windowID == windowID {
			delete(r.guestToHostID, key)
		}
	}
}

func (r *KittyRenderer) allocateHostID() uint32 {
	id := r.hostIDCounter
	r.hostIDCounter++
	if r.hostIDCounter == 0 {
		r.hostIDCounter = 1
	}
	return id
}

func findImage(images []*vt.KittyImage, id uint32) *vt.KittyImage {
	for _, img := range images {
		if img.ID == id {
			return img
		}
	}
	return nil
}
