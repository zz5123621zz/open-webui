package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestPasswordChangeAndLogoutAllRevokeSessions(t *testing.T) {
	dataDir := t.TempDir()
	dataStore, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	hash, err := auth.HashPassword("original-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateUser(context.Background(), "security-user", "Security", hash); err != nil {
		t.Fatal(err)
	}

	baseURL, _ := url.Parse("http://chat.test")
	providerURL, _ := url.Parse("http://127.0.0.1:1/v1")
	cfg := config.Config{
		BaseURL: baseURL, DataDir: dataDir, DatabasePath: filepath.Join(dataDir, "app.db"),
		AppSecret:  []byte("01234567890123456789012345678901"),
		SessionTTL: time.Hour, SessionCookieName: "owui_session",
		Provider: config.Provider{
			BaseURL: providerURL, APIKey: "test", DefaultModel: "gpt-test",
			ModelsTimeout: time.Second, RequestBodyMaxBytes: 50 << 20,
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal: 4, MaxConcurrentPerUser: 2,
			MaxQueuedPerUser: 2, QueueTimeout: time.Second,
		},
	}
	app := New(
		cfg, dataStore, provider.NewClient(cfg.Provider, "test"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	firstCookie, firstCSRF := loginTestUser(
		t, server.URL, "security-user", "original-password-123",
	)
	change := authenticatedRequest(
		t, http.MethodPut, server.URL+"/api/v1/me/password", firstCookie, firstCSRF,
		`{"currentPassword":"original-password-123","newPassword":"replacement-password-456"}`,
	)
	if change.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(change.Body)
		t.Fatalf("change password status=%d body=%s", change.StatusCode, raw)
	}
	change.Body.Close()
	if got := authenticatedRequest(t, http.MethodGet, server.URL+"/api/v1/me", firstCookie, "", ""); got.StatusCode != http.StatusUnauthorized {
		got.Body.Close()
		t.Fatalf("old session status = %d", got.StatusCode)
	} else {
		got.Body.Close()
	}

	oldLogin, _ := http.NewRequest(
		http.MethodPost, server.URL+"/api/v1/auth/login",
		strings.NewReader(`{"username":"security-user","password":"original-password-123"}`),
	)
	oldLogin.Header.Set("Origin", "http://chat.test")
	oldLogin.Header.Set("Content-Type", "application/json")
	oldResponse, err := http.DefaultClient.Do(oldLogin)
	if err != nil {
		t.Fatal(err)
	}
	if oldResponse.StatusCode != http.StatusUnauthorized {
		oldResponse.Body.Close()
		t.Fatalf("old password login status = %d", oldResponse.StatusCode)
	}
	oldResponse.Body.Close()

	secondCookie, secondCSRF := loginTestUser(
		t, server.URL, "security-user", "replacement-password-456",
	)
	thirdCookie, _ := loginTestUser(
		t, server.URL, "security-user", "replacement-password-456",
	)
	logout := authenticatedRequest(
		t, http.MethodPost, server.URL+"/api/v1/auth/logout-all",
		secondCookie, secondCSRF, "",
	)
	if logout.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(logout.Body)
		t.Fatalf("logout all status=%d body=%s", logout.StatusCode, raw)
	}
	logout.Body.Close()
	for _, cookie := range []*http.Cookie{secondCookie, thirdCookie} {
		response := authenticatedRequest(t, http.MethodGet, server.URL+"/api/v1/me", cookie, "", "")
		if response.StatusCode != http.StatusUnauthorized {
			response.Body.Close()
			t.Fatalf("revoked session status = %d", response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestSessionCookieSecurityAttributes(t *testing.T) {
	server := &Server{cfg: config.Config{
		SessionCookieName: "owui_session",
		SecureCookies:     true,
	}}
	recorder := httptest.NewRecorder()
	server.setSessionCookie(recorder, "random-session-token", time.Now().Add(time.Hour))
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode ||
		cookie.Path != "/" || cookie.MaxAge <= 0 {
		t.Fatalf("session cookie attributes = %#v", cookie)
	}
}
