package tuie2e

import (
	"image"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Hover help, driven through the real binary: the leader then h turns it on
// and off, the pointer and the help keys pick what it explains, and it
// takes no key and no click that is not its own. Every step saves its screen,
// plain and styled, under artifactDir.
//
// How these could pass while hover help is broken, written down first:
//   - A screen could hold the marker text for another reason. The markers are
//     the explainer's own words: its footer's close chord and the headings of
//     its pages, which nothing else in tuios draws.
//   - The keys could reach the pane and hover help both. The pass-through test
//     reads the bytes the pane got through cat -v, so a help key that leaked is
//     on screen as its escape sequence, and a plain key that was eaten is
//     missing from it.
//   - A click could be eaten and something else make the workspace. The
//     click lands on the dock's new-workspace button, the only thing on that
//     screen that makes one, and the dock's counter names workspace 2 only
//     once it exists.
//
//   - A page could be reached by the walk while the pointer finds nothing. The
//     pane and rail tests hover a cell first and wait for that part's own page
//     by its path, then walk.
//
// Negative control, run: on a build of main, which has no hover help, every
// test fails at its first wait for the explainer. On a build with hover help
// over the dock and the desktop only, the pane test fails at the body's page
// and the rail test at the rail's. With the meter parts measured with their
// margin again, the meter test shows the CPU page at the RAM readout's first
// column. With the parts ranked by size alone, the on-top test shows the page
// of the pane under the popup. NEGATIVE_CONTROLS.md has those two runs.

// The help keys as xterm sends them: CSI 1 ; 7 for ctrl+alt, ESC before a
// character for alt.
const (
	hhNext      tuitest.Key = "\x1b[1;7C"
	hhPrev      tuitest.Key = "\x1b[1;7D"
	hhDeeper    tuitest.Key = "\x1b[1;7B"
	hhShallower tuitest.Key = "\x1b[1;7A"
	hhAltShiftH tuitest.Key = "\x1bH"
	hhAltShiftK tuitest.Key = "\x1bK"
	hhUp        tuitest.Key = "\x1b[A"
	hhDown      tuitest.Key = "\x1b[B"
)

// hhClose is the footer's close hint under the default leader. Only the
// explainer draws it.
const hhClose = "Ctrl+b h close"

// hoverHelpStart boots tuios on one pane and returns the terminal and the
// folder the step frames go to.
func hoverHelpStart(t *testing.T, config string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	if config != "" {
		writeConfig(t, base, config)
	}
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	return term, artifactDir(t)
}

// toggleHoverHelp presses the leader then h.
func toggleHoverHelp(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "h")
}

// TestHoverHelpToggles turns hover help on and off with the leader then h,
// twice, so the second opening is not left over from the first.
func TestHoverHelpToggles(t *testing.T) {
	term, dir := hoverHelpStart(t, "")
	for i := range 2 {
		toggleHoverHelp(t, term)
		waitText(t, term, "the explainer after ctrl+b h", hhClose)
		if i == 0 {
			saveArtifact(t, term, dir, "on")
		}
		toggleHoverHelp(t, term)
		waitGone(t, term, "the explainer after ctrl+b h again", hhClose)
	}
	saveArtifact(t, term, dir, "off")
}

// TestHoverHelpExplainsTheDockCellUnderThePointer hovers the dock's mode pill
// and waits for its page, by its path and its own words.
func TestHoverHelpExplainsTheDockCellUnderThePointer(t *testing.T) {
	term, dir := hoverHelpStart(t, "")
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer", hhClose)
	_, rows := term.Screen().Size()
	mouseHover(t, term, 2, rows-1)
	waitText(t, term, "the mode pill's page", "Dock › Mode", "The input mode")
	saveArtifact(t, term, dir, "mode-pill")
}

