package app

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// fgSeq is the escape sequence a foreground color renders as, so a row can be
// checked for the color it was actually drawn in rather than for a color name.
func fgSeq(c color.Color) string {
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	return rendered[:strings.Index(rendered, "x")]
}

// TestUnknownWorkspaceTagsNothing: a pane whose workspace is not known, and a
// session whose current workspace is not known, must both go untagged. An older
// daemon sends neither field, and a rail that tagged every row on that listing
// would be confidently wrong about all of them.
func TestUnknownWorkspaceTagsNothing(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	tree := sessiontree.Build([]sessiontree.SessionInput{
		// No CurrentWorkspace and no per-pane workspace: an older daemon's reply.
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server"},
			{ID: "eeeeeeee5555", Title: "worker"},
		}},
	})
	for _, e := range m.sidebarTerminals(tree.Sessions, "api") {
		if e.Tag != "" {
			t.Errorf("pane %q was tagged %q off a listing that says nothing about workspaces", e.WindowID, e.Tag)
		}
	}
}
