package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// publishBells publishes n bell events, which the replay ring keeps.
func publishBells(h *eventHub, n int) {
	for range n {
		h.publish(streamEvent{Type: EventBell, Session: "work"})
	}
}

// prefaceSeqs returns the seqs of the replayed events in a preface and the
// reason of its gap marker, "" when there is none.
func prefaceSeqs(t *testing.T, sub *eventSub) (seqs []uint64, gapReason string) {
	t.Helper()
	for i, ev := range sub.preface {
		if ev.Type == EventGap {
			if i != 0 {
				t.Fatalf("gap marker at position %d, want it first: %+v", i, sub.preface)
			}
			gapReason = ev.Reason
			continue
		}
		seqs = append(seqs, ev.Seq)
	}
	return seqs, gapReason
}

// TestEventHubResumeReplaysExactlyMissed verifies a resume replays exactly the
// events after the named seq, with no gap, and that the next live event follows
// on without repeating any of them.
func TestEventHubResumeReplaysExactlyMissed(t *testing.T) {
	h := newEventHub()
	publishBells(h, 5)

	sub, baseline, err := h.subscribeFrom(eventFilter{}, 16, &resumePoint{afterSeq: 2, bootID: h.bootID})
	if err != nil {
		t.Fatalf("subscribeFrom: %v", err)
	}
	defer h.unsubscribe(sub)
	if baseline != 5 {
		t.Fatalf("baseline = %d, want 5", baseline)
	}
	seqs, gap := prefaceSeqs(t, sub)
	if gap != "" {
		t.Fatalf("unexpected gap %q on an exact resume", gap)
	}
	if fmt.Sprint(seqs) != "[3 4 5]" {
		t.Fatalf("replayed seqs = %v, want [3 4 5]", seqs)
	}

	publishBells(h, 1)
	if live := <-sub.ch; live.Seq != 6 {
		t.Fatalf("first live seq = %d, want 6", live.Seq)
	}
}

// TestEventHubResumeFiltersReplay verifies the replay honours the subscription
// filter, the same as live delivery does.
func TestEventHubResumeFiltersReplay(t *testing.T) {
	h := newEventHub()
	h.publish(streamEvent{Type: EventBell, Session: "work"})       // 1
	h.publish(streamEvent{Type: EventBell, Session: "other"})      // 2
	h.publish(streamEvent{Type: EventAgentState, Session: "work"}) // 3
	h.publish(streamEvent{Type: EventBell, Session: "work"})       // 4

	filter := eventFilter{session: "work", types: map[string]bool{EventBell: true}}
	sub, _, err := h.subscribeFrom(filter, 16, &resumePoint{afterSeq: 0, bootID: h.bootID})
	if err != nil {
		t.Fatalf("subscribeFrom: %v", err)
	}
	defer h.unsubscribe(sub)
	seqs, gap := prefaceSeqs(t, sub)
	if gap != "" || fmt.Sprint(seqs) != "[1 4]" {
		t.Fatalf("replay = %v gap %q, want [1 4] and no gap", seqs, gap)
	}
}

// TestEventHubRingIsBounded verifies the ring keeps only the newest events,
// and that a resume from before the oldest one gets a gap with reason evicted
// followed by what the ring still holds.
func TestEventHubRingIsBounded(t *testing.T) {
	h := newEventHubSize(4)
	publishBells(h, 10)

	if h.ringLen != 4 {
		t.Fatalf("ring holds %d events, want 4", h.ringLen)
	}
	if h.evictedSeq != 6 {
		t.Fatalf("evictedSeq = %d, want 6", h.evictedSeq)
	}

	sub, _, err := h.subscribeFrom(eventFilter{}, 16, &resumePoint{afterSeq: 2, bootID: h.bootID})
	if err != nil {
		t.Fatalf("subscribeFrom: %v", err)
	}
	defer h.unsubscribe(sub)
	seqs, gap := prefaceSeqs(t, sub)
	if gap != GapEvicted {
		t.Fatalf("gap reason = %q, want %q", gap, GapEvicted)
	}
	if fmt.Sprint(seqs) != "[7 8 9 10]" {
		t.Fatalf("replayed seqs = %v, want [7 8 9 10]", seqs)
	}

	// Resuming from exactly the newest evicted seq misses nothing.
	sub2, _, err := h.subscribeFrom(eventFilter{}, 16, &resumePoint{afterSeq: 6, bootID: h.bootID})
	if err != nil {
		t.Fatalf("subscribeFrom: %v", err)
	}
	defer h.unsubscribe(sub2)
	if seqs, gap := prefaceSeqs(t, sub2); gap != "" || fmt.Sprint(seqs) != "[7 8 9 10]" {
		t.Fatalf("resume at the edge = %v gap %q, want [7 8 9 10] and no gap", seqs, gap)
	}
}

