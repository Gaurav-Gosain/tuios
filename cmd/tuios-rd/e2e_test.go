//go:build linux || darwin

package main

// End-to-end: tuios-rd runs in a pty against a live screen-relay engine. The
// test plays the terminal: it answers the kitty graphics probe, reads each
// frame (shared memory, whole frames or frame edits) into a picture, and
// types and clicks through the pty.
//
//   SRTERM_E2E_URL=http://127.0.0.1:8090 SRTERM_E2E_DISPLAY=:79 go test -run E2E -v

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

type fakeTerm struct {
	t      *testing.T
	f      *os.File
	editOK bool
	shmOK  bool
	cellH  int // the cell height the terminal reports, 20 when 0

	mu       sync.Mutex
	w, h     int
	img      []byte
	frames   int
	edits    int
	shmReads int
	status   string
	bytesIn  int64
	chunk    []byte
	chunkKV  map[string]string
	plain    []byte // text outside escapes
	frame2   []byte // kitty's second frame, the scratch for scroll copies
	pc, pr   int    // placement cells of the last whole picture, 0 when native
	composes int
	deletes  int      // a=d for the picture
	clips    []string // OSC 52 clipboard texts
}

func (ft *fakeTerm) reply(id, msg string) {
	fmt.Fprintf(ft.f, "\x1b_Gi=%s;%s\x1b\\", id, msg)
}