// TestHoverHelpExplainsEachMeter puts the CPU and RAM readouts side by side
// and hovers the first column of the RAM readout, then the last column of the
// CPU graph. The two share one segment of the dock, so each records its own
// columns; once the segment's margin was counted in each readout, the first
// two columns of the RAM readout explained the CPU.
func TestHoverHelpExplainsEachMeter(t *testing.T) {
	term, dir := hoverHelpStart(t, "[appearance]\nshow_cpu = true\nshow_ram = true\nshow_clock = false\n\n"+
		"[dock]\nright = ['cpu', 'ram']\n")
	_, rows := term.Screen().Size()
	waitText(t, term, "the RAM readout on the dock", "RAM:")
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer", hhClose)
	dock := strings.Split(term.Screen().Text(), "\n")[rows-1]
	before, _, found := strings.Cut(dock, "RAM:")
	if !found {
		t.Fatalf("the dock has no RAM readout\n%s", term.Snapshot())
	}
	ramX := len([]rune(before))
	mouseHover(t, term, ramX, rows-1)
	waitText(t, term, "the RAM page at the readout's first column", "Dock › RAM", "The memory readout")
	saveArtifact(t, term, dir, "ram")
	// The CPU graph ends one space before the RAM readout.
	mouseHover(t, term, ramX-2, rows-1)
	waitText(t, term, "the CPU page at the graph's last column", "Dock › CPU", "The CPU readout")
	saveArtifact(t, term, dir, "cpu")
}

// TestHoverHelpWalksWithTheHelpKeys walks from the dock round to the
// desktop and back, into the dock's parts and out, then shows every key and goes back.
func TestHoverHelpWalksWithTheHelpKeys(t *testing.T) {
	term, dir := hoverHelpStart(t, "")
	toggleHoverHelp(t, term)
	waitText(t, term, "hover help opens on the dock", "Dock (", hhClose)
	saveArtifact(t, term, dir, "1-dock")

	sendKeys(t, term, hhPrev)
	waitText(t, term, "ctrl+alt+left wraps round to the desktop", "The desktop behind the panes")
	saveArtifact(t, term, dir, "2-desktop")

	sendKeys(t, term, hhNext)
	waitText(t, term, "ctrl+alt+right comes back to the dock", "Dock (")

	sendKeys(t, term, hhDeeper)
	waitText(t, term, "ctrl+alt+down goes into the dock", "Dock › ")
	saveArtifact(t, term, dir, "3-dock-part")

	sendKeys(t, term, hhShallower)
	waitText(t, term, "ctrl+alt+up comes out to the dock", "Dock (")
	waitGone(t, term, "ctrl+alt+up comes out to the dock", "Dock › ")

	sendKeys(t, term, hhAltShiftH)
	waitText(t, term, "alt+shift+h shows every key", "All keys", "h back")
	saveArtifact(t, term, dir, "4-all-keys")
	sendKeys(t, term, hhAltShiftH)
	waitGone(t, term, "alt+shift+h again goes back", "All keys")
}

// TestHoverHelpLeavesTypingToThePane runs a command in the shell with hover
// help up, and the explainer is still there after.
func TestHoverHelpLeavesTypingToThePane(t *testing.T) {
	term, dir := hoverHelpStart(t, "")
	enterTerminalMode(t, term)
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer", hhClose)
	runInShell(t, term, `echo typed""through`, "typedthrough", shellTimeout)
	waitText(t, term, "the explainer after the command", hhClose)
	saveArtifact(t, term, dir, "typed")
}

// TestHoverHelpPassesPlainKeysThrough runs cat -v in the pane with hover help
// up, then presses up, down, h and a help key. The pane must get the plain
// keys, and none of the help keys, which walk the explainer instead.
func TestHoverHelpPassesPlainKeysThrough(t *testing.T) {
	term, dir := hoverHelpStart(t, "")
	enterTerminalMode(t, term)
	runInShell(t, term, `echo cat""ready; cat -v`, "catready", shellTimeout)
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer opens on the dock", "Dock (", hhClose)

	sendKeys(t, term, hhUp, hhDown, "h", hhPrev)
	waitText(t, term, "ctrl+alt+left walks round to the desktop", "The desktop behind the panes")
	sendKeys(t, term, tuitest.Enter)
	// cat -v writes the line back after enter, so the line is on screen twice
	// once the pane had it: the terminal's echo and cat's copy.
	waitLines(t, term, "^[[A^[[Bh", 2)
	saveArtifact(t, term, dir, "cat-v")
	if text := term.Screen().Text(); strings.Contains(text, "^[[1;7") {
		t.Fatalf("ASSERTION: a help key reached the pane while hover help was up\n%s", term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('c'))
}

// waitLines waits until want is the whole content of n lines of a pane.
func waitLines(t *testing.T, term *tuitest.Terminal, want string, n int) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		count := 0
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")) == want {
				count++
			}
		}
		return count >= n
	}, shellTimeout); err != nil {
		t.Fatalf("ASSERTION: %q is not on %d lines: %v\n%s", want, n, err, term.Snapshot())
	}
}

