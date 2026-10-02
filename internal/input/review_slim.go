//go:build slim

package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// tuios-slim has no review overlay, so ReviewOpen is never true and this is
// never reached. It stands in for review_input.go.
func handleReviewInput(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) { return o, nil }
