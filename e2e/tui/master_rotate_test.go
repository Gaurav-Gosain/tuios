package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRotateSplitInMasterStack is issue #2417: in the master-stack layout,
// rotate split (Ctrl+B R) did nothing, and the dock said "Split rotated" or
// "Cannot rotate: window has no parent split". It worked on the BSP tree,
// which master-stack does not draw.
//
// In master-stack, rotate split turns the side the masters are drawn on:
// left goes to top and back, right goes to bottom and back. The tiler draws
// some shapes on another side than the one stored, and rotate works from the
// drawn side. Masters in the center with one stack pane are drawn on the
// left, so rotate takes them to the top. Masters drawn in the center have no
// turned form. Two panes on a tall screen are always drawn one above the
// other. In both, nothing moves and the dock says why. Each shape is read
// from the daemon's rectangles, so it is the session's layout, not one
// client's.
//
// How this could pass wrongly, written down first:
//   - The dock could say "Split rotated" while the panes stay. The shape is
//     read from the daemon after each press.
//   - A second press could do nothing. The second press must turn it back.
//   - A center check with one stack pane passes for masters on the left too.
//     The center case has two stack panes, so the masters must be between
//     them.
//   - Rotate could read the stored side, which the tall screen and the
//     center with one stack pane do not draw. Both cases are here.
//
// NEGATIVE CONTROL: with the inMasterStack branch cut from
// RotateFocusedSplit it fails at "after the first rotate", as on main. With
// rotate reading the stored side, it fails at "center with one stack pane,
// after a rotate" and at the tall screen's dock message.
func TestRotateSplitInMasterStack(t *testing.T) {
	t.Run("wide", func(t *testing.T) {
		base := t.TempDir()
		const name = "rotate"
		term := masterSession(t, base, name, "", 2)
		waitForShape(t, base, name, 2, "the default", masterShape("left", 1))
		dir := artifactDir(t)
		saveArtifact(t, term, dir, "left")

		sendKeys(t, term, tuitest.Ctrl('b'), "R")
		waitForShape(t, base, name, 2, "after the first rotate", masterShape("top", 1))
		waitScreen(t, term, "the dock message", "Split rotated")
		saveArtifact(t, term, dir, "top")

		sendKeys(t, term, tuitest.Ctrl('b'), "R")
		waitForShape(t, base, name, 2, "after the second rotate", masterShape("left", 1))

		if out, err := tuiosCLI(t, base, "set-layout", "--master-position", "right", "-s", name); err != nil {
			t.Fatalf("set-layout --master-position right: %v\n%s", err, out)
		}
		waitForShape(t, base, name, 2, "the masters on the right", masterShape("right", 1))
		sendKeys(t, term, tuitest.Ctrl('b'), "R")
		waitForShape(t, base, name, 2, "after the rotate from the right", masterShape("bottom", 1))
		saveArtifact(t, term, dir, "bottom")

		// Center with one stack pane is drawn on the left.
		if out, err := tuiosCLI(t, base, "set-layout", "--master-position", "center", "-s", name); err != nil {
			t.Fatalf("set-layout --master-position center: %v\n%s", err, out)
		}
		waitForShape(t, base, name, 2, "center with one stack pane", masterShape("left", 1))
		sendKeys(t, term, tuitest.Ctrl('b'), "R")
		waitForShape(t, base, name, 2, "center with one stack pane, after a rotate", masterShape("top", 1))
		saveArtifact(t, term, dir, "center-one-stack-rotated")

		// With two stack panes the masters are drawn in the center.
		if out, err := tuiosCLI(t, base, "set-layout", "--master-position", "center", "-s", name); err != nil {
			t.Fatalf("set-layout --master-position center: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "run-command", "-s", name, "NewWindow"); err != nil {
			t.Fatalf("open a third pane: %v\n%s", err, out)
		}
		waitForShape(t, base, name, 3, "the masters in the center", masterShape("center", 1))
		sendKeys(t, term, tuitest.Ctrl('b'), "R")
		waitScreen(t, term, "the dock message in the center", "Rotate split does not apply with the masters in the center")
		waitForShape(t, base, name, 3, "the center after a rotate", masterShape("center", 1))
		saveArtifact(t, term, dir, "center")
	})

	t.Run("tall", func(t *testing.T) {
		base := t.TempDir()
		const name = "rotate-tall"
		term := masterSession(t, base, name, "", 2)
		// Tall for two panes, and wide enough for the dock to say why.
		if err := term.Resize(90, 50); err != nil {
			t.Fatalf("resize the client: %v", err)
		}
		// The stored side is left. Two panes on a tall screen are drawn one
		// above the other.
		waitForShape(t, base, name, 2, "two panes on a tall screen", masterShape("top", 1))
		sendKeys(t, term, tuitest.Ctrl('b'), "R")
		// The dock cuts a long message to its width, so it is read in part here
		// and whole from the command below.
		waitScreen(t, term, "the dock message on a tall screen", "Two panes on a tall screen")
		waitForShape(t, base, name, 2, "the tall screen after a rotate", masterShape("top", 1))
		saveArtifact(t, term, artifactDir(t), "tall")
		// The command says the same, and fails, as nothing changed.
		out, err := tuiosCLI(t, base, "set-layout", "--rotate", "-s", name)
		if err == nil || !strings.Contains(out, "Two panes on a tall screen stay one above the other") {
			t.Fatalf("set-layout --rotate on a tall screen: err %v, want the reason\n%s", err, out)
		}
	})
}
