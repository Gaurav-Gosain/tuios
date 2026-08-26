package tfx

// Event is something a character's motion or animation reached. Effects hang
// actions off these, which is how a character chains one scene or path into
// the next without the effect polling for it.
type Event int

// The seven events, matching upstream's EventHandler.Event.
const (
	SegmentEntered Event = iota
	SegmentExited
	PathActivated
	PathComplete
	PathHolding
	SceneActivated
	SceneComplete
)

// callerKind separates the three things that can raise an event.
type callerKind int

const (
	callerSceneKind callerKind = iota
	callerPathKind
	callerWaypointKind
)

type waypointKey struct {
	id    string
	coord Coord
}

// Caller identifies what raised an event. Upstream hashes a whole Waypoint
// dataclass, so two waypoints with the same id and coord collide even across
// paths; the coord is kept in the key to match that.
type Caller struct {
	kind  callerKind
	id    string
	coord Coord
}

// SceneCaller keys handlers to a scene finishing or starting.
func SceneCaller(id string) Caller { return Caller{kind: callerSceneKind, id: id} }

// PathCaller keys handlers to a path finishing, starting or holding.
func PathCaller(id string) Caller { return Caller{kind: callerPathKind, id: id} }

// WaypointCaller keys handlers to a segment ending at a waypoint.
func WaypointCaller(w *Waypoint) Caller {
	return Caller{kind: callerWaypointKind, id: w.ID, coord: w.Coord}
}

func waypointCallerKey(k waypointKey) Caller {
	return Caller{kind: callerWaypointKind, id: k.id, coord: k.coord}
}

type handlerKey struct {
	event Event
	from  Caller
}

// Action is what happens when an event fires.
type Action struct {
	kind actionKind
	id   string
	// coord carries SetCoordinate's target, layer carries SetLayer's.
	coord    Coord
	layer    int
	callback func(e *Engine, ch *Character)
}

type actionKind int

const (
	actionActivatePath actionKind = iota
	actionActivateScene
	actionDeactivatePath
	actionDeactivateScene
	actionResetAppearance
	actionSetLayer
	actionSetCoordinate
	actionCallback
)

// ActivatePath starts a path when the event fires.
func ActivatePath(id string) Action { return Action{kind: actionActivatePath, id: id} }

// ActivateScene starts a scene when the event fires.
func ActivateScene(id string) Action { return Action{kind: actionActivateScene, id: id} }

// DeactivatePath stops a path. An empty id stops whatever is running.
func DeactivatePath(id string) Action { return Action{kind: actionDeactivatePath, id: id} }

// DeactivateScene stops a scene. An empty id stops whatever is running.
func DeactivateScene(id string) Action { return Action{kind: actionDeactivateScene, id: id} }

// ResetAppearance puts the character back to its input symbol and colours.
func ResetAppearance() Action { return Action{kind: actionResetAppearance} }

// SetLayer moves the character to a drawing layer. Higher layers win where two
// characters share a cell.
func SetLayer(layer int) Action { return Action{kind: actionSetLayer, layer: layer} }

// SetCoordinate teleports the character.
func SetCoordinate(coord Coord) Action { return Action{kind: actionSetCoordinate, coord: coord} }

// Callback runs effect code. Go closures let the effect keep its own state
// directly, so this port has no callback id table.
func Callback(fn func(e *Engine, ch *Character)) Action {
	return Action{kind: actionCallback, callback: fn}
}

// eventHandler stores per-character event subscriptions.
type eventHandler struct {
	actions map[handlerKey][]Action
	// subscribed is a bitmask over Event, so the hot emission sites can skip
	// building a key at all for a character that subscribes to nothing.
	subscribed uint8
}

func (h *eventHandler) register(event Event, c Caller, action Action) {
	if h.actions == nil {
		h.actions = make(map[handlerKey][]Action, 2)
	}
	key := handlerKey{event: event, from: c}
	h.actions[key] = append(h.actions[key], action)
	h.subscribed |= 1 << uint(event)
}

func (h *eventHandler) subscribes(event Event) bool {
	return h.subscribed&(1<<uint(event)) != 0
}
