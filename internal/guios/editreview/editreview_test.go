package editreview

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/charmbracelet/x/ansi"
)

// These are security-boundary tests: an allow of an edit is let through only
// when the person was shown every line of its body, and each way a card
// could show less than the body (a clipped line, a tab, trailing spaces, a
// hidden replace_all, a hunk scrolled past) is a way to allow an edit nobody
// read.

const width = 40

func TestEveryLineFitsTheWidth(t *testing.T) {
	e := integration.EditBody{Tool: "Write", Path: "/w/a.go", Hunks: []integration.EditHunk{
		{New: strings.Repeat("x", 32<<10-10) + "\n\tindented\ntrailing   \n中文中文中文中文中文中文中文中文中文中文中文"},
	}}
	lines := Lines(e, width)
	if len(lines) < (32<<10)/width {
		t.Fatalf("a 32 KiB line became %d lines, want it wrapped, not clipped", len(lines))
	}
	var text strings.Builder
	for i, l := range lines {
		if w := ansi.StringWidth(l.Text); w > width {
			t.Fatalf("line %d is %d cells wide, over %d: %q", i, w, width, l.Text)
		}
		if strings.ContainsAny(l.Text, "\t\r\n") {
			t.Fatalf("line %d holds a tab or a line break: %q", i, l.Text)
		}
		text.WriteString(l.Text)
		text.WriteByte('\n')
	}
	all := text.String()
	if strings.Count(all, "x") < 32<<10-10 {
		t.Error("some of the long line is not shown")
	}
	if !strings.Contains(all, "    indented") {
		t.Error("a tab is not shown as spaces")
	}
	if !strings.Contains(all, "trailing"+strings.Repeat(TrailingSpace, 3)) {
		t.Errorf("trailing spaces are not shown:\n%s", all)
	}
}

func TestEveryHunkSaysWhatItDoes(t *testing.T) {
	e := integration.EditBody{Tool: "MultiEdit", Path: "/w/a.go", Hunks: []integration.EditHunk{
		{Old: "a", New: "b"},
		{Old: "c", New: "d", ReplaceAll: true},
	}}
	lines := Lines(e, width)
	// A head may wrap to more than one line; join each hunk's.
	var heads []string
	for _, l := range lines {
		if l.Kind != KindHead {
			continue
		}
		if l.Hunk < len(heads) {
			heads[l.Hunk] += " " + l.Text
			continue
		}
		heads = append(heads, l.Text)
	}
	if len(heads) != 2 || strings.Contains(heads[0], "every") || !strings.Contains(heads[1], "every") {
		t.Fatalf("hunk heads %q, want two with only the second saying it replaces every match", heads)
	}
	w := Lines(integration.EditBody{Tool: "Write", Path: "/w/a.go", Hunks: []integration.EditHunk{{New: "x"}}}, width)
	if w[0].Kind != KindHead || !strings.Contains(w[0].Text, "whole file") {
		t.Fatalf("a Write starts with %+v, want a head that says it writes the whole file", w[0])
	}
	empty := Lines(integration.EditBody{Tool: "Edit", Path: "/w/a.go", Hunks: []integration.EditHunk{{Old: "", New: "x"}}}, width)
	if !hasKind(empty, KindOld) {
		t.Fatal("an empty old string draws no old line, so the person cannot see it is empty")
	}
	if Lines(e, MinWidth-1) != nil {
		t.Error("a width under MinWidth gives lines")
	}
}

func hasKind(lines []Line, k Kind) bool {
	for _, l := range lines {
		if l.Kind == k {
			return true
		}
	}
	return false
}

func TestCompleteOnlyOnceEveryLineWasShown(t *testing.T) {
	e := integration.EditBody{Tool: "Edit", Path: "/w/a.go", Hunks: []integration.EditHunk{{Old: strings.Repeat("o\n", 30), New: strings.Repeat("n\n", 30)}}}
	lines := Lines(e, width)
	r := New(len(lines), width)
	if r.Complete() {
		t.Fatal("complete before anything was shown")
	}
	r.Shown(width, 0, 10)
	r.Shown(width, 20, len(lines))
	if r.Complete() {
		t.Fatal("ASSERTION: complete with lines 10 to 20 never shown")
	}
	r.Shown(width+1, 10, 20)
	if r.Complete() {
		t.Fatal("ASSERTION: lines shown at another width count")
	}
	r.Shown(width, -5, 0)
	r.Shown(width, len(lines), len(lines)+9)
	r.Shown(width, 15, 12)
	if r.Complete() {
		t.Fatal("ASSERTION: out of range or empty spans count")
	}
	r.Shown(width, 9, 21)
	if !r.Complete() {
		t.Fatal("not complete after every line was shown")
	}
	if New(0, width).Complete() {
		t.Error("a review of no lines is complete")
	}
}
