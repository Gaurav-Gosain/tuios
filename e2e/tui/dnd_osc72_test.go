package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Drag and drop between panes over the kitty drag and drop protocol (OSC 72).
//
// Two tiled panes side by side in a daemon session. A program in the left pane
// offers files; the pointer presses on it, crosses to the right pane and
// releases. What each program received is read from its log, so the assertion
// is on the protocol exchange, not on the screen.
//
// How this could pass wrongly, written down first:
//   - the target could log a drop that never crossed tuios, if the two
//     programs shared a channel. They run in two panes with two PTYs and the
//     only path between them is tuios.
//   - the gesture could be recognised from the harness's own mouse reports
//     reaching the source program, which also turns on mouse reporting. The
//     source only offers after a t=o from tuios, which the harness never
//     writes, and the log records that t=o first.
//   - the fallback paste could come from the harness typing. The harness
//     types nothing into the target pane after starting its reader, and the
//     paste carries paths the harness never typed.
//
// Negative controls: with DnDMotion returning false for every event, the
// source never receives t=o and every subtest fails on its first wait. With
// the MIME list sent on the first move only, the target (which reads it from
// every move, as yazi does) refuses every later move and no drop comes. With
// a new press not ending a stale gesture, the ignored-gesture subtest never
// sees its second drag. See NEGATIVE_CONTROLS.md.

// dndConfig is the config of a board: tiled, so two panes sit side by side,
// and drag and drop on, which is off unless asked for.
const dndConfig = "[startup]\ndaemon = true\ntiled = true\nlayout = 'bsp'\n[panes]\ndrag_and_drop = true\n"

// dndClient starts a client with the arguments given: a fixed cell size, so a
// pixel report is arithmetic (see dndMouse), and the host's output kept for
// hostInPixelMode. env is added to that.
func dndClient(t *testing.T, base string, env []string, args ...string) *tuitest.Terminal {
	t.Helper()
	env = append([]string{"TUIOS_CELL_SIZE=8x16"}, env...)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: args, env: env, out: out})
	dndHostOut.Store(term, out)
	return term
}

// dndBoard is a daemon session with two tiled panes side by side, in terminal
// mode, and their rectangles, with drag and drop on.
func dndBoard(t *testing.T) (term *tuitest.Terminal, base, session string, left, right winRect) {
	return dndBoardWith(t, dndConfig)
}

// dndBoardWith is dndBoard under the config given.
func dndBoardWith(t *testing.T, config string) (term *tuitest.Terminal, base, session string, left, right winRect) {
	t.Helper()
	session = "e2e-dnd"
	base = t.TempDir()
	writeConfig(t, base, config)
	term = dndClient(t, base, nil, "new", session)
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "dnd board setup")
	rects := waitForSettledGeometryIn(t, base, session, 2)
	left, right = rects[0], rects[1]
	if right.X < left.X {
		left, right = right, left
	}
	if right.X < left.X+left.Width {
		t.Fatalf("the two panes are not side by side: %+v", rects)
	}
	enterTerminalMode(t, term)
	return term, base, session, left, right
}

// runInPane types a command into a pane through the daemon and waits for the
// pane to show marker.
func runInPane(t *testing.T, base, session string, pane winRect, command, marker string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", pane.ID, command+"\n"); err != nil {
		t.Fatalf("send-text to %s: %v\n%s", pane.ID, err, out)
	}
	waitCapture(t, base, session, pane.ID, marker)
}

// dndHostOut is each board's copy of what tuios wrote to its terminal, by
// terminal, so the mouse helpers can tell which form tuios asked for.
var dndHostOut sync.Map

// hostInPixelMode reports whether tuios has put its terminal in SGR-pixel
// mouse mode (1016), which it does while a pane that asked for pixel
// reports is on screen. The dnd kitten is such a pane. A real terminal then
// reports the pointer in pixels; the harness has to as well, or tuios reads
// cell 104 as pixel 104, which is cell 13.
func hostInPixelMode(term *tuitest.Terminal) bool {
	v, ok := dndHostOut.Load(term)
	if !ok {
		return false
	}
	s := v.(*lockedBuffer).String()
	return strings.LastIndex(s, "\x1b[?1016h") > strings.LastIndex(s, "\x1b[?1016l")
}

