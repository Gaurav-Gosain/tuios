package harness

// Seams the tests reach for. Production code calls the functions these wrap.

// ParseOptions reads the numbered menu nearest the bottom of lines: options
// numbered 1 to N with at most maxOptionGap other lines between two of them.
// A run that does not count down to 1 is not a menu and gives nothing, which
// is what keeps a numbered list in the transcript above from posing as one.
func ParseOptions(lines []string) []Option {
	out, _ := parseOptions(lines)
	return out
}

// compile compiles a rule on its own, the way parseManifest does, for a rule
// built in code rather than read from a file.
func (r *ScreenRule) compile(foldCase bool) error {
	return r.Gate.compile(foldCase, 0, &gateBudget{})
}
