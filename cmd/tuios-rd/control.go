//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// handleInput reads terminal events: answers go to the display loop, the
// person's input goes to the desktop.
func (a *app) handleInput(evs <-chan termEv, okCh chan<- *gfxReply) {
	var buttons uint16  // held mouse buttons, protocol bits
	superArmed := false // the --super-key prefix was pressed
	var held []record   // share mode: a drag waits here until the release
	var heldMods []uint16
	for e := range evs {
		switch {
		case e.reply != nil:
			select {
			case okCh <- e.reply:
			default:
			}
		case e.pixMode > 0:
			v := e.pixMode - 10
			a.pixelMouse.Store(v == 1 || v == 3)
		case e.da1, e.kbd >= 0, e.cellPx[0] > 0:
			if e.cellPx[0] > 0 {
				v := int64(e.cellPx[1])<<16 | int64(e.cellPx[0])
				if a.cellPx.Swap(v) != v {
					select {
					case a.cellChanged <- struct{}{}:
					default:
					}
				}
			}
		case e.mouse != nil:
			recs, flush := a.mouseRecords(e.mouse, &buttons, &heldMods, &superArmed)
			a.buttonsHeld.Store(buttons != 0)
			if a.cfg.input != "post" {
				// A socket holds buttons between records, so a drag goes live.
				a.queue(recs)
				held = nil
				continue
			}
			if buttons != 0 || len(held) > 0 {
				// POST /input releases what a batch holds when it ends, so a
				// drag goes as one batch: down, moves, up.
				held = appendCoalesced(held, recs, 240)
				if flush {
					a.queue(held)
					held = nil
				}
				continue
			}
			a.queue(recs)
		case e.paste != "":
			a.pasteText(e.paste)
		case e.key != nil:
			k := e.key
			switch {
			case a.cfg.quit.matches(k):
				a.stop()
				return
			case a.cfg.toggle.matches(k):
				a.toggleControl()
				continue
			case isSuperPrefix(k):
				if !superArmed {
					superArmed = true
					a.setNotice("Super: press a key.")
					continue
				}
				superArmed = false
			case superArmed:
				superArmed = false
				r := *k
				r.mods |= modSuper
				r.text = 0
				k = &r
			}
			a.send(keyRecords(k))
		}
	}
	a.stop()
}

func (a *app) toggleControl() {
	want := !a.control.Load()
	if !want && a.haveCtl.Load() {
		a.sendNow([]record{rec(recRelease, 0, 0, 0, 0, 0, 0)})
	}
	if want {
		a.closeControl()
	}
	a.control.Store(want)
	if want {
		a.setNotice("Taking control.")
	} else {
		a.setNotice("You gave control back. Agents can type again.")
	}
	a.requestRedial()
}

// mouseRecords turns one SGR mouse report into records. flush is true when
// the report releases the last held button.
func (a *app) mouseRecords(m *mouseEv, buttons *uint16, heldMods *[]uint16, superArmed *bool) ([]record, bool) {
	dw, dh := float64(a.dispW.Load()), float64(a.dispH.Load())
	if dw == 0 || dh == 0 {
		return nil, false
	}
	px, py := float64(m.x-1), float64(m.y-1)
	if !a.pixelMouse.Load() {
		// The terminal reports cells: use the middle of the cell.
		g := a.geom()
		px, py = (float64(m.x)-0.5)*g.cw, (float64(m.y)-0.5)*g.ch
	}
	px -= float64(a.offX.Load())
	py -= float64(a.offY.Load())
	if px < 0 || py < 0 || px >= dw || py >= dh {
		if m.release && *buttons != 0 { // released outside the picture
			px, py = min(max(px, 0), dw-1), min(max(py, 0), dh-1)
		} else {
			return nil, false
		}
	}
	X, Y := uint16(px/dw*65535), uint16(py/dh*65535)
	a.pointerX.Store(uint32(X) + 1)
	a.pointerY.Store(uint32(Y) + 1)
	var mods int
	if m.cb&4 != 0 {
		mods |= modShift
	}
	if m.cb&8 != 0 {
		mods |= modAlt
	}
	if m.cb&16 != 0 {
		mods |= modCtrl
	}
	btn := m.cb & 3
	switch {
	case m.cb&64 != 0 && m.cb&128 == 0: // wheel
		axis, d := uint16(0), int32(120)
		switch btn {
		case 0:
			d = -120
		case 2:
			axis, d = 1, -120
		case 3:
			axis = 1
		}
		return []record{rec(recMove, 0, 0, X, Y, *buttons, 0), rec(recWheel, axis, d, X, Y, *buttons, 0)}, false
	case m.cb&32 != 0: // motion
		return []record{rec(recMove, 0, 0, X, Y, *buttons, 0)}, false
	case m.cb&128 != 0 || btn == 3:
		return nil, false
	case m.release:
		code := uint16(btn + 1)
		*buttons &^= 1 << (code - 1)
		out := []record{rec(recMove, 0, 0, X, Y, *buttons, 0), rec(recBtnUp, code, 0, X, Y, *buttons, 0)}
		if *buttons == 0 {
			for i := len(*heldMods) - 1; i >= 0; i-- {
				out = append(out, rec(recKeyUp, (*heldMods)[i], 0, 0, 0, 0, 0))
			}
			*heldMods = nil
		}
		return out, *buttons == 0
	default:
		code := uint16(btn + 1)
		var out []record
		if *superArmed && *buttons == 0 {
			// The --super-key prefix holds Super for this whole drag.
			mods |= modSuper
			*superArmed = false
		}
		if *buttons == 0 {
			*heldMods = modKeys(mods)
			for _, k := range *heldMods {
				out = append(out, rec(recKeyDown, k, 0, 0, 0, 0, 0))
			}
		}
		*buttons |= 1 << (code - 1)
		out = append(out, rec(recMove, 0, 0, X, Y, *buttons, 0), rec(recBtnDown, code, 0, X, Y, *buttons, 0))
		return out, false
	}
}

