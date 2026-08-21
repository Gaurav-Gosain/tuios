// Package fuzzy scores fuzzy string matches and reports where they landed.
//
// Scoring follows fzf: a character earns points for landing on a word boundary,
// after a separator, or on a camelCase hump, and the alignment pays for every
// character it skips. Fingers trained on fzf get the ordering they expect,
// which matters most when the corpus is large enough that only the first row is
// ever read.
//
// Every result carries the matched byte offsets as well as the score, so a UI
// can rank a thousand candidates and still underline the hit.
package fuzzy

import (
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Scoring weights, taken from fzf so the ordering matches the tool most
// people's muscle memory was trained on.
const (
	scoreMatch        = 16
	scoreGapStart     = -3
	scoreGapExtension = -1

	// A character right after a boundary is worth half a match. Whitespace and
	// explicit delimiters are stronger boundaries than plain punctuation.
	bonusBoundary          = scoreMatch / 2
	bonusBoundaryWhite     = bonusBoundary + 2
	bonusBoundaryDelimiter = bonusBoundary + 1
	bonusNonWord           = scoreMatch / 2
	bonusCamel123          = bonusBoundary + scoreGapExtension

	// Staying consecutive must be worth exactly what reopening a gap costs,
	// otherwise the same characters spread out could beat an unbroken run.
	bonusConsecutive = -(scoreGapStart + scoreGapExtension)

	// The first character sets the tone of the match, so its bonus counts
	// double. This is what lifts a prefix hit over a mid-word one.
	bonusFirstCharMultiplier = 2

	// bonusPrefix rewards an unbroken run starting at offset zero, and
	// bonusExact stacks on top when the candidate is nothing but the pattern.
	// fzf leaves both to its tiebreakers; a launcher ranking gcc against
	// git-credential-cache for "gc" wants that margin in the score itself,
	// because the caller doing the ranking may not tiebreak at all.
	bonusPrefix = scoreMatch
	bonusExact  = scoreMatch
)

// noScore marks a cell no alignment can reach. It sits far below any reachable
// score and far above the point where accumulated gap penalties could overflow.
const noScore = int32(-1 << 28)

// charClass groups runes by the kind of boundary they create.
type charClass uint8

const (
	charWhite charClass = iota
	charNonWord
	charDelimiter
	charLower
	charUpper
	charLetter
	charNumber
)

// delimiters are the characters treated as explicit field separators. Keeping
// fzf's default set means path-like and flag-like candidates rank here the way
// they do there.
const delimiters = "/,:;|"

func classOf(r rune) charClass {
	switch {
	case r >= 'a' && r <= 'z':
		return charLower
	case r >= 'A' && r <= 'Z':
		return charUpper
	case r >= '0' && r <= '9':
		return charNumber
	case r == ' ', r == '\t', r == '\n', r == '\v', r == '\f', r == '\r':
		return charWhite
	case r < utf8.RuneSelf:
		if strings.ContainsRune(delimiters, r) {
			return charDelimiter
		}
		return charNonWord
	case unicode.IsLower(r):
		return charLower
	case unicode.IsUpper(r):
		return charUpper
	case unicode.IsNumber(r):
		return charNumber
	case unicode.IsLetter(r):
		return charLetter
	case unicode.IsSpace(r):
		return charWhite
	}
	return charNonWord
}

func bonusFor(prev, cur charClass) int32 {
	if cur > charNonWord {
		switch prev {
		case charWhite:
			return bonusBoundaryWhite
		case charDelimiter:
			return bonusBoundaryDelimiter
		case charNonWord:
			return bonusBoundary
		}
	}
	if prev == charLower && cur == charUpper || prev != charNumber && cur == charNumber {
		return bonusCamel123
	}
	switch cur {
	case charNonWord, charDelimiter:
		return bonusNonWord
	case charWhite:
		return bonusBoundaryWhite
	}
	return 0
}

func fold(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	if r < utf8.RuneSelf {
		return r
	}
	return unicode.ToLower(r)
}

// Result describes one successful match.
type Result struct {
	// Score ranks the match. Higher is better. Scores are only comparable
	// between results produced for the same pattern.
	Score int
	// Positions holds the byte offset within the candidate of every matched
	// character, ascending. It is nil when the pattern is empty.
	Positions []int
	// Start and End bound the matched region as byte offsets, End exclusive.
	Start, End int
}

// Hit is a Result paired with the candidate that produced it.
type Hit struct {
	// Index is the candidate's position in the input the caller supplied.
	Index int
	// Text is the candidate itself, carried so Sort can break ties without
	// calling back into the caller.
	Text string
	Result
}

// Matcher runs matches while reusing its scratch buffers, which is what keeps a
// per-keystroke sweep over a few thousand candidates allocation free. A Matcher
// is not safe for concurrent use; give each goroutine its own. The zero value
// is ready.
//
// Positions in a Result returned by Find point into a buffer the Matcher owns
// and overwrites on the next call. Copy them to keep them.
type Matcher struct {
	pat   []rune
	patCS bool // pattern carries an uppercase rune, so match case-sensitively

	text  []rune
	ascii bool  // candidate is pure ASCII, so rune index and byte offset agree
	offs  []int // byte offset of each rune plus a sentinel; empty when ascii
	class []charClass

	bonus []int32
	h     []int32
	c     []int32

	pos []int
}

var matcherPool = sync.Pool{New: func() any { return new(Matcher) }}

// Find scores pattern against text and reports whether it matched. The returned
// Positions belong to the caller.
//
// Matching is smart-case: an all-lowercase pattern ignores case, and a pattern
// carrying any uppercase rune matches case-sensitively.
func Find(pattern, text string) (Result, bool) {
	m := matcherPool.Get().(*Matcher)
	defer matcherPool.Put(m)
	r, ok := m.Find(pattern, text)
	if ok && r.Positions != nil {
		r.Positions = slices.Clone(r.Positions)
	}
	return r, ok
}

// Score reports the match score, or false when pattern does not match text.
func Score(pattern, text string) (int, bool) {
	m := matcherPool.Get().(*Matcher)
	defer matcherPool.Put(m)
	r, ok := m.Find(pattern, text)
	return r.Score, ok
}

// Match reports whether pattern matches text at all, ignoring the ranking. It
// is the shape a yes/no filter over an already-visible list wants.
func Match(pattern, text string) bool {
	_, ok := Score(pattern, text)
	return ok
}

// Filter scores pattern against every candidate and returns the matches ordered
// best first. An empty pattern matches every candidate, in input order.
func Filter(pattern string, texts []string) []Hit {
	m := matcherPool.Get().(*Matcher)
	defer matcherPool.Put(m)
	return m.FilterIndex(pattern, len(texts), func(i int) string { return texts[i] })
}

// FilterIndex is Filter over a caller-supplied accessor, so a caller whose
// candidates are not already a []string does not have to build one per
// keystroke.
func (m *Matcher) FilterIndex(pattern string, n int, text func(int) string) []Hit {
	hits := make([]Hit, 0, min(n, 64))
	var posBuf []int
	for i := range n {
		s := text(i)
		r, ok := m.Find(pattern, s)
		if !ok {
			continue
		}
		if r.Positions != nil {
			posBuf = append(posBuf, r.Positions...)
		}
		hits = append(hits, Hit{Index: i, Text: s, Result: r})
	}
	// posBuf reallocates as it grows, so every hit is re-sliced against the
	// final backing array once the sweep is done. Appends were in hit order, so
	// the runs sit end to end.
	off := 0
	for i := range hits {
		k := len(hits[i].Positions)
		if k == 0 {
			continue
		}
		hits[i].Positions = posBuf[off : off+k : off+k]
		off += k
	}
	// An empty pattern scores everything alike, and the caller's own order is
	// more informative than anything the tiebreak could invent.
	if pattern != "" {
		Sort(hits)
	}
	return hits
}

// Sort orders hits best first: score, then the tighter match, then the earlier
// one, then the shorter candidate, then the candidate text, then input order.
// Every term is a property of the candidate alone, so equal scores never
// shuffle between keystrokes.
func Sort(hits []Hit) {
	slices.SortFunc(hits, func(a, b Hit) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if d := (a.End - a.Start) - (b.End - b.Start); d != 0 {
			return d
		}
		if d := a.Start - b.Start; d != 0 {
			return d
		}
		if d := len(a.Text) - len(b.Text); d != 0 {
			return d
		}
		if d := strings.Compare(a.Text, b.Text); d != 0 {
			return d
		}
		return a.Index - b.Index
	})
}

