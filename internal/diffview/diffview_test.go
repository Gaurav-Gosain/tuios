package diffview

import (
	"strings"
	"testing"
)

func TestPairs(t *testing.T) {
	C, A, D := Context, Add, Delete
	tests := []struct {
		name  string
		kinds []Kind
		want  []Pair
	}{
		{"context", []Kind{C, C}, []Pair{{0, 0}, {1, 1}}},
		{"replace one", []Kind{C, D, A, C}, []Pair{{0, 0}, {1, 2}, {3, 3}}},
		{"more removed", []Kind{D, D, A}, []Pair{{0, 2}, {1, -1}}},
		{"more added", []Kind{D, A, A}, []Pair{{0, 1}, {-1, 2}}},
		{"added alone", []Kind{C, A}, []Pair{{0, 0}, {-1, 1}}},
		{"removed alone", []Kind{D, C}, []Pair{{0, -1}, {1, 1}}},
		{"two blocks", []Kind{D, A, C, D, A}, []Pair{{0, 1}, {2, 2}, {3, 4}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Pairs(tc.kinds)
			if len(got) != len(tc.want) {
				t.Fatalf("Pairs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Pairs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestChanged(t *testing.T) {
	tests := []struct {
		name, old, new   string
		wantOld, wantNew string
		wantNone         bool
	}{
		{name: "renamed word", old: "x := count + 1", new: "x := total + 1", wantOld: "count", wantNew: "total"},
		{name: "inserted", old: "f(a)", new: "f(a, b)", wantOld: "", wantNew: ", b"},
		{name: "inside a word widens to it", old: "value1 = 2", new: "value2 = 2", wantOld: "value1", wantNew: "value2"},
		{name: "rewritten", old: "time.Sleep(delay)", new: "log.Printf(\"x\")", wantNone: true},
		{name: "rewritten under shared indent", old: "        time.Sleep(delay)", new: "        return nil", wantNone: true},
		{name: "multibyte", old: "s := \"héllo\"", new: "s := \"hallo\"", wantOld: "héllo", wantNew: "hallo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, n := Changed(tc.old, tc.new)
			if tc.wantNone {
				if !o.Empty() || !n.Empty() {
					t.Fatalf("Changed marked %q and %q on lines with little in common", tc.old[o.Start:o.End], tc.new[n.Start:n.End])
				}
				return
			}
			if got := tc.old[o.Start:o.End]; got != tc.wantOld {
				t.Errorf("old part %q, want %q", got, tc.wantOld)
			}
			if got := tc.new[n.Start:n.End]; got != tc.wantNew {
				t.Errorf("new part %q, want %q", got, tc.wantNew)
			}
		})
	}
}

func TestHighlightLeavesUnknownAndHugeTextPlain(t *testing.T) {
	if Highlight("notes.unknownext", []string{"func x"}) != nil {
		t.Error("a file of no known type was highlighted")
	}
	if Highlight("big.go", []string{strings.Repeat("x", maxHighlightLine+1)}) != nil {
		t.Error("a line past the limit was tokenised")
	}
	if Enabled && Highlight("run", []string{"#!/bin/sh", "echo hi"}) == nil {
		t.Error("a script with a #! line was not highlighted")
	}
}
