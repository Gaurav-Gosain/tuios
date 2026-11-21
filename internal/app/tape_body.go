package app

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

const tapeStructuralSettle = 900 * time.Millisecond

func compileProjectBody(body string) []tape.Command {
	var cmds []tape.Command
	settle := tape.Command{Type: tape.CommandTypeSleep, Delay: tapeStructuralSettle}

	for line := range strings.SplitSeq(body, "\n") {
		toks := tokenizeTapeLine(line)
		if len(toks) == 0 {
			continue
		}
		kw := strings.ToLower(toks[0])
		switch kw {
		case "type":
			if len(toks) >= 2 {
				cmds = append(cmds, tape.Command{Type: tape.CommandTypeType, Args: []string{toks[1]}})
				if len(toks) >= 3 && strings.EqualFold(toks[2], "enter") {
					cmds = append(cmds, tape.Command{Type: tape.CommandTypeEnter})
				}
			}
		case "run":
			if len(toks) >= 2 {
				cmds = append(cmds,
					tape.Command{Type: tape.CommandTypeType, Args: []string{toks[1]}},
					tape.Command{Type: tape.CommandTypeEnter},
				)
			}
		case "enter":
			cmds = append(cmds, tape.Command{Type: tape.CommandTypeEnter})
		case "split":
			dir := "vertical"
			if len(toks) >= 2 {
				dir = normalizeSplitDir(toks[1])
			}
			cmds = append(cmds, tape.Command{Type: tape.CommandTypeSplit, Args: []string{dir}}, settle)
		case "newwindow":
			cmd := tape.Command{Type: tape.CommandTypeNewWindow}
			if len(toks) >= 2 {
				cmd.Args = []string{toks[1]}
			}
			cmds = append(cmds, cmd, settle)
		case "renamewindow", "rename":
			if len(toks) >= 2 {
				cmds = append(cmds, tape.Command{Type: tape.CommandTypeRenameWindow, Args: []string{toks[1]}})
			}
		case "focus", "focuswindow":
			if len(toks) >= 2 {
				cmds = append(cmds, tape.Command{Type: tape.CommandTypeFocusWindow, Args: []string{toks[1]}})
			}
		case "sleep", "wait":
			if len(toks) >= 2 {
				if d, err := time.ParseDuration(toks[1]); err == nil {
					cmds = append(cmds, tape.Command{Type: tape.CommandTypeSleep, Delay: d})
				}
			}
		case "enabletiling":
			cmds = append(cmds, tape.Command{Type: tape.CommandTypeEnableTiling})
		case "disabletiling":
			cmds = append(cmds, tape.Command{Type: tape.CommandTypeDisableTiling})
		default:
		}
	}
	return cmds
}

func normalizeSplitDir(s string) string {
	switch d := strings.ToLower(strings.Trim(s, `"`)); d {
	case "h", "horizontal":
		return "horizontal"
	case "left", "right", "up", "down":
		return d
	default:
		return "vertical"
	}
}

func tokenizeTapeLine(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}

	var toks []string
	var b strings.Builder
	inQuote := false
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case c == '\\' && inQuote:
			escaped = true
		case c == '"':
			if inQuote {
				toks = append(toks, b.String())
				b.Reset()
				inQuote = false
			} else {
				flush()
				inQuote = true
			}
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return toks
}
