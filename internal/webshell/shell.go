package webshell

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

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

type command func(s *shell, args []string, line string) int

var commands map[string]command

var shellNames map[string]bool

func initShellNames() {
	shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true}
}

var summaries = [][2]string{
	{"ls, cd, pwd", "look around"},
	{"cat, less, vim", "read a file (q or :q to quit)"},
	{"echo hi > f.txt", "write a file"},
	{"touch, mkdir, rm", "make and remove files"},
	{"git", "status, log and diff in ~/projects/hello"},
	{"go run .", "run the tiny Go project"},
	{"claude, codex, gemini", "pretend coding agents"},
	{"top", "a live process monitor (q quits)"},
	{"rain", "digital rain (any key stops it)"},
	{"fastfetch", "system info, the pretty way"},
	{"tuios tape play demo.tape", "watch tuios drive itself"},
	{"tuios tml show deploy.tml", "a form that tuios draws"},
	{"tuios mermaid flow.mmd", "a diagram, drawn as text"},
	{"tplot sales.csv", "a chart from CSV data"},
	{"fortune, cowsay", "wisdom, delivered"},
	{"colors", "the terminal palette"},
	{"tree", "the files as a tree"},
	{"history, clear", "the usual"},
	{"exit", "close this window"},
}

func fromTTY(f func(t *TTY, args []string) int) command {
	return func(s *shell, args []string, _ string) int { return f(s.t, args[1:]) }
}

func say(text string) command {
	return func(s *shell, _ []string, _ string) int {
		s.t.Print(text + "\r\n")
		return 0
	}
}

func initCommands() {
	ls := func(s *shell, args []string, _ string) int { return s.ls(args[1:], args[0] != "ls") }
	cat := func(s *shell, args []string, _ string) int { return s.cat(args[0], args[1:]) }
	view := func(s *shell, args []string, _ string) int { return cmdView(s, args) }
	shellNote := func(s *shell, args []string, _ string) int {
		s.t.Print("You are already in a shell: " + bold + "webshell" + reset + ", made for the tuios tour.\r\n")
		return 0
	}
	editorNote := func(s *shell, args []string, _ string) int {
		if len(args) > 1 {
			return cmdView(s, append([]string{"view"}, args[1:]...))
		}
		s.t.Print(yellow + args[0] + " is not in the demo." + reset + " Try " + bold + "vim README.md" + reset + " for a read-only look.\r\n")
		return 0
	}
	commands = map[string]command{
		"help":  func(s *shell, _ []string, _ string) int { return cmdHelp(s.t) },
		"exit":  func(s *shell, _ []string, _ string) int { s.exiting = true; return s.status },
		"cd":    (*shell).cd,
		"pwd":   func(s *shell, _ []string, _ string) int { s.t.Print(s.cwd + "\r\n"); return 0 },
		"ls":    ls,
		"ll":    ls,
		"la":    ls,
		"cat":   cat,
		"bat":   cat,
		"less":  view,
		"more":  view,
		"view":  view,
		"vim":   view,
		"vi":    view,
		"nvim":  view,
		"nano":  editorNote,
		"emacs": editorNote,
		"code":  editorNote,
		"echo":  (*shell).echo,
		"clear": func(s *shell, _ []string, _ string) int {
			s.t.Print("\x1b[H\x1b[2J\x1b[3J")
			return 0
		},
		"history": func(s *shell, _ []string, _ string) int {
			for i, h := range s.history {
				s.t.Printf("%s%4d%s  %s\r\n", dim, i+1, reset, h)
			}
			return 0
		},
		"touch": func(s *shell, args []string, _ string) int {
			for _, a := range args[1:] {
				p := resolve(s.cwd, a)
				if _, ok := readFile(p); !ok && !writeFile(p, "", false) {
					return s.fail("touch: cannot create " + a)
				}
			}
			return 0
		},
		"mkdir": func(s *shell, args []string, _ string) int {
			for _, a := range args[1:] {
				if a == "-p" {
					continue
				}
				if !mkdir(resolve(s.cwd, a)) {
					return s.fail("mkdir: cannot create " + a)
				}
			}
			return 0
		},
		"rm": func(s *shell, args []string, _ string) int {
			for _, a := range args[1:] {
				if strings.HasPrefix(a, "-") {
					continue
				}
				if !remove(resolve(s.cwd, a)) {
					return s.fail("rm: cannot remove " + a)
				}
			}
			return 0
		},
		"tree":      cmdTree,
		"git":       func(s *shell, args []string, _ string) int { return cmdGit(s, args[1:]) },
		"go":        cmdGo,
		"tuios":     cmdTuios,
		"tml":       cmdTML,
		"sketch":    cmdSketch,
		"tplot":     cmdTplot,
		"claude":    cmdAgent,
		"codex":     cmdAgent,
		"gemini":    cmdAgent,
		"agent":     cmdAgent,
		"neofetch":  fromTTY(cmdFastfetch),
		"fastfetch": fromTTY(cmdFastfetch),
		"top":       fromTTY(cmdTop),
		"htop":      fromTTY(cmdTop),
		"btop":      fromTTY(cmdTop),
		"rain":      fromTTY(cmdRain),
		"cmatrix":   fromTTY(cmdRain),
		"colors":    fromTTY(cmdColors),
		"fortune":   fromTTY(cmdFortune),
		"cowsay":    fromTTY(cmdCowsay),
		"whoami":    say("guest"),
		"hostname":  say("tuios"),
		"uname":     say("tuios " + text("js/wasm")),
		"date": func(s *shell, _ []string, _ string) int {
			s.t.Print(time.Now().Format(time.UnixDate) + "\r\n")
			return 0
		},
		"true":  func(*shell, []string, string) int { return 0 },
		"false": func(*shell, []string, string) int { return 1 },
		"sudo":  say("guest is not in the sudoers file. This incident will be reported to nobody."),
		"sh":    shellNote,
		"bash":  shellNote,
		"zsh":   shellNote,
		"fish":  shellNote,
	}
	commands["logout"] = commands["exit"]
}

