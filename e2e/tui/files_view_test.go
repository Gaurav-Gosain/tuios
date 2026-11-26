package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// filesViewFixture is a folder for the Files view: three folders, a Go
// file, a Markdown file, a hidden file, a file in a folder, and a folder of
// sixty files to scroll.
func filesViewFixture(t *testing.T) string {
	t.Helper()
	return filesViewFixtureIn(t, t.TempDir())
}

// filesViewFixtureIn makes the fixture in dir and answers dir.
func filesViewFixtureIn(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"alpha", "zulu", "many"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"main.go":          "package main\n\n// Hello says where it is.\nfunc Hello() string { return \"here\" }\n",
		"notes.md":         "# Notes\n\nThe Files view lists this folder.\n",
		".hidden":          "dot\n",
		"alpha/needle.txt": "found\n",
	}
	for i := range 60 {
		files[fmt.Sprintf("many/f%02d.txt", i)] = "row\n"
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestFilesViewOneSide drives the Files view the way a person does: Ctrl+B f
// opens it on the pane's folder with the finder over it, esc leaves the
// finder for the list, l and h walk a folder in and out, / filters, space
// shows quick look beside the list, . shows hidden files, : goes to a folder
// on a linked host, and I types a path into the pane and goes back to it.
// Frames at 160x44 and 80x24 are the artifact.
//
// Negative control: with handleOpenFileSearch opening the file search alone
// (main), the wait for the view's "this machine" row times out.
func TestFilesViewOneSide(t *testing.T) {
	requireReal(t, "links a second daemon as a host")
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, relayFilesPolicy)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	// Both folders are under the home folder of their machine, so the view
	// writes them with ~ and no frame shows a path of the machine the test
	// runs on.
	top := filepath.Join(remote, "HOME", "out")
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "far.go"), []byte("package far\n\n// Answer is far away.\nconst Answer = 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	local := filesViewFixtureIn(t, filepath.Join(base, "HOME", "project"))
	env := []string{"TUIOS_SSH=" + ssh}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env, cols: 160, rows: 44})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+local+" && clear && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)

	// Ctrl+B f: the view, with the finder over it.
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view with the finder", []string{"Search files", "this machine"}, nil)
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "esc did not leave the finder for the list",
		[]string{"Files", "this machine", "alpha/", "zulu/", "main.go", "notes.md", "Name", "Size"}, []string{"Search files", ".hidden"})
	copiesFrame(t, term, "files-view-160x44")

	// l goes into the folder under the cursor, h comes back with the cursor
	// on it.
	sendKeys(t, term, "l")
	railLists(t, term, "l did not go into alpha", []string{"needle.txt", "1 item"}, []string{"main.go"})
	sendKeys(t, term, "h")
	railLists(t, term, "h did not come back", []string{"main.go", "alpha/"}, []string{"needle.txt"})
	railLists(t, term, "the crumbs did not write the home folder as ~", []string{"~ › project"}, []string{"/HOME"})

	// The mouse: a click selects a row, a double click opens it, a click on
	// a crumb goes to that folder, the wheel scrolls, and a click on a hint
	// runs it.
	// click clicks text below the top bar, where the tab also names the
	// folder.
	click := func(text string) {
		t.Helper()
		s := term.Screen()
		_, rows := s.Size()
		for r := 1; r < rows; r++ {
			if i := strings.Index(s.Line(r), text); i >= 0 {
				mouseClick(t, term, len([]rune(s.Line(r)[:i]))+1, r, tuitest.MouseLeft, 0)
				return
			}
		}
		t.Fatalf("no %q on screen:\n%s", text, term.Snapshot())
	}
	// hint clicks a footer hint: the last row that has its word.
	hint := func(text string) {
		t.Helper()
		s := term.Screen()
		_, rows := s.Size()
		for r := rows - 1; r > 0; r-- {
			if i := strings.Index(s.Line(r), " "+text); i >= 0 {
				mouseClick(t, term, len([]rune(s.Line(r)[:i]))+2, r, tuitest.MouseLeft, 0)
				return
			}
		}
		t.Fatalf("no hint %q on screen:\n%s", text, term.Snapshot())
	}
	click("notes.md")
	sendKeys(t, term, " ")
	railLists(t, term, "a click did not select the row", []string{"The Files view lists this folder."}, nil)
	sendKeys(t, term, " ")
	railLists(t, term, "space did not close quick look", nil, []string{"The Files view lists this folder."})
	col, row, ok := findOnGrid(term.Screen(), "alpha/")
	if !ok {
		t.Fatalf("no alpha/ row:\n%s", term.Snapshot())
	}
	clickAt(t, term, col+1, row, 2)
	railLists(t, term, "a double click did not open the folder", []string{"needle.txt", "~ › project › alpha"}, []string{"main.go"})
	click("project")
	railLists(t, term, "a click on a crumb did not go to its folder", []string{"main.go", "alpha/"}, []string{"needle.txt"})
	col, row, ok = findOnGrid(term.Screen(), "many/")
	if !ok {
		t.Fatalf("no many/ row:\n%s", term.Snapshot())
	}
	clickAt(t, term, col+1, row, 2)
	railLists(t, term, "a double click did not open many", []string{"f00.txt", "60 items"}, nil)
	wheelAt(t, term, 20, 20, tuitest.MouseWheelDown, 10)
	railLists(t, term, "the wheel did not scroll the rows", []string{"f59.txt"}, []string{"f00.txt"})
	wheelAt(t, term, 20, 20, tuitest.MouseWheelUp, 10)
	railLists(t, term, "the wheel did not scroll back", []string{"f00.txt"}, nil)
	click("project")
	railLists(t, term, "a click on a crumb did not go back", []string{"main.go", "many/"}, []string{"f00.txt"})
	hint("hidden")
	railLists(t, term, "a click on the hidden hint did not show hidden files", []string{".hidden", "hidden on"}, nil)
	hint("hidden")
	railLists(t, term, "a second click on the hint did not hide them", []string{"hidden off"}, []string{".hidden"})

	// / filters as it is typed, and space shows quick look beside the list.
	sendKeys(t, term, "/", "mai")
	railLists(t, term, "the filter did not narrow the rows", []string{"main.go", "/ mai"}, []string{"notes.md", "alpha/"})
	sendKeys(t, term, tuitest.Enter)
	railLists(t, term, "enter did not keep the filter", []string{"filter mai"}, []string{"/ mai"})
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show the file beside the list", []string{"main.go", "Hello says where it is."}, nil)
	copiesFrame(t, term, "files-view-look-160x44")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not close quick look", nil, []string{"Hello says where it is."})
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "esc did not clear the filter", []string{"notes.md", "alpha/"}, nil)

	// . shows the hidden files.
	sendKeys(t, term, ".")
	railLists(t, term, ". did not show the hidden file", []string{".hidden", "hidden on"}, nil)
	sendKeys(t, term, ".")

	// : goes to a folder on the linked host.
	sendKeys(t, term, ":", "build:~/out", tuitest.Enter)
	railLists(t, term, ": did not list the folder on build", []string{"build", "linked", "far.go", "~ › out"}, []string{"main.go", "this machine"})
	sendKeys(t, term, "/", "far.go", tuitest.Enter, " ")
	railLists(t, term, "space did not show the far file", []string{"const Answer = 42"}, nil)
	copiesFrame(t, term, "files-view-far-160x44")
	sendKeys(t, term, " ")

	// The narrow form.
	if err := term.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "the view did not draw at 80x24", []string{"far.go", "build"}, nil)
	copiesFrame(t, term, "files-view-80x24")
	if err := term.Resize(160, 44); err != nil {
		t.Fatal(err)
	}

	// I types the path into the pane the view came from, and goes back.
	sendKeys(t, term, ":", "local:~/project", tuitest.Enter)
	railLists(t, term, ": did not come back to this machine", []string{"this machine", "notes.md"}, []string{"far.go"})
	sendKeys(t, term, "/", "notes", tuitest.Enter, "I")
	want := filepath.Join(local, "notes.md")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		// The path wraps in the pane, across its border.
		flat := strings.NewReplacer("\n", "", "│", "", " ", "").Replace(s.Text())
		return !strings.Contains(s.Text(), "this machine") && strings.Contains(flat, want)
	}, uiTimeout); err != nil {
		t.Fatalf("I did not type the path into the pane: %v\n%s", err, term.Snapshot())
	}
	// No frame of this step: the pane shows the whole path of the test's
	// folder.
}

