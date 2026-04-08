// Package scrollback provides a scrollback browser that parses terminal
// history into structured command-output blocks.
package scrollback

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// CommandBlock represents a single command and its output extracted from scrollback.
type CommandBlock struct {
	Command      string // command text (what the user typed)
	Output       string // plain text output
	StyledOutput string // ANSI-colored output (preserves terminal styling)
	ExitCode     int    // -1 if unknown
	StartLine    int    // absolute line index
	EndLine      int    // absolute line index (inclusive)
	Method       string // "osc133" or "regex"  - how this block was parsed
}

// DebugLogFunc can be set to capture parser diagnostic output.
var DebugLogFunc func(format string, args ...any)

func debugLog(format string, args ...any) {
	if DebugLogFunc != nil {
		DebugLogFunc(format, args...)
	}
}

// ParseBlocks extracts command blocks from the terminal's scrollback and screen.
// It uses OSC 133 markers when available, falling back to regex prompt detection.
func ParseBlocks(term *vt.Emulator) []CommandBlock {
	if term == nil {
		return nil
	}

	markers := term.SemanticMarkers()
	if markers != nil && markers.Len() > 0 {
		allMarkers := markers.Markers()
		debugLog("OSC 133: %d markers found", len(allMarkers))
		for i, m := range allMarkers {
			debugLog("  marker[%d]: type=%c absLine=%d col=%d exit=%d", i, m.Type, m.AbsLine, m.Col, m.ExitCode)
		}
		blocks := parseWithMarkers(term, allMarkers)
		if len(blocks) > 0 {
			debugLog("OSC 133: produced %d blocks", len(blocks))
			for i, b := range blocks {
				debugLog("  block[%d]: cmd=%q startLine=%d endLine=%d", i, b.Command, b.StartLine, b.EndLine)
			}
			return blocks
		}
		debugLog("OSC 133: markers present but produced 0 blocks, falling back to regex")
	} else {
		debugLog("OSC 133: no markers found, using regex fallback")
	}

	return parseWithRegex(term)
}

