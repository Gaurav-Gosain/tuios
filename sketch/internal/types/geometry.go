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

	// Timeline.
	Timeline *GeoTimeline

	// XY chart.
	XY *GeoXY

	// Quadrant chart.
	Quadrant *GeoQuadrant
	// Journey.
	Journey *GeoJourney

	// Kanban.
	Kanban *GeoKanban

	// GitGraph.
	Git *GeoGit

	// Sankey.
	Sankey *GeoSankey

	// Packet.
	Packet *GeoPacket

	// Radar.
	Radar *GeoRadar

	// Treemap.
	Treemap *GeoTreemap

	// Info.
	Info *GeoInfo

	// TreeView.
	TreeView *GeoTreeView

	// Ishikawa.
	Ishikawa *GeoIshikawa

	// Cynefin.
	Cynefin *GeoCynefin

	// Event modeling.
	EventModel *GeoEventModel

	// Venn.
	Venn *GeoVenn

	// Wardley.
	Wardley *GeoWardley

	// Railroad.
	Railroad *GeoRailroad
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
	// ShowData says the source asked for the values beside the labels.
	ShowData bool
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
	// Excluded holds the start and the end of each run of excluded days.
	Excluded [][2]float64
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

// GeoTimeline is a timeline as lists, not places: a text drawing lays it out again in cells.
type GeoTimeline struct {
	Title []string
	// Groups are the sections in order, each with the periods in it. A group without a
	// label holds the periods that come before the first section.
	Groups  []GeoGroup
	Periods []GeoPeriod
}

type GeoGroup struct {
	Label   []string
	Periods []int
}

type GeoPeriod struct {
	Label  []string
	Events [][]string
}

// GeoXY is an XY chart as lists: a text drawing lays it out again in cells. Places
// along an axis are fractions of its length.
type GeoXY struct {
	Title      []string
	Horizontal bool
	// ValueTitle and CategoryTitle are the titles of the value axis and of the
	// category axis, "" for none.
	ValueTitle, CategoryTitle string
	// Ticks are the labelled places of the value axis, At 0 at the low end of the
	// range the SVG draws and 1 at the high end.
	Ticks []GeoMark
	// Marks are the labelled places of the category axis: every category of a band
	// axis, or the ticks of a numeric one.
	Marks []GeoMark
	// Slots is the count of equal slots along the category axis that bars sit in.
	Slots int
	// Band is set when the categories are named slots, not a numeric axis.
	Band bool
	// Base is the place on the value axis that bars stand on.
	Base   float64
	Series []GeoSeries
}

type GeoMark struct {
	At    float64
	Label string
}

type GeoSeries struct {
	Bar bool
	// Name is the name in the legend, "" for none.
	Name   string
	Points []GeoPoint
}

// GeoPoint is a value of a series: At along the category axis, Value along the
// value axis, not clamped to the range. A missing value is not in the list.
type GeoPoint struct {
	At, Value float64
	// Gap is set when a value is missing before this one, which breaks a line.
	Gap bool
	// Label is the text the SVG writes at the point: the label of a line
	// point, or the value of a bar when showDataLabel is on. "" for none.
	Label string
}

// GeoQuadrant is a quadrant chart as lists: a text drawing lays it out again in cells.
type GeoQuadrant struct {
	Title []string
	// XLow and XHigh name the left and right ends of the x axis; YLow and YHigh
	// the bottom and the top of the y axis.
	XLow, XHigh, YLow, YHigh string
	// Quadrants are the names of quadrants 1 to 4: top right, top left, bottom
	// left, bottom right.
	Quadrants [4]string
	Points    []GeoQPoint
}

// GeoQPoint is a point with X and Y from 0 to 1.
type GeoQPoint struct {
	X, Y  float64
	Label string
}

