package app

// sidebarScrollThumb places the thumb of an overflowing rail section's scroll
// bar: its first line and its size, in lines of the section, and whether the
// section has a bar at all. Only a section with more rows than lines has one,
// and only one of two lines or more, since a bar of one cell cannot say where
// it is.
//
// The ends are exact, where plain rounding is not. The thumb touches the top
// line only at the top of the scroll and the bottom line only at the bottom.
// The bottom is also the only state with no "+N" row, so the thumb never sits
// on the line that owns up to the rows below it. Two lines have no middle to
// show, and there the bottom line keeps that meaning: the thumb is at the top
// until the scroll reaches the end.
func sidebarScrollThumb(start, rows, lines int) (top, size int, ok bool) {
	if lines < 2 || rows <= lines {
		return 0, 0, false
	}
	size = max(min(roundDiv(lines*lines, rows), lines-1), 1)
	maxScroll := rows - lines
	travel := lines - size
	switch {
	case start >= maxScroll:
		return travel, size, true
	case start <= 0 || travel < 2:
		return 0, size, true
	}
	return max(min(roundDiv(start*travel, maxScroll), travel-1), 1), size, true
}
