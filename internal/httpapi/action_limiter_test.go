package httpapi

import (
	"testing"
	"time"
)

func TestActionLimiterBoundsAndResetsWindow(t *testing.T) {
	limiter := newActionLimiter()
	now := time.Now()
	for range 2 {
		if allowed, _ := limiter.allow("user:upload", 2, time.Minute, now); !allowed {
			t.Fatal("action blocked before limit")
		}
	}
	if allowed, retry := limiter.allow("user:upload", 2, time.Minute, now); allowed || retry <= 0 {
		t.Fatalf("action after limit allowed=%v retry=%v", allowed, retry)
	}
	if allowed, _ := limiter.allow("user:upload", 2, time.Minute, now.Add(time.Minute)); !allowed {
		t.Fatal("action window did not reset")
	}
}

func TestKeyedGateAllowsOnlyOneActiveOperation(t *testing.T) {
	gate := newKeyedGate()
	if !gate.acquire("user") {
		t.Fatal("first acquire failed")
	}
	if gate.acquire("user") {
		t.Fatal("second acquire unexpectedly succeeded")
	}
	gate.release("user")
	if !gate.acquire("user") {
		t.Fatal("acquire after release failed")
	}
}
