package shot

import "slices"

// Emoji sequences: a flag, a ZWJ family, a keycap or a skin tone is several
// code points that a colour font draws as one glyph. The font says which
// glyph by a ligature: Noto Color Emoji in GSUB (lookup type 4), Apple Color
// Emoji in morx, the AAT table, as a chain of subtables run in order.
//
// shapeSequence runs that table over the glyphs of one cluster and returns
// the glyphs it leaves. Only what an emoji font uses is read: GSUB ligature
// lookups of the ccmp, liga and rlig features, and the morx noncontextual
// and ligature subtables the chain turns on by default. Every
// offset is checked against its table, every state machine is bounded in
// steps and stack, and a table that breaks a bound is refused: the cluster
// keeps its glyphs, the renderer draws the first one, and it never loops.

const (
	// maxShapeGlyphs is the longest cluster shaped. A family with skin tones
	// is 7 code points, 11 with variation selectors.
	maxShapeGlyphs = 32
	// maxLigStack bounds the morx ligature component stack.
	maxLigStack = 32
	// deletedGlyph is the glyph AAT writes for one a subtable took out, such
	// as the variation selector of a keycap; it goes after each subtable. A
	// cluster that comes out with none left is refused.
	deletedGlyph = 0xFFFF
)

// shaper is one face's ligature table, read whole on first use.
type shaper struct {
	gsub []byte
	morx []byte
	// bad is set once a table broke a bound: the face shapes nothing more.
	bad bool
}

// tbl is a bounded view of a table: every read past its end reports false.
type tbl []byte

func (t tbl) u16(off int) (int, bool) {
	if off < 0 || off+2 > len(t) {
		return 0, false
	}
	return int(t[off])<<8 | int(t[off+1]), true
}

// w32 is the 32-bit word at off, whole.
func (t tbl) w32(off int) (uint32, bool) {
	if off < 0 || off+4 > len(t) {
		return 0, false
	}
	return uint32(t[off])<<24 | uint32(t[off+1])<<16 | uint32(t[off+2])<<8 | uint32(t[off+3]), true
}

// u32 is a 32-bit offset or count at off; one past 1<<30 reads as broken.
func (t tbl) u32(off int) (int, bool) {
	if off < 0 || off+4 > len(t) {
		return 0, false
	}
	v, _ := t.w32(off)
	if v > 1<<30 {
		return 0, false
	}
	return int(v), true
}

// shape returns the glyphs of a cluster after the face's ligatures.
func (s *shaper) shape(glyphs []int) []int {
	if s == nil || s.bad || len(glyphs) < 2 || len(glyphs) > maxShapeGlyphs {
		return glyphs
	}
	out := slices.Clone(glyphs)
	ok := true
	switch {
	case len(s.morx) > 0:
		out, ok = morxShape(tbl(s.morx), out)
	case len(s.gsub) > 0:
		out, ok = gsubShape(tbl(s.gsub), out)
	}
	if !ok || len(out) == 0 {
		s.bad = true
		return glyphs
	}
	return out
}

// gsubShape applies the ligature lookups of ccmp, liga and rlig, in lookup
// order.
func gsubShape(t tbl, g []int) ([]int, bool) {
	featOff, ok1 := t.u16(6)
	lookOff, ok2 := t.u16(8)
	nFeat, ok3 := t.u16(featOff)
	nLook, ok4 := t.u16(lookOff)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return g, false
	}
	var lookups []int
	for i := range nFeat {
		rec := featOff + 2 + 6*i
		if rec+6 > len(t) {
			return g, false
		}
		switch string(t[rec : rec+4]) {
		case "ccmp", "liga", "rlig":
		default:
			continue
		}
		f, _ := t.u16(rec + 4)
		f += featOff
		n, ok := t.u16(f + 2)
		if !ok {
			return g, false
		}
		for j := range n {
			idx, ok := t.u16(f + 4 + 2*j)
			if !ok || idx >= nLook {
				return g, false
			}
			lookups = append(lookups, idx)
		}
	}
	slices.Sort(lookups)
	lookups = slices.Compact(lookups)
	for _, idx := range lookups {
		l, ok := t.u16(lookOff + 2 + 2*idx)
		if !ok {
			return g, false
		}
		l += lookOff
		kind, ok1 := t.u16(l)
		n, ok2 := t.u16(l + 4)
		if !ok1 || !ok2 {
			return g, false
		}
		for k := range n {
			sub, ok := t.u16(l + 6 + 2*k)
			if !ok {
				return g, false
			}
			sub += l
			subKind := kind
			if kind == 7 { // extension: the real subtable is further on
				subKind, ok1 = t.u16(sub + 2)
				ext, ok2 := t.u32(sub + 4)
				if !ok1 || !ok2 {
					return g, false
				}
				sub += ext
			}
			if subKind != 4 {
				continue
			}
			if g, ok = gsubLigature(t, sub, g); !ok {
				return g, false
			}
		}
	}
	return g, true
}