// dndMouse sends one report at a cell, in pixels at the cell's centre when
// the host is in pixel mode.
func dndMouse(t *testing.T, term *tuitest.Terminal, what string, action tuitest.MouseAction, col, row int) {
	t.Helper()
	ev := tuitest.MouseEvent{Col: col, Row: row, Button: tuitest.MouseLeft, Action: action}
	if hostInPixelMode(term) {
		ev.Col, ev.Row, ev.Pixel = col*8+4, row*16+8, true
	}
	sendMouse(t, term, what, ev)
}

// peerSource and peerTarget are the shell commands that run a dndpeer in a
// raw pane, logging to log. The source offers files; env goes in front of
// the peer, after the stty that the raw pane needs.
func peerSource(bin, log, env string, files ...string) string {
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = "'" + strings.ReplaceAll(f, "'", `'\''`) + "'"
	}
	return "stty raw -echo; " + env + bin + " source " + log + " " + strings.Join(quoted, " ")
}

func peerTarget(bin, log string) string {
	return "stty raw -echo; " + bin + " target " + log
}

// dragAcross presses in from, carries the pointer a cell at a time to to, and
// returns without releasing. The frame mid-drag is saved as an artifact.
func dragAcross(t *testing.T, term *tuitest.Terminal, fromX, fromY, toX, toY int, art string) {
	t.Helper()
	dndMouse(t, term, "press", tuitest.MousePress, fromX, fromY)
	steps := max(abs(toX-fromX), abs(toY-fromY))
	for i := 1; i <= steps; i++ {
		dndMouse(t, term, "motion", tuitest.MouseDrag, fromX+(toX-fromX)*i/steps, fromY+(toY-fromY)*i/steps)
	}
	time.Sleep(400 * time.Millisecond)
	saveArtifact(t, term, art, "mid-drag")
}

// dropAt releases the button at a cell.
func dropAt(t *testing.T, term *tuitest.Terminal, col, row int) {
	t.Helper()
	dndMouse(t, term, "release", tuitest.MouseRelease, col, row)
}

