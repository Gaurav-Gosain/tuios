package overlay

// Rect is a half-open rectangle [X0,X1) x [Y0,Y1) in panel-relative cell
// coordinates (0,0 is the panel's top-left cell). Hosts translate an absolute
// mouse position into panel-relative coordinates by subtracting the panel's
// on-screen origin, then hit-test against these.
type Rect struct {
	X0, Y0, X1, Y1 int
}

// Contains reports whether (x, y) falls inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X0 && x < r.X1 && y >= r.Y0 && y < r.Y1
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool {
	return r.X1 <= r.X0 || r.Y1 <= r.Y0
}

// Geometry describes the interactive layout of a rendered panel, in
// panel-relative coordinates. A host records the panel's absolute origin when
// it places the panel, then uses these rects to route clicks.
type Geometry struct {
	Width, Height int  // total rendered size in cells
	TitleBar      Rect // the title row; a natural drag handle
	Tabs          []Rect
	BodyX, BodyY  int // top-left cell of the body area
	InnerWidth    int // content width between the side padding
}
