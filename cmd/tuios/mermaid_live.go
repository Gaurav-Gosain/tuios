//go:build !windows && !js

package main

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/term"
	"tuios.dev/sketch"

	"github.com/Gaurav-Gosain/tuios/internal/mermaid"
)

// liveScreen is the full-screen view of tuios mermaid --watch and --demo: the
// diagram, the source beside it when split, and a status line.
type liveScreen struct {
	out   *os.File
	th    *sketch.Theme
	split bool
	name  string
	live  *mermaid.Live
	kitty bool

	mu     sync.Mutex
	cols   int
	rows   int
	cellW  int
	cellH  int
	src    string
	caret  int // -1: no caret
	status string

	statsMu sync.Mutex
	stats   []mermaid.FrameStat
}

func hexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func sgr(fg bool, c color.Color) string {
	r, g, b, _ := c.RGBA()
	k := 48
	if fg {
		k = 38
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", k, r>>8, g>>8, b>>8)
}

// layout splits the screen: the source on the left when the screen is wide,
// above when it is tall, and the status line at the bottom.
func (s *liveScreen) layout() (src, diagram mermaid.Area) {
	cols, rows := s.cols, s.rows-1
	if !s.split {
		return mermaid.Area{}, mermaid.Area{Top: 1, Left: 1, Cols: cols, Rows: rows, CellW: s.cellW, CellH: s.cellH}
	}
	if cols*s.cellW >= rows*s.cellH*4/3 {
		w := min(max(cols*36/100, 28), 64)
		return mermaid.Area{Top: 2, Left: 3, Cols: w - 3, Rows: rows - 2},
			mermaid.Area{Top: 1, Left: w + 2, Cols: cols - w - 2, Rows: rows, CellW: s.cellW, CellH: s.cellH}
	}
	h := min(max(rows*30/100, 6), 16)
	return mermaid.Area{Top: 2, Left: 3, Cols: cols - 4, Rows: h - 1},
		mermaid.Area{Top: h + 2, Left: 1, Cols: cols, Rows: rows - h - 1, CellW: s.cellW, CellH: s.cellH}
}

// paint draws the background, the source and the status line.
func (s *liveScreen) paint(full bool) {
	s.mu.Lock()
	src, caret, status := s.src, s.caret, s.status
	s.mu.Unlock()
	sa, _ := s.layout()
	bg := sgr(false, s.th.Bg)
	var b strings.Builder
	b.WriteString("\x1b[?2026h\x1b[?25l")
	if full {
		for r := 1; r <= s.rows; r++ {
			fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[0m%s%s", r, bg, bg, strings.Repeat(" ", s.cols))
		}
	}
	cr, cc := -1, -1
	if s.split {
		lines := strings.Split(src, "\n")
		line, col := caretPos(src, caret)
		first := 0
		if caret >= 0 && line >= sa.Rows {
			first = line - sa.Rows + 1
		} else if caret < 0 && len(lines) > sa.Rows {
			first = len(lines) - sa.Rows
		}
		for r := 0; r < sa.Rows; r++ {
			fmt.Fprintf(&b, "\x1b[%d;%dH%s%s", sa.Top+r, sa.Left, bg, strings.Repeat(" ", sa.Cols))
			i := first + r
			if i < len(lines) {
				fmt.Fprintf(&b, "\x1b[%d;%dH%s%s", sa.Top+r, sa.Left, bg, highlight(clip(paneText(lines[i]), sa.Cols), s.th))
			}
		}
		if caret >= 0 {
			cr, cc = sa.Top+line-first, sa.Left+min(col, sa.Cols-1)
		}
	}
	text := s.th.Muted
	if strings.HasPrefix(status, "!") {
		text, status = s.th.Critical, status[1:]
	}
	// The status quotes file names and parse errors, which quote the source.
	status = paneText(status)
	fmt.Fprintf(&b, "\x1b[%d;1H%s%s %s", s.rows, bg, sgr(true, text), clip(status, s.cols-2))
	fmt.Fprintf(&b, "\x1b[K%s%s", bg, strings.Repeat(" ", max(0, s.cols-2-len([]rune(status)))))
	if cr > 0 {
		fmt.Fprintf(&b, "\x1b[0m\x1b[%d;%dH\x1b[?25h", cr, cc)
	}
	b.WriteString("\x1b[?2026l")
	_, _ = s.live.Write([]byte(b.String()))
}

// paneText is a line of the file as the view may write it: nothing a terminal
// would act on (mermaid.TermSafe), and tabs as spaces, which keep to the pane.
func paneText(s string) string {
	return strings.ReplaceAll(mermaid.TermSafe(s), "\t", "    ")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:max(0, n)])
	}
	return s
}

