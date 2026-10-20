// Package lazyre holds regular expressions that compile on first use.
//
// sketch declares about a hundred patterns at package level. Compiling all of
// them at init cost about a millisecond on every start, for a render that uses
// a handful. A Regexp here compiles the first time a method needs it, once.
package lazyre

import (
	"regexp"
	"sync"
)

// Regexp is a regexp.Regexp compiled on first use. Its methods are those of
// regexp.Regexp that sketch calls.
type Regexp struct {
	pattern string
	once    sync.Once
	re      *regexp.Regexp
}

// all lists every pattern made by New, for MustCompileAll.
var all []*Regexp

// New returns a Regexp for pattern. The pattern compiles on first use and
// panics there if it is not valid, as regexp.MustCompile does.
//
// New is called from about a hundred package-level declarations. Kept out of
// line, each call is a few bytes of init code instead of the whole body.
//
//go:noinline
func New(pattern string) *Regexp {
	r := &Regexp{pattern: pattern}
	all = append(all, r)
	return r
}

// Re returns the compiled expression.
func (r *Regexp) Re() *regexp.Regexp {
	r.once.Do(func() { r.re = regexp.MustCompile(r.pattern) })
	return r.re
}

// MustCompileAll compiles every pattern made so far, and panics on the first
// that is not valid. Tests call it: a bad pattern would otherwise surface only
// on the input that reaches it.
func MustCompileAll() int {
	for _, r := range all {
		r.Re()
	}
	return len(all)
}

func (r *Regexp) String() string { return r.pattern }

func (r *Regexp) MatchString(s string) bool { return r.Re().MatchString(s) }

func (r *Regexp) Match(b []byte) bool { return r.Re().Match(b) }

func (r *Regexp) FindString(s string) string { return r.Re().FindString(s) }

func (r *Regexp) FindStringIndex(s string) []int { return r.Re().FindStringIndex(s) }

func (r *Regexp) FindStringSubmatch(s string) []string { return r.Re().FindStringSubmatch(s) }

func (r *Regexp) FindStringSubmatchIndex(s string) []int { return r.Re().FindStringSubmatchIndex(s) }

func (r *Regexp) FindAllString(s string, n int) []string { return r.Re().FindAllString(s, n) }

func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return r.Re().FindAllStringSubmatchIndex(s, n)
}

func (r *Regexp) ReplaceAllString(src, repl string) string {
	return r.Re().ReplaceAllString(src, repl)
}

func (r *Regexp) ReplaceAllLiteralString(src, repl string) string {
	return r.Re().ReplaceAllLiteralString(src, repl)
}

func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	return r.Re().ReplaceAllStringFunc(src, repl)
}

func (r *Regexp) Split(s string, n int) []string { return r.Re().Split(s, n) }
