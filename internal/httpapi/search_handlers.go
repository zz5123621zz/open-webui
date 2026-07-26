package httpapi

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) searchConversations(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []store.ConversationSearchResult{}})
		return
	}
	if utf8.RuneCountInString(query) > 120 {
		writeError(w, http.StatusBadRequest, "query_too_long", "The search query is too long.")
		return
	}
	var (
		results []store.ConversationSearchResult
		err     error
	)
	if session.User.Role == "admin" {
		results, err = s.store.SearchAllConversations(r.Context(), query, 20)
	} else {
		results, err = s.store.SearchConversations(r.Context(), session.User.ID, query, 20)
	}
	if err != nil {
		s.internalError(w, "search conversations", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) usageStats(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	var (
		rows []store.UsageRow
		err  error
	)
	if session.User.Role == "admin" {
		rows, err = s.store.UsageByMonthAllUsers(r.Context(), 6)
	} else {
		rows, err = s.store.UsageByMonth(r.Context(), session.User.ID, 6)
	}
	if err != nil {
		s.internalError(w, "aggregate usage", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": rows})
}
