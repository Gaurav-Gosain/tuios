package main

import (
	"log"
	"os"
	"time"
)

// lookTrace logs how long each step of showing a file takes, from the click
// to the kind known, the first page drawn, the index done: GUIOS_LOOK_TRACE=1
// turns it on, for the perf harness. Off, it costs one bool check a step.
var lookTrace = os.Getenv("GUIOS_LOOK_TRACE") != ""

// mark logs step what of the look, once, with the time since it opened.
func (l *fileLook) mark(what string) {
	if !lookTrace {
		return
	}
	if l.marked == nil {
		l.marked = map[string]bool{}
	}
	if l.marked[what] {
		return
	}
	l.marked[what] = true
	log.Printf("look %s %s %.1f ms", l.name, what, float64(time.Since(l.since).Microseconds())/1000)
}