func TestDnDBetweenPanes(t *testing.T) {
	bin := buildDnDPeer(t)

	t.Run("source to target over the protocol", func(t *testing.T) {
		term, base, session, left, right := dndBoard(t)
		art := artifactDir(t)
		dir := t.TempDir()
		for _, name := range []string{"a.txt", "b.txt"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		srcLog, dstLog := filepath.Join(dir, "source.log"), filepath.Join(dir, "target.log")
		runInPane(t, base, session, right, peerTarget(bin, dstLog), "DNDPEER-READY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")), "DNDPEER-READY")

		fromX, fromY := left.X+4, left.Y+5
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, fromX, fromY, toX, toY, art)

		// The label names the files and the pane they come from, so the person
		// can check both before letting go.
		if s := term.Screen().Text(); !strings.Contains(s, "a.txt, b.txt from ") {
			t.Errorf("the drag label is not on screen mid-drag\n%s", term.Snapshot())
		}

		dropAt(t, term, toX, toY)
		got := waitFile(t, dstLog, func(s string) bool { return strings.Contains(s, "data x=1") }, "the target never got the drop data")
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag finish")
		_ = os.WriteFile(filepath.Join(art, "source.log"), []byte(src), 0o644)
		_ = os.WriteFile(filepath.Join(art, "target.log"), []byte(got), 0o644)

		// The source: the gesture at the pressed cell, the start accepted, the
		// target's acceptance, the drop, the finish.
		for _, want := range []string{
			"recv t=o o=0 x=", "sent offer+start", `recv t=E o=0 x=0 y=0 m=false payload="OK"`,
			"recv t=e o=0 x=1 y=0", "recv t=e o=1 x=2", "recv t=e o=0 x=3", "finished canceled=0",
		} {
			if !strings.Contains(src, want) {
				t.Errorf("the source log lacks %q:\n%s", want, src)
			}
		}
		// The target: a move with the MIME list, the drop, the URI list.
		uris := "file://" + filepath.Join(dir, "a.txt") + "\\r\\nfile://" + filepath.Join(dir, "b.txt")
		for _, want := range []string{
			`recv t=m o=3 x=`, `payload="text/uri-list text/plain"`, "recv t=M o=1", `data x=1 err=<nil> "` + uris + `"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the target log lacks %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "error") {
			t.Errorf("the target was sent an error:\n%s", got)
		}
	})

	t.Run("drop on a pane without the protocol pastes the paths", func(t *testing.T) {
		term, base, session, left, right := dndBoard(t)
		art := artifactDir(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		srcLog, out := filepath.Join(dir, "source.log"), filepath.Join(dir, "pasted")
		// A reader with bracketed paste on, as a shell or an agent has it: the
		// paths must arrive bracketed, so the program knows they were not typed.
		runInPane(t, base, session, right,
			"stty raw -echo; printf '\\033[?2004h'; printf 'SINK%sREADY\\n' ''; cat > "+out, "SINKREADY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", filepath.Join(dir, "one.txt")), "DNDPEER-READY")

		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, art)
		dropAt(t, term, toX, toY)

		// The path and a trailing space, as the rail's drop pastes it, so the
		// next word typed is an argument of its own.
		want := "\x1b[200~" + filepath.Join(dir, "one.txt") + " \x1b[201~"
		pasted := waitFile(t, out, func(s string) bool { return strings.Contains(s, "\x1b[201~") }, "the pane never got the paste")
		if pasted != want {
			t.Errorf("the pane got %q, want %q", pasted, want)
		}
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag finish")
		if !strings.Contains(src, "finished canceled=0") {
			t.Errorf("the source was not told the drop succeeded:\n%s", src)
		}
	})

	t.Run("dropped text goes only into a pane with bracketed paste on", func(t *testing.T) {
		// A source that offers text/plain alone, two lines of it. A shell
		// without bracketed paste would run the first line as typed, so the
		// drop is refused there and nothing arrives; once the reader turns
		// bracketed paste on, the text arrives inside the marks. The same
		// reader script in two acts: a cat that times out, then a cat with
		// the mode on.
		term, base, session, left, right := dndBoard(t)
		dir := t.TempDir()
		srcLog, out1, out2 := filepath.Join(dir, "source.log"), filepath.Join(dir, "without"), filepath.Join(dir, "with")
		runInPane(t, base, session, right,
			"stty raw -echo; printf 'SINK%sREADY\\n' ''; timeout 12 cat > "+out1+"; printf '\\033[?2004h'; printf 'SINK%sREADY2\\n' ''; cat > "+out2, "SINKREADY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "DNDPEER_OFFER=text "), "DNDPEER-READY")
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2

		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
		if s := term.Screen().Text(); !strings.Contains(s, "no bracketed paste") {
			t.Errorf("the label does not say why the pane refuses the text\n%s", term.Snapshot())
		}
		dropAt(t, term, toX, toY)
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag end")
		if !strings.Contains(src, "finished canceled=1") {
			t.Errorf("a text drop on a pane without bracketed paste was not refused:\n%s", src)
		}
		time.Sleep(500 * time.Millisecond)
		if b, _ := os.ReadFile(out1); len(b) != 0 {
			t.Errorf("the pane without bracketed paste got %q", b)
		}

		waitCapture(t, base, session, right.ID, "SINKREADY2")
		dragAcross(t, term, left.X+4, left.Y+6, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
		got := waitFile(t, out2, func(s string) bool { return strings.Contains(s, "\x1b[201~") }, "the pane with bracketed paste never got the text")
		if want := "\x1b[200~line one\nline two\x1b[201~"; got != want {
			t.Errorf("the pane got %q, want %q", got, want)
		}
	})

	t.Run("a drop on a fish pane is quoted for fish", func(t *testing.T) {
		// The fallback goes through the rail's drop path, which quotes for
		// the program that reads the paste. fish reads \' inside single
		// quotes, where a POSIX shell wants '\''. Needs fish, as the other
		// fish tests of the suite do.
		fish, err := exec.LookPath("fish")
		if err != nil {
			t.Skip("fish is not installed")
		}
		term, base, session, left, right := dndBoard(t)
		// A short directory: the pasted path has to fit on one line of the
		// pane for the screen to show it whole.
		dir, err := os.MkdirTemp("", "dnd")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		file := filepath.Join(dir, "it's here.txt")
		if err := os.WriteFile(file, []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		srcLog := filepath.Join(t.TempDir(), "source.log")
		// fish at its prompt, with a marker printed once it is up.
		runInPane(t, base, session, right, "exec "+fish+" -C 'echo FISH\"\"READY'", "FISHREADY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", file), "DNDPEER-READY")
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
		// fish shows the pasted text on its command line.
		waitCapture(t, base, session, right.ID, `'`+strings.ReplaceAll(file, "'", `\'`)+`'`)
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag finish")
		if !strings.Contains(src, "finished canceled=0") {
			t.Errorf("the source was not told the drop succeeded:\n%s", src)
		}
	})

	t.Run("a gesture the program ignored does not block the next", func(t *testing.T) {
		term, base, session, left, right := dndBoard(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		srcLog, dstLog := filepath.Join(dir, "source.log"), filepath.Join(dir, "target.log")
		runInPane(t, base, session, right, peerTarget(bin, dstLog), "DNDPEER-READY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "DNDPEER_IGNORE_GESTURES=1 ", filepath.Join(dir, "one.txt")), "DNDPEER-READY")
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		// The first gesture lands on nothing the program drags: it stays
		// silent, and the button comes up.
		dragAcross(t, term, left.X+4, left.Y+5, left.X+12, left.Y+9, artifactDir(t))
		dropAt(t, term, left.X+12, left.Y+9)
		waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "ignored gesture") }, "the source never saw the first gesture")
		// The second gesture is a new drag, not a continuation of the first.
		dragAcross(t, term, left.X+4, left.Y+6, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
		waitFile(t, dstLog, func(s string) bool { return strings.Contains(s, "data x=1") }, "the target never got the drop data of the second gesture")
	})

	t.Run("with drag_and_drop off the press is the pane's and the query goes unanswered", func(t *testing.T) {
		term, base, session, left, right := dndBoardWith(t, "[startup]\ndaemon = true\ntiled = true\nlayout = 'bsp'\n")
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		srcLog, dstLog := filepath.Join(dir, "source.log"), filepath.Join(dir, "target.log")
		runInPane(t, base, session, right, peerTarget(bin, dstLog), "DNDPEER-READY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", filepath.Join(dir, "one.txt")), "DNDPEER-READY")
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
		// The press and the motion reach the source as mouse reports, which
		// is what a terminal without the protocol does with them.
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "mouse") }, "the source never got the press as a mouse report")
		time.Sleep(time.Second)
		b, _ := os.ReadFile(srcLog)
		src = string(b)
		if strings.Contains(src, "recv t=o") || strings.Contains(src, "recv t=q") {
			t.Errorf("with drag and drop off the source was offered a drag, or had its query answered:\n%s", src)
		}
		if b, _ := os.ReadFile(dstLog); strings.Contains(string(b), "recv t=") {
			t.Errorf("with drag and drop off the target heard of the protocol:\n%s", b)
		}
	})

	t.Run("a drop into the source pane is refused", func(t *testing.T) {
		term, base, session, left, _ := dndBoard(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		srcLog := filepath.Join(dir, "source.log")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", filepath.Join(dir, "one.txt")), "DNDPEER-READY")
		toX, toY := left.X+left.Width-6, left.Y+left.Height-6
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag end")
		if !strings.Contains(src, "finished canceled=1") || strings.Contains(src, "recv t=e o=0 x=3") {
			t.Errorf("a drop into the source pane was not cancelled:\n%s", src)
		}
	})
}