// gsubLigature applies one ligature substitution subtable (format 1).
func gsubLigature(t tbl, sub int, g []int) ([]int, bool) {
	cov, ok1 := t.u16(sub + 2)
	nSets, ok2 := t.u16(sub + 4)
	if !ok1 || !ok2 {
		return g, false
	}
	for i := 0; i < len(g); i++ {
		ci, ok := coverageIndex(t, sub+cov, g[i])
		if !ok {
			return g, false
		}
		if ci < 0 || ci >= nSets {
			continue
		}
		set, ok := t.u16(sub + 6 + 2*ci)
		if !ok {
			return g, false
		}
		set += sub
		nLig, ok := t.u16(set)
		if !ok {
			return g, false
		}
	ligs:
		for j := range nLig {
			lo, ok := t.u16(set + 2 + 2*j)
			if !ok {
				return g, false
			}
			lo += set
			lig, ok1 := t.u16(lo)
			comp, ok2 := t.u16(lo + 2)
			if !ok1 || !ok2 || comp == 0 {
				return g, false
			}
			if i+comp > len(g) {
				continue
			}
			for c := 1; c < comp; c++ {
				want, ok := t.u16(lo + 2 + 2*c)
				if !ok {
					return g, false
				}
				if g[i+c] != want {
					continue ligs
				}
			}
			g = append(append(g[:i:i], lig), g[i+comp:]...)
			break
		}
	}
	return g, true
}

// coverageIndex is the coverage index of glyph, -1 when it is not covered.
func coverageIndex(t tbl, cov, glyph int) (int, bool) {
	format, ok1 := t.u16(cov)
	n, ok2 := t.u16(cov + 2)
	if !ok1 || !ok2 {
		return -1, false
	}
	switch format {
	case 1:
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			v, ok := t.u16(cov + 4 + 2*m)
			if !ok {
				return -1, false
			}
			switch {
			case v == glyph:
				return m, true
			case v < glyph:
				lo = m + 1
			default:
				hi = m
			}
		}
	case 2:
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			r := cov + 4 + 6*m
			start, ok1 := t.u16(r)
			end, ok2 := t.u16(r + 2)
			first, ok3 := t.u16(r + 4)
			if !ok1 || !ok2 || !ok3 {
				return -1, false
			}
			switch {
			case glyph < start:
				hi = m
			case glyph > end:
				lo = m + 1
			default:
				return first + glyph - start, true
			}
		}
	default:
		return -1, false
	}
	return -1, true
}

