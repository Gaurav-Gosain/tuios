package app

import (
	"image"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// viewMarkOS is a client showing part of a 300x80 session on a 207x55
// screen, scrolled off its top left corner, so the mark has arrows on it.
func viewMarkOS() *OS {
	m := &OS{Settings: config.Global, Width: realCols, Height: realRows,
		EffectiveWidth: 300, EffectiveHeight: 80}
	m.sessionView = sessionView{
		on:   true,
		clip: image.Rect(0, 0, realCols, realRows-1),
		box:  image.Rect(0, 0, 300, 79),
		offX: 10, offY: 5,
	}
	return m
}

// BenchmarkRenderViewMark is the mark drawn on every frame of a view of a
// larger session, with nothing about it changed since the last frame.
func BenchmarkRenderViewMark(b *testing.B) {
	m := viewMarkOS()
	b.ReportAllocs()
	for b.Loop() {
		_ = m.renderViewMark()
	}
}

// TestRenderViewMarkAllocations pins what the mark costs on a frame where it
// did not change: the layer, and not the label built again.
func TestRenderViewMarkAllocations(t *testing.T) {
	m := viewMarkOS()
	allocsAtMost(t, "renderViewMark", 1, func() { _ = m.renderViewMark() })
}
