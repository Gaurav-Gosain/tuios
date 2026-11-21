package main

import (
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/apptarget"
)

type watched struct {
	*apptarget.Target
	live *current
}

func (w *watched) Apply(a fuzz.Action) error {
	if err := w.Target.Apply(a); err != nil {
		return err
	}
	w.live.capture(w.Target)
	return nil
}

func (w *watched) Close() {
	w.live.clear(w)
	w.Target.Close()
}

type current struct {
	want     atomic.Bool
	captures atomic.Int64

	mu     sync.Mutex
	frame  string
	target *watched
}

func (c *current) set(w *watched) {
	c.mu.Lock()
	c.target = w
	c.mu.Unlock()
}

func (c *current) clear(w *watched) {
	c.mu.Lock()
	if c.target == w {
		c.target = nil
	}
	c.mu.Unlock()
}

func (c *current) capture(t *apptarget.Target) {
	if !c.want.Swap(false) {
		return
	}
	c.captures.Add(1)
	f := t.Screen()
	c.mu.Lock()
	c.frame = f
	c.mu.Unlock()
}

func (c *current) screen() string {
	c.want.Store(true)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame
}
