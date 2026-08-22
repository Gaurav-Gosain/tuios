package tuie2e

// Style fidelity, asserted differentially against a bare pseudo-terminal.
//
// TestLsStyleStreamHasNoBackgroundBleed pins one property of one screen: a
// foreground-only listing must not arrive with a background. That caught the
// pink filename blocks, and it is blind to the other half of the failure the
// same cache produces, which is a cell served *another cell's* foreground.
//
// This test states the general property instead. The same bytes are replayed
// into a bare shell and into a tuios pane of the same size, and every cell must
// agree on content, foreground and background. tuios is then held to what a
// terminal that does nothing but apply the stream shows, so any colour it
// invents, drops or borrows is a failure regardless of which cache produced it.
//
// The rotation is the load-bearing part. A style cache keyed by identity rather
// than by value only misbehaves when styles are retired faster than the cache
// notices, and one command's output never retires the previous command's
// styles. Alternating streams with disjoint style sets does: each clear frees
// the previous screen's styles and lets new ones be allocated over them. A
// background-painted prompt runs before each listing because a prompt is where
// the backgrounds that get recycled come from.
//
// The streams are generated here rather than captured, so the test carries its
// own inputs and does not depend on eza, lla, or the contents of any directory.
// Their shape follows a real `lla --include-dirs --sizemap` capture: 10,980 SGR
// sequences on one screen, roughly half of them resets, and not one of them
// setting a background.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func TestListingStyleFidelityAgainstBarePTY(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	enterTerminalMode(t, term)

	if err := term.SendKeys("echo READY", tuitest.Enter); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := term.WaitForText("READY", shellTimeout); err != nil {
		t.Fatalf("no ready: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(400 * time.Millisecond)

	left, top, right, bottom := paneInterior(t, term)
	inW, inH := right-left-1, bottom-top-1
	if inW < 40 || inH < 10 {
		t.Fatalf("pane too small to test: %dx%d", inW, inH)
	}

	// The streams live where both the test process and the pane's shell can
	// read them. The pane's shell is a child of tuios, not of the test, so a
	// path is all they can share.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod stream dir: %v", err)
	}
	names := writeStreams(t, dir, inW)

	ref := tuitest.StartT(t, []string{"/bin/sh"},
		tuitest.WithSize(inW, inH),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv("PS1=$ ", "ENV=", "TERM=xterm-256color", "COLORTERM=truecolor"),
		tuitest.WithLog(io.Discard),
	)
	time.Sleep(500 * time.Millisecond)

	// Heavy background segments: the styles whose identity gets recycled.
	const prompt = `printf '\033[48;2;255;105;180m\033[38;5;231m SEG-A \033[48;5;61m SEG-B \033[0m\n'`

	for round := range 3 {
		for _, name := range names {
			// The marker is split so the command echo does not contain it.
			// Waiting on a marker the echo also carries returns before the
			// output is on screen, and the comparison then runs against the
			// previous screen, where the only cells that still line up are the
			// few whose character happens to match.
			marker := fmt.Sprintf("DONE%d%s", round, name)
			echoed := fmt.Sprintf(`DO""NE%d%s`, round, name)
			cmd := fmt.Sprintf("clear; %s; cat %s; echo %s",
				prompt, filepath.Join(dir, name), echoed)
			for _, side := range []struct {
				term *tuitest.Terminal
				what string
			}{{ref, "reference"}, {term, "tuios"}} {
				if err := side.term.SendKeys(cmd, tuitest.Enter); err != nil {
					t.Fatalf("%s: send %s: %v", side.what, name, err)
				}
				if err := side.term.WaitForText(marker, shellTimeout); err != nil {
					t.Fatalf("%s never finished %s: %v\n%s",
						side.what, name, err, side.term.Snapshot())
				}
			}

			// Content first: a colour is only a claim once both sides agree on
			// what is written where.
			var unsettled, wrong int
			deadline := time.Now().Add(10 * time.Second)
			for {
				unsettled, wrong = styleDiff(nil, ref.Screen(), term.Screen(), left+1, top+1, inW, inH)
				if unsettled == 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if unsettled > 0 {
				t.Fatalf("round %d %s: the pane never caught up with the stream (%d cells differ in content)\n%s",
					round, name, unsettled, term.Snapshot())
			}
			if wrong > 0 {
				styleDiff(t, ref.Screen(), term.Screen(), left+1, top+1, inW, inH)
				t.Fatalf("round %d %s: %d cells reached the host with a colour the stream never set",
					round, name, wrong)
			}
		}
	}
}

// writeStreams generates the listing streams, none of which sets a background.
func writeStreams(t *testing.T, dir string, width int) []string {
	t.Helper()

	palette := [][3]int{
		{156, 207, 216}, {196, 167, 231}, {49, 116, 143}, {64, 61, 82},
		{246, 193, 119}, {224, 222, 244}, {137, 175, 255}, {93, 228, 179},
	}
	files := []string{
		"AGENTS.md", "artifacts", "assets", "cmd", "docs", "e2e", "examples",
		"flake.lock", "go.mod", "internal", "nix", "pkg", "scripts", "web",
	}

	var sizemap, long, short, indexed strings.Builder

	// A size map: a name, a size, a proportion bar of dotted cells each wrapped
	// in its own SGR pair, and a percentage. This is the shape that puts ~11k
	// SGR sequences on one screen.
	bar := max(width-46, 10)
	for i, f := range files {
		c := palette[i%len(palette)]
		fmt.Fprintf(&sizemap, "\x1b[1;38;2;%d;%d;%dm%-20s\x1b[0m %6d KB  ", c[0], c[1], c[2], f, (i+1)*37)
		filled := bar * (i + 1) / (len(files) + 1)
		for j := range bar {
			if j < filled {
				fmt.Fprintf(&sizemap, "\x1b[38;2;%d;%d;%dm█\x1b[0m", c[0], c[1], c[2])
			} else {
				sizemap.WriteString("\x1b[37m⋅\x1b[0m")
			}
		}
		fmt.Fprintf(&sizemap, " %4.1f%%\r\n", float64(i+1)*100/float64(len(files)+1))
	}

	// A long listing: a permission field where the attribute parameter follows
	// the colour in the same SGR and resets land mid-field. This is the shape
	// that produces neighbouring cells with different styles and no background.
	for i, f := range files {
		c := palette[i%len(palette)]
		fmt.Fprintf(&long, "\x1b[1;38;2;%d;%d;%dmdr\x1b[38;2;%d;%d;%dmw\x1b[38;2;%d;%d;%dmx\x1b[0m",
			palette[0][0], palette[0][1], palette[0][2],
			palette[1][0], palette[1][1], palette[1][2],
			palette[2][0], palette[2][1], palette[2][2])
		fmt.Fprintf(&long, "\x1b[38;2;%d;%d;%dmr\x1b[1;38;2;%d;%d;%dm-\x1b[0m\x1b[38;2;%d;%d;%dmx",
			palette[0][0], palette[0][1], palette[0][2],
			palette[3][0], palette[3][1], palette[3][2],
			palette[2][0], palette[2][1], palette[2][2])
		fmt.Fprintf(&long, "\x1b[38;2;%d;%d;%dmr\x1b[1;38;2;%d;%d;%dm-\x1b[0m\x1b[38;2;%d;%d;%dmx\x1b[0m",
			palette[0][0], palette[0][1], palette[0][2],
			palette[3][0], palette[3][1], palette[3][2],
			palette[2][0], palette[2][1], palette[2][2])
		fmt.Fprintf(&long, " \x1b[38;2;%d;%d;%dm%8d\x1b[0m \x1b[1;38;2;%d;%d;%dm%s\x1b[0m\r\n",
			palette[4][0], palette[4][1], palette[4][2], (i+1)*911,
			c[0], c[1], c[2], f)
	}

	// One name per line, truecolor only.
	for i, f := range files {
		c := palette[i%len(palette)]
		fmt.Fprintf(&short, "\x1b[38;2;%d;%d;%dm%s\x1b[0m\r\n", c[0], c[1], c[2], f)
	}

	// The same listing in the 256-colour palette, so the rotation alternates
	// between two disjoint style sets rather than re-using one.
	for i, f := range files {
		fmt.Fprintf(&indexed, "\x1b[38;5;%dm%s\x1b[0m\r\n", 60+3*i, f)
	}

	streams := map[string]string{
		"sizemap": sizemap.String(),
		"long":    long.String(),
		"short":   short.String(),
		"indexed": indexed.String(),
	}
	order := []string{"short", "sizemap", "long", "indexed"}
	for name, body := range streams {
		if p, ok := setsBackground(body); ok {
			t.Fatalf("stream %q sets a background (SGR %d); the whole test assumes it does not", name, p)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write stream %s: %v", name, err)
		}
	}
	return order
}

// setsBackground reports the first background-setting SGR parameter in a
// stream, so a generator that grows one is caught rather than silently
// weakening every assertion below.
func setsBackground(s string) (int, bool) {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			continue
		}
		parts := strings.Split(s[i+2:j], ";")
		for k := 0; k < len(parts); k++ {
			n, err := strconv.Atoi(parts[k])
			if err != nil {
				continue
			}
			switch {
			case n == 38:
				// Skip the colour operands so their values are never read as
				// SGR codes of their own.
				if k+1 < len(parts) && parts[k+1] == "2" {
					k += 4
				} else if k+1 < len(parts) && parts[k+1] == "5" {
					k += 2
				}
			case n == 48, n >= 40 && n <= 49, n >= 100 && n <= 107:
				return n, true
			}
		}
	}
	return 0, false
}

