//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type cellGeom struct {
	cols, rows int
	cw, ch     float64 // cell size in pixels
}

func (a *app) geom() cellGeom {
	ws := getWinsize()
	g := cellGeom{cols: ws.cols, rows: ws.rows, cw: 8, ch: 16}
	// The terminal's own answer to CSI 16 t is exact. The pty's pixel size
	// divided by the cells can carry a remainder.
	if c := a.cellPx.Load(); c != 0 {
		g.cw, g.ch = float64(c>>16), float64(c&0xffff)
	} else if ws.xpx > 0 && ws.ypx > 0 {
		g.cw, g.ch = float64(ws.xpx/ws.cols), float64(ws.ypx/ws.rows)
	}
	return g
}

// layout fits a dw x dh desktop into the pane above the status line. The
// picture covers a whole number of cells, so a terminal (or tuios) that
// places it on cells shows it pixel for pixel, with no scaling. When the
// pane is smaller than the desktop, tuios-rd draws at the pane's pixel size.
// When it is larger, tuios-rd sends the desktop's own size and the terminal
// scales it up to the cells.
// tilesLayout places the desktop-size image on the cells that fit it. The
// terminal scales it. At a whole number of cells it needs no c and r.
func tilesLayout(g cellGeom, dw, dh int) (pl placement, dispW, dispH int) {
	rows := max(g.rows-1, 1)
	availW, availH := float64(g.cols)*g.cw, float64(rows)*g.ch
	s := math.Min(availW/float64(dw), availH/float64(dh))
	c := min(max(int(math.Round(float64(dw)*s/g.cw)), 1), g.cols)
	r := min(max(int(math.Round(float64(dh)*s/g.ch)), 1), rows)
	tw, th := int(float64(c)*g.cw), int(float64(r)*g.ch)
	if tw == dw && th == dh {
		return placement{w: dw, h: dh}, dw, dh
	}
	return placement{w: dw, h: dh, cols: c, rows: r}, tw, th
}

func layout(g cellGeom, dw, dh int) (pl placement, dispW, dispH int) {
	rows := max(g.rows-1, 1)
	availW, availH := float64(g.cols)*g.cw, float64(rows)*g.ch
	s := math.Min(availW/float64(dw), availH/float64(dh))
	c := min(max(int(math.Round(float64(dw)*s/g.cw)), 1), g.cols)
	r := min(max(int(math.Round(float64(dh)*s/g.ch)), 1), rows)
	tw, th := int(float64(c)*g.cw), int(float64(r)*g.ch)
	if s <= 1 {
		return placement{w: min(tw, dw), h: min(th, dh)}, min(tw, dw), min(th, dh)
	}
	return placement{w: dw, h: dh, cols: c, rows: r}, tw, th
}

type second struct {
	shown  int
	lat    []int64
	bytes  int64
	estLat bool
}

func cpuUS() int64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(0, &ru)
	return (ru.Utime.Sec+ru.Stime.Sec)*1_000_000 + int64(ru.Utime.Usec+ru.Stime.Usec)
}

func pctl(v []int64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return float64(s[min(len(s)-1, int(float64(len(s))*p))]) / 1000
}

