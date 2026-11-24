// Package testknob holds the switches the test suites set in a tuios they
// start: holds that let a test land between two steps, shorter timeouts, and
// faults on purpose. They all come in one value, the TUIOS_TEST_KNOBS
// variable, as comma-separated name=value pairs:
//
//	TUIOS_TEST_KNOBS=hold_nav=/tmp/x/hold,nav_captures=1
//
// No build is meant to ship with the variable set, and a process without it
// reads every knob as unset. The value is parsed once, on the first read. A
// hook on a path that runs often keeps the value it read, rather than asking
// again each time.
//
// An in-process test that starts a client and a daemon without a process of
// their own hands them knobs with Set, before either reads one.
package testknob

import (
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Env is the variable the knobs come in.
const Env = "TUIOS_TEST_KNOBS"

// Parse reads "name=value,name=value". A pair with no = is a knob set to
// "1". A later pair of one name wins.
func Parse(s string) map[string]string {
	k := map[string]string{}
	// strings.Split, not SplitSeq: the iterator adds about 300 bytes to the
	// binary, and this runs once.
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			value = "1"
		}
		k[name] = value
	}
	return k
}

// Merge adds the pairs to base, a value of the variable, and returns the
// value with both, sorted by name. A pair in pairs wins over one of the same
// name in base.
func Merge(base string, pairs ...string) string {
	k := Parse(base)
	maps.Copy(k, Parse(strings.Join(pairs, ",")))
	names := slices.Sorted(maps.Keys(k))
	for i, name := range names {
		names[i] = name + "=" + k[name]
	}
	return strings.Join(names, ",")
}

var (
	once  sync.Once
	knobs map[string]string
)

func load() { knobs = Parse(os.Getenv(Env)) }

// Set makes k the knobs of this process in place of the variable. Call it
// before anything reads a knob: a hook may keep the value it read first, and
// Set does not guard against a read at the same time.
func Set(k map[string]string) {
	once.Do(func() {})
	knobs = k
}

// String is the knob's value, "" when it is not set.
func String(name string) string {
	once.Do(load)
	return knobs[name]
}

// WaitWhile waits while the file at path exists, looking every poll, for
// at most limit. It is the hold a test lifts by removing the file.
func WaitWhile(path string, poll, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err != nil {
			return
		}
		time.Sleep(poll)
	}
}

// Millis is the knob as a count of milliseconds, 0 when it is unset, not a
// number, or below zero.
func Millis(name string) time.Duration {
	n, err := strconv.Atoi(String(name))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}

// written counts the bytes this process wrote to its terminal, for the answer
// to a settle probe (internal/app/settle_probe.go). Only a process with the
// settle_marks knob set counts.
var written atomic.Int64

// counting reports whether the settle_marks knob is set.
var counting = sync.OnceValue(func() bool { return String("settle_marks") != "" })

// Wrote adds n to the bytes this process wrote to its terminal, when the
// settle_marks knob is set. Every write to the terminal calls it: the
// client's writer, and the few lines a client writes before it starts.
func Wrote(n int) {
	if counting() {
		written.Add(int64(n))
	}
}

// Written is the number of bytes Wrote counted.
func Written() int64 { return written.Load() }
