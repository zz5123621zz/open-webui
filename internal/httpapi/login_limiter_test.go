package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginLimiterBoundsRepeatedAttempts(t *testing.T) {
	limiter := newLoginLimiter()
	request := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.1:12345"
	ipKey, usernameKey := limiter.keys(request, "Alice")
	keys := []string{ipKey, usernameKey}
	now := time.Now()
	for range loginAttemptLimit {
		if allowed, _ := limiter.allow(keys, now); !allowed {
			t.Fatal("attempt blocked too early")
		}
		limiter.recordFailure(keys, now)
	}
	if allowed, retry := limiter.allow(keys, now); allowed || retry <= 0 {
		t.Fatalf("allow after limit = %v, retry=%v", allowed, retry)
	}
	limiter.resetUsername(usernameKey)
	if allowed, _ := limiter.allow([]string{usernameKey}, now); !allowed {
		t.Fatal("successful login reset did not clear the username limiter")
	}
	if allowed, _ := limiter.allow([]string{ipKey}, now); allowed {
		t.Fatal("successful login unexpectedly cleared the IP limiter")
	}
}

func TestLoginLimiterBlocksRotatingUsernamesByIP(t *testing.T) {
	limiter := newLoginLimiter()
	request := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	now := time.Now()
	var ipKey string
	for index := range loginAttemptLimit {
		currentIPKey, usernameKey := limiter.keys(request, string(rune('a'+index)))
		ipKey = currentIPKey
		limiter.recordFailure([]string{currentIPKey, usernameKey}, now)
	}
	if allowed, _ := limiter.allow([]string{ipKey}, now); allowed {
		t.Fatal("rotating usernames bypassed the per-IP limit")
	}
}