// Program is one command the shell runs, for a launcher to list.
type Program struct {
	Name    string
	Summary string
}

// Programs lists the commands worth starting as a pane of their own: the
// full-screen and long-running ones. The browser build offers them in the
// tuios launcher in place of $PATH.
func Programs() []Program {
	ready()
	return []Program{
		{"top", "Live process monitor"},
		{"claude", "A pretend coding agent"},
		{"codex", "A pretend coding agent"},
		{"gemini", "A pretend coding agent"},
		{"rain", "Digital rain"},
		{"fastfetch", "System info, the pretty way"},
		{"vim", "Read-only file viewer"},
		{"fortune", "A little wisdom"},
		{"sh", "The web shell"},
	}
}

type shell struct {
	t       *TTY
	cwd     string
	line    []rune
	cursor  int
	history []string
	histPos int
	status  int
	exiting bool
	pending []byte
	paste   bool
	row     int
}

func runShell(t *TTY, dir string) int {
	s := &shell{t: t, cwd: startDir(dir)}
	t.laid, _ = t.Size()
	announceCwd(t, s.cwd)
	greeting := []string{bold + "tuios" + reset}
	for w := range strings.FieldsSeq(text("web shell.")) {
		greeting = append(greeting, dim+w+reset)
	}
	greeting = append(greeting, dim+"Type"+reset, bold+"help"+reset)
	for w := range strings.FieldsSeq("to see what it can do.") {
		greeting = append(greeting, dim+w+reset)
	}
	t.Lay(func(cols int) []string { return wrapTokens(greeting, cols) })
	s.prompt()
	for {
		select {
		case chunk, ok := <-t.In:
			if !ok {
				return 0
			}
			if s.feed(chunk) {
				return s.status
			}
		case <-t.Resized():
			if t.relaidNeeded() {
				s.repaint()
			}
		}
	}
}

