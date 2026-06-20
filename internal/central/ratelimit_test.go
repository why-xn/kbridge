package central

import (
	"sync"
	"testing"
)

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter(0, 3) // 0 rps refill, burst 3 → 3 then deny
	for i := 0; i < 3; i++ {
		if !l.allow("ip|a@x") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if l.allow("ip|a@x") {
		t.Fatal("4th request should be denied")
	}
	if !l.allow("ip|b@x") {
		t.Fatal("distinct key must have its own bucket")
	}
}

func TestLoginLimiterRace(t *testing.T) {
	l := newLoginLimiter(100, 100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.allow("ip|a@x") }()
	}
	wg.Wait()
}
