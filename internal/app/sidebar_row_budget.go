package app

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// How a rail row fits into a narrow rail.
//
// Every row in the rail is one name with a run of tokens around it: a session
// row carries its branch, its tags and its window count, a machine header the
// word saying why it is not answering, an agent row the session it is in, the
// harness running it, what it is doing, how long it has been doing it and the
// mail waiting for it. All of them have to fit the same 16-column rail. Until
// this file each row cut itself down in its own way, and most of them gave
// the name whatever was left after the right-hand figure, or dropped the last
// token of a run whatever that token said.
//
// This is herdr's arithmetic for the same problem, on the same grid, with one
// difference that follows from a rule tuios already keeps:
//
//  1. Survival from the right. Every token is dropped, then offered back one
//     at a time from the end of the row, and kept only if the whole of it
//     still fits. A token that does not fit is passed over and the next one is
//     still offered its cells, so one long token cannot take the width two
//     shorter ones needed. The rightmost token survives, which on an agent row
//     is the one saying what the pane is doing rather than where it is.
//
//  2. Spare cells are handed out round-robin across the tokens that survived.
//     A token here is kept whole or dropped, never cut, so the name is the one
//     survivor that can take a cell, and it takes every spare one. What stops
//     a long name starving the rest is step 1: while the tokens are offered
//     their cells the name holds only its keep (railNameKeep), not its whole
//     width.
//
// Nothing is ever cut inside a token, which is what lets a value rule in
// [appearance.sidebar.agent_row] ink a token by the value the person reads:
// the rule matches "needs input" and the row draws "needs input", never
// "needs in…" wearing the colour of a value that is not on screen. herdr needs
// a dedicated test for that because it truncates inside tokens. Here it falls
// out of the arithmetic.
//
// Separators are part of the token that follows them (or, for a prefix, the
// "/" after it), so a token that goes takes its separator with it and a row
// never ends in a dangling middle dot.
//
// Some tokens are context for the name rather than facts of their own: the
// prefix in front of an agent's name and the branch after a session's. They
// are offered their cells last, and only while the name is drawn whole beside
// them, because "claude/depl…" says less than "deploy" and a branch with its
// name cut from under it says nothing. See railToken.Whole.
//
// It costs nothing at rest: it is plain integer arithmetic on widths the row
// has already measured, run only while a row is being drawn.

// railToken is one token of a rail row, as the budget sees it.
type railToken struct {
	// Cost is the token's width in cells, with the separator or gap it brings.
	// A token that costs nothing is not on the row and is never kept.
	Cost int
	// Whole says the token is context for the name: it is kept only while the
	// name is drawn whole beside it, and it is offered its cells after every
	// other token.
	Whole bool
	// Right says the token sits in the row's right-hand slot. The slot holds
	// its figures one cell off the rail's edge rule, and that inset is paid
	// once, by the first figure the row keeps. Cost counts the blank in front
	// of the figure.
	Right bool
}

// railNameFloor is the fewest cells of a name a token beside it may leave it.
// A name is read by its start, and eight cells is the start of every generated
// name ("Terminal") and the whole of most chosen ones.
const railNameFloor = 8

// railNameKeep is the cells a row owes a name before any token beside it is
// kept: all of a short one, and railNameFloor of a long one plus the cell its
// ellipsis takes.
func railNameKeep(nameW int) int {
	return min(nameW, railNameFloor+1)
}

// railRowFit lays out one rail row.
//
// nameW is the name's own width and keepW the cells it holds while the tokens
// are placed, usually railNameKeep(nameW). A row with no name to cut passes 0
// for both. tokens are in the order they give way: the first gives way first.
// For most rows that is the order they are drawn in, which is survival from
// the right. avail is every cell between the row's spine and its right edge,
// with no figure taken off.
//
// It reports which tokens the row keeps, and how many cells the name may
// take. A kept token is drawn whole. nameRoom is what is left once the kept
// tokens are paid for, which is at least min(nameW, keepW, avail).
func railRowFit(nameW, keepW int, tokens []railToken, avail int) (keep []bool, nameRoom int) {
	keep = make([]bool, len(tokens))
	avail = max(avail, 0)
	reserve := min(nameW, keepW, avail)
	if nameW > 0 && avail > 0 {
		reserve = max(reserve, 1)
	}
	used := 0
	inset := false
	cost := func(tk railToken) int {
		if tk.Right && !inset {
			return tk.Cost + 1
		}
		return tk.Cost
	}
	take := func(i int, c int) {
		keep[i] = true
		used += c
		inset = inset || tokens[i].Right
	}
	// The tokens that are facts of their own, against the name's keep.
	for i := len(tokens) - 1; i >= 0; i-- {
		tk := tokens[i]
		if tk.Cost <= 0 || tk.Whole {
			continue
		}
		if c := cost(tk); reserve+used+c <= avail {
			take(i, c)
		}
	}
	// Then the tokens that are context for the name, against all of it.
	for i := len(tokens) - 1; i >= 0; i-- {
		tk := tokens[i]
		if tk.Cost <= 0 || !tk.Whole {
			continue
		}
		if c := cost(tk); nameW+used+c <= avail {
			take(i, c)
		}
	}
	return keep, avail - used
}

// railRowFitRight is railRowFit for the common row: a name and one figure in
// the right-hand slot, such as a window count or the word saying a machine is
// not answering. rightW is the figure's own width. It reports whether the
// figure is kept and the cells the name may take.
func railRowFitRight(nameW, rightW, avail int) (keepRight bool, nameRoom int) {
	if rightW <= 0 {
		return false, max(avail, 1)
	}
	keep, room := railRowFit(nameW, railNameKeep(nameW), []railToken{{Cost: rightW + 1, Right: true}}, avail)
	return keep[0], max(room, 1)
}

// sidebarFigureCost is what a figure in the right-hand slot costs the row
// beside its inset: its width and the blank in front of it, or nothing when
// there is no figure.
func sidebarFigureCost(figure string) int {
	if figure == "" {
		return 0
	}
	return lipgloss.Width(figure) + 1
}

// sidebarJoinFigures draws the figures a row's budget kept in its right-hand
// slot, in order, one blank apart. keep is railRowFit's answer for them.
func sidebarJoinFigures(figures []string, keep []bool, style lipgloss.Style) string {
	var kept []string
	for i, f := range figures {
		if f != "" && keep[i] {
			kept = append(kept, f)
		}
	}
	var b strings.Builder
	for i, f := range kept {
		if i < len(kept)-1 {
			f += " "
		}
		b.WriteString(style.Render(f))
	}
	return b.String()
}
