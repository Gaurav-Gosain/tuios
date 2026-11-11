package hostsetup

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"
)

// shellJoin joins words into one command line, each quoted for sh when it
// needs it.
func shellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = shquote.Word(w)
	}
	return strings.Join(out, " ")
}
