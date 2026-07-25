package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) registerResponse(messageID, userID string, cancel context.CancelCauseFunc) {
	s.activeMu.Lock()
	s.active[messageID] = activeResponse{userID: userID, cancel: cancel}
	s.activeMu.Unlock()
}

func (s *Server) unregisterResponse(messageID string) {
	s.activeMu.Lock()
	delete(s.active, messageID)
	s.activeMu.Unlock()
}

func (s *Server) getResponse(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	message, err := s.store.MessageByID(
		r.Context(), session.User.ID, r.PathValue("id"),
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Response not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get response", err)
		return
	}
	if message.Role != "assistant" {
		writeError(w, http.StatusNotFound, "not_found", "Response not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": message})
}

func (s *Server) cancelResponse(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	messageID := r.PathValue("id")
	s.activeMu.Lock()
	response, exists := s.active[messageID]
	if exists && response.userID == session.User.ID {
		response.cancel(errResponseCancelled)
	}
	s.activeMu.Unlock()
	if !exists || response.userID != session.User.ID {
		writeError(w, http.StatusNotFound, "not_found", "Active response not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
