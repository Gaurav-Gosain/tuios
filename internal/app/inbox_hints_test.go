package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// hintText is a footer as "key label" pairs, for matching.
func hintText(hints []overlay.Hint) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = h.Key + " " + h.Label
	}
	return strings.Join(parts, " | ")
}