// parseWithMarkers uses OSC 133 markers to precisely segment commands.
// Marker sequence: A (prompt start) -> B (command start) -> C (output start) -> D (command done)
func parseWithMarkers(term *vt.Emulator, markers []vt.SemanticMarker) []CommandBlock {
	var blocks []CommandBlock

	for i := range len(markers) {
		m := markers[i]
		if m.Type != vt.MarkerPromptStart {
			continue
		}

		// Find B (command start) after this A
		var bMarker, cMarker, dMarker *vt.SemanticMarker
		nextPromptAbsLine := -1
		for j := i + 1; j < len(markers); j++ {
			switch markers[j].Type {
			case vt.MarkerCommandStart:
				if bMarker == nil {
					bMarker = &markers[j]
				}
			case vt.MarkerCommandExecuted:
				if cMarker == nil {
					cMarker = &markers[j]
				}
			case vt.MarkerCommandFinished:
				if dMarker == nil {
					dMarker = &markers[j]
				}
			case vt.MarkerPromptStart:
				// Next prompt starts - record its position and stop looking
				nextPromptAbsLine = markers[j].AbsLine
				goto buildBlock
			}
			if dMarker != nil {
				break
			}
		}

		// If we found D but not the next prompt, scan ahead for it.
		// After CSI 2J (clear), the next prompt's AbsLine can be before D's
		// AbsLine because the cursor resets to screen row 0.
		if nextPromptAbsLine < 0 {
			for j := i + 1; j < len(markers); j++ {
				if markers[j].Type == vt.MarkerPromptStart && markers[j].AbsLine > m.AbsLine {
					nextPromptAbsLine = markers[j].AbsLine
					break
				}
			}
		}

	buildBlock:
		if bMarker == nil {
			continue
		}

		// Require C (command executed) or D (command finished)  - without either,
		// this is an unexecuted prompt (e.g., initial shell prompt, current input line).
		// Reading text from these positions is unreliable because the line content
		// may have changed since the marker was created (overwritten by output).
		if cMarker == nil && dMarker == nil {
			debugLog("  skipping A→B (absLine=%d col=%d): no C or D marker (unexecuted prompt)", bMarker.AbsLine, bMarker.Col)
			continue
		}

		// Prefer command text captured at C-marker time (before output overwrites buffer).
		// Fall back to reading from the terminal buffer if no capture available.
		command := ""
		if cMarker != nil && cMarker.CapturedText != "" {
			command = cMarker.CapturedText
		} else {
			// Fallback: extract from terminal buffer (may be overwritten by TUI programs)
			command = extractTextFromCol(term, bMarker.AbsLine, bMarker.Col)
			if bMarker.AbsLine < cmdEndLine(cMarker, dMarker, bMarker) {
				for line := bMarker.AbsLine + 1; line <= cmdEndLine(cMarker, dMarker, bMarker)-1; line++ {
					command += "\n" + extractAbsLineText(term, line)
				}
			}
			command = cleanCommandText(command)
		}
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}

		// Extract output (between C and D)
		output := ""
		styledOutput := ""
		if cMarker != nil {
			outEnd := 0
			if dMarker != nil {
				outEnd = dMarker.AbsLine - 1
			} else {
				outEnd = term.ScrollbackLen() + term.Height() - 1
			}

			// Cap output at the next prompt boundary. After CSI 2J (clear/ctrl+l),
			// the cursor resets to screen row 0 so the next prompt's AbsLine can
			// be BEFORE the current command's D marker. Without this cap we'd
			// read the new prompt content as part of the old command's output.
			if nextPromptAbsLine >= 0 && outEnd >= nextPromptAbsLine {
				outEnd = nextPromptAbsLine - 1
				debugLog("  capping output at next prompt: outEnd=%d (nextPrompt=%d)", outEnd, nextPromptAbsLine)
			}

			// If this command completed and a subsequent command was executed,
			// cap output at the scrollback boundary. On-screen lines may have
			// been overwritten by later programs that use cursor repositioning
			// (e.g. neofetch, htop). Scrollback lines are safe since they
			// can't be overwritten by cursor movement.
			if dMarker != nil && outEnd >= term.ScrollbackLen() {
				hasSubsequentExec := false
				for j := i + 1; j < len(markers); j++ {
					// Look for a C marker from a DIFFERENT (later) command.
					// Our own C marker has AbsLine < dMarker.AbsLine (since C
					// fires before output and D fires after). A subsequent
					// command's C marker will be at or after our D marker.
					if markers[j].Type == vt.MarkerCommandExecuted && markers[j].AbsLine >= dMarker.AbsLine {
						hasSubsequentExec = true
						break
					}
				}
				if hasSubsequentExec {
					outEnd = min(outEnd, term.ScrollbackLen()-1)
					debugLog("  capping output at scrollback boundary: outEnd=%d (scrollbackLen=%d)", outEnd, term.ScrollbackLen())
				}
			}

			if outEnd >= cMarker.AbsLine {
				output = extractLinesText(term, cMarker.AbsLine, outEnd)
				styledOutput = extractLinesStyledText(term, cMarker.AbsLine, outEnd)
			}
		}

		exitCode := -1
		if dMarker != nil {
			exitCode = dMarker.ExitCode
		}

		blocks = append(blocks, CommandBlock{
			Command:      command,
			Output:       strings.TrimRight(output, "\n "),
			StyledOutput: strings.TrimRight(styledOutput, "\n "),
			ExitCode:     exitCode,
			StartLine:    m.AbsLine,
			EndLine:      endLineForBlock(dMarker, cMarker, bMarker, term),
			Method:       "osc133",
		})
	}

	reverseBlocks(blocks)
	return blocks
}

func cmdEndLine(c, d, b *vt.SemanticMarker) int {
	if c != nil {
		return c.AbsLine
	}
	if d != nil {
		return d.AbsLine
	}
	return b.AbsLine + 1
}

func endLineForBlock(d, c, b *vt.SemanticMarker, term *vt.Emulator) int {
	if d != nil {
		return d.AbsLine
	}
	if c != nil {
		return term.ScrollbackLen() + term.Height() - 1
	}
	return b.AbsLine
}

// promptPattern matches common shell prompts at the start of a line.
// Very conservative to avoid false positives on command output.
var promptPattern = regexp.MustCompile(
	`^(\s*)` +
		`(` +
		`\w+@[\w.-]+\s*[$#]\s+|` + // user@host$ or user@host# (ssh, bash default)
		`\[[\w@. ~/-]{1,60}\]\s*[$#]\s+|` + // [user@host dir]$ (bash with brackets)
		`\$\s+` + // bare "$ " (very common default)
		`)`,
)

