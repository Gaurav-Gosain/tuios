package app

import (
	"image/color"
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// Who made each line, as review-diff's origins say (session/review_origins.go):
// a chip at the right of each hunk's header names the turn most of its lines
// came from, a bar in the first cell of each changed line is in its author's
// colour, and the file list marks each file with its main author's colour.
// The person is "you" in the caution colour; each agent has its own colour,
// the same for all its turns.

// reviewAuthorInfo is one author of review-diff's answer.
type reviewAuthorInfo struct {
	Kind    string `json:"kind"`
	Agent   string `json:"agent"`
	Pane    string `json:"pane"`
	Session string `json:"session"`
	Turn    uint64 `json:"turn"`
	Working bool   `json:"working"`
	At      int64  `json:"at"`
}

// reviewAuthorOf is the author a line's origin names, and whether it names
// one.
func (r *reviewState) reviewAuthorOf(origin int) (reviewAuthorInfo, bool) {
	if r.diff == nil || origin < 1 || origin > len(r.diff.Authors) {
		return reviewAuthorInfo{}, false
	}
	return r.diff.Authors[origin-1], true
}

// reviewAuthorInk is the colour of author origin: the person's, or the one
// its agent took in the order the agents first appear.
func (r *reviewState) reviewAuthorInk(origin int, pal overlay.Palette) color.Color {
	a, ok := r.reviewAuthorOf(origin)
	if !ok || a.Kind != "agent" {
		return pal.Warning
	}
	var names []string
	for _, b := range r.diff.Authors {
		if b.Kind == "agent" && !slices.Contains(names, b.Agent) {
			names = append(names, b.Agent)
		}
	}
	inks := []color.Color{pal.AccentBright, pal.Info, pal.Busy, pal.Attention}
	return inks[slices.Index(names, a.Agent)%len(inks)]
}

// reviewAuthorBar is the bar a changed line shows in its author's colour.
func reviewAuthorBar() string {
	if overlay.UseASCII() {
		return "|"
	}
	return "▎"
}

// reviewMainOrigin is the origin most of lines' added lines have, or of its
// removed ones when it adds none, and the next most common, 0 for none.
func reviewMainOrigin(lines []review.Line) (main, second int) {
	count := map[int]int{}
	for _, op := range []string{"add", "delete"} {
		for _, l := range lines {
			if l.Op == op && l.Origin > 0 {
				count[l.Origin]++
			}
		}
		if len(count) > 0 {
			break
		}
	}
	for o, n := range count {
		if main == 0 || n > count[main] || n == count[main] && o < main {
			main = o
		}
	}
	for o, n := range count {
		if o != main && (second == 0 || n > count[second] || n == count[second] && o < second) {
			second = o
		}
	}
	return main, second
}

// reviewFileOrigin is the main author of a file's changes.
func reviewFileOrigin(f *review.File) int {
	if f == nil {
		return 0
	}
	var all []review.Line
	for _, h := range f.Hunks {
		all = append(all, h.Lines...)
	}
	main, _ := reviewMainOrigin(all)
	return main
}

// reviewAuthorWords is how a hunk's chip names one author: "claude · turn 4 ·
// 2m ago", "claude · turn 5 · writing", or "you · not in a turn".
func (r *reviewState) reviewAuthorWords(origin int, now time.Time) (name, rest string) {
	a, ok := r.reviewAuthorOf(origin)
	if !ok || a.Kind != "agent" {
		return "you", " · not in a turn"
	}
	rest = " · turn " + strconv.FormatUint(a.Turn, 10)
	switch {
	case a.Working:
		rest += " · writing"
	case a.At > 0:
		rest += " · " + units.Waited(a.At, now) + " ago"
	}
	return reviewText(a.Agent), rest
}

// reviewHunkChip is the chip at the right of a hunk's header, nil when the
// review has no origins.
func (r *reviewState) reviewHunkChip(h review.Hunk, pal overlay.Palette, bg color.Color, now time.Time) []reviewSeg {
	main, second := reviewMainOrigin(h.Lines)
	if main == 0 {
		return nil
	}
	name, rest := r.reviewAuthorWords(main, now)
	segs := []reviewSeg{{overlay.Readable(r.reviewAuthorInk(main, pal), bg), name, true}, {overlay.Readable(pal.FgDim, bg), rest, false}}
	if second != 0 {
		n2, _ := r.reviewAuthorWords(second, now)
		segs = append(segs, reviewSeg{overlay.Readable(pal.FgDim, bg), " + ", false}, reviewSeg{overlay.Readable(r.reviewAuthorInk(second, pal), bg), n2, true})
	}
	return segs
}