// Find scores pattern against text. See Matcher for the lifetime of Positions.
func (m *Matcher) Find(pattern, text string) (Result, bool) {
	if pattern == "" {
		return Result{}, true
	}
	m.loadPattern(pattern)
	if !m.loadText(text) {
		return Result{}, false
	}
	lo, hi, ok := m.window()
	if !ok {
		return Result{}, false
	}
	return m.align(lo, hi), true
}

func (m *Matcher) loadPattern(pattern string) {
	m.pat = m.pat[:0]
	m.patCS = false
	for _, r := range pattern {
		if unicode.IsUpper(r) {
			m.patCS = true
		}
		m.pat = append(m.pat, r)
	}
	// Whether the pattern is case-sensitive is only known once it has been read
	// through, so folding happens in a second pass rather than inline.
	if !m.patCS {
		for i, r := range m.pat {
			m.pat[i] = fold(r)
		}
	}
}

// loadText decodes text into the scratch buffers, reporting false when the
// candidate is shorter than the pattern and so cannot possibly match.
//
// Character classes are left to align, which only needs them inside the window,
// and byte offsets are skipped entirely for ASCII candidates, where the rune
// index already is the offset. Both passes were pure overhead on the great
// majority of candidates, and this runs once per candidate per keystroke.
func (m *Matcher) loadText(text string) bool {
	m.text = m.text[:0]
	m.offs = m.offs[:0]
	m.ascii = true
	for _, r := range text {
		m.text = append(m.text, r)
		if r >= utf8.RuneSelf {
			m.ascii = false
		}
	}
	if !m.ascii {
		for i := range text {
			if utf8.RuneStart(text[i]) {
				m.offs = append(m.offs, i)
			}
		}
		m.offs = append(m.offs, len(text))
	}
	return len(m.text) >= len(m.pat)
}

