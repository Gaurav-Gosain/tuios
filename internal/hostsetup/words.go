package hostsetup

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"
)

func shellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = shquote.Word(w)
	}
	return strings.Join(out, " ")
}