func kv(s string) map[string]string {
	m := map[string]string{}
	for _, p := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(p, "=")
		m[k] = v
	}
	return m
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func unz(p []byte) []byte {
	r, err := zlib.NewReader(bytes.NewReader(p))
	if err != nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func (ft *fakeTerm) apc(body string) {
	keys, payload, _ := strings.Cut(body, ";")
	m := kv(keys)
	if ft.chunk != nil { // continuation
		raw, _ := base64.StdEncoding.DecodeString(payload)
		ft.chunk = append(ft.chunk, raw...)
		if m["m"] == "1" {
			return
		}
		m = ft.chunkKV
		data := ft.chunk
		ft.chunk, ft.chunkKV = nil, nil
		ft.transmit(m, data)
		return
	}
	a := m["a"]
	if a == "" {
		a = "t"
	}
	q := atoi(m["q"])
	switch a {
	case "d":
		if m["i"] == strconv.Itoa(imgID) {
			ft.mu.Lock()
			ft.deletes++
			ft.mu.Unlock()
		}
	case "q":
		if m["t"] == "s" || m["t"] == "f" {
			name, _ := base64.StdEncoding.DecodeString(payload)
			if ft.shmOK {
				os.Remove("/dev/shm/" + strings.TrimPrefix(string(name), "/dev/shm/"))
				ft.reply(m["i"], "OK")
			} else {
				ft.reply(m["i"], "EBADF:no shared memory here")
			}
			return
		}
		ft.reply(m["i"], "OK")
	case "t", "T":
		var data []byte
		switch m["t"] {
		case "f": // a file the terminal reads and leaves
			name, _ := base64.StdEncoding.DecodeString(payload)
			data, _ = os.ReadFile(string(name))
			ft.mu.Lock()
			ft.shmReads++
			ft.mu.Unlock()
		case "s":
			name, _ := base64.StdEncoding.DecodeString(payload)
			p := "/dev/shm/" + strings.TrimPrefix(string(name), "/dev/shm/")
			data, _ = os.ReadFile(p)
			os.Remove(p)
			ft.mu.Lock()
			ft.shmReads++
			ft.mu.Unlock()
		default:
			raw, _ := base64.StdEncoding.DecodeString(payload)
			if m["m"] == "1" {
				ft.chunk, ft.chunkKV = raw, m
				return
			}
			data = raw
		}
		ft.transmit(m, data)
	case "c": // compose: frame r at (X, Y) onto frame c at (x, y)
		ft.mu.Lock()
		src, dst := ft.frame(m["r"]), ft.frame(m["c"])
		if src != nil && dst != nil {
			X, Y, x, y, w, h := atoi(m["X"]), atoi(m["Y"]), atoi(m["x"]), atoi(m["y"]), atoi(m["w"]), atoi(m["h"])
			tmp := make([]byte, w*h*3)
			for r := 0; r < h; r++ {
				copy(tmp[r*w*3:(r+1)*w*3], src[((Y+r)*ft.w+X)*3:((Y+r)*ft.w+X+w)*3])
			}
			for r := 0; r < h; r++ {
				copy(dst[((y+r)*ft.w+x)*3:], tmp[r*w*3:(r+1)*w*3])
			}
			ft.composes++
		}
		ft.mu.Unlock()
	case "f":
		if !ft.editOK {
			if q < 2 {
				ft.reply(m["i"], "ENOTSUPPORTED:unimplemented action")
			}
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(payload)
		if m["o"] == "z" {
			raw = unz(raw)
		}
		ft.mu.Lock()
		if m["i"] == strconv.Itoa(imgID) && ft.img != nil {
			target := ft.frame(m["r"])
			if m["r"] == "" { // a new frame 2 on the base frame c
				ft.frame2 = append([]byte(nil), ft.frame(m["c"])...)
				target = ft.frame2
			}
			x, y, w, h := atoi(m["x"]), atoi(m["y"]), atoi(m["s"]), atoi(m["v"])
			for r := 0; target != nil && r < h && (r+1)*w*3 <= len(raw); r++ {
				copy(target[((y+r)*ft.w+x)*3:], raw[r*w*3:(r+1)*w*3])
			}
			ft.edits++
		}
		ft.mu.Unlock()
		if q == 0 {
			ft.reply(m["i"], "OK")
		}
	}
}

func (ft *fakeTerm) transmit(m map[string]string, data []byte) {
	if m["o"] == "z" {
		data = unz(data)
	}
	if m["i"] == strconv.Itoa(imgID) {
		w, h := atoi(m["s"]), atoi(m["v"])
		ft.mu.Lock()
		if len(data) == w*h*3 {
			ft.w, ft.h, ft.img = w, h, append([]byte(nil), data...)
			ft.frames++
			ft.pc, ft.pr = atoi(m["c"]), atoi(m["r"])
		}
		ft.mu.Unlock()
	}
	if atoi(m["q"]) == 0 {
		ft.reply(m["i"], "OK")
	}
}

func (ft *fakeTerm) read() {
	buf := make([]byte, 1<<20)
	var acc []byte
	for {
		n, err := ft.f.Read(buf)
		if err != nil {
			return
		}
		ft.mu.Lock()
		ft.bytesIn += int64(n)
		ft.mu.Unlock()
		acc = append(acc, buf[:n]...)
		for {
			i := bytes.IndexByte(acc, 0x1b)
			if i < 0 {
				ft.keepPlain(acc)
				acc = acc[:0]
				break
			}
			ft.keepPlain(acc[:i])
			acc = acc[i:]
			if len(acc) < 2 {
				break
			}
			if acc[1] == '_' {
				j := bytes.Index(acc, []byte("\x1b\\"))
				if j < 0 {
					break
				}
				body := string(acc[2:j])
				acc = acc[j+2:]
				if strings.HasPrefix(body, "G") {
					ft.apc(body[1:])
				}
				continue
			}
			if acc[1] == ']' { // OSC, ended by BEL or ST
				j, end := bytes.IndexByte(acc, 0x07), 1
				if st := bytes.Index(acc, []byte("\x1b\\")); st >= 0 && (j < 0 || st < j) {
					j, end = st, 2
				}
				if j < 0 {
					break
				}
				body := string(acc[2:j])
				acc = acc[j+end:]
				if b64, ok := strings.CutPrefix(body, "52;c;"); ok {
					if txt, err := base64.StdEncoding.DecodeString(b64); err == nil {
						ft.mu.Lock()
						ft.clips = append(ft.clips, string(txt))
						ft.mu.Unlock()
					}
				}
				continue
			}
			if acc[1] == '[' {
				j := 2
				for j < len(acc) && (acc[j] < 0x40 || acc[j] > 0x7e) {
					j++
				}
				if j >= len(acc) {
					break
				}
				seq := string(acc[:j+1])
				acc = acc[j+1:]
				switch seq {
				case "\x1b[16t":
					ch := ft.cellH
					if ch == 0 {
						ch = 20
					}
					fmt.Fprintf(ft.f, "\x1b[6;%d;10t", ch)
				case "\x1b[c":
					io.WriteString(ft.f, "\x1b[?62;22c")
				case "\x1b[?1016$p":
					io.WriteString(ft.f, "\x1b[?1016;1$y")
				case "\x1b[7m":
					k := bytes.Index(acc, []byte("\x1b[0m"))
					if k >= 0 {
						ft.mu.Lock()
						ft.status = strings.TrimSpace(string(acc[:k]))
						ft.mu.Unlock()
						acc = acc[k:]
					}
				}
				continue
			}
			acc = acc[1:]
		}
	}
}

// frame returns frame 1 (the shown image) or frame 2. The caller holds mu.
func (ft *fakeTerm) frame(n string) []byte {
	switch n {
	case "1", "":
		return ft.img
	case "2":
		return ft.frame2
	}
	return nil
}

func (ft *fakeTerm) keepPlain(p []byte) {
	ft.mu.Lock()
	ft.plain = append(ft.plain, p...)
	if len(ft.plain) > 1<<16 {
		ft.plain = ft.plain[len(ft.plain)-1<<15:]
	}
	ft.mu.Unlock()
}

func (ft *fakeTerm) snap() (frames, edits int, status string, w, h int) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.frames, ft.edits, ft.status, ft.w, ft.h
}

func buildSrterm(t *testing.T) string {
	bin := filepath.Join(t.TempDir(), "tuios-rd")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func startFake(t *testing.T, bin, url string, shm, edit bool, args ...string) (*fakeTerm, *exec.Cmd) {
	cmd := exec.Command(bin, append(args, url)...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30, X: 1000, Y: 600})
	if err != nil {
		t.Fatal(err)
	}
	ft := &fakeTerm{t: t, f: f, shmOK: shm, editOK: edit}
	go ft.read()
	t.Cleanup(func() {
		f.Write([]byte{0x1c}) // the quit key
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
		}
		f.Close()
	})
	return ft, cmd
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func screenshot(t *testing.T, url string) *image.RGBA {
	resp, err := http.Get(url + "/screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	img, err := png.Decode(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

// compare averages both pictures over a 16x10 grid and returns the mean
// absolute difference per channel, skipping cells inside skip (desktop
// pixels).
func compare(ft *fakeTerm, shot *image.RGBA, skip image.Rectangle) float64 {
	ft.mu.Lock()
	img, w, h := append([]byte(nil), ft.img...), ft.w, ft.h
	ft.mu.Unlock()
	sw, sh := shot.Bounds().Dx(), shot.Bounds().Dy()
	const gx, gy = 16, 10
	var sum float64
	n := 0
	for cy := 0; cy < gy; cy++ {
		for cx := 0; cx < gx; cx++ {
			r := image.Rect(cx*sw/gx, cy*sh/gy, (cx+1)*sw/gx, (cy+1)*sh/gy)
			if r.Overlaps(skip) {
				continue
			}
			var a, b [3]float64
			var na, nb float64
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					o := shot.PixOffset(x, y)
					for c := 0; c < 3; c++ {
						b[c] += float64(shot.Pix[o+c])
					}
					nb++
				}
			}
			for y := r.Min.Y * h / sh; y < r.Max.Y*h/sh; y++ {
				for x := r.Min.X * w / sw; x < r.Max.X*w/sw; x++ {
					for c := 0; c < 3; c++ {
						a[c] += float64(img[(y*w+x)*3+c])
					}
					na++
				}
			}
			for c := 0; c < 3; c++ {
				d := a[c]/na - b[c]/nb
				if d < 0 {
					d = -d
				}
				sum += d
			}
			n += 3
		}
	}
	return sum / float64(n)
}

func xdo(t *testing.T, display string, args ...string) string {
	cmd := exec.Command("xdotool", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xdotool %v: %v %s", args, err, out)
	}
	return string(out)
}

func postInput(url string) int {
	b := rec(recMove, 0, 0, 100, 100, 0, 0)
	resp, err := http.Post(url+"/input", "application/octet-stream", bytes.NewReader(b[:]))
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestE2E(t *testing.T) {
	url := os.Getenv("SRTERM_E2E_URL")
	display := os.Getenv("SRTERM_E2E_DISPLAY")
	if url == "" || display == "" {
		t.Skip("set SRTERM_E2E_URL and SRTERM_E2E_DISPLAY")
	}
	bin := buildSrterm(t)
	// The mpv window moves all the time; leave it out of the comparison.
	skip := image.Rect(0, 0, 700, 460)
	if s := os.Getenv("SRTERM_E2E_SKIP"); s != "" {
		f := strings.Split(s, ",")
		skip = image.Rect(atoi(f[0]), atoi(f[1]), atoi(f[2]), atoi(f[3]))
	}

	for _, c := range []struct {
		name       string
		shm, edit  bool
		wantStatus string
	}{
		{"shm", true, true, "shm"},
		{"edits", false, true, "edits"},
		{"frames", false, false, "frames"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ft, _ := startFake(t, bin, url, c.shm, c.edit)
			waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
			time.Sleep(2 * time.Second)
			frames, edits, status, w, h := ft.snap()
			t.Logf("frames %d, edits %d, image %dx%d, status %q", frames, edits, w, h, status)
			if !strings.Contains(status, c.wantStatus) || !strings.Contains(status, "Shared with agents") {
				t.Fatalf("status %q", status)
			}
			// 1000x580 pixels above the status line; the desktop is 1280x800.
			// 0.725 of 1280 is 928, which rounds to 93 cells of 10 pixels.
			// Tiles send the desktop's own size and the terminal scales it,
			// except for whole frames, which tuios-rd sends at the pane's size.
			if strings.Contains(status, "tiles") && c.name != "frames" {
				if w != 1280 || h != 800 {
					t.Fatalf("tiles image %dx%d, want the desktop size 1280x800", w, h)
				}
			} else if w != 930 || h != 580 {
				t.Fatalf("image %dx%d, want 930x580", w, h)
			}
			if c.name == "edits" && edits == 0 && !strings.Contains(status, "still") {
				t.Fatal("no frame edits")
			}
			if d := compare(ft, screenshot(t, url), skip); d > 6 {
				t.Fatalf("picture differs from the desktop: mean %.1f", d)
			} else {
				t.Logf("picture matches the desktop: mean difference %.2f", d)
			}
		})
	}

	t.Run("resize", func(t *testing.T) {
		ft, cmd := startFake(t, bin, url, true, true)
		waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
		// 60x20 cells of 10x20: 600x380 pixels above the status line.
		pty.Setsize(ft.f, &pty.Winsize{Cols: 60, Rows: 20, X: 600, Y: 400})
		cmd.Process.Signal(syscall.SIGWINCH)
		// The desktop does not change here: the redraw must come from the last frame.
		_, _, status, _, _ := ft.snap()
		if strings.Contains(status, "tiles") {
			// Tiles keep the desktop size; the placement shrinks to the pane.
			waitFor(t, "a smaller placement", 2*time.Second, func() bool {
				ft.mu.Lock()
				defer ft.mu.Unlock()
				return ft.pc > 0 && ft.pc <= 60 && ft.pr <= 19
			})
			t.Logf("after resize: placement %dx%d cells", ft.pc, ft.pr)
			return
		}
		waitFor(t, "a smaller image", 2*time.Second, func() bool { _, _, _, w, _ := ft.snap(); return w < 930 })
		_, _, _, w, h := ft.snap()
		if w > 600 || h > 380 {
			t.Fatalf("image %dx%d does not fit 600x380", w, h)
		}
		if w%10 != 0 || h%20 != 0 {
			t.Fatalf("image %dx%d is not a whole number of 10x20 cells", w, h)
		}
		t.Logf("after resize: %dx%d", w, h)
	})

	t.Run("input", func(t *testing.T) {
		ft, _ := startFake(t, bin, url, true, true)
		waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
		// The picture is 93 of 100 columns, centred: it starts 3 cells (30 px) in.
		// A click at pane pixel (500, 300) lands at 470/930 and 300/580 of the desktop.
		fmt.Fprintf(ft.f, "\x1b[<35;501;301M")
		time.Sleep(300 * time.Millisecond)
		loc := xdo(t, display, "getmouselocation")
		var x, y int
		fmt.Sscanf(loc, "x:%d y:%d", &x, &y)
		wantX, wantY := 470*1280/930, 300*800/580
		if abs(x-wantX) > 2 || abs(y-wantY) > 2 {
			t.Fatalf("pointer at %d,%d, want %d,%d", x, y, wantX, wantY)
		}
		t.Logf("pointer at %d,%d", x, y)
		// Share: while the person moves, tuios-rd holds input. 2 s after the
		// last input it gives input back, and an agent can send input again.
		if code := postInput(url); code == 200 {
			t.Fatal("POST /input works while the person's input is held")
		}
		time.Sleep(2500 * time.Millisecond)
		if code := postInput(url); code != 200 {
			t.Fatalf("POST /input 2.5 s after the last input in share mode: %d", code)
		}
		// Take control: agents are blocked.
		ft.f.Write([]byte{0x1d})
		waitFor(t, "control", 5*time.Second, func() bool { _, _, s, _, _ := ft.snap(); return strings.Contains(s, "You have control") })
		if code := postInput(url); code == 200 {
			t.Fatal("POST /input works while tuios-rd has control")
		}
		// Input in control mode goes on the stream.
		fmt.Fprintf(ft.f, "\x1b[<35;131;101M")
		time.Sleep(300 * time.Millisecond)
		loc = xdo(t, display, "getmouselocation")
		fmt.Sscanf(loc, "x:%d y:%d", &x, &y)
		if abs(x-100*1280/930) > 2 || abs(y-100*800/580) > 2 {
			t.Fatalf("control mode: pointer at %d,%d", x, y)
		}
		// Give it back.
		ft.f.Write([]byte{0x1d})
		waitFor(t, "share", 5*time.Second, func() bool { _, _, s, _, _ := ft.snap(); return strings.Contains(s, "Shared with agents") })
		time.Sleep(300 * time.Millisecond)
		if code := postInput(url); code != 200 {
			t.Fatalf("POST /input after giving control back: %d", code)
		}
	})

	t.Run("typing", func(t *testing.T) {
		out := os.Getenv("SRTERM_E2E_TYPED")
		win := os.Getenv("SRTERM_E2E_WINDOW")
		if out == "" || win == "" {
			t.Skip("set SRTERM_E2E_TYPED (a file cat writes) and SRTERM_E2E_WINDOW (its window id)")
		}
		ft, _ := startFake(t, bin, url, true, true)
		waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
		xdo(t, display, "windowfocus", win)
		io.WriteString(ft.f, "Hi there!\r")
		ft.f.Write([]byte{0x1b, '[', '2', '0', '0', '~'})
		io.WriteString(ft.f, "pasted line")
		ft.f.Write([]byte{0x1b, '[', '2', '0', '1', '~'})
		io.WriteString(ft.f, "\r")
		waitFor(t, "typed text", 5*time.Second, func() bool {
			b, _ := os.ReadFile(out)
			return strings.Contains(string(b), "Hi there!\npasted line\n")
		})
	})
}

// TestMeasure runs tuios-rd against SRTERM_MEASURE_URL in a pty with the test
// as the terminal, for SRTERM_MEASURE_SECS, and logs tuios-rd's summary.
// SRTERM_MEASURE_MODE is shm, edits or frames.
func TestMeasure(t *testing.T) {
	url := os.Getenv("SRTERM_MEASURE_URL")
	if url == "" {
		t.Skip("set SRTERM_MEASURE_URL")
	}
	secs := os.Getenv("SRTERM_MEASURE_SECS")
	if secs == "" {
		secs = "15"
	}
	mode := os.Getenv("SRTERM_MEASURE_MODE")
	shm, edit := mode == "shm", mode != "frames"
	bin := buildSrterm(t)
	report := filepath.Join(t.TempDir(), "sum")
	stats := os.Getenv("SRTERM_MEASURE_STATS")
	args := []string{"--duration", secs + "s", "--report", report, "--mode", mode}
	if stats != "" {
		args = append(args, "--stats", stats)
	}
	args = append(args, strings.Fields(os.Getenv("SRTERM_MEASURE_ARGS"))...)
	ft, cmd := startFake(t, bin, url, shm, edit, args...)
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Duration(atoi(secs)+20) * time.Second):
		t.Fatal("tuios-rd did not stop")
	}
	b, _ := os.ReadFile(report)
	ft.mu.Lock()
	in := ft.bytesIn
	ft.mu.Unlock()
	t.Logf("%s(pty bytes read by the test terminal: %.0f KB/s)", b, float64(in)/float64(atoi(secs))/1024)
}

