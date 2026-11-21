package app

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"tuios.dev/vt"
)

type countingWriter struct {
	mu    sync.Mutex
	total int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.total += len(p)
	w.mu.Unlock()
	return len(p), nil
}

func (w *countingWriter) Total() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}

func makeShmFrame(t *testing.T, w, h int) string {
	t.Helper()
	name := fmt.Sprintf("tuios-repro-%d", os.Getpid())
	path := "/dev/shm/" + name
	data := make([]byte, w*h*4)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Skipf("cannot write /dev/shm object (no shm?): %v", err)
	}
	shmFrames.Store(name, data)
	t.Cleanup(func() { _ = os.Remove(path) })
	return name
}

var shmFrames sync.Map

func resendShm(t *testing.T, name string) {
	t.Helper()
	data, ok := shmFrames.Load(name)
	if !ok {
		t.Fatalf("resendShm: %s was never made", name)
	}
	if err := os.WriteFile("/dev/shm/"+name, data.([]byte), 0o600); err != nil {
		t.Fatalf("resend shm: %v", err)
	}
}

func synthShmTransmitPlace(shmName string, w, h int) (*vt.KittyCommand, []byte) {
	cmd := &vt.KittyCommand{
		Action:   vt.KittyActionTransmitPlace,
		Medium:   vt.KittyMediumSharedMemory,
		Format:   vt.KittyFormatRGBA,
		ImageID:  1,
		Width:    w,
		Height:   h,
		More:     false,
		FilePath: shmName,
	}
	raw := []byte("\x1b_Ga=T,t=s,i=1,f=32,s=" + fmt.Sprint(w) + ",v=" + fmt.Sprint(h) + ";" + b64(shmName) + "\x1b\\")
	return cmd, raw
}

func b64(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	src := []byte(s)
	var out []byte
	for i := 0; i < len(src); i += 3 {
		var b [3]byte
		n := copy(b[:], src[i:])
		out = append(out, tbl[b[0]>>2])
		out = append(out, tbl[(b[0]&0x03)<<4|b[1]>>4])
		if n > 1 {
			out = append(out, tbl[(b[1]&0x0f)<<2|b[2]>>6])
		} else {
			out = append(out, '=')
		}
		if n > 2 {
			out = append(out, tbl[b[2]&0x3f])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}

// TestSSHShmFrameSurvives feeds the captured crash frame repeatedly through the
// remote-client re-encode path and the render path, exactly as the ssh tea
// program does, and asserts nothing panics and the host writer keeps receiving
// graphics bytes.
func TestSSHShmFrameSurvives(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true,
		TerminalName:  "kitty",
		CellWidth:     10,
		CellHeight:    20,
	})

	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)

	host := &countingWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		Output:       host,
		RemoteClient: true,
	})
	if !kp.IsEnabled() {
		t.Fatal("expected passthrough enabled")
	}

	const winID = "window-0000-0000-0000-000000000000"
	getWindows := func() map[string]*WindowPositionInfo {
		return map[string]*WindowPositionInfo{
			winID: {
				WindowX: 0, WindowY: 0,
				ContentOffsetX: 1, ContentOffsetY: 1,
				Width: 183, Height: 42,
				Visible:      true,
				ScreenWidth:  183,
				ScreenHeight: 42,
			},
		}
	}

	for range 10 {
		resendShm(t, shmName)
		kp.ForwardCommand(cmd, raw, winID,
			0, 0, 181, 40, 1, 1, 0, 0, 0, false,
			func(resp []byte) {})
		if kp.HasPlacements() {
			kp.RefreshAllPlacements(getWindows)
		}
		if data := kp.FlushPending(); len(data) > 0 {
			kp.WriteToHost(data)
		}
	}

	t.Logf("host received %d bytes over %d frames", host.Total(), 10)
	if host.Total() == 0 {
		t.Fatal("no graphics bytes reached the host writer")
	}
}