// TestFilesViewOpenAddsNoIdleCost holds the open Files view, with quick look
// beside its list, to the idle budget of the closed one: a folder is read
// when the person goes to it, and nothing runs at rest.
//
// Negative control: with filesReadingMsg asking for the next tick from
// HandleFilesReading, the open window counts a tick every 150 ms and the
// check fails.
func TestFilesViewOpenAddsNoIdleCost(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	base := t.TempDir()
	wire := &wireRecorder{}
	statsPath := filepath.Join(base, "tickstats")
	local := filesViewFixture(t)
	term := startIn(t, base, startOpts{daemonDefault: true, out: wire, env: []string{"TUIOS_STATS_FILE=" + statsPath}, noSettleProbe: true})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+local+" && clear && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	window := func(name string) idleBudget {
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("%s: screen never settled: %v", name, err)
		}
		pause(t, "let the screen's last frame land before the idle window starts", time.Second)
		pid := term.Pid()
		start := sampleIdle(t, pid, statsPath)
		before := wire.n.Load()
		pause(t, "the idle window: count what the client does while nothing happens", idleWindow)
		end := sampleIdle(t, pid, statsPath)
		got := idleBudget{
			wire:        wire.n.Load() - before,
			ticks:       end.stats["ticks"] - start.stats["ticks"],
			work:        end.stats["work"] - start.stats["work"],
			tickerWakes: end.stats["tickerWakes"] - start.stats["tickerWakes"],
			ctxsw:       end.ctxsw - start.ctxsw,
		}
		t.Logf("%s, idle window %v: wire=%dB ticks=%d work=%d tickerWakes=%d ctxsw=%d",
			name, idleWindow, got.wire, got.ticks, got.work, got.tickerWakes, got.ctxsw)
		return got
	}
	closed := window("Files view closed")
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view", []string{"Search files", "this machine"}, nil)
	sendKeys(t, term, tuitest.Esc, "/", "main", tuitest.Enter, " ")
	railLists(t, term, "quick look did not open beside the list", []string{"main.go", "Hello says where it is."}, []string{"Search files"})
	open := window("Files view open, with quick look")
	var over []string
	if open.wire > closed.wire+idleWireBudget/4 {
		over = append(over, fmt.Sprintf("wrote %d bytes, %d with it closed", open.wire, closed.wire))
	}
	if open.ticks > closed.ticks+2 || open.work > closed.work+2 {
		over = append(over, fmt.Sprintf("ran %d ticks (%d full), %d (%d) with it closed", open.ticks, open.work, closed.ticks, closed.work))
	}
	if open.tickerWakes > closed.tickerWakes+1 {
		over = append(over, fmt.Sprintf("woke the frame ticker %d times, %d with it closed", open.tickerWakes, closed.tickerWakes))
	}
	if closed.ctxsw >= 0 && open.ctxsw > closed.ctxsw*3/2+200 {
		over = append(over, fmt.Sprintf("made %d context switches, %d with it closed", open.ctxsw, closed.ctxsw))
	}
	if open.ticks > 2 || open.tickerWakes > 1 || open.wire > idleWireBudget {
		over = append(over, fmt.Sprintf("open: %d ticks, %d ticker wakes, %d bytes", open.ticks, open.tickerWakes, open.wire))
	}
	if len(over) > 0 {
		t.Fatalf("the open Files view costs more at idle than the closed one:\n  %s\n%s", strings.Join(over, "\n  "), term.Snapshot())
	}
}

