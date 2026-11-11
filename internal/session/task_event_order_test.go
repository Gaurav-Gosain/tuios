package session

import (
	"encoding/json"
	"maps"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTaskEventsLeaveInTheOrderOfTheirChanges holds that two changes to one
// card publish their task events in the order the changes were made. A board
// replaces a card's row with each event, so an older event that leaves last
// keeps the card on a stage it has left until its next event.
//
// The race is made deterministic with the session's state sink. mutateState
// releases the state lock and then hands the snapshot to the sink, so a sink
// that blocks holds change A after its change and, when the event is published
// after mutateState returns, before its event. Each parked row: (1) sets a sink
// that parks the first snapshot that carries change A, once, and lets every
// earlier snapshot through; (2) makes A on its own goroutine; (3) when A is
// parked, sets the sink to nil; (4) makes change B, which answers; (5) checks A
// has still not answered; (6) lets A go. The sink must be nil for B: publishState
// calls the sink while it holds the session's push lock, so while A is parked
// no other snapshot gets past a non-nil sink, and B would wait on A. With the
// sink nil, B's publishState returns before it takes that lock.
//
// Each row asserts exactly one task event per change, A's before B's by stream
// sequence, and the last event's row against the card the session holds (or,
// when B removes the card, that it is gone).
//
// How it could pass wrongly, and what stops it:
//   - The sink never parks: the row fails with "never reached the state sink".
//   - A snapshot of a change the daemon made itself on a session with a pane
//     is parked in A's place (it carries A, and publishState drops a snapshot
//     at or below the one it delivered last), so A answers early: A is checked
//     not to have answered before B is made and again before it is let go.
//     Rows that need no pane run on a session with no window. The rows that
//     need one (claim, release) may set up once more on a fresh session when A
//     answered before B; a second miss fails the row.
//   - The subscriber misses an event: both events are counted, the
//     subscription is filtered to the session and the three task event types
//     so output never fills it, and its drop counter must read 0.
//
// Sites no row reaches, held by internal/lint
// TestTaskEventsArePublishedInsideTheirChange: remove (its change is never
// parked: as B it would catch nothing the stage row does not), autoStage,
// stampFan and claimForPane (task_auto.go), blockErrored, unblockErrored and
// pullNext (task_dispatch.go), claimForRemote and releaseHeld
// (task_project.go), applyVerify (task_verify.go), restoreTasks, and the
// session event sink's publishReleased.
func TestTaskEventsLeaveInTheOrderOfTheirChanges(t *testing.T) {
	d, sp := startTestDaemon(t)

	staged := func(stage string) func(streamEvent) bool {
		return func(ev streamEvent) bool {
			return ev.Action == TaskActionStaged && ev.Task != nil && ev.Task.Stage == stage
		}
	}
	action := func(a string) func(streamEvent) bool {
		return func(ev streamEvent) bool { return ev.Action == a }
	}
	card := func(snap *SessionState) (TaskState, bool) {
		i := taskIndex(snap.Tasks, "t1")
		if i < 0 {
			return TaskState{}, false
		}
		return snap.Tasks[i], true
	}
	add := orderCall{"add-task", map[string]any{"title": "order"}}
	stage := func(to string) orderCall {
		return orderCall{"set-task-stage", map[string]any{"id": "t1", "stage": to}}
	}

	rows := []struct {
		name string
		row  orderRow
	}{
		{"add", orderRow{
			a:        func(string) orderCall { return add },
			carriesA: func(snap *SessionState, _ string) bool { _, ok := card(snap); return ok },
			b:        func(string) orderCall { return stage(TaskStagePlan) },
			isA:      action(TaskActionAdded),
			isB:      staged(TaskStagePlan),
		}},
		{"claim", orderRow{
			pane:    true,
			prepare: func(string) []orderCall { return []orderCall{add} },
			a: func(w string) orderCall {
				return orderCall{"claim-task", map[string]any{"id": "t1", "window": w}}
			},
			carriesA: func(snap *SessionState, w string) bool { tk, ok := card(snap); return ok && tk.Holder == w },
			b:        func(string) orderCall { return stage(TaskStageReview) },
			isA:      action(TaskActionClaimed),
			isB:      staged(TaskStageReview),
		}},
		{"finish", orderRow{
			prepare:  func(string) []orderCall { return []orderCall{add} },
			a:        func(string) orderCall { return orderCall{"finish-task", map[string]any{"id": "t1"}} },
			carriesA: func(snap *SessionState, _ string) bool { tk, ok := card(snap); return ok && tk.Stage == TaskStageDone },
			b:        func(string) orderCall { return stage(TaskStageReview) },
			isA:      action(TaskActionFinished),
			isB:      staged(TaskStageReview),
		}},
		{"stage", orderRow{
			prepare:  func(string) []orderCall { return []orderCall{add} },
			a:        func(string) orderCall { return stage(TaskStagePlan) },
			carriesA: func(snap *SessionState, _ string) bool { tk, ok := card(snap); return ok && tk.Stage == TaskStagePlan },
			b:        func(string) orderCall { return stage(TaskStageReview) },
			isA:      staged(TaskStagePlan),
			isB:      staged(TaskStageReview),
		}},
		{"block and unblock", orderRow{
			prepare: func(string) []orderCall { return []orderCall{add} },
			a: func(string) orderCall {
				return orderCall{"block-task", map[string]any{"id": "t1", "reason": "waits on a review"}}
			},
			carriesA: func(snap *SessionState, _ string) bool { tk, ok := card(snap); return ok && tk.Blocked != nil },
			b: func(string) orderCall {
				return orderCall{"block-task", map[string]any{"id": "t1", "clear": true}}
			},
			isA: action(TaskActionBlocked),
			isB: action(TaskActionUnblocked),
		}},
		{"release", orderRow{
			pane: true,
			prepare: func(w string) []orderCall {
				return []orderCall{add, {"claim-task", map[string]any{"id": "t1", "window": w}}}
			},
			a:        func(string) orderCall { return orderCall{"release-task", map[string]any{"id": "t1"}} },
			carriesA: func(snap *SessionState, _ string) bool { tk, ok := card(snap); return ok && tk.Holder == "" },
			b:        func(string) orderCall { return stage(TaskStageReview) },
			isA:      action(TaskActionReleased),
			isB:      staged(TaskStageReview),
		}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			setups := 1
			if r.row.pane {
				setups = 2
			}
			for n := 1; n <= setups; n++ {
				if orderParked(t, d, sp, r.row) {
					if n > 1 {
						t.Logf("change A was parked on setup %d", n)
					}
					return
				}
			}
			t.Fatalf("change A was not parked: it answered before change B was made, on %d setups", setups)
		})
	}

	// A change refused inside its closure (r1: a release of a card no pane
	// holds) and one refused before it (r2: a stage move of a card that does
	// not exist) publish no task event; nor does a change that moves nothing
	// (a finish of a card already in done). The accepted changes beside them
	// publish exactly one event each, and the stream carries them in order.
	t.Run("refused changes publish nothing", func(t *testing.T) {
		sess, c := orderSession(t, d, sp, false)
		result(t, callP(c, t, "add-task", map[string]any{"session": sess.Name(), "title": "order"}))
		sub := orderSubscribe(t, d, sess)
		next := func(what string) streamEvent {
			t.Helper()
			select {
			case ev := <-sub.ch:
				return ev
			case <-time.After(5 * time.Second * testDeadlineScale):
				t.Fatalf("%s published no task event", what)
			}
			return streamEvent{}
		}

		mustRefuse(t, callP(c, t, "release-task", map[string]any{"session": sess.Name(), "id": "t1"}), ErrVerbInvalidParams, "release of a card no pane holds")
		mustRefuse(t, callP(c, t, "set-task-stage", map[string]any{"session": sess.Name(), "id": "t99", "stage": TaskStagePlan}), ErrVerbNoTask, "stage move of a card that does not exist")
		result(t, callP(c, t, "set-task-stage", map[string]any{"session": sess.Name(), "id": "t1", "stage": TaskStagePlan}))
		if ev := next("the accepted stage move"); ev.Action != TaskActionStaged || ev.Task == nil || ev.Task.ID != "t1" || ev.Task.Stage != TaskStagePlan {
			t.Fatalf("the first task event is %s %s, want the accepted stage move first: %s", ev.Type, ev.Action, orderDescribe([]streamEvent{ev}))
		}

		result(t, callP(c, t, "finish-task", map[string]any{"session": sess.Name(), "id": "t1"}))
		if ev := next("the finish"); ev.Action != TaskActionFinished || ev.Task == nil || ev.Task.Stage != TaskStageDone {
			t.Fatalf("the finish published %s, want one task-finished event to done", orderDescribe([]streamEvent{ev}))
		}
		result(t, callP(c, t, "finish-task", map[string]any{"session": sess.Name(), "id": "t1"}))
		result(t, callP(c, t, "set-task-stage", map[string]any{"session": sess.Name(), "id": "t1", "stage": TaskStageReview}))
		if ev := next("the marker stage move"); ev.Action != TaskActionStaged || ev.Task == nil || ev.Task.Stage != TaskStageReview {
			t.Fatalf("the next task event after the finish is %s, want the marker stage move to review: a finish that moved nothing published", orderDescribe([]streamEvent{ev}))
		}
		if rest := orderDrain(sub); len(rest) != 0 {
			t.Fatalf("%d task events after the accepted changes, want none: %s", len(rest), orderDescribe(rest))
		}
		if n := sub.dropped.Load(); n != 0 {
			t.Fatalf("the subscription dropped %d events", n)
		}
	})

	// The positive half: two changes made one after the other arrive in
	// order, with no sink in the way.
	t.Run("two changes in sequence", func(t *testing.T) {
		sess, c := orderSession(t, d, sp, false)
		result(t, callP(c, t, "add-task", map[string]any{"session": sess.Name(), "title": "order"}))
		sub := orderSubscribe(t, d, sess)
		result(t, callP(c, t, "set-task-stage", map[string]any{"session": sess.Name(), "id": "t1", "stage": TaskStagePlan}))
		result(t, callP(c, t, "set-task-stage", map[string]any{"session": sess.Name(), "id": "t1", "stage": TaskStageReview}))
		orderCheck(t, sess, sub, orderDrain(sub), func(ev streamEvent) bool {
			return ev.Action == TaskActionStaged && ev.Task.Stage == TaskStagePlan
		}, func(ev streamEvent) bool {
			return ev.Action == TaskActionStaged && ev.Task.Stage == TaskStageReview
		})
	})
}

