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
// It takes herdr's survival rule for the same problem, on the same grid, and
// replaces herdr's round-robin share of the spare cells with a rule that fits
// tokens that are never cut:
//
//  1. Survival from the right. Every token is dropped, then offered back one
//     at a time from the end of the row, and kept only if the whole of it
//     still fits. A token that does not fit is passed over and the next one is
//     still offered its cells, so one long token cannot take the width two
//     shorter ones needed. The rightmost token survives, which on an agent row
//     is the one saying what the pane is doing rather than where it is.
//
//  2. While the tokens are offered their cells, the name holds only its keep
//     (railNameKeep), not its whole width, so one long name cannot starve the
//     tokens beside it.
//
//  3. The name takes every cell the kept tokens leave. herdr hands the spare
//     cells out round-robin because it cuts inside tokens. A token here is
//     kept whole or dropped, so the name is the only thing on the row that
//     can take a spare cell.
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
// name cut from under it says nothing. They are also never passed over: once
// one goes, every one before it goes too, so a prefix never reads as a
// different prefix. "api/deploy" with the harness dropped from between them
// reads as the harness called api. See railToken.Whole.
//
// It costs nothing at rest: it is plain integer arithmetic on widths the row
// has already measured, run only while a row is being drawn.

// railToken is one token of a rail row, as the budget sees it.
type railToken struct {
	// Cost is the token's width in cells, with the separator or gap it brings.
	// A token that costs nothing is not on the row and is never kept.
	Cost int
	// Whole says the token is context for the name: it is kept only while the
	// name is drawn whole beside it, it is offered its cells after every other
	// token, and once one context token goes, every one before it goes too.
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
	return railRowFitInto(nil, nameW, keepW, tokens, avail)
}

// railRowFitInto is railRowFit writing its answer into buf's storage, so a
// caller that draws row after row can reuse one slice. See railScratch.
func railRowFitInto(buf []bool, nameW, keepW int, tokens []railToken, avail int) (keep []bool, nameRoom int) {
	keep = buf[:0]
	for range tokens {
		keep = append(keep, false)
	}
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
	// The right-hand figures first: the row's state outranks its notes.
	for i := len(tokens) - 1; i >= 0; i-- {
		tk := tokens[i]
		if tk.Cost <= 0 || !tk.Right {
			continue
		}
		if c := cost(tk); reserve+used+c <= avail {
			take(i, c)
		}
	}
	// Then the tokens that are context for the name, against all of it. The
	// first one that does not fit ends the walk: a prefix is only worth its
	// cells while the whole name it names still fits beside it.
	wholeKept := false
	for i := len(tokens) - 1; i >= 0; i-- {
		tk := tokens[i]
		if tk.Cost <= 0 || !tk.Whole {
			continue
		}
		c := cost(tk)
		if nameW+used+c > avail {
			break
		}
		take(i, c)
		wholeKept = true
	}
	// Then the tokens that are facts of their own, against what is left. A
	// kept context token holds the whole name for the name: an address the
	// switch keys re-derive any time yields before the row loses the name it
	// was for.
	floor := reserve
	if wholeKept {
		floor = nameW
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		tk := tokens[i]
		if tk.Cost <= 0 || tk.Whole || tk.Right {
			continue
		}
		if c := cost(tk); floor+used+c <= avail {
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
	var one [1]bool
	keep, room := railRowFitInto(one[:0], nameW, railNameKeep(nameW), []railToken{{Cost: rightW + 1, Right: true}}, avail)
	return keep[0], max(room, 1)
}

// railScratch is storage one renderer reuses for every row's budget, so a
// rail rebuild does not allocate a token list and an answer per row. A row
// is done with it before the next row starts.
type railScratch struct {
	tokens []railToken
	keep   []bool
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
	// Each kept figure is drawn once the next one is known, so the blank
	// between two figures is drawn in their ink and the last one has none.
	var b strings.Builder
	prev := ""
	for i, f := range figures {
		if f == "" || !keep[i] {
			continue
		}
		if prev != "" {
			b.WriteString(style.Render(prev + " "))
		}
		prev = f
	}
	if prev == "" {
		return b.String()
	}
	if b.Len() == 0 {
		return style.Render(prev)
	}
	b.WriteString(style.Render(prev))
	return b.String()
}
