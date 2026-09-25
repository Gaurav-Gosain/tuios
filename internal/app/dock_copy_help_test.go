package app

// sameColor compares two colours by their RGBA words.
func sameColor(a, b interface{ RGBA() (r, g, bl, al uint32) }) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}
