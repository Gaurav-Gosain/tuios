package deep_nesting

func deep(xs [][]int) int {
	n := 0
	for _, row := range xs {
		for _, v := range row {
			if v > 0 {
				switch {
				case v > 10:
					if v > 100 { // want "control nesting in deep reaches 5 levels"
						n++
					}
				}
			}
		}
	}
	return n
}

// Not reported: else if stays on its level, and a function literal starts
// again at 0.

func flat(xs [][]int) int {
	n := 0
	for _, row := range xs {
		for _, v := range row {
			if v > 0 {
				if v > 10 {
					n++
				} else if v > 5 {
					n--
				} else {
					n += 2
				}
			}
		}
	}
	return n
}

func literal(xs []int) func() int {
	for range xs {
		if len(xs) > 1 {
			for range xs {
				return func() int {
					for range xs {
						if len(xs) > 2 {
							return 1
						}
					}
					return 0
				}
			}
		}
	}
	return nil
}
