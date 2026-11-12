package app

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestBoardSortsALongColumnByItsKeys holds the sort of a task board column to
// a budget, and the order it must keep. A session holds up to 1000 cards, the
// board lists every session's, and Done only grows, so a column can hold
// thousands of rows, and the rows are built again on every task event, every
// change of the agents or the Inbox and every minute.
//
// The budget is wall time of boardStageItems on one Done column of 2000 cards
// (two sessions of 1000), stage times shuffled with a fixed seed: the fastest
// of 5 runs, because load only adds time, skipped under -race and -short. On
// go1.27.4 on a Ryzen 5 5600, sorting the rows in place by insertion took
// 22 to 25 ms at 1000 rows and 91 to 93 ms at 2000; sorting small keys and
// placing each row once took 0.4 to 0.6 ms at 1000 rows and 1.3 to 2.4 ms at
// 2000.
// The ceiling of 10 ms is about 4x above the second and 9x under the first.
//
// The order is worked out here by its own rule: a blocked card first, then a
// card whose holder needs the person, then the rest; within each, the oldest
// stage time first; on equal stage times, the order the cards were given.
// Size 24 takes the short path, 25 and 1000 the long one.
//
// A column of 24 rows or fewer keeps the sort it had, with no added
// allocation, so the long path must start at 25: one more row there costs at
// least two more allocations (its keys and the sorted rows) than the row
// itself costs, which is one.
//
// How this test could pass wrongly, and what stops it:
//   - A fixture already in order: the cards are given out of id order, their
//     stage times shuffled, with four cards to a stage time, all three ranks,
//     and a card of the last rank older than a blocked card and than one
//     whose holder needs the person. The fixture is checked for each.
//   - Rows compared by position: a row is named by its card's session and id.
//   - A budget so loose the base meets it: the ceiling is under a tenth of
//     what insertion takes at 2000 rows.
//   - A column under the cutoff: the timed column holds 2000 rows, and every
//     subtest checks the column holds the rows it was given.
func TestBoardSortsALongColumnByItsKeys(t *testing.T) {
	t.Run("budget", func(t *testing.T) {
		if testing.Short() {
			t.Skip("timing budget")
		}
		if raceEnabled {
			t.Skip("timings are not meaningful under -race")
		}
		cards := append(boardSortCards("s", 1000, 91), boardSortCards("s2", 1000, 92)...)
		m, done := boardSortOS(t, cards)
		list := m.cockpitList()

		const ceiling = 10 * time.Millisecond
		best := time.Duration(1<<63 - 1)
		var rows []ckRow
		for range 5 {
			start := time.Now()
			rows = m.boardStageItems(list)[done]
			best = min(best, time.Since(start))
		}
		if len(rows) != len(cards) {
			t.Fatalf("the Done column holds %d rows, want the %d cards given", len(rows), len(cards))
		}
		t.Logf("fastest of 5 sorts of a Done column of %d cards: %v", len(rows), best)
		if best > ceiling {
			t.Errorf("sorting a Done column of %d cards took %v at best, want at most %v", len(rows), best, ceiling)
		}
	})

	t.Run("order", func(t *testing.T) {
		for _, n := range []int{24, 25, 1000} {
			t.Run(strconv.Itoa(n)+" rows", func(t *testing.T) {
				cards := boardSortCards("s", n, 7)
				boardSortFixtureHolds(t, cards)
				m, done := boardSortOS(t, cards)
				rows := m.boardStageItems(m.cockpitList())[done]
				got := make([]string, 0, len(rows))
				for _, r := range rows {
					c := m.Inbox.ck.cards[r.card]
					got = append(got, c.Session+"/"+c.ID)
				}
				if want := boardSortWant(cards); !slices.Equal(got, want) {
					t.Errorf("a Done column of %d cards is in the order\n%v\nwant\n%v", n, got, want)
				}
			})
		}
	})

	t.Run("long path from 25 rows", func(t *testing.T) {
		skipAllocCountsUnderRace(t)
		cards := boardSortCards("s", 25, 7)
		allocs := func(cards []session.TaskRow) float64 {
			m, done := boardSortOS(t, cards)
			list := m.cockpitList()
			if rows := m.boardStageItems(list)[done]; len(rows) != len(cards) {
				t.Fatalf("the Done column holds %d rows, want the %d cards given", len(rows), len(cards))
			}
			return testing.AllocsPerRun(20, func() { _ = m.boardStageItems(list) })
		}
		at24, at25 := allocs(cards[:24]), allocs(cards)
		t.Logf("boardStageItems allocates %v times at 24 rows and %v at 25", at24, at25)
		if at25-at24 < 2 {
			t.Errorf("a Done column of 25 rows allocates %v times and one of 24 rows %v: want at least 2 more at 25, "+
				"where the sort by keys starts", at25, at24)
		}
	})
}