// TestHoverHelpFooterShowsReboundKeys binds help_keys to alt+shift+k. The
// footer names that key, not the default, and the key works.
func TestHoverHelpFooterShowsReboundKeys(t *testing.T) {
	term, dir := hoverHelpStart(t, "[keybindings.help]\nhelp_keys = [\"alt+shift+k\"]\n")
	toggleHoverHelp(t, term)
	waitText(t, term, "the footer names the rebound key", "Alt+Shift+k keys")
	saveArtifact(t, term, dir, "footer")
	if strings.Contains(term.Screen().Text(), "Alt+Shift+h") {
		t.Fatalf("ASSERTION: the footer still names the default key\n%s", term.Snapshot())
	}
	sendKeys(t, term, hhAltShiftK)
	waitText(t, term, "alt+shift+k shows every key", "All keys")
}

// TestHoverHelpLetsClicksThrough clicks the dock's new-workspace button with
// hover help up. The click makes the workspace and the explainer stays.
func TestHoverHelpLetsClicksThrough(t *testing.T) {
	term, dir := hoverHelpStart(t, "")
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer", hhClose)
	_, rows := term.Screen().Size()
	dock := strings.Split(term.Screen().Text(), "\n")[rows-1]
	if strings.Contains(dock, "2:") {
		t.Fatalf("the dock names workspace 2 before the click\n%s", term.Snapshot())
	}
	before, _, found := strings.Cut(dock, " + ")
	if !found {
		t.Fatalf("the dock has no new-workspace button\n%s", term.Snapshot())
	}
	clickAt(t, term, len([]rune(before))+1, rows-1, 1)
	waitText(t, term, "the dock after a click on the new-workspace button with hover help up", "2:0")
	waitText(t, term, "the explainer after the click", hhClose)
	saveArtifact(t, term, dir, "clicked")
}

// TestHoverHelpExplainsAPaneAndItsParts hovers a floating pane's body and its
// title bar, then walks between them and out to the pane itself. The suite
// pins the title bar to the bottom, so the test puts it on top, the default.
// The body is hovered first, while the explainer is on the dock's page and
// leaves the pane uncovered.
func TestHoverHelpExplainsAPaneAndItsParts(t *testing.T) {
	term, dir := hoverHelpStart(t, "[appearance]\nwindow_title_position = \"top\"\n")
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer", "Dock (", hhClose)
	left, top := -1, -1
	for y, line := range strings.Split(term.Screen().Text(), "\n") {
		if before, _, ok := strings.Cut(line, "╭"); ok {
			left, top = len([]rune(before)), y
			break
		}
	}
	if top < 0 {
		t.Fatalf("no floating pane's top border on screen\n%s", term.Snapshot())
	}
	mouseHover(t, term, left+10, top+2)
	waitText(t, term, "the body's page", "› Terminal: ")
	saveArtifact(t, term, dir, "1-body")

	mouseHover(t, term, left+10, top)
	waitText(t, term, "the title bar's page", "› Title bar: ", "It names the pane")
	saveArtifact(t, term, dir, "2-title-bar")

	sendKeys(t, term, hhDeeper)
	waitText(t, term, "ctrl+alt+down from the title bar reaches the body", "› Terminal: ")
	sendKeys(t, term, hhShallower)
	waitText(t, term, "ctrl+alt+up from the body reaches the title bar", "› Title bar: ")
	sendKeys(t, term, hhShallower)
	waitText(t, term, "ctrl+alt+up from the title bar reaches the pane", "(2 parts)", "One terminal.")
	saveArtifact(t, term, dir, "3-pane")
}

// TestHoverHelpExplainsThePaneOnTop opens a popup larger than the floating
// pane under it and hovers a cell the two share. The popup is drawn on top, so
// the page is the popup's; once the smallest part under the pointer won, the
// smaller pane underneath was explained through the popup.
func TestHoverHelpExplainsThePaneOnTop(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nwindow_title_position = \"top\"\n")
	term := startIn(t, base, startOpts{args: []string{"new", "hh"}})
	waitBoot(t, term)
	newWindow(t, term)
	dir := artifactDir(t)
	cover := openPopup(t, base, "--width", "95%", "--height", "95%", "--name", "cover",
		"--", "sh", "-c", "printf 'COVER-IS-UP\\n'; sleep 600")
	waitText(t, term, "the popup", "COVER-IS-UP")
	rects := waitForSettledGeometryIn(t, base, "hh", 2)
	top := rectFor(t, rects, cover.WindowID)
	var under winRect
	for _, r := range rects {
		if r.ID != cover.WindowID {
			under = r
		}
	}
	both := image.Rect(top.X, top.Y, top.X+top.Width, top.Y+top.Height).
		Intersect(image.Rect(under.X, under.Y, under.X+under.Width, under.Y+under.Height))
	if both.Dx() < 6 || both.Dy() < 6 {
		t.Fatalf("the popup %+v does not cover the pane %+v", top, under)
	}
	toggleHoverHelp(t, term)
	waitText(t, term, "the explainer", hhClose)
	// Near the overlap's corner: the explainer sits in the middle, and motion
	// over it is ignored.
	at := both.Min.Add(image.Pt(3, 3))
	mouseHover(t, term, at.X, at.Y)
	waitText(t, term, "the page of the popup drawn over the pane", "Terminal: cover")
	saveArtifact(t, term, dir, "on-top")
}

