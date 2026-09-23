package webshell

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// ANSI styling kept short on purpose. The shell draws with the 16 ANSI colours
// so tuios themes recolour it the way they recolour a real shell.
const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	purple = "\x1b[35m"
	cyan   = "\x1b[36m"
)

// program is a command the shell can run. It returns an exit status.
type program func(t *TTY, args []string) int

// programs are the commands a pane can run, at the prompt or as the pane's own
// process. Filled in init so the table can refer to itself (help lists it).
var programs map[string]program

// summaries are the lines help prints, in the order it prints them.
var summaries = [][2]string{
	{"help", "this list"},
	{"ls", "list files"},
	{"cd", "change directory"},
	{"pwd", "print the current directory"},
	{"cat", "print a file"},
	{"echo", "print text (echo hi > file.txt writes a file)"},
	{"touch / mkdir / rm", "make and remove files"},
	{"clear", "clear the screen (or Ctrl+L)"},
	{"neofetch", "system info, the pretty way"},
	{"top", "live process monitor (q to quit)"},
	{"rain", "digital rain (any key to stop)"},
	{"colors", "the terminal palette"},
	{"agent", "a pretend coding agent"},
	{"history", "what you typed"},
	{"exit", "close this window"},
}

func init() {
	programs = map[string]program{
		"sh":       func(t *TTY, _ []string) int { return runShell(t) },
		"help":     cmdHelp,
		"echo":     cmdEcho,
		"whoami":   func(t *TTY, _ []string) int { t.Print("guest\r\n"); return 0 },
		"hostname": func(t *TTY, _ []string) int { t.Print("tuios\r\n"); return 0 },
		"uname":    func(t *TTY, _ []string) int { t.Print("tuios wasm js/wasm\r\n"); return 0 },
		"date":     func(t *TTY, _ []string) int { t.Print(time.Now().Format(time.UnixDate) + "\r\n"); return 0 },
		"neofetch": cmdNeofetch,
		"top":      cmdTop,
		"htop":     cmdTop,
		"btop":     cmdTop,
		"rain":     cmdRain,
		"cmatrix":  cmdRain,
		"colors":   cmdColors,
		"agent":    cmdAgent,
		"claude":   cmdAgent,
		"true":     func(*TTY, []string) int { return 0 },
		"false":    func(*TTY, []string) int { return 1 },
		"tuios": func(t *TTY, _ []string) int {
			t.Print("You are already in it. Press " + bold + "Ctrl+B" + reset + " then " + bold + "?" + reset + " for help.\r\n")
			return 0
		},
		"vim": cmdNoEditor, "vi": cmdNoEditor, "nvim": cmdNoEditor, "nano": cmdNoEditor, "emacs": cmdNoEditor,
		"sudo": func(t *TTY, _ []string) int {
			t.Print("guest is not in the sudoers file. This incident will be reported to nobody.\r\n")
			return 1
		},
	}
}

// shell is one prompt's state.
type shell struct {
	t       *TTY
	cwd     string
	line    []rune
	cursor  int
	history []string
	histPos int
	status  int
	pending []byte // an escape sequence split across reads
	paste   bool   // inside a bracketed paste
}

func runShell(t *TTY) int {
	s := &shell{t: t, cwd: Home}
	t.Print(bold + "tuios" + reset + dim + " web shell. Type " + reset + bold + "help" + reset + dim + " to see what it can do." + reset + "\r\n")
	s.prompt()
	for chunk := range t.In {
		if s.feed(chunk) {
			return s.status
		}
	}
	return 0
}

func (s *shell) promptText() string {
	mark := green + "❯" + reset
	if s.status != 0 {
		mark = red + "❯" + reset
	}
	return cyan + bold + prettyPath(s.cwd) + reset + " " + mark + " "
}

func (s *shell) prompt() {
	s.line = s.line[:0]
	s.cursor = 0
	s.histPos = len(s.history)
	s.t.Print(s.promptText())
}

// redraw repaints the line from the prompt. Lines longer than the pane wrap
// and are not repainted precisely; that is fine for a guided tour.
func (s *shell) redraw() {
	var b strings.Builder
	b.WriteString("\r\x1b[K")
	b.WriteString(s.promptText())
	b.WriteString(s.highlighted())
	if back := len(s.line) - s.cursor; back > 0 {
		b.WriteString("\x1b[" + strconv.Itoa(back) + "D")
	}
	s.t.Print(b.String())
}

