package mermaid

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tuios.dev/sketch"
	"tuios.dev/sketch/raster"
	sketchtext "tuios.dev/sketch/text"
)

// Area is a box of cells on the screen, from 1, and the pixel size of a cell.
type Area struct {
	Top, Left, Cols, Rows int
	CellW, CellH          int
}

// LiveOptions configure a Live view.
type LiveOptions struct {
	Out   io.Writer
	Theme *sketch.Theme
	// Kitty draws pictures. Without it the view draws box-drawing text and
	// changes without motion.
	Kitty bool
	// FileMedium sends frames through temporary files (kitty t=t), which skips
	// the compression and base64 of every frame. The terminal must read files.
	FileMedium bool
	// Motion is the length of a motion. 0 means 260 ms; below 0 turns motion off.
	Motion time.Duration
	// MaxScale is the most pixels per SVG unit at full density. 0 means 2.
	MaxScale float64
	// Color writes the text drawing in colour.
	Color bool
	// OnFrame, when set, hears about each frame sent.
	OnFrame func(FrameStat)
	// OnPanic, when set, is called with a panic in one of the view's own
	// goroutines, so that the caller can give the terminal back before the
	// process ends. It should not return. Without it the panic goes on.
	OnPanic func(v any, stack []byte)
}

// FrameStat is what one frame cost.
type FrameStat struct {
	Draw, Encode time.Duration
	Density      float64
	Final        bool
	Bytes        int
	// Sent is when the frame was written to the terminal.
	Sent time.Time
}

// Live shows one diagram and moves it smoothly to each new version of it.
// Between motions it holds no timer and does no work.
type Live struct {
	opt   LiveOptions
	id    uint32
	mu    sync.Mutex
	area  Area
	scene *raster.Scene
	diag  *sketch.Diagram
	// tickets numbers the calls of Show, and shown is the newest one that
	// reached the screen.
	tickets atomic.Uint64
	shown   uint64
	// lastSVG is the drawing on screen, so an unchanged one is not sent again.
	lastSVG string
	mo      *raster.Motion
	start   time.Time
	// running says the frame loop runs.
	running bool
	density float64
	err     error
	// out serialises writes to the terminal, between the encoder and the caller.
	out     sync.Mutex
	encode  chan frame
	free    chan *image.RGBA
	stopped chan struct{}
	// done is closed by Close. Nothing waits on the encoder after that.
	done      chan struct{}
	closeOnce sync.Once
}

type frame struct {
	img   *image.RGBA
	stat  FrameStat
	area  Area
	final bool
}

// NewLive starts a view in area.
func NewLive(area Area, opt LiveOptions) *Live {
	if opt.Motion == 0 {
		opt.Motion = 260 * time.Millisecond
	}
	if opt.MaxScale <= 0 {
		opt.MaxScale = 2
	}
	l := &Live{opt: opt, area: area, density: 1, id: uint32(0x5e700000 + os.Getpid()%0xffff),
		encode: make(chan frame, 1), free: make(chan *image.RGBA, 2), stopped: make(chan struct{}), done: make(chan struct{})}
	if opt.Kitty {
		go l.encoder()
	}
	return l
}