// TestPluginScript runs the tuios plugin's pane script (SRTERM_PLUGIN_SCRIPT)
// in a pty with the test as the terminal. SRTERM_PLUGIN_BIN is a folder with
// an tuios-rd that quits on its own. It checks that frames arrive and that the
// ssh tunnel stops with the pane.
func TestPluginScript(t *testing.T) {
	script := os.Getenv("SRTERM_PLUGIN_SCRIPT")
	if script == "" {
		t.Skip("set SRTERM_PLUGIN_SCRIPT")
	}
	// The outer shell stays after the script ends, as a terminal does, so
	// no hangup stops the tunnel for the script.
	cmd := exec.Command("sh", append([]string{"-c", "sh \"$0\" \"$@\"; sleep 4", script}, strings.Fields(os.Getenv("SRTERM_PLUGIN_ARGS"))...)...)
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("SRTERM_PLUGIN_BIN")+":"+os.Getenv("PATH"))
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30, X: 1000, Y: 600})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ft := &fakeTerm{t: t, f: f, shmOK: true, editOK: true}
	go ft.read()
	waitFor(t, "frames", 30*time.Second, func() bool { n, _, _, _, _ := ft.snap(); return n >= 1 })
	_, _, status, w, h := ft.snap()
	t.Logf("image %dx%d, status %q", w, h, status)
	// tuios-rd quits on its own; then the outer shell sleeps for 4 s.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	gone := false
	for end := time.Now().Add(40 * time.Second); time.Now().Before(end); {
		out, _ := exec.Command("pgrep", "-af", "tuios-rd .*http://127.0.0.1:").Output()
		if len(bytes.TrimSpace(out)) == 0 {
			gone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatal("tuios-rd did not stop")
	}
	time.Sleep(time.Second)
	out, _ := exec.Command("pgrep", "-af", "ssh .*-N -L 127.0.0.1:[2-5][0-9]{4}:127.0.0.1:808[01]").Output()
	if len(bytes.TrimSpace(out)) > 0 {
		t.Fatalf("the tunnel still runs after the script ended:\n%s", out)
	}
	if err := <-done; err != nil {
		t.Fatalf("the script failed: %v", err)
	}
}

