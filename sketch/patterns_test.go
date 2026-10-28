package sketch

import (
	"regexp"
	"sync"
	"testing"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// lazyPatterns is how many patterns sketch's packages declare. A package that
// drops out of this test binary would take its patterns out of the check, so
// the count is exact. Change it when a pattern is added or removed.
const lazyPatterns = 124

// Patterns compile on first use. A pattern that does not compile would panic
// only on the input that reaches it, so compile them all here.
func TestLazyPatternsCompile(t *testing.T) {
	if n := lazyre.MustCompileAll(); n != lazyPatterns {
		t.Fatalf("%d patterns registered, want %d", n, lazyPatterns)
	}
}

// regexpDetectors are Mermaid's detector regexps as pele writes them. detect
// matches them by hand; FuzzDetect holds the two to the same answer.
var regexpDetectors = sync.OnceValue(func() []struct {
	typ Type
	re  *regexp.Regexp
} {
	const sp = `[` + js.S + `]*`
	return []struct {
		typ Type
		re  *regexp.Regexp
	}{
		{"flowchart", regexp.MustCompile(`^` + sp + `flowchart-elk`)},
		{"mindmap", regexp.MustCompile(`^` + sp + `mindmap`)},
		{"architecture", regexp.MustCompile(`^` + sp + `architecture`)},
		{"agentflow", regexp.MustCompile(`^` + sp + `agentflow-beta\b`)},
		{"c4", regexp.MustCompile(`^` + sp + `(?:C4Context|C4Container|C4Component|C4Dynamic|C4Deployment)`)},
		{"kanban", regexp.MustCompile(`^` + sp + `kanban`)},
		{"class", regexp.MustCompile(`^` + sp + `classDiagram`)},
		{"er", regexp.MustCompile(`^` + sp + `erDiagram`)},
		{"gantt", regexp.MustCompile(`^` + sp + `gantt`)},
		{"info", regexp.MustCompile(`^` + sp + `info`)},
		{"pie", regexp.MustCompile(`^` + sp + `pie`)},
		{"requirement", regexp.MustCompile(`^` + sp + `requirement(Diagram)?`)},
		{"sequence", regexp.MustCompile(`^` + sp + `sequenceDiagram`)},
		{"swimlane", regexp.MustCompile(`^` + sp + `swimlane-beta\b`)},
		{"flowchart", regexp.MustCompile(`^` + sp + `(graph|flowchart)`)},
		{"timeline", regexp.MustCompile(`^` + sp + `timeline`)},
		{"gitGraph", regexp.MustCompile(`^` + sp + `gitGraph`)},
		{"state", regexp.MustCompile(`^` + sp + `stateDiagram`)},
		{"journey", regexp.MustCompile(`^` + sp + `journey`)},
		{"quadrantChart", regexp.MustCompile(`^` + sp + `quadrantChart`)},
		{"sankey", regexp.MustCompile(`^` + sp + `sankey(-beta)?`)},
		{"packet", regexp.MustCompile(`^` + sp + `packet(-beta)?`)},
		{"xychart", regexp.MustCompile(`^` + sp + `xychart(-beta)?`)},
		{"block", regexp.MustCompile(`^` + sp + `block(-beta)?`)},
		{"eventmodeling", regexp.MustCompile(`^` + sp + `eventmodeling`)},
		{"treeView", regexp.MustCompile(`^` + sp + `treeView-beta`)},
		{"radar", regexp.MustCompile(`^` + sp + `radar-beta`)},
		{"ishikawa", regexp.MustCompile(`(?i)^` + sp + `ishikawa(-beta)?\b`)},
		{"treemap", regexp.MustCompile(`^` + sp + `treemap`)},
		{"railroad", regexp.MustCompile(`(?i)^` + sp + `railroad-(?:ebnf-|abnf-|peg-)?beta`)},
		{"venn", regexp.MustCompile(`^` + sp + `venn-beta`)},
		{"wardley", regexp.MustCompile(`(?i)^` + sp + `wardley-beta`)},
		{"cynefin", regexp.MustCompile(`^` + sp + `cynefin-beta(?:[` + js.S + `:]|$)`)},
		{"usecase", regexp.MustCompile(`^` + sp + `usecase-beta(?:[` + js.S + `]|$)`)},
	}
})

func detectByRegexp(cleaned string) (Type, bool) {
	start := len(cleaned)
	for i, r := range cleaned {
		if !js.IsSpace(r) {
			start = i
			break
		}
	}
	head := cleaned[start:]
	if len(head) > detectPrefix {
		head = head[:detectPrefix]
	}
	for _, d := range regexpDetectors() {
		if d.re.MatchString(head) {
			return d.typ, true
		}
	}
	return "", false
}

func FuzzDetect(f *testing.F) {
	for _, s := range []string{"graph TD", "flowchart LR", "  \u00a0sequenceDiagram", "ishikawa", "ISHI\u212aAWA-beta x",
		"i\u017fhikawa_", "railroad-EBNF-beta", "railroad-peg-be", "wardley-BETA", "cynefin-beta:", "cynefin-betax",
		"usecase-beta\u3000", "usecase-beta", "agentflow-beta_", "agentflow-beta-", "swimlane-beta9", "C4Context",
		"x C4Dynamic", "requirementDiagram", "sankey-beta", "\ufeffpie", "stateDiagram-v2", "erDiagram", "info", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		gt, gok := detect(s)
		wt, wok := detectByRegexp(s)
		if gt != wt || gok != wok {
			t.Fatalf("detect(%q) = %q %v, regexps say %q %v", s, gt, gok, wt, wok)
		}
	})
}
