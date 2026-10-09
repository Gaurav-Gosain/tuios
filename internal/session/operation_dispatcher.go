package session

import (
	"errors"
	"fmt"
	"sync"
)

// SessionOp represents an operation that mutates session-owned fields.
// Operations are validated and applied under Session.mutateState, advancing
// the session Version and broadcasting canonical updates to attached clients.
type SessionOp interface {
	OpKind() string
	Apply(state *SessionState) error
}

// OpHandler processes a session operation against a SessionState.
type OpHandler func(state *SessionState, op SessionOp) error

// OperationDispatcher manages and routes operations that mutate session-owned fields.
type OperationDispatcher struct {
	mu       sync.RWMutex
	handlers map[string]OpHandler
}

// NewOperationDispatcher returns a new OperationDispatcher initialized with default handlers.
func NewOperationDispatcher() *OperationDispatcher {
	d := &OperationDispatcher{
		handlers: make(map[string]OpHandler),
	}
	d.registerDefaultHandlers()
	return d
}

// Register registers or overrides a handler for a specific operation kind.
func (d *OperationDispatcher) Register(kind string, handler OpHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[kind] = handler
}

// HasHandler reports whether an operation kind has a registered handler.
func (d *OperationDispatcher) HasHandler(kind string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.handlers[kind]
	return ok
}

// Dispatch executes a SessionOp against the provided Session under mutateState.
// It verifies the operation, advances the session Version, and publishes the
// state change to subscribers.
func (d *OperationDispatcher) Dispatch(s *Session, op SessionOp) error {
	if s == nil {
		return errors.New("nil session")
	}
	if op == nil {
		return errors.New("nil session operation")
	}

	if ltop, ok := op.(*LayoutTreeOp); ok {
		_, err := s.ApplyLayoutTreeOp(ltop)
		return err
	}

	kind := op.OpKind()
	d.mu.RLock()
	handler, hasHandler := d.handlers[kind]
	d.mu.RUnlock()

	return s.mutateState(func(state *SessionState) error {
		if hasHandler && handler != nil {
			return handler(state, op)
		}
		return op.Apply(state)
	})
}

// Execute applies an operation to a state using the registered handler, or op.Apply directly.
func (d *OperationDispatcher) Execute(state *SessionState, op SessionOp) error {
	if op == nil {
		return errors.New("nil session operation")
	}
	kind := op.OpKind()
	d.mu.RLock()
	handler, hasHandler := d.handlers[kind]
	d.mu.RUnlock()

	if hasHandler && handler != nil {
		return handler(state, op)
	}
	return op.Apply(state)
}

func (d *OperationDispatcher) registerDefaultHandlers() {
	d.handlers["LayoutTree"] = func(state *SessionState, op SessionOp) error {
		ltop, ok := op.(*LayoutTreeOp)
		if !ok {
			return fmt.Errorf("unexpected operation type for LayoutTree: %T", op)
		}
		return ltop.Apply(state)
	}
}

// LayoutTreeOp represents a BSP tree modification operation for a workspace.
type LayoutTreeOp struct {
	PushOrigin  string
	PushSeq     uint64
	Workspace   int
	Tree        *SerializedBSPTree
	Leaves      map[int]string
	BaseVersion int
}

func (op *LayoutTreeOp) OpKind() string { return "LayoutTree" }

func (op *LayoutTreeOp) Apply(state *SessionState) error {
	if op.Workspace < 0 || (op.Workspace > state.workspaceBound() && !IsScratchWorkspace(op.Workspace)) {
		return fmt.Errorf("workspace %d is out of range", op.Workspace)
	}
	trees, ids, next, changed := placeTree(state, op.Workspace, op.Tree, op.Leaves)
	if !changed {
		return errLayoutTreeSame
	}
	state.WorkspaceTrees, state.WindowToBSPID, state.NextBSPWindowID = trees, ids, next
	return nil
}

// guardSessionOwnedFields inspects an incoming client push against canonical state.
// When operation dispatch enforcement is active, direct mutation of the session-owned
// BSP tree via whole-state pushes is guarded and reverted to canonical state.
// Clients must dispatch BSP tree modifications via LayoutTree operations.
// Other client-driven state fields (such as zoom, float, ratios, scroll strip) remain
// ungoverned by this guard to preserve their normal push flows.
// It reports whether the BSP tree was reverted.
func guardSessionOwnedFields(incoming, canonical *SessionState) bool {
	if canonical == nil || incoming == nil {
		return false
	}

	// If the incoming push carries no trees, retainDaemonExclusive already preserves
	// canonical trees.
	if incoming.WorkspaceTrees == nil {
		return false
	}

	// If incoming trees differ from canonical trees, direct tree mutation was attempted.
	if !workspaceTreesEqual(incoming, canonical) {
		incoming.WorkspaceTrees = canonical.WorkspaceTrees
		incoming.WindowToBSPID = canonical.WindowToBSPID
		incoming.NextBSPWindowID = canonical.NextBSPWindowID
		return true
	}

	return false
}

// workspaceTreesEqual reports whether the BSP trees in incoming match canonical.
func workspaceTreesEqual(incoming, canonical *SessionState) bool {
	if len(incoming.WorkspaceTrees) == 0 && len(canonical.WorkspaceTrees) == 0 {
		return true
	}
	incNames := SessionTreeNames(incoming)
	canNames := SessionTreeNames(canonical)

	allWS := make(map[int]bool, len(incoming.WorkspaceTrees)+len(canonical.WorkspaceTrees))
	for ws := range incoming.WorkspaceTrees {
		allWS[ws] = true
	}
	for ws := range canonical.WorkspaceTrees {
		allWS[ws] = true
	}

	for ws := range allWS {
		incKey := TreeKey(incoming.WorkspaceTrees[ws], incNames)
		canKey := TreeKey(canonical.WorkspaceTrees[ws], canNames)
		if incKey != canKey {
			return false
		}
	}
	return true
}