// TestDnDRealPrograms runs yazi as the source and the dnd kitten, then a plain
// reader, as the target. It needs the binaries, named by TUIOS_E2E_YAZI and
// TUIOS_E2E_KITTEN, and skips without them: neither is a dependency of the
// suite. The kitten copies a dropped text/uri-list into its directory, which is
// the artifact: the file exists on the other side, made by the kitten alone.
func TestDnDRealPrograms(t *testing.T) {
	yazi, kitten := os.Getenv("TUIOS_E2E_YAZI"), os.Getenv("TUIOS_E2E_KITTEN")
	if yazi == "" {
		t.Skip("TUIOS_E2E_YAZI is not set")
	}

	// yaziIn starts yazi in a pane on dir and returns the screen row and
	// column of the file to drag.
	yaziIn := func(t *testing.T, term *tuitest.Terminal, base, session string, pane winRect, home, dir string) (int, int) {
		t.Helper()
		runInPane(t, base, session, pane, fmt.Sprintf("cd %s && HOME=%s %s", dir, home, yazi), "drag-me.txt")
		time.Sleep(time.Second)
		return findText(t, term, "drag-me.txt")
	}

	t.Run("yazi to the dnd kitten", func(t *testing.T) {
		if kitten == "" {
			t.Skip("TUIOS_E2E_KITTEN is not set")
		}
		term, base, session, left, right := dndBoard(t)
		art := artifactDir(t)
		home, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "drag-me.txt"), []byte("hello from yazi\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runInPane(t, base, session, right, fmt.Sprintf("cd %s && %s dnd --drop-anywhere=copy", dst, kitten), "transfer the files here")
		row, col := yaziIn(t, term, base, session, left, home, src)

		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, col+2, row, toX, toY, art)
		if s := term.Screen().Text(); !strings.Contains(s, "drag-me.txt from ") {
			t.Errorf("the drag label is not on screen mid-drag\n%s", term.Snapshot())
		}
		dropAt(t, term, toX, toY)
		got := waitFile(t, filepath.Join(dst, "drag-me.txt"), func(s string) bool { return s == "hello from yazi\n" }, "the kitten never copied the file")
		_ = os.WriteFile(filepath.Join(art, "copied.txt"), []byte(got), 0o644)
		saveArtifact(t, term, art, "after-drop")
	})

	t.Run("yazi to a plain reader", func(t *testing.T) {
		term, base, session, left, right := dndBoard(t)
		art := artifactDir(t)
		home, src := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "drag-me.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(home, "pasted")
		runInPane(t, base, session, right,
			"stty raw -echo; printf '\\033[?2004h'; printf 'SINK%sREADY\\n' ''; cat > "+out, "SINKREADY")
		row, col := yaziIn(t, term, base, session, left, home, src)
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, col+2, row, toX, toY, art)
		dropAt(t, term, toX, toY)
		want := "\x1b[200~" + filepath.Join(src, "drag-me.txt") + " \x1b[201~"
		pasted := waitFile(t, out, func(s string) bool { return strings.Contains(s, "\x1b[201~") }, "the pane never got the paste")
		if pasted != want {
			t.Errorf("the pane got %q, want %q", pasted, want)
		}
	})
}

