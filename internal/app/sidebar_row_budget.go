package app

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

type railToken struct {
	Cost  int
	Whole bool
	Right bool
}

const railNameFloor = 8

func railNameKeep(nameW int) int {
	return min(nameW, railNameFloor+1)
}

func railRowFit(nameW, keepW int, tokens []railToken, avail int) (keep []bool, nameRoom int) {
	return railRowFitInto(nil, nameW, keepW, tokens, avail)
}

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
	for i, tk := range slices.Backward(tokens) {
		if tk.Cost <= 0 || tk.Whole {
			continue
		}
		if c := cost(tk); reserve+used+c <= avail {
			take(i, c)
		}
	}
	for i, tk := range slices.Backward(tokens) {
		if tk.Cost <= 0 || !tk.Whole {
			continue
		}
		c := cost(tk)
		if nameW+used+c > avail {
			break
		}
		take(i, c)
	}
	return keep, avail - used
}

func railRowFitRight(nameW, rightW, avail int) (keepRight bool, nameRoom int) {
	if rightW <= 0 {
		return false, max(avail, 1)
	}
	var one [1]bool
	keep, room := railRowFitInto(one[:0], nameW, railNameKeep(nameW), []railToken{{Cost: rightW + 1, Right: true}}, avail)
	return keep[0], max(room, 1)
}

type railScratch struct {
	tokens []railToken
	keep   []bool
}

func sidebarFigureCost(figure string) int {
	if figure == "" {
		return 0
	}
	return lipgloss.Width(figure) + 1
}

func sidebarJoinFigures(figures []string, keep []bool, style lipgloss.Style) string {
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
