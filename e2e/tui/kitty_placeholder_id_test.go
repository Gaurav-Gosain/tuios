package tuie2e

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Kitty Unicode placeholders, checked on the stream tuios writes to the host.
//
// A placeholder cell names its image by id: the low 24 bits in the cell's
// foreground and the high 8 in a third combining mark. tuios re-registers
// every image under an id of its own, so both halves have to be rewritten.
// kitten icat --unicode-placeholder writes the high byte on every cell, and
// tuios rewrote the colour but kept the guest's mark, so the host was told
// about image 1 and the cells named 0xF7000001. Nothing was drawn.
//
// The host these tests play takes 256 colours (TERM=xterm-256color and no
// COLORTERM), which is the profile that rounded a true-colour id to another
// image in issue 292.

// icatPlaceholderGuestID has a non-zero high byte, as the ids icat picks do.
const icatPlaceholderGuestID = 0xF7000A0B

// icatStylePlaceholders is what kitten icat --unicode-placeholder writes for a
// 3x2 image, in stream mode: the image in one a=T,U=1 command, then the cells,
// each with its row, column and high byte marks, in the id's colour.
func icatStylePlaceholders(marker string) string {
	const cols, rows = 3, 2
	var b strings.Builder
	fmt.Fprintf(&b, `\033_Ga=T,U=1,i=%d,f=32,s=1,v=1,c=%d,r=%d,q=2;AAAA/w==\033\\`, icatPlaceholderGuestID, cols, rows)
	for row := range rows {
		b.WriteString(`\033[38;2;0;10;11m`)
		for col := range cols {
			b.WriteString(string(kitty.Placeholder) + string(kitty.Diacritic(row)) + string(kitty.Diacritic(col)) + string(kitty.Diacritic(0xF7)))
		}
		b.WriteString(`\033[39m`)
		if row == 0 {
			b.WriteString(marker)
		}
		b.WriteString(`\r\n`)
	}
	return octalEscapes(b.String())
}

// octalEscapes spells every byte outside ASCII as a printf octal escape, so
// the command line the shell echoes holds no placeholder cells of its own.
func octalEscapes(s string) string {
	var b strings.Builder
	for i := range len(s) {
		if c := s[i]; c < 0x80 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, `\%03o`, c)
		}
	}
	return b.String()
}

var virtualPlaceRE = regexp.MustCompile(`\x1b_Ga=p,U=1,i=(\d+),`)

// hostPlaceholderIDs replays what the host was sent into an emulator that
// keeps placeholder cells and returns the image id every such cell names, and
// the ids the host was given virtual placements for.
func hostPlaceholderIDs(t *testing.T, stream []byte, cols, rows int) (cells map[uint32]int, declared map[uint32]bool) {
	t.Helper()
	stream = markRE.ReplaceAll(stream, nil)
	term := vt.New(cols, rows)
	term.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	if _, err := term.Write(stream); err != nil {
		t.Fatal(err)
	}
	cells = map[uint32]int{}
	for y := range rows {
		for x := range cols {
			c := term.CellAt(x, y)
			if c == nil || !vt.IsKittyPlaceholder(c.Content) {
				continue
			}
			id, _ := vt.KittyPlaceholderImageID(c.Content, c.Style.Fg)
			cells[id]++
		}
	}
	declared = map[uint32]bool{}
	for _, m := range virtualPlaceRE.FindAllSubmatch(stream, -1) {
		id, _ := strconv.ParseUint(string(m[1]), 10, 32)
		declared[uint32(id)] = true
	}
	return cells, declared
}

// checkPlaceholdersNameDeclaredImages fails unless the host was given at least
// one virtual placement and every placeholder cell on its screen names one.
func checkPlaceholdersNameDeclaredImages(t *testing.T, host *kittyHost, want int) {
	t.Helper()
	cells, declared := hostPlaceholderIDs(t, host.bytes(), 120, 40)
	if len(declared) == 0 {
		t.Fatal("the host was never given a virtual placement")
	}
	total := 0
	for id, n := range cells {
		total += n
		if !declared[id] {
			t.Errorf("%d placeholder cells name image %#x, and the host has virtual placements only for %v", n, id, declared)
		}
	}
	if total < want {
		t.Errorf("the host screen shows %d placeholder cells, want at least %d", total, want)
	}
}

