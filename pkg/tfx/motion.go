package tfx

import "fmt"

// Waypoint is a point a Path passes through. Bezier control points bend the
// approach to it.
type Waypoint struct {
	ID            string
	Coord         Coord
	BezierControl []Coord
}

func (w *Waypoint) key() waypointKey {
	return waypointKey{id: w.ID, coord: w.Coord}
}

// Segment is the span between two waypoints, with the flags that stop its
// enter and exit events firing twice.
type Segment struct {
	Start    Waypoint
	End      Waypoint
	Distance float64

	enterTriggered bool
	exitTriggered  bool
}

// Path is an ordered set of waypoints a character travels along at a fixed
// speed, optionally eased over the whole path.
type Path struct {
	ID       string
	Speed    float64
	Ease     Easing
	HasEase  bool
	Layer    int
	HasLayer bool
	HoldTime int
	Loop     bool

	Segments  []Segment
	Waypoints []Waypoint

	totalDistance       float64
	currentStep         int
	maxSteps            int
	holdTimeRemaining   int
	lastDistanceReached float64
	// originSegment is the synthetic segment from wherever the character
	// stands to the first waypoint. It is rebuilt on every activation.
	originSegment    *Segment
	hasOriginSegment bool
}

// PathOptions are the knobs NewPath accepts.
type PathOptions struct {
	Speed    float64
	Ease     Easing
	HasEase  bool
	Layer    int
	HasLayer bool
	HoldTime int
	Loop     bool
}

// NewWaypoint appends a waypoint and extends the path to reach it. An empty id
// gets an auto-allocated one.
func (p *Path) NewWaypoint(coord Coord, bezierControl []Coord, id string) (*Waypoint, error) {
	if id == "" {
		candidate := len(p.Waypoints)
		for {
			id = fmt.Sprintf("%d", candidate)
			if p.findWaypoint(id) == nil {
				break
			}
			candidate++
		}
	} else if p.findWaypoint(id) != nil {
		return nil, fmt.Errorf("path %q already has a waypoint %q", p.ID, id)
	}
	if len(bezierControl) == 0 {
		bezierControl = nil
	}
	p.Waypoints = append(p.Waypoints, Waypoint{ID: id, Coord: coord, BezierControl: bezierControl})
	if len(p.Waypoints) < 2 {
		return &p.Waypoints[len(p.Waypoints)-1], nil
	}
	prev := p.Waypoints[len(p.Waypoints)-2]
	current := p.Waypoints[len(p.Waypoints)-1]
	var distance float64
	if len(current.BezierControl) > 0 {
		distance = FindLengthOfBezierCurve(prev.Coord, current.BezierControl, current.Coord)
	} else {
		distance = FindLengthOfLine(prev.Coord, current.Coord, true)
	}
	p.totalDistance += distance
	p.Segments = append(p.Segments, Segment{Start: prev, End: current, Distance: distance})
	p.maxSteps = roundHalfEven(p.totalDistance / p.Speed)
	return &p.Waypoints[len(p.Waypoints)-1], nil
}

func (p *Path) findWaypoint(id string) *Waypoint {
	for i := range p.Waypoints {
		if p.Waypoints[i].ID == id {
			return &p.Waypoints[i]
		}
	}
	return nil
}

// Motion is one character's movement state.
type Motion struct {
	paths orderedMap[Path]

	CurrentCoord  Coord
	PreviousCoord Coord

	activePath    string
	hasActivePath bool
	completedPath string
}

func newMotion(inputCoord Coord) Motion {
	return Motion{
		paths:         newOrderedMap[Path](),
		CurrentCoord:  inputCoord,
		PreviousCoord: C(-1, -1),
	}
}

// SetCoordinate teleports the character without touching any path.
func (m *Motion) SetCoordinate(coord Coord) { m.CurrentCoord = coord }

// Path looks a path up by id, returning nil when it is absent.
func (m *Motion) Path(id string) *Path { return m.paths.Get(id) }

// NewPath registers a path. An empty id gets an auto-allocated one.
func (m *Motion) NewPath(id string, opts PathOptions) (*Path, error) {
	if opts.Speed <= 0 {
		return nil, fmt.Errorf("path speed must be above 0, got %v", opts.Speed)
	}
	if id == "" {
		id = m.paths.nextAutoID()
	} else if m.paths.Has(id) {
		return nil, fmt.Errorf("a path %q already exists on this character", id)
	}
	path := &Path{
		ID:                id,
		Speed:             opts.Speed,
		Ease:              opts.Ease,
		HasEase:           opts.HasEase,
		Layer:             opts.Layer,
		HasLayer:          opts.HasLayer,
		HoldTime:          opts.HoldTime,
		Loop:              opts.Loop,
		holdTimeRemaining: opts.HoldTime,
	}
	m.paths.Set(id, path)
	return path, nil
}

// MovementIsComplete reports whether the character has no path running.
func (m *Motion) MovementIsComplete() bool { return !m.hasActivePath }

// DeactivatePath stops the named path. An empty id stops whatever is running.
func (m *Motion) DeactivatePath(id string) {
	if id == "" || m.activePath == id {
		m.hasActivePath = false
		m.activePath = ""
	}
}