// TestHoverHelpExplainsTheRailSections turns the rail on, walks into its
// sections, and hovers one.
func TestHoverHelpExplainsTheRailSections(t *testing.T) {
	term, dir := hoverHelpStart(t, "[appearance.sidebar]\nenabled = true\n")
	toggleHoverHelp(t, term)
	waitText(t, term, "hover help opens on the dock", "Dock (", hhClose)

	sendKeys(t, term, hhNext)
	waitText(t, term, "ctrl+alt+right reaches the rail", "Rail (", "The rail is the column")
	saveArtifact(t, term, dir, "1-rail")

	sendKeys(t, term, hhDeeper)
	waitText(t, term, "ctrl+alt+down goes into the rail's first section", "Rail › Sessions")
	sendKeys(t, term, hhDeeper)
	waitText(t, term, "ctrl+alt+down goes on to the next section", "Rail › Terminals")
	saveArtifact(t, term, dir, "2-terminals")
	hhRightOfRail(t, term, "Rail › Terminals")
	sendKeys(t, term, hhShallower, hhShallower)
	waitText(t, term, "ctrl+alt+up twice comes out to the rail", "Rail (")

	// The suite pins the rail to the left. Its sessions header is the row to
	// hover, and the explainer does not cover it.
	row := -1
	for y, line := range strings.Split(term.Screen().Text(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "sessions") {
			row = y
			break
		}
	}
	if row < 0 {
		t.Fatalf("no sessions header on screen\n%s", term.Snapshot())
	}
	mouseHover(t, term, 3, row)
	waitText(t, term, "hovering the rail's sessions header", "Rail › Sessions")
	saveArtifact(t, term, dir, "3-hover")
	hhRightOfRail(t, term, "Rail › Sessions")
}

// hhRightOfRail fails unless the explainer's line that names title starts
// right of the rail's edge, the │ on the sessions header's row: below a
// section is the rest of the rail, and the explainer must not cover it.
func hhRightOfRail(t *testing.T, term *tuitest.Terminal, title string) {
	t.Helper()
	edge, at := -1, -1
	for _, line := range strings.Split(term.Screen().Text(), "\n") {
		runes := []rune(line)
		if edge < 0 && strings.HasPrefix(strings.TrimSpace(line), "sessions") {
			edge = slices.Index(runes, '│')
		}
		if i := strings.Index(line, title); i >= 0 && at < 0 {
			at = len([]rune(line[:i]))
		}
	}
	if edge < 0 || at < 0 {
		t.Fatalf("no rail edge (%d) or no %q (%d) on screen\n%s", edge, title, at, term.Snapshot())
	}
	if at <= edge {
		t.Fatalf("ASSERTION: the explainer for %q starts at column %d, over the rail, whose edge is column %d\n%s",
			title, at, edge, term.Snapshot())
	}
}
