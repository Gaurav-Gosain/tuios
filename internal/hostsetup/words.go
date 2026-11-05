package hostsetup

import "strings"

// shellJoin joins words into one command line, each quoted for sh when it
// needs it.
func shellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = shellQuote(w)
	}
	return strings.Join(out, " ")
}

// shellQuote is w as sh reads it back unchanged.
func shellQuote(w string) string {
	if w == "" {
		return "''"
	}
	if strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-") == "" {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}