// buildDnDPeer compiles the protocol peer once per test binary.
func buildDnDPeer(t *testing.T) string {
	t.Helper()
	dndPeerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dndpeer")
		if err != nil {
			dndPeerErr = err
			return
		}
		bin := filepath.Join(dir, "dndpeer")
		build := exec.Command("go", "build", "-o", bin, "./dndpeer")
		if out, err := build.CombinedOutput(); err != nil {
			dndPeerErr = fmt.Errorf("build dndpeer: %v\n%s", err, out)
			return
		}
		dndPeerBin = bin
	})
	if dndPeerErr != nil {
		t.Fatalf("%v", dndPeerErr)
	}
	return dndPeerBin
}

var (
	dndPeerOnce sync.Once
	dndPeerBin  string
	dndPeerErr  error
)

// TestDnDRegistrationOutlivesTheClient: the registration a program makes once,
// when it starts, is kept by the daemon, so a client that attaches later can
// still drag from it and drop on it. And with two clients attached, the program
// hears one answer to its query and only the client that made the drag answers
// for it.
//
// How this could pass wrongly, written down first:
//   - the second client could see the registration because the program sent
//     it again. The peers register once, at start, before that client exists.
//   - the drop could reach the target from the first client, still attached.
//     attach -d takes it off before the drag; the drag is made in the second.
//   - one answer to the query could be the harness's. The harness writes
//     nothing to a pane's input but the drag's mouse reports.
//
// Negative control: with takeDaemonDnD returning at once, the reattach subtest
// fails: the new client never offers the drag. With dndShared reporting false,
// the two-client subtest fails: the peers get two answers to t=q and an EPERM
// from the client that did not make the drag. See NEGATIVE_CONTROLS.md.
func TestDnDRegistrationOutlivesTheClient(t *testing.T) {
	bin := buildDnDPeer(t)

	// peers starts a target in the right pane and a source in the left one.
	peers := func(t *testing.T, base, session string, left, right winRect) (dir, srcLog, dstLog string) {
		t.Helper()
		dir = t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
			t.Fatal(err)
		}
		srcLog, dstLog = filepath.Join(dir, "source.log"), filepath.Join(dir, "target.log")
		runInPane(t, base, session, right, peerTarget(bin, dstLog), "DNDPEER-READY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", filepath.Join(dir, "a.txt")), "DNDPEER-READY")
		return dir, srcLog, dstLog
	}
	// another attaches one more client of the board's size, in terminal mode.
	another := func(t *testing.T, base, session string, args ...string) *tuitest.Terminal {
		t.Helper()
		term := dndClient(t, base, nil, append([]string{"attach", session}, args...)...)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
			t.Fatalf("the client never showed the two panes: %v\n%s", err, term.Snapshot())
		}
		windowManagementMode(t, term)
		enterTerminalMode(t, term)
		return term
	}

	t.Run("a client attached after the programs started", func(t *testing.T) {
		_, base, session, left, right := dndBoard(t)
		dir, srcLog, dstLog := peers(t, base, session, left, right)
		// The first client goes; a new one comes, which never saw the peers
		// register.
		term := another(t, base, session, "-d")
		dragAcross(t, term, left.X+4, left.Y+5, right.X+right.Width/2, right.Y+right.Height/2, artifactDir(t))
		dropAt(t, term, right.X+right.Width/2, right.Y+right.Height/2)
		got := waitFile(t, dstLog, func(s string) bool { return strings.Contains(s, "data x=1") }, "the target never got the drop data")
		if !strings.Contains(got, "file://"+filepath.Join(dir, "a.txt")) {
			t.Errorf("the target got the wrong data:\n%s", got)
		}
		src, _ := os.ReadFile(srcLog)
		if !strings.Contains(string(src), "finished canceled=0") {
			t.Errorf("the source was not told the drop succeeded:\n%s", src)
		}
	})

	t.Run("two clients attached", func(t *testing.T) {
		first, base, session, left, right := dndBoard(t)
		second := another(t, base, session)
		_, srcLog, dstLog := peers(t, base, session, left, right)
		// The drag is made in the first client, still in terminal mode from
		// the board; the second one sees every byte the peers write and must
		// stay out of it.
		dragAcross(t, first, left.X+4, left.Y+5, right.X+right.Width/2, right.Y+right.Height/2, artifactDir(t))
		dropAt(t, first, right.X+right.Width/2, right.Y+right.Height/2)
		waitFile(t, dstLog, func(s string) bool { return strings.Contains(s, "data x=1") }, "the target never got the drop data")
		waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag finish")
		// A second answer to the query, or an error from the client that
		// did not make the drag, would have been written before the drop
		// finished: both clients read the same stream, and the second one
		// got every message long before the first one's drag ended. The
		// pause is for what is still in flight through the daemon.
		alive(t, second, "the second client after the drag")
		time.Sleep(time.Second)
		b, _ := os.ReadFile(dstLog)
		dst := string(b)
		b, _ = os.ReadFile(srcLog)
		src := string(b)
		for name, log := range map[string]string{"source": src, "target": dst} {
			if n := strings.Count(log, "recv t=q"); n != 1 {
				t.Errorf("the %s got %d answers to its query, want 1:\n%s", name, n, log)
			}
		}
		if n := strings.Count(dst, "data x=1"); n != 1 {
			t.Errorf("the target got the data %d times, want once:\n%s", n, dst)
		}
		if strings.Contains(dst, "error") || strings.Contains(src, "EPERM") || strings.Contains(src, "EINVAL") {
			t.Errorf("a client that did not make the drag answered for it:\nsource:\n%s\ntarget:\n%s", src, dst)
		}
		if strings.Count(src, `payload="OK"`) != 1 || !strings.Contains(src, "finished canceled=0") {
			t.Errorf("the source did not see one start and a finished drop:\n%s", src)
		}
	})
}

