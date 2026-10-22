// Ported from pele tests/render/sequence.test.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13:
// the case v1.8.0 added. The rest of that file is covered by the goldens.

package sketch

import (
	"regexp"
	"strconv"
	"testing"
)

func TestSequenceSelfMessageRow(t *testing.T) {
	baselines := func(svg, id string) []float64 {
		t.Helper()
		label := regexp.MustCompile(`<g class="pele-edge-label" data-id="` + id + `">(.*?)</g>`).FindStringSubmatch(svg)
		if label == nil {
			t.Fatalf("no label %s in %s", id, svg)
		}
		var out []float64
		for _, m := range regexp.MustCompile(` y="([\d.]+)"`).FindAllStringSubmatch(label[1], -1) {
			v, _ := strconv.ParseFloat(m[1], 64)
			out = append(out, v)
		}
		return out
	}
	step := func(svg string) float64 {
		self := baselines(svg, "i0")
		return baselines(svg, "i1")[0] - self[len(self)-1]
	}
	broken := renderSVG(t, "sequenceDiagram\n  A->>A: one<br/>two<br/>three\n  A->>B: next", RenderOptions{})
	own := baselines(broken, "i0")
	if len(own) != 3 {
		t.Fatalf("baselines %v", own)
	}
	if step(broken) < own[1]-own[0] {
		t.Errorf("broken: step %v < line %v", step(broken), own[1]-own[0])
	}
	long := "a message to self long enough to wrap onto several lines"
	width := 200.0
	wrapped := renderSVG(t, "sequenceDiagram\n  A->>A: "+long+"\n  A->>B: next", RenderOptions{MaxWidth: &width})
	if len(baselines(wrapped, "i0")) <= 1 {
		t.Errorf("the label did not wrap")
	}
	if step(wrapped) < own[1]-own[0] {
		t.Errorf("wrapped: step %v < line %v", step(wrapped), own[1]-own[0])
	}
}