// highlighted colours the first word green when it is a command the shell
// knows and red when it is not, the way fish does.
func (s *shell) highlighted() string {
	text := string(s.line)
	word, rest, _ := strings.Cut(text, " ")
	if word == "" {
		return text
	}
	colour := red
	if _, ok := programs[word]; ok || builtins[word] {
		colour = green
	}
	if rest != "" || strings.HasSuffix(text, " ") {
		return colour + word + reset + " " + rest
	}
	return colour + word + reset
}

var builtins = map[string]bool{"cd": true, "pwd": true, "ls": true, "cat": true, "clear": true, "exit": true, "history": true, "touch": true, "mkdir": true, "rm": true}

// feed handles one chunk of input and reports whether the shell should exit.
func (s *shell) feed(chunk []byte) bool {
	data := append(s.pending, chunk...)
	s.pending = nil
	for i := 0; i < len(data); {
		c := data[i]
		if c == 0x1b {
			n, complete := s.escape(data[i:])
			if !complete {
				s.pending = append([]byte(nil), data[i:]...)
				return false
			}
			i += n
			continue
		}
		i++
		switch c {
		case '\r', '\n':
			if s.paste && c == '\n' {
				continue
			}
			if s.enter() {
				return true
			}
		case 0x7f, 0x08:
			if s.cursor > 0 {
				s.line = append(s.line[:s.cursor-1], s.line[s.cursor:]...)
				s.cursor--
				s.redraw()
			}
		case 0x03: // Ctrl+C
			s.t.Print("^C\r\n")
			s.status = 130
			s.prompt()
		case 0x04: // Ctrl+D
			if len(s.line) == 0 {
				s.t.Print("exit\r\n")
				return true
			}
		case 0x0c: // Ctrl+L
			s.t.Print("\x1b[H\x1b[2J")
			s.redraw()
		case 0x01: // Ctrl+A
			s.cursor = 0
			s.redraw()
		case 0x05: // Ctrl+E
			s.cursor = len(s.line)
			s.redraw()
		case 0x15: // Ctrl+U
			s.line = append(s.line[:0], s.line[s.cursor:]...)
			s.cursor = 0
			s.redraw()
		case 0x17: // Ctrl+W
			j := s.cursor
			for j > 0 && s.line[j-1] == ' ' {
				j--
			}
			for j > 0 && s.line[j-1] != ' ' {
				j--
			}
			s.line = append(s.line[:j], s.line[s.cursor:]...)
			s.cursor = j
			s.redraw()
		case '\t':
			s.complete()
		default:
			if c < 0x20 {
				continue
			}
			// Decode one UTF-8 rune.
			size := utf8Len(c)
			start := i - 1
			if start+size > len(data) {
				s.pending = append([]byte(nil), data[start:]...)
				return false
			}
			r := []rune(string(data[start : start+size]))
			i = start + size
			s.insert(r...)
		}
	}
	return false
}

func utf8Len(c byte) int {
	switch {
	case c < 0x80:
		return 1
	case c>>5 == 0x6:
		return 2
	case c>>4 == 0xe:
		return 3
	case c>>3 == 0x1e:
		return 4
	}
	return 1
}

func (s *shell) insert(r ...rune) {
	tail := append([]rune(nil), s.line[s.cursor:]...)
	s.line = append(append(s.line[:s.cursor], r...), tail...)
	s.cursor += len(r)
	s.redraw()
}

// escape consumes one escape sequence and reports its length, or that more
// bytes are needed.
func (s *shell) escape(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	if b[1] != '[' && b[1] != 'O' {
		return 2, true // Alt+key: ignored
	}
	j := 2
	for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
		j++
	}
	if j >= len(b) {
		return 0, false
	}
	params, final := string(b[2:j]), b[j]
	switch {
	case final == 'A':
		s.historyMove(-1)
	case final == 'B':
		s.historyMove(1)
	case final == 'C':
		if s.cursor < len(s.line) {
			s.cursor++
			s.redraw()
		}
	case final == 'D':
		if s.cursor > 0 {
			s.cursor--
			s.redraw()
		}
	case final == 'H' || (final == '~' && (params == "1" || params == "7")):
		s.cursor = 0
		s.redraw()
	case final == 'F' || (final == '~' && (params == "4" || params == "8")):
		s.cursor = len(s.line)
		s.redraw()
	case final == '~' && params == "3":
		if s.cursor < len(s.line) {
			s.line = append(s.line[:s.cursor], s.line[s.cursor+1:]...)
			s.redraw()
		}
	case final == '~' && params == "200":
		s.paste = true
	case final == '~' && params == "201":
		s.paste = false
	}
	return j + 1, true
}