// looksLikeOutput detects lines that are clearly command output, not prompts.
var looksLikeOutput = regexp.MustCompile(
	`^(?:` +
		`[\s]*[drwx.\-lbcps]{10}|` + // ls -l permissions
		`[\s]*total\s+\d|` + // "total 48" from ls -l
		`[\s]*[│├└─┌┐┘┬┤┼╭╮╰╯]+|` + // box drawing
		`[\s]*\d+\s+\d+\s+\d+|` + // numeric columns
		`[\s]*[■●◆▪▸▹►▻→←↑↓＞]+` + // icon chars from eza/tree (including fullwidth >)
		`)`,
)

// looksLikeFileEntry detects "commands" that are really file listing entries.
var looksLikeFileEntry = regexp.MustCompile(
	`(?:` +
		`\d+\.?\d*\s*[KMGT]i?B|` + // file sizes: "29.1 MB", "4.0K", "10KiB"
		`\d{1,2}\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s|` + // dates in file listings
		`^[.\-/~]\S*\s+\d|` + // filename followed by numbers (eza grid)
		`^-{3,}` + // lines starting with dashes (permission strings, separators)
		`)`,
)

// parseWithRegex uses prompt pattern matching as a fallback.
func parseWithRegex(term *vt.Emulator) []CommandBlock {
	totalLines := term.ScrollbackLen() + term.Height()
	if totalLines == 0 {
		return nil
	}

	allText := make([]string, totalLines)
	for i := range totalLines {
		allText[i] = extractAbsLineText(term, i)
	}

	type promptLine struct {
		line int
		cmd  string
	}
	var prompts []promptLine

	for i, text := range allText {
		if text == "" {
			continue
		}
		if looksLikeOutput.MatchString(text) {
			continue
		}

		loc := promptPattern.FindStringSubmatchIndex(text)
		if loc == nil {
			continue
		}

		matchEnd := loc[1]
		cmd := strings.TrimSpace(text[matchEnd:])
		if cmd == "" || len(cmd) > 200 {
			continue
		}
		if looksLikeOutput.MatchString(cmd) {
			continue
		}
		if looksLikeFileEntry.MatchString(cmd) {
			continue
		}

		prompts = append(prompts, promptLine{line: i, cmd: cmd})
	}

	if len(prompts) == 0 {
		return nil
	}

	var blocks []CommandBlock
	for i, p := range prompts {
		outputStart := p.line + 1
		outputEnd := totalLines - 1
		if i+1 < len(prompts) {
			outputEnd = prompts[i+1].line - 1
		}

		output := ""
		styledOutput := ""
		if outputEnd >= outputStart {
			lines := allText[outputStart : outputEnd+1]
			output = strings.TrimRight(strings.Join(lines, "\n"), "\n ")
			styledOutput = extractLinesStyledText(term, outputStart, outputEnd)
			styledOutput = strings.TrimRight(styledOutput, "\n ")
		}

		blocks = append(blocks, CommandBlock{
			Command:      p.cmd,
			Output:       output,
			StyledOutput: styledOutput,
			ExitCode:     -1,
			StartLine:    p.line,
			EndLine:      outputEnd,
			Method:       "regex",
		})
	}

	reverseBlocks(blocks)
	return blocks
}

// extractTextFromCol extracts plain text from a line starting at the given column.
func extractTextFromCol(term *vt.Emulator, absLine, col int) string {
	full := extractAbsLineText(term, absLine)
	// Convert column (cell position) to rune offset
	runes := []rune(full)
	if col >= len(runes) {
		return ""
	}
	return strings.TrimSpace(string(runes[col:]))
}

// extractAbsLineText extracts plain text from an absolute line index.
func extractAbsLineText(term *vt.Emulator, absLine int) string {
	sbLen := term.ScrollbackLen()
	if absLine < sbLen {
		return lineToText(term.ScrollbackLine(absLine))
	}
	screenY := absLine - sbLen
	if screenY >= term.Height() {
		return ""
	}
	w := term.Width()
	var sb strings.Builder
	for x := range w {
		cell := term.CellAt(x, screenY)
		if cell != nil && cell.Content != "" {
			sb.WriteString(string(cell.Content))
		} else {
			sb.WriteByte(' ')
		}
	}
	return strings.TrimRight(sb.String(), " ")
}