func (s *shell) repaint() { s.t.repaint(s.editLine()) }

func runProgram(t *TTY, dir, name string, args []string) int {
	s := &shell{t: t, cwd: startDir(dir)}
	announceCwd(t, s.cwd)
	return commands[name](s, append([]string{name}, args...), strings.Join(append([]string{name}, args...), " "))
}

func (s *shell) promptText() string {
	mark := green + "❯" + reset
	if s.status != 0 {
		mark = red + "❯" + reset
	}
	branch := ""
	if b := branchAt(s.cwd); b != "" {
		branch = " " + purple + b + reset
	}
	return cyan + bold + promptPath(s.cwd) + reset + branch + " " + mark + " "
}

func promptPath(dir string) string {
	p := prettyPath(dir)
	if len(p) <= 28 {
		return p
	}
	parts := strings.Split(p, "/")
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		n := 1
		if strings.HasPrefix(part, ".") {
			n = 2
		}
		if len(part) > n {
			parts[i] = part[:n]
		}
	}
	return strings.Join(parts, "/")
}

const (
	markPrompt = "\x1b]133;A\x07"
	markInput  = "\x1b]133;B\x07"
	markOutput = "\x1b]133;C\x07"
)

func markDone(status int) string { return "\x1b]133;D;" + strconv.Itoa(status) + "\x07" }

func (s *shell) prompt() {
	s.line = s.line[:0]
	s.cursor = 0
	s.histPos = len(s.history)
	s.row = 0
	s.t.write(markPrompt + s.promptText() + markInput)
}

func (s *shell) editLine() string {
	var b strings.Builder
	prompt := s.promptText()
	b.WriteString(markPrompt)
	b.WriteString(prompt)
	b.WriteString(markInput)
	b.WriteString(s.highlighted())
	s.place(&b, prompt)
	return b.String()
}

func (s *shell) spans(prompt string) (end, at int) {
	pw := visibleLen(prompt)
	return pw + ansi.StringWidth(string(s.line)), pw + ansi.StringWidth(string(s.line[:s.cursor]))
}

func lastRow(end, cols int) int {
	if end > 0 && end%cols == 0 {
		return end/cols - 1
	}
	return end / cols
}

func (s *shell) place(b *strings.Builder, prompt string) {
	cols, _ := s.t.Size()
	cols = max(cols, 1)
	end, at := s.spans(prompt)
	from := lastRow(end, cols)
	if at == end {
		if end > 0 && end%cols == 0 {
			b.WriteString("\r\n")
			from++
		}
		s.row = from
		return
	}
	row := at / cols
	b.WriteString("\r")
	if from > row {
		csi(b, from-row, 'A')
	}
	if col := at % cols; col > 0 {
		csi(b, col, 'C')
	}
	s.row = row
}

func csi(b *strings.Builder, n int, final byte) {
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(n))
	b.WriteByte(final)
}

func (s *shell) toEnd() {
	cols, _ := s.t.Size()
	cols = max(cols, 1)
	end, _ := s.spans(s.promptText())
	if last := lastRow(end, cols); last > s.row {
		s.t.write("\x1b[" + strconv.Itoa(last-s.row) + "B")
	}
	s.row = 0
}

func (s *shell) keepLine() {
	s.t.keep(markPrompt + s.promptText() + markInput + s.highlighted())
}

func (s *shell) redraw() {
	var b strings.Builder
	if s.row > 0 {
		csi(&b, s.row, 'A')
	}
	b.WriteString("\r\x1b[J")
	prompt := s.promptText()
	b.WriteString(prompt)
	b.WriteString(s.highlighted())
	s.place(&b, prompt)
	s.t.write(b.String())
}

