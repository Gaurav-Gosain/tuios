package main

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
	"tuios.dev/nitpick"
)

type finding struct {
	File      string  `json:"file"`
	Line      int     `json:"line"`
	Column    int     `json:"column"`
	Rule      string  `json:"rule"`
	Message   string  `json:"message"`
	Precision float64 `json:"precision"`

	path string // the absolute path, for the diff scope
}

type result struct {
	Findings   []finding `json:"findings"`
	Errors     []string  `json:"errors,omitempty"`
	Gate       *float64  `json:"gate,omitempty"`
	GateFailed bool      `json:"gate_failed"`
}

// analyze loads the packages and runs the rules on them.
func analyze(dir string, patterns []string, rules []nitpick.Rule, tests bool) result {
	var res result
	cfg := &packages.Config{
		Mode:  packages.LoadSyntax | packages.NeedModule,
		Dir:   dir,
		Tests: tests,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	var roots []*packages.Package
	for _, p := range pkgs {
		// The test main that go list makes has no code of the module.
		if strings.HasSuffix(p.ID, ".test") {
			continue
		}
		if len(p.Errors) > 0 {
			for _, e := range p.Errors {
				res.Errors = append(res.Errors, e.Error())
			}
			continue
		}
		roots = append(roots, p)
	}
	precision := map[string]float64{}
	var analyzers []*analysis.Analyzer
	for _, r := range rules {
		analyzers = append(analyzers, r.Analyzer)
		precision[r.Name()] = r.Precision
	}
	graph, err := checker.Analyze(analyzers, roots, nil)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	// A package and its test variant hold the same files: keep one copy.
	seen := map[string]bool{}
	for _, act := range graph.Roots {
		if act.Err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %s: %v", act.Package.PkgPath, act.Analyzer.Name, act.Err))
			continue
		}
		for _, d := range act.Diagnostics {
			pos := act.Package.Fset.Position(d.Pos)
			key := fmt.Sprintf("%s:%d:%d:%s:%s", pos.Filename, pos.Line, pos.Column, act.Analyzer.Name, d.Message)
			if seen[key] {
				continue
			}
			seen[key] = true
			res.Findings = append(res.Findings, finding{
				Line:      pos.Line,
				Column:    pos.Column,
				Rule:      act.Analyzer.Name,
				Message:   d.Message,
				Precision: precision[act.Analyzer.Name],
				path:      normPath(pos.Filename),
			})
		}
	}
	slices.SortFunc(res.Findings, func(a, b finding) int {
		return cmp.Or(strings.Compare(a.path, b.path), cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Column, b.Column), strings.Compare(a.Rule, b.Rule))
	})
	slices.Sort(res.Errors)
	res.Errors = slices.Compact(res.Errors)
	return res
}

// normPath gives one spelling of a path, so that the paths of git and of
// go list compare equal.
func normPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// relPath shows a path from the working directory when it is below it.
func relPath(cwd, p string) string {
	if rel, err := filepath.Rel(normPath(cwd), p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}
