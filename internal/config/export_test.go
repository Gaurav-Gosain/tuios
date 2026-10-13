package config

// Seams the tests reach for. Production code calls the functions these wrap.

// PrefixMenuActions lists every action a which-key menu has a row for, in
// order. The coverage test reads it to check that each action a prefix binds
// by default has a row with a description.
func PrefixMenuActions(prefixType string, st MenuState) []string {
	var out []string
	for _, sec := range menuSections(prefixType, st) {
		for _, mr := range sec.rows {
			for _, p := range mr.parts {
				out = append(out, p.action)
			}
		}
	}
	return out
}
