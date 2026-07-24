package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if !readJSON(w, r, &request) {
		return
	}
	now := time.Now()
	ipKey, usernameKey := s.logins.keys(r, request.Username)
	loginKeys := []string{ipKey, usernameKey}
	if allowed, retry := s.logins.allow(loginKeys, now); !allowed {
		writeLoginRateLimit(w, retry)
		return
	}

	user, err := s.store.UserByUsername(r.Context(), request.Username)
	if err != nil {
		s.logins.recordFailure(loginKeys, now)
		s.invalidCredentials(w)
		return
	}
	ok, verifyErr := auth.VerifyPassword(user.PasswordHash, request.Password)
	if verifyErr != nil {
		s.logger.Error("password hash verification failed", "user_id", user.ID, "error", verifyErr)
		s.logins.recordFailure(loginKeys, now)
		s.invalidCredentials(w)
		return
	}
	if !ok || user.Status != "active" {
		s.logins.recordFailure(loginKeys, now)
		s.invalidCredentials(w)
		return
	}
	s.logins.resetUsername(usernameKey)

	token, err := ids.NewSecret()
	if err != nil {
		s.internalError(w, "generate session token", err)
		return
	}
	session, err := s.store.CreateSession(r.Context(), user.ID, token, r.UserAgent(), s.cfg.SessionTTL)
	if err != nil {
		s.internalError(w, "create session", err)
		return
	}
	s.setSessionCookie(w, token, session.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      session.User,
		"csrfToken": s.csrfToken(token),
	})
}

func (s *Server) invalidCredentials(w http.ResponseWriter) {
	time.Sleep(150 * time.Millisecond)
	writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password.")
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	cookie, _ := r.Cookie(s.cfg.SessionCookieName)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      session.User,
		"csrfToken": s.csrfToken(cookie.Value),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.DeleteSession(r.Context(), session.ID, session.User.ID); err != nil {
		s.internalError(w, "delete session", err)
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.DeleteSessions(r.Context(), session.User.ID); err != nil {
		s.internalError(w, "delete all sessions", err)
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var request changePasswordRequest
	if !readJSON(w, r, &request) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	ok, err := auth.VerifyPassword(session.User.PasswordHash, request.CurrentPassword)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid_current_password", "Current password is incorrect.")
		return
	}
	hash, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	if err := s.store.UpdatePassword(r.Context(), session.User.ID, hash); err != nil {
		s.internalError(w, "update password", err)
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cfg.SessionCookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
		Expires: expiresAt, MaxAge: int(time.Until(expiresAt).Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cfg.SessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
		Expires: time.Unix(1, 0), MaxAge: -1,
	})
}

func (s *Server) internalError(w http.ResponseWriter, operation string, err error) {
	if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error(operation, "error", err)
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
}
