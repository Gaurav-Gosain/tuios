package webshell

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// TestRepaintFitsThePane runs the shell in a wide pane, then narrows and
// widens the pane one column at a time, from 120 down to 20 and back. After
// each resize, every line the shell drew for the screen it shows must fit
// the new width. The /learn and docs demos run fastfetch in one wide pane
// and then split it: the terminal rewrapped the 120-column logo into a
// 36-column pane, and the info lines landed on the broken logo.
func TestRepaintFitsThePane(t *testing.T) {
	p := NewPty(120, 40)
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var mu sync.Mutex
	var out strings.Builder
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := p.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	// waitFor waits until ok holds for what the shell printed from offset
	// from on, and returns that.
	waitFor := func(from int, what string, ok func(string) bool) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			s := out.String()[from:]
			mu.Unlock()
			if ok(s) {
				return s
			}
			if time.Now().After(deadline) {
				t.Fatalf("waited 5s for %s; the shell printed:\n%q", what, s)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	printed := func() int {
		mu.Lock()
		defer mu.Unlock()
		return out.Len()
	}

	cmds := []string{"fastfetch", "help", "colors", "ls", "ls -l", "tree", "cowsay hello", "fortune", "tuios", "tuios ls", "tuios tape list"}
	for _, c := range cmds {
		from := printed()
		_, _ = io.WriteString(p, c+"\r")
		waitFor(from, c+" to finish", func(s string) bool { return strings.Contains(s, markDone(0)) && strings.HasSuffix(s, markInput) })
	}

	widths := []int{}
	for w := 119; w >= 20; w-- {
		widths = append(widths, w)
	}
	for w := 21; w <= 120; w++ {
		widths = append(widths, w)
	}
	for _, cols := range widths {
		from := printed()
		_ = p.Resize(cols, 40)
		got := waitFor(from, "a repaint at "+strconv.Itoa(cols)+" columns", func(s string) bool {
			return strings.Contains(s, "\x1b[3J") && strings.HasSuffix(s, markInput)
		})
		// The screen is what was drawn after the last clear.
		screen := got[strings.LastIndex(got, "\x1b[3J")+len("\x1b[3J"):]
		for i, line := range strings.Split(screen, "\r\n") {
			if w := ansi.StringWidth(line); w > cols {
				t.Fatalf("cols %d: line %d is %d cells wide: %q", cols, i, w, ansi.Strip(line))
			}
		}
		plain := ansi.Strip(screen)
		for _, want := range []string{"guest@tuios", "Things to try", "README.md", "directories", "^__^", "Play one with"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("cols %d: the repaint lost %q:\n%s", cols, want, plain)
			}
		}
	}
}

// TestLogStaysBounded prints far more plain output than the log keeps, with
// no layout in between to split it into blocks. The log must stay near its
// bound and keep the newest output.
func TestLogStaysBounded(t *testing.T) {
	var l screenLog
	l.layout(func(int) []string { return []string{"x"} })
	line := strings.Repeat("a", 79) + "\r\n"
	for range 20000 {
		l.write(line)
	}
	l.write("last\r\n")
	if l.size > maxLogBytes+rawChunk {
		t.Fatalf("the log holds %d bytes, bound %d", l.size, maxLogBytes)
	}
	if !strings.HasSuffix(l.replay(80), "last\r\n") {
		t.Fatal("the log lost the newest output")
	}
	// One write bigger than the bound.
	l.write(strings.Repeat(line, 2*maxLogBytes/len(line)))
	if l.size > maxLogBytes {
		t.Fatalf("after one big write the log holds %d bytes, bound %d", l.size, maxLogBytes)
	}
}
