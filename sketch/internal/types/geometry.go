// Not a file of pele: the geometry a renderer worked out, kept for drawing the
// diagram some other way, such as with box-drawing characters (sketch/text).
// Coordinates are those of the SVG.

package types

// Geometry is filled by a renderer when RenderOptions.Geometry is set. It has
// no effect on the SVG.
type Geometry struct {
	Width, Height float64

	// Flowchart.
	Nodes []GeoNode
	Edges []GeoEdge

	// Sequence.
	Actors   []GeoActor
	Messages []GeoMessage
	Notes    []GeoBox
	Blocks   []GeoBlock

	// State, class and ER diagrams fill Nodes and Edges too, and Title.
	Title []string
	// Pie, mindmap and gantt.
	Pie     *GeoPie
	Mindmap []GeoMindNode
	Gantt   *GeoGantt
}

type GeoNode struct {
	ID    string
	Shape string
	// X and Y are the centre; W and H the drawn size.
	X, Y, W, H float64
	Label      []string
	Group      bool
	Parent     int
	// Sections are the compartments under the label: a class's attributes and
	// methods, or a state's description.
	Sections [][]string
	// Table holds an ER entity's attributes: type, name, keys and comment.
	Table [][4]string
}

type GeoEdge struct {
	From, To int
	// Route holds x, y, axis triples from the start node to the end node.
	Route []float64
	// Start and End are Mermaid marker names: arrow_point, arrow_circle, arrow_cross or none.
	Start, End string
	// Stroke is normal, thick, dotted or invisible.
	Stroke         string
	Label          []string
	LabelX, LabelY float64
	// StartText and EndText sit at the ends: a class relation's cardinality.
	StartText, EndText string
}

type GeoActor struct {
	ID    string
	Kind  string
	Label []string
	// X is the lifeline; Top the top of the head box, W and H its size.
	X, Top, W, H float64
	// Foot is the top of the box under the lifeline, or -1 for none.
	Foot               float64
	LifeStart, LifeEnd float64
}

type GeoMessage struct {
	// From and To are actor indexes.
	From, To int
	Y        float64
	// X0 and X1 are where the line starts and ends.
	X0, X1 float64
	// Self is a message to the same actor: it leaves at Y and comes back at Y2.
	Self   bool
	Y2     float64
	Dotted bool
	// HeadStart and HeadEnd say whether there is a head at each end.
	HeadStart, HeadEnd bool
	Label              []string
	LabelX, LabelY     float64
}

type GeoBox struct {
	X, Y, W, H float64
	Label      []string
}

type GeoBlock struct {
	Kind                string
	Title               []string
	X0, X1, Top, Bottom float64
	Dividers            []float64
	DividerLabels       [][]string
}

// GeoPie is a pie chart's slices in the order pele draws them.
type GeoPie struct {
	Title  []string
	Slices []GeoSlice
}

type GeoSlice struct {
	Label string
	// Value is the number as the source gives it, Percent the share as pele
	// writes it on the slice, and Share the fraction of the whole.
	Value, Percent string
	Share          float64
}

// GeoMindNode is a mindmap node, in source order: the root first, every
// node after its parent.
type GeoMindNode struct {
	Label  []string
	Shape  string
	Parent int
}

// GeoGantt is a gantt chart as lists: a text drawing lays it out again in cells.
// Times are milliseconds since 1970.
type GeoGantt struct {
	Title       []string
	Start, Stop float64
	// Rows holds the section of each row.
	Rows  []string
	Tasks []GeoTask
	Ticks []GeoTick
	// Verts are the vertical markers.
	Verts []GeoTick
	// Today is the time of the today line, or NaN for none.
	Today float64
}

type GeoTask struct {
	Label string
	Row   int
	// Scheduled is false for a task that never resolved; it has no bar.
	Scheduled                     bool
	Start, End                    float64
	Milestone, Crit, Done, Active bool
}

type GeoTick struct {
	T     float64
	Label string
}