// extractAbsLineStyledText extracts ANSI-styled text from an absolute line index.
func extractAbsLineStyledText(term *vt.Emulator, absLine int) string {
	sbLen := term.ScrollbackLen()
	if absLine < sbLen {
		return lineToStyledText(term.ScrollbackLine(absLine))
	}
	screenY := absLine - sbLen
	if screenY >= term.Height() {
		return ""
	}
	w := term.Width()
	cells := make([]uv.Cell, w)
	for x := range w {
		cell := term.CellAt(x, screenY)
		if cell != nil {
			cells[x] = *cell
		}
	}
	return cellsToStyledText(cells)
}

func extractLinesText(term *vt.Emulator, from, to int) string {
	var lines []string
	for i := from; i <= to; i++ {
		lines = append(lines, extractAbsLineText(term, i))
	}
	return strings.Join(lines, "\n")
}

func extractLinesStyledText(term *vt.Emulator, from, to int) string {
	var lines []string
	for i := from; i <= to; i++ {
		lines = append(lines, extractAbsLineStyledText(term, i))
	}
	return strings.Join(lines, "\n")
}

func lineToText(line uv.Line) string {
	if line == nil {
		return ""
	}
	var sb strings.Builder
	for _, cell := range line {
		if cell.Content != "" {
			sb.WriteString(string(cell.Content))
		} else {
			sb.WriteByte(' ')
		}
	}
	return strings.TrimRightFunc(sb.String(), unicode.IsSpace)
}

func lineToStyledText(line uv.Line) string {
	if len(line) == 0 {
		return ""
	}
	cells := make([]uv.Cell, len(line))
	copy(cells, line)
	return cellsToStyledText(cells)
}

func cellsToStyledText(cells []uv.Cell) string {
	if len(cells) == 0 {
		return ""
	}

	// Find last non-space cell
	lastContent := -1
	for i := len(cells) - 1; i >= 0; i-- {
		c := string(cells[i].Content)
		if c != "" && strings.TrimSpace(c) != "" {
			lastContent = i
			break
		}
	}
	if lastContent < 0 {
		return ""
	}

	var sb strings.Builder
	for i := 0; i <= lastContent; i++ {
		cell := &cells[i]
		content := string(cell.Content)
		if content == "" {
			content = " "
		}

		hasStyle := cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0
		if hasStyle {
			prefix := buildCellANSI(cell)
			if prefix != "" {
				sb.WriteString(prefix)
				sb.WriteString(content)
				sb.WriteString("\x1b[0m")
				continue
			}
		}
		sb.WriteString(content)
	}

	return sb.String()
}

func buildCellANSI(cell *uv.Cell) string {
	var te ansi.Style

	if cell.Style.Fg != nil {
		te = te.ForegroundColor(cell.Style.Fg)
	}
	if cell.Style.Bg != nil {
		te = te.BackgroundColor(cell.Style.Bg)
	}

	attrs := cell.Style.Attrs
	if attrs&1 != 0 {
		te = te.Bold()
	}
	if attrs&2 != 0 {
		te = te.Faint()
	}
	if attrs&4 != 0 {
		te = te.Italic(true)
	}
	if attrs&32 != 0 {
		te = te.Reverse(true)
	}
	if attrs&128 != 0 {
		te = te.Strikethrough(true)
	}

	return te.String()
}

// cleanCommandText strips output garbage from extracted command text.
// Programs like neofetch overwrite the command line with TUI output,
// so the raw extraction may include box drawing, CJK, emoji, etc.
// This function truncates at the first character that's clearly not
// part of a typed shell command.
func cleanCommandText(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if isOutputGarbage(r) {
			cleaned := strings.TrimSpace(string(runes[:i]))
			if cleaned != "" {
				return cleaned
			}
			// If everything before was whitespace, return original
			return text
		}
	}
	return text
}

// isOutputGarbage returns true for characters that are very unlikely to appear
// in typed shell commands but commonly appear in TUI program output.
func isOutputGarbage(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x259F: // Box drawing + block elements
		return true
	case r >= 0x2800 && r <= 0x28FF: // Braille patterns
		return true
	case r >= 0x3000 && r <= 0x9FFF: // CJK ideographs + symbols
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility
		return true
	case r >= 0xE000 && r <= 0xF8FF: // Private Use Area (Nerd Font icons)
		return true
	case r >= 0x1F000: // Emoji, supplementary symbols
		return true
	}
	return false
}

func reverseBlocks(blocks []CommandBlock) {
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
	}
}
