package tfx

// Engine is the mutable world an effect runs in: the terminal, the random
// source, and the set of characters that still have work to do.
//
// Every stepping routine that can raise an event lives here rather than on the
// character, because an event action runs inline at the point it is raised and
// may reach back into the same structures being stepped. Ported from ttfx
// src/engine/ctx.rs.
type Engine struct {
	Terminal *Terminal
	Rng      *Rng

	// activeMask is indexed by character id. A dense mask keeps insertion and
	// removal at constant cost while iteration stays in ascending id order,
	// which several effects depend on.
	activeMask  []bool
	activeCount int
	// snapshot is reused by Update so a frame does not allocate.
	snapshot []*Character
}

// NewEngine builds an engine over a terminal.
func NewEngine(terminal *Terminal, rng *Rng) *Engine {
	return &Engine{Terminal: terminal, Rng: rng}
}

// ActiveCount is how many characters are still animating.
func (e *Engine) ActiveCount() int { return e.activeCount }

func (e *Engine) growMask() {
	if len(e.activeMask) < len(e.Terminal.Characters) {
		grown := make([]bool, len(e.Terminal.Characters))
		copy(grown, e.activeMask)
		e.activeMask = grown
	}
}

// Activate adds a character to the active set.
func (e *Engine) Activate(ch *Character) {
	e.growMask()
	if e.activeMask[ch.index] {
		return
	}
	e.activeMask[ch.index] = true
	e.activeCount++
}

// Deactivate removes a character from the active set.
func (e *Engine) Deactivate(ch *Character) {
	e.growMask()
	if !e.activeMask[ch.index] {
		return
	}
	e.activeMask[ch.index] = false
	e.activeCount--
}

// ClearActive empties the active set.
func (e *Engine) ClearActive() {
	for i := range e.activeMask {
		e.activeMask[i] = false
	}
	e.activeCount = 0
}

// ActiveCharacters returns the active set in ascending id order. The slice is
// reused between calls.
func (e *Engine) ActiveCharacters() []*Character {
	e.snapshot = e.snapshot[:0]
	for i, active := range e.activeMask {
		if active && i < len(e.Terminal.Characters) {
			e.snapshot = append(e.snapshot, e.Terminal.Characters[i])
		}
	}
	return e.snapshot
}

// handleEvent runs every action registered for an event and caller, in the
// order they were registered. An action may register more actions on the same
// key, so the list is indexed rather than ranged over.
func (e *Engine) handleEvent(ch *Character, event Event, from Caller) {
	if ch.handler.actions == nil {
		return
	}
	key := handlerKey{event: event, from: from}
	for i := 0; i < len(ch.handler.actions[key]); i++ {
		action := ch.handler.actions[key][i]
		switch action.kind {
		case actionActivatePath:
			e.ActivatePath(ch, action.id)
		case actionActivateScene:
			e.ActivateScene(ch, action.id)
		case actionDeactivatePath:
			ch.Motion.DeactivatePath(action.id)
		case actionDeactivateScene:
			e.DeactivateScene(ch, action.id)
		case actionResetAppearance:
			ch.Animation.SetAppearance(ch.InputSymbol, ColorPair{}, ch.UsesInputColors)
		case actionSetLayer:
			ch.Layer = action.layer
		case actionSetCoordinate:
			ch.Motion.CurrentCoord = action.coord
		case actionCallback:
			if action.callback != nil {
				action.callback(e, ch)
			}
		}
	}
}

