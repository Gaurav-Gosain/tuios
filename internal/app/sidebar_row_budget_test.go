package app

import (
	"slices"
	"testing"
)

// TestRailRowFit pins the arithmetic every budgeted rail row is drawn with.
//
// Each case is one a simpler rule gets wrong, named in its comment, so a
// change that slides back to one of those rules fails here by name:
//
//   - drop from the end: the old agent row cut its run from the right until it
//     fit, so the token saying what the pane is doing went first.
//   - name first: the name holds its whole width before any token is placed,
//     so one long name leaves every token nothing.
//   - no skip: the first token that does not fit ends the walk, so a long
//     token takes the width a shorter one needed.
func TestRailRowFit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		nameW  int
		tokens []railToken
		avail  int
		keep   []bool
		room   int
	}{
		{
			name:  "everything fits",
			nameW: 6, tokens: []railToken{{Cost: 4}, {Cost: 5}}, avail: 20,
			keep: []bool{true, true}, room: 11,
		},
		{
			// Drop from the end keeps the first token and loses the last.
			name:  "the rightmost token survives",
			nameW: 6, tokens: []railToken{{Cost: 5}, {Cost: 5}}, avail: 12,
			keep: []bool{false, true}, room: 7,
		},
		{
			// No skip stops at the long last token and keeps nothing.
			name:  "a token that does not fit is passed over",
			nameW: 6, tokens: []railToken{{Cost: 3}, {Cost: 9}}, avail: 12,
			keep: []bool{true, false}, room: 9,
		},
		{
			// Name first gives the 20-cell name the whole row and drops the state.
			name:  "a long name holds only its keep while tokens are placed",
			nameW: 20, tokens: []railToken{{Cost: 10}}, avail: 20,
			keep: []bool{true}, room: 10,
		},
		{
			name:  "a token that would cut the name below its keep goes",
			nameW: 20, tokens: []railToken{{Cost: 12}}, avail: 20,
			keep: []bool{false}, room: 20,
		},
		{
			name:  "a short name keeps all of itself",
			nameW: 4, tokens: []railToken{{Cost: 10}}, avail: 13,
			keep: []bool{false}, room: 13,
		},
		{
			name:  "context is kept while the whole name fits beside it",
			nameW: 6, tokens: []railToken{{Cost: 7, Whole: true}}, avail: 13,
			keep: []bool{true}, room: 6,
		},
		{
			// A plain token here would be kept: 7 cells and the name's keep of 6.
			name:  "context goes before a cell of the name",
			nameW: 6, tokens: []railToken{{Cost: 7, Whole: true}}, avail: 12,
			keep: []bool{false}, room: 12,
		},
		{
			name:  "context is offered its cells before the facts that give way",
			nameW: 6, tokens: []railToken{{Cost: 7, Whole: true}, {Cost: 7}}, avail: 14,
			keep: []bool{true, false}, room: 7,
		},
		{
			// A plain token here would come back: 3 cells beside the whole name.
			name:  "context is never passed over",
			nameW: 6, tokens: []railToken{{Cost: 3, Whole: true}, {Cost: 7, Whole: true}}, avail: 12,
			keep: []bool{false, false}, room: 12,
		},
		{
			name:  "the right-hand slot pays its inset once",
			nameW: 4, tokens: []railToken{{Cost: 3, Right: true}, {Cost: 2, Right: true}}, avail: 10,
			keep: []bool{true, true}, room: 4,
		},
		{
			name:  "the inset is paid by whichever figure is kept",
			nameW: 4, tokens: []railToken{{Cost: 3, Right: true}, {Cost: 9, Right: true}}, avail: 8,
			keep: []bool{true, false}, room: 4,
		},
		{
			name:  "a token that costs nothing is not on the row",
			nameW: 4, tokens: []railToken{{Cost: 0}, {Cost: 0, Right: true}}, avail: 8,
			keep: []bool{false, false}, room: 8,
		},
		{
			name:  "a row with no name spends every cell on tokens",
			nameW: 0, tokens: []railToken{{Cost: 4}, {Cost: 4}}, avail: 8,
			keep: []bool{true, true}, room: 0,
		},
		{
			name:  "a row too narrow for anything keeps a cell of the name",
			nameW: 6, tokens: []railToken{{Cost: 1}}, avail: 1,
			keep: []bool{false}, room: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keep, room := railRowFit(tc.nameW, railNameKeep(tc.nameW), tc.tokens, tc.avail)
			if !slices.Equal(keep, tc.keep) || room != tc.room {
				t.Errorf("railRowFit(name %d, %+v, avail %d) kept %v with %d cells for the name, want %v and %d",
					tc.nameW, tc.tokens, tc.avail, keep, room, tc.keep, tc.room)
			}
		})
	}
}

// TestRailRowFitNeverOverflows walks every width from nothing to wide for a
// crowded row and checks the one promise every caller leans on: what is kept,
// with the name at its room, fits the width it was given.
func TestRailRowFitNeverOverflows(t *testing.T) {
	tokens := []railToken{
		{Cost: 4, Whole: true}, {Cost: 7, Whole: true},
		{Cost: 10}, {Cost: 6},
		{Cost: 3, Right: true}, {Cost: 4, Right: true},
	}
	const nameW = 22
	for avail := 1; avail <= 70; avail++ {
		keep, room := railRowFit(nameW, railNameKeep(nameW), tokens, avail)
		used, inset := 0, false
		for i, k := range keep {
			if !k {
				continue
			}
			used += tokens[i].Cost
			if tokens[i].Right && !inset {
				used++
				inset = true
			}
			if tokens[i].Whole && room < nameW {
				t.Errorf("avail %d: kept context token %d beside a name cut to %d of %d", avail, i, room, nameW)
			}
		}
		if used+room != avail {
			t.Errorf("avail %d: tokens take %d and the name %d, which is not the row", avail, used, room)
		}
		if room < min(railNameKeep(nameW), avail) {
			t.Errorf("avail %d: the name has %d cells, under its keep", avail, room)
		}
	}
}
