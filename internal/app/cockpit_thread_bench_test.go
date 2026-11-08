package app

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// benchBigThread gives the selected agent the 200 entries the client reads
// of a long conversation (threadReadLimit): prompts, answers in Markdown with
// code, reads, and edits with diffs, at 200x50.
func benchBigThread(b testing.TB) (*OS, *agentThread) {
	m, t := benchDiffThreadT(b)
	m.Width, m.Height = 200, 50
	t.entries = t.entries[:0]
	para := strings.Repeat("The retry loop gave up after the third attempt because the backoff doubled past the deadline. ", 6)
	code := "```go\n" + strings.Repeat("func retry(n int) error {\n\tfor i := 0; i < n; i++ {\n\t\tif err := call(); err == nil {\n\t\t\treturn nil\n\t\t}\n\t}\n\treturn errTimeout\n}\n", 3) + "```\n"
	for i := 0; len(t.entries) < threadReadLimit; i++ {
		id := strconv.Itoa(i)
		var add []threadEntry
		switch i % 4 {
		case 0:
			add = append(add, threadEntry{ID: "u" + id, Role: "user", Kind: "text", Text: "Why does the retry loop stop before the deadline? Look at retry.go " + id})
		case 1:
			add = append(add, threadEntry{ID: "a" + id, Role: "assistant", Kind: "text", Text: "## Step " + id + "\n\n" + para + "\n\n" + code + "\n- one\n- two\n"})
		case 2:
			add = append(add,
				threadEntry{ID: "r" + id, Role: "assistant", Kind: "tool_call", Tool: "Read", Target: "internal/app/file" + id + ".go", ToolID: "t" + id},
				threadEntry{ID: "s" + id, Role: "tool", Kind: "tool_result", ToolID: "t" + id, Text: strings.Repeat("line of the file\n", 40)})
		case 3:
			d := &threadDiff{File: "internal/app/file" + id + ".go", Added: 30, Removed: 30}
			h := threadHunk{OldStart: 1, NewStart: 1}
			for j := range 30 {
				h.Lines = append(h.Lines, threadDiffLine{Op: "-", Text: "\tbackoff *= " + strconv.Itoa(j)}, threadDiffLine{Op: "+", Text: "\tbackoff = min(backoff*2, deadline/4) // " + strconv.Itoa(j)})
			}
			d.Hunks = []threadHunk{h}
			add = append(add, threadEntry{ID: "e" + id, Role: "assistant", Kind: "tool_call", Tool: "Edit", Target: d.File, Diff: d})
		}
		for _, en := range add {
			en.sum = threadSum(&en)
			t.entries = append(t.entries, en)
		}
	}
	t.gen++
	t.expandAll = true
	return m, t
}

// BenchmarkCockpitThreadOpen is the first frame of the long thread: every
// card built and drawn, as when the cursor comes to the agent.
func BenchmarkCockpitThreadOpen(b *testing.B) {
	m, t := benchBigThread(b)
	b.ReportAllocs()
	for b.Loop() {
		t.cards, t.body = nil, nil
		t.gen++
		m.renderInbox()
	}
}

// BenchmarkCockpitThreadScroll is a frame after one line of scroll in the
// long thread.
func BenchmarkCockpitThreadScroll(b *testing.B) {
	m, t := benchBigThread(b)
	m.renderInbox()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if i%2 == 0 {
			t.follow.Scroll(1)
		} else {
			t.follow.Scroll(-1)
		}
		m.cockpitTouch()
		m.renderInbox()
		i++
	}
}

// BenchmarkCockpitThreadAppend is a frame after the long thread grew by one
// answer.
func BenchmarkCockpitThreadAppend(b *testing.B) {
	m, t := benchBigThread(b)
	m.renderInbox()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		en := threadEntry{ID: "n" + strconv.Itoa(i), Role: "assistant", Kind: "text", Text: "Next step."}
		en.sum = threadSum(&en)
		t.entries = append(t.entries, en)
		t.gen++
		m.renderInbox()
		i++
	}
}

// TestCockpitThreadOpenBudget holds the allocations of the first frame of a
// long thread, and of a frame after it grew by one answer. Building every
// card of the 200 entries made 1.5 million allocations on open; the thread
// draws the newest turns the view needs, about 314 thousand. The budgets
// leave room for small changes, not for building the whole thread again.
func TestCockpitThreadOpenBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector adds allocations")
	}
	m, th := benchBigThread(t)
	open := testing.AllocsPerRun(3, func() {
		th.cards, th.body = nil, nil
		th.gen++
		m.renderInbox()
	})
	if open > 600_000 {
		t.Errorf("the first frame of a 200 entry thread made %.0f allocations, the budget is 600000", open)
	}
	// The entries slice is grown first, so a run is not charged for the
	// append that doubles it. The budget holds the fewest allocations of a
	// few measurements: the count moves by a few dozen between runs on
	// Linux, and only extra work makes it rise in every one.
	th.entries = slices.Grow(th.entries, 64)
	i := 0
	grow := math.Inf(1)
	for range 3 {
		grow = min(grow, testing.AllocsPerRun(5, func() {
			en := threadEntry{ID: "n" + strconv.Itoa(i), Role: "assistant", Kind: "text", Text: "Next step."}
			en.sum = threadSum(&en)
			th.entries = append(th.entries, en)
			th.gen++
			m.renderInbox()
			i++
		}))
	}
	if grow > 6_000 {
		t.Errorf("a frame after one new answer made %.0f allocations, the budget is 6000", grow)
	}
}
