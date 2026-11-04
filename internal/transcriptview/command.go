package transcriptview

import (
	"path"
	"strings"
	"unicode/utf8"
)

// The label of a shell command: the program a tool call ran, with the
// wrappers a harness puts around it taken off. Codex runs every command as
// bash -lc '...', agents prefix cd dir && to most of theirs, and sudo, env,
// time and nice hide the program behind them. CommandLabel is for display
// only. What runs, what a risk rule reads and what goes back to a harness
// keep the command as it was.

// labelSteps bounds how many wrappers one label takes off. Each step eats
// at least one word, so the bound only matters for a pathological input.
const labelSteps = 32

// shellTools are the tool names harnesses give a shell command: Claude Code
// and Amp Bash, pi bash, Codex shell, shell_command, exec_command and
// local_shell, Gemini CLI run_shell_command, Cursor run_terminal_cmd, and
// Copilot on Windows powershell.
var shellTools = map[string]bool{
	"Bash": true, "bash": true, "shell": true, "shell_command": true, "exec_command": true,
	"local_shell": true, "run_shell_command": true, "run_terminal_cmd": true, "powershell": true,
}

// ShellTool reports whether a tool call's name is one harnesses give a
// shell command, whose target is then a command line.
func ShellTool(name string) bool {
	return shellTools[name]
}

// CommandLabel is the command a shell tool ran, as a person reads it: the
// real program, without bash -lc '...', sh -c, zsh -lc, env (with -i and
// X=Y), leading X=Y assignments, sudo and its flags, builtin, exec, time,
// command, nohup, nice, a leading cd dir && or cd dir;, and PowerShell's
// -NoProfile and -Command. The script of a -c is unquoted one level. Pipes
// and later && parts stay as they are. ESC shows as ␛, and every other C0
// control byte and DEL as its control picture, never raw.
//
// A label is never longer in runes than cmd, and it is never empty for a
// non-empty cmd: when nothing is left it is cmd trimmed, or cmd itself when
// that is only space.
func CommandLabel(cmd string) string {
	s := strings.TrimSpace(cmd)
	if s == "" {
		return ControlPictures(cmd)
	}
	label := s
	for range labelSteps {
		next, ok := unwrap(label)
		if !ok {
			break
		}
		next = strings.TrimSpace(next)
		if next == "" {
			break
		}
		label = next
	}
	return ControlPictures(label)
}

