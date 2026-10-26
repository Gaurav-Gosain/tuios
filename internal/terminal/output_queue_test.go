package terminal

import (
	"math/rand/v2"
	"testing"
)

// TestOutputQueueAgreesWithASliceModel drives a pane's output queue and a
// plain slice through the same seeded run of pushes and pops. The bursts are
// long enough to fill the queue to its bound and to make it grow while its
// oldest chunk sits in the middle of the array, which is where a ring loses or
// reorders chunks. Every pop and every refused push must agree with the slice.
// It then checks the trim, the close and the wake token on their own.
func TestOutputQueueAgreesWithASliceModel(t *testing.T) {
	q := newOutputQueue()
	if c := cap(q.wake()); c != 1 {
		t.Fatalf("a new queue's wake channel has %d slots, want 1", c)
	}
	if _, ok, closed := q.pop(); ok || closed {
		t.Fatalf("pop on a new queue reported ok=%v closed=%v, want neither", ok, closed)
	}
	q.trim()
	if q.oversized() {
		t.Fatal("a new queue reports oversized after a trim")
	}

	var model []uint64
	next := uint64(1)
	// drainWake takes a token left by an earlier push, so the next push that
	// makes the queue non-empty is the only thing that can leave one.
	drainWake := func() {
		select {
		case <-q.wake():
		default:
		}
	}
	push := func() {
		wasEmpty := len(model) == 0
		if wasEmpty {
			drainWake()
		}
		accepted := q.push(outputChunk{epoch: next})
		if want := len(model) < outputQueueSlots; accepted != want {
			t.Fatalf("push of chunk %d with %d queued reported %v, want %v", next, len(model), accepted, want)
		}
		if accepted {
			model = append(model, next)
			if wasEmpty {
				select {
				case <-q.wake():
				default:
					t.Fatalf("push of chunk %d into an empty queue left no token on wake", next)
				}
				select {
				case <-q.wake():
					t.Fatalf("push of chunk %d into an empty queue left more than one token on wake", next)
				default:
				}
			}
		}
		next++
	}
	pop := func() {
		c, ok, closed := q.pop()
		if closed {
			t.Fatal("pop on an open queue reported closed")
		}
		if len(model) == 0 {
			if ok {
				t.Fatalf("pop on an empty queue returned chunk %d", c.epoch)
			}
			return
		}
		if !ok {
			t.Fatalf("pop found nothing with %d queued", len(model))
		}
		if c.epoch != model[0] {
			t.Fatalf("pop returned chunk %d, want %d", c.epoch, model[0])
		}
		model = model[1:]
	}
	checkLen := func() {
		if got := q.len(); got != len(model) {
			t.Fatalf("len is %d, want %d", got, len(model))
		}
	}

	const minOps = 200_000
	rng := rand.New(rand.NewPCG(0x6f75747075, 0x71756575))
	ops, refused, trims := 0, 0, 0
	for ops < minOps {
		switch r := rng.IntN(10); {
		case r < 6:
			// A push burst up to twice the backlog makes the queue grow,
			// and near the bound it runs past it.
			for range 1 + rng.IntN(max(outputQueueKeep, 2*len(model))) {
				if len(model) == outputQueueSlots {
					refused++
				}
				push()
				ops++
				checkLen()
			}
		case r < 9:
			// A pop burst that takes part of the backlog leaves the oldest
			// chunk in the middle of the array for the next growth.
			for range 1 + rng.IntN(len(model)+1) {
				pop()
				ops++
				checkLen()
			}
		default:
			// Empty it and trim, so the next bursts grow the ring again from
			// its smallest size.
			for len(model) > 0 {
				pop()
				ops++
			}
			checkLen()
			q.trim()
			trims++
			push()
			ops++
			if q.oversized() {
				t.Fatalf("after a trim the next push did not start a %d-slot array", outputQueueKeep)
			}
		}
	}
	t.Logf("%d operations, %d pushes refused at the bound, %d trims", ops, refused, trims)
	if refused == 0 {
		t.Fatal("the run never pushed into a full queue, so it did not test the bound")
	}
	if trims == 0 {
		t.Fatal("the run never trimmed, so the ring grew only once")
	}

	// Fill to the bound and empty again: a queue that grew must say so, and a
	// trim must let the array go so the next push starts a small one.
	for len(model) < outputQueueSlots {
		push()
	}
	for len(model) > 0 {
		pop()
	}
	checkLen()
	if !q.oversized() {
		t.Fatalf("a queue that held %d chunks and emptied does not report oversized", outputQueueSlots)
	}
	q.trim()
	push()
	if q.oversized() {
		t.Fatalf("after a trim the next push did not start a %d-slot array", outputQueueKeep)
	}
	pop()

	// A trim of a queue that still holds chunks does nothing.
	for range 4 * outputQueueKeep {
		push()
	}
	if !q.oversized() {
		t.Fatalf("a queue holding %d chunks does not report oversized", 4*outputQueueKeep)
	}
	q.trim()
	if !q.oversized() {
		t.Fatal("a trim let the array of a queue that still holds chunks go")
	}
	checkLen()
	for len(model) > 0 {
		pop()
	}

	// A closed queue refuses chunks and still hands out what it holds, then
	// says nothing more will come.
	for range 3 {
		push()
	}
	q.close()
	if q.push(outputChunk{epoch: next}) {
		t.Fatal("a closed queue took a chunk")
	}
	checkLen()
	for len(model) > 0 {
		c, ok, closed := q.pop()
		if !ok || closed {
			t.Fatalf("pop on a closed queue holding %d chunks reported ok=%v closed=%v", len(model), ok, closed)
		}
		if c.epoch != model[0] {
			t.Fatalf("pop on a closed queue returned chunk %d, want %d", c.epoch, model[0])
		}
		model = model[1:]
	}
	if _, ok, closed := q.pop(); ok || !closed {
		t.Fatalf("pop on a closed and emptied queue reported ok=%v closed=%v, want ok=false closed=true", ok, closed)
	}
}

// TestOutputQueueIsOversizedOnceItGrowsPastSixteenSlots holds the size at
// which a drained queue is worth trimming. The ring starts at 16 slots and
// doubles, so 16 chunks fit the first array and the 17th makes it 32 slots,
// which is larger than the 16 a queue keeps.
func TestOutputQueueIsOversizedOnceItGrowsPastSixteenSlots(t *testing.T) {
	q := newOutputQueue()
	for i := range outputQueueKeep {
		if !q.push(outputChunk{epoch: uint64(i + 1)}) {
			t.Fatalf("push of chunk %d into an open queue was refused", i+1)
		}
	}
	if q.oversized() {
		t.Fatalf("a queue holding %d chunks reports oversized, want its first %d-slot array",
			outputQueueKeep, outputQueueKeep)
	}
	if !q.push(outputChunk{epoch: outputQueueKeep + 1}) {
		t.Fatalf("push of chunk %d into an open queue was refused", outputQueueKeep+1)
	}
	if !q.oversized() {
		t.Fatalf("a queue holding %d chunks does not report oversized", outputQueueKeep+1)
	}
	for range outputQueueKeep + 1 {
		if _, ok, _ := q.pop(); !ok {
			t.Fatal("pop found nothing in a queue that still held chunks")
		}
	}
	if !q.oversized() {
		t.Fatalf("a queue that grew to hold %d chunks and emptied does not report oversized", outputQueueKeep+1)
	}
	q.trim()
	if q.oversized() {
		t.Fatal("an emptied queue still reports oversized after a trim")
	}
}
