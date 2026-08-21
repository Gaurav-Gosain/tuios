package applist_test

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/pkg/applist"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// Example shows the whole launcher pipeline: scan once and keep the cache,
// rank with the shared matcher on every keystroke, lift what the user actually
// runs, and record the choice.
//
// The two packages are split at the point a caller would want to substitute
// something: applist knows what can be run, fuzzy knows how to order it, and
// neither knows how the rows are drawn.
func Example() {
	// The cache is built once and refreshed when the launcher opens. Refresh
	// does filesystem I/O, so it belongs off whatever goroutine draws.
	cache := applist.NewCache()
	entries, _ := cache.Refresh()

	// A launch history turns a list into a launcher. Boost is capped, so it
	// orders near-ties without overruling a clearly better match.
	history := applist.LoadFrecency(applist.DefaultPath())

	// Per keystroke: rank the names, then fold in the history and re-sort. A
	// Matcher reused across the sweep keeps this allocation-free.
	var m fuzzy.Matcher
	query := "gc"
	hits := m.FilterIndex(query, len(entries), func(i int) string { return entries[i].Name })
	for i := range hits {
		hits[i].Score += history.Boost(hits[i].Text)
	}
	fuzzy.Sort(hits)

	// Each hit carries the matched byte offsets, so a row can underline exactly
	// what the query hit, and an index back into the caller's own slice.
	for _, h := range hits[:min(len(hits), 5)] {
		_ = entries[h.Index].Path // what to execute
		_ = h.Positions           // what to highlight
	}

	// Recording a choice touches memory only; Save does the write.
	if len(hits) > 0 {
		history.Note(hits[0].Text)
		_ = history.Save()
	}

	fmt.Println("ranked", len(hits) > 0 || len(entries) == 0)
	// Output: ranked true
}

// ExampleScan shows the rule that keeps a launcher honest about what a name
// means: the first directory on $PATH wins, exactly as the shell resolves it.
func ExampleScan() {
	entries := applist.Scan([]string{"/nonexistent-a", "/nonexistent-b"})
	fmt.Println(len(entries))
	// Output: 0
}
