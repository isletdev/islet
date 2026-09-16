package api

import (
	"sync"
	"testing"
)

// The newest connection wins, the one it replaced is told, and a connection
// that leaves late does not evict whoever took its place.
func TestSeatGoesToTheNewestAndSaysSo(t *testing.T) {
	s := newSeats()
	displaced := []string{}
	var mu sync.Mutex
	note := func(who string) func() {
		return func() { mu.Lock(); displaced = append(displaced, who); mu.Unlock() }
	}

	releaseA := s.take("ws:1", note("a"))
	if len(displaced) != 0 {
		t.Fatal("the first connection displaced somebody")
	}
	releaseB := s.take("ws:1", note("b"))
	if len(displaced) != 1 || displaced[0] != "a" {
		t.Fatalf("displaced = %v, want [a]", displaced)
	}
	// A different session is a different seat.
	releaseC := s.take("ws:2", note("c"))
	if len(displaced) != 1 {
		t.Fatalf("taking another seat displaced %v", displaced)
	}

	// a leaves late: b keeps the seat.
	releaseA()
	s.mu.Lock()
	_, held := s.held["ws:1"]
	s.mu.Unlock()
	if !held {
		t.Fatal("a late departure evicted the connection that replaced it")
	}
	releaseB()
	releaseC()
	s.mu.Lock()
	n := len(s.held)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d seats left held", n)
	}
}