// TestEventHubResumeBootChanged verifies a resume that names another boot id,
// or a seq this daemon has not reached without a boot id, gets a gap with
// reason boot_changed and no replay: the seqs are not comparable.
func TestEventHubResumeBootChanged(t *testing.T) {
	h := newEventHub()
	publishBells(h, 3)

	for name, from := range map[string]resumePoint{
		"other boot id":         {afterSeq: 1, bootID: "not-this-boot"},
		"no boot id, seq ahead": {afterSeq: 99},
	} {
		t.Run(name, func(t *testing.T) {
			sub, _, err := h.subscribeFrom(eventFilter{}, 16, &from)
			if err != nil {
				t.Fatalf("subscribeFrom: %v", err)
			}
			defer h.unsubscribe(sub)
			if len(sub.preface) != 1 || sub.preface[0].Type != EventGap || sub.preface[0].Reason != GapBootChanged {
				t.Fatalf("preface = %+v, want one gap with reason boot_changed", sub.preface)
			}
			if sub.preface[0].BootID != h.bootID {
				t.Fatalf("gap boot_id = %q, want the current %q", sub.preface[0].BootID, h.bootID)
			}
		})
	}
}

// TestEventHubResumeAheadIsRefused verifies a seq this boot has not assigned,
// under this boot's own id, is an error rather than a silent empty replay.
func TestEventHubResumeAheadIsRefused(t *testing.T) {
	h := newEventHub()
	publishBells(h, 3)
	_, _, err := h.subscribeFrom(eventFilter{}, 16, &resumePoint{afterSeq: 4, bootID: h.bootID})
	if _, ok := errors.AsType[errResumeAhead](err); !ok {
		t.Fatalf("err = %v, want errResumeAhead", err)
	}
	if len(h.subs) != 0 {
		t.Fatalf("a refused resume left %d subscriptions registered", len(h.subs))
	}
}

// TestEventHubOutputNotRetained verifies output events stay out of the ring,
// and that a resume whose filter admits them is told so with a gap, while one
// that does not admit them replays exactly.
func TestEventHubOutputNotRetained(t *testing.T) {
	h := newEventHubSize(4)
	h.publish(streamEvent{Type: EventBell})   // 1
	h.publish(streamEvent{Type: EventOutput}) // 2
	h.publish(streamEvent{Type: EventOutput}) // 3
	h.publish(streamEvent{Type: EventBell})   // 4

	if h.ringLen != 2 {
		t.Fatalf("ring holds %d events, want 2 (output is not kept)", h.ringLen)
	}

	all, _, _ := h.subscribeFrom(eventFilter{}, 16, &resumePoint{afterSeq: 0, bootID: h.bootID})
	defer h.unsubscribe(all)
	if seqs, gap := prefaceSeqs(t, all); gap != GapNotRetained || fmt.Sprint(seqs) != "[1 4]" {
		t.Fatalf("unfiltered resume = %v gap %q, want [1 4] with gap not_retained", seqs, gap)
	}

	bells, _, _ := h.subscribeFrom(eventFilter{types: map[string]bool{EventBell: true}}, 16, &resumePoint{afterSeq: 0, bootID: h.bootID})
	defer h.unsubscribe(bells)
	if seqs, gap := prefaceSeqs(t, bells); gap != "" || fmt.Sprint(seqs) != "[1 4]" {
		t.Fatalf("bell resume = %v gap %q, want [1 4] and no gap", seqs, gap)
	}

	// A resume past the last output event has missed no output.
	late, _, _ := h.subscribeFrom(eventFilter{}, 16, &resumePoint{afterSeq: 3, bootID: h.bootID})
	defer h.unsubscribe(late)
	if seqs, gap := prefaceSeqs(t, late); gap != "" || fmt.Sprint(seqs) != "[4]" {
		t.Fatalf("resume after output = %v gap %q, want [4] and no gap", seqs, gap)
	}
}

// TestEventHubResumeNoDuplicatesUnderLoad resumes while another goroutine
// publishes, and checks that replay plus live delivery is every seq after the
// resume point exactly once, in order.
func TestEventHubResumeNoDuplicatesUnderLoad(t *testing.T) {
	h := newEventHub()
	publishBells(h, 50)

	const more = 500
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Go(func() {
		<-start
		publishBells(h, more)
	})

	close(start)
	time.Sleep(time.Millisecond)
	sub, _, err := h.subscribeFrom(eventFilter{}, more+50, &resumePoint{afterSeq: 20, bootID: h.bootID})
	if err != nil {
		t.Fatalf("subscribeFrom: %v", err)
	}
	defer h.unsubscribe(sub)
	wg.Wait()

	got, gap := prefaceSeqs(t, sub)
	if gap != "" {
		t.Fatalf("unexpected gap %q", gap)
	}
	for len(sub.ch) > 0 {
		got = append(got, (<-sub.ch).Seq)
	}
	want := uint64(21)
	for _, seq := range got {
		if seq != want {
			t.Fatalf("delivered seq %d, want %d (duplicate or hole): %v", seq, want, got)
		}
		want++
	}
	if want != 50+more+1 {
		t.Fatalf("delivered up to seq %d, want %d", want-1, 50+more)
	}
}