// filesDividerCol is the column of the Files view's divider on screen row
// row, or -1.
func filesDividerCol(s tuitest.Screen, row int) int {
	for i, r := range []rune(s.Line(row)) {
		if r == '│' {
			return i
		}
	}
	return -1
}

// TestFilesViewTwoSides puts this machine and a linked host side by side:
// \ opens the second side, : takes it to build, tab moves the keys, quick
// look takes the other side's place, < and > and a drag move the divider,
// Z shows one side, + [ ] and a click switch tabs, and ctrl+w closes one.
// Frames at 160, 120 and 80 columns are the artifact; at 80 one side is
// drawn at a time and tab flips between them.
//
// Negative control: with FilesTwoSides leaving two off, the wait for the
// second side's machine row times out.
func TestFilesViewTwoSides(t *testing.T) {
	requireReal(t, "links a second daemon as a host")
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, relayFilesPolicy)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	top := filepath.Join(remote, "HOME", "out")
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "far.go"), []byte("package far\n\nconst Answer = 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	filesViewFixtureIn(t, filepath.Join(base, "HOME", "project"))
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: []string{"TUIOS_SSH=" + ssh}, cols: 160, rows: 44})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/project && clear && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view", []string{"Search files"}, nil)
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "the view did not list the folder", []string{"main.go", "this machine"}, []string{"Search files"})

	// \ opens the second side on the same folder, and : takes it to build.
	sendKeys(t, term, "\\")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "this machine") == 2 && filesDividerCol(s, 4) > 0
	}, uiTimeout); err != nil {
		t.Fatalf("\\ did not open a second side: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, ":", "build:~/out", tuitest.Enter)
	railLists(t, term, "the second side did not go to build", []string{"far.go", "build", "linked", "this machine", "main.go", "←→"}, nil)
	copiesFrame(t, term, "files-two-160x44")

	// tab gives the keys to the left side: space shows its file in the right
	// side's place.
	sendKeys(t, term, tuitest.Tab, "/", "main", tuitest.Enter, " ")
	railLists(t, term, "quick look did not take the other side's place", []string{"Hello says where it is.", "main.go"}, []string{"far.go"})
	copiesFrame(t, term, "files-two-look-160x44")
	sendKeys(t, term, " ", tuitest.Esc)
	railLists(t, term, "space did not give the side back", []string{"far.go", "notes.md"}, []string{"Hello says where it is."})

	// A click in a side gives it the keys: space then shows its file.
	click := func(text string) {
		t.Helper()
		s := term.Screen()
		_, rows := s.Size()
		for r := 1; r < rows; r++ {
			if i := strings.Index(s.Line(r), text); i >= 0 {
				mouseClick(t, term, len([]rune(s.Line(r)[:i]))+1, r, tuitest.MouseLeft, 0)
				return
			}
		}
		t.Fatalf("no %q on screen:\n%s", text, term.Snapshot())
	}
	click("far.go")
	sendKeys(t, term, " ")
	railLists(t, term, "a click did not give the keys to the far side", []string{"const Answer = 42"}, nil)
	sendKeys(t, term, " ")
	railLists(t, term, "space did not close the far file", []string{"main.go", "far.go"}, []string{"const Answer = 42"})

	// < moves the divider, a drag moves it, a double click puts it back.
	half := filesDividerCol(term.Screen(), 4)
	sendKeys(t, term, "<", "<")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) < half-8 }, uiTimeout); err != nil {
		t.Fatalf("< did not move the divider left of %d: %v\n%s", half, err, term.Snapshot())
	}
	at := filesDividerCol(term.Screen(), 4)
	mouseDrag(t, term, at, 10, at+40, 10, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) > half+20 }, uiTimeout); err != nil {
		t.Fatalf("a drag did not move the divider right of %d: %v\n%s", half+20, err, term.Snapshot())
	}
	at = filesDividerCol(term.Screen(), 4)
	clickAt(t, term, at, 10, 2)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) == half }, uiTimeout); err != nil {
		t.Fatalf("a double click did not put the divider back at %d: %v\n%s", half, err, term.Snapshot())
	}

	// Z shows the side with the keys alone, and both again.
	sendKeys(t, term, "Z")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) < 0 }, uiTimeout); err != nil {
		t.Fatalf("Z did not show one side: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "Z")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) == half }, uiTimeout); err != nil {
		t.Fatalf("Z did not show both sides again: %v\n%s", err, term.Snapshot())
	}

	// Tabs: + opens one, [ goes back, a click on the top bar switches, and
	// ctrl+w closes it.
	sendKeys(t, term, "+")
	railLists(t, term, "+ did not open a second tab", []string{" 2  build ~/out "}, nil)
	sendKeys(t, term, "[")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) > 0 }, uiTimeout); err != nil {
		t.Fatalf("[ did not go back to the first tab: %v\n%s", err, term.Snapshot())
	}
	col, _, ok := findOnGrid(term.Screen(), " 2  build")
	if !ok {
		t.Fatalf("no second tab on the top bar:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+2, 0, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return filesDividerCol(s, 4) < 0 && strings.Contains(s.Text(), "far.go") }, uiTimeout); err != nil {
		t.Fatalf("a click on the second tab did not go there: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('w'))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return filesDividerCol(s, 4) > 0 && !strings.Contains(s.Text(), " 2  build")
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+w did not close the second tab: %v\n%s", err, term.Snapshot())
	}

	// 120 columns: still side by side. 80: one side at a time.
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return filesDividerCol(s, 4) > 0 && containsAll(s.Text(), "far.go", "main.go")
	}, uiTimeout); err != nil {
		t.Fatalf("the two sides did not draw at 120 columns: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-two-120x40")
	if err := term.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return filesDividerCol(s, 4) < 0 && strings.Contains(s.Text(), "far.go") && !strings.Contains(s.Text(), "main.go")
	}, uiTimeout); err != nil {
		t.Fatalf("80 columns did not draw one side: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-two-80x24")
	sendKeys(t, term, tuitest.Tab)
	railLists(t, term, "tab did not flip to the other side at 80 columns", []string{"main.go", "this machine"}, []string{"far.go"})
	copiesFrame(t, term, "files-two-80x24-flipped")
	// A click on the other machine's name in the tab flips back.
	col, _, ok = findOnGrid(term.Screen(), "build")
	if !ok {
		t.Fatalf("no build in the tab:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col, 0, tuitest.MouseLeft, 0)
	railLists(t, term, "a click on the tab's other name did not flip", []string{"far.go", "linked"}, []string{"main.go"})
}

// TestFilesViewCopies copies three marked files from this machine to a
// linked host, with a rate limit so a frame catches them on their way, and
// moves a fourth. The copies show in the far side before they land and in
// the Copies list, and each far file has the sha256 of its source. A move
// takes its source away only after the copy is checked. On this machine, a
// rename in the row, a new folder and the trash each come back with u.
//
// Negative control: with filesArrivals answering nil, the wait for the
// arriving rows times out.
func TestFilesViewCopies(t *testing.T) {
	requireReal(t, "links a second daemon as a host")
	base := t.TempDir()
	remote := remoteMachine(t)
	// build lets this machine write in its home folder, so a copy can land
	// in ~/out.
	writeRemoteConfig(t, remote, relayFilesPolicy+`files_roots = ["~"]`+"\n")
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	far := filepath.Join(remote, "HOME", "out")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	local := filesViewFixtureIn(t, filepath.Join(base, "HOME", "project"))
	big := filepath.Join(local, "big")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 2<<20)
	for i, name := range []string{"part1.bin", "part2.bin", "part3.bin", "whole.bin"} {
		for j := range blob {
			blob[j] = byte(i*31 + j*7)
		}
		if err := os.WriteFile(filepath.Join(big, name), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"TUIOS_SSH=" + ssh, "TUIOS_TEST_KNOBS=files_rate=131072"}
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env, cols: 160, rows: 44})
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
	railLists(t, term, "Ctrl+B f did not open the view", []string{"Search files"}, nil)
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "the view did not list big", []string{"part1.bin", "whole.bin"}, []string{"Search files"})
	sendKeys(t, term, "\\", ":", "build:~/out", tuitest.Enter)
	railLists(t, term, "the second side did not go to build", []string{"build", "linked", "part1.bin"}, nil)
	sendKeys(t, term, tuitest.Tab)

	// x marks three rows; the footer counts them.
	sendKeys(t, term, "x", "x", "x")
	railLists(t, term, "x did not mark three rows", []string{"3 marked", "6 MiB"}, nil)
	copiesFrame(t, term, "files-copy-marked")

	// c copies them to build. They show in build's side before they land.
	sendKeys(t, term, "c")
	moving := regexp.MustCompile(` [1-9][0-9]?%`)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Copies", "1 running", "~/project/big", "~/out", "left") && moving.MatchString(s.Text()) && strings.Count(s.Text(), "part2.bin") >= 3
	}, 30*time.Second); err != nil {
		t.Fatalf("the copies did not show on their way as one job: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-copy-mid")
	// The job's pause and go on are clickable.
	clickWord := func(word string) {
		t.Helper()
		s := term.Screen()
		_, rows := s.Size()
		for r := rows - 1; r > 0; r-- {
			if i := strings.LastIndex(s.Line(r), word); i >= 0 {
				mouseClick(t, term, len([]rune(s.Line(r)[:i]))+1, r, tuitest.MouseLeft, 0)
				return
			}
		}
		t.Fatalf("no %q on screen:\n%s", word, term.Snapshot())
	}
	clickWord("pause")
	railLists(t, term, "a click on pause did not pause the job", []string{"1 paused", "go on"}, []string{"1 running"})
	copiesFrame(t, term, "files-copy-paused")
	clickWord("go on")
	railLists(t, term, "a click on go on did not go on", []string{"1 running"}, []string{"1 paused"})
	for _, name := range []string{"part1.bin", "part2.bin", "part3.bin"} {
		filesWaitDisk(t, 120*time.Second, "the copy of "+name+" to land", func() bool {
			_, err := os.Stat(filepath.Join(far, name))
			return err == nil
		})
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "2 MiB") >= 7 && !strings.Contains(s.Text(), "%")
	}, 120*time.Second); err != nil {
		t.Fatalf("the far side never listed the three files: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "files-copy-done")
	// One notice for the job, not one per file.
	railLists(t, term, "the job did not tell its end once", []string{"Copied 3 files to build."}, []string{"Copied part1.bin"})
	for _, name := range []string{"part1.bin", "part2.bin", "part3.bin"} {
		if a, b := fileSHA(t, filepath.Join(big, name)), fileSHA(t, filepath.Join(far, name)); a != b {
			t.Fatalf("%s on build has sha256 %s, its source %s", name, b, a)
		}
	}

	// m moves whole.bin: the source stays while the copy runs, and goes once
	// the copy is checked.
	sendKeys(t, term, "/", "whole", tuitest.Enter, "m")
	filesWaitDisk(t, 30*time.Second, "the move to start", func() bool {
		return strings.Contains(term.Screen().Text(), "%")
	})
	if _, err := os.Stat(filepath.Join(big, "whole.bin")); err != nil {
		t.Fatalf("the move took the source away before the copy ended: %v", err)
	}
	filesWaitDisk(t, 120*time.Second, "the move to end", func() bool {
		_, errSrc := os.Stat(filepath.Join(big, "whole.bin"))
		_, errDst := os.Stat(filepath.Join(far, "whole.bin"))
		return os.IsNotExist(errSrc) && errDst == nil
	})
	sendKeys(t, term, tuitest.Esc)

	// On this machine: a rename in the row, undone; a new folder, undone; a
	// file to the trash, brought back.
	sendKeys(t, term, "/", "part1", tuitest.Enter, "r")
	for range len("part1.bin") {
		sendKeys(t, term, tuitest.Backspace)
	}
	sendKeys(t, term, "renamed.bin", tuitest.Enter)
	filesWaitDisk(t, uiTimeout, "the rename", func() bool {
		_, err := os.Stat(filepath.Join(big, "renamed.bin"))
		return err == nil
	})
	sendKeys(t, term, tuitest.Esc, "u")
	filesWaitDisk(t, uiTimeout, "the undo of the rename", func() bool {
		_, errOld := os.Stat(filepath.Join(big, "part1.bin"))
		_, errNew := os.Stat(filepath.Join(big, "renamed.bin"))
		return errOld == nil && os.IsNotExist(errNew)
	})
	sendKeys(t, term, "a", "fresh/", tuitest.Enter)
	filesWaitDisk(t, uiTimeout, "the new folder", func() bool {
		fi, err := os.Stat(filepath.Join(big, "fresh"))
		return err == nil && fi.IsDir()
	})
	sendKeys(t, term, "u")
	filesWaitDisk(t, uiTimeout, "the undo of the new folder", func() bool {
		_, err := os.Stat(filepath.Join(big, "fresh"))
		return os.IsNotExist(err)
	})
	sendKeys(t, term, "/", "part2", tuitest.Enter, "d")
	filesWaitDisk(t, uiTimeout, "the trash", func() bool {
		_, err := os.Stat(filepath.Join(big, "part2.bin"))
		return os.IsNotExist(err)
	})
	sendKeys(t, term, tuitest.Esc, "u")
	filesWaitDisk(t, uiTimeout, "the undo of the trash", func() bool {
		_, err := os.Stat(filepath.Join(big, "part2.bin"))
		return err == nil
	})
	if a, b := fileSHA(t, filepath.Join(big, "part2.bin")), fileSHA(t, filepath.Join(far, "part2.bin")); a != b {
		t.Fatalf("part2.bin came back from the trash with other bytes")
	}
}

// filesWaitDisk waits for cond, a fact of a disk or of the screen, polled
// every 50 ms, up to within.
func filesWaitDisk(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	waitUntil(t, what, within, func() (bool, string) { return cond(), "" })
}
