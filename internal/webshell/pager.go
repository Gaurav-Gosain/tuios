package webshell

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A read-only file viewer that behaves enough like less and vim for a tour:
// scrolling, jumping, searching, and the ways out people know (q, :q, :wq).
// It never writes, and it says so kindly when someone tries to edit.

type viewer struct {
	t      *TTY
	name   string
	lines  []string
	vim    bool // vim look: line numbers, tildes, a vim status line
	top    int
	msg    string
	cmd    []rune // the : or / line being typed
	cmdOn  bool
	search string
}

// cmdView is less, more, view, vim and friends.
func cmdView(s *shell, args []string) int {
	name := args[0]
	var target string
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
			target = a
			break
		}
	}
	isVim := name == "vim" || name == "vi" || name == "nvim" || name == "view"
	if target == "" && !isVim {
		s.fail(name + ": which file? Try " + bold + name + " README.md" + reset)
		return 1
	}
	var content, shown string
	if target != "" {
		p := resolve(s.cwd, target)
		c, ok := readFile(p)
		if !ok {
			if isDir(p) {
				s.fail(name + ": " + target + " is a directory")
			} else {
				s.fail(name + ": " + target + ": no such file")
			}
			return 1
		}
		content = c
		shown = path.Base(p)
	} else {
		content = vimSplash
		shown = "[No Name]"
	}
	v := &viewer{t: s.t, name: shown, lines: splitLines(content), vim: isVim}
	if len(v.lines) == 0 {
		v.lines = []string{""}
	}
	return v.run()
}

const vimSplash = `
                      VIM - Vi IMproved (demo)

              A read-only viewer for the tuios tour

              type  :q<Enter>        to exit
              type  j and k          to scroll
              try   vim README.md    to open a file
`

func (v *viewer) run() int {
	t := v.t
	t.Print("\x1b[?1049h\x1b[?25l")
	defer t.Print("\x1b[?25h\x1b[?1049l")
	v.draw()
	for {
		select {
		case b, ok := <-t.In:
			if !ok {
				return 0
			}
			if v.feed(b) {
				return 0
			}
			v.draw()
		case <-t.Resized():
			v.draw()
		}
	}
}

// body is how many rows of text fit above the status line.
func (v *viewer) body() int {
	_, rows := v.t.Size()
	return max(rows-1, 1)
}

func (v *viewer) scroll(d int) {
	maxTop := max(len(v.lines)-v.body(), 0)
	v.top = min(max(v.top+d, 0), maxTop)
}

// feed handles one chunk of input and reports whether to quit.
func (v *viewer) feed(b []byte) bool {
	for i := 0; i < len(b); i++ {
		c := b[i]
		if v.cmdOn {
			if v.feedCmd(c) {
				return true
			}
			continue
		}
		if c == 0x1b {
			// Arrow and page keys; a lone Esc just clears the message.
			if i+2 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
				j := i + 2
				for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
					j++
				}
				if j < len(b) {
					v.key(string(b[i+2 : j+1]))
					i = j
					continue
				}
			}
			v.msg = ""
			continue
		}
		switch c {
		case 'q', 'Q':
			return true
		case 'j', '\r', '\n', 0x0e:
			v.scroll(1)
		case 'k', 'y', 0x10:
			v.scroll(-1)
		case ' ', 'f', 0x06:
			v.scroll(v.body())
		case 'b', 0x02:
			v.scroll(-v.body())
		case 'd', 0x04:
			v.scroll(v.body() / 2)
		case 'u', 0x15:
			v.scroll(-v.body() / 2)
		case 'g', '<':
			v.top = 0
		case 'G', '>':
			v.scroll(len(v.lines))
		case 'n':
			v.findNext(1)
		case 'N':
			v.findNext(-1)
		case ':', '/':
			v.cmdOn = true
			v.cmd = []rune{rune(c)}
		case 'i', 'a', 'o', 'O', 'A', 'I', 'x', 'c', 's':
			v.msg = "This is a read-only view. Type :q and Enter to quit."
		case 0x03:
			if v.vim {
				v.msg = "Type :q and press Enter to quit Vim"
			} else {
				return true
			}
		}
	}
	return false
}

func (v *viewer) key(seq string) {
	switch seq {
	case "A":
		v.scroll(-1)
	case "B":
		v.scroll(1)
	case "5~":
		v.scroll(-v.body())
	case "6~":
		v.scroll(v.body())
	case "H", "1~", "7~":
		v.top = 0
	case "F", "4~", "8~":
		v.scroll(len(v.lines))
	}
}

// feedCmd handles a key while the : or / line is open.
func (v *viewer) feedCmd(c byte) bool {
	switch c {
	case 0x1b, 0x03:
		v.cmdOn = false
	case 0x7f, 0x08:
		if len(v.cmd) > 1 {
			v.cmd = v.cmd[:len(v.cmd)-1]
		} else {
			v.cmdOn = false
		}
	case '\r', '\n':
		v.cmdOn = false
		line := string(v.cmd)
		if strings.HasPrefix(line, "/") {
			v.search = line[1:]
			v.findNext(1)
			return false
		}
		switch strings.TrimSpace(line[1:]) {
		case "q", "q!", "wq", "x", "qa", "qa!", "wq!":
			return true
		case "w", "w!":
			v.msg = "Read only in the demo, so nothing to save. :q to quit."
		case "":
		default:
			if n, err := strconv.Atoi(strings.TrimSpace(line[1:])); err == nil {
				v.top = 0
				v.scroll(n - 1)
				return false
			}
			v.msg = "Not in this demo: " + line + ". Try :q"
		}
	default:
		if c >= 0x20 {
			v.cmd = append(v.cmd, rune(c))
		}
	}
	return false
}

