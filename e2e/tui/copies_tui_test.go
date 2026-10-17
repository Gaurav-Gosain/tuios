package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// The copies between machines in the TUI: the dock item, the mark on the
// machine's rail row, the Copies panel, the question a file that is there
// asks, the send from the rail's files list, and a dropped file on a pane on
// another machine.
//
// Each test runs a real client attached to a real hub daemon, linked to a
// second daemon (build) over the ssh stand-in, and the copies run in the hub
// as they do for a person. What would pass a weaker test and fail these:
//   - A dock item drawn from a stale listing: the bar has to move while the
//     bytes move, and leave after the copy ends.
//   - An item drawn when no copy exists: the frame before the first copy is
//     read for the bar and the panel's rows.
//   - A pause that only changed the row's word: the daemon's byte count is
//     read twice while paused.
//   - A keep-both answer sent as replace: the file there is read back, and
//     the copy has to be next to it under "name (from HOST).ext".
//   - A drop that reached a local pane: the same paste into a pane on this
//     machine has to arrive as the text it was.

// dockBarRe is the dock's bar for one copy: six cells and a percent. The
// panel's bar is ten cells, so it never matches.
var dockBarRe = regexp.MustCompile(`\[[#-]{6}\] +(\d+)%`)

// railMarkRe is the mark on a machine's rail row.
var railMarkRe = regexp.MustCompile(`(→|->) (\d+)%`)

// copiesMachines starts build's daemon and a client on this machine attached
// to a new session, whose daemon links to build through a stand-in ssh that
// passes build's bytes at rate. Build lets a link write in its own root.
func copiesMachines(t *testing.T, rate int, env ...string) (base, remote string, term *tuitest.Terminal) {
	t.Helper()
	return copiesMachinesConf(t, rate, "", env...)
}

// copiesMachinesConf is copiesMachines with more lines in this machine's
// config.toml, such as keys bound again.
func copiesMachinesConf(t *testing.T, rate int, conf string, env ...string) (base, remote string, term *tuitest.Terminal) {
	t.Helper()
	base = t.TempDir()
	remote = remoteMachine(t)
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nfiles_roots = [\"~\", \""+remote+"\"]\n")
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	ssh := writeSlowFakeSSH(t, base, remote, rate, 1<<20)
	writeOneHostConfig(t, base, tuiosBin)
	if conf != "" {
		path := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("\n" + conf); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	env = append(env, "TUIOS_SSH="+ssh)
	term = startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")
	return base, remote, term
}

// copiesFrame saves the screen as a PNG and as text under TUIOS_E2E_FRAMES.
func copiesFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, name)
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		savePNG(t, term.Screen(), shot.XTermPalette(), dir, name)
	}
}

// openCopies opens the Copies panel with the prefix chord.
func openCopies(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "u"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Copies", uiTimeout); err != nil {
		t.Fatalf("prefix u did not open the Copies panel: %v\n%s", err, term.Snapshot())
	}
}

