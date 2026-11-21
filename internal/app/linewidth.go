package app

import (
	"sync/atomic"

	"github.com/charmbracelet/x/ansi"
)

var lineWidthFallbacks atomic.Uint64

func slowLineWidth(line string) int {
	lineWidthFallbacks.Add(1)
	return ansi.StringWidth(line)
}

func lineWidth(line string) int {
	w := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c >= 0x80:
			return slowLineWidth(line)

		case c == 0x1b:
			if i+1 >= len(line) {
				return slowLineWidth(line)
			}
			switch line[i+1] {
			case '[':
				j := i + 2
				for j < len(line) && line[j] >= 0x30 && line[j] <= 0x3f {
					j++
				}
				if j >= len(line) || line[j] < 0x40 || line[j] > 0x7e {
					return slowLineWidth(line)
				}
				i = j
			default:
				return slowLineWidth(line)
			}

		case c < 0x20 || c == 0x7f:
		default:
			w++
		}
	}
	return w
}

func framesWidth(lines []string) int {
	widest := 0
	for _, line := range lines {
		if w := lineWidth(line); w > widest {
			widest = w
		}
	}
	return widest
}