func (s *shell) historyMove(d int) {
	if len(s.history) == 0 {
		return
	}
	s.histPos = min(max(s.histPos+d, 0), len(s.history))
	if s.histPos == len(s.history) {
		s.line = s.line[:0]
	} else {
		s.line = []rune(s.history[s.histPos])
	}
	s.cursor = len(s.line)
	s.redraw()
}

// complete fills in a command name or a path.
func (s *shell) complete() {
	text := string(s.line[:s.cursor])
	space := strings.LastIndexByte(text, ' ')
	word := text[space+1:]
	var candidates []string
	if space < 0 {
		for name := range programs {
			candidates = append(candidates, name)
		}
		for name := range builtins {
			candidates = append(candidates, name)
		}
	} else {
		dir, prefix := s.cwd, word
		if k := strings.LastIndexByte(word, '/'); k >= 0 {
			dir, prefix = resolve(s.cwd, word[:k+1]), word[k+1:]
			_ = prefix
		}
		base := word[:strings.LastIndexByte(word, '/')+1]
		for _, e := range list(dir) {
			name := base + e.name
			if e.dir {
				name += "/"
			}
			candidates = append(candidates, name)
		}
	}
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(c, word) && c != word {
			matches = append(matches, c)
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return
	}
	common := matches[0]
	for _, m := range matches[1:] {
		for !strings.HasPrefix(m, common) {
			common = common[:len(common)-1]
		}
	}
	if common != word {
		add := []rune(common[len(word):])
		if len(matches) == 1 && !strings.HasSuffix(common, "/") {
			add = append(add, ' ')
		}
		s.insert(add...)
		return
	}
	s.t.Print("\r\n" + strings.Join(matches, "  ") + "\r\n")
	s.redraw()
}

// enter runs the line and reports whether the shell should exit.
func (s *shell) enter() bool {
	s.t.Print("\r\n")
	line := strings.TrimSpace(string(s.line))
	if line == "" {
		s.prompt()
		return false
	}
	if len(s.history) == 0 || s.history[len(s.history)-1] != line {
		s.history = append(s.history, line)
	}
	for _, part := range strings.Split(line, "&&") {
		if s.run(strings.TrimSpace(part)) {
			return true
		}
		if s.status != 0 {
			break
		}
	}
	s.prompt()
	return false
}

