package risk

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type command struct {
	raw           string
	argv          []string
	writes        []string
	pipedFrom     [][]string
	fedBy         [][]string
	sudo          bool
	scope         string
	inPipe        bool
	chdir, cdpath bool
}

func (c command) name() string {
	if len(c.argv) == 0 {
		return ""
	}
	return filepath.Base(c.argv[0])
}

func (c command) args() []string {
	if len(c.argv) < 2 {
		return nil
	}
	return c.argv[1:]
}

func parseCommands(text string, depth int) []command {
	return parseCommandsIn(text, depth, "")
}

func parseCommandsIn(text string, depth int, prefix string) []command {
	var out []command
	var nested []string
	segments := splitSegments(text, &nested)
	var pipeline [][]string
	for si, seg := range segments {
		words, writes := tokenize(seg.text, &nested)
		c := command{raw: stripLeadRaw(strings.TrimSpace(seg.text)), writes: writes, scope: prefix + seg.scope}
		c.inPipe = seg.piped || (si+1 < len(segments) && segments[si+1].piped)
		for {
			n := len(words)
			for len(words) > 0 && leadWords[words[0]] {
				words = words[1:]
			}
			for len(words) > 0 && isAssignment(words[0]) {
				if strings.HasPrefix(words[0], "CDPATH=") {
					c.cdpath = true
				}
				words = words[1:]
			}
			if len(words) == n {
				break
			}
		}
		if len(words) > 0 && (words[0] == "export" || words[0] == "declare" || words[0] == "typeset" || words[0] == "readonly" || words[0] == "set") {
			for _, w := range words[1:] {
				if strings.HasPrefix(w, "CDPATH") {
					c.cdpath = true
				}
			}
		}
		words, c.sudo, c.chdir = unwrap(words)
		c.argv = words
		if !seg.piped {
			pipeline = nil
		}
		c.pipedFrom = pipeline
		if len(words) > 0 {
			pipeline = append(append([][]string(nil), pipeline...), words)
		}
		var line string
		hasLine := false
		if depth < maxDepth {
			line, hasLine = commandLine(c)
			if runsText(c.name()) || hasLine {
				for _, sub := range seg.subst {
					for _, fc := range parseCommands(sub, depth+1) {
						if len(fc.argv) > 0 {
							c.fedBy = append(c.fedBy, fc.argv)
						}
					}
				}
			}
		}
		out = append(out, c)
		if hasLine {
			scope := c.scope + "/c" + strconv.Itoa(si)
			if c.name() == "eval" {
				scope = c.scope
			}
			out = append(out, parseCommandsIn(line, depth+1, scope)...)
		}
	}
	if depth < maxDepth {
		for i, n := range nested {
			out = append(out, parseCommandsIn(n, depth+1, prefix+"/s"+strconv.Itoa(i))...)
		}
	}
	return out
}

var leadWords = map[string]bool{
	"{": true, "}": true, "!": true, "if": true, "then": true, "elif": true,
	"else": true, "while": true, "until": true, "do": true, "fi": true,
	"done": true, "esac": true,
}

func stripLeadRaw(raw string) string {
	for {
		end := strings.IndexAny(raw, " \t")
		if end < 0 || !leadWords[raw[:end]] {
			return raw
		}
		raw = strings.TrimLeft(raw[end:], " \t")
	}
}

func runsText(name string) bool {
	return name == "eval" || name == "source" || name == "." || slices.Contains(pipeTargets, name)
}

func commandLine(c command) (string, bool) {
	name := c.name()
	args := c.args()
	if name == "eval" {
		if len(args) == 0 {
			return "", false
		}
		return strings.Join(args, " "), true
	}
	if !isShellName(name) {
		return "", false
	}
	sawC := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--" || a == "-":
			if sawC && i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		case a == "-o" || a == "+o" || a == "-O" || a == "+O":
			i++
		case a == "--command" && name == "fish":
			if i+1 < len(args) {
				return args[i+1], true
			}
		case strings.HasPrefix(a, "--command=") && name == "fish":
			return strings.TrimPrefix(a, "--command="), true
		case strings.HasPrefix(a, "--"):
		case (strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+")) && len(a) > 1:
			if strings.HasPrefix(a, "-") && strings.Contains(a[1:], "c") {
				sawC = true
			}
		default:
			return a, sawC
		}
	}
	return "", false
}

var shellNames = []string{"sh", "bash", "zsh", "dash", "ksh", "fish"}

func isShellName(name string) bool {
	return slices.Contains(shellNames, name)
}

var wrappers = map[string]bool{
	"sudo": true, "doas": true, "env": true, "nohup": true, "time": true,
	"nice": true, "command": true, "exec": true, "xargs": true, "timeout": true,
	"stdbuf": true, "ionice": true,
}

var wrapperTakesValue = map[string]bool{
	"-u": true, "-g": true, "-n": true, "-C": true, "-p": true, "-h": true,
	"-U": true, "-r": true, "-t": true, "-I": true, "-P": true, "-L": true,
	"-s": true, "-k": true, "-D": true, "--chdir": true,
}

func unwrap(words []string) ([]string, bool, bool) {
	sudo, chdir := false, false
	for len(words) > 0 && wrappers[filepath.Base(words[0])] {
		w := filepath.Base(words[0])
		if w == "sudo" || w == "doas" {
			sudo = true
		}
		words = words[1:]
		for len(words) > 0 && (strings.HasPrefix(words[0], "-") || isAssignment(words[0]) || (w == "timeout" && isDuration(words[0]))) {
			flag := words[0]
			if w == "env" && (flag == "-C" || strings.HasPrefix(flag, "--chdir") || (strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.Contains(flag, "C"))) {
				chdir = true
			}
			if strings.HasPrefix(flag, "CDPATH=") || (w == "sudo" && strings.HasPrefix(flag, "--chdir")) || (w == "sudo" && flag == "-D") {
				chdir = true
			}
			words = words[1:]
			if wrapperTakesValue[flag] && len(words) > 0 {
				words = words[1:]
			}
		}
	}
	return words, sudo, chdir
}

