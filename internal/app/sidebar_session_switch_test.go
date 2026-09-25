package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

func sessionIDs(sessions []sessiontree.Node) []string {
	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.ID)
	}
	return names
}
