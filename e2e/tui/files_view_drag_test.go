package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestFilesViewDragAndDrop drags rows of the Files view with the mouse, the
// way a person does, between this machine and a linked host:
//
//   - three marked files onto a folder row on build: the label says the
//     copy, the folder opens when it is held, and the release copies the
//     files there. transfer-list shows the copies, into that folder, not
//     moves;
//   - a file that is there already: the label says it has the same bytes,
//     and esc puts it down;
//   - a file with alt: a move between machines;
//   - a file onto a crumb: a move on this machine;
//   - a folder onto itself: refused, in the warning colour;
//   - a file onto the pane on the drop shelf: the pane has its path.
//
// Frames of the label, the check, the refusal and the shelf are the
// artifact.
//
// Negative control: with filesArmDrag cut from FilesClick, the wait for
// the first label times out.
func TestFilesViewDragAndDrop(t *testing.T) {
	requireReal(t, "links a second daemon as a host")
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, relayFilesPolicy+`files_roots = ["~"]`+"\n")
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	far := filepath.Join(remote, "HOME", "out", "inbox")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	project := filepath.Join(base, "HOME", "project")
	big := filepath.Join(project, "big")
	if err := os.MkdirAll(filepath.Join(big, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 1<<20)
	for i, name := range []string{"part1.bin", "part2.bin", "part3.bin", "whole.bin"} {
		for j := range blob {
			blob[j] = byte(i*31 + j*7)
		}
		if err := os.WriteFile(filepath.Join(big, name), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: []string{"TUIOS_SSH=" + ssh}, cols: 160, rows: 44})
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := tuiosCLI(t, base, "transfers")
			t.Logf("copies at the end:\n%s\nscreen:\n%s", out, term.Snapshot())
		}
	})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/project/big && clear && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view on big", []string{"part1.bin", "whole.bin"}, []string{"Search files"})
	sendKeys(t, term, "\\", ":", "build:~/out", tuitest.Enter)
	railLists(t, term, "the second side did not go to build", []string{"build", "linked", "inbox"}, nil)
	sendKeys(t, term, tuitest.Tab)

	// at finds text on the screen, on the left side or the right one, and
	// answers the cell of its first letter.
	at := func(text string, right bool) (int, int) {
		t.Helper()
		s := term.Screen()
		_, rows := s.Size()
		for r := range rows {
			div := filesDividerCol(s, r)
			line := []rune(s.Line(r))
			for i := 0; i+len([]rune(text)) <= len(line); i++ {
				if string(line[i:i+len([]rune(text))]) != text {
					continue
				}
				if div < 0 || (i > div) == right {
					return i, r
				}
			}
		}
		t.Fatalf("no %q on the screen:\n%s", text, term.Snapshot())
		return 0, 0
	}
	// carry presses at the first cell, moves to the second cell by cell,
	// and waits for the label.
	carry := func(fromX, fromY, toX, toY int, mods tuitest.KeyMods, label string) {
		t.Helper()
		mousePress(t, term, fromX, fromY, tuitest.MouseLeft, mods)
		steps := max(abs(toX-fromX), abs(toY-fromY))
		for i := 1; i <= steps; i++ {
			mouseMotion(t, term, fromX+(toX-fromX)*i/steps, fromY+(toY-fromY)*i/steps, tuitest.MouseLeft, mods)
		}
		if err := term.WaitForText(label, uiTimeout); err != nil {
			t.Fatalf("the label never said %q: %v\n%s", label, err, term.Snapshot())
		}
	}
	// below is a cell of a side's footer, which is the side's own folder.
	below := func(right bool) (int, int) {
		x, _ := at("Name", right)
		_, y := at(" item", right)
		return x + 1, y
	}

	// Three marked files onto inbox on build: a copy. Held, inbox opens,
	// and the label names it.
	sendKeys(t, term, "j", "x", "x", "x")
	railLists(t, term, "x did not mark three rows", []string{"3 marked"}, nil)
	px, py := at("part1.bin", false)
	ix, iy := at("inbox", true)
	carry(px+2, py, ix+2, iy, 0, "Copy 3 items")
	if err := term.WaitForText("out › inbox", uiTimeout); err != nil {
		t.Fatalf("inbox did not open while it was held: %v\n%s", err, term.Snapshot())
	}
	mouseMotion(t, term, ix+3, iy, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Copy 3 items (3 MiB) into build ~/out/inbox.", uiTimeout); err != nil {
		t.Fatalf("the label does not name inbox: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-drag-label")
	mouseRelease(t, term, ix+3, iy, tuitest.MouseLeft, 0)
	for _, name := range []string{"part1.bin", "part2.bin", "part3.bin"} {
		filesWaitDisk(t, 60*time.Second, "the copy of "+name+" to land in inbox", func() bool {
			_, err := os.Stat(filepath.Join(far, name))
			return err == nil
		})
		if _, err := os.Stat(filepath.Join(big, name)); err != nil {
			t.Fatalf("ASSERTION: a drag between machines took %s away: %v", name, err)
		}
	}
	var list struct {
		Transfers []struct {
			Dst struct {
				Host string `json:"host"`
				Path string `json:"path"`
			} `json:"dst"`
			Move bool `json:"move"`
		} `json:"transfers"`
	}
	raw := dialFileVerbs(t, base).mustRaw("transfer-list", map[string]any{})
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("transfer-list: %v\n%s", err, raw)
	}
	copies := 0
	for _, tr := range list.Transfers {
		if tr.Dst.Host == "build" && strings.Contains(tr.Dst.Path, "out/inbox/part") {
			copies++
			if tr.Move {
				t.Errorf("ASSERTION: the drag to another machine is a move: %s", raw)
			}
		}
	}
	if copies != 3 {
		t.Fatalf("ASSERTION: transfer-list has %d copies into build ~/out/inbox, want 3:\n%s", copies, raw)
	}
	saveTransferArtifact(t, "files-drag-transfers", json.RawMessage(raw))

	// part1.bin is in inbox now: resting on the folder, the label says so.
	// esc puts it down, and nothing is copied.
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "marked") }, uiTimeout); err != nil {
		t.Fatalf("the drop left the marks: %v\n%s", err, term.Snapshot())
	}
	px, py = at("part1.bin", false)
	bx, by := below(true)
	carry(px+2, py, bx, by, 0, "1 is there with the same bytes: left as is.")
	copiesFrame(t, term, "files-drag-check")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Copy part1.bin") }, uiTimeout); err != nil {
		t.Fatalf("esc did not put the file down: %v\n%s", err, term.Snapshot())
	}
	mouseRelease(t, term, bx, by, tuitest.MouseLeft, 0)

	// whole.bin with alt: a move between machines.
	wx, wy := at("whole.bin", false)
	carry(wx+2, wy, bx, by, tuitest.ModAlt, "Move whole.bin (1 MiB) into build ~/out/inbox.")
	mouseRelease(t, term, bx, by, tuitest.MouseLeft, tuitest.ModAlt)
	filesWaitDisk(t, 60*time.Second, "the move of whole.bin", func() bool {
		_, errSrc := os.Stat(filepath.Join(big, "whole.bin"))
		_, errDst := os.Stat(filepath.Join(far, "whole.bin"))
		return os.IsNotExist(errSrc) && errDst == nil
	})

	// part3.bin onto the crumb project: a move on this machine.
	cx, cy := at("project", false)
	px, py = at("part3.bin", false)
	carry(px+2, py, cx+1, cy, 0, "into")
	if err := term.WaitForText("~/project.", uiTimeout); err != nil {
		t.Fatalf("the label does not name ~/project: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Move part3.bin") {
		t.Fatalf("ASSERTION: a drag on one machine is not a move:\n%s", term.Snapshot())
	}
	mouseRelease(t, term, cx+1, cy, tuitest.MouseLeft, 0)
	filesWaitDisk(t, uiTimeout, "the move of part3.bin into project", func() bool {
		_, errSrc := os.Stat(filepath.Join(big, "part3.bin"))
		_, errDst := os.Stat(filepath.Join(project, "part3.bin"))
		return os.IsNotExist(errSrc) && errDst == nil
	})

	// sub onto itself: refused, and it stays.
	sx, sy := at("sub", false)
	mousePress(t, term, sx+1, sy, tuitest.MouseLeft, 0)
	mouseMotion(t, term, sx+4, sy, tuitest.MouseLeft, 0)
	mouseMotion(t, term, sx+6, sy, tuitest.MouseLeft, 0)
	if err := term.WaitForText("A folder cannot go into itself.", uiTimeout); err != nil {
		t.Fatalf("the label did not refuse a folder onto itself: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-drag-refused")
	mouseRelease(t, term, sx+6, sy, tuitest.MouseLeft, 0)
	if fi, err := os.Stat(filepath.Join(big, "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("ASSERTION: the refused drop moved sub: %v", err)
	}

	// part2.bin onto the pane on the drop shelf: the pane has the path.
	px, py = at("part2.bin", false)
	mousePress(t, term, px+2, py, tuitest.MouseLeft, 0)
	mouseMotion(t, term, px+4, py+1, tuitest.MouseLeft, 0)
	mouseMotion(t, term, px+6, py+2, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Drop on a pane:", uiTimeout); err != nil {
		t.Fatalf("the drop shelf did not show: %v\n%s", err, term.Snapshot())
	}
	hx, hy := at("Drop on a pane:", false)
	tx := hx + len("Drop on a pane:") + 3
	for y := py + 2; y <= hy; y++ {
		mouseMotion(t, term, tx, y, tuitest.MouseLeft, 0)
	}
	if err := term.WaitForText("part2.bin (1 MiB) → ", uiTimeout); err != nil {
		t.Fatalf("the label does not name the pane: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-drag-shelf")
	mouseRelease(t, term, tx, hy, tuitest.MouseLeft, 0)
	if err := term.WaitForText("big/part2.bin", uiTimeout); err != nil {
		t.Fatalf("the pane did not get the path: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-drag-pane")
}