// dockPercent is the percent the dock's bar shows, or -1.
func dockPercent(s tuitest.Screen) int {
	m := dockBarRe.FindStringSubmatch(s.Text())
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// startHubCopy starts a copy in the hub's daemon and returns its id.
func startHubCopy(t *testing.T, base string, params map[string]any) string {
	t.Helper()
	var row transferRow
	dialFileVerbs(t, base).must("transfer-start", params, &row)
	if row.ID == "" {
		t.Fatalf("transfer-start named no copy")
	}
	return row.ID
}

// waitTransferState polls the hub until the copy is in state.
func waitTransferState(t *testing.T, base, id, state string, timeout time.Duration) transferRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r := transferNow(t, base, id)
		if r.State == state {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy never reached %s: %+v", state, r)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestCopiesShowInTheDockRailAndPanel follows one copy from start to end: no
// element before it, a bar that moves in the dock and a mark on build's rail
// row while it runs, the panel's row, a pause that stops the bytes, a cut
// link that the dock says is waiting, the end checked, and the dock item
// leaving after it.
func TestCopiesShowInTheDockRailAndPanel(t *testing.T) {
	base, remote, term := copiesMachines(t, 64<<20)

	// Nothing before the first copy.
	if s := term.Screen().Text(); dockBarRe.MatchString(s) || railMarkRe.MatchString(s) || strings.Contains(s, "copies") {
		t.Fatalf("ASSERTION: a copy element is drawn while no copy exists:\n%s", term.Snapshot())
	}
	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText("build", uiTimeout); err != nil {
		t.Fatalf("the rail does not list build: %v\n%s", err, term.Snapshot())
	}
	if s := term.Screen().Text(); railMarkRe.MatchString(s) || dockBarRe.MatchString(s) {
		t.Fatalf("ASSERTION: the rail or the dock shows a copy while none exists:\n%s", term.Snapshot())
	}
	copiesFrame(t, term, "copies-none")
	openCopies(t, term)
	if err := term.WaitForText("No copies.", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the empty panel does not say there are no copies: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-panel-empty")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(base, "big.bin")
	want := randomFile(t, src, 12<<20)
	dstDir := filepath.Join(remote, "in")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dstDir, "big.bin")
	id := startHubCopy(t, base, map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": dst},
		"rate_limit": 1 << 20,
	})

	// The bar moves.
	first := -1
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		first = dockPercent(s)
		return first >= 0 && first < 90
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock shows no bar for a running copy: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		p := dockPercent(s)
		return p > first && railMarkRe.MatchString(s.Text())
	}, 3*uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock bar did not move from %d%%, or build's rail row has no mark: %v\n%s", first, err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-dock-running")

	// The panel lists it, and p pauses the bytes.
	openCopies(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "big.bin", "this machine -> build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the panel does not list the copy: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-panel-running")
	if err := term.SendKeys("p"); err != nil {
		t.Fatal(err)
	}
	waitTransferState(t, base, id, "paused", uiTimeout)
	if err := term.WaitForText("paused", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the panel row does not say paused: %v\n%s", err, term.Snapshot())
	}
	a := transferNow(t, base, id).Done
	time.Sleep(1500 * time.Millisecond)
	if b := transferNow(t, base, id).Done; b != a {
		t.Fatalf("ASSERTION: a paused copy moved from %d to %d bytes", a, b)
	}
	copiesFrame(t, term, "copies-panel-paused")
	if err := term.SendKeys("p"); err != nil {
		t.Fatal(err)
	}
	waitTransferState(t, base, id, "running", uiTimeout)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	// A cut link: the dock says waiting.
	if n := cutSlowLink(t, base); n == 0 {
		t.Fatalf("no link to cut")
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "waiting")
	}, 3*uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock does not say the copy is waiting: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-dock-waiting")

	// It ends checked, and the dock says so.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Copied big.bin to build. Checked.")
	}, 90*time.Second); err != nil {
		t.Fatalf("ASSERTION: no notice that the copy ended checked: %v\n%s", err, term.Snapshot())
	}
	r := transferNow(t, base, id)
	if r.State != "done" || !r.Verified || fileSHA(t, dst) != want {
		t.Fatalf("ASSERTION: the copy did not end checked with the file's bytes: %+v", r)
	}
	copiesFrame(t, term, "copies-dock-done")

	// Once the notice is gone the item says done, and then it and the mark
	// leave on their own. The notice holds the dock's right end while it is
	// up, so the item is read after it.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "] done")
	}, 15*time.Second); err != nil {
		t.Fatalf("ASSERTION: the dock item does not say done after the copy ended: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-dock-lingers")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return !dockBarRe.MatchString(txt) && !railMarkRe.MatchString(txt) && !strings.Contains(txt, "] done") &&
			!strings.Contains(txt, "Copied big.bin")
	}, 25*time.Second); err != nil {
		t.Fatalf("ASSERTION: the dock item did not leave after the copy ended: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-after")
	alive(t, term, "after a copy")
}

