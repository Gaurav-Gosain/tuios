package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestKittyImageSurvivesCaptureMode is the launch clip's bug: a pane showing an
// image (the clip ran chafa -f kitty), then the leader chord into screenshot
// capture mode, and the image vanished. Capture mode was on the list of
// overlays that take every image off screen, but it covers nothing: it draws a
// hint strip and a marquee over the panes, and the panes are what it captures.
//
// On the wire the bug is a delete. So the host is watched from the moment the
// image is up: opening capture mode, and closing it again, must not delete the
// image, and the host must still be holding a placement for it at the end.
//
// It then takes the window from capture mode and reads the PNG: the image has
// to be in the picture, at its cells. A kitty image is drawn by the host, so
// it is in no cell the capture reads, and a screenshot without it shows the
// blank cells under it (issue 678).
//
// Negative control: making decodeShotPieces in internal/app return nil left
// the window's cells without the image in the PNG, and the check on its
// pixels failed. See NEGATIVE_CONTROLS.md.
func TestKittyImageSurvivesCaptureMode(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			host := newKittyHost()
			term, base := start(t, startOpts{
				cols: 120, rows: 40,
				env:           []string{"TUIOS_SIXEL_GRAPHICS=0", "TMPDIR=" + t.TempDir()},
				out:           host,
				daemonDefault: daemon,
			})
			if daemon {
				killDaemon(t, base)
			}
			host.answerProbe(t, term)
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)

			// Transmit and display in one command, the way chafa does.
			_, img := writeIcatPNG(t, t.TempDir())
			enc := base64.StdEncoding.EncodeToString(img)
			host.mark("image")
			runToExit(t, term, fmt.Sprintf(`printf '\033_Ga=T,q=2,f=100,s=%d,v=%d,c=10,r=5;%s\033\\'`,
				icatImageW, icatImageH, enc))

			hostID := 0
			deadline := time.Now().Add(uiTimeout)
			for hostID == 0 && time.Now().Before(deadline) {
				for _, c := range wireCmds(host.bytes()) {
					if c.phase == "image" && c.action == "p" && c.image != 0 {
						hostID = c.image
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			if hostID == 0 {
				t.Fatalf("the image was never placed on the host\n%s", term.Snapshot())
			}

			host.mark("capture")
			if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
				t.Fatalf("send leader+C: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "capture window", "cancel")
			}, uiTimeout); err != nil {
				t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
			}
			// Frames keep coming while the mode is open; give a hide the
			// chance to happen.
			time.Sleep(time.Second)

			host.mark("closed")
			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatalf("send esc: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !screenHas(s, "capture window")
			}, uiTimeout); err != nil {
				t.Fatalf("capture mode never closed: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(time.Second)

			for _, c := range wireCmds(host.bytes()) {
				if (c.phase == "capture" || c.phase == "closed") && c.action == "d" && c.image == hostID {
					t.Fatalf("the image was deleted in phase %q: %s", c.phase, c.params)
				}
			}

			// Take the window and look for the image in the picture.
			host.mark("shot")
			if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
				t.Fatalf("send leader+C: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "capture window", "cancel")
			}, uiTimeout); err != nil {
				t.Fatalf("capture mode never opened again: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatalf("send enter: %v", err)
			}
			var shotPath string
			deadline = time.Now().Add(uiTimeout)
			for shotPath == "" && time.Now().Before(deadline) {
				shotPath = findCapturePNG(base)
				if shotPath == "" {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if shotPath == "" {
				t.Fatalf("no capture was written under %s\n%s", base, term.Snapshot())
			}
			checkIcatInPicture(t, shotPath)
		})
	}
}

// TestReplyPreviewInScreenCapture takes the whole screen while the reply
// editor shows an image preview on a kitty host. tuios places the preview on
// the host itself, beside the kitty passthrough, so the passthrough does not
// list it, and the capture showed the blank cells under it.
//
// How this could pass wrongly, written down first:
//   - the preview might be drawn as block glyphs, which are cells: the host
//     wire is checked for a placement in the preview's id range first;
//   - the pixels might come from a pane's image: no pane shows one;
//   - the shot might be of a frame without the editor: the gradient is
//     checked, and nothing else on the screen draws it.
//
// Negative control: with the call to replyPreview.shotPieces in
// screenShotPieces removed, the PNG holds none of the gradient. See
// NEGATIVE_CONTROLS.md.
func TestReplyPreviewInScreenCapture(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-shotreply", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	host := newKittyHost()
	term := startIn(t, base, startOpts{
		args: []string{"attach", "e2e-shotreply"},
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
	})
	host.answerProbe(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	dir := shotDir(t, base)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "png")
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "window management mode", "Window management mode")
	time.Sleep(insertGuard)

	hook := func(payload string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-shotreply", "--window", "0", "--timeout", "10s")
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("the hook for %s failed or printed: %v\n%s", payload, err, out)
		}
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-shotreply","prompt":"plot the latency"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-shotreply","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	waitText(t, term, "the working row", "Bash: go test")

	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("G", "k", "k", "r"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the reply editor", "Reply to", "Type your reply.")

	// A wide gradient, so the thumbnail is as wide as the strip allows: red
	// rises from left to right, green is at least 40, blue is 99.
	const w, h = 320, 60
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(40 + y*200/h), 99, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(t.TempDir(), "graph.png")
	if err := os.WriteFile(pngPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	host.mark("preview")
	if err := term.Paste(pngPath); err != nil {
		t.Fatal(err)
	}

	// The preview is a kitty placement of tuios's own, at 0xF2000000 and up.
	placed := false
	deadline := time.Now().Add(uiTimeout)
	for !placed && time.Now().Before(deadline) {
		for _, c := range wireCmds(host.bytes()) {
			if c.phase == "preview" && c.action == "p" && c.image >= 0xF200_0000 && c.image < 0xF300_0000 {
				placed = true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !placed {
		t.Fatalf("the reply preview was never placed on the host\n%s", term.Snapshot())
	}
	saveFrame(t, term, "reply-preview-before-capture")

	before := len(shotFiles(t, dir))
	openCaptureMode(t, term)
	if err := term.SendKeys("f"); err != nil {
		t.Fatal(err)
	}
	files := waitForShot(t, term, dir, before+1)
	shotPath := files[len(files)-1]
	if data, err := os.ReadFile(shotPath); err == nil {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "reply-preview-capture.png"), data, 0o644)
	}
	checkIcatInPicture(t, shotPath)
}

// findCapturePNG returns the first finished PNG capture under base, or "".
func findCapturePNG(base string) string {
	var found string
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		name := d.Name()
		if !d.IsDir() && strings.HasPrefix(name, "tuios-") && strings.HasSuffix(name, ".png") {
			found = path
		}
		return nil
	})
	return found
}

// checkIcatInPicture checks that the test image is drawn in a capture. Its
// pixels are (7x, 11y, 99): the blue channel is 99 everywhere and the red one
// rises from left to right, which neither the theme nor any glyph draws. The
// green rises from top to bottom, and the rows with some green are counted.
// The
// image fills 10 by 5 cells of a window about 118 cells wide, so it is a few
// percent of the picture, and the cells under it are blank.
func checkIcatInPicture(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	b := img.Bounds()
	var n int
	var leftR, rightR, leftN, rightN float64
	type px struct{ x, r int }
	var hits []px
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(r>>8), int(g>>8), int(bl>>8)
			// The frame's wash crosses blue 99 too, but it has almost no
			// green. The image's green is 11y, so most of its rows pass.
			if b8 >= 96 && b8 <= 102 && g8 >= 40 {
				n++
				hits = append(hits, px{x, r8})
			}
		}
	}
	// A cell is at least 10 by 20 pixels at the default scale, so the image
	// is at least 100 by 100. Half of that is far more than stray pixels of
	// antialiased text could make.
	if n < 5000 {
		t.Fatalf("the capture %s has %d pixels of the image, want at least 5000: the image is not in the picture", path, n)
	}
	// Split at the mean column of the matches: the image holds nearly all of
	// them, and the few stray pixels of antialiased text barely move it.
	var sumX int
	for _, h := range hits {
		sumX += h.x
	}
	mid := sumX / len(hits)
	for _, h := range hits {
		if h.x < mid {
			leftR += float64(h.r)
			leftN++
		} else {
			rightR += float64(h.r)
			rightN++
		}
	}
	if leftN == 0 || rightN == 0 || leftR/leftN >= rightR/rightN {
		t.Fatalf("the image in %s does not rise in red from left to right (left %.0f, right %.0f)",
			path, leftR/max(leftN, 1), rightR/max(rightN, 1))
	}
}