// orderCall is one verb call of a row, without its session.
type orderCall struct {
	verb   string
	params map[string]any
}

// orderRow is one parked row: what is made before the sink is set, change A,
// which snapshot carries it, change B, and which event belongs to which.
// Every call acts on the card t1 as the person, and the window argument is
// the session's pane, empty on a session with no window.
type orderRow struct {
	pane     bool
	prepare  func(window string) []orderCall
	a, b     func(window string) orderCall
	carriesA func(snap *SessionState, window string) bool
	isA, isB func(streamEvent) bool
}

// orderSessions numbers the sessions so every subtest and every run in one
// process has its own.
var orderSessions atomic.Uint64

// orderSession makes a session of its own, with one live pane when pane is
// set and no window otherwise, and a connection that acts as the person.
func orderSession(t *testing.T, d *Daemon, sp string, pane bool) (*Session, *verbConn) {
	t.Helper()
	name := "event-order-" + strconv.FormatUint(orderSessions.Add(1), 10)
	if pane {
		return makeSessionWithWindow(t, d, name), dialVerb(t, sp)
	}
	sess, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess, dialVerb(t, sp)
}

// orderSubscribe reads the session's task events.
func orderSubscribe(t *testing.T, d *Daemon, sess *Session) *eventSub {
	t.Helper()
	sub := d.events.subscribe(eventFilter{sess: sess, types: map[string]bool{
		EventTaskChanged: true, EventTaskClaimed: true, EventTaskFinished: true,
	}}, 64)
	t.Cleanup(func() { d.events.unsubscribe(sub) })
	return sub
}