// GeoJourney is a user journey as lists: a text drawing lays it out again in cells.
type GeoJourney struct {
	Title []string
	// Sections are the runs of tasks that share a section, in order. A run outside any
	// section has no label.
	Sections []GeoGroup
	Tasks    []GeoStep
}

// GeoStep is a task of a user journey.
type GeoStep struct {
	Label []string
	// Level is the score as drawn, from 1 to 5; a score that is not a number is drawn at 3.
	Level float64
	// Mood is happy, neutral or sad.
	Mood   string
	People []string
}

// GeoKanban is a kanban board as lists: a text drawing lays it out again in cells.
type GeoKanban struct {
	Title   []string
	Columns []GeoColumn
}

type GeoColumn struct {
	Label []string
	Cards []GeoCard
}

type GeoCard struct {
	Label []string
	// Ticket, Assigned and Priority are as written, "" for none.
	Ticket, Assigned, Priority string
}

// GeoGit is a git graph as lists: a text drawing lays it out again in cells.
type GeoGit struct {
	Title []string
	// Lanes are the branch names in drawing order, "" where branch names are hidden.
	Lanes   []string
	Commits []GeoCommit
}

type GeoCommit struct {
	Lane int
	// Pos is the place along the history; commits that grow side by side share one.
	Pos float64
	// Kind is normal, reverse, highlight, merge or cherry-pick.
	Kind string
	// Label is the commit id as drawn, nil when ids are hidden.
	Label []string
	Tags  [][]string
	// Parents are indexes into Commits.
	Parents []int
}

// GeoSankey is a sankey diagram as lists: a text drawing lays it out again in cells.
type GeoSankey struct {
	Title []string
	Nodes []GeoFlowNode
	// Flows are in the order of the source.
	Flows []GeoFlow
}

type GeoFlowNode struct {
	Label string
	// Value is the flow through the node as the SVG writes it, "" when values are hidden.
	Value string
	// Layer is the column of the node, from 0 at the left.
	Layer int
	// Y is the middle of the node's bar, in SVG units.
	Y float64
}

type GeoFlow struct {
	// Source and Target are indexes into Nodes.
	Source, Target int
	// Value is the flow as written, with the prefix and the suffix, and
	// Size the number.
	Value string
	Size  float64
	// SourceY and TargetY are the middle of the band where it leaves its source
	// and where it enters its target, in SVG units.
	SourceY, TargetY float64
}

// GeoPacket is a packet as lists: a text drawing lays it out again in cells.
type GeoPacket struct {
	Title []string
	// BitsPerRow is the row length the fields were split at.
	BitsPerRow int
	// Descending numbers the bits of a row from the right.
	Descending bool
	ShowBits   bool
	// Fields are the pieces of the fields in order, each within one row.
	Fields []GeoField
}

type GeoField struct {
	Row        int
	Start, End float64
	Label      string
}

// GeoRadar is a radar chart as lists: a text drawing shows each value as a bar.
type GeoRadar struct {
	Title []string
	// Axes are the axis labels, in order around the chart.
	Axes []string
	// Curves are the curves drawn, each with a value for every axis.
	Curves []GeoCurve
	// Min and Max are the values at the centre and at the rim.
	Min, Max float64
	// Ticks is the count of graticule rings, and Polygon says they are
	// polygons through the axes instead of circles.
	Ticks   int
	Polygon bool
}

type GeoCurve struct {
	Label  string
	Values []float64
}

// GeoTreemap is a treemap as its placed boxes, and as the whole tree for a drawing that
// has no room for the boxes.
type GeoTreemap struct {
	Title []string
	// Width and Height are the size of the area the boxes fill.
	Width, Height float64
	// Boxes are the boxes with a size, parents before their children, in the area's coordinates.
	Boxes []GeoTreeBox
	// Tree is every node in order, with its depth; its places are not set.
	Tree []GeoTreeBox
}

type GeoTreeBox struct {
	X, Y, W, H float64
	Label      string
	// Value is the value as the diagram formats it, "" when values are hidden.
	Value   string
	Section bool
	Depth   int
}

