package tuie2e

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAgentsViewLayoutKeysSayWhyWhenNarrow presses w and Z where they have
// no room to act. At 120 columns the side column cannot show: w says so in
// the key bar, and the saved choice in sidebar.json is not touched. Closed by
// a double-click and opened again, the view draws no note. Widened to 200 the
// side column is drawn, so the press hid nothing, and w hides and shows it
// there as before. At 70 columns Z says it needs 80 and sets no zoom: the key
// bar offers no unzoom, and widened to 120 the list and the focus are both
// drawn. Z at 120 zooms as before.
//
// How this could pass wrongly, written down first:
//   - A w that never reached the view would leave sidebar.json unchanged. The
//     note is waited for first, and the file is read for a further second
//     after it: its bytes, its size and its time, since a save of the same
//     bytes still moves the time.
//   - The cursor walk marks the finished rows it passes as viewed, which
//     saves the file. The file is recorded once the cursor is on bench-idle,
//     a working agent whose view writes nothing.
//   - A resize does not wait for the frame. Every resize is followed by a
//     wait for the frame at the new width before a key or a held check.
//   - A note that stays after the next key would still be on the key bar at
//     70 columns, where the side column has no room either. The two presses
//     of w at 200 are other keys, and the key bar at 70 is read for it.
//   - The key bar is cut from its end at 70 columns. The note is the first
//     hint, and the wait reads it whole there.
//   - A double-click that left the view open would show no note on a view
//     that was never opened again. The view is waited for gone before
//     ctrl+b i opens it.
//
// Negative controls: NEGATIVE_CONTROLS.md, "w and Z in the agents view say
// why on a narrow view".
func TestAgentsViewLayoutKeysSayWhyWhenNarrow(t *testing.T) {
	term, base, _, _ := startCockpitFleet(t, 120, 40, nil)
	path := sidebarStatePath(base)

	type fileState struct {
		absent bool
		data   []byte
		size   int64
		mod    time.Time
	}
	readState := func() fileState {
		fi, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return fileState{absent: true}
		}
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return fileState{data: data, size: fi.Size(), mod: fi.ModTime()}
	}
	sameState := func(a, b fileState) bool {
		if a.absent || b.absent {
			return a.absent == b.absent
		}
		return bytes.Equal(a.data, b.data) && a.size == b.size && a.mod.Equal(b.mod)
	}
	describe := func(f fileState) string {
		if f.absent {
			return "absent"
		}
		return "size " + strconv.FormatInt(f.size, 10) + ", modified " + f.mod.Format(time.RFC3339Nano) + ": " + string(f.data)
	}
	keyBar := func() string { return ckKeyBar(term.Screen()) }
	listAlone := func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		text := s.Text()
		return cols == 70 && strings.Contains(ckCursorRow(s), "bench-idle") &&
			strings.Contains(text, "Needs you") && !strings.Contains(text, "Says:") &&
			!strings.Contains(text, "Reply to bench-idle") && !strings.Contains(text, "Thread    Checkpoints")
	}
	listAndFocus := func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		text := s.Text()
		return cols == 120 && strings.Contains(ckCursorRow(s), "bench-idle") && strings.Contains(text, "Needs you") &&
			strings.Contains(text, "Says:") && strings.Contains(text, "Reply to bench-idle") &&
			strings.Contains(text, "Thread    Checkpoints")
	}

	cursorTo(t, term, func(line string) bool { return strings.Contains(line, "bench-idle") })
	waitUntil(t, "sidebar.json holds the client's claim of the file (its socket) before w is pressed", uiTimeout, func() (bool, string) {
		return strings.Contains(string(readState().data), "\"socket\""), "sidebar.json: " + describe(readState())
	})
	before := readState()
	ckPress(t, term, "w")
	waitUntil(t, "w at 120 columns says in the key bar that the side column needs 170 columns", uiTimeout, func() (bool, string) {
		return strings.Contains(keyBar(), "the side column needs 170 columns"), term.Snapshot()
	})
	stayTrue(t, "w at 120 columns leaves sidebar.json as it was and the note on the key bar", time.Second, func() (bool, string) {
		now := readState()
		return sameState(before, now) && strings.Contains(keyBar(), "the side column needs 170 columns"),
			"sidebar.json before w: " + describe(before) + "\nsidebar.json now: " + describe(now) + "\n" + term.Snapshot()
	})
	saveFrame(t, term, "layout-narrow-w")

	s := term.Screen()
	row := fleetRowWith(s, "bench-idle")
	if row < 0 {
		t.Fatalf("bench-idle is not in the fleet\n%s", term.Snapshot())
	}
	clickAt(t, term, len([]rune(s.Line(row)[:strings.Index(s.Line(row), "bench-idle")])), row, 2)
	waitUntil(t, "a double-click on bench-idle closes the agents view", uiTimeout, func() (bool, string) {
		return !ckViewUp(term.Screen()), term.Snapshot()
	})
	ckPress(t, term, tuitest.Ctrl('b'), "i")
	waitUntil(t, "ctrl+b i opens the agents view again at 120 columns", uiTimeout, func() (bool, string) {
		s := term.Screen()
		cols, _ := s.Size()
		return cols == 120 && ckViewUp(s), term.Snapshot()
	})
	stayTrue(t, "the agents view opened again holds no side note in the key bar", time.Second, func() (bool, string) {
		return !strings.Contains(keyBar(), "the side column needs"), term.Snapshot()
	})
	cursorTo(t, term, func(line string) bool { return strings.Contains(line, "bench-idle") })
	ckPress(t, term, "w")
	waitUntil(t, "w at 120 columns in the view opened again says that the side column needs 170 columns", uiTimeout, func() (bool, string) {
		return strings.Contains(keyBar(), "the side column needs 170 columns"), term.Snapshot()
	})

	if err := term.Resize(200, 50); err != nil {
		t.Fatalf("resize to 200x50: %v", err)
	}
	waitUntil(t, "at 200 columns the side column is drawn and the key bar holds no side note", uiTimeout, func() (bool, string) {
		s := term.Screen()
		return strings.Contains(s.Text(), "Last turn") && !strings.Contains(ckKeyBar(s), "the side column needs"), term.Snapshot()
	})
	saveFrame(t, term, "layout-narrow-w-200")
	ckPress(t, term, "w")
	waitUntil(t, "w at 200 columns hides the side column", uiTimeout, func() (bool, string) {
		return !strings.Contains(term.Screen().Text(), "Last turn"), term.Snapshot()
	})
	ckPress(t, term, "w")
	waitUntil(t, "w at 200 columns shows the side column again", uiTimeout, func() (bool, string) {
		return strings.Contains(term.Screen().Text(), "Last turn"), term.Snapshot()
	})

	cursorTo(t, term, func(line string) bool { return strings.Contains(line, "bench-idle") })
	if err := term.Resize(70, 40); err != nil {
		t.Fatalf("resize to 70x40: %v", err)
	}
	waitUntil(t, "at 70 columns the list is drawn alone", uiTimeout, func() (bool, string) {
		return listAlone(term.Screen()), term.Snapshot()
	})
	if bar := keyBar(); strings.Contains(bar, "the side column needs") {
		t.Fatalf("ASSERTION: the side note outlived the presses of w at 200 columns: the key bar at 70 columns is %q\n%s", bar, term.Snapshot())
	}
	ckPress(t, term, "Z")
	waitUntil(t, "Z at 70 columns says in the key bar that the zoom needs 80 columns, and offers no unzoom", uiTimeout, func() (bool, string) {
		bar := keyBar()
		return strings.Contains(bar, "zoom needs 80 columns") && !strings.Contains(bar, "unzoom"), term.Snapshot()
	})
	saveFrame(t, term, "layout-narrow-z-70")
	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("resize to 120x40: %v", err)
	}
	waitUntil(t, "at 120 columns after Z at 70 the list and the focus are both drawn", uiTimeout, func() (bool, string) {
		s := term.Screen()
		return listAndFocus(s) && !strings.Contains(ckKeyBar(s), "zoom needs"), term.Snapshot()
	})
	stayTrue(t, "at 120 columns after Z at 70 no zoom is set: no unzoom, the list and the focus both drawn", time.Second, func() (bool, string) {
		s := term.Screen()
		return !strings.Contains(ckKeyBar(s), "unzoom") && listAndFocus(s) && !strings.Contains(ckKeyBar(s), "zoom needs"), term.Snapshot()
	})
	ckPress(t, term, "Z")
	waitUntil(t, "Z at 120 columns zooms and the key bar offers the unzoom", uiTimeout, func() (bool, string) {
		return strings.Contains(keyBar(), "unzoom"), term.Snapshot()
	})
	saveFrame(t, term, "layout-narrow-z-120")
	ckPress(t, term, tuitest.Esc)
	alive(t, term, "after w and Z on a narrow view")
}
