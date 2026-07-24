package httpapi

import (
	"context"
	"net/http"
)

func (s *Server) registerResponse(messageID, userID string, cancel context.CancelFunc) {
	s.activeMu.Lock()
	s.active[messageID] = activeResponse{userID: userID, cancel: cancel}
	s.activeMu.Unlock()
}

func (s *Server) unregisterResponse(messageID string) {
	s.activeMu.Lock()
	delete(s.active, messageID)
	s.activeMu.Unlock()
}

func (s *Server) cancelResponse(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	messageID := r.PathValue("id")
	s.activeMu.Lock()
	response, exists := s.active[messageID]
	if exists && response.userID == session.User.ID {
		response.cancel()
	}
	s.activeMu.Unlock()
	if !exists || response.userID != session.User.ID {
		writeError(w, http.StatusNotFound, "not_found", "Active response not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
