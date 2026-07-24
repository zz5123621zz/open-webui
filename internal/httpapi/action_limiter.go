package httpapi

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

type actionBucket struct {
	count       int
	windowStart time.Time
}

type actionLimiter struct {
	mu      sync.Mutex
	buckets map[string]actionBucket
}

func newActionLimiter() *actionLimiter {
	return &actionLimiter{buckets: make(map[string]actionBucket)}
}

func (l *actionLimiter) allow(key string, limit int, window time.Duration, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[key]
	if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= window {
		bucket = actionBucket{windowStart: now}
	}
	if bucket.count >= limit {
		return false, window - now.Sub(bucket.windowStart)
	}
	bucket.count++
	l.buckets[key] = bucket
	if len(l.buckets) > 1024 {
		for currentKey, current := range l.buckets {
			if now.Sub(current.windowStart) >= window {
				delete(l.buckets, currentKey)
			}
		}
	}
	return true, 0
}

func (s *Server) limitAction(action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Please sign in.")
			return
		}
		allowed, retry := s.actions.allow(session.User.ID+":"+action, limit, window, time.Now())
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Round(time.Second)/time.Second))))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again shortly.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type keyedGate struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func newKeyedGate() *keyedGate {
	return &keyedGate{active: make(map[string]struct{})}
}

func (g *keyedGate) acquire(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.active[key]; exists {
		return false
	}
	g.active[key] = struct{}{}
	return true
}

func (g *keyedGate) release(key string) {
	g.mu.Lock()
	delete(g.active, key)
	g.mu.Unlock()
}
