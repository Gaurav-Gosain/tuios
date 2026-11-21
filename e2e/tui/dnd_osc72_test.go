package tuie2e

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const dndConfig = "[startup]\ndaemon = true\ntiled = true\nlayout = 'bsp'\n[panes]\ndrag_and_drop = true\n"

func dndClient(t *testing.T, base string, env []string, args ...string) *tuitest.Terminal {
	t.Helper()
	env = append([]string{"TUIOS_CELL_SIZE=8x16"}, env...)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: args, env: env, out: out})
	dndHostOut.Store(term, out)
	return term
}

func dndBoard(t *testing.T) (term *tuitest.Terminal, base, session string, left, right winRect) {
	return dndBoardWith(t, dndConfig)
}

func dndBoardWith(t *testing.T, config string) (term *tuitest.Terminal, base, session string, left, right winRect) {
	t.Helper()
	return dndBoardEnv(t, config, nil)
}

func dndBoardEnv(t *testing.T, config string, env []string) (term *tuitest.Terminal, base, session string, left, right winRect) {
	t.Helper()
	session = "e2e-dnd"
	base = t.TempDir()
	writeConfig(t, base, config)
	term = dndClient(t, base, env, "new", session)
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

func runInPane(t *testing.T, base, session string, pane winRect, command, marker string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", pane.ID, command+"\n"); err != nil {
		t.Fatalf("send-text to %s: %v\n%s", pane.ID, err, out)
	}
	waitCapture(t, base, session, pane.ID, marker)
}

var dndHostOut sync.Map

func hostInPixelMode(term *tuitest.Terminal) bool {
	v, ok := dndHostOut.Load(term)
	if !ok {
		return false
	}
	s := v.(*lockedBuffer).String()
	return strings.LastIndex(s, "\x1b[?1016h") > strings.LastIndex(s, "\x1b[?1016l")
}

func dndMouse(t *testing.T, term *tuitest.Terminal, what string, action tuitest.MouseAction, col, row int) {
	t.Helper()
	ev := tuitest.MouseEvent{Col: col, Row: row, Button: tuitest.MouseLeft, Action: action}
	if hostInPixelMode(term) {
		ev.Col, ev.Row, ev.Pixel = col*8+4, row*16+8, true
	}
	sendMouse(t, term, what, ev)
}

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

func dropAt(t *testing.T, term *tuitest.Terminal, col, row int) {
	t.Helper()
	dndMouse(t, term, "release", tuitest.MouseRelease, col, row)
}

func TestDnDBetweenPanes(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
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

		if s := term.Screen().Text(); !strings.Contains(s, "a.txt, b.txt from ") {
			t.Errorf("the drag label is not on screen mid-drag\n%s", term.Snapshot())
		}

		dropAt(t, term, toX, toY)
		got := waitFile(t, dstLog, func(s string) bool { return strings.Contains(s, "data x=1") }, "the target never got the drop data")
		src := waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag finish")
		_ = os.WriteFile(filepath.Join(art, "source.log"), []byte(src), 0o644)
		_ = os.WriteFile(filepath.Join(art, "target.log"), []byte(got), 0o644)

		for _, want := range []string{
			"recv t=o o=0 x=", "sent offer+start", `recv t=E o=0 x=0 y=0 m=false payload="OK"`,
			"recv t=e o=0 x=1 y=0", "recv t=e o=1 x=2", "recv t=e o=0 x=3", "finished canceled=0",
		} {
			if !strings.Contains(src, want) {
				t.Errorf("the source log lacks %q:\n%s", want, src)
			}
		}
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
		runInPane(t, base, session, right,
			"stty raw -echo; printf '\\033[?2004h'; printf 'SINK%sREADY\\n' ''; cat > "+out, "SINKREADY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", filepath.Join(dir, "one.txt")), "DNDPEER-READY")

		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, art)
		dropAt(t, term, toX, toY)

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
		fish := needTool(t, "fish")
		term, base, session, left, right := dndBoard(t)
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
		runInPane(t, base, session, right, "exec "+fish+" -C 'set -g fish_greeting; function fish_prompt; echo -n \"> \"; end; echo FISH\"\"READY'", "FISHREADY")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "", file), "DNDPEER-READY")
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
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
		dragAcross(t, term, left.X+4, left.Y+5, left.X+12, left.Y+9, artifactDir(t))
		dropAt(t, term, left.X+12, left.Y+9)
		waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "ignored gesture") }, "the source never saw the first gesture")
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