// byteOff maps a rune index to its byte offset. Index len(text) maps to the
// length, so a match's exclusive end has an answer.
func (m *Matcher) byteOff(i int) int {
	if m.ascii {
		return i
	}
	return m.offs[i]
}

func (m *Matcher) eq(pi, ti int) bool {
	if m.patCS {
		return m.pat[pi] == m.text[ti]
	}
	return m.pat[pi] == fold(m.text[ti])
}

// window narrows the candidate to the tightest span that can hold the pattern:
// greedy forward for the end, then greedy backward for the start. Everything
// outside it is dead weight the matrix would otherwise pay for.
func (m *Matcher) window() (lo, hi int, ok bool) {
	pi := 0
	for ti := range m.text {
		if m.eq(pi, ti) {
			pi++
			if pi == len(m.pat) {
				hi = ti + 1
				break
			}
		}
	}
	if pi < len(m.pat) {
		return 0, 0, false
	}
	pi = len(m.pat) - 1
	for ti := hi - 1; ti >= 0; ti-- {
		if m.eq(pi, ti) {
			if pi == 0 {
				lo = ti
				break
			}
			pi--
		}
	}
	return lo, hi, true
}

func (m *Matcher) align(lo, hi int) Result {
	width := hi - lo
	rows := len(m.pat)

	m.bonus = grow32(m.bonus, width)
	prev := charWhite
	if lo > 0 {
		prev = classOf(m.text[lo-1])
	}
	for j := range width {
		cur := classOf(m.text[lo+j])
		m.bonus[j] = bonusFor(prev, cur)
		prev = cur
	}

	// A one-character pattern has no gaps to weigh, so the strongest boundary
	// wins outright and the matrix is not worth building.
	if rows == 1 {
		best, bestJ := noScore, 0
		for j := range width {
			if !m.eq(0, lo+j) {
				continue
			}
			if s := scoreMatch + m.bonus[j]*bonusFirstCharMultiplier; s > best {
				best, bestJ = s, j
			}
		}
		m.pos = append(m.pos[:0], lo+bestJ)
		return m.finish(int(best))
	}

	m.h = grow32(m.h, rows*width)
	m.c = grow32(m.c, rows*width)

	for i := range rows {
		row := i * width
		prevRow := row - width
		carry, hasCarry, inGap := noScore, false, false
		for j := range width {
			match, run := noScore, int32(0)
			if m.eq(i, lo+j) {
				base, ok := int32(0), true
				var diagRun int32
				if i > 0 {
					if j == 0 || m.h[prevRow+j-1] == noScore {
						ok = false
					} else {
						base = m.h[prevRow+j-1]
						diagRun = m.c[prevRow+j-1]
					}
				}
				if ok {
					b := m.bonus[j]
					if i == 0 {
						match = scoreMatch + b*bonusFirstCharMultiplier
						run = 1
					} else {
						run = diagRun + 1
						if run > 1 {
							first := m.bonus[j-int(run)+1]
							// A stronger boundary here is worth more than the
							// run that led into it, so let the run restart on
							// this character instead.
							if b >= bonusBoundary && b > first {
								run = 1
							} else {
								b = max(b, max(int32(bonusConsecutive), first))
							}
						}
						match = base + scoreMatch + b
					}
				}
			}

			// Carrying the row rightward under a gap penalty is what lets the
			// next row read a diagonal that already priced the skip.
			gap := noScore
			if hasCarry {
				if inGap {
					gap = carry + scoreGapExtension
				} else {
					gap = carry + scoreGapStart
				}
			}

			if match != noScore && match >= gap {
				m.h[row+j], m.c[row+j] = match, run
				carry, hasCarry, inGap = match, true, false
			} else {
				m.h[row+j], m.c[row+j] = gap, 0
				carry, hasCarry, inGap = gap, gap != noScore, gap != noScore
			}
		}
	}

	last := (rows - 1) * width
	best, bestJ := noScore, 0
	for j := range width {
		if m.h[last+j] > best {
			best, bestJ = m.h[last+j], j
		}
	}

	// Walk back to the cells that actually held a match: a cell beat both its
	// diagonal and its left neighbour exactly when one landed there. preferMatch
	// keeps a consecutive run intact when the two tie.
	m.pos = m.pos[:0]
	i, j := rows-1, bestJ
	preferMatch := true
	for {
		row := i * width
		s := m.h[row+j]
		diag, left := noScore, noScore
		if i > 0 && j > 0 {
			diag = m.h[row-width+j-1]
		}
		if j > 0 {
			left = m.h[row+j-1]
		}
		if s > diag && (s > left || s == left && preferMatch) {
			m.pos = append(m.pos, lo+j)
			if i == 0 {
				break
			}
			i--
		}
		if j == 0 {
			break
		}
		preferMatch = m.c[row+j] > 1 || row+width+j+1 < len(m.c) && m.c[row+width+j+1] > 1
		j--
	}
	slices.Reverse(m.pos)
	return m.finish(int(best))
}

// finish applies the bonuses that depend on the shape of the whole alignment
// rather than on any one character, then converts rune positions to the byte
// offsets callers highlight with.
func (m *Matcher) finish(score int) Result {
	if len(m.pos) == 0 {
		return Result{Score: score}
	}
	contiguous := m.pos[0] == 0
	for k := 1; contiguous && k < len(m.pos); k++ {
		contiguous = m.pos[k] == m.pos[k-1]+1
	}
	if contiguous {
		score += bonusPrefix
		if len(m.pos) == len(m.text) {
			score += bonusExact
		}
	}
	start, end := m.byteOff(m.pos[0]), m.byteOff(m.pos[len(m.pos)-1]+1)
	if !m.ascii {
		for k, p := range m.pos {
			m.pos[k] = m.offs[p]
		}
	}
	return Result{
		Score:     score,
		Positions: m.pos,
		Start:     start,
		End:       end,
	}
}

func grow32(b []int32, n int) []int32 {
	if cap(b) < n {
		return make([]int32, n)
	}
	return b[:n]
}