// paneInterior returns the bounds of the single pane's box.
func paneInterior(t *testing.T, term *tuitest.Terminal) (left, top, right, bottom int) {
	t.Helper()
	screen := term.Screen()
	cols, rows := screen.Size()
	left, top, right, bottom = -1, -1, -1, -1
	for row := range rows {
		for col := range cols {
			switch screen.Cell(col, row).Content {
			case "╭":
				top, left = row, col
			case "╯":
				bottom, right = row, col
			}
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatalf("no pane box on screen:\n%s", term.Snapshot())
	}
	return left, top, right, bottom
}

// styleDiff counts cells that disagree on content, and cells that agree on
// content but disagree on colour. A non-nil t reports the colour ones.
func styleDiff(t *testing.T, ref, got tuitest.Screen, x0, y0, w, h int) (unsettled, wrong int) {
	refW, refH := ref.Size()
	w, h = min(w, refW), min(h, refH)
	for row := range h {
		for col := range w {
			r := ref.Cell(col, row)
			g := got.Cell(x0+col, y0+row)
			if r.Content != g.Content {
				unsettled++
				continue
			}
			// A blank cell's foreground paints nothing, and the emulators
			// disagree harmlessly about whether to keep one.
			fgMatters := r.Content != " " && r.Content != ""
			if styleKey(r.Bg) != styleKey(g.Bg) || (fgMatters && styleKey(r.Fg) != styleKey(g.Fg)) {
				if t != nil && wrong < 12 {
					t.Errorf("cell (%d,%d) %q: bare pty has fg=%s bg=%s, tuios has fg=%s bg=%s",
						col, row, r.Content, styleKey(r.Fg), styleKey(r.Bg), styleKey(g.Fg), styleKey(g.Bg))
				}
				wrong++
			}
		}
	}
	return unsettled, wrong
}

func styleKey(c tuitest.Color) string {
	return fmt.Sprintf("%d/%d/%d,%d,%d", c.Kind, c.Index, c.R, c.G, c.B)
}