// TestDnDLabelNamesEachPathOfALine drags a text/uri-list that a program wrote
// by hand, with a raw space in a line ("file:///t/x /t/y"), as a program may.
// The drop reads such a line as two paths, so the label has to name both, and
// the person's screen has to stay theirs: the label is drawn while the drag
// is in flight, and on the desktop route the same names are the drag image
// the terminal shows.
//
// How this could pass wrongly, written down first:
//   - the names could come from the files on disk, not from the list the
//     program sent ahead. The paths under /t do not exist; the peer sends the
//     list as given (DNDPEER_URILIST), and no FILE argument names them.
//   - the label could be read off a frame drawn before the drag. It names
//     the pane it comes from ("from "), which only the drag label does.
//   - the desktop drag could start with no image of tuios's own. The image
//     the client writes ahead of the start is decoded and must name both
//     paths.
//
// Negative control: with the label naming one entry a line again, the label
// never shows, the crash overlay does, and the desktop drag never starts.
// See NEGATIVE_CONTROLS.md.
func TestDnDLabelNamesEachPathOfALine(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	bin := buildDnDPeer(t)

	for _, tc := range []struct {
		name, list, label, paste string
	}{
		{"a line with a space names both paths", "file:///t/x /t/y", "x, y from ", "\x1b[200~/t/x /t/y \x1b[201~"},
		{"a line with a space after a plain line", "file:///t/a|file:///t/b /t/c", "a, b, c from ", "\x1b[200~/t/a /t/b /t/c \x1b[201~"},
		{"a blank line between two paths", "file:///t/a| |file:///t/b", "a, b from ", "\x1b[200~/t/a /t/b \x1b[201~"},
		{"one path a line names each", "file:///t/a|file:///t/b", "a, b from ", "\x1b[200~/t/a /t/b \x1b[201~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, base, session, left, right := dndBoard(t)
			dir := t.TempDir()
			srcLog, out := filepath.Join(dir, "source.log"), filepath.Join(dir, "pasted")
			runInPane(t, base, session, right, readerPane(out), "SINKREADY")
			runInPane(t, base, session, left, peerSource(bin, srcLog, "DNDPEER_URILIST='"+tc.list+"' "), "DNDPEER-READY")

			toX, toY := right.X+right.Width/2, right.Y+right.Height/2
			dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
			deadline := time.Now().Add(uiTimeout)
			for {
				s := term.Screen().Text()
				if strings.Contains(s, tc.label) {
					if strings.Contains(s, "tuios hit a bug") {
						t.Errorf("the crash overlay is on screen beside the label\n%s", term.Snapshot())
					}
					break
				}
				if time.Now().After(deadline) {
					t.Errorf("the drag label never said %q mid-drag\n%s", tc.label, term.Snapshot())
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			dropAt(t, term, toX, toY)
			pasted := waitFile(t, out, func(s string) bool { return strings.Contains(s, "\x1b[201~") }, "the pane never got the paste")
			if pasted != tc.paste {
				t.Errorf("the pane got %q, want %q", pasted, tc.paste)
			}
			if s := term.Screen().Text(); strings.Contains(s, "tuios hit a bug") {
				t.Errorf("the crash overlay is on screen after the drop\n%s", term.Snapshot())
			}
		})
	}

	t.Run("a line with a space starts the desktop drag", func(t *testing.T) {
		term, base, session, left, _ := dndBoardWith(t, deskConfig)
		srcLog := filepath.Join(t.TempDir(), "source.log")
		waitHostWrote(t, term, osc72("t=q"), "the probe")
		if err := term.Type(osc72("t=q")); err != nil {
			t.Fatal(err)
		}
		waitHostWrote(t, term, "\x1b]72;t=o:x=1", "the registration")
		runInPane(t, base, session, left, peerSource(bin, srcLog, "DNDPEER_URILIST='file:///t/x /t/y' "), "DNDPEER-READY")

		fromX, fromY := left.X+4, left.Y+5
		dndMouse(t, term, "press", tuitest.MousePress, fromX, fromY)
		if err := term.Type(osc72(fmt.Sprintf("t=o:x=%d:y=%d", fromX+2, fromY))); err != nil {
			t.Fatal(err)
		}
		waitHostWrote(t, term, "\x1b]72;t=P", "the start of the desktop drag")
		art := artifactDir(t)
		saveArtifact(t, term, art, "started")
		if err := os.WriteFile(filepath.Join(art, "host-wrote.txt"), []byte(hostWrote(term)), 0o644); err != nil {
			t.Errorf("save host-wrote: %v", err)
		}

		wrote := hostWrote(term)
		var enc strings.Builder
		for msg := range strings.SplitSeq(wrote[:strings.Index(wrote, "\x1b]72;t=P")], "\x1b]72;") {
			msg, _, _ = strings.Cut(msg, "\x1b\\")
			meta, payload, _ := strings.Cut(msg, ";")
			keys := strings.Split(meta, ":")
			if slices.Contains(keys, "t=p") && slices.Contains(keys, "x=-1") {
				enc.WriteString(payload)
			}
		}
		image, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(enc.String(), "="))
		if err != nil || !strings.Contains(string(image), "x, y from ") {
			t.Errorf("the drag image is %q (%v), want it to name x, y from the pane", image, err)
		}
		if s := term.Screen().Text(); strings.Contains(s, "tuios hit a bug") {
			t.Errorf("the crash overlay is on screen\n%s", term.Snapshot())
		}
	})
}