func (v *viewer) findNext(dir int) {
	if v.search == "" {
		return
	}
	n := len(v.lines)
	for k := 1; k <= n; k++ {
		i := ((v.top+dir*k)%n + n) % n
		if strings.Contains(strings.ToLower(v.lines[i]), strings.ToLower(v.search)) {
			v.top = 0
			v.scroll(i)
			v.msg = ""
			return
		}
	}
	v.msg = "Pattern not found: " + v.search
}

func (v *viewer) draw() {
	cols, rows := v.t.Size()
	body := max(rows-1, 1)
	gutter := 0
	if v.vim {
		gutter = len(strconv.Itoa(len(v.lines))) + 2
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	lang := path.Ext(v.name)
	for r := 0; r < body; r++ {
		i := v.top + r
		b.WriteString("\x1b[K")
		if i < len(v.lines) {
			if v.vim {
				b.WriteString(yellow + padLeft(strconv.Itoa(i+1), gutter-1) + " " + reset)
			}
			b.WriteString(highlight(lang, clip(v.lines[i], cols-gutter), v.search))
		} else if v.vim {
			b.WriteString(blue + "~" + reset)
		}
		b.WriteString("\r\n")
	}
	b.WriteString("\x1b[K")
	switch {
	case v.cmdOn:
		b.WriteString(string(v.cmd) + "\x1b[?25h")
	case v.msg != "":
		b.WriteString(yellow + clip(v.msg, cols) + reset)
	case v.vim:
		pos := "Top"
		if v.top > 0 {
			pos = strconv.Itoa(100*v.top/max(len(v.lines)-body, 1)) + "%"
			if v.top >= len(v.lines)-body {
				pos = "Bot"
			}
		}
		left := "\"" + v.name + "\" [readonly] " + strconv.Itoa(len(v.lines)) + "L"
		right := dim + ":q quits" + reset + "  " + strconv.Itoa(v.top+1) + ",1  " + pos
		gap := max(cols-utf8.RuneCountInString(left)-visibleLen(right), 1)
		b.WriteString(left + strings.Repeat(" ", gap) + right)
	default:
		label := v.name
		if v.top+body >= len(v.lines) {
			label += " (END)"
		}
		b.WriteString("\x1b[7m " + clip(label, cols-2) + " \x1b[0m" + dim + "  q quits, / searches" + reset)
	}
	if !v.cmdOn {
		b.WriteString("\x1b[?25l")
	}
	v.t.Print(b.String())
}

func padLeft(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

// clip expands tabs and cuts a line to width cells. The files are ASCII and
// box drawing, so one rune is one cell.
func clip(s string, width int) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	return string(r[:width])
}

var goKeywords = map[string]bool{
	"package": true, "import": true, "func": true, "return": true, "if": true,
	"else": true, "for": true, "range": true, "var": true, "const": true,
	"type": true, "struct": true, "map": true, "go": true, "defer": true,
	"switch": true, "case": true, "default": true, "nil": true, "true": true,
	"false": true, "string": true, "int": true, "error": true,
}

// highlight colours one line: Go keywords, strings and comments, Markdown
// headings and bullets, and the current search matches.
func highlight(ext, line, search string) string {
	var out string
	switch ext {
	case ".go", ".mod":
		out = highlightGo(line)
	case ".md":
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#"):
			out = cyan + bold + line + reset
		case strings.HasPrefix(trimmed, "- "):
			out = yellow + "-" + reset + strings.TrimPrefix(line, "-")
		case strings.HasPrefix(line, "    "):
			out = green + line + reset
		default:
			out = line
		}
	case ".tape":
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			out = dim + line + reset
		} else if word, rest, ok := strings.Cut(line, " "); ok {
			out = purple + word + reset + " " + green + rest + reset
		} else {
			out = purple + line + reset
		}
	default:
		out = line
	}
	if search != "" && strings.Contains(line, search) {
		out = strings.ReplaceAll(out, search, "\x1b[7m"+search+"\x1b[27m")
	}
	return out
}

func highlightGo(line string) string {
	var b strings.Builder
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			b.WriteString(dim + line[i:] + reset)
			return b.String()
		case c == '"' || c == '`':
			j := i + 1
			for j < len(line) && line[j] != c {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(line))
			b.WriteString(green + line[i:j] + reset)
			i = j
		case isWordByte(c):
			j := i
			for j < len(line) && isWordByte(line[j]) {
				j++
			}
			word := line[i:j]
			if goKeywords[word] {
				b.WriteString(purple + word + reset)
			} else if j < len(line) && line[j] == '(' {
				b.WriteString(blue + word + reset)
			} else {
				b.WriteString(word)
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
