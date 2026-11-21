package nitpick

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NolintWithoutReason reports a //nolint directive with no reason after it.
var NolintWithoutReason = &analysis.Analyzer{
	Name: "nolint_without_reason",
	Doc: `report a //nolint directive with no reason

	x, _ := f() //nolint:errcheck
	x, _ := f() //nolint:errcheck // the cache is best effort

The second line gives the reason. A comment on the line above also gives
the reason. The rule is off by default.`,
	URL: "https://pkg.go.dev/tuios.dev/nitpick#NolintWithoutReason",
	Run: runNolintWithoutReason,
}

func reasonAbove(fset *token.FileSet, f *ast.File, c *ast.Comment) bool {
	line := fset.Position(c.Pos()).Line
	for _, g := range f.Comments {
		if !isReason(g) {
			continue
		}
		for i, o := range g.List {
			if o == c || strings.HasPrefix(o.Text, "//nolint") {
				continue
			}
			end := fset.Position(o.End()).Line
			if end == line-1 || (end < line && i+1 < len(g.List) && g.List[i+1] == c) {
				return true
			}
		}
	}
	return false
}

func runNolintWithoutReason(pass *analysis.Pass) (any, error) {
	files := handWritten(pass)
	for _, f := range pass.Files {
		if !files[f] {
			continue
		}
		for _, g := range f.Comments {
			for _, c := range g.List {
				rest, ok := strings.CutPrefix(c.Text, "//nolint")
				if !ok || (rest != "" && rest[0] != ':' && rest[0] != ' ' && rest[0] != '\t') {
					continue
				}
				if strings.HasPrefix(rest, ":") {
					if i := strings.IndexAny(rest, " \t"); i < 0 {
						rest = ""
					} else {
						rest = rest[i:]
					}
				}
				rest = strings.TrimSpace(rest)
				if reason, ok := strings.CutPrefix(rest, "//"); ok && strings.TrimSpace(reason) != "" {
					continue
				}
				if reasonAbove(pass.Fset, f, c) {
					continue
				}
				pass.Report(analysis.Diagnostic{
					Pos:     c.Pos(),
					End:     c.End(),
					Message: "the nolint directive has no reason: add // and the reason after it",
				})
			}
		}
	}
	return nil, nil
}