// TestDnDRealPrograms runs yazi as the source and the dnd kitten, then a plain
// reader, as the target. It needs the binaries, named by TUIOS_E2E_YAZI and
// TUIOS_E2E_KITTEN, and skips without them: neither is a dependency of the
// suite. The kitten copies a dropped text/uri-list into its directory, which is
// the artifact: the file exists on the other side, made by the kitten alone.
func TestDnDRealPrograms(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	yazi, kitten := os.Getenv("TUIOS_E2E_YAZI"), os.Getenv("TUIOS_E2E_KITTEN")
	if yazi == "" {
		t.Skip("TUIOS_E2E_YAZI is not set")
	}

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

func buildDnDPeer(t *testing.T) string {
	t.Helper()
	dndPeerOnce.Do(func() {
		dir, err := runScratchDir("dndpeer")
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
	requireReal(t, "perf or external program, env-gated")
	bin := buildDnDPeer(t)

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
		dragAcross(t, first, left.X+4, left.Y+5, right.X+right.Width/2, right.Y+right.Height/2, artifactDir(t))
		dropAt(t, first, right.X+right.Width/2, right.Y+right.Height/2)
		waitFile(t, dstLog, func(s string) bool { return strings.Contains(s, "data x=1") }, "the target never got the drop data")
		waitFile(t, srcLog, func(s string) bool { return strings.Contains(s, "finished") }, "the source never heard the drag finish")
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
	requireReal(t, "perf or external program, env-gated")
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
	requireReal(t, "perf or external program, env-gated")
	bin := buildDnDPeer(t)
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeConfig(t, base, dndConfig+"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n")
	writeConfig(t, remote, dndConfig)
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
	farRow := strings.Split(term.Screen().Text(), "\n")[far.Y+far.Height-1]
	if !strings.Contains(farRow, "build:faraway") {
		near, far = far, near
	}
	enterTerminalMode(t, term)

	dragAcross(t, term, near.X+4, near.Y+5, far.X+far.Width/2, far.Y+far.Height/2, artifactDir(t))
	dropAt(t, term, far.X+far.Width/2, far.Y+far.Height/2)
	local := waitFile(t, localLog, func(s string) bool { return strings.Contains(s, "finished") }, "the local source never heard its drag end")
	if !strings.Contains(local, "finished canceled=1") {
		t.Errorf("a drop onto a pane hosted elsewhere was not cancelled:\n%s", local)
	}
	dragAcross(t, term, far.X+4, far.Y+5, near.X+near.Width/2, near.Y+near.Height/2, artifactDir(t))
	dropAt(t, term, near.X+near.Width/2, near.Y+near.Height/2)
	farOut := waitFile(t, farLog, func(s string) bool { return strings.Contains(s, "mouse") }, "the hosted source never got the press as a mouse report")
	time.Sleep(time.Second)
	b, _ := os.ReadFile(farLog)
	farOut = string(b)
	if strings.Contains(farOut, "recv t=o") || strings.Contains(farOut, "recv t=m") || strings.Contains(farOut, "recv t=M") {
		t.Errorf("a pane hosted elsewhere took part in a drag:\n%s", farOut)
	}
	if n := strings.Count(farOut, "recv t=q"); n != 0 {
		t.Errorf("the hosted program got %d answers to its query, want none:\n%s", n, farOut)
	}
}

// TestDnDRegistrationEndsWithItsProgram: a program killed without taking its
// registration back leaves its pane to whatever runs there next. A reader
// started in that pane gets a drop as a paste, as any pane without the
// protocol does, from the client that saw the program register and from a
// client that attached after it died.
//
// How this could pass wrongly, written down first:
//   - the program could have unregistered after all. It is killed with
//     SIGKILL, which it cannot catch, and the peer only unregisters on q.
//   - the paste could come from the reader being a new program the client
//     somehow told apart. The reader never speaks the protocol; it is the
//     dead peer's group, not the reader, that ends the registration.
//   - the late client could take nothing because the daemon never had the
//     registration. TestDnDRegistrationOutlivesTheClient shows a late client
//     takes it, and the control on the daemon's check fails this subtest.
//
// Negative controls: with dndSweep returning at once, the first subtest
// fails: the reader gets protocol messages and no paste. With the liveness
// check in the daemon's fact() cut, the second fails the same way. See
// NEGATIVE_CONTROLS.md.
func TestDnDRegistrationEndsWithItsProgram(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	bin := buildDnDPeer(t)

	doomed := func(t *testing.T, base, session string, pane winRect, dir string) string {
		t.Helper()
		pidFile := filepath.Join(dir, "target.pid")
		runInPane(t, base, session, pane,
			"sh -c 'echo $$ > "+pidFile+"; stty raw -echo; exec "+bin+" target "+filepath.Join(dir, "target.log")+"'", "DNDPEER-READY")
		waitFile(t, pidFile, func(s string) bool { return strings.TrimSpace(s) != "" }, "the target never wrote its pid")
		return pidFile
	}
	kill := func(t *testing.T, pidFile string) {
		t.Helper()
		b, _ := os.ReadFile(pidFile)
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("pid file holds %q", b)
		}
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Kill()
		}
		if err != nil {
			t.Fatalf("kill the target: %v", err)
		}
	}
	dropOnReader := func(t *testing.T, term *tuitest.Terminal, base, session string, left, right winRect, dir string) string {
		t.Helper()
		out := filepath.Join(dir, "pasted")
		runInPane(t, base, session, right, "stty sane; "+readerPane(out), "SINKREADY")
		toX, toY := right.X+right.Width/2, right.Y+right.Height/2
		dragAcross(t, term, left.X+4, left.Y+5, toX, toY, artifactDir(t))
		dropAt(t, term, toX, toY)
		return waitFile(t, out, func(s string) bool { return strings.Contains(s, "\x1b[201~") || strings.Contains(s, "\x1b]72;") }, "the reader got nothing")
	}

	t.Run("the client that saw it register", func(t *testing.T) {
		term, base, session, left, right := dndBoard(t)
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		if err := os.WriteFile(file, []byte("a"), 0o600); err != nil {
			t.Fatal(err)
		}
		pidFile := doomed(t, base, session, right, dir)
		runInPane(t, base, session, left, peerSource(bin, filepath.Join(dir, "source.log"), "", file), "DNDPEER-READY")
		kill(t, pidFile)
		got := dropOnReader(t, term, base, session, left, right, dir)
		if want := "\x1b[200~" + file + " \x1b[201~"; got != want {
			t.Errorf("the pane the dead program held got %q, want the paste %q", got, want)
		}
	})

	t.Run("a client that attached after it died", func(t *testing.T) {
		_, base, session, left, right := dndBoard(t)
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		if err := os.WriteFile(file, []byte("a"), 0o600); err != nil {
			t.Fatal(err)
		}
		pidFile := doomed(t, base, session, right, dir)
		runInPane(t, base, session, left, peerSource(bin, filepath.Join(dir, "source.log"), "", file), "DNDPEER-READY")
		kill(t, pidFile)
		time.Sleep(300 * time.Millisecond)
		term := dndClient(t, base, nil, "attach", session, "-d")
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
			t.Fatalf("the client never showed the two panes: %v\n%s", err, term.Snapshot())
		}
		windowManagementMode(t, term)
		enterTerminalMode(t, term)
		got := dropOnReader(t, term, base, session, left, right, dir)
		if want := "\x1b[200~" + file + " \x1b[201~"; got != want {
			t.Errorf("the pane the dead program held got %q, want the paste %q", got, want)
		}
	})
}
