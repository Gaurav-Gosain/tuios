package tuie2e

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/vt"
)

const (
	ghostTarget = "ABCDE FGHIJ KLMNO PQRST ZZZZZ QQQQQ WWWW"
	ghostStale  = "ABCDE#FGHIJ#KLMNO#PQRST#ZZZZZ#QQQQQ#WWWW"

	ghostRunTarget = " FGHIJ KLMNO PQRST ZZZZZ QQQQQ "
	ghostRunStale  = "#FGHIJ#KLMNO#PQRST#ZZZZZ#QQQQQ#"
)

func ghostCmd(passes int) string {
	return fmt.Sprintf(`awk 'BEGIN{`+
		`printf "\033[?1049h\033[2J\033[H\033[8;1HGHOSTREADY";fflush();`+
		`a="%s";b="%s";`+
		`for(n=0;n<%d;n++){`+
		`s=(n%%2==0)?a:b;o="\033[?2026h";`+
		`for(i=1;i<=length(s);i++){o=o sprintf("\033[5;%%dH%%s",i,substr(s,i,1))}`+
		`printf "%%s\033[?2026l",o;fflush()`+
		`}`+
		`}'; exec sleep 300`, ghostStale, ghostTarget, passes)
}

const ghostFloodCmd = `awk 'BEGIN{for(n=0;n<2000000;n++){printf "flood %d line of noise\n",n;fflush()}}'; exec sleep 300`

var (
	syncBegin = []byte("\x1b[?2026h")
	syncEnd   = []byte("\x1b[?2026l")
)

type ghostGate struct {
	mu sync.Mutex
	on bool
	b  bytes.Buffer
}

func (g *ghostGate) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.on {
		g.b.Write(p)
	}
	return len(p), nil
}

func (g *ghostGate) open() { g.mu.Lock(); g.on = true; g.mu.Unlock() }

func (g *ghostGate) bytes() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]byte(nil), g.b.Bytes()...)
}

// TestGhostTextFramesArePresentedWhole is the regression test for ghost text: a
// line a full-screen guest was rewriting in place appeared on screen with stale
// characters woven through the new ones, while the lines around it were fine.
//
// The cause was not what tuios computed but how it handed the frame over. The
// renderer writes only the cells that changed, so a frame is a cursor move and a
// run of characters; a host presenting partway through one shows the leading
// cells carrying the new text and the rest still carrying the old. On the
// reported line the changed cells were the spaces, so the tear read as junk
// sitting exactly where the spaces belonged. Every point inside an unbracketed
// frame is a tear waiting to be presented, and a busy neighbour pane keeps
// frames coming.
//
// bubbletea brackets frames in DEC 2026 itself, but only once the host answers a
// DECRQM query for the mode, and it never even asks over SSH or on Apple
// Terminal. tuios now brackets its own frames, which does not depend on an
// answer.
//
// The assertion is on the bytes, not on a sampled screen. Sampling a host
// emulator asynchronously reports tears that were never presented, because a
// snapshot can land between two PTY reads of one frame; a byte stream in which
// every frame is bracketed cannot be presented torn at all.
//
// Negative control: against a build without the bracketing, every frame is
// wrapped in hide-cursor/show-cursor and carries no DEC 2026 at all, so the
// first assertion fails with 0 bracketed frames. Verified against the parent
// commit's binary.
func TestGhostTextFramesArePresentedWhole(t *testing.T) {
	var g ghostGate
	cols, rows := 120, 40
	term, _ := start(t, startOpts{cols: cols, rows: rows, out: &g})
	waitBoot(t, term)

	newWindow(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys(ghostFloodCmd, tuitest.Enter); err != nil {
		t.Fatalf("start the flooding neighbour: %v", err)
	}
	leaveTerminalMode(t, term)

	newWindow(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys(ghostCmd(1000000), tuitest.Enter); err != nil {
		t.Fatalf("start the ghost fixture: %v", err)
	}
	if err := term.WaitForText("GHOSTREADY", shellTimeout); err != nil {
		t.Fatalf("the ghost fixture never started: %v\n%s", err, term.Snapshot())
	}

	g.open()
	time.Sleep(10 * time.Second)
	stream := g.bytes()
	if len(stream) == 0 {
		t.Fatalf("tuios wrote nothing to the host\n%s", term.Snapshot())
	}

	brackets := bytes.Count(stream, syncBegin)
	if brackets == 0 {
		t.Fatalf("not one of the %d bytes tuios wrote to the host was inside a "+
			"synchronized update: every frame can be presented half-written",
			len(stream))
	}
	if closes := bytes.Count(stream, syncEnd); closes != brackets {
		t.Errorf("synchronized updates are unbalanced: %d opened, %d closed", brackets, closes)
	}
	if outside := bytesOutsideSync(stream); outside > 0 {
		t.Errorf("%d bytes were written to the host outside any synchronized update", outside)
	}

	emu := vt.NewEmulator(cols, rows)
	var presented, torn int
	var example string
	rest := stream
	for {
		i := bytes.Index(rest, syncEnd)
		if i < 0 {
			break
		}
		seg := rest[:i+len(syncEnd)]
		rest = rest[i+len(syncEnd):]
		if _, err := emu.Write(seg); err != nil {
			t.Fatalf("replay: %v", err)
		}
		row := ghostRowOf(emu, cols, rows)
		if row == "" {
			continue
		}
		presented++
		if row != ghostRunTarget && row != ghostRunStale {
			torn++
			if example == "" {
				example = row
			}
		}
	}
	if presented == 0 {
		t.Fatalf("the fixture's row was never presented in %d frames\n%s",
			brackets, term.Snapshot())
	}
	t.Logf("presented=%d composed-torn=%d", presented, torn)
	if torn > 0 {
		t.Logf("example of a frame composed mid-guest-update: %q", example)
	}
}

func bytesOutsideSync(stream []byte) int {
	outside, rest := 0, stream
	for len(rest) > 0 {
		i := bytes.Index(rest, syncBegin)
		if i < 0 {
			return outside + len(rest)
		}
		outside += i
		rest = rest[i+len(syncBegin):]
		j := bytes.Index(rest, syncEnd)
		if j < 0 {
			return outside
		}
		rest = rest[j+len(syncEnd):]
	}
	return outside
}

func ghostRowOf(emu *vt.Emulator, cols, rows int) string {
	for y := range rows {
		var sb strings.Builder
		for x := range cols {
			if c := emu.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteString(" ")
			}
		}
		line := sb.String()
		i := strings.Index(line, "FGHIJ")
		if i < 1 {
			continue
		}
		end := min(i-1+len(ghostRunTarget), len(line))
		return line[i-1 : end]
	}
	return ""
}
