package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
	"tuios.dev/shot"
)

// ttyState is the pty's line discipline as stty -g writes it, read from the
// slave side.
func ttyState(t *testing.T, p xpty.Pty) string {
	t.Helper()
	name := p.Name()
	if up, ok := p.(*xpty.UnixPty); ok {
		name = up.SlaveName()
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("open the pty slave: %v", err)
	}
	defer func() { _ = f.Close() }()
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = f
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stty: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

var (
	reMermaidFrame = regexp.MustCompile(`\x1b_Ga=T`)
	// The end of a frame: the cursor restored and synchronized output ended.
	mermaidFrameEnd = "\x1b8\x1b[?2026l"
)

// TestMermaidLiveGivesTheTerminalBack runs tuios mermaid --demo on a
// terminal with kitty graphics and ends it each way it can end. Each time the
// line discipline must be what it was before the program started, and after
// the last frame the program must delete its picture, show the cursor and
// leave the alternate screen. A terminal left in raw mode prints each later
// line one step to the right of the one before.
//
// The stalled case stops reading before it sends SIGTERM, so a frame is stuck
// in a write: termios must come back while the write still blocks, and the
// reset must not land inside that frame.
func TestMermaidLiveGivesTheTerminalBack(t *testing.T) {
	cases := []struct {
		name string
		// tags builds tuios from source with these build tags for the case.
		tags  string
		stall bool
		end   func(t *testing.T, ft *fakeTerm)
		code  int
	}{
		{name: "q", end: func(t *testing.T, ft *fakeTerm) { _, _ = ft.pty.Write([]byte("q")) }},
		{name: "ctrl+c", end: func(t *testing.T, ft *fakeTerm) { _, _ = ft.pty.Write([]byte{3}) }},
		{name: "SIGTERM", end: func(t *testing.T, ft *fakeTerm) { _ = ft.cmd.Process.Signal(syscall.SIGTERM) }},
		{name: "SIGHUP", end: func(t *testing.T, ft *fakeTerm) { _ = ft.cmd.Process.Signal(syscall.SIGHUP) }},
		{name: "panic", tags: "mermaidpanic", end: func(*testing.T, *fakeTerm) {}, code: 2},
		{name: "SIGTERM while stalled", stall: true, end: func(t *testing.T, ft *fakeTerm) { _ = ft.cmd.Process.Signal(syscall.SIGTERM) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			var before string
			opts := fakeTermOpts{bg: "rgb:0000/0000/0000", fg: "rgb:ffff/ffff/ffff", kitty: true,
				onPty: func(p xpty.Pty) { before = ttyState(t, p) }}
			if tc.tags != "" {
				opts.bin = buildTuiosWithTags(t, tc.tags)
			}
			ft := startFakeTerm(t, base, 120, 36, opts, "mermaid", "--demo", "--loop", "--theme", "flexoki")
			if _, ok := ft.waitFor(reMermaidFrame, 20*time.Second); !ok {
				t.Fatalf("no kitty frame; output:\n%q", tailString(ft.output(), 400))
			}
			if tc.stall {
				ft.hold.Store(true)
				time.Sleep(600 * time.Millisecond)
			}
			tc.end(t, ft)
			if tc.stall {
				time.Sleep(1500 * time.Millisecond)
				if got := ttyState(t, ft.pty); got != before {
					t.Errorf("while the terminal is stalled, termios is %s, want %s", got, before)
				}
				ft.hold.Store(false)
			}
			code, ok := ft.exitCode(15 * time.Second)
			if !ok {
				t.Fatal("tuios mermaid did not exit")
			}
			if code != tc.code {
				t.Errorf("exit code %d, want %d", code, tc.code)
			}
			if got := ttyState(t, ft.pty); got != before {
				t.Errorf("termios after exit is %s, want %s as before", got, before)
			}
			out := ft.output()
			after := out
			if i := strings.LastIndex(out, mermaidFrameEnd); i >= 0 {
				after = out[i:]
			}
			for _, want := range []struct{ seq, what string }{
				{"\x1b_Ga=d,d=I", "delete the picture"},
				{"\x1b[?25h", "show the cursor"},
				{"\x1b[?1049l", "leave the alternate screen"},
			} {
				if !strings.Contains(after, want.seq) {
					t.Errorf("after the last frame it does not %s; tail:\n%q", want.what, tailString(after, 300))
				}
			}
		})
	}
}

// buildTuiosWithTags builds tuios from this checkout with the given build
// tags. The panic case needs it: only a mermaidpanic build panics in the frame
// encoder, and a release build has no hook to make it. It ignores
// TUIOS_E2E_BIN, so a negative control of that case edits the source.
func buildTuiosWithTags(t *testing.T, tags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tuios-"+tags)
	build := exec.Command("go", "build", "-tags", tags, "-o", bin, "./cmd/tuios")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tuios with -tags %s: %v\n%s", tags, err, out)
	}
	return bin
}