// GeoInfo is the info diagram: its title and its line of text.
type GeoInfo struct {
	Title []string
	Text  string
}

// GeoTreeView is a file tree as its rows, in the order they are drawn.
type GeoTreeView struct {
	Title []string
	Rows  []GeoTreeRow
}

type GeoTreeRow struct {
	// Depth is 0 for a top-level entry; a row's children follow it one level deeper.
	Depth       int
	Name        string
	Directory   bool
	Icon        string
	Description string
	Highlight   bool
}

// GeoIshikawa is a fishbone as its outline: the effect, and each category with its causes.
type GeoIshikawa struct {
	Title      []string
	Effect     string
	Categories []GeoCategory
}

type GeoCategory struct {
	Label string
	// Causes are in outline order; Depth is 1 for a cause of the category itself.
	Causes []GeoCause
}

type GeoCause struct {
	Label string
	Depth int
}

// GeoCynefin is a Cynefin diagram as lists: the domains in the order complex, complicated,
// chaotic, clear and confusion, and the transitions between them.
type GeoCynefin struct {
	Title       []string
	Domains     []GeoDomain
	Transitions []GeoTransition
}

type GeoDomain struct {
	Name string
	// Model and Practice are the decision model and the kind of practice, "" when hidden.
	Model, Practice string
	Items           []string
}

type GeoTransition struct {
	From, To, Label string
}

// GeoEventModel is an event model as lists: the lanes from top to bottom, the frames in time
// order, the relations between them, and the notes and specifications.
type GeoEventModel struct {
	Title     []string
	Lanes     []string
	Boxes     []GeoFrame
	Relations [][2]int
	Notes     []GeoEventCard
}

type GeoFrame struct {
	ID, Name, Kind string
	// Lane is an index into Lanes.
	Lane  int
	Reset bool
	Data  []string
}

type GeoEventCard struct {
	Head  string
	Lines []string
}

// GeoVenn is a venn diagram as its regions: each set, then each overlap the source states, with
// the relative size it was given and what is written in it.
type GeoVenn struct {
	Title   []string
	Regions []GeoVennRegion
	// Circles are the sets' circles as the SVG places them, in the order of
	// their series colours.
	Circles []GeoVennCircle
}

type GeoVennCircle struct {
	Set     string
	X, Y, R float64
}

type GeoVennRegion struct {
	Sets  []string
	Size  float64
	Label string
	Texts []string
	// X and Y are where the SVG centres the region's label and texts.
	X, Y float64
}

// GeoWardley is a wardley map as lists. Evolution and visibility are percentages.
type GeoWardley struct {
	Title  []string
	Stages []string
	// Boundaries are the right edges of the stages, from 0 to 1, when the map sets them.
	Boundaries []float64
	Nodes      []GeoWardleyNode
	Links      []GeoWardleyLink
	// Trends are a node's index and the evolution it is heading for.
	Trends [][2]float64
	// Notes are the notes and the numbered annotations.
	Notes []string
}

type GeoWardleyNode struct {
	Label                 string
	Evolution, Visibility float64
	// Kind is anchor, component, pipeline or pipeline-component.
	Kind     string
	Strategy string
	Inertia  bool
}

type GeoWardleyLink struct {
	From, To int
	Label    string
	Dashed   bool
	Flow     string
}

// GeoRailroad is a railroad diagram as its rules, each a tree.
type GeoRailroad struct {
	Title []string
	Rules []GeoRule
}

type GeoRule struct {
	Name string
	Root *GeoRailNode
}

// GeoRailNode is a terminal, nonterminal or special with its Text, or a sequence, choice,
// optional or repetition of its Kids. A repetition runs from Min to Max times; Max is +Inf
// for no bound.
type GeoRailNode struct {
	Kind     string
	Text     string
	Kids     []*GeoRailNode
	Min, Max float64
}
