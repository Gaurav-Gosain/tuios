// Ported from pele src/diagrams/treeview/boxDrawing.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele indexes lines in UTF-16 code units. Every box character is one unit, so the port keeps
// byte offsets for slicing and counts units only where an index is a number: the column of a
// branch character.

package treeview

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

// Mermaid's box-drawing preprocessor: turns `├──`, `└──`, `│` (and the heavy variants) into the
// indentation the grammar reads.

// ALL_BOX_CHARS is /[─━│┃└┗├┣]/.
func isBoxChar(c rune) bool {
	switch c {
	case '─', '━', '│', '┃', '└', '┗', '├', '┣':
		return true
	}
	return false
}

// BRANCH_CHAR is /[└┗├┣]/.
func isBranchChar(c rune) bool { return c == '└' || c == '┗' || c == '├' || c == '┣' }

// DASH_CHAR is /[─━]/.
func isDashChar(c rune) bool { return c == '─' || c == '━' }

func hasBoxChar(s string) bool { return strings.IndexFunc(s, isBoxChar) >= 0 }

// every is /^[...]+$/ for a set of characters.
func every(s string, in func(rune) bool) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !in(c) {
			return false
		}
	}
	return true
}

// DECORATION_ONLY is /^[\s│┃]+$/.
func decorationOnly(s string) bool {
	return every(s, func(c rune) bool { return js.IsSpace(c) || c == '│' || c == '┃' })
}

// BOX_ONLY is /^[\s─━│┃└┗├┣]+$/.
func boxOnly(s string) bool {
	return every(s, func(c rune) bool { return js.IsSpace(c) || isBoxChar(c) })
}

// METADATA_LINE is /^\s*(title[\t ]|accTitle[\t ]*:|accDescr[\t ]*[:{])/.
func metadataLine(s string) bool {
	s = js.TrimStart(s)
	blanksThen := func(word string, ends string) bool {
		if !strings.HasPrefix(s, word) {
			return false
		}
		rest := strings.TrimLeft(s[len(word):], "\t ")
		return rest != "" && strings.IndexByte(ends, rest[0]) >= 0
	}
	if strings.HasPrefix(s, "title") && len(s) > 5 && (s[5] == '\t' || s[5] == ' ') {
		return true
	}
	return blanksThen("accTitle", ":") || blanksThen("accDescr", ":{")
}

// COMMENT_LINE is /^\s*%%/.
func commentLine(s string) bool { return strings.HasPrefix(js.TrimStart(s), "%%") }

const INDENT_UNIT = "    "

// PreprocessResult: the text the grammar reads, and output line number to original line number,
// both from 1, which is empty when the text is unchanged.
type PreprocessResult struct {
	text    string
	lineMap map[int]int
	// columns maps, per output line, a UTF-16 index of the rewritten line to a byte offset in
	// the original line.
	columns map[int]func(at int) int
}

func isBoxDrawingFormat(lines []string) bool {
	for _, line := range lines {
		if hasBoxChar(line) {
			return true
		}
	}
	return false
}

// branchAt is BRANCH_CHAR.exec(line): the byte offset and the UTF-16 index of the first branch
// character, or -1.
func branchAt(line string) (int, int) {
	units := 0
	for i, c := range line {
		if isBranchChar(c) {
			return i, units
		}
		units++
		if c >= 0x10000 {
			units++
		}
	}
	return -1, -1
}

func inferSegmentWidth(contentLines []string) int {
	for _, line := range contentLines {
		if _, index := branchAt(line); index > 0 {
			return index
		}
	}
	return 4
}

// remapErrorLines is message.replace(/\bline\s+(\d+)\b/gi, ...): each line number the map holds
// becomes the original one.
func remapErrorLines(message string, lineMap map[int]int) string {
	var out strings.Builder
	last := 0
	for i := 0; i < len(message); {
		if !common.WordAt(message, i, "line") || (i > 0 && util.IsWord(rune(message[i-1]))) {
			i++
			continue
		}
		q := i + 4
		for q < len(message) {
			c, size := utf8.DecodeRuneInString(message[q:])
			if !js.IsSpace(c) {
				break
			}
			q += size
		}
		if q == i+4 {
			i++
			continue
		}
		digits := q
		for q < len(message) && message[q] >= '0' && message[q] <= '9' {
			q++
		}
		if q == digits || (q < len(message) && util.IsWord(rune(message[q]))) {
			i++
			continue
		}
		n, err := strconv.Atoi(message[digits:q])
		if original, ok := lineMap[n]; err == nil && ok && original != 0 {
			out.WriteString(message[last:i])
			out.WriteString("line " + strconv.Itoa(original))
			last = q
		}
		i = q
	}
	if last == 0 {
		return message
	}
	out.WriteString(message[last:])
	return out.String()
}