// TestMermaidLiveWritesNoTerminalControls watches a diagram whose labels,
// source lines and parse error hold terminal controls, with the source pane
// shown, in a kitty terminal and a text one. "#27;" in a label decodes to
// ESC, and the file also holds raw ESC bytes, so a view that wrote either
// as it is would set the window title and write the clipboard. The text of
// each must still be on screen, with U+FFFD in place of the control.
func TestMermaidLiveWritesNoTerminalControls(t *testing.T) {
	src := "flowchart LR\n  A[\"#27;]2;pwned#7;\"] --> B\n  %% \x1b]52;c;aGk=\x07 clip\n  C[\"\x1b]2;title\x07\"] --> D\n"
	broken := "flowchart LR\n  A[\x1b]2;broken\x07 -->\n"
	for _, tc := range []struct {
		name  string
		kitty bool
		src   string
	}{
		{"kitty", true, src},
		{"text", false, src},
		{"parse error", false, broken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			opts := fakeTermOpts{bg: "rgb:0000/0000/0000", fg: "rgb:ffff/ffff/ffff", kitty: tc.kitty}
			path := filepath.Join(t.TempDir(), "hostile.mmd")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			ft := startFakeTerm(t, base, 140, 40, opts, "mermaid", path, "--watch", "--split", "--theme", "flexoki")
			// The source pane colours the source, so a match is on a word of it.
			shown := regexp.MustCompile(`aGk=|broken`)
			if _, ok := ft.waitFor(shown, 20*time.Second); !ok {
				t.Fatalf("the source pane never showed; output:\n%q", tailString(ft.output(), 600))
			}
			time.Sleep(300 * time.Millisecond)
			_, _ = ft.pty.Write([]byte("q"))
			if _, ok := ft.exitCode(15 * time.Second); !ok {
				t.Fatal("tuios mermaid did not exit")
			}
			out := ft.output()
			for _, bad := range []string{"\x1b]2;pwned", "\x1b]2;title", "\x1b]2;broken", "\x1b]52;", "\x07"} {
				if strings.Contains(out, bad) {
					t.Errorf("the view wrote %q to the terminal", bad)
				}
			}
			if !strings.Contains(out, "\uFFFD") {
				t.Errorf("no U+FFFD where the control was; output:\n%q", tailString(out, 600))
			}
		})
	}
}

