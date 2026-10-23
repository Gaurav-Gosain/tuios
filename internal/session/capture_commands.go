package session

import (
	"slices"
	"strings"

	"tuios.dev/vt"
)

// This file is capture-pane's commands parameter: the last N shell commands
// of a pane, found by their OSC 133 marks, each from its prompt row to the
// row before the next command's prompt, with the whole capture held under a
// ceiling of lines by cutting the middle of the longest outputs.

// defaultCommandCeiling is the total lines a commands capture keeps when the
// call gives no lines.
const defaultCommandCeiling = 2000

// commandBlock is one command in the rows of a recent capture: rows from
// through to, both inclusive, as absolute rows (scrollback first).
type commandBlock struct {
	from, to int
	cmdline  string
	exit     int
	finished bool
}

// captureCommandRows reads the recent capture (scrollback then screen, one
// row per line) and the pane's command marks under one lock, so every mark's
// row indexes the rows it came with. ok is false when the pane has no
// executed command under marks, or shows the alternate screen, whose rows
// the marks do not describe.
func (p *PTY) captureCommandRows(ansi bool) (rows []string, blocks []commandBlock, meta paneMeta, ok bool) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	meta = p.metaLocked()
	if p.terminal == nil || p.terminal.IsAltScreen() {
		return nil, nil, meta, false
	}
	list := p.terminal.SemanticMarkers()
	if list == nil {
		return nil, nil, meta, false
	}
	content := p.captureContent(true, ansi)
	rows = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	blocks = commandBlocks(list.Markers(), len(rows))
	return rows, blocks, meta, len(blocks) > 0
}

// commandBlocks cuts n rows into commands by their marks. A command starts at
// the prompt (A) mark before its C mark, or at the row above the C mark when
// the shell sends no A, which is the row the command line was typed on. It
// ends on the row before the next command starts. The last one runs to the
// end, so it carries the prompt that follows it, or the output so far of a
// command still running.
func commandBlocks(marks []vt.SemanticMarker, n int) []commandBlock {
	var blocks []commandBlock
	prompt := -1
	for _, m := range marks {
		switch m.Type {
		case vt.MarkerPromptStart:
			prompt = m.AbsLine
		case vt.MarkerCommandExecuted:
			start := m.AbsLine - 1
			if prompt >= 0 && prompt <= m.AbsLine {
				start = prompt
			}
			start = max(start, 0)
			prompt = -1
			// A mark behind the last block (the cursor moved up between
			// them) cannot start a new one.
			if start >= n || (len(blocks) > 0 && start <= blocks[len(blocks)-1].from) {
				continue
			}
			blocks = append(blocks, commandBlock{from: start, cmdline: shellCmdline(m.CapturedText)})
		case vt.MarkerCommandFinished:
			if k := len(blocks) - 1; k >= 0 && !blocks[k].finished {
				blocks[k].finished, blocks[k].exit = true, m.ExitCode
			}
		}
	}
	for i := range blocks {
		if i+1 < len(blocks) {
			blocks[i].to = blocks[i+1].from - 1
		} else {
			blocks[i].to = n - 1
		}
	}
	return blocks
}

// commandBudgets shares ceiling lines among commands of the given sizes. A
// command smaller than an even share keeps all its lines, and what it does
// not use goes to the others.
func commandBudgets(sizes []int, ceiling int) []int {
	budgets := make([]int, len(sizes))
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	// Smallest first, so each share is decided with what is left.
	slices.SortStableFunc(order, func(a, b int) int { return sizes[a] - sizes[b] })
	left := ceiling
	for k, i := range order {
		share := left / (len(order) - k)
		budgets[i] = min(sizes[i], share)
		left -= budgets[i]
	}
	return budgets
}

// trimMiddle keeps budget lines of rows: the first part, a line that says how
// many lines it left out, and the tail. It returns the lines and the count
// left out.
func trimMiddle(rows []string, budget int) ([]string, int) {
	if len(rows) <= budget {
		return rows, 0
	}
	budget = max(budget, 3)
	head := max(budget/4, 1)
	tail := budget - head - 1
	omitted := len(rows) - head - tail
	out := make([]string, 0, budget)
	out = append(out, rows[:head]...)
	out = append(out, "… "+groupDigits(omitted)+" lines omitted …")
	out = append(out, rows[len(rows)-tail:]...)
	return out, omitted
}

// captureCommands builds the commands capture: the last count blocks, each
// with its blank rows at the end dropped, under ceiling lines in total.
func captureCommands(rows []string, blocks []commandBlock, count, ceiling int) (content string, omitted int, list []map[string]any) {
	if count < len(blocks) {
		blocks = blocks[len(blocks)-count:]
	}
	parts := make([][]string, len(blocks))
	sizes := make([]int, len(blocks))
	for i, b := range blocks {
		part := rows[b.from : b.to+1]
		for len(part) > 1 && strings.TrimSpace(part[len(part)-1]) == "" {
			part = part[:len(part)-1]
		}
		parts[i], sizes[i] = part, len(part)
	}
	budgets := commandBudgets(sizes, max(ceiling, 3*len(blocks)))
	var sb strings.Builder
	for i, b := range blocks {
		kept, cut := trimMiddle(parts[i], budgets[i])
		omitted += cut
		for _, line := range kept {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		entry := map[string]any{"lines": len(kept), "lines_omitted": cut, "finished": b.finished}
		if b.cmdline != "" {
			entry["cmdline"] = b.cmdline
		}
		if b.finished && b.exit >= 0 {
			entry["exit_code"] = b.exit
		}
		list = append(list, entry)
	}
	return sb.String(), omitted, list
}
