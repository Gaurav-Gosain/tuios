package risk

import (
	"path/filepath"
	"slices"
	"strings"
)

// command is one simple command of a shell line: the words, the redirects
// that write, and whether it reads a pipe from the command before it.
type command struct {
	// raw is the command's text as written, for the person's patterns.
	raw string
	// argv is the words with quotes removed, leading VAR=value assignments
	// dropped.
	argv []string
	// writes are the targets of > and >> redirects.
	writes []string
	// pipedFrom is the argv of every command earlier in the same pipeline,
	// nearest last, so a rule can see "curl ... | sh".
	pipedFrom [][]string
	// fedBy is the argv of every command inside a substitution in this
	// command's words when this command runs what that prints: the curl of
	// bash <(curl x) and of sh -c "$(curl x)".
	fedBy [][]string
	// sudo is set when the command runs under sudo or doas.
	sudo bool
}

// name is the command's program: the base name of argv[0].
func (c command) name() string {
	if len(c.argv) == 0 {
		return ""
	}
	return filepath.Base(c.argv[0])
}

// args are the words after the program.
func (c command) args() []string {
	if len(c.argv) < 2 {
		return nil
	}
	return c.argv[1:]
}

// parseCommands splits a shell line into its simple commands, following
// command substitutions, process substitutions, sh -c (and -lc, -ec and the
// like) and eval into the commands they run. It is a reader of what a person
// or a model types, not a shell: it knows quotes, escapes, the command
// separators, subshells and groups, the reserved words that can come before a
// command, pipes and redirects, and nothing else.
func parseCommands(text string, depth int) []command {
	var out []command
	var nested []string
	segments := splitSegments(text, &nested)
	var pipeline [][]string
	for _, seg := range segments {
		words, writes := tokenize(seg.text, &nested)
		c := command{raw: stripLeadRaw(strings.TrimSpace(seg.text)), writes: writes}
		for {
			n := len(words)
			for len(words) > 0 && leadWords[words[0]] {
				words = words[1:]
			}
			for len(words) > 0 && isAssignment(words[0]) {
				words = words[1:]
			}
			if len(words) == n {
				break
			}
		}
		words, c.sudo = unwrap(words)
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
				// What a substitution in the command's own words prints
				// is what this command runs: bash <(curl x),
				// sh -c "$(curl x)", eval "$(curl x)".
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
		// sh -c 'command' and eval 'command' run a command line of their own.
		if hasLine {
			out = append(out, parseCommands(line, depth+1)...)
		}
	}
	if depth < maxDepth {
		for _, n := range nested {
			out = append(out, parseCommands(n, depth+1)...)
		}
	}
	return out
}

// leadWords are words that can stand before a command without being it: the
// reserved words that open a branch or a loop body, negation and a group
// opener. A subshell's ( and ) are split off as separators before this.
var leadWords = map[string]bool{
	"{": true, "}": true, "!": true, "if": true, "then": true, "elif": true,
	"else": true, "while": true, "until": true, "do": true, "fi": true,
	"done": true, "esac": true,
}

// stripLeadRaw drops the lead words from the front of a command's text, so a
// person's pattern anchored at the start sees the command.
func stripLeadRaw(raw string) string {
	for {
		end := strings.IndexAny(raw, " \t")
		if end < 0 || !leadWords[raw[:end]] {
			return raw
		}
		raw = strings.TrimLeft(raw[end:], " \t")
	}
}

// runsText reports whether a program runs what a substitution in its words
// prints: a shell reading a script file, eval, or source.
func runsText(name string) bool {
	return name == "eval" || name == "source" || name == "." || slices.Contains(pipeTargets, name)
}

// commandLine returns the command line a command runs as its argument: the
// operand after a short flag cluster holding c for a shell (sh -c, bash -lc,
// sh -ec), or the words of eval joined.
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
			// set -o and shopt take the option name as the next word.
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
			// The first operand is the command line under -c, else a
			// script file.
			return a, sawC
		}
	}
	return "", false
}

