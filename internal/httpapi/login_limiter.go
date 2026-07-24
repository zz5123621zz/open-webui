package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginAttemptWindow = 15 * time.Minute
	loginAttemptLimit  = 8
)

type loginAttempt struct {
	count       int
	windowStart time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt)}
}

func (l *loginLimiter) keys(r *http.Request, username string) (ipKey, usernameKey string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(forwarded) != nil {
		host = forwarded
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return "ip:" + host, "username:" + hex.EncodeToString(sum[:12])
}

func (l *loginLimiter) allow(keys []string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var longestRetry time.Duration
	for _, key := range keys {
		attempt := l.attempts[key]
		if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= loginAttemptWindow {
			delete(l.attempts, key)
			continue
		}
		if attempt.count >= loginAttemptLimit {
			retry := loginAttemptWindow - now.Sub(attempt.windowStart)
			if retry > longestRetry {
				longestRetry = retry
			}
		}
	}
	return longestRetry == 0, longestRetry
}

func (l *loginLimiter) recordFailure(keys []string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		attempt := l.attempts[key]
		if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= loginAttemptWindow {
			attempt = loginAttempt{windowStart: now}
		}
		attempt.count++
		l.attempts[key] = attempt
	}
	if len(l.attempts) > 2048 {
		for currentKey, current := range l.attempts {
			if now.Sub(current.windowStart) >= loginAttemptWindow {
				delete(l.attempts, currentKey)
			}
		}
	}
}

func (l *loginLimiter) resetUsername(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func writeLoginRateLimit(w http.ResponseWriter, retry time.Duration) {
	seconds := max(1, int(retry.Round(time.Second)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "login_rate_limited", "Too many login attempts. Try again later.")
}
