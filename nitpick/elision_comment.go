package nitpick

import (
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// ElisionComment reports a comment that stands in for code that is not
// there: "rest of implementation", "existing code unchanged", "TODO:
// implement".
var ElisionComment = &analysis.Analyzer{
	Name: "elision_comment",
	Doc: `report a comment that stands in for missing code

Such comments come from a generated edit that left out part of a body. A
line of the comment must be the marker and nothing more:

	// ... rest of implementation
	// existing code unchanged
	// TODO: implement

The rule is off by default.`,
	URL: "https://pkg.go.dev/tuios.dev/nitpick#ElisionComment",
	Run: runElisionComment,
}

var elision = regexp.MustCompile(`(?i)(^\W*(\.\.\.\s*)?(the )?rest of (the )?(implementation|code|function|method|logic|body)( (here|unchanged|omitted|goes here|as before|stays the same))?\W*$` +
	`|^\W*(\.\.\.\s*)?(existing|remaining|other|previous|original|same) (code|logic|implementation|methods?|functions?|cases|fields|handlers?)( (stays?|remains?|is|are))? (here|unchanged|the same|as before|omitted|goes here)\W*$` +
	`|^\W*(todo|fixme)\W*implement( (me|this|it|later))?\W*$` +
	`|^\W*(\.\.\.\s*)?(implementation|code|logic) goes here\W*$` +
	`|^\W*(\.\.\.\s*)?([\w-]+ ){0,4}omitted for brevity\W*$` +
	`|^\W*\.\.\.\s*(rest|existing|remaining|more|other|same)\b(\W+\w+){0,5}\W*$)`)

func isElision(line string) bool {
	return !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && elision.MatchString(line)
}

func runElisionComment(pass *analysis.Pass) (any, error) {
	files := handWritten(pass)
	for _, f := range pass.Files {
		if !files[f] {
			continue
		}
		for _, g := range f.Comments {
			if !isReason(g) || !slices.ContainsFunc(strings.Split(g.Text(), "\n"), isElision) {
				continue
			}
			pass.Report(analysis.Diagnostic{
				Pos:     g.Pos(),
				End:     g.End(),
				Message: "the comment stands in for code that is not there: write the code, or remove the comment",
			})
		}
	}
	return nil, nil
}
