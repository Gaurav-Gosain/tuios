package hints

import (
	"strconv"
	"strings"
	"sync"
)

var pathMatcher = sync.OnceValue(func() *Matcher {
	m, _ := New([]string{URL, Path}, nil)
	return m
})

// PathAt returns the byte range of the file path that covers byte i of line:
// an absolute path, one under ~/, ./ or ../, or a relative one with a slash in
// it, with an optional :line or :line:col. It is the path pattern hints mode
// labels. Whether the path names a file is the caller's question.
func PathAt(line string, i int) (start, end int, ok bool) {
	if i < 0 || i >= len(line) || strings.IndexByte(line, '/') < 0 {
		return 0, 0, false
	}
	for _, m := range pathMatcher().Find(line) {
		if m.Start > i {
			break
		}
		if m.Kind == Path && i < m.End {
			return m.Start, m.End, true
		}
	}
	return 0, 0, false
}

// SplitLineCol splits the :line or :line:col a compiler prints off a path. The
// numbers are 0 when the text has none.
func SplitLineCol(text string) (path string, line, col int) {
	path = text
	var nums []int
	for range 2 {
		i := strings.LastIndexByte(path, ':')
		if i <= 0 || i == len(path)-1 {
			break
		}
		n, err := strconv.Atoi(path[i+1:])
		if err != nil || n < 0 {
			break
		}
		nums = append(nums, n)
		path = path[:i]
	}
	switch len(nums) {
	case 1:
		line = nums[0]
	case 2:
		line, col = nums[1], nums[0]
	}
	return path, line, col
}