// TestSnapshot saves the picture tuios-rd draws for a pane of
// SRTERM_SNAP_COLS x SRTERM_SNAP_ROWS cells of 10x22 pixels to SRTERM_SNAP_PNG.
func TestSnapshot(t *testing.T) {
	url, out := os.Getenv("SRTERM_SNAP_URL"), os.Getenv("SRTERM_SNAP_PNG")
	if url == "" || out == "" {
		t.Skip("set SRTERM_SNAP_URL and SRTERM_SNAP_PNG")
	}
	cols, rows := atoi(os.Getenv("SRTERM_SNAP_COLS")), atoi(os.Getenv("SRTERM_SNAP_ROWS"))
	bin := buildSrterm(t)
	cmd := exec.Command(bin, url)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows), X: uint16(cols * 10), Y: uint16(rows * 22)})
	if err != nil {
		t.Fatal(err)
	}
	ft := &fakeTerm{t: t, f: f, shmOK: true, editOK: true, cellH: 22}
	go ft.read()
	defer func() { f.Write([]byte{0x1c}); cmd.Wait(); f.Close() }()
	waitFor(t, "frames", 15*time.Second, func() bool { n, _, _, _, _ := ft.snap(); return n >= 1 })
	time.Sleep(1500 * time.Millisecond)
	ft.mu.Lock()
	img := image.NewRGBA(image.Rect(0, 0, ft.w, ft.h))
	for i := 0; i < ft.w*ft.h; i++ {
		copy(img.Pix[i*4:i*4+3], ft.img[i*3:i*3+3])
		img.Pix[i*4+3] = 255
	}
	ft.mu.Unlock()
	o, _ := os.Create(out)
	png.Encode(o, img)
	o.Close()
	t.Logf("saved %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
}

