package session

import (
	"slices"
	"strings"

	"tuios.dev/vt"
)

const defaultCommandCeiling = 2000

type commandBlock struct {
	from, to int
	cmdline  string
	exit     int
	finished bool
}

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

func commandBudgets(sizes []int, ceiling int) []int {
	budgets := make([]int, len(sizes))
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return sizes[a] - sizes[b] })
	left := ceiling
	for k, i := range order {
		share := left / (len(order) - k)
		budgets[i] = min(sizes[i], share)
		left -= budgets[i]
	}
	return budgets
}

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
