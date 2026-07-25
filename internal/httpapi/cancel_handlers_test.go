package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestCancelResponseIsScopedToCurrentUser(t *testing.T) {
	server := &Server{active: make(map[string]activeResponse)}
	jobContext, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	server.registerResponse("assistant-1", "user-a", cancel)

	otherRequest := httptest.NewRequest(http.MethodPost, "/api/v1/responses/assistant-1/cancel", nil)
	otherRequest.SetPathValue("id", "assistant-1")
	otherRequest = otherRequest.WithContext(withSession(otherRequest.Context(), store.Session{
		User: store.User{ID: "user-b"},
	}))
	otherResponse := httptest.NewRecorder()
	server.cancelResponse(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-user cancel status = %d", otherResponse.Code)
	}
	select {
	case <-jobContext.Done():
		t.Fatal("cross-user request cancelled the job")
	default:
	}

	ownerRequest := httptest.NewRequest(http.MethodPost, "/api/v1/responses/assistant-1/cancel", nil)
	ownerRequest.SetPathValue("id", "assistant-1")
	ownerRequest = ownerRequest.WithContext(withSession(ownerRequest.Context(), store.Session{
		User: store.User{ID: "user-a"},
	}))
	ownerResponse := httptest.NewRecorder()
	server.cancelResponse(ownerResponse, ownerRequest)
	if ownerResponse.Code != http.StatusNoContent {
		t.Fatalf("owner cancel status = %d", ownerResponse.Code)
	}
	select {
	case <-jobContext.Done():
	default:
		t.Fatal("owner cancel did not cancel the job context")
	}
	if !errors.Is(context.Cause(jobContext), errResponseCancelled) {
		t.Fatalf("cancel cause = %v", context.Cause(jobContext))
	}
}