// Err is the last source that did not parse, or nil.
func (l *Live) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Show draws src. A source that does not parse keeps the last diagram on
// screen and is reported by Err.
func (l *Live) Show(src string) {
	// Two calls can overlap: a slow parse and a newer save, or a demo that
	// restarts. Each takes a ticket first, and one that finishes after a newer
	// one has been shown is dropped, so the screen never ends on an older
	// source.
	ticket := l.tickets.Add(1)
	d, err := sketch.Parse(src, sketch.ParseOptions{})
	var out sketch.SVG
	if err == nil {
		out, err = d.RenderSVG(sketch.RenderOptions{Theme: l.opt.Theme})
	}
	var sc *raster.Scene
	if err == nil && l.opt.Kitty {
		sc, err = raster.ParseScene(out.Data)
	}
	l.mu.Lock()
	if ticket < l.shown {
		l.mu.Unlock()
		return
	}
	l.shown = ticket
	l.err = err
	if err != nil {
		l.mu.Unlock()
		return
	}
	if out.Data == l.lastSVG {
		// The same drawing: nothing to move or send.
		l.mu.Unlock()
		return
	}
	l.lastSVG = out.Data
	l.diag = d
	if !l.opt.Kitty {
		l.mu.Unlock()
		l.drawText()
		return
	}
	now := time.Now()
	at := 1.0
	if l.mo != nil && l.opt.Motion > 0 {
		at = math.Min(1, float64(now.Sub(l.start))/float64(l.opt.Motion))
	}
	if l.opt.Motion < 0 {
		l.mo = raster.NewMotion(nil, 1, sc)
	} else {
		l.mo = raster.NewMotion(l.mo, at, sc)
	}
	l.scene, l.start = sc, now
	still := l.mo.Still()
	start := !l.running
	if start {
		l.running = true
	}
	l.mu.Unlock()
	if start {
		if still {
			// Nothing moves: one frame, and no loop.
			go func() {
				defer l.guard()
				l.sendFinal()
				l.mu.Lock()
				l.running = false
				l.mu.Unlock()
			}()
		} else {
			go l.loop()
		}
	}
}

// Resize moves the view to a new area and draws it again.
func (l *Live) Resize(area Area) {
	l.mu.Lock()
	l.area = area
	scene := l.scene
	l.mu.Unlock()
	if !l.opt.Kitty {
		l.drawText()
		return
	}
	if scene != nil {
		l.mu.Lock()
		running := l.running
		l.mu.Unlock()
		if !running {
			l.sendFinal()
		}
	}
}

// Redraw draws the diagram again where it is, as after the screen was cleared.
func (l *Live) Redraw() { l.Resize(l.Area()) }

// Area is where the view draws.
func (l *Live) Area() Area {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.area
}

// Close stops the view and removes the picture. It returns within wait even
// when the terminal has stopped reading and a frame is stuck in a write.
func (l *Live) Close(wait time.Duration) {
	l.closeOnce.Do(func() { close(l.done) })
	if !l.opt.Kitty {
		return
	}
	deadline := time.Now().Add(wait)
	select {
	case <-l.stopped:
	case <-time.After(wait / 2):
	}
	l.WriteWithin([]byte(l.DeleteImage()), time.Until(deadline))
}

// DeleteImage is the sequence that removes the view's picture, or "" without
// kitty graphics.
func (l *Live) DeleteImage() string {
	if !l.opt.Kitty {
		return ""
	}
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", l.id)
}

// WriteWithin writes to the terminal between frames, waiting at most wait
// for a frame that is being written to finish. It reports whether it wrote.
// Bytes written in the middle of a frame would become part of the picture.
func (l *Live) WriteWithin(p []byte, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for !l.out.TryLock() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	defer l.out.Unlock()
	_, err := l.opt.Out.Write(p)
	return err == nil
}

// guard hands a panic in one of the view's goroutines to OnPanic.
func (l *Live) guard() {
	if v := recover(); v != nil {
		if l.opt.OnPanic == nil {
			panic(v)
		}
		l.opt.OnPanic(v, debug.Stack())
	}
}

// loop draws frames at up to 60 a second while a motion runs, then the last
// frame at full density, and ends.
func (l *Live) loop() {
	defer l.guard()
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	for {
		l.mu.Lock()
		p := float64(time.Since(l.start)) / float64(l.opt.Motion)
		mo, area, density := l.mo, l.area, l.density
		l.mu.Unlock()
		if p >= 1 {
			l.sendFinal()
			l.mu.Lock()
			// A Show that came while the last frame was drawn starts a new motion.
			again := float64(time.Since(l.start))/float64(l.opt.Motion) < 1
			if !again {
				l.running = false
			}
			l.mu.Unlock()
			if !again {
				return
			}
			continue
		}
		select {
		case img := <-l.free:
			l.drawFrame(mo, img, area, p, density, false)
		default:
			// The encoder still holds both buffers: this frame is dropped.
		}
		select {
		case <-tick.C:
		case <-l.done:
			return
		}
	}
}