// TestDnDRefusesPanesOnAnotherMachine: a client attached with --host draws
// panes whose shells run on another machine. A path a program there offers
// is a path over there, and a local path dropped there means nothing. Until
// a file transfer carries the data (phase 4), such panes take no part: the
// gesture is not offered, no pane is a target, and the daemon on this side
// does not answer the query for a pane it only relays, so the program hears
// one answer, from the daemon that runs it.
//
// How this could pass wrongly, written down first:
//   - the source could stay silent because it never saw the press. The press
//     reaches it as a mouse report, which its log shows.
//   - the query could go unanswered on both sides. The far daemon answers it,
//     and the log shows exactly one answer.
//
// Negative control: with dndPaneLocal reporting true for every pane, the
// source is offered the drag and the test fails. See NEGATIVE_CONTROLS.md.
func TestDnDRefusesPanesOnAnotherMachine(t *testing.T) {
	bin := buildDnDPeer(t)
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeConfig(t, base, dndConfig+"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n")
	writeConfig(t, remote, dndConfig)
	const session = "far-dnd"
	if out, err := tuiosCLI(t, remote, "new", session, "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	srcLog, dstLog := filepath.Join(dir, "source.log"), filepath.Join(dir, "target.log")
	// The session's first window and one more: two tiled panes side by side.
	if out, err := tuiosCLI(t, remote, "new-window", "dst", "-s", session); err != nil {
		t.Fatalf("new-window on the far machine: %v\n%s", err, out)
	}
	rects := waitForSettledGeometryIn(t, remote, session, 2)
	if rects[1].X < rects[0].X {
		rects[0], rects[1] = rects[1], rects[0]
	}
	ids := []string{rects[0].ID, rects[1].ID}
	for i, cmd := range []string{peerSource(bin, srcLog, "", filepath.Join(dir, "one.txt")), peerTarget(bin, dstLog)} {
		if out, err := tuiosCLI(t, remote, "send-text", "-s", session, "-w", ids[i], cmd+"\n"); err != nil {
			t.Fatalf("send-text on the far machine: %v\n%s", err, out)
		}
		waitCapture(t, remote, session, ids[i], "DNDPEER-READY")
	}

	term := dndClient(t, base, []string{"TUIOS_SSH=" + ssh}, "attach", "--host", "build", session)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never drew the far session's two panes: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	// Two tiled panes side by side in 140 columns: the source on the left.
	dragAcross(t, term, 20, 12, 110, 12, artifactDir(t))
	dropAt(t, term, 110, 12)
	src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "mouse") }, "the source never got the press as a mouse report")
	time.Sleep(time.Second)
	b, _ := os.ReadFile(srcLog)
	src = string(b)
	if strings.Contains(src, "recv t=o") {
		t.Errorf("a pane on another machine was offered a drag:\n%s", src)
	}
	if n := strings.Count(src, "recv t=q"); n != 1 {
		t.Errorf("the source got %d answers to its query, want 1 (the far daemon's):\n%s", n, src)
	}
	if b, _ := os.ReadFile(dstLog); strings.Contains(string(b), "recv t=m") || strings.Contains(string(b), "recv t=M") {
		t.Errorf("a pane on another machine was made a drop target:\n%s", b)
	}
}

