package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

func TestOriginMiddlewareRequiresExactOrigin(t *testing.T) {
	baseURL, _ := url.Parse("https://chat.example.test")
	server := &Server{cfg: config.Config{BaseURL: baseURL}}
	next := server.origin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, testCase := range []struct {
		name   string
		origin string
		want   int
	}{
		{name: "missing", want: http.StatusForbidden},
		{name: "wrong host", origin: "https://evil.example", want: http.StatusForbidden},
		{name: "wrong scheme", origin: "http://chat.example.test", want: http.StatusForbidden},
		{name: "exact", origin: "https://chat.example.test", want: http.StatusNoContent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			response := httptest.NewRecorder()
			next.ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status = %d, want %d", response.Code, testCase.want)
			}
		})
	}
}