func (l *Live) sendFinal() {
	l.mu.Lock()
	mo, area := l.mo, l.area
	l.mu.Unlock()
	if mo == nil {
		return
	}
	var img *image.RGBA
	select {
	case img = <-l.free:
	default:
		img = nil
	}
	l.drawFrame(mo, img, area, 1, 1, true)
}

// MaxFramePixels bounds the picture of one frame. The cell size comes from
// the terminal, and one that reports 65535 pixels a cell would otherwise ask
// for gigabytes a frame. A larger canvas is scaled down to fit, aspect kept.
const MaxFramePixels = 16_000_000

// canvas is the pixel size of the picture for an area at a density.
func canvas(a Area, density float64) (int, int) {
	w := float64(a.Cols) * float64(a.CellW) * density
	h := float64(a.Rows) * float64(a.CellH) * density
	if w*h > MaxFramePixels {
		k := math.Sqrt(MaxFramePixels / (w * h))
		w, h = w*k, h*k
	}
	// A side under a pixel rounds up to one, so the other side is held to
	// the bound on its own too.
	w, h = math.Max(1, w), math.Max(1, h)
	w = math.Min(w, MaxFramePixels/h)
	h = math.Min(h, MaxFramePixels/w)
	return max(1, int(w)), max(1, int(h))
}

func (l *Live) drawFrame(mo *raster.Motion, img *image.RGBA, area Area, p, density float64, final bool) {
	w, h := canvas(area, density)
	if img == nil || img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		img = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	t := time.Now()
	_ = mo.Draw(img, p, raster.FrameOptions{Theme: l.opt.Theme, MaxScale: l.opt.MaxScale * density, Readable: true})
	stat := FrameStat{Draw: time.Since(t), Density: density, Final: final}
	select {
	case l.encode <- frame{img: img, stat: stat, area: area, final: final}:
	case <-l.done:
	}
}

// encoder turns frames into kitty graphics and writes them. Each frame
// replaces the last: the same image id and placement id, so the terminal
// swaps the picture in place.
func (l *Live) encoder() {
	defer close(l.stopped)
	defer l.guard()
	l.free <- nil
	l.free <- nil
	var rgb []byte
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
	for {
		var f frame
		select {
		case f = <-l.encode:
		case <-l.done:
			return
		}
		t := time.Now()
		b := f.img.Bounds()
		n := b.Dx() * b.Dy() * 3
		if cap(rgb) < n {
			rgb = make([]byte, n)
		}
		rgb = rgb[:n]
		pix := f.img.Pix
		for i, j := 0, 0; j < n; i, j = i+4, j+3 {
			rgb[j], rgb[j+1], rgb[j+2] = pix[i], pix[i+1], pix[i+2]
		}
		var seq []byte
		head := fmt.Sprintf("a=T,i=%d,p=1,q=2,C=1,f=24,s=%d,v=%d,c=%d,r=%d", l.id, b.Dx(), b.Dy(), f.area.Cols, f.area.Rows)
		if l.opt.FileMedium {
			seq = l.fileFrame(head, rgb)
		}
		if seq == nil {
			z.Reset()
			zw.Reset(&z)
			_, _ = zw.Write(rgb)
			_ = zw.Close()
			seq = directFrame(head+",o=z", z.Bytes())
		}
		f.stat.Encode = time.Since(t)
		f.stat.Bytes = len(seq)
		l.out.Lock()
		var w bytes.Buffer
		// Synchronized output keeps the terminal from showing half a frame.
		w.WriteString("\x1b[?2026h\x1b7")
		fmt.Fprintf(&w, "\x1b[%d;%dH", f.area.Top, f.area.Left)
		w.Write(seq)
		w.WriteString("\x1b8\x1b[?2026l")
		_, _ = l.opt.Out.Write(w.Bytes())
		l.out.Unlock()
		f.stat.Sent = time.Now()
		if l.opt.OnFrame != nil {
			l.opt.OnFrame(f.stat)
		}
		l.adapt(f.stat)
		select {
		case l.free <- f.img:
		default:
		}
	}
}

