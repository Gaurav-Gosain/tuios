package review

import "strconv"

// Author is one entry of a review's authors, which every line's Origin
// indexes, plus one. The daemon builds the list (session/review_origins.go)
// and the review view and the CLI read it, so a hunk names its author the
// same way everywhere.
type Author struct {
	// Kind is "person" or "agent". A person with a turn is the person while
	// that turn of the pane ran.
	Kind string `json:"kind"`
	// Agent names the agent: its harness, or the pane's title.
	Agent   string `json:"agent,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Session string `json:"session,omitempty"`
	// Turn is the pane's turn the lines came from. Working marks the turn
	// running now, which has no checkpoint yet.
	Turn    uint64 `json:"turn,omitempty"`
	Working bool   `json:"working,omitempty"`
	// At is when the turn's checkpoint was taken, in Unix nanoseconds.
	At int64 `json:"at,omitempty"`
}

// MainOrigin is the origin most of lines' added lines have, or of its
// removed ones when it adds none, and the next most common, 0 for none. A
// tie goes to the lower origin.
func MainOrigin(lines []Line) (main, second int) {
	count := map[int]int{}
	for _, op := range []string{OpAdd, OpDelete} {
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

// HunkUnsure reports whether a line of h credited to origin is marked
// unsure.
func HunkUnsure(h Hunk, origin int) bool {
	for _, l := range h.Lines {
		if l.Origin == origin && l.Unsure {
			return true
		}
	}
	return false
}

// AuthorWords is how a hunk names the author origin indexes in authors:
// "claude · turn 4", "claude · turn 4 ?" for an unsure turn, "you · during
// turn 4" for a line the person wrote while a turn ran, "you ?" for a line an
// agent's tool may have made, or "you · not in a turn". name is the agent as
// its pane reports it, so a caller that draws it cleans it first. The time of
// the turn is left to the caller, which knows the time now.
func AuthorWords(authors []Author, origin int, unsure bool) (name, rest string) {
	ok := origin >= 1 && origin <= len(authors)
	var a Author
	if ok {
		a = authors[origin-1]
	}
	if !ok || a.Kind != "agent" {
		switch {
		case unsure:
			return "you", " ?"
		case ok && a.Turn > 0:
			return "you", " · during turn " + strconv.FormatUint(a.Turn, 10)
		}
		return "you", " · not in a turn"
	}
	rest = " · turn " + strconv.FormatUint(a.Turn, 10)
	if unsure {
		rest += " ?"
	}
	return a.Agent, rest
}

// NameHunks sets each hunk's Origin, Also, Unsure and Who from its lines'
// origins, as the review view's chip shows them. A hunk with no origins is
// left as it is.
func NameHunks(files []File, authors []Author) {
	for i := range files {
		for j := range files[i].Hunks {
			h := &files[i].Hunks[j]
			main, second := MainOrigin(h.Lines)
			if main == 0 {
				continue
			}
			h.Origin, h.Also = main, second
			h.Unsure = HunkUnsure(*h, main)
			name, rest := AuthorWords(authors, main, h.Unsure)
			h.Who = name + rest
			if second != 0 {
				n2, _ := AuthorWords(authors, second, false)
				h.Who += " + " + n2
			}
		}
	}
}