// caretPos is the line and the column, in characters, of a byte offset.
func caretPos(src string, caret int) (int, int) {
	if caret < 0 {
		return 0, 0
	}
	caret = min(caret, len(src))
	line := strings.Count(src[:caret], "\n")
	start := strings.LastIndexByte(src[:caret], '\n') + 1
	return line, len([]rune(src[start:caret]))
}

var mermaidKeywords = []string{"flowchart", "graph", "subgraph", "end", "classDef", "class", "style", "linkStyle",
	"sequenceDiagram", "participant", "actor", "note", "Note", "alt", "else", "opt", "loop", "par", "and", "rect",
	"activate", "deactivate", "autonumber", "stateDiagram", "stateDiagram-v2", "state", "erDiagram", "classDiagram",
	"pie", "mindmap", "gantt", "title", "section", "direction", "LR", "RL", "TD", "TB", "BT", "over", "right", "left", "of"}

// highlight colours one line of Mermaid as pele's video does: keywords in the
// accent, strings and labels in green, numbers in yellow, punctuation muted.
func highlight(line string, th *sketch.Theme) string {
	fg, muted := sgr(true, th.Fg), sgr(true, th.Muted)
	key, str, num := sgr(true, th.Accent), sgr(true, th.Series[3]), sgr(true, th.Series[2])
	var b strings.Builder
	rs := []rune(line)
	label := false
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '%' && i+1 < len(rs) && rs[i+1] == '%':
			b.WriteString(muted + string(rs[i:]))
			i = len(rs)
		case c == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			j = min(j+1, len(rs))
			b.WriteString(str + string(rs[i:j]))
			i = j
		case c == '|':
			label = !label
			b.WriteString(muted + "|")
			i++
		case label:
			b.WriteString(str + string(c))
			i++
		case c >= '0' && c <= '9':
			b.WriteString(num + string(c))
			i++
		case c == '_' || c == '-' && i+1 < len(rs) && (rs[i+1] >= 'a' && rs[i+1] <= 'z') || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(rs) && (rs[j] == '_' || rs[j] == '-' && j+1 < len(rs) && rs[j+1] != '-' && rs[j+1] != '>' && rs[j+1] != '.' || rs[j] >= 'a' && rs[j] <= 'z' || rs[j] >= 'A' && rs[j] <= 'Z' || rs[j] >= '0' && rs[j] <= '9') {
				j++
			}
			j = max(j, i+1)
			word := string(rs[i:j])
			if slices.Contains(mermaidKeywords, word) {
				b.WriteString(key + word)
			} else {
				b.WriteString(fg + word)
			}
			i = j
		case strings.ContainsRune("-.=<>[](){}:;,&|", c):
			b.WriteString(muted + string(c))
			i++
		default:
			b.WriteString(fg + string(c))
			i++
		}
	}
	return b.String()
}

func (s *liveScreen) setStatus(st string) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

// show draws a new source: the diagram moves to it, the source and the status
// line follow.
func (s *liveScreen) show(src string, caret int, typed bool) { s.showTyped(src, caret, src, typed) }

// showTyped shows src in the editor and draws draw, which a typing demo
// completes ahead of the caret.
func (s *liveScreen) showTyped(src string, caret int, draw string, typed bool) {
	s.mu.Lock()
	s.src, s.caret = src, caret
	s.mu.Unlock()
	s.live.Show(draw)
	st := s.name
	if err := s.live.Err(); err != nil && !typed {
		msg := strings.SplitN(err.Error(), "\n", 2)[0]
		st = "!" + s.name + "  " + msg
	} else if s.kitty {
		st += "  live"
	} else {
		st += "  live, text"
	}
	s.setStatus(st)
	s.paint(false)
}

