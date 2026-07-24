package httpapi

import (
	"errors"
	"net/http"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) listContextCheckpoints(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	conversation, err := s.readableConversation(
		r.Context(), session, r.PathValue("id"), true,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "authorize context checkpoint list", err)
		return
	}
	checkpoints, err := s.store.ListCheckpoints(
		r.Context(), conversation.UserID, conversation.ID,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "list context checkpoints", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoints": checkpoints})
}
