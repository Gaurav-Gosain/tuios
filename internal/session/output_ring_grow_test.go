package session

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

// ringSize is the catch-up ring's bound: the length of the ring every pane
// held from the moment it was made, and the length a growing ring stops at.
const ringSize = 64 * 1024

// TestAnIdlePaneHoldsNoCatchUpRing is a perf budget. A pane that prints only
// a prompt used to reserve the whole catch-up ring when it was made, 64 KiB
// for every idle pane on the daemon. The ways it can fail:
//
//  1. The ring is allocated when the pane is made, before any output.
//  2. The first output allocates the whole ring instead of what it needs.
func TestAnIdlePaneHoldsNoCatchUpRing(t *testing.T) {
	_, sess := newTestDaemonSession(t)
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Command: []string{"sh", "-c", "sleep 1; printf 'ring-probe> '; sleep 30"}}, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	p := sess.GetPTY(win.PTYID)
	if p == nil {
		t.Fatalf("no PTY %q for the window", win.PTYID)
	}

	p.outputMu.RLock()
	before := cap(p.outputBuffer)
	p.outputMu.RUnlock()
	if before != 0 {
		t.Errorf("a pane that has printed nothing holds a ring of %d bytes, want none", before)
	}

	prompt := []byte("ring-probe> ")
	deadline := time.Now().Add(10 * time.Second)
	for {
		p.outputMu.RLock()
		held := bytes.Contains(p.outputBuffer[:p.outputPos], prompt)
		after := cap(p.outputBuffer)
		p.outputMu.RUnlock()
		if held {
			if after > 4096 {
				t.Errorf("a pane that has printed a prompt holds a ring of %d bytes, want at most 4096", after)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ring never held the prompt %q", prompt)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAGrowingRingHoldsWhatTheFixedRingHolds is a seeded differential, the
// fuzz kind. A pane's catch-up ring that starts empty and grows with its
// output is driven side by side with a ring that is 64 KiB from the start,
// by the same appends and resizes. The ways it can fail, written down first:
//
//  1. The ring shifts out bytes before it has grown to its bound, so a
//     resubscribing client is resumed from a later start than before.
//  2. Growth drops, doubles or moves a byte it copies.
//  3. The ring stops short of 64 KiB, or grows past it on a large chunk.
//  4. A chunk larger than the room left, or larger than the ring, keeps
//     another tail or leaves another position than the fixed ring does.
//  5. The stream position or the position returned for a chunk drifts.
//  6. A resize mark placed while the ring is short is pruned on the short
//     length, so a catch-up loses the width its bytes were laid out at.
//
// Half the pairs take chunks of log-uniform size from 1 B to 80 KiB, half
// take small appends that walk the growing ring through every size it passes
// on the way to 64 KiB. Every pair places at least two resizes while the
// growing ring is still short, and appends well past the ring after them.
func TestAGrowingRingHoldsWhatTheFixedRingHolds(t *testing.T) {
	const pairs = 240
	for i := range pairs {
		rng := rand.New(rand.NewPCG(0x6d656d33, uint64(i)))
		var (
			plan []ringOp
			kind string
		)
		if i%2 == 0 {
			kind = "log-uniform chunks"
			plan = logUniformRingPlan(rng)
		} else {
			kind = "growth walk"
			plan = growthWalkRingPlan(rng)
		}
		if err := ringDifferential(rng, plan); err != nil {
			t.Fatalf("pair %d (%s, %d steps): %v", i, kind, len(plan), err)
		}
	}
}

// ringOp is one step a pair is driven by: a resize, or an append of n bytes.
type ringOp struct {
	resize bool
	n      int
}

// shortMarkLimit is a stream position the growing ring is still well below
// 64 KiB at, so a resize placed before it is placed on a short ring.
const shortMarkLimit = 16 * 1024

// logUniformRingPlan draws chunks of log-uniform size from 1 B to 80 KiB,
// with resizes between them, until the stream is far past the ring. A plan
// that places fewer than two resizes before shortMarkLimit is drawn again.
func logUniformRingPlan(rng *rand.Rand) []ringOp {
	for {
		target := 96*1024 + rng.IntN(320*1024)
		plan := []ringOp{{resize: true}}
		seq, short := 0, 1
		for seq < target {
			if seq > 0 && (rng.IntN(3) == 0 || (short < 2 && seq < shortMarkLimit)) {
				plan = append(plan, ringOp{resize: true})
				if seq < shortMarkLimit {
					short++
				}
			}
			n := min(max(int(math.Exp(rng.Float64()*math.Log(80*1024))), 1), 80*1024)
			plan = append(plan, ringOp{n: n})
			seq += n
		}
		if short >= 2 {
			return plan
		}
	}
}

// growthWalkRingPlan appends small chunks until the stream is far past the
// ring, so the growing ring crosses 1, 2, 4, 8, 16, 32 and 64 KiB in turn.
// Resizes come at random, and always once past 1 KiB and once past 8 KiB.
func growthWalkRingPlan(rng *rand.Rand) []ringOp {
	target := 160*1024 + rng.IntN(64*1024)
	var plan []ringOp
	seq := 0
	forced := []int{1024, 8 * 1024}
	for seq < target {
		if len(forced) > 0 && seq >= forced[0] {
			forced = forced[1:]
			plan = append(plan, ringOp{resize: true})
		} else if rng.IntN(16) == 0 {
			plan = append(plan, ringOp{resize: true})
		}
		n := 1 + rng.IntN(600)
		plan = append(plan, ringOp{n: n})
		seq += n
	}
	return plan
}

// newRingPTY is a PTY with the fields Resize touches and the given ring.
func newRingPTY(buf []byte) *PTY {
	return &PTY{
		ID:           "ptytest-00000009",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: buf,
		vtWriteChan:  make(chan vtChunk, 8),
		ctx:          context.Background(),
	}
}

// appendRing appends under the locks every append in the daemon holds.
func appendRing(p *PTY, data []byte) int64 {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	return p.appendToBuffer(data)
}

// resizeRing resizes p and takes the resize back off its emulator queue, so a
// long plan never fills it.
func resizeRing(p *PTY, width, height int) error {
	if err := p.Resize(width, height); err != nil {
		return err
	}
	for {
		select {
		case <-p.vtWriteChan:
		default:
			return nil
		}
	}
}

// ringDifferential drives a fixed ring and a growing ring by plan and
// compares them after every step.
func ringDifferential(rng *rand.Rand, plan []ringOp) error {
	fixed := newRingPTY(make([]byte, ringSize))
	grow := newRingPTY(nil)
	resizes := 0
	for step, op := range plan {
		if op.resize {
			resizes++
			w, h := 40+resizes%50, 10+resizes%20
			if err := resizeRing(fixed, w, h); err != nil {
				return fmt.Errorf("step %d: resizing the fixed ring: %w", step, err)
			}
			if err := resizeRing(grow, w, h); err != nil {
				return fmt.Errorf("step %d: resizing the growing ring: %w", step, err)
			}
		} else {
			data := make([]byte, op.n)
			for j := range data {
				data[j] = byte(rng.UintN(256))
			}
			want := appendRing(fixed, data)
			got := appendRing(grow, data)
			if got != want {
				return fmt.Errorf("step %d: append of %d bytes returned %d, the fixed ring %d", step, op.n, got, want)
			}
		}
		if grow.outputSeq != fixed.outputSeq {
			return fmt.Errorf("step %d: stream position %d, the fixed ring %d", step, grow.outputSeq, fixed.outputSeq)
		}
		if grow.outputPos != fixed.outputPos {
			return fmt.Errorf("step %d: ring holds %d bytes, the fixed ring %d", step, grow.outputPos, fixed.outputPos)
		}
		if !bytes.Equal(grow.outputBuffer[:grow.outputPos], fixed.outputBuffer[:fixed.outputPos]) {
			return fmt.Errorf("step %d: ring holds other bytes than the fixed ring (%d held)", step, fixed.outputPos)
		}
		if !slices.Equal(grow.resizeMarks, fixed.resizeMarks) {
			return fmt.Errorf("step %d: resize marks %+v, the fixed ring %+v", step, grow.resizeMarks, fixed.resizeMarks)
		}
	}
	return nil
}