func (s *liveScreen) resize() {
	cols, rows, cellW, cellH := terminalCells(s.out)
	s.mu.Lock()
	s.cols, s.rows, s.cellW, s.cellH = cols, rows, cellW, cellH
	s.mu.Unlock()
	_, da := s.layout()
	s.paint(true)
	s.live.Resize(da)
}

// kittyFileMedium asks whether the terminal reads pictures from temporary
// files (t=t), which saves compressing every frame.
func kittyFileMedium(out *os.File) bool {
	in := os.Stdin
	f, err := os.CreateTemp("", "tty-graphics-protocol-sketch-probe-*.rgb")
	if err != nil {
		return false
	}
	_, _ = f.Write([]byte{0, 0, 0})
	_ = f.Close()
	defer func() { _ = os.Remove(f.Name()) }()
	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return false
	}
	defer guardRaw(int(in.Fd()), state)()
	q := "\x1b_Gi=32,s=1,v=1,a=q,t=t,f=24;" + base64.StdEncoding.EncodeToString([]byte(f.Name())) + "\x1b\\\x1b[c"
	if _, err := out.WriteString(q); err != nil {
		return false
	}
	answer, err := readAnswer(in, 500*time.Millisecond)
	return err == nil && strings.Contains(answer, "_Gi=32;OK")
}