// TestKeyLatency types into a window on the test desktop and times each key
// from the pty write to the first picture that changed in the window's area.
// SRTERM_E2E_WINDOW is the window; SRTERM_E2E_AREA is x0,y0,x1,y1 in picture
// pixels; SRTERM_INPUT is socket or post.
func TestKeyLatency(t *testing.T) {
	url, display, win := os.Getenv("SRTERM_E2E_URL"), os.Getenv("SRTERM_E2E_DISPLAY"), os.Getenv("SRTERM_E2E_WINDOW")
	area := strings.Split(os.Getenv("SRTERM_E2E_AREA"), ",")
	if url == "" || win == "" || len(area) != 4 {
		t.Skip("set SRTERM_E2E_URL, SRTERM_E2E_DISPLAY, SRTERM_E2E_WINDOW and SRTERM_E2E_AREA")
	}
	x0, y0, x1, y1 := atoi(area[0]), atoi(area[1]), atoi(area[2]), atoi(area[3])
	bin := buildSrterm(t)
	ft, _ := startFake(t, bin, url, true, true, append([]string{"--input", os.Getenv("SRTERM_INPUT")}, strings.Fields(os.Getenv("SRTERM_KEY_ARGS"))...)...)
	waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
	xdo(t, display, "windowfocus", win)
	sum := func() uint64 {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		var s uint64
		for y := y0; y < y1 && y < ft.h; y++ {
			for x := x0; x < x1 && x < ft.w; x++ {
				o := (y*ft.w + x) * 3
				s = s*1099511628211 + uint64(ft.img[o])<<16 + uint64(ft.img[o+1])<<8 + uint64(ft.img[o+2])
			}
		}
		return s
	}
	time.Sleep(500 * time.Millisecond)
	var lat []time.Duration
	n := max(atoi(os.Getenv("SRTERM_KEYS")), 20)
	for i := 0; i < n; i++ {
		before := sum()
		t0 := time.Now()
		key := "x"
		if os.Getenv("SRTERM_KEY") == "down-up" { // a pager: down, then up
			key = "\x1b[B"
			if i%2 == 1 {
				key = "\x1b[A"
			}
		}
		io.WriteString(ft.f, key)
		if key == "x" && i%50 == 49 {
			io.WriteString(ft.f, "\r")
			time.Sleep(100 * time.Millisecond)
		}
		for time.Since(t0) < time.Second {
			if sum() != before {
				lat = append(lat, time.Since(t0))
				break
			}
			time.Sleep(200 * time.Microsecond)
		}
		time.Sleep(150 * time.Millisecond)
	}
	io.WriteString(ft.f, "\r")
	if len(lat) < n*3/4 {
		t.Fatalf("only %d of %d keys changed the picture", len(lat), n)
	}
	slices.Sort(lat)
	t.Logf("input %s: key to picture p50 %.1f ms, p90 %.1f ms, max %.1f ms (%d keys)", os.Getenv("SRTERM_INPUT"),
		float64(lat[len(lat)/2].Microseconds())/1000, float64(lat[len(lat)*9/10].Microseconds())/1000, float64(lat[len(lat)-1].Microseconds())/1000, len(lat))
}

// TestInputRTT times a harmless "release all" record to SRTERM_RTT_URL:
// POST /input against a request on a held /control socket.
func TestInputRTT(t *testing.T) {
	url := os.Getenv("SRTERM_RTT_URL")
	if url == "" {
		t.Skip("set SRTERM_RTT_URL")
	}
	token := os.Getenv("SCREEN_RELAY_TOKEN")
	r := rec(recRelease, 0, 0, 0, 0, 0, 0)
	stats := func(name string, d []time.Duration) {
		slices.Sort(d)
		t.Logf("%s: p50 %.1f ms, p90 %.1f ms, max %.1f ms (%d)", name, float64(d[len(d)/2].Microseconds())/1000,
			float64(d[len(d)*9/10].Microseconds())/1000, float64(d[len(d)-1].Microseconds())/1000, len(d))
	}
	cl := &http.Client{}
	var post []time.Duration
	for i := 0; i < 40; i++ {
		req, _ := http.NewRequest("POST", url+"/input", bytes.NewReader(r[:]))
		req.Header.Set("Authorization", "Bearer "+token)
		t0 := time.Now()
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("POST /input: %s", resp.Status)
		}
		post = append(post, time.Since(t0))
		time.Sleep(20 * time.Millisecond)
	}
	stats("POST /input", post)

	ctx := context.Background()
	protos := []string{"nsl1"}
	if token != "" {
		protos = append(protos, "bearer."+token)
	}
	t0 := time.Now()
	c, _, err := websocket.Dial(ctx, strings.Replace(url, "http", "ws", 1)+"/control", &websocket.DialOptions{Subprotocols: protos})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	_, m, err := c.Read(ctx)
	if err != nil || m[5] != 4 {
		t.Fatalf("no control-ready message: %v", err)
	}
	t.Logf("/control open and ready: %.1f ms", float64(time.Since(t0).Microseconds())/1000)
	gen := binary.BigEndian.Uint32(m[8:])
	var sock []time.Duration
	for i := 1; i <= 40; i++ {
		msg := make([]byte, 36)
		copy(msg, "NSL1")
		msg[4], msg[5] = 1, 3
		binary.BigEndian.PutUint32(msg[8:], gen)
		binary.BigEndian.PutUint32(msg[12:], uint32(i))
		binary.BigEndian.PutUint32(msg[16:], 16)
		copy(msg[20:], r[:])
		t0 := time.Now()
		if err := c.Write(ctx, websocket.MessageBinary, msg); err != nil {
			t.Fatal(err)
		}
		_, m, err := c.Read(ctx)
		if err != nil || m[5] != 5 || m[20] != 0 {
			t.Fatalf("reply: %v %v", err, m)
		}
		sock = append(sock, time.Since(t0))
		time.Sleep(20 * time.Millisecond)
	}
	stats("/control socket", sock)
}

