package app

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// A project tape body is deliberately NOT the full recorder tape language. The
// recorder grammar is one keystroke-command per line and does not cleanly
// express layout construction: `Type "x" Enter` drops the trailing Enter,
// `Split vertical` drops its direction, and `Focus "name"` maps to a command the
// executor does not implement. Rather than bend that grammar (and its fuzz
// tests) to a different job, a project tape body is compiled by the small,
// explicit interpreter below into the exact executor commands that do work.
//
// Supported body commands (one per line, keyword case-insensitive):
//
//	Type "text" [Enter]       type text into the focused pane, optional submit
//	Run "cmd"                 shorthand for Type "cmd" Enter
//	Enter                     submit a line in the focused pane
//	Split vertical|horizontal split the focused pane into a new tiled pane (v/h ok)
//	NewWindow ["name"]        create a new tiled pane
//	RenameWindow "name"       name the focused pane (Rename is an alias)
//	Focus "name"              focus a pane by name
//	Sleep <duration>          pause (e.g. 500ms, 1s)
//	EnableTiling|DisableTiling toggle tiling
//
// Blank lines and lines beginning with # are ignored, as is any unrecognized
// command (robust to hostile or future input).

// tapeStructuralSettle is the pause inserted after a command that creates a pane
// (Split, NewWindow). In a daemon session the pane is created asynchronously -
// the daemon makes it, starts its shell, and pushes it back - so the next
// command waits this out both for the pane to exist and for its shell to be
// ready to accept input, before typing into it.
const tapeStructuralSettle = 900 * time.Millisecond

// compileProjectBody turns a project tape body into the executor command list
// that builds its layout. It emits a settle Sleep after every pane-creating
// command so the interactive player, which runs one command per tick, does not
// race the daemon's asynchronous window creation.
func compileProjectBody(body string) []tape.Command {
	var cmds []tape.Command
	settle := tape.Command{Type: tape.CommandTypeSleep, Delay: tapeStructuralSettle}

	for _, line := range strings.Split(body, "\n") {
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
			// A comment (# ...) or any unrecognized command: skip it.
		}
	}
	return cmds
}

// normalizeSplitDir folds the accepted split-direction spellings to the two the
// executor understands.
func normalizeSplitDir(s string) string {
	switch strings.ToLower(strings.Trim(s, `"`)) {
	case "h", "horizontal":
		return "horizontal"
	default:
		return "vertical"
	}
}

// tokenizeTapeLine splits one body line into a keyword and its arguments. A
// double-quoted run is one token with the quotes stripped (\" is a literal
// quote); everything else is whitespace-delimited. A leading # comments the line
// out (returns no tokens).
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
				// End of a quoted token; keep it even if empty.
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
