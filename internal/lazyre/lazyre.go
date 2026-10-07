// Package lazyre compiles a regular expression the first time it is used.
//
// Every tuios process links every package, and most of the package-level
// patterns serve one command or one overlay. Compiled at package init they
// cost every one-shot CLI command for patterns it never runs.
package lazyre

import (
	"regexp"
	"sync"
)

// New returns a function that compiles expr on its first call and returns
// the same *regexp.Regexp on every call. Like regexp.MustCompile, it panics
// when expr does not compile, on that first call.
//
// It is not inlined. Inlined, every pattern carried its own copy of the
// closures sync.OnceValue makes, about 1.5 KB each, and 45 patterns put the
// binary over its size budget. Called, a pattern adds a call and its string.
//
//go:noinline
func New(expr string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(expr) })
}