// TestRefine needs a desktop that does not move. With refine on, the
// picture must equal the engine's screenshot, scaled by tuios-rd's filter.
// With --refine 0 it must not: the H.264 frame is lossy.
func TestRefine(t *testing.T) {
	url := os.Getenv("SRTERM_E2E_URL")
	if url == "" || os.Getenv("SRTERM_E2E_STILL") == "" {
		t.Skip("set SRTERM_E2E_URL and SRTERM_E2E_STILL (a desktop that does not move)")
	}
	bin := buildSrterm(t)
	maxDiff := func(args ...string) (int, float64) {
		ft, _ := startFake(t, bin, url, true, true, args...)
		waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
		time.Sleep(1500 * time.Millisecond)
		shot := screenshot(t, url)
		var sp stillPicture
		sp.fromImage(shot)
		ft.mu.Lock()
		defer ft.mu.Unlock()
		want := make([]byte, ft.w*ft.h*3)
		sp.scale(want, ft.w, ft.h, 4)
		m, sum := 0, 0
		for i := range want {
			d := abs(int(want[i]) - int(ft.img[i]))
			m = max(m, d)
			sum += d
		}
		return m, float64(sum) / float64(len(want))
	}
	onMax, onMean := maxDiff()
	offMax, offMean := maxDiff("--refine", "0")
	t.Logf("refine on: max difference %d, mean %.3f; refine off: max %d, mean %.3f", onMax, onMean, offMax, offMean)
	if onMax > 1 {
		t.Fatalf("with refine, the picture is not the lossless screenshot (max difference %d)", onMax)
	}
	if offMax <= 1 {
		t.Fatal("without refine the picture is lossless too, so this test proves nothing")
	}
}

// TestNoDisplay runs against an engine whose display is black
// (SRTERM_BLACK_URL, X display SRTERM_BLACK_DISPLAY). tuios-rd must take the
// picture away and show one line, then bring the picture back when the
// display shows something.
func TestNoDisplay(t *testing.T) {
	url, display := os.Getenv("SRTERM_BLACK_URL"), os.Getenv("SRTERM_BLACK_DISPLAY")
	if url == "" || display == "" {
		t.Skip("set SRTERM_BLACK_URL and SRTERM_BLACK_DISPLAY")
	}
	setRoot := func(color string) {
		cmd := exec.Command("xsetroot", "-solid", color)
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("xsetroot: %v %s", err, out)
		}
	}
	setRoot("black")
	bin := buildSrterm(t)
	ft, _ := startFake(t, bin, url, true, true)
	waitFor(t, "the no-picture line", 6*time.Second, func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		return ft.deletes > 0 && bytes.Contains(ft.plain, []byte("The desktop has no picture."))
	})
	ft.mu.Lock()
	f0 := ft.frames
	ft.mu.Unlock()
	setRoot("gray50")
	waitFor(t, "the picture back", 6*time.Second, func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		return ft.frames > f0 && len(ft.img) > 0 && ft.img[0] > 60
	})
	setRoot("black")
}

// TestWhy points tuios-rd at a port where nothing listens. The status line
// must show the first line of --why.
func TestWhy(t *testing.T) {
	bin := buildSrterm(t)
	ft, _ := startFake(t, bin, "http://127.0.0.1:1", true, true, "--why", "echo The Mac display went away; echo second line")
	waitFor(t, "the reason in the status line", 6*time.Second, func() bool {
		_, _, s, _, _ := ft.snap()
		return strings.Contains(s, "The Mac display went away") && !strings.Contains(s, "second line")
	})
}

// TestTilesExact needs a still desktop on a tiles engine. The tiles
// picture must equal the engine's screenshot scaled by tuios-rd's filter, up
// to rounding (1 level).
func TestTilesExact(t *testing.T) {
	url := os.Getenv("SRTERM_TILES_URL")
	if url == "" || os.Getenv("SRTERM_E2E_STILL") == "" {
		t.Skip("set SRTERM_TILES_URL and SRTERM_E2E_STILL")
	}
	bin := buildSrterm(t)
	ft, _ := startFake(t, bin, url, true, true, "--codec", "tiles", "--refine", "0")
	waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
	time.Sleep(1500 * time.Millisecond)
	var sp stillPicture
	sp.fromImage(screenshot(t, url))
	ft.mu.Lock()
	defer ft.mu.Unlock()
	want := make([]byte, ft.w*ft.h*3)
	sp.scale(want, ft.w, ft.h, 4)
	m, bad := 0, 0
	for i := range want {
		d := abs(int(want[i]) - int(ft.img[i]))
		m = max(m, d)
		if d > 1 {
			bad++
		}
	}
	t.Logf("tiles picture %dx%d: max difference %d, %d samples off by more than 1", ft.w, ft.h, m, bad)
	if m > 1 {
		t.Fatalf("the tiles picture is not the desktop (max difference %d)", m)
	}
}