// orderParked runs one parked row on a fresh session. It reports false only
// when change A answered before change B was made on a session with a pane,
// which may set up once more.
func orderParked(t *testing.T, d *Daemon, sp string, r orderRow) bool {
	t.Helper()
	sess, c := orderSession(t, d, sp, r.pane)
	window := ""
	if r.pane {
		window = sess.GetState().Windows[0].ID
	}
	with := func(call orderCall) orderCall {
		params := map[string]any{"session": sess.Name()}
		maps.Copy(params, call.params)
		return orderCall{call.verb, params}
	}
	if r.prepare != nil {
		for _, call := range r.prepare(window) {
			call = with(call)
			result(t, callP(c, t, call.verb, call.params))
		}
	}
	sub := orderSubscribe(t, d, sess)

	parked, release := make(chan struct{}), make(chan struct{})
	var parkedOnce atomic.Bool
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	sess.SetStateSink(func(snap *SessionState) {
		if r.carriesA(snap, window) && parkedOnce.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	})
	t.Cleanup(func() { sess.SetStateSink(nil) })

	a := orderStart(dialVerb(t, sp), with(r.a(window)))
	select {
	case <-parked:
	case <-a.done:
		t.Fatalf("change A answered (%v, %v) and never reached the state sink: nothing was parked", a.resp, a.err)
	case <-time.After(10 * time.Second * testDeadlineScale):
		t.Fatal("change A never reached the state sink: nothing was parked")
	}
	sess.SetStateSink(nil)
	if orderAnswered(a) {
		letGo()
		if r.pane {
			return false
		}
		t.Fatalf("change A was not parked: it answered before change B was made (%v)", a.resp)
	}

	b := with(r.b(window))
	result(t, callP(c, t, b.verb, b.params))
	if orderAnswered(a) {
		t.Fatalf("change A was not parked: it answered before it was let go (%v)", a.resp)
	}
	letGo()
	select {
	case <-a.done:
	case <-time.After(10 * time.Second * testDeadlineScale):
		t.Fatal("change A did not answer after it was let go")
	}
	if a.err != nil {
		t.Fatalf("change A: %v", a.err)
	}
	if e, ok := a.resp["error"]; ok && e != nil {
		t.Fatalf("change A was refused: %v", e)
	}
	orderCheck(t, sess, sub, orderDrain(sub), r.isA, r.isB)
	return true
}

