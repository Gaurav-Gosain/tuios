package mosaic

var (
	halfGlyphs     = []rune{' ', '▀', '▄', '█'}
	quadrantGlyphs = []rune{
		' ', '▘', '▝', '▀', '▖', '▌', '▞', '▛',
		'▗', '▚', '▐', '▜', '▄', '▙', '▟', '█',
	}
	sextantGlyphs = buildSextants()
	octantGlyphs  = buildOctants()
)

func glyphStrings(k Kind) []string {
	if int(k) >= len(glyphStringTables) {
		return nil
	}
	return glyphStringTables[k]
}

var glyphStringTables = func() (t [Octant + 1][]string) {
	for k := Half; k <= Octant; k++ {
		for _, r := range glyphTable(k) {
			t[k] = append(t[k], string(r))
		}
	}
	return t
}()

func glyphTable(k Kind) []rune {
	switch k {
	case Half:
		return halfGlyphs
	case Quadrant:
		return quadrantGlyphs
	case Sextant:
		return sextantGlyphs
	case Octant:
		return octantGlyphs
	}
	return nil
}

func buildSextants() []rune {
	t := make([]rune, 64)
	t[0], t[63] = ' ', '█'
	t[0b010101], t[0b101010] = '▌', '▐'
	next := rune(0x1FB00)
	for m := 1; m < 63; m++ {
		if t[m] != 0 {
			continue
		}
		t[m] = next
		next++
	}
	return t
}

func buildOctants() []rune {
	t := make([]rune, 256)
	for m, r := range map[int]rune{
		0x00: ' ', 0xFF: '█',
		0x05: '▘', 0x0A: '▝', 0x0F: '▀', 0x50: '▖', 0x55: '▌', 0x5A: '▞',
		0x5F: '▛', 0xA0: '▗', 0xA5: '▚', 0xAA: '▐', 0xAF: '▜', 0xF0: '▄',
		0xF5: '▙', 0xFA: '▟',
		0x03: '\U0001FB82',
		0xC0: '▂',
		0x3F: '\U0001FB85',
		0xFC: '▆',
		0x14: '\U0001FBE6',
		0x28: '\U0001FBE7',
		0x01: '\U0001CEA8',
		0x02: '\U0001CEAB',
		0x40: '\U0001CEA3',
		0x80: '\U0001CEA0',
	} {
		t[m] = r
	}
	next := rune(0x1CD00)
	for m := range 256 {
		if t[m] != 0 {
			continue
		}
		t[m] = next
		next++
	}
	return t
}

// Shape is the sub-pixel grid and foreground mask of a sextant, an octant or
// one of the quarter blocks that complete the octant set, for a renderer that
// draws block glyphs itself rather than trusting the font: a terminal does
// that so neighbouring cells meet without seams, and tuios shot does it
// because its embedded font has none of these glyphs. ok is false for any
// other rune, the blocks of U+2580..U+259F included.
func Shape(r rune) (gx, gy int, mask uint8, ok bool) {
	s, ok := shapes[r]
	return s.gx, s.gy, s.mask, ok
}

type shape struct {
	gx, gy int
	mask   uint8
}

var shapes = func() map[rune]shape {
	m := map[rune]shape{}
	add := func(table []rune, gx, gy int) {
		for mask, r := range table {
			if r == ' ' || (r >= 0x2580 && r <= 0x259F) {
				continue
			}
			m[r] = shape{gx: gx, gy: gy, mask: uint8(mask)}
		}
	}
	add(sextantGlyphs, 2, 3)
	add(octantGlyphs, 2, 4)
	return m
}()