// TestScroll scrolls a pager on SRTERM_SCROLL_DISPLAY with xdotool for
// SRTERM_SCROLL_SECS while tuios-rd shows SRTERM_SCROLL_URL, and logs the
// medians of tuios-rd's stats: fps, bytes on the wire, CPU. With
// SRTERM_ENGINE_PID it also logs the engine's CPU.
func TestScroll(t *testing.T) {
	url, display := os.Getenv("SRTERM_SCROLL_URL"), os.Getenv("SRTERM_SCROLL_DISPLAY")
	if url == "" || display == "" {
		t.Skip("set SRTERM_SCROLL_URL and SRTERM_SCROLL_DISPLAY")
	}
	secs := max(atoi(os.Getenv("SRTERM_SCROLL_SECS")), 8)
	stats := filepath.Join(t.TempDir(), "stats")
	bin := buildSrterm(t)
	args := append([]string{"--stats", stats}, strings.Fields(os.Getenv("SRTERM_SCROLL_ARGS"))...)
	var ft *fakeTerm
	if p := os.Getenv("SRTERM_SCROLL_PANE"); p != "" { // COLSxROWS, cells of 10x22
		var cols, rows int
		fmt.Sscanf(p, "%dx%d", &cols, &rows)
		cmd := exec.Command(bin, append(args, url)...)
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows), X: uint16(cols * 10), Y: uint16(rows * 22)})
		if err != nil {
			t.Fatal(err)
		}
		ft = &fakeTerm{t: t, f: f, shmOK: os.Getenv("SRTERM_SCROLL_MODE") != "edits", editOK: true, cellH: 22}
		go ft.read()
		t.Cleanup(func() { f.Write([]byte{0x1c}); cmd.Wait(); f.Close() })
	} else {
		ft, _ = startFake(t, bin, url, os.Getenv("SRTERM_SCROLL_MODE") != "edits", true, args...)
	}
	waitFor(t, "frames", 10*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
	_, _, _, iw, ih := ft.snap()
	t.Logf("picture %dx%d", iw, ih)
	cpuTicks := func() int64 {
		b, err := os.ReadFile("/proc/" + os.Getenv("SRTERM_ENGINE_PID") + "/stat")
		if err != nil {
			return -1
		}
		f := strings.Fields(string(b)[strings.LastIndexByte(string(b), ')')+2:])
		return int64(atoi(f[11]) + atoi(f[12]))
	}
	time.Sleep(time.Second)
	c0, t0 := cpuTicks(), time.Now()
	// Down then up, so the pager never reaches an end and stops.
	half := secs * 60 / 2
	cmd := exec.Command("sh", "-c", fmt.Sprintf("xdotool key --delay 16 --repeat %d Down; xdotool key --delay 16 --repeat %d Up", half, half))
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("xdotool: %v %s", err, out)
	}
	c1, el := cpuTicks(), time.Since(t0).Seconds()
	time.Sleep(1100 * time.Millisecond)
	// After the scroll, the picture must be the desktop. For tiles it is
	// the desktop's own size, so it must be exact.
	if strings.Contains(os.Getenv("SRTERM_SCROLL_ARGS"), "tiles") {
		shot := screenshot(t, url)
		ft.mu.Lock()
		m, comp := 0, ft.composes
		if ft.w == shot.Bounds().Dx() && ft.h == shot.Bounds().Dy() {
			for y := 0; y < ft.h; y++ {
				for x := 0; x < ft.w; x++ {
					o, s := (y*ft.w+x)*3, shot.PixOffset(x, y)
					for c := 0; c < 3; c++ {
						m = max(m, abs(int(ft.img[o+c])-int(shot.Pix[s+c])))
					}
				}
			}
		} else {
			m = -1 // the engine scaled the capture: no full-size screenshot to compare
		}
		ft.mu.Unlock()
		t.Logf("after the scroll: max difference %d from the screenshot, %d a=c copies", m, comp)
		if m > 0 {
			t.Errorf("the picture after the scroll is not the desktop")
		}
	}
	b, _ := os.ReadFile(stats)
	type line struct {
		FPS  float64 `json:"fps"`
		Wire float64 `json:"wire_kb_s"`
		CPU  float64 `json:"cpu_pct"`
		Lat  float64 `json:"lat_p50_ms"`
		Recv float64 `json:"recv_fps"`
		Pty  float64 `json:"kb_s"`
	}
	var ls []line
	for _, s := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var l line
		if json.Unmarshal([]byte(s), &l) == nil && l.FPS > 5 {
			ls = append(ls, l)
		}
	}
	if len(ls) < 3 {
		t.Fatalf("only %d seconds with frames", len(ls))
	}
	med := func(get func(line) float64) float64 {
		v := make([]float64, len(ls))
		for i, l := range ls {
			v[i] = get(l)
		}
		slices.Sort(v)
		return v[len(v)/2]
	}
	eng := "not measured"
	if c0 >= 0 && c1 >= 0 {
		eng = fmt.Sprintf("%.0f%%", float64(c1-c0)/el)
	}
	t.Logf("pty %.0f KB/s", med(func(l line) float64 { return l.Pty }))
	t.Logf("scroll, %d s with frames: fps %.0f (received %.0f), wire %.0f KB/s, latency p50 %.1f ms, tuios-rd CPU %.0f%%, engine CPU %s",
		len(ls), med(func(l line) float64 { return l.FPS }), med(func(l line) float64 { return l.Recv }),
		med(func(l line) float64 { return l.Wire }), med(func(l line) float64 { return l.Lat }), med(func(l line) float64 { return l.CPU }), eng)
}

// TestTilesSize needs an engine with "tiles_scale" (SRTERM_SIZE_URL) and a
// 1320x858 desktop. A 66x20 pane of 10x22 cells asks for 660x418, which
// is k=2: a 660x429 picture. Widened to 132x40, tuios-rd asks again and gets
// the desktop's own 1320x858.
func TestTilesSize(t *testing.T) {
	url := os.Getenv("SRTERM_SIZE_URL")
	if url == "" {
		t.Skip("set SRTERM_SIZE_URL")
	}
	bin := buildSrterm(t)
	cmd := exec.Command(bin, "--codec", "tiles", url)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 66, Rows: 20, X: 660, Y: 440})
	if err != nil {
		t.Fatal(err)
	}
	ft := &fakeTerm{t: t, f: f, shmOK: true, editOK: true, cellH: 22}
	go ft.read()
	defer func() { f.Write([]byte{0x1c}); cmd.Wait(); f.Close() }()
	waitFor(t, "a 660x429 picture", 10*time.Second, func() bool { _, _, _, w, h := ft.snap(); return w == 660 && h == 429 })
	pty.Setsize(f, &pty.Winsize{Cols: 132, Rows: 40, X: 1320, Y: 880})
	cmd.Process.Signal(syscall.SIGWINCH)
	waitFor(t, "a 1320x858 picture", 10*time.Second, func() bool { _, _, _, w, h := ft.snap(); return w == 1320 && h == 858 })
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

var _ = binary.BigEndian

