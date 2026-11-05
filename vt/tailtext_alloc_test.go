package vt_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// tailTextFullScreen returns a 120x40 screen whose 40 rows each hold exactly
// 100 ASCII characters followed by 20 blank cells, and those rows top-down.
func tailTextFullScreen() (*vt.Emulator, []string) {
	emu := vt.NewEmulator(120, 40)
	letters := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 4)
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = fmt.Sprintf("%03d ", i) + letters[:96]
		_, _ = fmt.Fprintf(emu, "\x1b[%d;1H%s", i+1, rows[i])
	}
	return emu, rows
}

// TestTailTextAllocatesOnce holds reading a screen's tail to a fixed number of
// allocations, however many rows it returns, and its bytes to the text it
// returns when n asks for far more rows than the screen has.
//
// Measured on go1.27.3 with one strings.Builder reset per row: 51 allocations
// at n=10 and 201 at n=40, and 75,458 bytes at n=4096, of which the out slice
// sized by n is 64 KiB alone.
func TestTailTextAllocatesOnce(t *testing.T) {
	emu, rows := tailTextFullScreen()

	for _, n := range []int{10, 40} {
		got := testing.AllocsPerRun(50, func() { _ = emu.TailText(n) })
		if got > 5 {
			t.Errorf("TailText(%d) on a 120x40 screen allocates %v times, want at most 5", n, got)
		}
	}

	// The budget at n=4096 is built from what the screen holds:
	//   text    the rows' trimmed bytes,
	//   k*16    one string header per kept row,
	//   k*8     one int per kept row, for a length kept while walking the rows,
	//   1024    slack for the allocator's size classes.
	text := 0
	for _, r := range rows {
		text += len(strings.TrimRight(r, " \t"))
	}
	k := len(rows)
	budget := text + k*16 + k*8 + 1024
	res := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = emu.TailText(4096)
		}
	})
	if got := res.AllocedBytesPerOp(); got > int64(budget) {
		t.Errorf("TailText(4096) on a 120x40 screen of %d text bytes allocates %d bytes per call, want at most %d",
			text, got, budget)
	}
}

// TestTailTextRowsAreTheSame checks the rows TailText returns: blank rows are
// skipped and not counted, trailing spaces and tabs are trimmed, and a row of
// box drawing or wide text comes back whole.
func TestTailTextRowsAreTheSame(t *testing.T) {
	emu := vt.NewEmulator(120, 40)
	_, _ = fmt.Fprintf(emu, "\x1b[3;1Hfirst line")
	// Row 4 stays blank.
	_, _ = fmt.Fprintf(emu, "\x1b[5;1H日本語 wide")
	_, _ = fmt.Fprintf(emu, "\x1b[6;1Hspaces after   ")
	_, _ = fmt.Fprintf(emu, "\x1b[7;1Htab ")
	// Write moves the cursor for a tab, so the tab is set as a cell.
	emu.SetCell(4, 6, &uv.Cell{Content: "\t", Width: 1})
	_, _ = fmt.Fprintf(emu, "\x1b[9;1H╭──❯ ask ──╮")

	all := []string{
		"first line",
		"日本語 wide",
		"spaces after",
		"tab",
		"╭──❯ ask ──╮",
	}
	for _, tc := range []struct {
		n    int
		want []string
	}{
		{2, all[3:]},
		{4, all[1:]},
		{5, all},
		{40, all},
	} {
		if got := emu.TailText(tc.n); !slices.Equal(got, tc.want) {
			t.Errorf("TailText(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}

	// A first call's rows stay as they were after the screen changes.
	first := emu.TailText(40)
	_, _ = fmt.Fprintf(emu, "\x1b[3;1HFIRST LINE")
	_, _ = fmt.Fprintf(emu, "\x1b[9;1H┌──❯ new ──┐")
	second := emu.TailText(40)
	if !slices.Equal(first, all) {
		t.Errorf("rows of the first call after the screen changed = %q, want %q", first, all)
	}
	wantSecond := []string{"FIRST LINE", all[1], all[2], all[3], "┌──❯ new ──┐"}
	if !slices.Equal(second, wantSecond) {
		t.Errorf("TailText(40) after the screen changed = %q, want %q", second, wantSecond)
	}

	blank := vt.NewEmulator(120, 40).TailText(10)
	if blank == nil || len(blank) != 0 {
		t.Errorf("TailText(10) on a blank screen = %#v, want a non-nil empty slice", blank)
	}
}
