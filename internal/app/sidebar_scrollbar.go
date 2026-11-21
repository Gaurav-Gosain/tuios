package app

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