// runMermaidLive runs the full-screen view until q, Esc or Ctrl+C.
func runMermaidLive(name string, o mermaidOptions, lo liveOptions, th *sketch.Theme, stdout *os.File) error {
	if !term.IsTerminal(int(stdout.Fd())) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("--watch and --demo need a terminal")
	}
	// The terminal is given back on every way out: q, Ctrl+C, a signal, a
	// panic in any goroutine, an error. termios is restored first, because it
	// is one ioctl and cannot block. The picture and the screen are reset
	// after it, within a time limit, as a terminal that has stopped reading
	// would block those writes.
	fd := int(os.Stdin.Fd())
	orig, err := term.GetState(fd)
	if err != nil {
		return err
	}
	var s *liveScreen
	var cleanOnce sync.Once
	cleanup := func() {
		cleanOnce.Do(func() {
			_ = term.Restore(fd, orig)
			done := make(chan struct{})
			go func() {
				const reset = "\x1b[0m\x1b[2J\x1b[0 q\x1b]112\x1b\\\x1b[?25h\x1b[?1049l"
				if s != nil && s.live != nil {
					// Through the view, so the reset does not land inside a
					// frame that a stalled terminal left half written.
					// The delete goes again with the reset, for a terminal that
					// was too slow for the one Close sends.
					s.live.Close(600 * time.Millisecond)
					s.live.WriteWithin([]byte(s.live.DeleteImage()+reset), 1200*time.Millisecond)
				} else {
					_, _ = stdout.WriteString(reset)
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		})
	}
	crash := func(v any, stack []byte) {
		cleanup()
		fmt.Fprintf(os.Stderr, "tuios mermaid: panic: %v\n%s", v, stack)
		os.Exit(2)
	}
	defer func() {
		if v := recover(); v != nil {
			crash(v, debug.Stack())
		}
	}()
	// safe runs f on another goroutine's behalf: a panic there ends the
	// process the same way, with the terminal given back.
	safe := func(f func()) func() {
		return func() {
			defer func() {
				if v := recover(); v != nil {
					crash(v, debug.Stack())
				}
			}()
			f()
		}
	}
	quit := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(quit) }) }
	// The first signal quits as q does. A second one, or one while the quit
	// is stuck, ends the process at once.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)
	go func() {
		n := 0
		for range sigs {
			if n++; n == 1 {
				stop()
				continue
			}
			cleanup()
			os.Exit(130)
		}
	}()
	kitty := o.format != "text" && kittyGraphics(stdout)
	file := false
	if kitty {
		switch lo.medium {
		case "file":
			file = true
		case "auto", "":
			file = kittyFileMedium(stdout)
		}
	}
	s = &liveScreen{out: stdout, th: th, split: lo.split || lo.demo, kitty: kitty, caret: -1}
	s.name = filepath.Base(name)
	if lo.demo && name == "" {
		s.name = "demo.mmd"
	}
	cols, rows, cellW, cellH := terminalCells(stdout)
	s.cols, s.rows, s.cellW, s.cellH = cols, rows, cellW, cellH
	_, da := s.layout()
	motion := lo.motion
	if motion == 0 {
		motion = 260 * time.Millisecond
	}
	frames := 0
	s.live = mermaid.NewLive(da, mermaid.LiveOptions{
		Out: stdout, Theme: th, Kitty: kitty, FileMedium: file, Motion: motion, Color: true,
		OnPanic: crash,
		OnFrame: func(f mermaid.FrameStat) {
			// framePanicAt is zero in a release build, so the compiler drops
			// this branch. See mermaid_panic.go.
			if frames++; framePanicAt > 0 && frames == framePanicAt {
				panic("mermaidpanic build: frame " + strconv.Itoa(frames))
			}
			if lo.stats {
				s.statsMu.Lock()
				s.stats = append(s.stats, f)
				s.statsMu.Unlock()
			}
		},
	})

	if _, err := term.MakeRaw(fd); err != nil {
		return err
	}
	defer cleanup()
	// The cursor is the caret: a bar in the theme's orange, as in pele's video.
	_, _ = stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[6 q\x1b]12;" + hexOf(th.Series[1]) + "\x1b\\")
	s.paint(true)

	keys := make(chan byte, 16)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				stop()
				return
			}
			// A read that starts with ESC and goes on is a terminal reply or
			// a special key, not the Esc key.
			if n > 1 && buf[0] == 0x1b {
				continue
			}
			for _, c := range buf[:n] {
				select {
				case keys <- c:
				default:
				}
			}
		}
	}()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	var demoStart func()
	var runErr error
	switch {
	case lo.demo:
		steps := mermaid.DemoSteps
		if name != "" {
			data, err := mermaid.ReadSource(name)
			if err != nil {
				cleanup()
				return fmt.Errorf("cannot read %s: %w", name, err)
			}
			if steps = mermaid.Sources(name, data); len(steps) == 0 {
				cleanup()
				return fmt.Errorf("%s has no Mermaid diagram", name)
			}
			if !strings.HasSuffix(strings.ToLower(name), ".md") {
				// One diagram: type it from nothing, a line at a time.
				steps = linesOf(steps[0])
			}
			// Each keystroke holds the text typed so far, so the memory grows
			// with the square of a step's length.
			for _, st := range steps {
				if len(st) > mermaid.MaxDemoStep {
					cleanup()
					return fmt.Errorf("%s: --demo types at most %d characters a diagram", name, mermaid.MaxDemoStep)
				}
			}
		}
		strokes := mermaid.Type(steps)
		var timer *time.Timer
		var tmu sync.Mutex
		var at func(i int)
		at = func(i int) {
			select {
			case <-quit:
				return
			default:
			}
			if i >= len(strokes) {
				if lo.loop {
					tmu.Lock()
					timer = time.AfterFunc(2*time.Second, safe(func() { at(0) }))
					tmu.Unlock()
				}
				return
			}
			k := strokes[i]
			s.showTyped(mermaid.Balanced(k.Text), k.Caret, mermaid.Preview(k), !k.Commit)
			tmu.Lock()
			timer = time.AfterFunc(k.Hold, safe(func() { at(i + 1) }))
			tmu.Unlock()
		}
		demoStart = func() {
			tmu.Lock()
			if timer != nil {
				timer.Stop()
			}
			tmu.Unlock()
			go safe(func() { at(0) })()
		}
		demoStart()
	default:
		load := func() {
			data, err := mermaid.ReadSource(name)
			if err != nil {
				s.setStatus("!" + s.name + "  " + err.Error())
				s.paint(false)
				return
			}
			srcs := mermaid.Sources(name, data)
			n := max(o.diagram, 1)
			if n > len(srcs) {
				s.setStatus(fmt.Sprintf("!%s  has no diagram %d", s.name, n))
				s.paint(false)
				return
			}
			s.show(srcs[n-1], -1, false)
		}
		load()
		w, err := fsnotify.NewWatcher()
		if err != nil {
			cleanup()
			return err
		}
		defer func() { _ = w.Close() }()
		abs, _ := filepath.Abs(name)
		if err := w.Add(filepath.Dir(abs)); err != nil {
			cleanup()
			return err
		}
		// Editors save in bursts (write, rename, chmod): one load after the
		// burst. The timer exists only while a burst is in flight. Loads run
		// one at a time on one goroutine, and a save during a load asks for
		// one more, so the newest file is always the last one shown.
		reload := make(chan struct{}, 1)
		go safe(func() {
			for {
				select {
				case <-reload:
					load()
				case <-quit:
					return
				}
			}
		})()
		ask := func() {
			select {
			case reload <- struct{}{}:
			default:
			}
		}
		var debounce *time.Timer
		go safe(func() {
			for {
				select {
				case ev, ok := <-w.Events:
					if !ok {
						return
					}
					if filepath.Clean(ev.Name) != abs || ev.Op == fsnotify.Chmod {
						continue
					}
					if debounce != nil {
						debounce.Stop()
					}
					debounce = time.AfterFunc(30*time.Millisecond, ask)
				case _, ok := <-w.Errors:
					if !ok {
						return
					}
				case <-quit:
					return
				}
			}
		})()
	}