// orderCheck asserts exactly one event of A and one of B, A's first by stream
// sequence, the last event's row against the card t1 the session holds, and no
// event dropped.
func orderCheck(t *testing.T, sess *Session, sub *eventSub, evs []streamEvent, isA, isB func(streamEvent) bool) {
	t.Helper()
	if n := sub.dropped.Load(); n != 0 {
		t.Fatalf("the subscription dropped %d events", n)
	}
	if len(evs) != 2 {
		t.Fatalf("%d task events, want one for each of the two changes: %s", len(evs), orderDescribe(evs))
	}
	var seqA, seqB uint64
	for _, ev := range evs {
		switch {
		case isA(ev) && seqA == 0:
			seqA = ev.Seq
		case isB(ev) && seqB == 0:
			seqB = ev.Seq
		default:
			t.Fatalf("task events %s: want exactly one for change A and one for change B", orderDescribe(evs))
		}
	}
	if seqA > seqB {
		t.Errorf("change B's event (seq %d) left before change A's (seq %d): the events left in the reverse order of their changes", seqB, seqA)
	}
	last := evs[0]
	if evs[1].Seq > last.Seq {
		last = evs[1]
	}
	i := taskIndex(sess.GetState().Tasks, "t1")
	if i < 0 {
		t.Fatal("the session holds no card t1")
	}
	tk := sess.GetState().Tasks[i]
	got := last.Task
	holder, heldAt := "", int64(0)
	if got.Holder != nil {
		holder, heldAt = got.Holder.Window, got.Holder.HeldAt
	}
	if got.Stage != tk.Stage || got.StageAt != tk.StageAt || holder != tk.Holder || heldAt != tk.HeldAt ||
		(got.Blocked == nil) != (tk.Blocked == nil) || got.Blocked != nil && *got.Blocked != *tk.Blocked {
		t.Errorf("the last task event (%s %s) says stage %s, holder %q, blocked %v; the session holds stage %s, holder %q, blocked %v",
			last.Type, last.Action, got.Stage, holder, got.Blocked != nil, tk.Stage, tk.Holder, tk.Blocked != nil)
	}
}

// orderPending is a verb call answered on its own goroutine: done is closed
// when the answer, or the error that ended the wait, is in.
type orderPending struct {
	done chan struct{}
	resp map[string]any
	err  error
}

// orderStart sends call on c and reads the answer on a goroutine of its own.
func orderStart(c *verbConn, call orderCall) *orderPending {
	p := &orderPending{done: make(chan struct{})}
	line := `{"id":1,"verb":"` + call.verb + `","params":` + jsonParams(call.params) + "}\n"
	go func() {
		defer close(p.done)
		_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second * testDeadlineScale))
		if _, p.err = c.conn.Write([]byte(line)); p.err != nil {
			return
		}
		raw, err := c.r.ReadBytes('\n')
		if err != nil {
			p.err = err
			return
		}
		p.err = json.Unmarshal(raw, &p.resp)
	}()
	return p
}

// orderAnswered reports whether p has answered, without waiting.
func orderAnswered(p *orderPending) bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// orderDrain takes the events the subscription holds now. Every change in
// these tests has answered before it is called, and a verb answers after its
// event is published.
func orderDrain(sub *eventSub) []streamEvent {
	var evs []streamEvent
	for {
		select {
		case ev := <-sub.ch:
			evs = append(evs, ev)
		default:
			return evs
		}
	}
}

// orderDescribe lists events as seq, type, action and stage.
func orderDescribe(evs []streamEvent) string {
	out := ""
	for _, ev := range evs {
		stage := ""
		if ev.Task != nil {
			stage = ev.Task.Stage
		}
		out += " [" + strconv.FormatUint(ev.Seq, 10) + " " + ev.Type + " " + ev.Action + " " + stage + "]"
	}
	return out
}