// appendCoalesced appends recs. Past limit records it drops the older of
// two pointer moves in a row.
func appendCoalesced(dst, recs []record, limit int) []record {
	for _, r := range recs {
		if r[0] == recMove && len(dst) > 0 && dst[len(dst)-1][0] == recMove && len(dst) >= limit {
			dst[len(dst)-1] = r
			continue
		}
		dst = append(dst, r)
	}
	return dst
}

// send delivers records on the path of the current mode.
func (a *app) send(recs []record) {
	if len(recs) == 0 {
		return
	}
	a.queue(recs)
}

func (a *app) queue(recs []record) {
	if len(recs) == 0 {
		return
	}
	select {
	case a.inputQ <- recs:
	default:
		a.setNotice("Input is too fast for the desktop. Some input was dropped.")
	}
}

// sendNow writes records as type 3 messages on the stream that owns input.
func (a *app) sendNow(recs []record) {
	a.connMu.Lock()
	c, gen := a.conn, a.gen
	a.connMu.Unlock()
	if c == nil || gen == 0 {
		return
	}
	for _, r := range recs {
		m := make([]byte, 36)
		copy(m, "NSL1")
		m[4], m[5] = 1, 3
		binary.BigEndian.PutUint32(m[8:], gen)
		binary.BigEndian.PutUint32(m[16:], 16)
		copy(m[20:], r[:])
		_ = c.Write(a.ctx, websocket.MessageBinary, m)
	}
}

