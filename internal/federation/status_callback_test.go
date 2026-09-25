package federation

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// statusLog records what OnStatus reported, per host.
type statusLog struct {
	mu   sync.Mutex
	seen map[string][]Status
}

func (s *statusLog) record(host string, st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string][]Status{}
	}
	s.seen[host] = append(s.seen[host], st)
}

func (s *statusLog) of(host string) []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Status(nil), s.seen[host]...)
}

// TestOnStatusReportsAChangeOnce is the other half: a host that fails the same
// way on every redial is one change, not one call per backoff period, so a
// subscriber is not woken for nothing.
func TestOnStatusReportsAChangeOnce(t *testing.T) {
	var log statusLog
	var dials sync.WaitGroup
	dials.Add(5)
	var once sync.Map
	count := 0
	var mu sync.Mutex
	opts := testOptions(func(context.Context, Host) (Transport, error) {
		mu.Lock()
		count++
		n := count
		mu.Unlock()
		if n <= 5 {
			if _, loaded := once.LoadOrStore(n, true); !loaded {
				dials.Done()
			}
		}
		return nil, errors.New("connect: connection refused")
	})
	opts.OnStatus = log.record
	managerFor(t, opts, Host{Name: "dead", Addr: "unused"})
	dials.Wait()
	got := log.of("dead")
	if len(got) != 1 || got[0] != StatusUnreachable {
		t.Fatalf("five failed dials reported %v, want exactly one unreachable", got)
	}
}