func (s *shell) highlighted() string {
	text := string(s.line)
	var b strings.Builder
	for i, part := range strings.Split(text, "&&") {
		if i > 0 {
			b.WriteString("&&")
		}
		lead := len(part) - len(strings.TrimLeft(part, " "))
		b.WriteString(part[:lead])
		rest := part[lead:]
		word, tail, found := strings.Cut(rest, " ")
		if word == "" {
			b.WriteString(rest)
			continue
		}
		colour := red
		if runnable(word) {
			colour = green
		}
		b.WriteString(colour)
		b.WriteString(word)
		b.WriteString(reset)
		if found {
			b.WriteString(" ")
			b.WriteString(tail)
		}
	}
	return b.String()
}

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
		case 0x03:
			s.toEnd()
			s.keepLine()
			s.t.Print("^C\r\n")
			s.status = 130
			s.prompt()
		case 0x04:
			if len(s.line) == 0 {
				s.toEnd()
				s.keepLine()
				s.t.Print("exit\r\n")
				return true
			}
		case 0x0c:
			s.t.Print("\x1b[H\x1b[2J")
			s.row = 0
			s.redraw()
		case 0x01:
			s.cursor = 0
			s.redraw()
		case 0x05:
			s.cursor = len(s.line)
			s.redraw()
		case 0x15:
			s.line = append(s.line[:0], s.line[s.cursor:]...)
			s.cursor = 0
			s.redraw()
		case 0x17:
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

func (s *shell) escape(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	if b[1] != '[' && b[1] != 'O' {
		return 2, true
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

func (s *shell) complete() {
	text := string(s.line[:s.cursor])
	space := strings.LastIndexByte(text, ' ')
	word := text[space+1:]
	var candidates []string
	if space < 0 {
		candidates = commandNames()
	} else {
		dir := s.cwd
		if k := strings.LastIndexByte(word, '/'); k >= 0 {
			dir = resolve(s.cwd, word[:k+1])
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
	s.toEnd()
	s.keepLine()
	s.t.Print("\r\n")
	s.t.Lay(func(cols int) []string { return columns(matches, cols) })
	s.redraw()
}

func (s *shell) enter() bool {
	s.toEnd()
	s.keepLine()
	s.t.Print("\r\n")
	line := strings.TrimSpace(string(s.line))
	if line == "" {
		s.prompt()
		return false
	}
	if len(s.history) == 0 || s.history[len(s.history)-1] != line {
		s.history = append(s.history, line)
	}
	s.t.Print(markOutput)
	for part := range strings.SplitSeq(line, "&&") {
		if s.run(strings.TrimSpace(part)) {
			s.t.Print(markDone(s.status))
			return true
		}
		if s.status != 0 {
			break
		}
	}
	s.t.Print(markDone(s.status))
	if s.t.relaidNeeded() {
		s.t.repaint("")
	}
	s.prompt()
	return false
}

func (s *shell) run(line string) bool {
	args := splitArgs(line)
	if len(args) == 0 {
		return false
	}
	name := args[0]
	s.t.Emit(EventCommandStart, map[string]any{"command": name, "line": line, "cwd": s.cwd})
	cmd, ok := commands[name]
	if ok {
		s.status = cmd(s, args, line)
	} else {
		s.fail(name + ": command not found. Type " + bold + "help" + reset + " to see what is here.")
		s.status = 127
	}
	s.t.Emit(EventCommand, map[string]any{"command": name, "line": line, "cwd": s.cwd, "exitCode": s.status})
	return s.exiting
}

func runnable(name string) bool {
	_, ok := commands[name]
	return ok
}

func commandNames() []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *shell) cd(args []string, _ string) int {
	target := resolve(s.cwd, argOr(args, 1, "~"))
	if argOr(args, 1, "") == "-" {
		target = Home
	}
	if !isDir(target) {
		return s.fail("cd: no such directory: " + argOr(args, 1, ""))
	}
	s.cwd = target
	s.t.Emit(EventCwd, map[string]any{"cwd": s.cwd})
	s.t.Print(cwdReport(s.cwd))
	return 0
}

func cwdReport(dir string) string { return "\x1b]7;file://" + dir + "\x1b\\" }

func announceCwd(t *TTY, dir string) { t.write(cwdReport(dir)) }

func startDir(dir string) string {
	if dir != "" && isDir(dir) {
		return dir
	}
	return Home
}

func (s *shell) cat(name string, paths []string) int {
	if len(paths) == 0 {
		return s.fail(name + ": which file? Try " + bold + "cat README.md" + reset)
	}
	status := 0
	for _, a := range paths {
		p := resolve(s.cwd, a)
		content, ok := readFile(p)
		if !ok {
			if isDir(p) {
				status = s.fail(name + ": " + a + ": is a directory")
			} else {
				status = s.fail(name + ": " + a + ": no such file")
			}
			continue
		}
		s.t.Print(strings.ReplaceAll(content, "\n", "\r\n"))
	}
	return status
}

func (s *shell) echo(args []string, line string) int {
	if k := strings.Index(line, ">"); k >= 0 {
		appendTo := strings.HasPrefix(line[k:], ">>")
		target := strings.TrimSpace(strings.TrimLeft(line[k:], ">"))
		text := strings.Join(splitArgs(line[:k])[1:], " ") + "\n"
		if target == "" || !writeFile(resolve(s.cwd, target), text, appendTo) {
			return s.fail("echo: cannot write " + target)
		}
		return 0
	}
	s.t.Print(strings.Join(args[1:], " ") + "\r\n")
	return 0
}

//go:noinline
func (s *shell) fail(msg string) int {
	s.t.Print(red + msg + reset + "\r\n")
	return 1
}

func (s *shell) ls(args []string, long bool) int {
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
			return 0
		}
		return s.fail("ls: no such file or directory")
	}
	var parts []string
	var rows [][3]string
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
			rows = append(rows, [3]string{dim + kind + reset + " guest ", fmt.Sprintf("%s%6d%s  ", green, size, reset), name})
			continue
		}
		parts = append(parts, name)
	}
	if long {
		s.t.Lay(func(cols int) []string { return longListing(rows, cols) })
	} else {
		s.t.Lay(func(cols int) []string { return columns(parts, cols) })
	}
	return 0
}