// boardSortOS is benchAgents200OS with cards, given as the watcher gives
// them, in place of its own, and the index of the Done stage.
func boardSortOS(t *testing.T, cards []session.TaskRow) (*OS, int) {
	t.Helper()
	m := benchAgents200OS(t)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: benchInboxItems(), Cards: cards})
	done := slices.Index(session.TaskStages, session.TaskStageDone)
	if done < 0 {
		t.Fatal("no Done stage")
	}
	return m, done
}

// boardSortCards is a Done column of n cards of session sess, in an order
// that is not their ids' order. Each fifth card is blocked (every tenth with
// a holder that needs the person as well), each fifth after that is held by
// a pane that needs the person and is not in the agents list, and the rest
// are held by a working pane or by nobody. The stage times are shuffled,
// four cards to each.
func boardSortCards(sess string, n int, seed uint64) []session.TaskRow {
	r := rand.New(rand.NewPCG(seed, uint64(n)))
	ids, ages := r.Perm(n), r.Perm(n)
	anchor := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	cards := make([]session.TaskRow, 0, n)
	for i := range n {
		c := session.TaskRow{
			ID: "t" + strconv.Itoa(ids[i]+1), Session: sess, Title: "make the retry jitter configurable " + strconv.Itoa(i),
			Stage: session.TaskStageDone, StageAt: anchor - int64(ages[i]/4)*int64(time.Second),
		}
		holder := func(state string) *session.TaskHolder {
			return &session.TaskHolder{Window: "gone" + strconv.Itoa(i), Name: "task" + strconv.Itoa(i), Harness: "codex", State: state, HeldAt: anchor}
		}
		switch i % 5 {
		case 0:
			c.Blocked = &session.TaskBlock{Reason: "waits on the api review", At: anchor}
			if i%10 == 0 {
				c.Holder = holder("needs_input")
			}
		case 1:
			c.Holder = holder("needs_input")
		case 2:
			c.Holder = holder("working")
		}
		cards = append(cards, c)
	}
	return cards
}

// boardSortRank is the test's own rank of a card the agents list does not
// hold: blocked, then a holder that needs the person, then the rest.
func boardSortRank(c session.TaskRow) int {
	switch {
	case c.Blocked != nil:
		return 0
	case c.Holder != nil && c.Holder.State == "needs_input":
		return 1
	}
	return 2
}

// boardSortWant is the order of cards by rank, then stage time, then their
// place in cards, each named by its session and id.
func boardSortWant(cards []session.TaskRow) []string {
	want := make([]string, 0, len(cards))
	for rank := range 3 {
		var at []int
		for i, c := range cards {
			if boardSortRank(c) == rank {
				at = append(at, i)
			}
		}
		// The place breaks a tie, so the comparison is total and the sort
		// need not be stable.
		slices.SortFunc(at, func(a, b int) int {
			return cmp.Or(cmp.Compare(cards[a].StageAt, cards[b].StageAt), cmp.Compare(a, b))
		})
		for _, i := range at {
			want = append(want, cards[i].Session+"/"+cards[i].ID)
		}
	}
	return want
}

// boardSortFixtureHolds stops the test when cards could not tell a wrong
// order from the right one.
func boardSortFixtureHolds(t *testing.T, cards []session.TaskRow) {
	t.Helper()
	ranks := map[int]bool{}
	ties, idsInOrder, timesInOrder := false, true, true
	seen := map[[2]int64]bool{}
	oldest := [3]int64{1<<63 - 1, 1<<63 - 1, 1<<63 - 1}
	newest := [3]int64{-1 << 63, -1 << 63, -1 << 63}
	for i, c := range cards {
		rank := boardSortRank(c)
		ranks[rank] = true
		k := [2]int64{int64(rank), c.StageAt}
		ties = ties || seen[k]
		seen[k] = true
		oldest[rank], newest[rank] = min(oldest[rank], c.StageAt), max(newest[rank], c.StageAt)
		if i > 0 {
			prev, _ := strconv.Atoi(cards[i-1].ID[1:])
			cur, _ := strconv.Atoi(c.ID[1:])
			idsInOrder = idsInOrder && prev < cur
			timesInOrder = timesInOrder && cards[i-1].StageAt <= c.StageAt
		}
	}
	switch {
	case len(ranks) != 3:
		t.Fatalf("the fixture holds ranks %v, want all three", ranks)
	case !ties:
		t.Fatal("the fixture holds no two cards of the same rank and stage time")
	case idsInOrder:
		t.Fatal("the fixture's cards are in the order of their ids")
	case timesInOrder:
		t.Fatal("the fixture's stage times are in the cards' order")
	case len(cards) >= 24 && (oldest[2] >= newest[0] || oldest[2] >= newest[1]):
		t.Fatal("no card of the last rank is older than a blocked card and than one that needs the person")
	}
}