// morxShape runs the subtables of the first morx chain that its default
// flags turn on.
func morxShape(t tbl, g []int) ([]int, bool) {
	version, ok1 := t.u16(0)
	nChains, ok2 := t.u32(4)
	if !ok1 || !ok2 || version < 2 || nChains == 0 {
		return g, false
	}
	flags, ok1 := t.u32(8)
	chainLen, ok2 := t.u32(12)
	nFeat, ok3 := t.u32(16)
	nSub, ok4 := t.u32(20)
	if !ok1 || !ok2 || !ok3 || !ok4 || 8+chainLen > len(t) || nSub > 256 {
		return g, false
	}
	chain := t[8 : 8+chainLen]
	off := 16 + 12*nFeat
	for range nSub {
		length, ok1 := chain.u32(off)
		cov, ok2 := chain.u32(off + 4)
		subFlags, ok3 := chain.u32(off + 8)
		if !ok1 || !ok2 || !ok3 || length < 12 || off+length > len(chain) {
			return g, false
		}
		body := chain[off+12 : off+length]
		off += length
		if subFlags&flags == 0 || cov&0x80000000 != 0 && cov&0x20000000 == 0 {
			continue // off by default, or vertical text only
		}
		var ok bool
		switch cov & 0xFF {
		case 4:
			g, ok = morxNoncontextual(body, g)
		case 2:
			g, ok = morxLigature(body, g)
		default:
			// Rearrangement and insertion change no emoji glyph, and
			// Apple Color Emoji's contextual subtables change none of
			// 780 flags, ZWJ sequences, keycaps and skin tones checked.
			continue
		}
		if !ok {
			return g, false
		}
		g = slices.DeleteFunc(g, func(v int) bool { return v == deletedGlyph })
	}
	return g, true
}

// aatLookup looks glyph up in an AAT lookup table of 16-bit values.
func aatLookup(t tbl, off, glyph int) (int, bool, bool) {
	format, ok := t.u16(off)
	if !ok {
		return 0, false, false
	}
	switch format {
	case 0:
		v, ok := t.u16(off + 2 + 2*glyph)
		return v, ok, true
	case 2, 4, 6:
		unit, ok1 := t.u16(off + 2)
		n, ok2 := t.u16(off + 4)
		if !ok1 || !ok2 || unit < 4 {
			return 0, false, false
		}
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			r := off + 12 + unit*m
			last, ok1 := t.u16(r)
			first := last
			if format != 6 {
				first, ok2 = t.u16(r + 2)
			}
			if !ok1 || !ok2 {
				return 0, false, false
			}
			if last == 0xFFFF && first == 0xFFFF {
				hi = m // the end marker
				continue
			}
			switch {
			case glyph < first:
				hi = m
			case glyph > last:
				lo = m + 1
			case format == 2:
				v, ok := t.u16(r + 4)
				return v, ok, ok
			case format == 6:
				v, ok := t.u16(r + 2)
				return v, ok, ok
			default:
				vo, ok := t.u16(r + 4)
				if !ok {
					return 0, false, false
				}
				v, ok := t.u16(off + vo + 2*(glyph-first))
				return v, ok, ok
			}
		}
		return 0, false, true
	case 8:
		first, ok1 := t.u16(off + 2)
		n, ok2 := t.u16(off + 4)
		if !ok1 || !ok2 {
			return 0, false, false
		}
		if glyph < first || glyph >= first+n {
			return 0, false, true
		}
		v, ok := t.u16(off + 6 + 2*(glyph-first))
		return v, ok, ok
	}
	return 0, false, false
}

// stateTable is an extended state table header (STXHeader).
type stateTable struct {
	t                         tbl
	nClasses, classes, states int
	entries, entrySize        int
}

func readStateTable(t tbl, entrySize int) (stateTable, bool) {
	n, ok1 := t.u32(0)
	c, ok2 := t.u32(4)
	s, ok3 := t.u32(8)
	e, ok4 := t.u32(12)
	if !ok1 || !ok2 || !ok3 || !ok4 || n < 4 || n > 4096 || s >= len(t) || e >= len(t) {
		return stateTable{}, false
	}
	return stateTable{t: t, nClasses: n, classes: c, states: s, entries: e, entrySize: entrySize}, true
}

// class is the class of the glyph at i, end of text past the end.
func (st stateTable) class(g []int, i int) (int, bool) {
	if i >= len(g) {
		return 0, true
	}
	if g[i] == deletedGlyph {
		return 2, true
	}
	v, found, ok := aatLookup(st.t, st.classes, g[i])
	if !ok {
		return 0, false
	}
	if !found || v >= st.nClasses {
		return 1, true
	}
	return v, true
}

