// Ported from pele tests/render/timeline.test.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13:
// the case v1.8.0 added. The rest of that file is covered by the goldens.

package sketch

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestTimelineFirstEventStraight(t *testing.T) {
	source := "timeline TD\n  2002 : LinkedIn\n  2004 : Facebook : Google\n  2005<br>and on<br>and on : YouTube\n  2006 : first<br>second<br>third<br>fourth\n"
	svg := renderSVG(t, source, RenderOptions{})
	type box struct{ top, bottom, middle float64 }
	reBox := regexp.MustCompile(` y="([\d.]+)" width="[\d.]+" height="([\d.]+)"`)
	toBox := func(rect string) box {
		m := reBox.FindStringSubmatch(rect)
		var y, h float64
		if m != nil {
			y, _ = strconv.ParseFloat(m[1], 64)
			h, _ = strconv.ParseFloat(m[2], 64)
		}
		return box{y, y + h, y + h/2}
	}
	rows := strings.Split(svg, `<g class="pele-node pele-period"`)[1:]
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	reRect := regexp.MustCompile(`<rect [^>]+>`)
	rePath := regexp.MustCompile(`class="pele-edge" d="([^"]+)"`)
	reStub := regexp.MustCompile(`M[\d.]+,([\d.]+)H`)
	above := 0.0
	for i, row := range rows {
		var boxes []box
		for _, r := range reRect.FindAllString(row, -1) {
			boxes = append(boxes, toBox(r))
		}
		period, event := boxes[0], boxes[1]
		if event.middle != period.middle {
			t.Errorf("row %d: event middle %v, period middle %v", i, event.middle, period.middle)
		}
		path := ""
		if m := rePath.FindStringSubmatch(row); m != nil {
			path = m[1]
		}
		var stubs []float64
		for _, m := range reStub.FindAllStringSubmatch(path, 2) {
			v, _ := strconv.ParseFloat(m[1], 64)
			stubs = append(stubs, v)
		}
		if !slices.Equal(stubs, []float64{period.middle, period.middle}) {
			t.Errorf("row %d: stubs %v, period middle %v", i, stubs, period.middle)
		}
		top, bottom := math.Inf(1), math.Inf(-1)
		for _, b := range boxes {
			top = math.Min(top, b.top)
			bottom = math.Max(bottom, b.bottom)
		}
		if top <= above {
			t.Errorf("row %d: top %v not below %v", i, top, above)
		}
		above = bottom
	}
}