// shellNames are programs that run a command line or a script from stdin.
var shellNames = []string{"sh", "bash", "zsh", "dash", "ksh", "fish"}

func isShellName(name string) bool {
	return slices.Contains(shellNames, name)
}

// wrappers are programs that run the command after them. The flags of sudo
// and the others are skipped with them, so "sudo -u root rm -rf /" reads as
// rm.
var wrappers = map[string]bool{
	"sudo": true, "doas": true, "env": true, "nohup": true, "time": true,
	"nice": true, "command": true, "exec": true, "xargs": true, "timeout": true,
	"stdbuf": true, "ionice": true,
}

// wrapperTakesValue are wrapper flags followed by a value word.
var wrapperTakesValue = map[string]bool{
	"-u": true, "-g": true, "-n": true, "-C": true, "-p": true, "-h": true,
	"-U": true, "-r": true, "-t": true, "-I": true, "-P": true, "-L": true,
	"-s": true, "-k": true,
}

// unwrap drops the wrapper programs in front of a command, reporting whether
// one of them was sudo or doas.
func unwrap(words []string) ([]string, bool) {
	sudo := false
	for len(words) > 0 && wrappers[filepath.Base(words[0])] {
		w := filepath.Base(words[0])
		if w == "sudo" || w == "doas" {
			sudo = true
		}
		words = words[1:]
		for len(words) > 0 && (strings.HasPrefix(words[0], "-") || isAssignment(words[0]) || (w == "timeout" && isDuration(words[0]))) {
			flag := words[0]
			words = words[1:]
			if wrapperTakesValue[flag] && len(words) > 0 {
				words = words[1:]
			}
		}
	}
	return words, sudo
}

// isDuration reports whether a word reads as timeout's duration: 10, 5s, 1.5m.
func isDuration(w string) bool {
	if w == "" || w[0] < '0' || w[0] > '9' {
		return false
	}
	return strings.Trim(w, "0123456789.smhd") == ""
}

// isAssignment reports whether a word is a NAME=value assignment.
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

// segment is the text between two command separators, and whether a pipe
// joined it to the one before.
type segment struct {
	text  string
	piped bool
	// subst is the text inside each $( ), backtick pair, <( ) and >( ) in
	// the segment.
	subst []string
}

// splitSegments splits text on ; & && || | |& ( ) and newlines outside
// quotes. The contents of $( ), backticks, <( ) and >( ) are appended to
// nested, to be read as commands of their own, and to the segment's subst. A
// process substitution stands in the segment as /dev/fd/63, the word a shell
// passes in its place.
func splitSegments(text string, nested *[]string) []segment {
	var out []segment
	var cur strings.Builder
	var subst []string
	piped := false
	flush := func(nextPiped bool) {
		if strings.TrimSpace(cur.String()) == "" && len(subst) == 0 {
			// Nothing between two separators, as between ) and |: the
			// pipe still joins what came before to what comes after.
			cur.Reset()
			piped = piped || nextPiped
			return
		}
		out = append(out, segment{text: cur.String(), piped: piped, subst: subst})
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
				// >( ) is a file the command writes into, not a redirect.
				cur.WriteString(" /dev/fd/63 ")
			} else {
				cur.WriteString("/dev/fd/63")
			}
			i = end
			continue
		case ch == '(' || ch == ')':
			// A subshell opens or closes: what follows ( is a command.
			flush(false)
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

// matchParen returns the index of the ) that closes the ( at open, or the end
// of text when none does.
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

// tokenize splits one simple command into words with quotes removed, and
// returns the targets of its writing redirects (> >> &> 1> 2>) apart.
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
			// A digit or & glued in front is the stream being redirected,
			// not a word.
			if s := cur.String(); have && (s == "1" || s == "2" || s == "&") {
				cur.Reset()
				have = false
			}
			emit()
			if i+1 < len(text) && text[i+1] == '>' {
				i++
			}
			if i+1 < len(text) && text[i+1] == '&' {
				// >&2 duplicates a stream and writes no file.
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
