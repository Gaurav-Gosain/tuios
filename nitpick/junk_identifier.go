package nitpick

import (
	"fmt"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// JunkIdentifier reports a name that says nothing about what it holds:
// foo, bar, stuff, data2.
var JunkIdentifier = &analysis.Analyzer{
	Name: "junk_identifier",
	Doc: `report a name that says nothing about its value

The rule reports declarations named foo, baz, stuff, thing, asdf and
the like, and such a word with a number, such as foo2. The name bar is left
alone, because a bar chart or a progress bar needs it, and so are numbered
pairs such as val1 and val2. Test files are skipped, because foo is a normal
test value. The rule
is off by default.`,
	URL: "https://pkg.go.dev/tuios.dev/nitpick#JunkIdentifier",
	Run: runJunkIdentifier,
}

var junkNames = setOf("foo", "baz", "qux", "quux", "corge", "grault", "garply", "waldo",
	"fred", "plugh", "xyzzy", "thud", "foobar", "asdf", "qwerty", "xxx", "yyy", "zzz", "blah",
	"blahblah", "stuff", "thing", "things", "myvar", "myvalue", "mydata", "mystruct",
	"myfunc", "myfunction", "hoge", "fuga", "piyo", "aaa", "bbb", "ccc")

var junkNumbered = regexp.MustCompile(`^(foo|bar|baz|thing|stuff|asdf)\d+$`)

func runJunkIdentifier(pass *analysis.Pass) (any, error) {
	files := handWritten(pass)
	skip := map[string]bool{}
	for _, f := range pass.Files {
		if !files[f] || isTestFile(pass, f) {
			skip[pass.Fset.File(f.FileStart).Name()] = true
		}
	}
	for id, obj := range pass.TypesInfo.Defs {
		if obj == nil || id.Name == "_" || skip[pass.Fset.File(id.Pos()).Name()] {
			continue
		}
		switch obj.(type) {
		case *types.Var, *types.Const, *types.Func, *types.TypeName:
		default:
			continue
		}
		lower := strings.ToLower(id.Name)
		if !junkNames[lower] && !junkNumbered.MatchString(lower) {
			continue
		}
		pass.Report(analysis.Diagnostic{
			Pos:     id.Pos(),
			End:     id.End(),
			Message: fmt.Sprintf("the name %s says nothing about its value: use a name that says what it holds", id.Name),
		})
	}
	return nil, nil
}
