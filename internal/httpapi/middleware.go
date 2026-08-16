package httpapi

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func contextWithTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cfg.SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Please sign in.")
			return
		}
		session, err := s.store.SessionByToken(r.Context(), cookie.Value)
		if err != nil {
			if err != store.ErrNotFound {
				s.logger.Error("session lookup failed", "error", err)
			}
			s.clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, "authentication_required", "Please sign in.")
			return
		}
		if metadata, ok := requestMetadataFromContext(r.Context()); ok {
			metadata.userIDHash = hashIdentifier(session.User.ID)
		}
		next.ServeHTTP(w, r.WithContext(withSession(r.Context(), session)))
	})
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cfg.SessionCookieName)
		provided := r.Header.Get("X-CSRF-Token")
		if err != nil || provided == "" {
			writeError(w, http.StatusForbidden, "csrf_failed", "Request verification failed.")
			return
		}
		expected := s.csrfToken(cookie.Value)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			writeError(w, http.StatusForbidden, "csrf_failed", "Request verification failed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) administrator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok || session.User.Role != "admin" {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		parsed, err := url.Parse(origin)
		if origin == "" || err != nil ||
			!strings.EqualFold(parsed.Scheme, s.cfg.BaseURL.Scheme) ||
			!strings.EqualFold(parsed.Host, s.cfg.BaseURL.Host) {
			writeError(w, http.StatusForbidden, "origin_not_allowed", "Request origin is not allowed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webSocketScheme := "ws://"
		if s.cfg.BaseURL.Scheme == "https" {
			webSocketScheme = "wss://"
		}
		webSocketSource := webSocketScheme + s.cfg.BaseURL.Host
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set(
			"Permissions-Policy",
			"camera=(), microphone=(self), geolocation=()",
		)
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self' "+webSocketSource+"; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := validRequestID(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID, _ = ids.New()
		}
		metadata := &requestMetadata{requestID: requestID}
		r = r.WithContext(withRequestMetadata(r.Context(), metadata))
		w.Header().Set("X-Request-ID", requestID)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
			slog.String("request_id", metadata.requestID),
			slog.String("user_id_hash", metadata.userIDHash),
			slog.String("method", r.Method),
			slog.String("route", r.Pattern),
			slog.Int("status", recorder.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	})
}

func hashIdentifier(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func validRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || current == '-' || current == '_' || current == '.' {
			continue
		}
		return ""
	}
	return value
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
