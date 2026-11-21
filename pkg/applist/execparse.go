package applist

import (
	"errors"
	"strings"
)

var errNoExec = errors.New("applist: empty Exec")

var errUnterminatedQuote = errors.New("applist: unterminated quote in Exec")

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
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					n := s[i+1]
					if n == '"' || n == '`' || n == '$' || n == '\\' {
						cur.WriteByte(n)
						i += 2
						continue
					}
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

func expandFieldCodes(arg, path, nameVal, iconVal string) (out []string, drop bool) {
	if !strings.ContainsRune(arg, '%') {
		return []string{arg}, false
	}
	var b strings.Builder
	var pre []string
	sawCode := false
	wroteText := false

	for i := 0; i < len(arg); i++ {
		if arg[i] != '%' {
			b.WriteByte(arg[i])
			wroteText = true
			continue
		}
		if i+1 >= len(arg) {
			break
		}
		i++
		switch arg[i] {
		case '%':
			b.WriteByte('%')
			wroteText = true
		case 'f', 'F', 'u', 'U':
			sawCode = true
		case 'd', 'D', 'n', 'N', 'v', 'm':
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