func isDuration(w string) bool {
	if w == "" || w[0] < '0' || w[0] > '9' {
		return false
	}
	return strings.Trim(w, "0123456789.smhd") == ""
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

type segment struct {
	text  string
	piped bool
	scope string
	subst []string
}

func splitSegments(text string, nested *[]string) []segment {
	var out []segment
	var cur strings.Builder
	var subst []string
	piped := false
	var groups []string
	opened := 0
	flush := func(nextPiped bool) {
		if strings.TrimSpace(cur.String()) == "" && len(subst) == 0 {
			cur.Reset()
			piped = piped || nextPiped
			return
		}
		var scope strings.Builder
		for _, g := range groups {
			scope.WriteString("/g")
			scope.WriteString(g)
		}
		out = append(out, segment{text: cur.String(), piped: piped, subst: subst, scope: scope.String()})
		cur.Reset()
		subst = nil
		piped = nextPiped
	}
	var single, double bool
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case ch == '\\' && !single && i+1 < len(text):
			cur.WriteByte(ch)
			cur.WriteByte(text[i+1])
			i++
			continue
		case ch == '\'' && !double:
			single = !single
		case ch == '"' && !single:
			double = !double
		case single:
		case ch == '$' && i+1 < len(text) && text[i+1] == '(':
			end := matchParen(text, i+1)
			if end >= i+2 {
				*nested = append(*nested, text[i+2:end])
				subst = append(subst, text[i+2:end])
			}
			cur.WriteString(text[i:min(end+1, len(text))])
			i = end
			continue
		case ch == '`':
			end := strings.IndexByte(text[i+1:], '`')
			if end < 0 {
				end = len(text) - i - 1
			}
			*nested = append(*nested, text[i+1:i+1+end])
			subst = append(subst, text[i+1:i+1+end])
			cur.WriteString(text[i:min(i+2+end, len(text))])
			i += 1 + end
			continue
		case double:
		case (ch == '<' || ch == '>') && i+1 < len(text) && text[i+1] == '(':
			end := matchParen(text, i+1)
			if end >= i+2 {
				*nested = append(*nested, text[i+2:end])
				subst = append(subst, text[i+2:end])
			}
			if ch == '>' {
				cur.WriteString(" /dev/fd/63 ")
			} else {
				cur.WriteString("/dev/fd/63")
			}
			i = end
			continue
		case ch == '(' || ch == ')':
			flush(false)
			if ch == '(' {
				opened++
				groups = append(groups, strconv.Itoa(opened))
			} else if len(groups) > 0 {
				groups = groups[:len(groups)-1]
			}
			continue
		case ch == ';' || ch == '\n':
			flush(false)
			continue
		case ch == '&':
			prev := byte(0)
			if i > 0 {
				prev = text[i-1]
			}
			next := byte(0)
			if i+1 < len(text) {
				next = text[i+1]
			}
			if prev == '>' || prev == '<' || next == '>' {
				break
			}
			if next == '&' {
				i++
			}
			flush(false)
			continue
		case ch == '|':
			if i+1 < len(text) && text[i+1] == '|' {
				i++
				flush(false)
				continue
			}
			if i+1 < len(text) && text[i+1] == '&' {
				i++
			}
			flush(true)
			continue
		}
		cur.WriteByte(ch)
	}
	flush(false)
	return out
}

func matchParen(text string, open int) int {
	depth := 0
	var single, double bool
	for i := open; i < len(text); i++ {
		ch := text[i]
		switch {
		case ch == '\\' && !single:
			i++
		case ch == '\'' && !double:
			single = !single
		case ch == '"' && !single:
			double = !double
		case single || double:
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(text) - 1
}

func tokenize(text string, nested *[]string) (words, writes []string) {
	var cur strings.Builder
	have := false
	redirect := false
	emit := func() {
		if !have {
			return
		}
		w := cur.String()
		cur.Reset()
		have = false
		if redirect {
			writes = append(writes, w)
			redirect = false
			return
		}
		words = append(words, w)
	}
	var single, double bool
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case single:
			if ch == '\'' {
				single = false
				continue
			}
			cur.WriteByte(ch)
			continue
		case double:
			switch {
			case ch == '"':
				double = false
			case ch == '\\' && i+1 < len(text):
				i++
				cur.WriteByte(text[i])
			default:
				cur.WriteByte(ch)
			}
			continue
		}
		switch {
		case ch == '\\' && i+1 < len(text):
			i++
			cur.WriteByte(text[i])
			have = true
		case ch == '\'':
			single, have = true, true
		case ch == '"':
			double, have = true, true
		case ch == ' ' || ch == '\t' || ch == '\r':
			emit()
		case ch == '>':
			if s := cur.String(); have && (s == "1" || s == "2" || s == "&") {
				cur.Reset()
				have = false
			}
			emit()
			if i+1 < len(text) && text[i+1] == '>' {
				i++
			}
			if i+1 < len(text) && text[i+1] == '&' {
				i++
				for i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9' {
					i++
				}
				continue
			}
			redirect = true
		case ch == '<':
			emit()
		default:
			cur.WriteByte(ch)
			have = true
		}
	}
	emit()
	return words, writes
}