// adapt lowers the density of motion frames when a frame costs more than the
// 16.7 ms a 60 Hz frame has, and raises it again when there is room. The
// final frame of a motion is always at full density.
func (l *Live) adapt(s FrameStat) {
	if s.Final {
		return
	}
	cost := max(s.Draw, s.Encode)
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case cost > 14*time.Millisecond:
		l.density = math.Max(0.35, l.density*0.8)
	case cost < 7*time.Millisecond && l.density < 1:
		l.density = math.Min(1, l.density*1.1)
	}
}

func directFrame(head string, data []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(data)
	var b bytes.Buffer
	const chunk = 4096
	for i := 0; i < len(enc) || i == 0; i += chunk {
		end := min(i+chunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		b.WriteString("\x1b_G")
		if i == 0 {
			b.WriteString(head + ",")
		}
		b.WriteString("m=" + strconv.Itoa(more) + ";" + enc[i:end] + "\x1b\\")
		if end >= len(enc) {
			break
		}
	}
	return b.Bytes()
}

// fileFrame writes the pixels to a temporary file the terminal reads and
// deletes (t=t). The name holds tty-graphics-protocol, as kitty requires.
func (l *Live) fileFrame(head string, rgb []byte) []byte {
	f, err := os.CreateTemp("", "tty-graphics-protocol-sketch-*.rgb")
	if err != nil {
		return nil
	}
	_, werr := f.Write(rgb)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return nil
	}
	name := base64.StdEncoding.EncodeToString([]byte(f.Name()))
	return []byte("\x1b_G" + head + ",t=t;" + name + "\x1b\\")
}

// drawText draws the diagram with box-drawing characters in the area.
func (l *Live) drawText() {
	l.mu.Lock()
	d, a, err := l.diag, l.area, l.err
	l.mu.Unlock()
	var lines []string
	if d != nil {
		var derr error
		lines, derr = sketchtext.Draw(d, sketchtext.Options{Cols: a.Cols, Color: l.opt.Color})
		switch {
		case errors.Is(derr, sketchtext.ErrTooWide):
			lines = []string{"", fmt.Sprintf("  The diagram is wider than %d columns. Make the terminal wider.", a.Cols)}
		case errors.Is(derr, sketchtext.ErrUnsupported):
			lines = []string{"", "  This diagram type has no text drawing. Use a terminal with kitty graphics."}
		case derr != nil:
			lines = []string{"", "  " + TermSafe(derr.Error())}
		}
	}
	_ = err
	var b strings.Builder
	b.WriteString("\x1b[?2026h\x1b7")
	for r := 0; r < a.Rows; r++ {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[0m%s", a.Top+r, a.Left, strings.Repeat(" ", a.Cols))
		if r < len(lines) {
			fmt.Fprintf(&b, "\x1b[%d;%dH%s\x1b[0m", a.Top+r, a.Left, lines[r])
		}
	}
	b.WriteString("\x1b8\x1b[?2026l")
	l.out.Lock()
	_, _ = io.WriteString(l.opt.Out, b.String())
	l.out.Unlock()
}

// Write writes to the terminal between frames, so text and pictures do not
// interleave inside an escape sequence.
func (l *Live) Write(p []byte) (int, error) {
	l.out.Lock()
	defer l.out.Unlock()
	return l.opt.Out.Write(p)
}

// TempDir is where FileMedium writes, for a terminal that checks the place.
func TempDir() string { return filepath.Clean(os.TempDir()) }