// The diagrams of a Markdown file, drawn in a tuios pane on a kitty host.
//
// Reported from a tall pane: the first diagram lost its top, the next one was
// drawn over its end, and the right side of a diagram was cut at a straight
// edge. Two causes, written down before the fix:
//
//   - tuios cut a picture it shows in part (scrolled, or at the bottom of the
//     pane) with a whole number of pixels a cell. 761 pixels over 96 columns
//     is 7.93, and the truncated 7 cut the right 89 pixels off and moved each
//     row of the cut a little up the picture.
//   - tuios mermaid gave the picture's width in cells and let the terminal
//     work out the height from its cell size. A pane can report a cell size
//     the host is not drawing (a daemon told by a client with another font).
//     The picture then came out taller than the pane, so its top scrolled
//     away before it was ever whole on screen.
//
// So the host view is rebuilt from what the host was told. The prompt comes
// after the last diagram, so every picture still on screen has its bottom on
// screen: each must show its picture to the right edge and to the bottom row.
// The pictures must stand one under the other with a gap, and the prompt must
// come after the last one. The frame is saved as a PNG with the pictures in it.
//
// How it could pass wrongly: with no picture on screen every check on the
// pictures passes, so the count sent and the last diagram on screen are
// checked first.
//
// Every diagram has its text at the terminal's text size. The small diagram
// must keep its own size and not be made larger to fill the pane. The tall one
// is taller than the pane: it keeps its size, runs into the scrollback, and
// all its rows are reserved. The tall diagram is first drawn alone with --fit,
// which makes it fit the pane, so it must be whole on screen.
//
// Negative controls: see NEGATIVE_CONTROLS.md.
func TestMermaidDiagramsStackInAPane(t *testing.T) {
	cellsize := buildCellsize(t)
	cases := []struct {
		name   string
		daemon bool
		// wrap runs tuios mermaid in a pane that reports these cell pixels.
		wrap string
		// cellW is the cell width the pane reports.
		cellW float64
	}{
		{name: "standalone", cellW: 10},
		{name: "daemon", daemon: true, cellW: 10},
		{name: "other-cell-size", wrap: cellsize + " 8x20 ", cellW: 8},
	}
	dir := t.TempDir()
	md := filepath.Join(dir, "diagrams.md")
	var tall, wide strings.Builder
	tall.WriteString("flowchart TB\n")
	for i := range 24 {
		fmt.Fprintf(&tall, "    T%d[Step %d of the tall one] --> T%d\n", i, i, i+1)
	}
	wide.WriteString("flowchart LR\n")
	for i := range 16 {
		fmt.Fprintf(&wide, "    W%d[Wide %d] --> W%d\n", i, i, i+1)
	}
	doc := "# Diagrams\n\n```mermaid\n" + tall.String() + "```\n\nText.\n\n```mermaid\n" + wide.String() +
		"```\n\n```mermaid\nflowchart LR\n    A[Small] --> B[Done]\n```\n"
	if err := os.WriteFile(md, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	// The tall and the small diagram's own sizes, in diagram units.
	tallW, tallH := svgSize(t, md, 1)
	smallW, _ := svgSize(t, md, 3)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const cols, rows = 120, 60
			host := newKittyHost()
			term, base := start(t, startOpts{
				cols: cols, rows: rows,
				env:           []string{"TUIOS_SIXEL_GRAPHICS=0", "TUIOS_CELL_SIZE=10x20"},
				out:           host,
				daemonDefault: tc.daemon,
			})
			if tc.daemon {
				killDaemon(t, base)
			}
			answerKittyProbe(t, term, host.probe, cols, rows)
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)
			// With --fit the tall diagram fits the pane, so it must be whole
			// on screen. A picture sized for cells the host is not drawing
			// came out taller than the pane and lost its top.
			runToExit(t, term, "clear")
			host.mark("tall")
			runToExit(t, term, tc.wrap+tuiosBin+" mermaid --theme flexoki-dark --fit --diagram 1 "+md)
			alone := settleKittyHost(t, host, "tall", 1)
			if len(alone.order) != 1 {
				t.Fatalf("the host was sent %d pictures for one diagram", len(alone.order))
			}
			if p := alone.live[[2]int{alone.order[0], 1}]; p == nil {
				t.Errorf("the tall diagram is not on screen\n%s", term.Snapshot())
			} else if up := alone.uploads[p.id]; p.sy != 0 || p.sh < up.h-1 || p.sw < up.w-1 {
				t.Errorf("with --fit the tall diagram (%dx%d) is not whole on screen: %+v\n%s", up.w, up.h, *p, term.Snapshot())
			}

			runToExit(t, term, "clear")
			host.mark("mermaid")
			runToExit(t, term, tc.wrap+tuiosBin+" mermaid --theme flexoki-dark "+md)
			// The refresh passes that place the pictures run on the next frames.
			view := settleKittyHost(t, host, "mermaid", 3)
			g := screenGrid(term.Screen(), shot.XTermPalette())
			for _, p := range view.live {
				g.Images = append(g.Images, p.shotImage(view))
			}
			saveGridPNG(t, g, artifactDir(t), "diagrams")

			if len(view.uploads) != 3 {
				t.Fatalf("the host was sent %d pictures, want the 3 diagrams", len(view.uploads))
			}
			// Every picture on screen at the end has its bottom on screen, as
			// the prompt comes after the last one. So each must show its
			// picture down to the last pixel row, and across its whole width.
			for _, p := range view.live {
				up := view.uploads[p.id]
				if p.sx != 0 || p.sw < up.w-1 {
					t.Errorf("picture %d (%dx%d) shows pixels %d to %d across, want all of them: %+v", p.id, up.w, up.h, p.sx, p.sx+p.sw, *p)
				}
				if p.sy+p.sh < up.h-1 {
					t.Errorf("picture %d (%dx%d) shows pixel rows %d to %d, want its bottom row %d: %+v", p.id, up.w, up.h, p.sy, p.sy+p.sh, up.h, *p)
				}
			}
			// The tall diagram keeps the text size of the others: 16px labels
			// at four fifths of a 20 pixel cell is one pixel a diagram unit,
			// so its box is its own width over the cell width the pane
			// reports, and its own height over 20 pixels. It is taller than
			// the pane, so its top is in the scrollback, and every row is
			// reserved: its bottom is on screen and the next diagram starts
			// under it (checked above).
			if p := view.live[[2]int{view.order[0], 1}]; p == nil {
				t.Errorf("the bottom of the tall diagram is not on screen\n%s", term.Snapshot())
			} else {
				up := view.uploads[p.id]
				wantC, wantR := int(math.Ceil(tallW/tc.cellW)), int(math.Ceil(tallH/20))
				if p.c < wantC-1 || p.c > wantC+1 {
					t.Errorf("the tall diagram (%.0f units wide) takes %d columns, want about %d", tallW, p.c, wantC)
				}
				// The rows of the whole box, from the part on screen.
				if p.sh <= 0 {
					t.Errorf("the tall diagram shows no pixel rows: %+v", *p)
				} else if got := int(math.Round(float64(p.r) * float64(up.h) / float64(p.sh))); got < wantR-1 || got > wantR+1 {
					t.Errorf("the tall diagram (%.0f units high) takes %d rows, want about %d: %+v", tallH, got, wantR, *p)
				}
			}
			// The last diagram is small and fits whole, with its text at the
			// terminal's text size: 16px labels at four fifths of a 20 pixel
			// cell is one pixel a diagram unit, so the box is its own width
			// over the cell width the pane reports. It must not be blown up to
			// fill the pane.
			if p := view.live[[2]int{view.order[len(view.order)-1], 1}]; p != nil {
				if p.sy != 0 || p.sh < view.uploads[p.id].h-1 {
					t.Errorf("the small diagram is not whole on screen: %+v", *p)
				}
				want := int(math.Ceil(smallW / tc.cellW))
				if p.c < want-1 || p.c > want+1 {
					t.Errorf("the small diagram (%.0f units wide) takes %d columns, want about %d", smallW, p.c, want)
				}
			}
			// The pictures on screen at the end, top to bottom, stand apart.
			live := slices.SortedFunc(maps.Values(view.live), func(a, b *hostPlacement) int { return a.row - b.row })
			if len(live) == 0 || live[len(live)-1].id != view.order[len(view.order)-1] {
				t.Fatalf("the last diagram is not on screen at the end\n%s", term.Snapshot())
			}
			for i := 1; i < len(live); i++ {
				if gap := live[i].row - (live[i-1].row + live[i-1].r); gap < 1 {
					t.Errorf("picture %d starts %d rows after the end of picture %d, want a gap of at least 1",
						live[i].id, gap, live[i-1].id)
				}
			}
			last := live[len(live)-1]
			done := -1
			for y := range rows {
				if strings.Contains(term.Screen().Line(y), "DONE-RC=0") {
					done = y
				}
			}
			if done < last.row+last.r {
				t.Errorf("the prompt is on row %d, inside the last picture (rows %d to %d)\n%s",
					done, last.row, last.row+last.r-1, term.Snapshot())
			}
		})
	}
}

