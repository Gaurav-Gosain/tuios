// Not part of pele. pele trusts the markup of a math resolver and writes it as
// it is (tests/render/math.test.ts checks only that the output is well formed).
// These tests check what pele does write around it: hostile $$...$$ labels with
// no resolver and with one that typesets (but not "none"), in every
// type that takes math.

package sketch

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fuzzMathMarkup is the only markup fuzzMath gives. It is inert.
const fuzzMathMarkup = `<path d="M0,0H9"/>`

// fuzzMath typesets every expression but those that hold "none", in a box that
// grows with the TeX. It is the resolver of the math security tests and of
// FuzzRender.
func fuzzMath(tex string, size float64) (MathMarkup, bool) {
	if strings.Contains(tex, "none") {
		return MathMarkup{}, false
	}
	return MathMarkup{Markup: fuzzMathMarkup, Width: float64(len(tex)%97) + size/2, Height: size + float64(len(tex)%13)}, true
}

var reMathElement = regexp.MustCompile(`<svg class="pele-math" x="([^"]*)" y="([^"]*)" width="([^"]*)" height="([^"]*)" overflow="visible"(?: color="([^"]*)")?(?: opacity="([^"]*)")?>` + regexp.QuoteMeta(fuzzMathMarkup) + `</svg>`)

// mathInertProblem is inertProblem for a drawing made with fuzzMath: each
// expression is checked and taken out, and the rest must be inert. The colour and
// opacity are copied from the label's own attributes.
func mathInertProblem(svg string) string {
	if err := assertWellFormed(svg); err != nil {
		return err.Error()
	}
	problem := ""
	rest := reMathElement.ReplaceAllStringFunc(svg, func(el string) string {
		m := reMathElement.FindStringSubmatch(el)
		for _, n := range m[1:5] {
			if !reNumber.MatchString(n) {
				problem = "math box " + strconv.Quote(n) + " is not a number"
			}
		}
		for _, v := range m[5:7] {
			if reUnsafePaint.MatchString(v) || strings.ContainsAny(v, "<>&") {
				problem = "math colour or opacity " + strconv.Quote(v)
			}
		}
		return "<g/>"
	})
	if problem != "" {
		return problem
	}
	return inertProblem(rest)
}

// mathTry renders src and fails the test on output that is not inert. It reports
// whether the source rendered.
func mathTry(t *testing.T, src string, opt RenderOptions) bool {
	t.Helper()
	out, err := Render(src, opt)
	if err != nil {
		var pe *Error
		if !errors.As(err, &pe) || strings.HasPrefix(pe.Message, "sketch: internal error") {
			t.Fatalf("%q: %v", trimSrc(src), err)
		}
		return false
	}
	check := inertProblem
	if opt.Math != nil {
		check = mathInertProblem
	}
	if problem := check(out.Data); problem != "" {
		t.Fatalf("%q: output is not inert: %s", trimSrc(src), problem)
	}
	return true
}

// The types that take $$...$$, with their hostile templates and corpus names.
// A resolver that has nothing draws as no resolver does (TestMathInLabels), so
// the options leave it out.
var mathTypes = []struct {
	name      string
	templates []func(string) string
}{
	{"flowchart", flowchartTemplates}, {"sequence", sequenceTemplates}, {"swimlane", swimlaneTemplates},
	{"block", blockTemplates}, {"class", classTemplates}, {"er", erTemplates}, {"kanban", kanbanTemplates},
	{"mindmap", mindmapTemplates}, {"requirement", requirementTemplates}, {"state", stateTemplates},
	{"usecase", usecaseTemplates},
}

var mathOptions = map[string]RenderOptions{
	"no resolver":   {},
	"typeset":       {Math: fuzzMath},
	"typeset small": {Math: fuzzMath, MaxWidth: &[]float64{240}[0]},
}

func TestMathOutputIsInert(t *testing.T) {
	wraps := []func(string) string{
		func(p string) string { return "$$" + p + "$$" },
		func(p string) string { return "a $$" + p + "$$ <b>b $$none" + p + "$$</b>" },
	}
	// Every fourth payload: the templates times the options make this the
	// slowest of the inert tests otherwise.
	var payloads []string
	for i := 0; i < len(PAYLOADS); i += 4 {
		payloads = append(payloads, PAYLOADS[i])
	}
	if testing.Short() {
		payloads = payloads[:3]
	}
	for _, typ := range mathTypes {
		t.Run(typ.name, func(t *testing.T) {
			for name, opt := range mathOptions {
				rendered := 0
				for _, template := range typ.templates {
					for _, payload := range payloads {
						for _, wrap := range wraps {
							if mathTry(t, template(wrap(payload)), opt) {
								rendered++
							}
						}
					}
				}
				if rendered == 0 {
					t.Errorf("%s: no hostile source rendered", name)
				}
			}
		})
	}
}

func TestMathCorpusMutations(t *testing.T) {
	fragments := append(append([]string{}, PAYLOADS...), "$$", "$$x^2$$", "$$none$$", "$$<b>a</b>$$", "$$a\n$$", "$$#35;$$",
		"$$&lt;$$", "$$$", "<br/>$$a$$", "**$$a$$**", "$$\\frac{a}{b}$$ text", "", "$$$$")
	count := 400
	if testing.Short() {
		count /= 8
	}
	for i, typ := range mathTypes {
		t.Run(typ.name, func(t *testing.T) {
			next := mutator(corpusOf(t, typ.name), fragments, lcg(uint32(0x6d617468+i)))
			rendered := 0
			for range count {
				src := next()
				if len(utf16Of(src)) > 4000 {
					continue
				}
				for _, opt := range []RenderOptions{mathOptions["typeset"], mathOptions["typeset small"]} {
					if mathTry(t, src, opt) {
						rendered++
					}
				}
			}
			t.Logf("%s: %d of %d mutated sources rendered", typ.name, rendered, 2*count)
		})
	}
}
