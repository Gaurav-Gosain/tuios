// Package listnav is the one rule every vertical list in the TUI moves by.
//
// Each list used to carry its own copy of "add delta and clamp", and the
// copies had drifted: the context menu and the tape manager wrapped at the
// ends, the settings page, the palette and the pickers stopped, and home, end
// and the page keys worked in two lists out of twenty. A list now asks this
// package where the cursor goes, and the keys that move it are read in one
// place (Motion), so the rule is the same wherever a person meets it.
//
// The rule:
//   - One step off either end wraps to the other end when wrapping is on
//     (appearance.wrap_lists, on by default). Up on the first row lands on the
//     last, and down on the last lands on the first.
//   - Any longer move, a page or a jump to an end, stops at the end. A page
//     that wrapped would put the cursor somewhere the eye cannot follow.
//   - A list with rows the cursor cannot rest on (a heading, a separator)
//     steps over them, and wrapping steps over them too.
package listnav

// Motion is a movement a key asks a list for.
type Motion int

// The motions a list understands.
const (
	None Motion = iota
	Up
	Down
	PageUp
	PageDown
	Home
	End
)

// jump is the delta Home and End stand for: further than any list is long, so
// it clamps to the end rather than wrapping.
const jump = 1 << 30

// Delta is the delta Step takes for a motion, given how many rows a page is.
func Delta(m Motion, page int) int {
	page = max(page, 1)
	switch m {
	case Up:
		return -1
	case Down:
		return 1
	case PageUp:
		return -page
	case PageDown:
		return page
	case Home:
		return -jump
	case End:
		return jump
	}
	return 0
}

// Step is where the cursor at cur lands after moving delta rows in a list of
// n. A single step off either end wraps when wrap is set; anything longer
// clamps. An empty list has nowhere to go and answers 0.
func Step(cur, delta, n int, wrap bool) int {
	return StepSkip(cur, delta, n, wrap, nil)
}

// StepSkip is Step for a list with rows the cursor cannot rest on. rest
// reports whether row i can hold the cursor; nil means every row can.
//
// A move of k rows is k moves to the next restable row. A single step that
// finds nothing restable before the end wraps to the first restable row from
// the other end, and a longer move stops on the last restable row it reached.
// When no row is restable the cursor stays where it is.
func StepSkip(cur, delta, n int, wrap bool, rest func(int) bool) int {
	if n <= 0 {
		return 0
	}
	cur = min(max(cur, 0), n-1)
	if delta == 0 {
		return cur
	}
	ok := func(i int) bool { return rest == nil || rest(i) }
	dir := 1
	count := delta
	if delta < 0 {
		dir, count = -1, -delta
	}
	single := count == 1

	if rest == nil {
		next := cur + delta
		if single && wrap {
			return ((next % n) + n) % n
		}
		return min(max(next, 0), n-1)
	}

	at := cur
	for range min(count, n) {
		next := at + dir
		for next >= 0 && next < n && !ok(next) {
			next += dir
		}
		if next < 0 || next >= n {
			if !single || !wrap {
				break
			}
			// Off the end: come in from the other end to its first row that
			// can hold the cursor.
			next = 0
			if dir < 0 {
				next = n - 1
			}
			for next >= 0 && next < n && !ok(next) {
				next += dir
			}
			if next < 0 || next >= n {
				break
			}
		}
		at = next
	}
	return at
}

// Scroll keeps the row at selected inside a window of visible rows starting
// at scroll, and returns the window's new start. It moves the window as
// little as it can, so a wrap from the last row to the first puts the window
// at the top and a wrap the other way puts it at the bottom.
func Scroll(scroll, selected, n, visible int) int {
	if visible <= 0 || n <= visible {
		return 0
	}
	scroll = min(max(scroll, 0), n-visible)
	if selected < scroll {
		scroll = selected
	}
	if selected >= scroll+visible {
		scroll = selected - visible + 1
	}
	return min(max(scroll, 0), n-visible)
}

// Keys reads the list movement keys. With letters set it also reads k, j, g
// and G, for a list that does not type into a filter; a list with a filter
// needs its letters for the filter.
//
// The spelling is Bubble Tea's key string. ctrl+p and ctrl+n are read here
// because the palette and the pickers already answered to them, and a list
// that moved on them in one place and not another was the drift this package
// exists to end.
func Keys(key string, letters bool) Motion {
	switch key {
	case "up", "ctrl+p":
		return Up
	case "down", "ctrl+n":
		return Down
	case "pgup":
		return PageUp
	case "pgdown":
		return PageDown
	case "home":
		return Home
	case "end":
		return End
	}
	if !letters {
		return None
	}
	switch key {
	case "k":
		return Up
	case "j":
		return Down
	case "g":
		return Home
	case "G", "shift+g":
		return End
	}
	return None
}
