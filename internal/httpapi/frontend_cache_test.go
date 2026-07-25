package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrontendCachePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		wantControl string
	}{
		{name: "index", path: "/", wantControl: "no-store, max-age=0"},
		{name: "spa fallback", path: "/chat/example", wantControl: "no-store, max-age=0"},
		{name: "hashed asset placeholder", path: "/placeholder.txt", wantControl: "public, max-age=31536000, immutable"},
	}

	server := &Server{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://chat.test"+test.path, nil)
			response := httptest.NewRecorder()

			server.serveFrontend(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Header().Get("Cache-Control"); got != test.wantControl {
				t.Fatalf("Cache-Control = %q, want %q", got, test.wantControl)
			}
		})
	}
}