// svgSize is the size in diagram units of diagram n of the Markdown file md.
func svgSize(t *testing.T, md string, n int) (w, h float64) {
	t.Helper()
	svg, err := exec.Command(tuiosBin, "mermaid", "--diagram", strconv.Itoa(n), "--format", "svg", md).Output()
	if err != nil {
		t.Fatalf("draw diagram %d as SVG: %v", n, err)
	}
	m := regexp.MustCompile(`width="([0-9.]+)" height="([0-9.]+)"`).FindSubmatch(svg)
	if m == nil {
		t.Fatalf("the SVG of diagram %d has no size: %.200s", n, svg)
	}
	w, _ = strconv.ParseFloat(string(m[1]), 64)
	h, _ = strconv.ParseFloat(string(m[2]), 64)
	return w, h
}

var (
	cellsizeOnce sync.Once
	cellsizeBin  string
	cellsizeErr  error
)

// buildCellsize compiles ./cellsize once per test binary.
func buildCellsize(t *testing.T) string {
	t.Helper()
	cellsizeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cellsize")
		if err != nil {
			cellsizeErr = err
			return
		}
		bin := filepath.Join(dir, "cellsize")
		if out, err := exec.Command("go", "build", "-o", bin, "./cellsize").CombinedOutput(); err != nil {
			cellsizeErr = fmt.Errorf("build cellsize: %v\n%s", err, out)
			return
		}
		cellsizeBin = bin
	})
	if cellsizeErr != nil {
		t.Fatal(cellsizeErr)
	}
	return cellsizeBin
}

// hostUpload is one picture the host was sent.
type hostUpload struct {
	png  []byte
	w, h int
}

