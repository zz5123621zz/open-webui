package httpapi

import (
	"net/http"
)

func (s *Server) storageStatus(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	status, err := s.store.StorageStatus(
		r.Context(),
		session.User.ID,
		s.cfg.Lifecycle.MaxStorageBytes,
		s.cfg.Lifecycle.MaxActiveConversations,
		s.cfg.Lifecycle.MaxPinnedConversations,
		int(s.cfg.Lifecycle.RetentionTTL.Hours()/24),
	)
	if err != nil {
		s.internalError(w, "get storage status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"storage": status})
}
