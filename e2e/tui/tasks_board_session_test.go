package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The task board keys a card by its session's name. These tests watch another
// session's cards from an open board while that session is renamed, and
// while it is killed and made again under the same name. Either leaves rows
// the daemon no longer holds, or hides rows it does hold, unless the board
// lists the cards again.
//
// How they could pass wrongly: a stale row and a fresh one carry the same
// title, so the titles alone hold even when the board shows a card twice or
// under the wrong stage. Each step waits for the count in each column head as
// well.

// TestTaskBoardFollowsARenamedSession renames S while the board is open in V,
// then moves one of its cards from the command line under the new name. The
// board must show both cards, one in Todo and one in Verify.
func TestTaskBoardFollowsARenamedSession(t *testing.T) {
	base, term := openSessionBoard(t, "board-rename")
	mustCLI(t, base, "rename-session", "S", "S2")
	mustCLI(t, base, "task", "stage", "-s", "S2", "t1", "verify")
	boardShows(t, term, "board-rename-after", []string{"Todo  1", "Verify  1", "first card", "second card"}, nil)
}

// TestTaskBoardDropsAKilledSessionsCards kills S while the board is open in
// V, makes a new S and adds a card to it. The board must show the new card
// alone: the killed session's cards are gone.
func TestTaskBoardDropsAKilledSessionsCards(t *testing.T) {
	base, term := openSessionBoard(t, "board-kill")
	mustCLI(t, base, "kill-session", "S")
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	mustCLI(t, base, "task", "add", "-s", "S", "fresh card")
	boardShows(t, term, "board-kill-after", []string{"Todo  1", "fresh card"}, []string{"first card", "second card"})
}

// openSessionBoard starts a daemon with sessions V and S, adds two cards to
// S, attaches to V and opens the board in the agents view. No pane runs an
// agent, so the view waits on its empty state. The board's frame is saved as
// prefix-open.
func openSessionBoard(t *testing.T, prefix string) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	mustCLI(t, base, "new", "V", "--detach")
	waitForSessionInfo(t, base, "V")
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	mustCLI(t, base, "task", "add", "-s", "S", "first card")
	mustCLI(t, base, "task", "add", "-s", "S", "second card")
	term := attachIn(t, base, "V", startOpts{cols: 200, rows: 40, shippedLooks: true})
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agents view", "No agents yet.")
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
	boardShows(t, term, prefix+"-open", []string{"Todo  2", "first card", "second card"}, nil)
	return base, term
}

// boardShows waits for the board to show every string in want and none in
// gone, and saves the screen as frame whether or not it does.
func boardShows(t *testing.T, term *tuitest.Terminal, frame string, want, gone []string) {
	t.Helper()
	err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		for _, w := range want {
			if !strings.Contains(txt, w) {
				return false
			}
		}
		for _, g := range gone {
			if strings.Contains(txt, g) {
				return false
			}
		}
		return true
	}, uiTimeout)
	copiesFrame(t, term, frame)
	if err != nil {
		t.Fatalf("ASSERTION: %s: want %q and not %q: %v\n%s", frame, want, gone, err, term.Snapshot())
	}
}
