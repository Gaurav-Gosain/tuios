package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// A restored session is indistinguishable from a long-lived one on every
// session surface unless the tag is drawn, which was the whole complaint. These
// read it off the rendered frame rather than off the model.

// TestSwitcherRowTagsARestoredSession pins the tag onto the switcher row, and
// pins its absence on an ordinary session so the tag stays information.
func TestSwitcherRowTagsARestoredSession(t *testing.T) {
	m := switcherOS([]sessiontree.Node{
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name: "work", WindowCount: 2, Restored: true,
		}),
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name: "notes", WindowCount: 1,
		}),
	})
	out, _, _ := m.renderSessionSwitcher()

	if !strings.Contains(out, session.RestoredTag) {
		t.Errorf("the switcher does not tag a restored session:\n%s", out)
	}
	if strings.Count(out, session.RestoredTag) != 1 {
		t.Errorf("the tag was drawn %d times for one restored session:\n%s",
			strings.Count(out, session.RestoredTag), out)
	}
}

// TestSidebarSessionRowTagsARestoredSession does the same for the rail, at the
// full width where there is room for a word.
func TestSidebarSessionRowTagsARestoredSession(t *testing.T) {
	m := &OS{}
	pal := overlay.Palette{}

	restored := sessiontree.BuildSession(sessiontree.SessionInput{
		Name: "work", WindowCount: 2, Restored: true,
	})
	row := m.sidebarSessionRow(restored, sidebarVariantFull, 28, pal, false, false)
	if !strings.Contains(row, session.RestoredTag) {
		t.Errorf("the rail does not tag a restored session: %q", row)
	}

	plain := sessiontree.BuildSession(sessiontree.SessionInput{
		Name: "work", WindowCount: 2,
	})
	if got := m.sidebarSessionRow(plain, sidebarVariantFull, 28, pal, false, false); strings.Contains(got, session.RestoredTag) {
		t.Errorf("the rail tagged a session that was never restored: %q", got)
	}
}
