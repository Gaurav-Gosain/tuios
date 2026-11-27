package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// numberedBody is n lines "line i", with the lines in at put in their place.
func numberedBody(n int, at map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if s, ok := at[i]; ok {
			b.WriteString(s)
		} else {
			fmt.Fprintf(&b, "line %02d", i)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestRevertTakesOutOneHunkAndPutsItBack: of a file with two hunks against
// HEAD, a new untracked file and a deleted file, each hunk reverted alone
// leaves the rest as it is, and the same patch applied reversed puts it back,
// byte for byte. The index is not written.
func TestRevertTakesOutOneHunkAndPutsItBack(t *testing.T) {
	repo := testutil.GitRepo(t)
	write(t, repo, "a.go", numberedBody(40, nil))
	write(t, repo, "gone.go", "one\ntwo\n")
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "base")

	edited := numberedBody(40, map[int]string{5: "FIRST", 30: "SECOND"})
	write(t, repo, "a.go", edited)
	write(t, repo, "new.go", "fresh\n")
	if err := os.Remove(filepath.Join(repo, "gone.go")); err != nil {
		t.Fatal(err)
	}
	index := indexHash(t, repo)

	read := func() *Diff {
		t.Helper()
		base, err := ResolveBase(ctx(t), repo, "", "", true)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Build(ctx(t), Options{Dir: repo, Base: base, Context: 3})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	body := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(repo, name))
		return string(b), err == nil
	}
	revert := func(path string, hunk int, reverse bool, patch string) string {
		t.Helper()
		if patch == "" {
			f := fileOf(t, read(), path)
			var err error
			if patch, err = RevertPatch(f, &f.Hunks[hunk]); err != nil {
				t.Fatal(err)
			}
		}
		if err := ApplyToWork(ctx(t), repo, patch, reverse); err != nil {
			t.Fatalf("%s hunk %d (reverse %v): %v\n%s", path, hunk, reverse, err, patch)
		}
		return patch
	}

	if f := fileOf(t, read(), "a.go"); len(f.Hunks) != 2 {
		t.Fatalf("a.go has %d hunks, want 2", len(f.Hunks))
	}
	p := revert("a.go", 1, false, "")
	if got, _ := body("a.go"); got != numberedBody(40, map[int]string{5: "FIRST"}) {
		t.Errorf("after the revert of the second hunk, a.go =\n%s", got)
	}
	revert("a.go", 1, true, p)
	if got, _ := body("a.go"); got != edited {
		t.Errorf("after the undo, a.go =\n%s", got)
	}

	p = revert("new.go", 0, false, "")
	if _, ok := body("new.go"); ok {
		t.Error("the revert of the only hunk of a new file left the file")
	}
	revert("new.go", 0, true, p)
	if got, _ := body("new.go"); got != "fresh\n" {
		t.Errorf("after the undo, new.go = %q", got)
	}

	p = revert("gone.go", 0, false, "")
	if got, _ := body("gone.go"); got != "one\ntwo\n" {
		t.Errorf("the revert of a deletion made gone.go = %q", got)
	}
	revert("gone.go", 0, true, p)
	if _, ok := body("gone.go"); ok {
		t.Error("the undo of the revert of a deletion left gone.go")
	}

	if indexHash(t, repo) != index {
		t.Error("a revert or an undo wrote the index")
	}

	// A patch whose file changed under it is refused, and writes nothing.
	f := fileOf(t, read(), "a.go")
	p, err := RevertPatch(f, &f.Hunks[0])
	if err != nil {
		t.Fatal(err)
	}
	moved := numberedBody(40, map[int]string{4: "IN-BETWEEN", 5: "FIRST", 30: "SECOND"})
	write(t, repo, "a.go", moved)
	if err := ApplyToWork(ctx(t), repo, p, false); err == nil {
		t.Error("a revert applied over an edit made in between")
	}
	if got, _ := body("a.go"); got != moved {
		t.Error("a refused revert wrote the file")
	}

	// The digest tells two hunks apart that share a header.
	g := fileOf(t, read(), "a.go")
	if g.Hunks[0].Header == f.Hunks[0].Header && g.Hunks[0].Sum() == f.Hunks[0].Sum() {
		t.Error("a hunk with a new line has the same digest")
	}
}
