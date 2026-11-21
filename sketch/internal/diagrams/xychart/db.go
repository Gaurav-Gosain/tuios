package xychart

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type XyText struct {
	text string
	typ  string
}

type DataPoint struct {
	value float64
	label string
}

// Axis is pele's BandAxis or LinearAxis, told apart by typ.
type Axis struct {
	typ        string
	title      string
	categories []string
	min, max   float64
}

// Plot is a series. Its data are the values in their places on the x axis; a category
// with no value has NaN, which stands for pele's `undefined`. pele also keeps each
// place's name as text, which nothing reads; the port leaves it out.
type Plot struct {
	typ         string
	title       string
	data        []float64
	length      int
	pointLabels []string
}

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
	orientation string
	xAxis       Axis
	yAxis       Axis
	hasSetXAxis bool
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