// lineStart is the byte offset of a line, from 1, as text.indexOf('\n', at) + 1 finds it.
func lineStart(text string, line int) int {
	at := 0
	for n := 1; n < line; n++ {
		if i := strings.IndexByte(text[at:], '\n'); i >= 0 {
			at += i + 1
		} else {
			at = 0
		}
	}
	return at
}

// unitsToBytes is the byte offset of the UTF-16 index at in s.
func unitsToBytes(s string, at int) int {
	i := 0
	for units := 0; units < at && i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		units++
		if r >= 0x10000 {
			units++
		}
		i += size
	}
	return i
}

// beforeTabs undoes four-space tab expansion: the byte offset in line of byte offset at of
// the expanded line.
func beforeTabs(line string, at int) int {
	i := 0
	for n := 0; i < len(line) && n < at; {
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == '\t' {
			n += 4
		} else {
			n += size
		}
		i += size
	}
	return i
}

func fail(input string, index int, message string) {
	line := types.Locate(input, lineStart(input, index+1)).Line
	panic(types.NewPeleError("Line "+strconv.Itoa(line)+": "+message, types.CodeSyntax, types.ErrorInfo{Type: "treeView", Line: line}))
}

func preprocessBoxDrawing(input string) PreprocessResult {
	columns := map[int]func(int) int{}
	if !hasBoxChar(input) {
		return PreprocessResult{text: input, columns: columns}
	}
	lines := strings.Split(input, "\n")

	keywordIdx := -1
	for i, line := range lines {
		if js.Trim(line) == "treeView-beta" {
			keywordIdx = i
			break
		}
	}
	if keywordIdx == -1 {
		return PreprocessResult{text: input, columns: columns}
	}

	passes := func(line string) bool { return js.Trim(line) == "" || commentLine(line) || metadataLine(line) }

	var contentLines []string
	for i := keywordIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if passes(line) || decorationOnly(line) {
			continue
		}
		contentLines = append(contentLines, strings.ReplaceAll(line, "\t", "    "))
	}
	if !isBoxDrawingFormat(contentLines) {
		return PreprocessResult{text: input, columns: columns}
	}

	segmentWidth := inferSegmentWidth(contentLines)
	var out strings.Builder
	count := 0
	lineMap := map[int]int{}
	keep := func(text string, index int, column func(int) int) {
		if count > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(text)
		count++
		lineMap[count] = index + 1
		if column != nil {
			columns[count] = column
		}
	}

	for i := 0; i <= keywordIdx; i++ {
		keep(lines[i], i, nil)
	}
	for i := keywordIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if passes(line) {
			keep(line, i, nil)
			continue
		}
		if decorationOnly(line) {
			continue
		}

		normalized := strings.ReplaceAll(line, "\t", "    ")
		if at, index := branchAt(normalized); at >= 0 {
			depth := int(js.Round(float64(index)/float64(segmentWidth))) + 1
			_, size := utf8.DecodeRuneInString(normalized[at:])
			pos := at + size
			for pos < len(normalized) {
				c, size := utf8.DecodeRuneInString(normalized[pos:])
				if !isDashChar(c) {
					break
				}
				pos += size
			}
			for pos < len(normalized) && normalized[pos] == ' ' {
				pos++
			}
			content := js.TrimEnd(normalized[pos:])
			if content == "" {
				fail(input, i, "Empty node — expected a filename or directory name after the box-drawing prefix")
			}
			indent := strings.Repeat(INDENT_UNIT, depth)
			keep(indent+content, i, func(at int) int {
				return beforeTabs(line, pos+unitsToBytes(normalized[pos:], max(at-len(indent), 0)))
			})
		} else if boxOnly(normalized) {
			continue
		} else if hasBoxChar(normalized) {
			// A name that happens to contain a box character, such as "Section ─ A.txt", is a root item.
			keep(line, i, nil)
		} else if c, _ := utf8.DecodeRuneInString(normalized); normalized != "" && js.IsSpace(c) {
			fail(input, i, "Unexpected indentation without box-drawing characters. In box-drawing format, use ├── or └── prefixes for indented nodes.")
		} else {
			keep(line, i, nil)
		}
	}
	return PreprocessResult{text: out.String(), lineMap: lineMap, columns: columns}
}
