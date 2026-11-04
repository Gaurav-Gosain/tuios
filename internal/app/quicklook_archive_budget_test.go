package app

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestQuickLookArchiveRowsStayBounded holds an archive's tree to its budget.
// A path 1990 folders deep made a joined string for each of its folders, and
// 200 such paths, a 4.4 MB zip, made quick look's client allocate about 2 GB.
// The tree shows 64 folders; what is below them shows as one row at depth 64,
// the rest of the path with the file's name. An ordinary path is drawn as it
// was.
func TestQuickLookArchiveRowsStayBounded(t *testing.T) {
	folders := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf("f%d", i))
		}
		return out
	}

	deep := strings.Join(folders(1, 1990), "/")
	entries := make([]session.PreviewArchiveEntry, 200)
	for i := range entries {
		entries[i] = session.PreviewArchiveEntry{Name: fmt.Sprintf("%s/name%03d.txt", deep, i), Size: 1}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _ = quickLookArchiveRows(entries, false)
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 32<<20 {
		t.Errorf("the tree of 200 paths 1990 folders deep allocated %d MB, want under 32 MB", n>>20)
	}

	got := func(entries ...session.PreviewArchiveEntry) []quickLookArchiveRow {
		rows, _ := quickLookArchiveRows(entries, false)
		return rows
	}
	var want []quickLookArchiveRow
	for i, f := range folders(1, 64) {
		want = append(want, quickLookArchiveRow{depth: i, name: f + "/", dir: true})
	}
	want = append(want, quickLookArchiveRow{depth: 64, name: strings.Join(folders(65, 70), "/") + "/name.txt", size: 7})
	path := strings.Join(folders(1, 70), "/") + "/name.txt"
	if rows := got(session.PreviewArchiveEntry{Name: path, Size: 7}); !slices.Equal(rows, want) {
		t.Errorf("a path 70 folders deep gives the rows\n%v\nwant the first 64 folders and the rest at depth 64\n%v", rows, want)
	}

	want = []quickLookArchiveRow{{0, "a/", 0, true}, {1, "b/", 0, true}, {2, "c.txt", 5, false}}
	if rows := got(session.PreviewArchiveEntry{Name: "a/b/c.txt", Size: 5}); !slices.Equal(rows, want) {
		t.Errorf("a/b/c.txt gives the rows %v, want %v", rows, want)
	}
}