// TestClipboard checks clipboard sync in control mode against a live engine.
//
// On X (SRTERM_CLIP_DISPLAY), SRTERM_CLIP_WINDOW is an alacritty with Ctrl+V
// bound to Paste and Super+C bound to the text "<SUPER-C>", running
//
//	sh -c 'stty -isig; (while sleep 0.1; do [ -f T ] && printf "\033]52;c;%s\007" "$(base64 -w0 T)" && rm T; done) & cat > TYPED'
//
// so a file written to SRTERM_CLIP_TRIGGER (T) becomes a desktop copy and
// what reaches the window lands in SRTERM_CLIP_TYPED.
//
// Elsewhere (a Mac), SRTERM_CLIP_COPY_CMD makes a desktop copy of its stdin
// (ssh mac pbcopy), SRTERM_CLIP_TYPED_CMD prints what reached the focused
// window, and SRTERM_CLIP_NO_SUPER=1 skips the Super+C check (Terminal keeps
// Cmd+C for itself).
func TestClipboard(t *testing.T) {
	url := os.Getenv("SRTERM_CLIP_URL")
	display, win := os.Getenv("SRTERM_CLIP_DISPLAY"), os.Getenv("SRTERM_CLIP_WINDOW")
	trigger, typed := os.Getenv("SRTERM_CLIP_TRIGGER"), os.Getenv("SRTERM_CLIP_TYPED")
	copyCmd, typedCmd := os.Getenv("SRTERM_CLIP_COPY_CMD"), os.Getenv("SRTERM_CLIP_TYPED_CMD")
	if url == "" || (copyCmd == "" && trigger == "") || (typedCmd == "" && typed == "") {
		t.Skip("set SRTERM_CLIP_URL and either _DISPLAY, _WINDOW, _TRIGGER, _TYPED or _COPY_CMD, _TYPED_CMD")
	}
	desktopCopy := func(s string) {
		if copyCmd == "" {
			os.WriteFile(trigger, []byte(s), 0o644)
			return
		}
		cmd := exec.Command("sh", "-c", copyCmd)
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("copy command: %v %s", err, out)
		}
	}
	readTyped := func() string {
		if typedCmd == "" {
			b, _ := os.ReadFile(typed)
			return string(b)
		}
		b, _ := exec.Command("sh", "-c", typedCmd).Output()
		return string(b)
	}
	token := os.Getenv("SCREEN_RELAY_TOKEN")
	bin := buildSrterm(t)
	ft, _ := startFake(t, bin, url, true, true)
	waitFor(t, "frames", 15*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
	if display != "" {
		xdo(t, display, "windowfocus", win)
	}
	ft.f.Write([]byte{0x1d})
	waitFor(t, "control", 5*time.Second, func() bool { _, _, s, _, _ := ft.snap(); return strings.Contains(s, "You have control") })
	time.Sleep(300 * time.Millisecond)

	// (1) A desktop app copies: the host terminal gets OSC 52.
	want := fmt.Sprintf("copied on the desktop %d ✓", time.Now().UnixNano()%1000)
	t0 := time.Now()
	desktopCopy(want)
	waitFor(t, "OSC 52", 5*time.Second, func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		return slices.Contains(ft.clips, want)
	})
	t.Logf("desktop copy to OSC 52: %d ms (with the copy command)", time.Since(t0).Milliseconds())
	waitFor(t, "copy notice", 3*time.Second, func() bool { _, _, s, _, _ := ft.snap(); return strings.Contains(s, "Copied") })
	_, _, st, _, _ := ft.snap()
	t.Logf("status: %s", st)

	// (2) A bracketed paste goes to the desktop clipboard, then the paste key.
	paste := "pasted through the clipboard\rsecond line ✓"
	t0 = time.Now()
	fmt.Fprintf(ft.f, "\x1b[200~%s\x1b[201~", paste)
	time.Sleep(200 * time.Millisecond)
	io.WriteString(ft.f, "\r") // cat gets a line at its end
	waitFor(t, "pasted text", 5*time.Second, func() bool {
		return strings.Contains(readTyped(), "pasted through the clipboard\nsecond line ✓")
	})
	t.Logf("paste to the window: %d ms (with a 200 ms pause)", time.Since(t0).Milliseconds())
	req, _ := http.NewRequest("GET", url+"/clipboard", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ Text string }
	json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()
	if c.Text != "pasted through the clipboard\nsecond line ✓" {
		t.Fatalf("desktop clipboard: %q", c.Text)
	}
	// Our own write must not come back as a copy.
	time.Sleep(500 * time.Millisecond)
	ft.mu.Lock()
	echo := slices.Contains(ft.clips, c.Text)
	ft.mu.Unlock()
	if echo {
		t.Fatal("the pasted text came back as OSC 52")
	}

	// (3) Ctrl+C (and Super+C) go to the desktop as keys.
	io.WriteString(ft.f, "\x03")
	wantKeys := "\x03\n"
	if os.Getenv("SRTERM_CLIP_NO_SUPER") == "" {
		io.WriteString(ft.f, "\x1b[99;9u")
		wantKeys = "\x03<SUPER-C>\n"
	}
	io.WriteString(ft.f, "\r")
	waitFor(t, "Ctrl+C as a key", 5*time.Second, func() bool { return strings.Contains(readTyped(), wantKeys) })
	t.Logf("window got %q", readTyped())
}

// TestRawKeys takes control on SRTERM_RAW_URL and writes SRTERM_RAW_SEND (a
// Go-quoted string) to tuios-rd as terminal input, one byte group per 300 ms.
func TestRawKeys(t *testing.T) {
	url, send := os.Getenv("SRTERM_RAW_URL"), os.Getenv("SRTERM_RAW_SEND")
	if url == "" || send == "" {
		t.Skip("set SRTERM_RAW_URL and SRTERM_RAW_SEND")
	}
	bin := buildSrterm(t)
	ft, _ := startFake(t, bin, url, true, true)
	waitFor(t, "frames", 15*time.Second, func() bool { f, _, _, _, _ := ft.snap(); return f >= 1 })
	ft.f.Write([]byte{0x1d})
	waitFor(t, "control", 5*time.Second, func() bool { _, _, s, _, _ := ft.snap(); return strings.Contains(s, "You have control") })
	time.Sleep(300 * time.Millisecond)
	for _, part := range strings.Split(send, "|") {
		s, err := strconv.Unquote("\"" + part + "\"")
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(ft.f, s)
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
}
