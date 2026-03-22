package app

import "strings"

// fixBorderJunctions scans composited output for overlapping border characters
// and replaces them with proper junction characters.
//
// When windows overlap by 1 cell (shared borders), the compositor renders the
// higher-Z window's border on top of the lower-Z window's border. This creates
// incorrect characters — e.g., a ╭ (top-left corner) where a ┬ (T-junction)
// should be. This function fixes those by looking at context.
func fixBorderJunctions(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 {
		return content
	}

	// We need to work with runes to handle multi-byte border characters.
	// But ANSI escape sequences complicate direct rune scanning.
	// Instead, use simple string replacement for known bad patterns.
	//
	// The key insight: when two windows overlap horizontally, the right window's
	// top-left corner (╭) lands on the left window's top border (─).
	// The correct character is ┬ (T-junction pointing down).
	// Similarly for other corners.

	// Build a replacer for all the junction fixes.
	// These patterns occur when a corner character is adjacent to continuation
	// border characters that indicate a junction.
	r := strings.NewReplacer(
		// Vertical split junctions (right window's corners on left window's horizontal borders)
		"─╭", "─┬", // top border meets top-left corner → T-junction down
		"╮─", "┬─", // top-right corner meets continuing top border → T-junction down (reversed)
		"─╰", "─┴", // bottom border meets bottom-left corner → T-junction up
		"╯─", "┴─", // bottom-right corner meets continuing bottom border → T-junction up

		// Horizontal split junctions (bottom window's corners on top window's vertical borders)
		// These are harder because they span lines. We'll handle the common case:
		// a corner character that should be a side T-junction.

		// When corners stack vertically with │:
		// (handled below in line-by-line pass)
	)

	result := r.Replace(content)

	// Line-by-line pass for vertical junction fixes.
	// When ╭ or ╰ appears at the same column as │ from the line above/below,
	// it should be ├. When ╮ or ╯ appears at same column as │, it should be ┤.
	resultLines := strings.Split(result, "\n")
	for i := 1; i < len(resultLines)-1; i++ {
		runes := []rune(resultLines[i])
		aboveRunes := []rune(resultLines[i-1])
		belowRunes := []rune(resultLines[i+1])

		changed := false
		for j, r := range runes {
			switch r {
			case '╭':
				// If the character above is │ or a vertical border, this is ├
				if j < len(aboveRunes) && isVerticalBorder(aboveRunes[j]) {
					runes[j] = '├'
					changed = true
				}
			case '╮':
				if j < len(aboveRunes) && isVerticalBorder(aboveRunes[j]) {
					runes[j] = '┤'
					changed = true
				}
			case '╰':
				if j < len(belowRunes) && isVerticalBorder(belowRunes[j]) {
					runes[j] = '├'
					changed = true
				}
				if j < len(aboveRunes) && isVerticalBorder(aboveRunes[j]) {
					runes[j] = '└' // standard corner if above is vertical
					changed = true
				}
			case '╯':
				if j < len(belowRunes) && isVerticalBorder(belowRunes[j]) {
					runes[j] = '┤'
					changed = true
				}
			case '┬':
				// If above has │, this should be ┼
				if j < len(aboveRunes) && isVerticalBorder(aboveRunes[j]) {
					runes[j] = '┼'
					changed = true
				}
			case '┴':
				// If below has │, this should be ┼
				if j < len(belowRunes) && isVerticalBorder(belowRunes[j]) {
					runes[j] = '┼'
					changed = true
				}
			}
		}
		if changed {
			resultLines[i] = string(runes)
		}
	}

	return strings.Join(resultLines, "\n")
}

func isVerticalBorder(r rune) bool {
	return r == '│' || r == '┃' || r == '║' || r == '|'
}
