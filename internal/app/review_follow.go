package app

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// Following the agent that writes. While follow is on, a live read that
// brings a hunk the review has not shown before puts the cursor on its
// header, in whatever file it is: the newest of the new hunks, the working
// agent's when there are several. It is on when the review opens while an
// agent works in the tree, and f turns it on and off.
//
// Any move the person makes turns it off, and the status line says so: the
// review never takes the cursor from someone who is reading. With follow off
// the new hunks are counted in the header ("2 new hunks ]"), which a click
// or ] goes to, and each new hunk's header is marked until the cursor has
// been on it.

// reviewFollow is follow's state.
type reviewFollow struct {
	on bool
	// seen are the hunks the review has shown, by reviewHunkKey, and fresh
	// those shown since the person was last on them.
	seen, fresh map[string]bool
	// span is where the header's count of new hunks is, for the mouse.
	x0, x1 int
}

// reviewFollowOff is what the status line says when a move turns follow off.
const reviewFollowOff = "Follow off: you moved. f follows again."

// reviewHunkKey names a hunk by its file and its changed lines, so a hunk
// that moved down the file because lines were added above it is the same.
func reviewHunkKey(path string, h review.Hunk) string {
	var b strings.Builder
	b.WriteString(path)
	for _, l := range h.Lines {
		if l.Op != "context" {
			b.WriteByte(0)
			b.WriteString(l.Op)
			b.WriteString(l.Text)
		}
	}
	return b.String()
}

// reviewFollowOpen starts follow for a review that just opened: every hunk
// is seen, and follow is on when an agent works in the tree.
func (m *OS) reviewFollowOpen() {
	r := &m.review
	r.follow = reviewFollow{seen: map[string]bool{}, fresh: map[string]bool{}}
	if r.diff == nil {
		return
	}
	for _, f := range r.diff.Files {
		for _, h := range f.Hunks {
			r.follow.seen[reviewHunkKey(f.Path, h)] = true
		}
	}
	for _, a := range r.diff.Authors {
		if a.Working {
			r.follow.on = true
		}
	}
}

// reviewFollowRead takes a live read's hunks: the ones not seen before are
// fresh, and with follow on the cursor goes to the newest of them.
func (m *OS) reviewFollowRead() {
	r := &m.review
	if r.diff == nil || r.follow.seen == nil {
		return
	}
	type found struct {
		file, hunk int
		working    bool
	}
	var fresh []found
	for fi, e := range r.files {
		if e.file == nil {
			continue
		}
		for hi, h := range e.file.Hunks {
			k := reviewHunkKey(e.path, h)
			if r.follow.seen[k] {
				continue
			}
			r.follow.seen[k] = true
			// A hunk the filter hides is not new to the person.
			if !r.reviewHunkShown(h) {
				continue
			}
			r.follow.fresh[k] = true
			main, _ := reviewMainOrigin(h.Lines)
			a, ok := r.reviewAuthorOf(main)
			fresh = append(fresh, found{fi, hi, ok && a.Working})
		}
	}
	if !r.follow.on || len(fresh) == 0 || r.editor != nil {
		return
	}
	to := fresh[len(fresh)-1]
	for _, f := range fresh {
		if f.working {
			to = f
		}
	}
	m.reviewGoToHunk(to.file, to.hunk)
}

// reviewGoToHunk puts the cursor on the header of hunk h of file f.
func (m *OS) reviewGoToHunk(f, h int) {
	r := &m.review
	if f != r.file {
		r.file, r.cursor, r.scroll = f, 0, 0
	}
	r.listFocus = false
	for i, row := range m.reviewRows(m.reviewRowsWidth()) {
		if row.kind == reviewRowHunk && row.hunk == h {
			r.cursor = i
			return
		}
	}
}

// ReviewToggleFollow turns follow on or off.
func (m *OS) ReviewToggleFollow() {
	r := &m.review
	r.follow.on = !r.follow.on
	if r.follow.on {
		m.ReviewNextNew()
	}
}

// reviewPersonMoved is every move the person makes: it turns follow off,
// and the hunk the cursor lands on is no longer new.
func (m *OS) reviewPersonMoved() {
	r := &m.review
	if r.follow.on {
		r.follow.on = false
		m.reviewNotify(reviewFollowOff, "info", m.Settings.NotificationDuration)
	}
}

// reviewMoved is reviewPersonMoved and reviewSeenUnderCursor, which every
// move of the person's ends with.
func (m *OS) reviewMoved() {
	m.reviewPersonMoved()
	m.reviewSeenUnderCursor()
}

// reviewSeenUnderCursor marks the hunk under the cursor as seen.
func (m *OS) reviewSeenUnderCursor() {
	r := &m.review
	row, ok := m.reviewRowUnderCursor()
	e := r.currentFile()
	if !ok || e == nil || e.file == nil || row.hunk < 0 || row.hunk >= len(e.file.Hunks) || len(r.follow.fresh) == 0 {
		return
	}
	delete(r.follow.fresh, reviewHunkKey(e.path, e.file.Hunks[row.hunk]))
}

// ReviewNextNew goes to the next new hunk after the cursor, in file order,
// and reports whether there was one.
func (m *OS) ReviewNextNew() bool {
	r := &m.review
	if len(r.follow.fresh) == 0 {
		return false
	}
	row, _ := m.reviewRowUnderCursor()
	start := r.file
	for step := range len(r.files) + 1 {
		fi := (start + step) % len(r.files)
		e := r.files[fi]
		if e.file == nil {
			continue
		}
		for hi, h := range e.file.Hunks {
			if step == 0 && hi <= row.hunk {
				continue
			}
			if r.follow.fresh[reviewHunkKey(e.path, h)] && r.reviewHunkShown(h) {
				m.reviewGoToHunk(fi, hi)
				m.reviewSeenUnderCursor()
				return true
			}
		}
	}
	return false
}

// reviewFreshCount is how many hunks are new to the person.
func (r *reviewState) reviewFreshCount() int { return len(r.follow.fresh) }

// reviewFreshWords is the header's count of new hunks.
func reviewFreshWords(n int) string {
	if n == 1 {
		return "1 new hunk ]"
	}
	return strconv.Itoa(n) + " new hunks ]"
}

// reviewHunkFresh reports whether hunk h of the file at path is new.
func (r *reviewState) reviewHunkFresh(path string, h review.Hunk) bool {
	return len(r.follow.fresh) > 0 && r.follow.fresh[reviewHunkKey(path, h)]
}

// reviewFollowing is the agent follow follows, empty when follow is off.
func (r *reviewState) reviewFollowing() string {
	if !r.follow.on {
		return ""
	}
	if r.diff != nil {
		for _, a := range r.diff.Authors {
			if a.Working && a.Agent != "" {
				return reviewText(a.Agent)
			}
		}
	}
	return "the agent"
}