// TestCopiesConflictKeepBoth answers a copy that waits in state conflict from
// the panel: the question names the file and the machine, and Keep both puts
// the copy beside the file there as "report (from HOST).txt".
func TestCopiesConflictKeepBoth(t *testing.T) {
	base, remote, term := copiesMachines(t, 64<<20)

	src := filepath.Join(base, "report.txt")
	if err := os.WriteFile(src, []byte("the new report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(remote, "docs")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	there := filepath.Join(dstDir, "report.txt")
	if err := os.WriteFile(there, []byte("the report that was there first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := startHubCopy(t, base, map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": there},
		"conflict": "ask",
	})
	waitTransferState(t, base, id, "conflict", uiTimeout)
	if err := term.WaitForText("1 copy asks", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock does not say a copy asks: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-dock-asks")
	openCopies(t, term)
	if err := term.WaitForText("waits for an answer", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the panel row does not say the copy waits: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "report.txt is there on build.", "Keep both", "Replace", "Skip")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: enter did not open the question: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-conflict-dialog")
	if err := term.SendKeys(tuitest.Down, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitTransferState(t, base, id, "done", 30*time.Second)
	if got, _ := os.ReadFile(there); string(got) != "the report that was there first\n" {
		t.Fatalf("ASSERTION: keep both changed the file there: %q", got)
	}
	matches, _ := filepath.Glob(filepath.Join(dstDir, "report (from *).txt"))
	if len(matches) != 1 {
		entries, _ := os.ReadDir(dstDir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("ASSERTION: keep both made no \"report (from HOST).txt\": %v", names)
	}
	if got, _ := os.ReadFile(matches[0]); string(got) != "the new report\n" {
		t.Fatalf("ASSERTION: %s holds %q", matches[0], got)
	}
	alive(t, term, "after answering a copy")
}

// TestCopiesReadAtLowColour draws a paused copy in the dock and the panel on
// a 16 colour and a 256 colour terminal. The bar is glyphs, so its text has
// to be on the screen at both depths.
func TestCopiesReadAtLowColour(t *testing.T) {
	for _, depth := range chromeDepths[:2] {
		t.Run(depth.name, func(t *testing.T) {
			base, remote, term := copiesMachines(t, 64<<20, depth.env...)
			src := filepath.Join(base, "slow.bin")
			randomFile(t, src, 8<<20)
			id := startHubCopy(t, base, map[string]any{
				"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": filepath.Join(remote, "slow.bin")},
				"rate_limit": 256 << 10,
			})
			// Far enough that the bar has filled cells: with no colour, the
			// filled cells are what says how far the copy is.
			if err := term.WaitFor(func(s tuitest.Screen) bool { return dockPercent(s) >= 20 }, 3*uiTimeout); err != nil {
				t.Fatalf("ASSERTION: no dock bar at %s colours: %v\n%s", depth.name, err, term.Snapshot())
			}
			dialFileVerbs(t, base).must("transfer-pause", map[string]any{"id": id}, nil)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return regexp.MustCompile(`\[#+-*\] paused`).MatchString(s.Text())
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the dock does not read paused at %s colours: %v\n%s", depth.name, err, term.Snapshot())
			}
			copiesFrame(t, term, "copies-dock-"+depth.name)
			openCopies(t, term)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return regexp.MustCompile(`\[#+-*\] +\d+%`).MatchString(s.Text()) && strings.Contains(s.Text(), "slow.bin")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the panel bar does not read at %s colours: %v\n%s", depth.name, err, term.Snapshot())
			}
			copiesFrame(t, term, "copies-panel-"+depth.name)
			dialFileVerbs(t, base).must("transfer-cancel", map[string]any{"id": id}, nil)
		})
	}
}

// TestSendFromTheRailFilesList sends a file from the rail's files list to
// build: the row's menu, the machine picker, the folder field, and the file
// on build checked.
func TestSendFromTheRailFilesList(t *testing.T) {
	base, remote, term := copiesMachines(t, 64<<20)
	dir := filepath.Join(base, "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "send-me.txt")
	if err := os.WriteFile(src, []byte("sent from the rail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "near", "-s", "home"); err != nil {
		t.Fatalf("create a window: %v\n%s", err, out)
	}
	if err := term.WaitForText("near", uiTimeout); err != nil {
		t.Fatalf("the window never appeared: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf 'in-the-%s\\n' dir", "in-the-dir", uiTimeout)
	runInShell(t, term, `printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText("send-me.txt", uiTimeout); err != nil {
		t.Fatalf("the rail never listed the file: %v\n%s", err, term.Snapshot())
	}
	col, row, ok := findOnGrid(term.Screen(), "send-me.txt")
	if !ok {
		t.Fatalf("no file row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Send to host", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the file row's menu has no send: %v\n%s", err, term.Snapshot())
	}
	mcol, mrow, _ := findOnGrid(term.Screen(), "Send to host")
	mouseClick(t, term, mcol+2, mrow, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Send send-me.txt to which host?", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: send did not ask for a machine: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-send-machine")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("~/Downloads/tuios/", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the folder field is not filled with the receive folder: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-send-folder")
	// A folder in build's own root, not the person's Downloads.
	to := filepath.Join(remote, "inbox")
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatal(err)
	}
	if err := term.Type(to); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Copied send-me.txt to build. Checked.")
	}, 30*time.Second); err != nil {
		t.Fatalf("ASSERTION: the send did not end checked: %v\n%s", err, term.Snapshot())
	}
	if got, err := os.ReadFile(filepath.Join(to, "send-me.txt")); err != nil || string(got) != "sent from the rail\n" {
		t.Fatalf("ASSERTION: the file on build holds %q (%v)", got, err)
	}
	alive(t, term, "after a send from the rail")
}

// TestADroppedFileOnAFarPaneIsSent pastes a dropped file's quoted path, as
// GNOME Terminal and kitty send it, into a pane on build: tuios asks, Send
// copies it, and the path on build is pasted. The same paste into a pane on
// this machine arrives as the text it was.
func TestADroppedFileOnAFarPaneIsSent(t *testing.T) {
	base, _, term := copiesMachines(t, 64<<20)
	dir := filepath.Join(base, "desk top")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "drop me.txt")
	if err := os.WriteFile(src, []byte("dropped-marker-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + src + "' "

	// A pane on this machine: the paste is the text.
	if out, err := tuiosCLI(t, base, "new-window", "near", "-s", "home"); err != nil {
		t.Fatalf("create a window: %v\n%s", err, out)
	}
	if err := term.WaitForText("near", uiTimeout); err != nil {
		t.Fatalf("the window never appeared: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.Type("p="); err != nil {
		t.Fatal(err)
	}
	if err := term.Paste(quoted); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if strings.Contains(term.Screen().Text(), "dropped file") {
		t.Fatalf("ASSERTION: a paste into a pane on this machine asked to send:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	runInShell(t, term, `[ "$p" = "`+src+`" ] && echo LOCAL""-SAME || echo LOCAL""-OTHER`, "LOCAL-", uiTimeout)
	if !strings.Contains(term.Screen().Text(), "LOCAL-SAME") {
		t.Fatalf("ASSERTION: the paste into a local pane changed:\n%s", term.Snapshot())
	}
	leaveTerminalMode(t, term)

	// A pane on build: tuios asks.
	if out, err := tuiosCLI(t, base, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	if err := term.WaitForText("build:faraway", uiTimeout); err != nil {
		t.Fatalf("the far window never appeared: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.Type("q="); err != nil {
		t.Fatal(err)
	}
	if err := term.Paste(quoted); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Send drop me.txt", "to build?", "Paste the text")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a dropped path on a pane on build did not ask: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-drop-dialog")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Pasted the path on build.", 30*time.Second); err != nil {
		t.Fatalf("ASSERTION: the far path was not pasted: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	runInShell(t, term, `case "$q" in */drop/*) cat "$q"; echo FAR""-DROP;; *) echo FAR""-OTHER "$q";; esac`, "FAR-", uiTimeout)
	if txt := term.Screen().Text(); !containsAll(txt, "FAR-DROP", "dropped-marker-7") {
		t.Fatalf("ASSERTION: the pasted path is not the copy in build's drop folder:\n%s", term.Snapshot())
	}
	alive(t, term, "after a drop")
}

// TestCopiesPanelGivesKeysToAPanelOverIt opens Settings over the Copies
// panel with its prefix chord. Esc then closes Settings, the panel in front,
// and the Copies panel stays. Before the fix the Copies panel took every key
// while it was up, so esc closed it behind Settings and Settings stayed.
func TestCopiesPanelGivesKeysToAPanelOverIt(t *testing.T) {
	_, _, term := copiesMachines(t, 64<<20)
	openCopies(t, term)
	if err := term.WaitForText("No copies.", uiTimeout); err != nil {
		t.Fatalf("the empty panel did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), ","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("prefix , did not open Settings over the Copies panel: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-settings-over-panel")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return !strings.Contains(txt, "Settings") && strings.Contains(txt, "No copies.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close Settings and leave the Copies panel: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-settings-closed")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "No copies.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the Copies panel once it was in front: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after Settings over the Copies panel")
}

// TestCopiesLeaveThePanelWhenCleared ends two copies with the panel open.
// One is cleared by tuios transfers clear from outside the client, and the
// other leaves the daemon's list on its own when it is due, which a short
// TUIOS_TRANSFER_KEEP_SECONDS makes seconds. Each row has to leave the open
// panel. Before the fix neither said anything on the event stream, so both
// rows stayed until the client listed the copies again.
func TestCopiesLeaveThePanelWhenCleared(t *testing.T) {
	base, remote, term := copiesMachines(t, 64<<20, "TUIOS_TRANSFER_KEEP_SECONDS=20")
	openCopies(t, term)
	if err := term.WaitForText("No copies.", uiTimeout); err != nil {
		t.Fatalf("the empty panel did not open: %v\n%s", err, term.Snapshot())
	}
	start := func(name string) string {
		src := filepath.Join(base, name)
		if err := os.WriteFile(src, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return startHubCopy(t, base, map[string]any{
			"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": filepath.Join(remote, name)},
		})
	}
	cleared := start("cleared.txt")
	waitTransferState(t, base, cleared, "done", 30*time.Second)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "cleared.txt")
	}, uiTimeout); err != nil {
		t.Fatalf("the panel does not list the copy: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "transfers", "clear", cleared); err != nil {
		t.Fatalf("tuios transfers clear: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "cleared.txt")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a copy cleared outside the client stays in the panel: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-cleared-outside")

	expired := start("expired.txt")
	waitTransferState(t, base, expired, "done", 30*time.Second)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "expired.txt")
	}, uiTimeout); err != nil {
		t.Fatalf("the panel does not list the copy: %v\n%s", err, term.Snapshot())
	}
	// Nothing lists the copies here: the panel must hear the expiry itself.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "expired.txt") && strings.Contains(s.Text(), "No copies.")
	}, 40*time.Second); err != nil {
		t.Fatalf("ASSERTION: a copy that left the daemon's list on its own stays in the panel: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-expired")
	alive(t, term, "after copies left the list")
}

// TestCopiesNameTheKeysBoundNow binds the rail's send and the panel's answer
// to other keys. The empty panel and a copy that waits for an answer have
// to name those keys, and the answer key has to open the question. A folder
// copy with two files that differ says the second one in the singular.
func TestCopiesNameTheKeysBoundNow(t *testing.T) {
	conf := "[keybindings.sidebar_files]\nfile_send = [\"Y\"]\n\n[keybindings.transfers]\ntransfers_answer = [\"a\"]\n"
	base, remote, term := copiesMachinesConf(t, 64<<20, conf)
	openCopies(t, term)
	if err := term.WaitForText("No copies. Press Y on a file in the rail", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the empty panel does not name the send key bound now: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-empty-bound-key")

	src := filepath.Join(base, "docs")
	dst := filepath.Join(remote, "docs")
	for _, dir := range []string{src, dst} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("new "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), []byte("old "+name+", longer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id := startHubCopy(t, base, map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": dst},
		"conflict": "merge", "each": "ask", "place": "contents",
	})
	waitTransferState(t, base, id, "conflict", uiTimeout)
	if err := term.WaitForText("Press a to say what to do with the files that are there.", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the copy that waits does not name the answer key bound now: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Press enter") {
		t.Fatalf("ASSERTION: the panel names enter, which answers nothing now:\n%s", term.Snapshot())
	}
	copiesFrame(t, term, "copies-conflict-bound-key")
	if err := term.SendKeys("a"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("1 other file of this copy is there too.", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the bound key did not open the question, or the question does not say one other file in the singular: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "copies-conflict-singular")
	dialFileVerbs(t, base).must("transfer-cancel", map[string]any{"id": id}, nil)
	alive(t, term, "after keys bound again")
}