// hostPlacement is one a=p the host was given: its picture, the cell of its
// top-left corner, its box in cells, and the part of the picture it shows.
type hostPlacement struct {
	id, pid        int
	col, row, c, r int
	sx, sy, sw, sh int
}

func (p *hostPlacement) shotImage(v *kittyHostView) shot.Image {
	up := v.uploads[p.id]
	img, _ := png.Decode(bytes.NewReader(up.png))
	return shot.Image{Pixels: img, Source: image.Rect(p.sx, p.sy, p.sx+p.sw, p.sy+p.sh),
		Col: p.col, Row: p.row, Cols: p.c, Rows: p.r}
}

// kittyHostView is what a kitty host holds after a stream: the pictures sent
// after the phase mark, in order, and the placements still on screen.
type kittyHostView struct {
	uploads map[int]*hostUpload
	order   []int
	live    map[[2]int]*hostPlacement
}

// hostCmdRE is a graphics command with the cursor move tuios puts in front of
// each placement.
var hostCmdRE = regexp.MustCompile(`(?:\x1b\[(\d+);(\d+)H)?\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\`)

// settleKittyHost waits until the host was sent at least want pictures since
// the phase mark and the stream has gone quiet, then replays it. The pictures
// arrive over the next frames after the command exits, and a fixed sleep lost
// the last of them on a busy runner.
func settleKittyHost(t *testing.T, host *kittyHost, phase string, want int) *kittyHostView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	last, quietSince := -1, time.Now()
	for {
		stream := host.bytes()
		view := replayKittyHost(t, stream, phase)
		if len(stream) != last {
			last, quietSince = len(stream), time.Now()
		}
		if len(view.uploads) >= want && time.Since(quietSince) >= 500*time.Millisecond {
			return view
		}
		if time.Now().After(deadline) {
			return view
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// replayKittyHost plays the host's stream from the phase mark the way kitty
// keeps pictures and placements.
func replayKittyHost(t *testing.T, stream []byte, phase string) *kittyHostView {
	t.Helper()
	if i := bytes.Index(stream, []byte(phaseMark+phase)); i >= 0 {
		stream = stream[i:]
	}
	v := &kittyHostView{uploads: map[int]*hostUpload{}, live: map[[2]int]*hostPlacement{}}
	var upID int
	var data []byte
	for _, m := range hostCmdRE.FindAllSubmatch(stream, -1) {
		kv := map[string]int{}
		action := ""
		for f := range strings.SplitSeq(string(m[3]), ",") {
			k, val, _ := strings.Cut(f, "=")
			if k == "a" || k == "d" {
				if k == "a" {
					action = val
				} else {
					kv["d"] = int(val[0])
				}
				continue
			}
			kv[k], _ = strconv.Atoi(val)
		}
		switch {
		case action == "t" || (action == "" && upID != 0):
			if action == "t" {
				upID, data = kv["i"], nil
			}
			data = append(data, m[4]...)
			if kv["m"] == 0 {
				raw, err := base64.StdEncoding.DecodeString(string(data))
				if err != nil {
					t.Fatalf("picture %d: %v", upID, err)
				}
				cfg, err := png.DecodeConfig(bytes.NewReader(raw))
				if err != nil {
					t.Fatalf("picture %d: %v", upID, err)
				}
				if v.uploads[upID] == nil {
					v.order = append(v.order, upID)
				}
				v.uploads[upID] = &hostUpload{png: raw, w: cfg.Width, h: cfg.Height}
				upID = 0
			}
		case action == "p":
			up := v.uploads[kv["i"]]
			if up == nil {
				continue
			}
			row, _ := strconv.Atoi(string(m[1]))
			col, _ := strconv.Atoi(string(m[2]))
			p := &hostPlacement{id: kv["i"], pid: kv["p"], col: col - 1, row: row - 1, c: kv["c"], r: kv["r"],
				sx: kv["x"], sy: kv["y"], sw: kv["w"], sh: kv["h"]}
			if p.sw == 0 {
				p.sw = up.w - p.sx
			}
			if p.sh == 0 {
				p.sh = up.h - p.sy
			}
			v.live[[2]int{p.id, p.pid}] = p
		case action == "d":
			for k := range v.live {
				switch rune(kv["d"]) {
				case 'a', 'A':
					delete(v.live, k)
				case 'i', 'I':
					if k[0] == kv["i"] {
						delete(v.live, k)
					}
				case 'p', 'P':
					if k[0] == kv["i"] && k[1] == kv["p"] {
						delete(v.live, k)
					}
				}
			}
		}
	}
	return v
}