// TestDnDRefusesAPaneHostedElsewhere: a pane of a local session whose shell
// runs on another machine (new-window --host). It is drawn beside local
// panes, so a drag can cross into it or start from it, and neither may
// happen: a local path means nothing there, and a path a program there
// offers means nothing here. The daemon on this side relays the pane and
// does not answer its query, and no daemon does: the program is in a
// terminal without the protocol.
//
// How this could pass wrongly, written down first:
//   - the hosted pane could be missed by the drag. The pointer is carried
//     into the cell its program drew, which the client shows.
//   - the hosted source could stay silent because the press never reached
//     it. Its log shows the press as a mouse report.
//
// Negative control: with dndPaneLocal ignoring where a pane's shell runs,
// the hosted pane is sent the drag and the test fails. See
// NEGATIVE_CONTROLS.md.
func TestDnDRefusesAPaneHostedElsewhere(t *testing.T) {
	bin := buildDnDPeer(t)
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeConfig(t, base, dndConfig+"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n")
	writeConfig(t, remote, dndConfig)
	// build's daemon has to be up for a window to be opened there.
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	env := []string{"TUIOS_SSH=" + ssh}
	const session = "home"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	localLog, farLog := filepath.Join(dir, "local.log"), filepath.Join(dir, "far.log")

	term := dndClient(t, base, env, "new", session)
	killDaemon(t, base)
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	// A local pane with a peer that offers and takes, and a pane on build
	// with the same peer. The far machine shares this file system, so the
	// peer there logs into the same directory.
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "near", "-s", session, "--", "sh", "-c", peerSource(bin, localLog, "", filepath.Join(dir, "one.txt"))); err != nil {
		t.Fatalf("create the local window: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", session, "--host", "build", "--", "sh", "-c", peerSource(bin, farLog, "", filepath.Join(dir, "one.txt"))); err != nil {
		t.Fatalf("create the window on build: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 2 && strings.Count(s.Text(), "DNDPEER-READY") == 2
	}, bootTimeout); err != nil {
		t.Fatalf("the two peers never came up: %v\n%s", err, term.Snapshot())
	}
	rects := waitForSettledGeometryIn(t, base, session, 2)
	if rects[1].X < rects[0].X {
		rects[0], rects[1] = rects[1], rects[0]
	}
	near, far := rects[0], rects[1]
	if !strings.Contains(term.Screen().Text(), "build:faraway") {
		t.Fatalf("the far pane is not marked as on build:\n%s", term.Snapshot())
	}
	// Which rectangle is the far pane: the one whose title carries the host.
	farRow := strings.Split(term.Screen().Text(), "\n")[far.Y+far.Height-1]
	if !strings.Contains(farRow, "build:faraway") {
		near, far = far, near
	}
	enterTerminalMode(t, term)

	// Local to hosted: the hosted pane is no target, so the drop is
	// cancelled and nothing reaches it.
	dragAcross(t, term, near.X+4, near.Y+5, far.X+far.Width/2, far.Y+far.Height/2, artifactDir(t))
	dropAt(t, term, far.X+far.Width/2, far.Y+far.Height/2)
	local := waitFile(t, localLog, func(s string) bool { return strings.Contains(s, "finished") }, "the local source never heard its drag end")
	if !strings.Contains(local, "finished canceled=1") {
		t.Errorf("a drop onto a pane hosted elsewhere was not cancelled:\n%s", local)
	}
	// Hosted to local: the press is the hosted program's, as a mouse report,
	// and no gesture follows.
	dragAcross(t, term, far.X+4, far.Y+5, near.X+near.Width/2, near.Y+near.Height/2, artifactDir(t))
	dropAt(t, term, near.X+near.Width/2, near.Y+near.Height/2)
	farOut := waitFile(t, farLog, func(s string) bool { return strings.Contains(s, "mouse") }, "the hosted source never got the press as a mouse report")
	time.Sleep(time.Second)
	b, _ := os.ReadFile(farLog)
	farOut = string(b)
	if strings.Contains(farOut, "recv t=o") || strings.Contains(farOut, "recv t=m") || strings.Contains(farOut, "recv t=M") {
		t.Errorf("a pane hosted elsewhere took part in a drag:\n%s", farOut)
	}
	// Nothing answers the hosted program's query: the daemon here relays
	// the pane and does not speak for it, and the daemon on build runs the
	// process without an emulator of its own. To the program this is a
	// terminal without the protocol, which is what a pane that takes no
	// part should look like.
	if n := strings.Count(farOut, "recv t=q"); n != 0 {
		t.Errorf("the hosted program got %d answers to its query, want none:\n%s", n, farOut)
	}
}
