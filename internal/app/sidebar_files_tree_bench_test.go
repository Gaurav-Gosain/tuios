package app

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// bigFileTree is the tree at its limits: a root of 2000 names whose first 64
// are folders, every one of them open and holding 2000 names: 130,001 rows
// with "..".
func bigFileTree() *OS {
	m := &OS{}
	m.Settings.SidebarFilesStyle = config.SidebarFilesTree
	root := "/bench/root"
	names := func(dirs int) []fileEntry {
		out := make([]fileEntry, 0, fileViewMaxEntries)
		for i := range fileViewMaxEntries {
			out = append(out, fileEntry{Name: fmt.Sprintf("name-%04d", i), Dir: i < dirs})
		}
		return out
	}
	m.filesView = fileViewState{Dir: root, Want: root, Entries: names(fileTreeMaxOpen), Gen: 1}
	m.filesTree.dirs = map[string]*fileTreeDir{}
	for _, e := range m.filesView.Entries[:fileTreeMaxOpen] {
		m.filesTree.dirs[filepath.Join(root, e.Name)] = &fileTreeDir{Entries: names(0)}
	}
	m.filesTree.gen = 1
	return m
}

// BenchmarkFileTreeRows is the perf budget of the files section's rows at the
// tree's limits. rebuild is a frame after the tree changed, and what every
// render cache miss and every j, k, g and G paid before the rows were kept.
// kept is every other frame and key.
func BenchmarkFileTreeRows(b *testing.B) {
	m := bigFileTree()
	if n := len(m.fileRowsBuilt()); n != 1+fileViewMaxEntries*(fileTreeMaxOpen+1) { // and ".."
		b.Fatalf("built %d rows", n)
	}
	b.Run("rebuild", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.filesTree.gen++
			_ = m.fileRowsBuilt()
		}
	})
	b.Run("kept", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = m.fileRowsBuilt()
		}
	})
}

// TestFileTreeRowsKeptCostNothing holds the budget the benchmark measures: a
// frame or a key over a tree that has not changed builds no row and
// allocates nothing.
func TestFileTreeRowsKeptCostNothing(t *testing.T) {
	m := bigFileTree()
	_ = m.fileRowsBuilt()
	if allocs := testing.AllocsPerRun(20, func() { _ = m.fileRowsBuilt() }); allocs != 0 {
		t.Fatalf("reading the kept rows allocated %v times, want 0", allocs)
	}
}
