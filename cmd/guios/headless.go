package main

import (
	"fmt"
	"image/png"
	"os"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
)

// renderHeadless draws the window into PNG files with MyGo's in-memory
// renderer, with no window and no GPU: for frames on a machine where no
// window may open, and for a quick check that a build draws. It types the
// given text into the focused pane first, then times frames.
func (a *guiApp) renderHeadless(path string, w, h int, scale float32, typeText string, frames int) error {
	tt := ui.NewTester(a.view, w, h)
	tt.SetScale(scale)
	settle := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			tt.Frame()
			time.Sleep(50 * time.Millisecond)
		}
	}
	settle(2 * time.Second)
	if typeText != "" {
		a.control("type " + typeText)
		settle(time.Second)
	}
	var times []float64
	for range max(frames, 1) {
		t0 := time.Now()
		tt.Frame()
		times = append(times, float64(time.Since(t0).Microseconds())/1000)
	}
	sort.Float64s(times)
	pct := func(p float64) float64 { return times[min(len(times)-1, int(float64(len(times))*p))] }
	rss, anon := memoryKB()
	fmt.Printf("headless frames=%d p50=%.2fms p95=%.2fms max=%.2fms rss_kb=%d anon_kb=%d\n",
		len(times), pct(.5), pct(.95), times[len(times)-1], rss, anon)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, tt.Image())
}
