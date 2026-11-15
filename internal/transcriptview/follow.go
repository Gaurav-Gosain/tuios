package transcriptview

import "slices"

// Follow is where a person reads one agent's thread, kept per agent so a
// switch away and back keeps the place. It knows lines, not cards: the
// client lays the thread out, and tells Follow how many lines it drew, how
// many rows show, and where the prompts start. The TUI and guios share it,
// so both scroll a thread by the same two rules:
//
//  1. Follow: while the view is at the bottom, new lines keep the bottom in
//     view. A new prompt is a new line like any other: it shows at the
//     bottom and pushes the older lines up, with no blank rows under it.
//     A line on screen keeps its row or moves up by the lines added under
//     it, never down.
//  2. Hold: a scroll up stops following. New lines keep the view where it
//     is, and Unseen counts the entries that arrived. Bottom, or a scroll
//     back down to the bottom, follows again and clears the count.
//
// Jump puts a prompt at the top of the view on request, as far as the
// thread allows.
//
// The zero value follows. Nothing in it runs on a timer: it changes on a
// layout, a scroll, a jump or a send.
type Follow struct {
	// Up is how many lines the view is scrolled up from the newest line. It
	// is never negative: the view never shows rows past the newest line.
	Up int
	// Unseen is how many entries arrived while the view held still.
	Unseen int

	laid        bool
	lines, room int
	taken       int
}

// Layout is the thread as the client draws it now.
type Layout struct {
	// Lines is how many lines the thread has, and Room how many rows show.
	Lines, Room int
	// Taken counts the entries the thread took since it was made. It only
	// grows, and its growth is what Unseen counts.
	Taken int
	// Prompt names the newest prompt, such as its entry id, and PromptAt is
	// the line it starts on, -1 when it is not drawn. Apply does not move
	// the view for a new prompt any more; the fields stay so a client that
	// fills them builds as before.
	Prompt   string
	PromptAt int
}

// Held reports whether the view holds still above the bottom.
func (f *Follow) Held() bool { return f.Up > 0 }

// Apply takes a new layout and moves the view by the rules. The first layout
// only records where the thread is.
func (f *Follow) Apply(l Layout) {
	first := !f.laid
	grew := l.Lines - f.lines
	taken := l.Taken - f.taken
	f.laid, f.lines, f.room, f.taken = true, l.Lines, l.Room, l.Taken
	if !first && f.Held() {
		// Rule 2: the lines above the view stay where they are.
		f.Up += grew
		if taken > 0 {
			f.Unseen += taken
		}
	}
	// Rule 1 needs nothing: Up stays 0, and the bottom stays in view.
	f.clamp()
}

// Older records n lines drawn above the oldest line, such as older turns
// laid out once the view scrolled near the top. The view stays on the lines
// it shows.
func (f *Follow) Older(n int) {
	if f.laid {
		f.lines += n
	}
}

// Scroll moves the view by delta lines, up when delta is positive. A scroll
// that reaches the bottom follows again; one that leaves it holds.
func (f *Follow) Scroll(delta int) {
	f.Up += delta
	f.clamp()
}

// Bottom shows the newest line and follows again. It reports whether the
// view held, so a key that also does something else can pass on otherwise.
func (f *Follow) Bottom() bool {
	held := f.Held() || f.Unseen > 0
	f.Up, f.Unseen = 0, 0
	return held
}

// Window is the lines to draw, [start, end).
func (f *Follow) Window() (start, end int) {
	end = f.lines - f.Up
	return max(end-f.room, 0), end
}

// Jump puts the next prompt (dir > 0) or the previous one (dir < 0) at the
// top of the view, as far as the thread allows. prompts are the lines the
// prompts start on, in order. It reports whether the view moved.
func (f *Follow) Jump(prompts []int, dir int) bool {
	start, _ := f.Window()
	at := -1
	if dir > 0 {
		for _, p := range prompts {
			if p > start {
				at = p
				break
			}
		}
	} else {
		for _, p := range slices.Backward(prompts) {
			if p < start {
				at = p
				break
			}
		}
	}
	if at < 0 {
		return false
	}
	before := f.Up
	f.Up = max(f.lines-at-f.room, 0)
	f.clamp()
	if f.Up == 0 {
		f.Unseen = 0
	}
	return f.Up != before
}

// clamp keeps the view on the thread: no higher than its first line, and
// no lower than its last.
func (f *Follow) clamp() {
	f.Up = max(min(f.Up, f.lines-f.room), 0)
	if f.Up == 0 {
		f.Unseen = 0
	}
}

// RulerMarks maps the lines prompts start on to rows of a ruler rows tall
// that stands for a thread of lines lines. Two prompts on one row share a
// mark.
func RulerMarks(prompts []int, lines, rows int) []int {
	if lines <= 0 || rows <= 0 {
		return nil
	}
	var out []int
	for _, p := range prompts {
		r := min(max(p*rows/lines, 0), rows-1)
		if n := len(out); n == 0 || out[n-1] != r {
			out = append(out, r)
		}
	}
	return out
}