// startPlaceholderPane is startGraphicsPane with placeholders on. The fake
// host answers the probe the way kitty does but cannot answer XTVERSION in a
// way tuios reads, so the setting is pinned.
func startPlaceholderPane(t *testing.T, daemon bool) (*tuitest.Terminal, *kittyHost) {
	t.Helper()
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		env:           []string{"TUIOS_SIXEL_GRAPHICS=0", "TUIOS_KITTY_PLACEHOLDERS=1"},
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
	return term, host
}

// TestIcatPlaceholdersNameTheHostImage is the printf fixture, which needs no
// kitten, in the standalone TUI and against a daemon.
//
// Negative control: keeping the guest's third mark in rewriteKittyPlaceholder
// made every cell name 0xf7000001 and failed this.
func TestIcatPlaceholdersNameTheHostImage(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			term, host := startPlaceholderPane(t, daemon)
			host.mark("icat")
			runToExit(t, term, "printf '"+icatStylePlaceholders("")+"'")
			time.Sleep(time.Second)
			checkPlaceholdersNameDeclaredImages(t, host, 6)
		})
	}
}

// TestKittenIcatPlaceholdersNameTheHostImage runs the real kitten icat, when it
// is installed, in file and stream mode.
func TestKittenIcatPlaceholdersNameTheHostImage(t *testing.T) {
	kitten, err := exec.LookPath("kitten")
	if err != nil {
		t.Skip("kitten is not installed")
	}
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			term, host := startPlaceholderPane(t, daemon)
			path, _ := writeIcatPNG(t, t.TempDir())
			host.mark("icat")
			for _, mode := range []string{"file", "stream"} {
				runToExit(t, term, kitten+" icat --stdin=no --unicode-placeholder --transfer-mode="+mode+" "+path)
			}
			time.Sleep(time.Second)
			checkPlaceholdersNameDeclaredImages(t, host, 2)
		})
	}
}

// TestPlaceholdersOnAKittyAndAPlainClient attaches two clients to one daemon
// session: one on a kitty host that draws placeholders, one on a host with no
// graphics. The same pane is shown to both. The kitty client's host gets the
// image and cells that name it. The plain client's host gets neither, and the
// text after the image stays in its column.
func TestPlaceholdersOnAKittyAndAPlainClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const name = "mixed"
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	host := newKittyHost()
	kittyTerm := attachIn(t, base, name, startOpts{
		cols: 120, rows: 40, out: host,
		env: []string{"TUIOS_SIXEL_GRAPHICS=0", "TUIOS_KITTY_PLACEHOLDERS=1"},
	})
	host.answerProbe(t, kittyTerm)
	plain := &lockedStream{}
	plainTerm := attachIn(t, base, name, startOpts{
		cols: 120, rows: 40, out: plain,
		env: []string{"TUIOS_SIXEL_GRAPHICS=0", "TUIOS_KITTY_GRAPHICS=0"},
	})

	enterTerminalMode(t, kittyTerm)
	host.mark("icat")
	plain.mark()
	runToExit(t, kittyTerm, "printf '"+icatStylePlaceholders("AF''TER")+"'")
	if err := plainTerm.WaitForText("AFTER", shellTimeout); err != nil {
		t.Fatalf("the plain client never showed the pane's output: %v\n%s", err, plainTerm.Snapshot())
	}
	time.Sleep(time.Second)

	checkPlaceholdersNameDeclaredImages(t, host, 6)

	got := plain.since()
	if bytes.Contains(got, []byte(string(kitty.Placeholder))) {
		t.Error("the plain client's host was sent placeholder cells it cannot draw")
	}
	if bytes.Contains(got, []byte("\x1b_G")) {
		t.Error("the plain client's host was sent kitty graphics")
	}
	// The three image cells stay three blanks, so AFTER starts three
	// columns right of the pane's left edge, where the image did.
	scr := plainTerm.Screen()
	_, rows := scr.Size()
	for y := range rows {
		line := scr.Line(y)
		i := strings.Index(line, "AFTER")
		if i < 0 {
			continue
		}
		if !strings.HasSuffix(line[:i], "│   ") {
			t.Errorf("the image did not keep its three cells on the plain client: %q", line)
		}
		return
	}
	t.Errorf("AFTER is not on the plain client's screen\n%s", plainTerm.Snapshot())
}

// lockedStream records what a client writes to its host, with a mark to read
// from.
type lockedStream struct {
	kittyHost
	from int
}

func (l *lockedStream) mark() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.from = l.buf.Len()
}

func (l *lockedStream) since() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()[l.from:]...)
}

func (l *lockedStream) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
