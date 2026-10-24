// Ported from pele tests/render/sequence.test.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13:
// the case v1.8.0 added. The rest of that file is covered by the goldens.

package sketch

import (
	"regexp"
	"strconv"
	"strings"
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

// Issue 1032: at a narrow width a message label breaks at a hyphen or a space,
// not in the middle of a word. The source is the one the issue gives.
func TestSequenceNarrowLabelBreaksBetweenWords(t *testing.T) {
	src := `sequenceDiagram
    participant A as agent A (one-shot CLI)
    participant D as daemon
    participant B as agent B (one-shot CLI)
    A->>D: send-agent-message -w B
    D-->>A: message 2 queued for B
    B->>D: read-agent-messages -w B
`
	width := 358.0
	svg := renderSVG(t, src, RenderOptions{MaxWidth: &width})
	reLabel := regexp.MustCompile(`<g class="pele-edge-label" data-id="(i\d+)">(.*?)</g>`)
	reText := regexp.MustCompile(`>([^<]+)</tspan>|>([^<]+)</text>`)
	messages := map[string]string{"i0": "send-agent-message -w B", "i1": "message 2 queued for B", "i2": "read-agent-messages -w B"}
	seen := 0
	for _, m := range reLabel.FindAllStringSubmatch(svg, -1) {
		raw, ok := messages[m[1]]
		if !ok {
			continue
		}
		seen++
		var lines []string
		for _, tm := range reText.FindAllStringSubmatch(m[2], -1) {
			lines = append(lines, tm[1]+tm[2])
		}
		if len(lines) < 2 {
			t.Fatalf("%s: %q did not wrap at width %v: %q", m[1], raw, width, lines)
		}
		// Each line is the next piece of the message, and a break falls after
		// a hyphen or at a space.
		rest := raw
		for i, line := range lines {
			if !strings.HasPrefix(rest, line) {
				t.Fatalf("%s: line %d %q is not the next piece of %q (lines %q)", m[1], i, line, raw, lines)
			}
			rest = rest[len(line):]
			if i == len(lines)-1 {
				break
			}
			switch {
			case strings.HasSuffix(line, "-"):
			case strings.HasPrefix(rest, " "):
				rest = strings.TrimLeft(rest, " ")
			default:
				t.Errorf("%s: line %d %q breaks inside a word of %q (lines %q)", m[1], i, line, raw, lines)
			}
		}
		if rest != "" {
			t.Errorf("%s: %q left over from %q (lines %q)", m[1], rest, raw, lines)
		}
	}
	if seen != len(messages) {
		t.Fatalf("found %d of %d message labels", seen, len(messages))
	}
}
