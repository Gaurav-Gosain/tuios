package text

import (
	"slices"
	"strings"

	"tuios.dev/sketch"
)

// A git graph is drawn the way git log --graph draws one: the history runs
// down, oldest first, each branch is a column with its name at the top, and
// each commit is a row with its id and tags right of the lanes. A line across
// a commit's row joins it to a parent on another branch. Each branch takes
// a series colour, as pele's do: its lane, its name, its commits and the
// lines that join it to other branches.

var commitMarks = map[string]rune{
	"normal":      '●',
	"reverse":     '×',
	"highlight":   '■',
	"merge":       '○',
	"cherry-pick": '◆',
}

func drawGit(l *sketch.Layout, opt Options) (*canvas, error) {
	g := l.Git
	if g == nil {
		return nil, ErrUnsupported
	}
	lanes := len(g.Lanes)
	// Commits that share a place stay in the order they were made.
	order := make([]int, len(g.Commits))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		pa, pb := g.Commits[a].Pos, g.Commits[b].Pos
		switch {
		case pa < pb:
			return -1
		case pa > pb:
			return 1
		}
		return 0
	})
	row := make([]int, len(g.Commits))
	texts := make([]string, len(g.Commits))
	graphW := 2*lanes - 1
	wd := max(graphW, widest(g.Title))
	titleH := 0
	if len(g.Title) > 0 {
		titleH = len(g.Title) + 1
	}
	// One row for each branch name, then one for each commit.
	top := titleH + lanes
	for r, i := range order {
		c := g.Commits[i]
		row[i] = top + r
		parts := []string{}
		if len(c.Label) > 0 {
			parts = append(parts, strings.Join(c.Label, " "))
		}
		for _, tag := range c.Tags {
			parts = append(parts, "["+strings.Join(tag, " ")+"]")
		}
		texts[i] = strings.Join(parts, " ")
		if texts[i] != "" {
			wd = max(wd, graphW+2+width(texts[i]))
		}
	}
	for k, name := range g.Lanes {
		if name != "" {
			wd = max(wd, 2*k+2+width(name))
		}
	}
	if opt.Cols > 0 && wd > opt.Cols {
		return nil, ErrTooWide
	}
	// The lanes run one row past the last commit, as pele's run past it.
	c, err := newCanvas(max(wd, 1), top+len(order)+1)
	if err != nil {
		return nil, err
	}
	for i, line := range g.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	bottom := top + len(order)
	for k, name := range g.Lanes {
		y := titleH + k
		c.vline(2*k, y, max(bottom, y), dotted, seriesRole(k))
		if name != "" {
			c.hline(2*k, 2*k+1, y, dotted, seriesRole(k))
			c.text(2*k+2, y, name, seriesRole(k))
		}
	}
	for i, commit := range g.Commits {
		for _, p := range commit.Parents {
			if parent := g.Commits[p]; parent.Lane != commit.Lane {
				c.hline(2*parent.Lane, 2*commit.Lane, row[i], light, seriesRole(commit.Lane))
			}
		}
	}
	for i, commit := range g.Commits {
		mark, ok := commitMarks[commit.Kind]
		if !ok {
			mark = '●'
		}
		c.put(2*commit.Lane, row[i], mark, seriesRole(commit.Lane))
		c.text(graphW+2, row[i], texts[i], roleText)
	}
	return c, nil
}