// ActivatePath starts a path, splicing in a segment from wherever the
// character currently stands to the path's first waypoint.
func (e *Engine) ActivatePath(ch *Character, pathID string) {
	path := ch.Motion.Path(pathID)
	if path == nil || len(path.Waypoints) == 0 {
		return
	}
	first := path.Waypoints[0]
	var distance float64
	if len(first.BezierControl) > 0 {
		distance = FindLengthOfBezierCurve(ch.Motion.CurrentCoord, first.BezierControl, first.Coord)
	} else {
		distance = FindLengthOfLine(ch.Motion.CurrentCoord, first.Coord, true)
	}
	origin := Segment{
		Start:    Waypoint{ID: "origin", Coord: ch.Motion.CurrentCoord},
		End:      first,
		Distance: distance,
	}

	ch.Motion.activePath = pathID
	ch.Motion.hasActivePath = true
	path.totalDistance += distance
	if path.hasOriginSegment {
		path.totalDistance -= path.originSegment.Distance
		path.Segments[0] = origin
	} else {
		path.Segments = append([]Segment{origin}, path.Segments...)
	}
	originCopy := origin
	path.originSegment = &originCopy
	path.hasOriginSegment = true
	path.currentStep = 0
	path.holdTimeRemaining = path.HoldTime
	path.maxSteps = roundHalfEven(path.totalDistance / path.Speed)
	for i := range path.Segments {
		path.Segments[i].enterTriggered = false
		path.Segments[i].exitTriggered = false
	}
	if path.HasLayer {
		ch.Layer = path.Layer
	}
	if ch.handler.subscribes(PathActivated) {
		e.handleEvent(ch, PathActivated, PathCaller(pathID))
	}
}

// pathStep advances a path by one step and returns the coordinate reached.
func (e *Engine) pathStep(ch *Character, pathID string) Coord {
	path := ch.Motion.Path(pathID)
	if path == nil || len(path.Segments) == 0 {
		return ch.Motion.CurrentCoord
	}
	if path.maxSteps == 0 || path.currentStep >= path.maxSteps || path.totalDistance == 0 {
		return path.Segments[len(path.Segments)-1].End.Coord
	}
	path.currentStep++
	ratio := float64(path.currentStep) / float64(path.maxSteps)
	factor := ratio
	if path.HasEase {
		factor = path.Ease.Ease(ratio)
	}
	distanceToTravel := factor * path.totalDistance
	path.lastDistanceReached = distanceToTravel

	activeSegment := -1
	watchesEnter := ch.handler.subscribes(SegmentEntered)
	watchesExit := ch.handler.subscribes(SegmentExited)
	for i := 0; ; i++ {
		path = ch.Motion.Path(pathID)
		if path == nil || i >= len(path.Segments) {
			break
		}
		segment := &path.Segments[i]
		if distanceToTravel <= segment.Distance {
			activeSegment = i
			if !segment.enterTriggered {
				segment.enterTriggered = true
				if watchesEnter {
					key := segment.End.key()
					e.handleEvent(ch, SegmentEntered, waypointCallerKey(key))
				}
			}
			break
		}
		distanceToTravel -= segment.Distance
		if !segment.enterTriggered || !segment.exitTriggered {
			key := segment.End.key()
			if !segment.enterTriggered {
				segment.enterTriggered = true
				if watchesEnter {
					e.handleEvent(ch, SegmentEntered, waypointCallerKey(key))
				}
			}
			// An event action can replace the segment list, so re-resolve
			// before touching the exit flag.
			if path = ch.Motion.Path(pathID); path == nil || i >= len(path.Segments) {
				break
			}
			segment = &path.Segments[i]
			if !segment.exitTriggered {
				segment.exitTriggered = true
				if watchesExit {
					e.handleEvent(ch, SegmentExited, waypointCallerKey(key))
				}
			}
		}
	}

	path = ch.Motion.Path(pathID)
	if path == nil || len(path.Segments) == 0 {
		return ch.Motion.CurrentCoord
	}
	if activeSegment < 0 || activeSegment >= len(path.Segments) {
		// Upstream's for-else branch: an eased path can overshoot past the
		// last waypoint, and it is allowed to travel beyond it.
		activeSegment = len(path.Segments) - 1
		distanceToTravel += path.Segments[activeSegment].Distance
	}
	segment := path.Segments[activeSegment]
	t := 0.0
	if segment.Distance != 0 {
		t = distanceToTravel / segment.Distance
		if !path.HasEase && t > 1 {
			t = 1
		}
	}
	if len(segment.End.BezierControl) > 0 {
		return FindCoordOnBezierCurve(segment.Start.Coord, segment.End.BezierControl, segment.End.Coord, t)
	}
	return FindCoordOnLine(segment.Start.Coord, segment.End.Coord, t)
}