func longListing(rows [][3]string, cols int) []string {
	for _, from := range []int{0, 1, 2} {
		lines := make([]string, len(rows))
		fits := true
		for i, r := range rows {
			lines[i] = strings.Join(r[from:], "")
			fits = fits && ansi.StringWidth(lines[i]) <= cols
		}
		if fits || from == 2 {
			return fitLines(lines, cols)
		}
	}
	return nil
}

func colourName(e entry) string {
	switch {
	case e.dir:
		return blue + bold + e.name + "/" + reset
	case strings.HasSuffix(e.name, ".go"), strings.HasSuffix(e.name, ".mod"):
		return cyan + e.name + reset
	case strings.HasSuffix(e.name, ".md"):
		return yellow + e.name + reset
	case strings.HasSuffix(e.name, ".tape"):
		return green + e.name + reset
	case strings.HasSuffix(e.name, ".html"), strings.HasSuffix(e.name, ".css"):
		return purple + e.name + reset
	case strings.HasSuffix(e.name, ".tml"), strings.HasSuffix(e.name, ".mmd"), strings.HasSuffix(e.name, ".csv"):
		return blue + e.name + reset
	}
	return e.name
}

func argOr(args []string, i int, def string) string {
	if i < len(args) {
		return args[i]
	}
	return def
}

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

func cmdHelp(t *TTY) int {
	t.Lay(func(cols int) []string {
		lines := append([]string{fit(bold+"Things to try"+reset, cols)}, table(summaries, 2, green, cols)...)
		lines = append(lines, "")
		for _, l := range wrapWords("tuios keys: Ctrl+B then ? shows every keybinding.", cols) {
			lines = append(lines, dim+l+reset)
		}
		return lines
	})
	return 0
}
