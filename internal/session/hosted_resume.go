package session

import (
	"net"
	"time"
)

const hostedRingSize = 64 * 1024

func (hp *hostedPane) start() {
	hp.startOnce.Do(func() {
		go func() {
			if hp.cmd != nil {
				_ = hp.cmd.Wait()
			}
			hp.close()
		}()
		go hp.pump()
	})
}

func (hp *hostedPane) pump() {
	buf := make([]byte, 32*1024)
	out := hp.deliver
	var filter *kittyLinkFilter
	if hp.kittyLink != nil {
		filter = &kittyLinkFilter{deliver: hp.deliver, handle: hp.kittyFileCommand, refuse: hp.kittyRefuseParts}
		out = filter.feed
	}
	for {
		n, err := hp.pty.Read(buf)
		if n > 0 {
			out(buf[:n])
		}
		if err != nil {
			if filter != nil {
				filter.flush()
			}
			hp.close()
			return
		}
	}
}

func (hp *hostedPane) deliver(chunk []byte) {
	hp.out.Lock()
	hp.ring = append(hp.ring, chunk...)
	if over := len(hp.ring) - hostedRingSize; over > 0 {
		hp.ring = append(hp.ring[:0], hp.ring[over:]...)
	}
	hp.outSeq += int64(len(chunk))
	hp.connMu.Lock()
	c := hp.conn
	hp.connMu.Unlock()
	var err error
	if c != nil {
		_, err = c.Write(chunk)
	}
	hp.out.Unlock()
	if err != nil {
		hp.detach(c)
	}
}

func (hp *hostedPane) replayFromLocked(from int64) ([]byte, bool) {
	start := hp.outSeq - int64(len(hp.ring))
	if from < start || from > hp.outSeq {
		return hp.ring, from != hp.outSeq
	}
	return hp.ring[from-start:], false
}

func (hp *hostedPane) resumeGap(offset int64) bool {
	hp.out.Lock()
	defer hp.out.Unlock()
	_, gap := hp.replayFromLocked(offset)
	return gap
}

func (hp *hostedPane) attach(c net.Conn, from int64) bool {
	hp.connMu.Lock()
	if hp.closed {
		hp.connMu.Unlock()
		return false
	}
	old := hp.conn
	hp.conn = nil
	if hp.orphan != nil {
		hp.orphan.Stop()
		hp.orphan = nil
	}
	hp.connMu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	hp.out.Lock()
	replay, _ := hp.replayFromLocked(from)
	if len(replay) > 0 {
		if _, err := c.Write(replay); err != nil {
			hp.out.Unlock()
			hp.detach(c)
			return true
		}
	}
	hp.connMu.Lock()
	ok := !hp.closed
	if ok {
		hp.conn = c
		if hp.orphan != nil {
			hp.orphan.Stop()
			hp.orphan = nil
		}
	}
	hp.connMu.Unlock()
	hp.out.Unlock()
	if !ok {
		return false
	}
	hp.start()
	return true
}

func (hp *hostedPane) detach(c net.Conn) {
	_ = c.Close()
	hp.connMu.Lock()
	if hp.conn != c && hp.conn != nil {
		hp.connMu.Unlock()
		return
	}
	hp.conn = nil
	if hp.closed {
		hp.connMu.Unlock()
		return
	}
	if hp.grace <= 0 || hp.ending {
		hp.connMu.Unlock()
		hp.close()
		return
	}
	if hp.orphan == nil {
		grace := hp.grace
		hp.orphan = time.AfterFunc(grace, func() {
			hp.connMu.Lock()
			still := hp.conn == nil && !hp.closed
			hp.connMu.Unlock()
			if still {
				LogBasic("Pane %s ended: its owner did not come back within %s", shortID(hp.id), grace)
				hp.close()
			}
		})
		LogBasic("Pane %s lost its owner's connection and waits %s to be reattached", shortID(hp.id), grace)
	}
	hp.connMu.Unlock()
}

func (hp *hostedPane) ended() bool {
	hp.connMu.Lock()
	defer hp.connMu.Unlock()
	return hp.closed
}

func (hp *hostedPane) end() {
	hp.connMu.Lock()
	hp.ending = true
	hp.connMu.Unlock()
	hp.close()
}