// run runs one command and reports whether the shell should exit.
func (s *shell) run(line string) bool {
	args := splitArgs(line)
	if len(args) == 0 {
		return false
	}
	name := args[0]
	s.t.Emit("shell.command", map[string]string{"command": name, "line": line, "cwd": s.cwd})
	s.status = 0
	switch name {
	case "exit", "logout":
		return true
	case "cd":
		target := resolve(s.cwd, argOr(args, 1, "~"))
		if !isDir(target) {
			s.fail("cd: no such directory: " + argOr(args, 1, ""))
			return false
		}
		s.cwd = target
		s.t.Emit("shell.cwd", map[string]string{"cwd": s.cwd})
		// OSC 7 tells tuios the directory, the way a configured real shell does.
		s.t.Print("\x1b]7;file://tuios" + s.cwd + "\x1b\\")
	case "pwd":
		s.t.Print(s.cwd + "\r\n")
	case "ls", "ll", "la":
		s.ls(args[1:], name != "ls")
	case "cat", "less", "more", "bat":
		if len(args) < 2 {
			s.fail(name + ": which file? Try " + bold + "cat README.md" + reset)
			return false
		}
		for _, a := range args[1:] {
			p := resolve(s.cwd, a)
			content, ok := readFile(p)
			if !ok {
				if isDir(p) {
					s.fail(name + ": " + a + ": is a directory")
				} else {
					s.fail(name + ": " + a + ": no such file")
				}
				continue
			}
			s.t.Print(strings.ReplaceAll(content, "\n", "\r\n"))
		}
	case "clear":
		s.t.Print("\x1b[H\x1b[2J\x1b[3J")
	case "history":
		for i, h := range s.history {
			s.t.Printf("%s%4d%s  %s\r\n", dim, i+1, reset, h)
		}
	case "touch":
		for _, a := range args[1:] {
			p := resolve(s.cwd, a)
			if _, ok := readFile(p); !ok && !writeFile(p, "", false) {
				s.fail("touch: cannot create " + a)
			}
		}
	case "mkdir":
		for _, a := range args[1:] {
			if !mkdir(resolve(s.cwd, a)) {
				s.fail("mkdir: cannot create " + a)
			}
		}
	case "rm":
		for _, a := range args[1:] {
			if !remove(resolve(s.cwd, a)) {
				s.fail("rm: cannot remove " + a)
			}
		}
	case "echo":
		// Redirection is the one piece of shell syntax worth faking.
		if k := strings.Index(line, ">"); k >= 0 {
			appendTo := strings.HasPrefix(line[k:], ">>")
			target := strings.TrimSpace(strings.TrimLeft(line[k:], ">"))
			text := strings.Join(splitArgs(line[:k])[1:], " ") + "\n"
			if target == "" || !writeFile(resolve(s.cwd, target), text, appendTo) {
				s.fail("echo: cannot write " + target)
			}
			return false
		}
		s.status = cmdEcho(s.t, args[1:])
	default:
		prog, ok := programs[name]
		if !ok || name == "sh" {
			s.fail(name + ": command not found. Type " + bold + "help" + reset + " to see what is here.")
			s.status = 127
			return false
		}
		s.status = prog(s.t, args[1:])
	}
	return false
}

func (s *shell) fail(msg string) {
	s.t.Print(red + msg + reset + "\r\n")
	s.status = 1
}

func (s *shell) ls(args []string, long bool) {
	target := s.cwd
	showAll := long
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			if strings.Contains(a, "a") {
				showAll = true
			}
			if strings.Contains(a, "l") {
				long = true
			}
			continue
		}
		target = resolve(s.cwd, a)
	}
	if !isDir(target) {
		if _, ok := readFile(target); ok {
			s.t.Print(target + "\r\n")
			return
		}
		s.fail("ls: no such file or directory")
		return
	}
	var parts []string
	for _, e := range list(target) {
		if strings.HasPrefix(e.name, ".") && !showAll {
			continue
		}
		name := colourName(e)
		if long {
			size := 4096
			if !e.dir {
				c, _ := readFile(target + "/" + e.name)
				size = len(c)
			}
			kind := "-rw-r--r--"
			if e.dir {
				kind = "drwxr-xr-x"
			}
			s.t.Printf("%s%s%s guest %s%6d%s  %s\r\n", dim, kind, reset, green, size, reset, name)
			continue
		}
		parts = append(parts, name)
	}
	if !long && len(parts) > 0 {
		s.t.Print(strings.Join(parts, "  ") + "\r\n")
	}
}

func colourName(e entry) string {
	switch {
	case e.dir:
		return blue + bold + e.name + "/" + reset
	case strings.HasSuffix(e.name, ".go"):
		return cyan + e.name + reset
	case strings.HasSuffix(e.name, ".md"):
		return yellow + e.name + reset
	case strings.HasSuffix(e.name, ".html"), strings.HasSuffix(e.name, ".css"):
		return purple + e.name + reset
	}
	return e.name
}

func argOr(args []string, i int, def string) string {
	if i < len(args) {
		return args[i]
	}
	return def
}

// splitArgs splits on spaces, honouring single and double quotes.
func splitArgs(line string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func cmdEcho(t *TTY, args []string) int {
	t.Print(strings.Join(args, " ") + "\r\n")
	return 0
}

func cmdNoEditor(t *TTY, _ []string) int {
	t.Print(yellow + "No editor in the browser demo." + reset + " Try " + bold + "cat notes.txt" + reset + " instead.\r\n")
	return 1
}

func cmdHelp(t *TTY, _ []string) int {
	t.Print(bold + "Commands" + reset + "\r\n")
	for _, s := range summaries {
		t.Printf("  %s%-20s%s %s\r\n", green, s[0], reset, s[1])
	}
	t.Print("\r\n" + dim + "tuios keys: Ctrl+B then ? shows every keybinding." + reset + "\r\n")
	return 0
}
