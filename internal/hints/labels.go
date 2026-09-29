package hints

import (
	"slices"
	"strings"
	"unicode"
)

// DefaultAlphabet is the home row, which is where tmux-fingers and kitty's
// hints kitten both start. Nine letters give nine one-key labels and
// eighty-one two-key ones.
const DefaultAlphabet = "asdfghjkl"

// NormalizeAlphabet makes a label alphabet usable: lowercase ASCII letters
// only, each once, in the order given. It returns the default when fewer than
// two letters are left, because one letter cannot tell two matches apart.
//
// Letters only, and lowercase only, because the keys around a label have
// meanings of their own: Shift on a letter types the match, Ctrl on a letter
// opens it, and a digit or a symbol would need a Shift of its own on many
// layouts.
func NormalizeAlphabet(alphabet string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(alphabet) {
		if r > unicode.MaxASCII || r < 'a' || r > 'z' || strings.ContainsRune(b.String(), r) {
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() < 2 {
		return DefaultAlphabet
	}
	return b.String()
}

// Labels returns n labels drawn from alphabet, shortest first, none of them
// the start of another, so typing a label ends the moment it is complete.
//
// Every letter starts as a label of its own. While there are too few, the
// last of the shortest labels is taken apart into one longer label per
// letter. Each split costs one short label and adds len(alphabet) longer
// ones, so the labels stay as short as they can for the count, and the split
// eats the alphabet from its end, so the first letters (the easiest on the
// home row) stay one key long for as long as possible.
func Labels(n int, alphabet string) []string {
	if n <= 0 {
		return nil
	}
	alphabet = NormalizeAlphabet(alphabet)
	leaves := make([]string, 0, max(n, len(alphabet)))
	for _, r := range alphabet {
		leaves = append(leaves, string(r))
	}
	for len(leaves) < n {
		// leaves is ordered by length, so the shortest ones are at the front
		// and the last of them sits right before the first longer one.
		short := len(leaves[0])
		idx := 0
		for idx+1 < len(leaves) && len(leaves[idx+1]) == short {
			idx++
		}
		prefix := leaves[idx]
		leaves = slices.Delete(leaves, idx, idx+1)
		for _, r := range alphabet {
			leaves = append(leaves, prefix+string(r))
		}
	}
	rank := func(label string) []int {
		out := make([]int, 0, len(label))
		for _, c := range label {
			out = append(out, strings.IndexRune(alphabet, c))
		}
		return out
	}
	slices.SortStableFunc(leaves, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return slices.Compare(rank(a), rank(b))
	})
	return leaves[:n]
}

// Target is one match on the screen, as label assignment sees it: its text,
// and how far it is from where the person is looking.
type Target struct {
	Text     string
	Distance int
	// Key, when set, is what makes two targets the same thing to type.
	// Targets with the same key share a label. An empty key is the text, so
	// by default the same text gets the same label. A path shown on two panes
	// can name two files, and a caller gives each pane's path its own key.
	Key string
}

// Assign gives every target a label and returns them in the targets' order.
//
// The same text gets the same label wherever it appears, so a hash printed
// twice is one thing to type, not two. A target's Key, when set, stands in
// for its text here. The texts nearest the person get the
// shortest labels: a text's distance is that of its nearest occurrence, and a
// tie goes to the text seen first.
func Assign(targets []Target, alphabet string) []string {
	type text struct {
		value    string
		distance int
		first    int
	}
	byText := map[string]*text{}
	var order []*text
	key := func(t Target) string {
		if t.Key != "" {
			return t.Key
		}
		return t.Text
	}
	for i, t := range targets {
		k := key(t)
		if e, ok := byText[k]; ok {
			e.distance = min(e.distance, t.Distance)
			continue
		}
		e := &text{value: k, distance: t.Distance, first: i}
		byText[k] = e
		order = append(order, e)
	}
	slices.SortStableFunc(order, func(a, b *text) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return a.first - b.first
	})
	labels := Labels(len(order), alphabet)
	labelOf := make(map[string]string, len(order))
	for i, e := range order {
		labelOf[e.value] = labels[i]
	}
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = labelOf[key(t)]
	}
	return out
}