loop:
	for {
		select {
		case <-quit:
			break loop
		case <-winch:
			s.resize()
		case k := <-keys:
			switch k {
			case 'q', 3, 0x1b:
				break loop
			case 'r':
				if demoStart != nil {
					demoStart()
				}
			case 12: // Ctrl+L
				s.resize()
			}
		}
	}
	stop()
	cleanup()
	if lo.stats {
		printFrameStats(os.Stderr, s.stats)
	}
	return runErr
}

// linesOf is a source as steps: the first line, then each next line added.
func linesOf(src string) []string {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	var steps []string
	for i := range lines {
		steps = append(steps, strings.Join(lines[:i+1], "\n"))
	}
	return steps
}

func printFrameStats(w io.Writer, stats []mermaid.FrameStat) {
	if len(stats) == 0 {
		fmt.Fprintln(w, "no frames")
		return
	}
	var draw, enc []time.Duration
	finals := 0
	var bytes int
	dens := map[float64]int{}
	for _, f := range stats {
		if f.Final {
			finals++
			continue
		}
		draw, enc = append(draw, f.Draw), append(enc, f.Encode)
		bytes += f.Bytes
		dens[float64(int(f.Density*100))/100]++
	}
	q := func(d []time.Duration, p float64) time.Duration {
		if len(d) == 0 {
			return 0
		}
		s := slices.Clone(d)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return s[min(len(s)-1, int(float64(len(s))*p))]
	}
	fmt.Fprintf(w, "frames: %d in motion, %d final\n", len(draw), finals)
	fmt.Fprintf(w, "draw   median %v  p95 %v\n", q(draw, 0.5).Round(10*time.Microsecond), q(draw, 0.95).Round(10*time.Microsecond))
	fmt.Fprintf(w, "encode median %v  p95 %v\n", q(enc, 0.5).Round(10*time.Microsecond), q(enc, 0.95).Round(10*time.Microsecond))
	if len(draw) > 0 {
		fmt.Fprintf(w, "bytes per motion frame: %d\n", bytes/len(draw))
	}
	fmt.Fprintf(w, "density of motion frames: %v\n", dens)
	// Gaps between motion frames: the frame rate while something moves.
	var gaps []time.Duration
	for i := 1; i < len(stats); i++ {
		if stats[i].Final || stats[i-1].Final {
			continue
		}
		if g := stats[i].Sent.Sub(stats[i-1].Sent); g < 100*time.Millisecond {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) > 0 {
		m := q(gaps, 0.5)
		fmt.Fprintf(w, "frame gap median %v (%.0f fps)  p95 %v\n", m.Round(10*time.Microsecond), float64(time.Second)/float64(m), q(gaps, 0.95).Round(10*time.Microsecond))
	}
}
