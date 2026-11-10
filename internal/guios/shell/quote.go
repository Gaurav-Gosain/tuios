package shell

import "strings"

// psQuotes are the characters PowerShell ends a string of single quotes
// at: the apostrophe, and the typographic single quotes, which it takes
// for one.
var psQuotes = strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019", "\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b")

// PSQuote quotes s for PowerShell, as a string of single quotes, in
// which nothing expands.
func PSQuote(s string) string { return "'" + psQuotes.Replace(s) + "'" }