// MotionMove advances the character along its active path by one step.
func (e *Engine) MotionMove(ch *Character) {
	ch.Motion.PreviousCoord = ch.Motion.CurrentCoord
	if !ch.Motion.hasActivePath {
		return
	}
	pathID := ch.Motion.activePath
	path := ch.Motion.Path(pathID)
	if path == nil || len(path.Segments) == 0 {
		return
	}
	ch.Motion.CurrentCoord = e.pathStep(ch, pathID)

	// The step may have swapped the active path from inside a callback, so
	// read it again rather than reusing pathID.
	if !ch.Motion.hasActivePath {
		return
	}
	activeID := ch.Motion.activePath
	path = ch.Motion.Path(activeID)
	if path == nil {
		return
	}
	if path.currentStep != path.maxSteps {
		return
	}
	if path.HoldTime != 0 && path.holdTimeRemaining == path.HoldTime {
		if ch.handler.subscribes(PathHolding) {
			e.handleEvent(ch, PathHolding, PathCaller(activeID))
		}
		if held := ch.Motion.Path(activeID); held != nil {
			held.holdTimeRemaining--
		}
		return
	}
	if path.holdTimeRemaining != 0 {
		path.holdTimeRemaining--
		return
	}
	if path.Loop && len(path.Segments) > 1 {
		ch.Motion.DeactivatePath(activeID)
		e.ActivatePath(ch, activeID)
		return
	}
	ch.Motion.completedPath = activeID
	ch.Motion.DeactivatePath(activeID)
	if ch.handler.subscribes(PathComplete) {
		e.handleEvent(ch, PathComplete, PathCaller(activeID))
	}
}

// ChainPaths makes each path activate the next one when it completes.
func (e *Engine) ChainPaths(ch *Character, pathIDs []string, loop bool) {
	if len(pathIDs) < 2 {
		return
	}
	for i := 1; i < len(pathIDs); i++ {
		ch.RegisterEvent(PathComplete, PathCaller(pathIDs[i-1]), ActivatePath(pathIDs[i]))
	}
	if loop {
		ch.RegisterEvent(PathComplete, PathCaller(pathIDs[len(pathIDs)-1]), ActivatePath(pathIDs[0]))
	}
}

// ActivateScene starts a scene. It resumes rather than restarts: a scene that
// was part way through picks up where it stopped.
func (e *Engine) ActivateScene(ch *Character, sceneID string) {
	scene := ch.Animation.Scene(sceneID)
	if scene == nil {
		return
	}
	visual, err := scene.activate()
	if err != nil {
		return
	}
	ch.Animation.activeScene = sceneID
	ch.Animation.hasActive = true
	ch.Animation.currentVisual = visual
	if ch.handler.subscribes(SceneActivated) {
		e.handleEvent(ch, SceneActivated, SceneCaller(sceneID))
	}
}

// DeactivateScene stops a scene. An empty id stops whatever is running.
func (e *Engine) DeactivateScene(ch *Character, sceneID string) {
	if sceneID == "" || ch.Animation.activeScene == sceneID {
		ch.Animation.hasActive = false
		ch.Animation.activeScene = ""
	}
}

// StepAnimation advances the character's active scene by one tick.
func (e *Engine) StepAnimation(ch *Character) {
	if !ch.Animation.hasActive {
		return
	}
	scene := ch.Animation.Scene(ch.Animation.activeScene)
	if scene == nil || len(scene.frames) == 0 {
		return
	}
	switch {
	case scene.Sync != SyncNone:
		e.stepSyncedScene(ch, scene)
	case scene.HasEase:
		e.stepEasedScene(ch, scene)
	default:
		ch.Animation.currentVisual = scene.nextVisual()
	}
	e.completeSceneIfFinished(ch, scene)
}

