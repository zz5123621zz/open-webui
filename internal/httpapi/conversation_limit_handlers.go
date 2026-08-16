package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type conversationLimitSettingResponse struct {
	MaxActiveConversations int    `json:"maxActiveConversations"`
	Unlimited              bool   `json:"unlimited"`
	Source                 string `json:"source"`
	EnvDefault             int    `json:"envDefault"`
	UpdatedBy              string `json:"updatedBy,omitempty"`
	UpdatedAt              int64  `json:"updatedAt,omitempty"`
}

type updateConversationLimitSettingRequest struct {
	MaxActiveConversations *int `json:"maxActiveConversations"`
}

func (s *Server) effectiveMaxActiveConversations(ctx context.Context) int {
	setting, err := s.store.ConversationLimitSetting(ctx)
	if err != nil {
		return s.cfg.Lifecycle.MaxActiveConversations
	}
	return setting.MaxActive
}

func (s *Server) conversationLimitPayload(
	ctx context.Context,
) conversationLimitSettingResponse {
	payload := conversationLimitSettingResponse{
		MaxActiveConversations: s.cfg.Lifecycle.MaxActiveConversations,
		Unlimited:              s.cfg.Lifecycle.MaxActiveConversations == 0,
		Source:                 "env",
		EnvDefault:             s.cfg.Lifecycle.MaxActiveConversations,
	}
	setting, err := s.store.ConversationLimitSetting(ctx)
	if err != nil {
		return payload
	}
	payload.MaxActiveConversations = setting.MaxActive
	payload.Unlimited = setting.MaxActive == 0
	payload.Source = "admin"
	payload.UpdatedBy = setting.UpdatedBy
	payload.UpdatedAt = setting.UpdatedAt
	return payload
}

func (s *Server) getConversationLimitSetting(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"conversationLimit": s.conversationLimitPayload(r.Context()),
	})
}

func (s *Server) updateConversationLimitSetting(w http.ResponseWriter, r *http.Request) {
	var request updateConversationLimitSettingRequest
	if !readJSON(w, r, &request) {
		return
	}
	if request.MaxActiveConversations == nil {
		writeError(
			w, http.StatusBadRequest, "invalid_conversation_limit",
			"Active conversation limit must be 0 (unlimited) or a number up to 10000.",
		)
		return
	}
	session, _ := sessionFromContext(r.Context())
	_, err := s.store.SetConversationLimitSetting(
		r.Context(), session.User.ID, *request.MaxActiveConversations,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		if *request.MaxActiveConversations < 0 ||
			*request.MaxActiveConversations > store.MaxConfigurableActiveConversations {
			writeError(
				w, http.StatusBadRequest, "invalid_conversation_limit",
				"Active conversation limit must be 0 (unlimited) or a number up to 10000.",
			)
			return
		}
		s.internalError(w, "update conversation limit setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversationLimit": s.conversationLimitPayload(r.Context()),
	})
}