func (a *app) display(okCh <-chan *gfxReply, sig <-chan os.Signal) int {
	var (
		pl         placement
		havePl     bool
		rgb        []byte
		prev       []byte
		scratch    []byte
		pending    int
		blackSince time.Time
		blackCheck <-chan time.Time
		noDisplay  bool
		padLeft    int
		padTop     int
		lastWrite  int64
		pace       <-chan time.Time
		shownN     int
		still      <-chan time.Time
		refining   bool
		refined    bool
		baseline   []uint16
		last       *yuv
		lastF      *frame
		pendAt     []int64
		firstOK    = false
		writtenN   = 0
		g          = a.geom()
		cur        second
		lastCPU    = cpuUS()
		allLat     []int64
		allShown   int
		allBytes   int64
		startUS    = monoUS()
		fps        float64
		latP50     float64
		cpuPct     float64
		estLat     bool
	)
	a.replies = a.mode == modeShm && !a.cfg.noReply
	var glog *os.File
	gseq := 0
	if a.cfg.guestLog != "" {
		if f, err := os.OpenFile(a.cfg.guestLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			glog = f
			defer f.Close()
		}
	}
	var statsF *os.File
	if a.cfg.stats != "" {
		f, err := os.OpenFile(a.cfg.stats, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			statsF = f
			defer f.Close()
		}
	}
	var done <-chan time.Time
	if a.cfg.duration > 0 {
		done = time.After(a.cfg.duration)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	noReplyCheck := time.After(3 * time.Second)
	var buf bytes.Buffer
	// The frames path sends the picture at the pane's pixel size: a smaller copy of the desktop.
	var small []byte
	var smallTmp []uint16

	status := func() {
		a.drawStatus(g, fps, latP50, estLat, cpuPct, refined)
	}
	status()

	show := func(srcW, srcH int, fill func(dst []byte, w, h int), f *frame) {
		npl, dw, dh := layout(g, srcW, srcH)
		if !havePl || npl != pl {
			pl, havePl = npl, true
			// Centre the picture on whole cells.
			c, r := pl.cols, pl.rows
			if c == 0 {
				c, r = int(float64(pl.w)/g.cw+0.5), int(float64(pl.h)/g.ch+0.5)
			}
			padLeft, padTop = max((g.cols-c)/2, 0), max((max(g.rows-1, 1)-r)/2, 0)
			a.offX.Store(int64(float64(padLeft) * g.cw))
			a.offY.Store(int64(float64(padTop) * g.ch))
			prev = nil
			buf.Reset()
			buf.WriteString("\x1b[H\x1b[2J")
			_, _ = a.out.Write(buf.Bytes())
			status()
		}
		a.dispW.Store(int64(dw))
		a.dispH.Store(int64(dh))
		a.deskW.Store(int64(srcW))
		a.deskH.Store(int64(srcH))
		size := pl.w * pl.h * 3
		buf.Reset()
		fmt.Fprintf(&buf, "\x1b[?2026h\x1b[%d;%dH", padTop+1, padLeft+1)
		switch a.mode {
		case modeShm:
			slot, err := a.pool.next(size)
			if err != nil {
				a.setNotice("Shared memory failed: " + err.Error() + ". Sending frames over the terminal.")
				a.mode = modeEdits
				a.replies = false
				return
			}
			fill(slot.mem, pl.w, pl.h)
			if glog != nil && f != nil {
				gseq++
				seq := a.cfg.seqBase + gseq
				binary.LittleEndian.PutUint64(slot.mem[:8], uint64(seq))
				lat, _ := a.latency(f, monoUS())
				fmt.Fprintf(glog, "%d %d\n", seq, time.Now().UnixNano()-lat*1000)
			}
			q := 2
			if a.replies {
				q = 0
			}
			fmt.Fprintf(&buf, "\x1b_Ga=T,i=%d,p=1,f=24,s=%d,v=%d%s,C=1,q=%d,t=f,S=%d;%s\x1b\\",
				imgID, pl.w, pl.h, pl.keys(), q, size, b64(slot.path))
		default:
			if len(rgb) != size {
				rgb = make([]byte, size)
				prev = nil
			}
			fill(rgb, pl.w, pl.h)
			if a.mode == modeFull || prev == nil {
				writeFull(&buf, rgb, pl)
			} else {
				for _, r := range damage(prev, rgb, pl.w, pl.h) {
					writePatch(&buf, rgb, pl.w, r.x, r.y, r.w, r.h, &scratch)
				}
			}
			if len(prev) != size {
				prev = make([]byte, size)
			}
			copy(prev, rgb)
		}
		buf.WriteString("\x1b[?2026l")
		_, _ = a.out.Write(buf.Bytes())
		now := monoUS()
		writtenN++
		if a.mode == modeShm && a.replies {
			pending++
			pendAt = append(pendAt, now)
		}
		shownN++
		if f == nil { // a refined still picture: no frame timing
			return
		}
		cur.shown++
		cur.bytes += int64(buf.Len())
		lat, est := a.latency(f, now)
		cur.lat = append(cur.lat, lat)
		cur.estLat = est
		if now-startUS > 2_000_000 {
			allLat = append(allLat, lat)
			allShown++
			allBytes += int64(buf.Len())
		}
	}

	// Tiles: tuios-rd never scales. The image keeps the desktop size and the
	// terminal scales the placement (c=, r=) on its GPU. Frame edits then
	// stay small: a scroll is two a=c copies through a scratch frame 2, and
	// new pixels are a=f patches at the desktop's own resolution.
	var tilesPace <-chan time.Time
	tilesFull := true // the terminal has no whole picture yet (edits mode)
	tilesShow := func() {
		if iv := a.cfg.pace.Microseconds(); iv > 0 {
			if wait := lastWrite + iv - monoUS(); wait > 1000 {
				if tilesPace == nil {
					tilesPace = time.After(time.Duration(wait) * time.Microsecond)
				}
				return
			}
		}
		if a.mode == modeShm && a.replies && pending >= a.cfg.inflight {
			if len(pendAt) > 0 && monoUS()-pendAt[0] > 1_000_000 {
				pendAt = pendAt[1:]
				pending--
			} else {
				return
			}
		}
		tb := &a.tb
		tb.mu.Lock()
		// The lock covers the picture, not the terminal write: the decoder applies and ACKs the
		// next frame while a slow terminal takes this one.
		locked := true
		defer func() {
			if locked {
				tb.mu.Unlock()
			}
		}()
		// A partial frame is not drawn, unless partial frames have come for
		// 50 ms: then the engine is behind, and a mixed picture beats none.
		waitPartial := tb.partial && time.Since(tb.partialSince) < 50*time.Millisecond
		if !tb.ready || waitPartial || (!tb.all && len(tb.log) == 0) {
			if waitPartial && tilesPace == nil {
				tilesPace = time.After(10 * time.Millisecond)
			}
			return
		}
		scaleStart := monoUS()
		npl, dw, dh := tilesLayout(g, tb.w, tb.h)
		if !havePl || npl != pl {
			pl, havePl = npl, true
			c, r := pl.cols, pl.rows
			if c == 0 {
				c, r = int(float64(pl.w)/g.cw+0.5), int(float64(pl.h)/g.ch+0.5)
			}
			padLeft, padTop = max((g.cols-c)/2, 0), max((max(g.rows-1, 1)-r)/2, 0)
			a.offX.Store(int64(float64(padLeft) * g.cw))
			a.offY.Store(int64(float64(padTop) * g.ch))
			_, _ = a.out.WriteString("\x1b[H\x1b[2J")
			status()
			tilesFull = true
		}
		a.dispW.Store(int64(dw))
		a.dispH.Store(int64(dh))
		a.deskW.Store(int64(tb.w))
		a.deskH.Store(int64(tb.h))
		w, size := tb.w, tb.w*tb.h*3
		buf.Reset()
		fmt.Fprintf(&buf, "\x1b[?2026h\x1b[%d;%dH", padTop+1, padLeft+1)
		switch {
		case a.mode == modeShm:
			slot, err := a.pool.next(size)
			if err != nil {
				a.setNotice("Shared memory failed: " + err.Error() + ". Sending frames over the terminal.")
				a.mode = modeEdits
				a.replies = false
				return
			}
			copy(slot.mem, tb.pix)
			q := 2
			if a.replies {
				q = 0
			}
			fmt.Fprintf(&buf, "\x1b_Ga=T,i=%d,p=1,f=24,s=%d,v=%d%s,C=1,q=%d,t=f,S=%d;%s\x1b\\",
				imgID, pl.w, pl.h, pl.keys(), q, size, b64(slot.path))
		case a.mode == modeFull && (dw < tb.w || dh < tb.h):
			// Whole frames go through the terminal: send them at the pane's pixel size.
			if n := dw * dh * 3; cap(small) < n {
				small = make([]byte, n)
			}
			small = small[:dw*dh*3]
			areaScale(small, dw, dh, tb.pix, tb.w, tb.h, &smallTmp, a.cfg.jobs)
			writeFull(&buf, small, placement{w: dw, h: dh, cols: pl.cols, rows: pl.rows})
		case a.mode == modeFull || tilesFull || tb.all:
			writeFull(&buf, tb.pix, pl)
			if a.mode == modeEdits {
				// The scratch frame 2 for scroll copies: a copy of frame 1.
				fmt.Fprintf(&buf, "\x1b_Ga=f,i=%d,c=1,x=0,y=0,s=1,v=1,f=24,X=1,q=2;%s\x1b\\", imgID, base64.StdEncoding.EncodeToString(tb.pix[:3]))
				tilesFull = false
			}
		default:
			// Replay in order. A copy after new pixels would move pixels that
			// are already final, so it goes as a patch of its destination.
			patched := false
			for _, op := range tb.log {
				r := op.r
				if op.copy && !patched {
					fmt.Fprintf(&buf, "\x1b_Ga=c,i=%d,r=1,c=2,C=1,X=%d,Y=%d,x=%d,y=%d,w=%d,h=%d,q=2\x1b\\", imgID, op.sx, op.sy, r.x, r.y, r.w, r.h)
					fmt.Fprintf(&buf, "\x1b_Ga=c,i=%d,r=2,c=1,C=1,X=%d,Y=%d,x=%d,y=%d,w=%d,h=%d,q=2\x1b\\", imgID, r.x, r.y, r.x, r.y, r.w, r.h)
					continue
				}
				patched = true
				writePatch(&buf, tb.pix, w, r.x, r.y, r.w, r.h, &scratch)
			}
		}
		buf.WriteString("\x1b[?2026l")
		tb.log, tb.all = tb.log[:0], false
		f := tb.f
		if d := monoUS() - scaleStart; d > a.tilesScaleMax.Load() {
			a.tilesScaleMax.Store(d)
		}
		tb.mu.Unlock()
		locked = false
		_, _ = a.out.Write(buf.Bytes())
		now := monoUS()
		lastWrite = now
		writtenN++
		shownN++
		if a.mode == modeShm && a.replies {
			pending++
			pendAt = append(pendAt, now)
		}
		cur.shown++
		cur.bytes += int64(buf.Len())
		lat, est := a.latency(&f, now)
		cur.lat = append(cur.lat, lat)
		cur.estLat = est
		if now-startUS > 2_000_000 {
			allLat = append(allLat, lat)
			allShown++
			allBytes += int64(buf.Len())
		}
	}

	showYUV := func(b *yuv, f *frame) {
		show(b.w, b.h, func(dst []byte, w, h int) { b.rgb(dst, w, h, a.cfg.jobs) }, f)
	}

	tryShow := func() {
		if a.useTiles.Load() {
			tilesShow()
			return
		}
		// Pace: at most one frame a display interval. A frame that comes
		// sooner waits for the tick, and a newer one replaces it.
		if iv := a.cfg.pace.Microseconds(); iv > 0 {
			if wait := lastWrite + iv - monoUS(); wait > 1000 {
				if pace == nil {
					pace = time.After(time.Duration(wait) * time.Microsecond)
				}
				return
			}
		}
		if a.mode == modeShm && a.replies && pending >= a.cfg.inflight {
			// The terminal has not answered; drop answers older than a second.
			if len(pendAt) > 0 && monoUS()-pendAt[0] > 1_000_000 {
				pendAt = pendAt[1:]
				pending--
			} else {
				return
			}
		}
		a.decMu.Lock()
		b, f := a.latest, a.latestF
		a.latest, a.latestF = nil, nil
		a.decMu.Unlock()
		if b == nil {
			return
		}
		if isBlack(b) {
			if blackSince.IsZero() {
				blackSince = time.Now()
				blackCheck = time.After(time.Second)
			}
		} else {
			blackSince, blackCheck = time.Time{}, nil
			if noDisplay {
				noDisplay, havePl = false, false
				_, _ = a.out.WriteString("\x1b[2J")
				status()
			}
		}
		if noDisplay {
			a.free <- b
			return
		}
		if refined && !visiblyChanged(baseline, b) {
			// A refinement frame of the engine: the lossless picture on
			// screen is better. Keep it.
			a.free <- b
			return
		}
		refined = false
		showYUV(b, f)
		lastWrite = monoUS()
		if last != nil {
			a.free <- last
		}
		last, lastF = b, f
		still = time.After(a.cfg.refineAfter)
	}

	exit := 0
	refineCh := make(chan stillPicture, 1)
	var resized time.Time
	var sizeCheck <-chan time.Time
	for {
		select {
		case <-a.ctx.Done():
			goto out
		case <-done:
			goto out
		case s := <-sig:
			if s == syscall.SIGWINCH {
				// Ask the cell size again (a font change moves it), then draw
				// the last frame at the new size at once.
				_, _ = a.out.WriteString("\x1b[16t")
				g = a.geom()
				havePl = false
				status()
				if last != nil && !a.useTiles.Load() {
					showYUV(last, lastF)
				}
				if a.useTiles.Load() {
					a.tb.mu.Lock()
					a.tb.all = true
					a.tb.mu.Unlock()
					havePl = false
					tilesShow()
				}
				refined = false
				still = time.After(a.cfg.refineAfter)
				resized = time.Now()
				if a.useTiles.Load() && a.tilesScale.Load() {
					sizeCheck = time.After(300 * time.Millisecond)
				}
				continue
			}
			goto out
		case <-noReplyCheck:
			if a.replies && !firstOK && writtenN > 0 {
				a.replies = false // the terminal does not answer: no flow control
				pending, pendAt = 0, nil
			}
		case r := <-okCh:
			if r.id != imgID {
				continue
			}
			if r.msg != "OK" && a.mode == modeShm {
				a.setNotice("The terminal refused a frame (" + r.msg + "). Sending frames over the terminal.")
				a.mode = modeEdits
				a.replies = false
			}
			firstOK = true
			if pending > 0 {
				pending--
				pendAt = pendAt[1:]
			}
			tryShow()
		case <-a.wake:
			tryShow()
		case <-pace:
			pace = nil
			tryShow()
		case <-a.tilesWake:
			tilesShow()
		case <-sizeCheck:
			// The pane settled at a new size: ask the engine for it.
			sizeCheck = nil
			if s, _ := a.sentSize.Load().(string); s != a.paneSize() {
				a.requestRedial()
			}
		case <-tilesPace:
			tilesPace = nil
			tilesShow()
		case <-blackCheck:
			blackCheck = nil
			if !blackSince.IsZero() && !noDisplay {
				// One line in place of a black picture.
				noDisplay = true
				msg := a.noDisplayText()
				fmt.Fprintf(a.out, "\x1b[?2026h\x1b_Ga=d,d=I,i=%d,q=2\x1b\\\x1b[2J\x1b[%d;%dH%s\x1b[?2026l",
					imgID, max(g.rows/2, 1), max((g.cols-len(msg))/2+1, 1), msg)
				status()
			}
		case <-still:
			still = nil
			if a.cfg.refineAfter > 0 && !refining && !refined && last != nil {
				refining = true
				go a.fetchStill(shownN, refineCh)
			}
		case r := <-refineCh:
			refining = false
			if r.err != nil {
				if r.err != errStillBusy {
					a.setNotice("The lossless picture failed: " + r.err.Error())
				}
				continue
			}
			if r.shownN != shownN || last == nil || r.w != last.w || r.h != last.h {
				continue // a newer frame came while the screenshot loaded
			}
			show(r.w, r.h, func(dst []byte, w, h int) { r.scale(dst, w, h, a.cfg.jobs) }, nil)
			refined = true
			baseline = blockMeans(last)
			a.refines.Add(1)
			status()
		case <-a.cellChanged:
			if ng := a.geom(); ng != g {
				g = ng
				havePl = false
				status()
				if last != nil {
					showYUV(last, lastF)
				}
			}
			_ = resized
		case <-tick.C:
			c := cpuUS()
			cpuPct = float64(c-lastCPU) / 10_000
			lastCPU = c
			fps = float64(cur.shown)
			latP50 = pctl(cur.lat, .5)
			estLat = cur.estLat
			if statsF != nil {
				line, _ := json.Marshal(map[string]any{
					"t": time.Now().Format(time.RFC3339), "mode": a.mode.String(), "tiles": a.useTiles.Load(), "control": a.haveCtl.Load(),
					"fps": fps, "recv_fps": a.recvN.Swap(0), "lat_p50_ms": latP50, "lat_p99_ms": pctl(cur.lat, .99),
					"lat_estimated": estLat, "rtt_ms": float64(a.rttUS.Load()) / 1000, "input_ms": float64(a.inRTTUS.Load()) / 1000,
					"kb_s": float64(cur.bytes) / 1024, "wire_kb_s": float64(a.wireBytes.Swap(0)) / 1024, "cpu_pct": cpuPct, "desk": fmt.Sprintf("%dx%d", a.deskW.Load(), a.deskH.Load()),
					"image": fmt.Sprintf("%dx%d", pl.w, pl.h), "decode_errors": a.decErrs.Load(), "control_dials": a.controlDials.Load(), "refines": a.refines.Load(), "tiles_apply_max_ms": float64(a.tilesApplyMax.Swap(0)) / 1000, "tiles_scale_max_ms": float64(a.tilesScaleMax.Swap(0)) / 1000, "tiles_gap_max_ms": float64(a.tilesGapMax.Swap(0)) / 1000,
				})
				_, _ = statsF.Write(append(line, '\n'))
			} else {
				a.recvN.Store(0)
			}
			cur = second{}
			status()
		}
	}
out:
	if a.cfg.duration > 0 {
		secs := float64(monoUS()-startUS-2_000_000) / 1e6
		total := float64(cpuUS()) / 1e6
		sum := fmt.Sprintf("tuios-rd: mode %s, desktop %dx%d, image %dx%d, %.1f fps shown, latency p50 %.1f ms p99 %.1f ms, %.0f KB/s to the terminal, CPU %.0f%% over the run\n",
			a.mode, a.deskW.Load(), a.deskH.Load(), pl.w, pl.h, float64(allShown)/secs, pctl(allLat, .5), pctl(allLat, .99), float64(allBytes)/secs/1024, total/(secs+2)*100)
		if a.cfg.report != "" {
			if f, err := os.OpenFile(a.cfg.report, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.WriteString(sum)
				f.Close()
			}
		}
		defer fmt.Print(sum)
	}
	return exit
}

// latency is capture to written. When the engine's clock is not this
// machine's (a remote desktop), it is the time above the fastest frame seen
// plus half the network round trip, and est is true.
func (a *app) latency(f *frame, now int64) (int64, bool) {
	raw := now - f.capUS
	mr := a.minRecv.Load()
	if mr >= 0 && mr < 200_000 {
		return raw, false
	}
	return raw - mr + a.rttUS.Load()/2, true
}

func (a *app) drawStatus(g cellGeom, fps, lat float64, est bool, cpu float64, lossless bool) {
	var who string
	switch {
	case a.haveCtl.Load():
		who = "You have control"
	case a.control.Load():
		who = "Taking control"
	default:
		who = "Shared with agents"
	}
	parts := []string{who}
	if m, _ := a.connMsg.Load().(string); m != "" {
		parts = []string{m}
	} else {
		switch {
		case lossless:
			// The engine sends frames only when the screen changes.
			parts = append(parts, "still, lossless")
		case fps < 1:
			parts = append(parts, "still")
		default:
			parts = append(parts, fmt.Sprintf("%.0f fps", fps))
			l := fmt.Sprintf("%.0f ms", lat)
			if est {
				l = "~" + l
			}
			parts = append(parts, l)
		}
		codec := "h264"
		if a.useTiles.Load() {
			codec = "tiles"
		}
		parts = append(parts, fmt.Sprintf("CPU %.0f%%", cpu), codec, a.mode.String())
		if d := a.displayLabel(); d != "" {
			parts = append(parts, d)
		}
	}
	a.noticeMu.Lock()
	notice := a.notice
	if time.Since(a.noticeAt) > 5*time.Second {
		notice = ""
	}
	a.noticeMu.Unlock()
	if notice != "" {
		parts = append(parts, notice)
	} else {
		give := "take control"
		if a.control.Load() {
			give = "give control back"
		}
		parts = append(parts, a.cfg.toggle.String()+" "+give, a.cfg.quit.String()+" quit")
	}
	line := " " + strings.Join(parts, " · ") + " "
	if n := utf8.RuneCountInString(line); n > g.cols {
		r := []rune(line)
		line = string(r[:max(g.cols, 0)])
	} else {
		line += strings.Repeat(" ", g.cols-n)
	}
	// One synchronized update, so the terminal never shows half a line.
	fmt.Fprintf(a.out, "\x1b[?2026h\x1b7\x1b[%d;1H\x1b[7m%s\x1b[0m\x1b8\x1b[?2026l", g.rows, line)
}
