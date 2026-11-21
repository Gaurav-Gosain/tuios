package app

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"tuios.dev/vt"
)

type paneRig struct {
	kp       *KittyPassthrough
	em       *vt.Emulator
	hostFile *os.File
	winID    string
	screenW  int
	screenH  int
	border   int
	winW     int
	winH     int
	consumed int
}

func newPaneRig(t *testing.T, screenW, screenH, border int) *paneRig {
	t.Helper()
	clientCapabilities.Store(&HostCapabilities{
		TerminalName: "kitty", KittyGraphics: true, KittyFileTransfer: true,
		KittyAnimation: true, TrueColor: true, CellWidth: 10, CellHeight: 20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })

	hostFile, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	r := &paneRig{
		hostFile: hostFile, winID: "win-direct-resize",
		screenW: screenW, screenH: screenH, border: border,
		winW: screenW, winH: screenH,
	}
	r.kp = NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: hostFile})
	if !r.kp.IsEnabled() {
		t.Fatal("passthrough not enabled")
	}
	r.em = vt.NewEmulator(screenW-2*border, screenH-2*border)
	r.em.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := r.em.CursorPosition()
		r.kp.ForwardCommand(cmd, rawData, r.winID, 0, 0,
			r.winW-2*r.border, r.winH-2*r.border, r.border, r.border,
			cur.X, cur.Y, r.em.ScrollbackLen(), r.em.IsAltScreen(), func([]byte) {})
	})
	_, _ = r.em.Write([]byte("\x1b[?1049h"))
	return r
}

func (r *paneRig) frame(stream []byte) []string {
	_, _ = r.em.Write(stream)
	info := &WindowPositionInfo{
		ContentOffsetX: r.border, ContentOffsetY: r.border,
		Width: r.winW, Height: r.winH,
		ContentWidth: r.winW - 2*r.border, ContentHeight: r.winH - 2*r.border,
		Visible:     true,
		ScreenWidth: r.screenW, ScreenHeight: r.screenH,
		IsAltScreen:   r.em.IsAltScreen(),
		ScrollbackLen: r.em.ScrollbackLen(),
	}
	r.kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
		return map[string]*WindowPositionInfo{r.winID: info}
	})
	written, _ := os.ReadFile(r.hostFile.Name())
	out := append(append([]byte(nil), written[r.consumed:]...), r.kp.FlushPending()...)
	r.consumed = len(written)
	return graphicsCommands(out)
}

var graphicsParamsRE = regexp.MustCompile(`\x1b_G([^;\x1b]*)`)

func graphicsCommands(out []byte) []string {
	var res []string
	for _, m := range graphicsParamsRE.FindAllSubmatch(out, -1) {
		params := string(m[1])
		if !strings.Contains(params, "a=") {
			continue
		}
		res = append(res, params)
	}
	return res
}
