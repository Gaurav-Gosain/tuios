package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
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
		time.Sleep(time.Second)
		pid := term.Pid()
		start := sampleIdle(t, pid, statsPath)
		before := wire.n.Load()
		time.Sleep(idleWindow)
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