// ControlPictures is s with each C0 control byte and DEL as its picture from
// the Control Pictures block (ESC as ␛, a tab as ␉), and each byte that is
// not UTF-8 as U+FFFD, so a command shows what it holds and a terminal never
// acts on it. A rune stays one rune.
func ControlPictures(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c >= utf8.RuneSelf {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r < 0x20:
			b.WriteRune(0x2400 + r)
		case r == 0x7f:
			b.WriteRune('␡')
		default:
			// An invalid byte comes as RuneError, written as U+FFFD.
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unwrap takes one wrapper off the front of s. It reports false when s
// starts with no wrapper it knows, and then s is the label.
func unwrap(s string) (string, bool) {
	word, raw, rest := shellWord(s)
	if raw == "" {
		return "", false
	}
	if isAssignment(raw) {
		return rest, true
	}
	name := strings.ToLower(path.Base(strings.ReplaceAll(word, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "bash", "sh", "zsh", "dash", "ksh", "mksh", "ash", "fish":
		return unwrapShell(rest)
	case "env":
		return skipFlags(rest, "uC", []string{"--unset", "--chdir"}), true
	case "sudo", "doas":
		return skipFlags(rest, "ugpCrtTUDh", []string{"--user", "--group", "--prompt", "--close-from", "--role", "--type", "--command-timeout", "--other-user", "--chdir", "--host"}), true
	case "exec":
		return skipFlags(rest, "a", nil), true
	case "time":
		return skipFlags(rest, "fo", []string{"--format", "--output"}), true
	case "nice":
		return skipFlags(rest, "n", []string{"--adjustment"}), true
	case "command":
		// command -v and -V look a name up and run nothing.
		if w, _, _ := shellWord(rest); w == "-v" || w == "-V" {
			return "", false
		}
		return skipFlags(rest, "", nil), true
	case "builtin", "nohup":
		return rest, true
	case "cd", "pushd":
		_, dir, after := shellWord(rest)
		if dir == "" {
			return "", false
		}
		after = strings.TrimSpace(after)
		if a, ok := strings.CutPrefix(after, "&&"); ok {
			return a, true
		}
		if a, ok := strings.CutPrefix(after, ";"); ok {
			return a, true
		}
		return "", false
	case "powershell", "pwsh":
		return unwrapPowerShell(rest)
	case "cmd":
		return unwrapCmd(rest)
	}
	return "", false
}

// unwrapShell is the script a shell's -c runs, unquoted one level. A shell
// that runs a file, or a -c whose line goes on into a pipe or a list, is not
// taken off.
func unwrapShell(s string) (string, bool) {
	for {
		_, raw, rest := shellWord(s)
		switch {
		case raw == "":
			return "", false
		case raw == "--command" || raw == "-c" || len(raw) > 1 && raw[0] == '-' && raw[1] != '-' && strings.ContainsRune(raw, 'c'):
			script, sraw, after := shellWord(rest)
			if sraw == "" {
				return "", false
			}
			// Words after the script are its $0 and arguments. An operator
			// means the line goes on past the shell.
			if a := strings.TrimSpace(after); a != "" && strings.ContainsRune("|&;<>()", rune(a[0])) {
				return "", false
			}
			return script, true
		case raw == "-o" || raw == "+o" || raw == "-O" || raw == "+O" || raw == "--rcfile" || raw == "--init-file":
			_, _, s = shellWord(rest)
		case raw[0] == '-' || raw[0] == '+':
			s = rest
		default:
			return "", false
		}
	}
}

// unwrapPowerShell is the script of powershell -Command or pwsh -c, past
// switches such as -NoProfile and -ExecutionPolicy Bypass.
func unwrapPowerShell(s string) (string, bool) {
	for {
		_, raw, rest := shellWord(s)
		low := strings.ToLower(raw)
		switch {
		case raw == "":
			return "", false
		case len(low) >= 2 && strings.HasPrefix("-command", low):
			script := strings.TrimSpace(rest)
			if w, wraw, after := shellWord(script); wraw == script && strings.TrimSpace(after) == "" {
				return w, true
			}
			return script, true
		case slicesHasPrefix(low, "-executionpolicy", "-ep", "-ex", "-windowstyle", "-w", "-inputformat", "-if", "-outputformat", "-of",
			"-configurationname", "-workingdirectory", "-wd", "-version", "-psconsolefile", "-settingsfile"):
			_, _, s = shellWord(rest)
		case strings.HasPrefix(low, "-file") || strings.HasPrefix(low, "-encodedcommand") || low == "-e" || low == "-ec":
			return "", false
		case raw[0] == '-':
			s = rest
		default:
			// A bare script after the switches is a command.
			return s, true
		}
	}
}

// slicesHasPrefix reports whether low is one of the switches, written out or
// cut short as PowerShell allows (-ExecutionPolicy as -Exec).
func slicesHasPrefix(low string, switches ...string) bool {
	for _, sw := range switches {
		if low == sw || len(low) >= 4 && strings.HasPrefix(sw, low) {
			return true
		}
	}
	return false
}

// unwrapCmd is the command of cmd /c or cmd /k.
func unwrapCmd(s string) (string, bool) {
	for {
		_, raw, rest := shellWord(s)
		low := strings.ToLower(raw)
		switch {
		case low == "/c" || low == "/k":
			return rest, true
		case strings.HasPrefix(low, "/"):
			s = rest
		default:
			return "", false
		}
	}
}

// skipFlags is s past its leading flags, X=Y assignments and a "--". A short
// flag cluster that ends in one of withArg, or a long flag in long written
// without "=", takes the next word as its value.
func skipFlags(s, withArg string, long []string) string {
	for {
		_, raw, rest := shellWord(s)
		switch {
		case raw == "":
			return s
		case raw == "--":
			return rest
		case raw == "-":
			s = rest
		case strings.HasPrefix(raw, "--"):
			s = rest
			for _, l := range long {
				if raw == l {
					_, _, s = shellWord(rest)
				}
			}
		case raw[0] == '-' && len(raw) > 1:
			// A cluster of letters that ends in a flag with a value takes
			// the next word: sudo -Eu root. nice -10 and nice -n10 carry
			// the value in the word.
			s = rest
			if letters(raw[1:]) && strings.IndexByte(withArg, raw[len(raw)-1]) >= 0 {
				_, _, s = shellWord(rest)
			}
		case isAssignment(raw):
			s = rest
		default:
			return s
		}
	}
}

// letters reports whether s is only ASCII letters.
func letters(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// isAssignment reports whether a word, as written, is NAME=value.
func isAssignment(raw string) bool {
	name, _, ok := strings.Cut(raw, "=")
	if !ok || name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// shellWord splits the first word off s as a POSIX shell reads it: word is
// its value with one level of quotes and escapes taken off, raw is how it is
// written, and rest is what follows. A word ends at unquoted space or at an
// operator character. An operator at the start is a word of its own, so
// "&&" is never mistaken for a program. An unclosed quote runs to the end.
func shellWord(s string) (word, raw, rest string) {
	s = strings.TrimLeft(s, " \t\n")
	if s == "" {
		return "", "", ""
	}
	if strings.IndexByte("|&;<>()", s[0]) >= 0 {
		n := 1
		if len(s) > 1 && s[1] == s[0] && s[0] != '(' && s[0] != ')' {
			n = 2
		}
		return s[:n], s[:n], s[n:]
	}
	var b strings.Builder
	i := 0
loop:
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || strings.IndexByte("|&;<>()", c) >= 0:
			break loop
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				b.WriteString(s[i+1:])
				i = len(s)
				break loop
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 2
		case c == '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			i++
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	i = min(i, len(s))
	return b.String(), s[:i], s[i:]
}
