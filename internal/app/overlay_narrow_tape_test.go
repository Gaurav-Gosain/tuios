//go:build !slim

package app

import (
	"strings"
	"testing"
	"time"
)

// TestTapeDialogsFitNarrowScreens renders the tape manager and the project-tape
// review dialog, both of which size themselves from their own content.
func TestTapeDialogsFitNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)

			m.InitTapeManager()
			m.TapeManager.Files = []TapeFile{
				{Name: "a-tape-file-name-that-is-much-too-long-to-fit", Size: 4096, Modified: time.Now()},
				{Name: "short", Size: 12, Modified: time.Now()},
			}
			assertFitsScreen(t, "tape manager", m.RenderTapeManager(), sc.w, sc.h)

			m.TapeManager.Mode = TapeManagerNaming
			m.TapeManager.NameBuffer = strings.Repeat("name", 30)
			assertFitsScreen(t, "tape manager naming", m.RenderTapeManager(), sc.w, sc.h)

			m.TapeManager.Mode = TapeManagerConfirmDelete
			assertFitsScreen(t, "tape manager delete", m.RenderTapeManager(), sc.w, sc.h)

			m.TapeReview = &TapeReviewState{
				Path:    "/home/someone/very/deep/project/directory/tree/.tuios.tape",
				Dir:     "/home/someone/very/deep/project/directory/tree",
				Content: []byte(strings.Repeat("Type \"a command line that is quite long indeed\"\n", 40)),
			}
			assertFitsScreen(t, "tape review", m.RenderTapeReview(), sc.w, sc.h)
		})
	}
}