// entry is the entry offset for a state and class.
func (st stateTable) entry(state, class int) (int, bool) {
	idx, ok := st.t.u16(st.states + 2*(state*st.nClasses+class))
	if !ok {
		return 0, false
	}
	off := st.entries + st.entrySize*idx
	if off+st.entrySize > len(st.t) {
		return 0, false
	}
	return off, true
}

// run drives a state machine over g: act performs an entry at glyph i and
// returns its flags, of which 0x4000 holds the glyph for the next step. The
// steps are bounded, so a table that holds a glyph forever is refused.
func (st stateTable) run(g []int, act func(entry, i int) (int, bool)) bool {
	state := 0
	for i, steps := 0, 0; i <= len(g); steps++ {
		if steps > 4*len(g)+16 {
			return false
		}
		class, ok := st.class(g, i)
		if !ok {
			return false
		}
		e, ok := st.entry(state, class)
		if !ok {
			return false
		}
		next, _ := st.t.u16(e)
		flags, _ := st.t.u16(e + 2)
		if _, ok := act(e, i); !ok {
			return false
		}
		state = next
		if i == len(g) {
			break
		}
		if flags&0x4000 == 0 {
			i++
		}
	}
	return true
}

// morxNoncontextual swaps each glyph its lookup table names.
func morxNoncontextual(t tbl, g []int) ([]int, bool) {
	for i, v := range g {
		if v == deletedGlyph {
			continue
		}
		to, found, ok := aatLookup(t, 0, v)
		if !ok {
			return g, false
		}
		if found {
			g[i] = to
		}
	}
	return g, true
}

// morxLigature joins the glyphs an entry's actions name into one. It
// follows HarfBuzz and CoreText: an action walks the component stack down
// from the top, adds each glyph's component value to the ligature index, and
// at a store or the last action replaces the glyph under the cursor with the
// ligature and deletes the components above it.
func morxLigature(t tbl, g []int) ([]int, bool) {
	st, ok := readStateTable(t, 6)
	actions, ok1 := t.u32(16)
	components, ok2 := t.u32(20)
	ligatures, ok3 := t.u32(24)
	if !ok || !ok1 || !ok2 || !ok3 {
		return g, false
	}
	var stack []int
	ok = st.run(g, func(e, i int) (int, bool) {
		flags, _ := t.u16(e + 2)
		action, _ := t.u16(e + 4)
		if flags&0x8000 != 0 && i < len(g) {
			// A glyph held by dontAdvance is not pushed twice.
			if n := len(stack); n == 0 || stack[n-1] != i {
				if n == maxLigStack {
					return 0, false
				}
				stack = append(stack, i)
			}
		}
		if flags&0x2000 == 0 || len(stack) == 0 || i >= len(g) {
			return flags, true
		}
		cursor, index := len(stack), 0
		for a := action; ; a++ {
			if cursor == 0 || a-action > maxLigStack {
				return 0, false
			}
			cursor--
			word, ok := t.w32(actions + 4*a)
			if !ok {
				return 0, false
			}
			offset := int(word & 0x3FFFFFFF)
			if offset&0x20000000 != 0 {
				offset -= 0x40000000 // the offset is 30 bits, signed
			}
			pos := stack[cursor]
			if g[pos] == deletedGlyph {
				return 0, false
			}
			c, ok := t.u16(components + 2*(g[pos]+offset))
			if !ok {
				return 0, false
			}
			index += c
			if word&0xC0000000 != 0 { // store or last
				lig, ok := t.u16(ligatures + 2*index)
				if !ok {
					return 0, false
				}
				g[pos] = lig
				for len(stack)-1 > cursor {
					g[stack[len(stack)-1]] = deletedGlyph
					stack = stack[:len(stack)-1]
				}
				index = 0
			}
			if word&0x80000000 != 0 {
				return flags, true
			}
		}
	})
	return g, ok
}
