package harness

// Seams the tests reach for. Production code calls the functions these wrap.

// compile compiles a rule on its own, the way parseManifest does, for a rule
// built in code rather than read from a file.
func (r *ScreenRule) compile(foldCase bool) error {
	return r.Gate.compile(foldCase, 0, &gateBudget{})
}
