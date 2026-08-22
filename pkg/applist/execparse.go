package applist

// The Exec key of the freedesktop Desktop Entry Specification.
//
// Exec= is not a shell command line. It has its own quoting rules and its own
// field codes, and handing it to `sh -c` is both wrong and a command injection
// vector, because a .desktop file can be dropped anywhere on the XDG search
// path by anything. The value is tokenized here per the spec and the resulting
// argv is exec'd directly, with no shell anywhere in the path.
//
// Two consequences that look like bugs and are not:
//
//   - `Exec=notice-cmd && /usr/lib/real-app %u` (a real entry in the wild) runs
//     `notice-cmd` with the literal arguments "&&" and "/usr/lib/real-app". `&`
//     is a reserved character the spec requires to be quoted, and an unquoted
//     one has no shell meaning because there is no shell. GIO behaves the same
//     way.
//   - A single quote is not a quoting character in this format. Only the double
//     quote is. `'` must itself be escaped or double-quoted.
//
// Quoting rules (spec, "The Exec key"): arguments are separated by whitespace;
// an argument may be enclosed in double quotes; inside double quotes a
// backslash escapes `"`, `` ` ``, `$` and `\`. The characters
// `" ' \ > < ~ | & ; $ * ? # ( )` and backtick are reserved.
//
// Field codes:
//
//	%f %F   a file / a list of files      -> removed (nothing is being opened)
//	%u %U   a URL / a list of URLs        -> removed
//	%i      the Icon key as "--icon VAL"  -> two arguments, or none
//	%c      the translated Name           -> one argument
//	%k      the location of the file      -> one argument
//	%%      a literal percent
//	%d %D %n %N %v %m  deprecated         -> removed

import (
	"errors"
	"strings"
)

// errNoExec reports an Exec value with no program left to run, either because
// it was blank or because every token was a field code that gets removed.
var errNoExec = errors.New("applist: empty Exec")

// errUnterminatedQuote reports an Exec value whose double quote never closes.
// The spec gives no reading for the remainder, so guessing one would invent an
// argv the author never wrote.
var errUnterminatedQuote = errors.New("applist: unterminated quote in Exec")

// parseExec tokenizes an already value-unescaped Exec string and expands its
// field codes. iconVal and nameVal feed %i and %c; path feeds %k.
//
// The returned argv is safe to hand straight to execve: it never contains a
// shell, and no element is re-interpreted by anything downstream.
func parseExec(execVal, path, nameVal, iconVal string) ([]string, error) {
	toks, err := tokenizeExec(execVal)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, errNoExec
	}

	var argv []string
	for _, t := range toks {
		expanded, drop := expandFieldCodes(t, path, nameVal, iconVal)
		if drop {
			continue
		}
		argv = append(argv, expanded...)
	}
	if len(argv) == 0 {
		return nil, errNoExec
	}
	return argv, nil
}

// tokenizeExec splits an Exec value into arguments.
//
// The spec's reserved characters are not tracked. Recording them would only
// support a diagnostic about an entry relying on shell behaviour it will not
// get, and nothing here shells out, so their unquoted presence changes no
// output: they are literal bytes of the token either way.
func tokenizeExec(s string) ([]string, error) {
	var (
		toks    []string
		cur     strings.Builder
		started bool
	)
	flush := func() {
		if started {
			toks = append(toks, cur.String())
			cur.Reset()
			started = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		case c == '"':
			started = true
			// Consume up to the matching close quote, honouring the four
			// escapes the spec defines inside quotes.
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					n := s[i+1]
					if n == '"' || n == '`' || n == '$' || n == '\\' {
						cur.WriteByte(n)
						i += 2
						continue
					}
					// An escape the spec does not define: keep the backslash
					// literally rather than inventing a meaning for it.
					cur.WriteByte('\\')
					i++
					continue
				}
				cur.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, errUnterminatedQuote
			}
		case c == '\\':
			// Outside quotes a backslash is only valid as part of an escape, so
			// it takes the next byte literally and a trailing lone one is
			// dropped.
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				started = true
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return toks, nil
}

// expandFieldCodes turns one tokenized argument into zero or more real
// arguments. drop reports that the argument disappeared entirely, which is what
// "%f should be removed" means for an argument that was only a field code.
func expandFieldCodes(arg, path, nameVal, iconVal string) (out []string, drop bool) {
	if !strings.ContainsRune(arg, '%') {
		return []string{arg}, false
	}
	var b strings.Builder
	var pre []string // arguments emitted before this one (%i)
	sawCode := false
	wroteText := false

	for i := 0; i < len(arg); i++ {
		if arg[i] != '%' {
			b.WriteByte(arg[i])
			wroteText = true
			continue
		}
		if i+1 >= len(arg) {
			// A trailing lone '%' is not a valid field code. The spec calls an
			// unrecognised code an error; be lenient and drop it.
			break
		}
		i++
		switch arg[i] {
		case '%':
			b.WriteByte('%')
			wroteText = true
		case 'f', 'F', 'u', 'U':
			// No files or URLs are being opened, so these are removed.
			sawCode = true
		case 'd', 'D', 'n', 'N', 'v', 'm':
			// Deprecated: "must be removed" and not substituted.
			sawCode = true
		case 'i':
			sawCode = true
			if iconVal != "" {
				pre = append(pre, "--icon", iconVal)
			}
		case 'c':
			sawCode = true
			b.WriteString(nameVal)
			if nameVal != "" {
				wroteText = true
			}
		case 'k':
			sawCode = true
			b.WriteString(path)
			if path != "" {
				wroteText = true
			}
		default:
			// Unknown field code: remove it rather than passing it through, so
			// an application never receives a literal "%z".
			sawCode = true
		}
	}
	text := b.String()
	if sawCode && !wroteText && strings.TrimSpace(text) == "" {
		if len(pre) > 0 {
			return pre, false
		}
		return nil, true
	}
	return append(pre, text), false
}