// TestLauncherIconsInScreenCapture takes the whole screen while the launcher
// shows its icons on a kitty host. The icons are tuios's own kitty pictures,
// placed as the reply previews are, and the capture showed blank cells.
//
// Each icon is a solid (11, 222, 99), a colour the theme does not draw, so
// the pixels counted are the icons and nothing else.
//
// Negative control: with the call to launcherIcons.shotPieces in
// screenShotPieces removed, the PNG holds none of that colour. See
// NEGATIVE_CONTROLS.md.
func TestLauncherIconsInScreenCapture(t *testing.T) {
	const apps = 6
	stream := &hostStream{}
	base := t.TempDir()
	plantApps(t, base, apps)
	dir := shotDir(t, base)
	writeConfig(t, base, fmt.Sprintf("[screenshot]\ndirectory = %q\nformat = \"png\"\n", dir))
	icons := filepath.Join(base, "XDG_DATA_HOME", "icons", "hicolor", "apps")
	for i := range apps {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := range 32 {
			for x := range 32 {
				img.Set(x, y, color.RGBA{11, 222, 99, 255})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(icons, fmt.Sprintf("zzapp%02d.png", i)), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"--standalone"}, env: iconEnv(base), out: stream})
	waitBoot(t, term)
	f := &launcherFixture{term: term, stream: stream, base: base}
	f.openWithQuery(t, "zzapp", apps)
	// A decode that lands on a still panel waits for the next frame to be
	// placed (see requirePlaced), so the selection is moved until every row
	// has its icon.
	f.requirePlaced(t)
	deadline := time.Now().Add(uiTimeout)
	for len(f.live()) < apps {
		if !time.Now().Before(deadline) {
			t.Fatalf("only %d of %d icons were placed on the host\n%s", len(f.live()), apps, f.term.Snapshot())
		}
		f.nudgeSelection()
		f.waitPlacements(func(p []string) bool { return len(p) >= apps }, nudgeBudget)
	}

	before := len(shotFiles(t, dir))
	openCaptureMode(t, f.term)
	if err := f.term.SendKeys("f"); err != nil {
		t.Fatal(err)
	}
	files := waitForShot(t, f.term, dir, before+1)
	shotPath := files[len(files)-1]
	data, err := os.ReadFile(shotPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(artifactDir(t), "launcher-icons-capture.png"), data, 0o644)
	pic, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", shotPath, err)
	}
	n := 0
	b := pic.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := pic.At(x, y).RGBA()
			if d := absDiff(int(r>>8), 11) + absDiff(int(g>>8), 222) + absDiff(int(bl>>8), 99); d <= 12 {
				n++
			}
		}
	}
	// An icon is about one cell square: at least 10 by 20 pixels at the
	// default scale, so six of them are well over 1000.
	if n < 1000 {
		t.Fatalf("the capture %s has %d pixels of the icons, want at least 1000: the icons are not in the picture", shotPath, n)
	}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
