//go:build linux || darwin

package main

import "testing"

// BenchmarkMacFrame converts one 3024x1964 frame to the 1320x858 picture a
// 155x40 pane of 10x22 cells shows.
func BenchmarkMacFrame(b *testing.B) {
	y := &yuv{w: 3024, h: 1964, cw: 1512, ch: 982}
	y.y = make([]byte, 3024*1964)
	y.u = make([]byte, 1512*982)
	y.v = make([]byte, 1512*982)
	for i := range y.y {
		y.y[i] = byte(i * 7)
	}
	dst := make([]byte, 1320*858*3)
	for b.Loop() {
		y.rgb(dst, 1320, 858, 8)
	}
}

func TestAreaScaleFlat(t *testing.T) {
	src := make([]byte, 30*20)
	for i := range src {
		src[i] = 200
	}
	dst := make([]byte, 13*7)
	var tmp []uint16
	areaScale(dst, 13, 7, src, 30, 20, &tmp, 2)
	for i, v := range dst {
		if v != 200 {
			t.Fatalf("sample %d is %d, want 200", i, v)
		}
	}
}