// stepSyncedScene picks a frame from how far the character's motion has got
// rather than from elapsed ticks.
func (e *Engine) stepSyncedScene(ch *Character, scene *Scene) {
	var path *Path
	if ch.Motion.hasActivePath {
		path = ch.Motion.Path(ch.Motion.activePath)
	}
	if path == nil {
		// No motion to sync to: jump to the last frame and force the scene to
		// finish, which is what upstream does.
		last := scene.frames[len(scene.frames)-1]
		ch.Animation.currentVisual = scene.allFrames[last].Visual
		scene.playedFrames = append(scene.playedFrames, scene.frames...)
		scene.frames = scene.frames[:0]
		return
	}
	finalIndex := len(scene.frames) - 1
	var progress float64
	if scene.Sync == SyncStep {
		progress = float64(max(path.currentStep, 1)) / float64(max(path.maxSteps, 1))
	} else {
		total := math64Max(path.totalDistance, 1)
		remaining := math64Max(path.totalDistance-path.lastDistanceReached, 1)
		reached := math64Max(total-remaining, 1)
		progress = reached / total
	}
	index := roundHalfEven(float64(finalIndex) * progress)
	index = min(max(index, 0), finalIndex)
	if index < 0 || index >= len(scene.frames) {
		return
	}
	ch.Animation.currentVisual = scene.allFrames[scene.frames[index]].Visual
}

func math64Max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// stepEasedScene picks a frame from the easing curve rather than at a constant
// rate, so the scene speeds up or slows down over its run.
func (e *Engine) stepEasedScene(ch *Character, scene *Scene) {
	if scene.easingTotalSteps == 0 {
		return
	}
	ratio := float64(scene.easingStep) / float64(scene.easingTotalSteps)
	factor := scene.Ease.Ease(ratio)
	finalIndex := max(scene.easingTotalSteps-1, 0)
	index := roundHalfEven(factor * float64(finalIndex))
	index = min(max(index, 0), finalIndex)
	if index < len(scene.frameIndexMap) {
		ch.Animation.currentVisual = scene.allFrames[scene.frameIndexMap[index]].Visual
	}
	scene.easingStep++
	if scene.easingStep == scene.easingTotalSteps {
		if scene.IsLooping {
			scene.easingStep = 0
		} else {
			scene.playedFrames = append(scene.playedFrames, scene.frames...)
			scene.frames = scene.frames[:0]
		}
	}
}

// completeSceneIfFinished retires a scene that ran out of frames. A looping
// scene raises SceneComplete on every tick, faithfully.
func (e *Engine) completeSceneIfFinished(ch *Character, scene *Scene) {
	if len(scene.frames) != 0 && !scene.IsLooping {
		return
	}
	if !scene.IsLooping {
		scene.reset()
		ch.Animation.hasActive = false
		ch.Animation.activeScene = ""
	}
	if ch.handler.subscribes(SceneComplete) {
		e.handleEvent(ch, SceneComplete, SceneCaller(scene.ID))
	}
}

// Tick advances one character: motion first, then animation.
func (e *Engine) Tick(ch *Character) {
	e.MotionMove(ch)
	e.StepAnimation(ch)
}

// Update ticks every active character, then drops the ones that finished.
func (e *Engine) Update() {
	for _, ch := range e.ActiveCharacters() {
		e.Tick(ch)
	}
	e.growMask()
	for i, active := range e.activeMask {
		if !active || i >= len(e.Terminal.Characters) {
			continue
		}
		if !e.Terminal.Characters[i].IsActive() {
			e.activeMask[i] = false
			e.activeCount--
		}
	}
}

// Frame renders the current state as an ANSI string.
func (e *Engine) Frame() string { return e.Terminal.Frame() }

// FrameRows renders the current state as rows of visuals, top row first.
func (e *Engine) FrameRows() [][]*CharacterVisual { return e.Terminal.FrameRows() }
