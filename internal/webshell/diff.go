package webshell

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/linediff"
)

type diffOp = linediff.Op

const maxDiffCells = 1 << 20

func lineDiff(a, b string) []diffOp {
	return linediff.Bounded(linediff.Split(a), linediff.Split(b), maxDiffCells)
}

func unifiedHunks(ops []diffOp) string {
	var b strings.Builder
	line := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
		}
		b.WriteString("\r\n")
	}
	for _, h := range linediff.Hunks(ops, 3) {
		line(cyan, h.Header(), reset)
		for _, op := range h.Ops {
			switch op.Op {
			case '-':
				line(red, "-", op.Line, reset)
			case '+':
				line(green, "+", op.Line, reset)
			default:
				line(" ", op.Line)
			}
		}
	}
	return b.String()
}
