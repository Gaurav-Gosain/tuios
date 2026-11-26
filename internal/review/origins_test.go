package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestOriginsAskTheJudge: a step's judge decides each line it added: the
// step's, the step's but unsure, or the person's.
//
// Negative control: ignoring the judge credits all three lines to step 1.
func TestOriginsAskTheJudge(t *testing.T) {
	dir := testutil.GitRepo(t)
	ctx := context.Background()
	commit := func(body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, dir, "add", "f.txt")
		testutil.Git(t, dir, "commit", "-q", "-m", "x")
		return testutil.Git(t, dir, "rev-parse", "HEAD^{tree}")
	}
	commit("a\n")
	baseSHA := testutil.Git(t, dir, "rev-parse", "HEAD")
	t1 := commit("a\nMADE\nMAYBE\nYOURS\n")
	d, err := Build(ctx, Options{Dir: dir, Base: Base{Name: "base", SHA: baseSHA}, Tree: t1, Context: 3})
	if err != nil {
		t.Fatal(err)
	}
	judge := func(path, text string) Verdict {
		switch text {
		case "MAYBE":
			return Unsure
		case "YOURS":
			return NotMade
		}
		return Made
	}
	steps := []OriginStep{{From: baseSHA, To: t1, Author: 1, Judge: judge}}
	read := func(ctx context.Context, from, to string, paths []string) (string, error) {
		return StepDiff(ctx, dir, from, to, paths)
	}
	if err := Origins(ctx, d.Files, steps, read, 0); err != nil {
		t.Fatal(err)
	}
	type got struct {
		origin int
		unsure bool
	}
	want := map[string]got{"MADE": {2, false}, "MAYBE": {2, true}, "YOURS": {1, false}}
	for _, l := range d.Files[0].Hunks[0].Lines {
		if w, ok := want[l.Text]; ok && (l.Origin != w.origin || l.Unsure != w.unsure) {
			t.Errorf("line %q: origin %d unsure %v, want %d %v", l.Text, l.Origin, l.Unsure, w.origin, w.unsure)
		}
	}
}

// TestOriginsCreditEachLineToItsStep: a chain of three trees, each step
// adding lines, one moving the lines below it down. Each added line of the
// whole diff is credited to the step that added it, after the later steps
// moved it, and a line rewritten by a later step goes to that step.
//
// Negative control: crediting every added line to the last step fails this.
func TestOriginsCreditEachLineToItsStep(t *testing.T) {
	dir := testutil.GitRepo(t)
	ctx := context.Background()
	commit := func(body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, dir, "add", "f.txt")
		testutil.Git(t, dir, "commit", "-q", "-m", "x")
		return testutil.Git(t, dir, "rev-parse", "HEAD^{tree}")
	}
	commit("a\nb\nc\nd\n")
	baseSHA := testutil.Git(t, dir, "rev-parse", "HEAD")
	t1 := commit("a\nONE\nb\nc\nd\n")            // step 1 adds ONE
	t2 := commit("TOP\na\nONE\nb\nc\nd\nTWO\n")  // step 2 adds TOP above and TWO below
	t3 := commit("TOP\na\nONE\nb\nC3\nd\nTWO\n") // after the last step: c becomes C3

	d, err := Build(ctx, Options{Dir: dir, Base: Base{Name: "base", SHA: baseSHA}, Tree: t3, Context: 3})
	if err != nil {
		t.Fatal(err)
	}
	steps := []OriginStep{{From: baseSHA, To: t1, Author: 1}, {From: t1, To: t2, Author: 2}, {From: t2, To: t3, Author: 0}}
	read := func(ctx context.Context, from, to string, paths []string) (string, error) {
		return StepDiff(ctx, dir, from, to, paths)
	}
	if err := Origins(ctx, d.Files, steps, read, 0); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"ONE": 2, "TOP": 3, "TWO": 3, "C3": 1}
	got := map[string]int{}
	for _, f := range d.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Op == "add" {
					got[strings.TrimSpace(l.Text)] = l.Origin
				}
			}
		}
	}
	for text, origin := range want {
		if got[text] != origin {
			t.Errorf("line %q has origin %d, want %d (all: %v)", text, got[text], origin, got)
		}
	}
}
