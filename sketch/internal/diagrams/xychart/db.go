// Ported from pele src/diagrams/xychart/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package xychart

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type XyText struct {
	text string
	// "text" or "markdown".
	typ string
}

type DataPoint struct {
	value float64
	label string
}

// Axis is pele's BandAxis or LinearAxis, told apart by typ.
type Axis struct {
	typ        string // "band" or "linear"
	title      string
	categories []string
	min, max   float64
}

// Plot is a series. Its data are the values in their places on the x axis; a category
// with no value has NaN, which stands for pele's `undefined`. pele also keeps each
// place's name as text, which nothing reads; the port leaves it out.
type Plot struct {
	typ   string // "line" or "bar"
	title string
	data  []float64
	// length is the series length pele works with: on a band axis, the count of
	// categories when the series was added. data stops at the last value given,
	// and at reads the rest as NaN, so a source cannot ask for one value per
	// category for each of thousands of series.
	length      int
	pointLabels []string
}

// at is value i of the series, NaN past the values given.
func (p *Plot) at(i int) float64 {
	if i < len(p.data) {
		return p.data[i]
	}
	return math.NaN()
}

type XyChartDb struct {
	title       string
	accTitle    string
	accDescr    string
	orientation string // "vertical", "horizontal" or "" for unset
	xAxis       Axis
	yAxis       Axis
	hasSetXAxis bool
	// False while the y range comes from the data and not from a `y-axis` statement.
	hasSetYAxis bool
	plots       []Plot
}

func newXyChartDb() *XyChartDb {
	return &XyChartDb{
		xAxis: Axis{typ: "band"},
		yAxis: Axis{typ: "linear", min: math.Inf(1), max: math.Inf(-1)},
	}
}

func (db *XyChartDb) setOrientation(orientation string) {
	if orientation == "horizontal" {
		db.orientation = "horizontal"
	} else {
		db.orientation = "vertical"
	}
}

func (db *XyChartDb) setXAxisTitle(title XyText) {
	db.xAxis.title = js.Trim(title.text)
}

func (db *XyChartDb) setXAxisRangeData(min, max float64) {
	db.xAxis = Axis{typ: "linear", title: db.xAxis.title, min: min, max: max}
	db.hasSetXAxis = true
}

func (db *XyChartDb) setXAxisBand(categories []XyText) {
	names := make([]string, len(categories))
	for i, c := range categories {
		names[i] = js.Trim(c.text)
	}
	db.xAxis = Axis{typ: "band", title: db.xAxis.title, categories: names}
	db.hasSetXAxis = true
}

func (db *XyChartDb) setYAxisTitle(title XyText) {
	db.yAxis.title = js.Trim(title.text)
}

func (db *XyChartDb) setYAxisRangeData(min, max float64) {
	db.yAxis = Axis{typ: "linear", title: db.yAxis.title, min: min, max: max}
	db.hasSetYAxis = true
}

func (db *XyChartDb) plotData(values []float64) ([]float64, int) {
	if len(values) == 0 {
		return nil, 0
	}
	// The first series fixes the x range when no x-axis statement did.
	if !db.hasSetXAxis {
		db.setXAxisRangeData(1, float64(len(values)))
	}
	x := db.xAxis
	if x.typ == "band" && len(values) > len(x.categories) {
		values = values[:len(x.categories)]
	}
	if !db.hasSetYAxis {
		min, max := db.yAxis.min, db.yAxis.max
		for _, value := range values {
			if value < min {
				min = value
			}
			if value > max {
				max = value
			}
		}
		db.yAxis = Axis{typ: "linear", title: db.yAxis.title, min: min, max: max}
	}
	if x.typ == "band" {
		// pele pads the series with NaN to the count of categories; Plot.at does that on read.
		return values, len(x.categories)
	}
	return values, len(values)
}

func (db *XyChartDb) addPlot(typ string, title XyText, data []DataPoint) {
	values := make([]float64, len(data))
	for i, d := range data {
		values[i] = d.value
	}
	plot := Plot{typ: typ, title: js.Trim(title.text)}
	plot.data, plot.length = db.plotData(values)
	// Mermaid accepts labels on the points of a bar series and ignores them.
	if typ == "line" {
		for _, d := range data {
			if js.Trim(d.label) != "" {
				plot.pointLabels = make([]string, len(data))
				for i, d := range data {
					plot.pointLabels[i] = js.Trim(d.label)
				}
				break
			}
		}
	}
	db.plots = append(db.plots, plot)
}

func (db *XyChartDb) setLineData(title XyText, data []DataPoint) { db.addPlot("line", title, data) }

func (db *XyChartDb) setBarData(title XyText, data []DataPoint) { db.addPlot("bar", title, data) }

func (db *XyChartDb) setDiagramTitle(text string) { db.title = text }

func (db *XyChartDb) setAccTitle(text string) { db.accTitle = js.TrimStart(text) }

func (db *XyChartDb) setAccDescription(text string) { db.accDescr = joinLines(text) }

// joinLines is text.replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