// inputLoop delivers queued records in order. Pointer moves go at most once
// a frame (the newest wins). Keys, buttons and wheel go at once.
func (a *app) inputLoop() {
	tick := time.NewTicker(4 * time.Millisecond)
	defer tick.Stop()
	var move *record
	var lastMove time.Time
	frame := 16 * time.Millisecond
	var batch []record
	flushBatch := func() {
		if len(batch) > 0 {
			a.deliver(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case <-a.ctx.Done():
			a.closeControl()
			return
		case recs := <-a.inputQ:
			for _, r := range recs {
				if r[0] == recMove {
					rr := r
					move = &rr
					continue
				}
				if move != nil { // keep the order: the move first
					batch = append(batch, *move)
					move = nil
					lastMove = time.Now()
				}
				batch = append(batch, r)
			}
			flushBatch()
			if move != nil && time.Since(lastMove) >= frame {
				a.deliver([]record{*move})
				move, lastMove = nil, time.Now()
			}
		case <-tick.C:
			if move != nil && time.Since(lastMove) >= frame {
				a.deliver([]record{*move})
				move, lastMove = nil, time.Now()
			}
			a.closeIdleControl()
		}
	}
}

// deliver sends records on the path of the moment: the stream when tuios-rd
// has control, else the /control socket, else POST /input.
func (a *app) deliver(recs []record) {
	a.inputSent.Store(monoUS())
	if a.haveCtl.Load() {
		a.sendNow(recs)
		return
	}
	if a.cfg.input != "post" && a.controlSend(recs) {
		return
	}
	for len(recs) > 0 {
		n := min(len(recs), 256)
		a.post(recs[:n])
		recs = recs[n:]
	}
}

// ---- the /control socket ----

type controlSock struct {
	mu       sync.Mutex
	c        *websocket.Conn
	gen      uint32 // from the control-ready message; every request carries it
	req      uint32
	lastUse  time.Time
	busyTill time.Time
	sent     map[uint32]int64 // request id -> send time, for the round trip
}

func (a *app) controlSend(recs []record) bool {
	cs := &a.ctl
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if time.Now().Before(cs.busyTill) {
		return false
	}
	if cs.c == nil {
		c, gen, err := a.dialControl()
		if err != nil {
			cs.busyTill = time.Now().Add(2 * time.Second)
			if strings.Contains(err.Error(), "busy") {
				a.setNotice("Another viewer or an agent has control, so your input does not reach the desktop.")
			}
			return false
		}
		cs.c, cs.gen = c, gen
		cs.sent = map[uint32]int64{}
	}
	cs.lastUse = time.Now()
	for _, r := range recs {
		cs.req++
		if cs.req == 0 {
			cs.req = 1
		}
		m := make([]byte, 36)
		copy(m, "NSL1")
		m[4], m[5] = 1, 3
		binary.BigEndian.PutUint32(m[8:], cs.gen)
		binary.BigEndian.PutUint32(m[12:], cs.req)
		binary.BigEndian.PutUint32(m[16:], 16)
		copy(m[20:], r[:])
		if len(cs.sent) < 64 {
			cs.sent[cs.req] = monoUS()
		}
		wctx, cancel := context.WithTimeout(a.ctx, time.Second)
		err := cs.c.Write(wctx, websocket.MessageBinary, m)
		cancel()
		if err != nil {
			_ = cs.c.CloseNow()
			cs.c = nil
			return false
		}
	}
	return true
}

// dialControl opens /control and waits for its ready message, which gives
// the generation for the requests.
func (a *app) dialControl() (*websocket.Conn, uint32, error) {
	protos := []string{"nsl1"}
	if a.cfg.token != "" {
		protos = append(protos, "bearer."+a.cfg.token)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	u := strings.Replace(a.cfg.base, "http", "ws", 1) + "/control" + a.outputQuery("?")
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{Subprotocols: protos})
	if err != nil {
		return nil, 0, err
	}
	_, m, err := c.Read(ctx)
	if err != nil {
		_ = c.CloseNow()
		if websocket.CloseStatus(err) == 4003 {
			return nil, 0, errors.New("busy")
		}
		return nil, 0, err
	}
	if len(m) < 20 || m[5] != 4 {
		_ = c.CloseNow()
		return nil, 0, errors.New("no control-ready message")
	}
	go a.readControl(c)
	a.controlDials.Add(1)
	return c, binary.BigEndian.Uint32(m[8:]), nil
}

// readControl reads the answers to input requests.
func (a *app) readControl(c *websocket.Conn) {
	for {
		_, m, err := c.Read(a.ctx)
		if err != nil {
			a.ctl.mu.Lock()
			if a.ctl.c == c {
				a.ctl.c = nil
			}
			a.ctl.mu.Unlock()
			if websocket.CloseStatus(err) == 4001 {
				a.setNotice("Another viewer took control.")
			}
			return
		}
		if len(m) >= 21 && m[5] == 5 {
			id := binary.BigEndian.Uint32(m[12:])
			a.ctl.mu.Lock()
			if t, ok := a.ctl.sent[id]; ok {
				a.inRTTUS.Store(monoUS() - t)
				delete(a.ctl.sent, id)
			}
			a.ctl.mu.Unlock()
			if m[20] != 0 {
				a.setNotice("The desktop refused an input record.")
			}
		}
	}
}

// closeIdleControl gives input back after 2 s without input, so agents can
// type again. It keeps the socket while a mouse button is down.
func (a *app) closeIdleControl() {
	cs := &a.ctl
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.c != nil && time.Since(cs.lastUse) > 2*time.Second && !a.buttonsHeld.Load() {
		cs.c.Close(websocket.StatusNormalClosure, "")
		cs.c = nil
	}
}

func (a *app) closeControl() {
	cs := &a.ctl
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.c != nil {
		cs.c.Close(websocket.StatusNormalClosure, "")
		cs.c = nil
	}
}

func (a *app) post(recs []record) {
	body := make([]byte, 0, len(recs)*16)
	for _, r := range recs {
		body = append(body, r[:]...)
	}
	req, _ := http.NewRequestWithContext(a.ctx, "POST", a.cfg.base+"/input"+a.outputQuery("?"), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	if a.cfg.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.token)
	}
	t0 := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		if a.ctx.Err() == nil {
			a.setNotice("Input failed: " + shortErr(err))
		}
		return
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	a.inRTTUS.Store(time.Since(t0).Microseconds())
	if resp.StatusCode != 200 {
		s := strings.TrimSpace(string(msg))
		if strings.Contains(s, "owner") || strings.Contains(s, "busy") || resp.StatusCode == 409 {
			a.setNotice("Another viewer has control, so your input does not reach the desktop.")
			return
		}
		a.setNotice("The desktop refused input: " + resp.Status + " " + s)
	}
}
