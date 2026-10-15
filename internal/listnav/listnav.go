// Package listnav is the one rule every vertical list in the TUI moves by.
//
// A list asks this package where the cursor goes, and the keys that move it are read in one
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

import (
	"unicode"
	"unicode/utf8"
)

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
	scroll = min(selected, min(max(scroll, 0), n-visible))
	if selected >= scroll+visible {
		scroll = selected - visible + 1
	}
	return min(max(scroll, 0), n-visible)
}

// Keymap is the keys a list moves on: keys for every list, letters for a list
// with no filter to type into. A nil *Keymap reads the defaults. Build one
// with NewKeymap.
type Keymap struct {
	keys    map[string]Motion
	letters map[string]Motion
}

// defaults are the keys a list moves on when nothing has been configured:
// the same ones [keybindings.lists] starts with.
var defaults = map[Motion][]string{
	Up:       {"up", "ctrl+p", "k"},
	Down:     {"down", "ctrl+n", "j"},
	PageUp:   {"pgup"},
	PageDown: {"pgdown"},
	Home:     {"home", "g"},
	End:      {"end", "G"},
}

var defaultKeymap = NewKeymap(nil)

// NewKeymap builds the keys each motion answers to. A motion with no keys has
// none: it is how a key is handed back. A motion left out of bindings keeps its
// default keys.
//
// A key that is one printable character is a letter key, read only by a list
// with no filter to type into; any other key is read by every list.
//
// A key two motions both claim goes to the configured one over a default, and
// otherwise to the later motion. Both passes walk the motions in order, so the
// answer never depends on map iteration.
func NewKeymap(bindings map[Motion][]string) *Keymap {
	k := &Keymap{keys: map[string]Motion{}, letters: map[string]Motion{}}
	for _, configured := range [...]bool{false, true} {
		for m := Up; m <= End; m++ {
			keys, ok := bindings[m]
			if ok != configured {
				continue
			}
			if !ok {
				keys = defaults[m]
			}
			for _, key := range keys {
				k.add(key, m)
			}
		}
	}
	return k
}

// add binds one key to m. An upper-case letter is also read as shift and the
// lower-case letter, the spelling some terminals send it as.
func (k *Keymap) add(key string, m Motion) {
	if !isLetter(key) {
		k.keys[key] = m
		return
	}
	k.letters[key] = m
	if r, _ := utf8.DecodeRuneInString(key); unicode.IsUpper(r) {
		k.letters["shift+"+string(unicode.ToLower(r))] = m
	}
}

// isLetter reports whether key is a single printable character.
func isLetter(key string) bool {
	r, n := utf8.DecodeRuneInString(key)
	return n == len(key) && r != utf8.RuneError && unicode.IsPrint(r) && r != ' '
}

// Keys reads the list movement keys. With letters set it also reads the
// letter keys (k, j, g and G by default), for a list that does not type into a
// filter; a list with a filter needs its letters for the filter.
//
// The spelling is Bubble Tea's key string. The keys are those of
// [keybindings.lists]; ctrl+p and ctrl+n are among the defaults because the
// palette and the pickers already answered to them, and a list that moved on
// them in one place and not another was the drift this package exists to end.
func (k *Keymap) Keys(key string, letters bool) Motion {
	if k == nil {
		k = defaultKeymap
	}
	if m, ok := k.keys[key]; ok {
		return m
	}
	if letters {
		return k.letters[key]
	}
	return None
}

// Keys reads the list movement keys under the default keymap.
func Keys(key string, letters bool) Motion {
	return defaultKeymap.Keys(key, letters)
}
