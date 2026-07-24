package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	archived := r.URL.Query().Get("archived") == "true"
	conversations, err := s.store.ListConversationsByArchive(r.Context(), session.User.ID, 100, archived)
	if err != nil {
		s.internalError(w, "list conversations", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
}

type createConversationRequest struct {
	Title           string `json:"title"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	var request createConversationRequest
	if !readJSON(w, r, &request) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	catalog, err := s.models.Models(r.Context())
	if err != nil {
		s.providerCatalogError(w, err)
		return
	}

	modelID := strings.TrimSpace(request.Model)
	if modelID == "" {
		modelID = strings.TrimSpace(session.User.PreferredModel)
	}
	model, ok := s.models.FindSelectable(catalog, modelID)
	if !ok {
		modelID = strings.TrimSpace(s.cfg.Provider.DefaultModel)
		model, ok = s.models.FindSelectable(catalog, modelID)
	}
	if !ok {
		for _, candidate := range catalog.Models {
			if candidate.Selectable {
				model, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no_model_available", "No chat model is currently available.")
		return
	}

	effort := normalizeRequestedEffort(request.ReasoningEffort, s.cfg.Provider.DefaultReasoningEffort)
	if !provider.SupportsEffort(model, effort) {
		writeError(w, http.StatusBadRequest, "reasoning_effort_unsupported", "The selected model does not support this reasoning effort.")
		return
	}
	conversation, err := s.store.CreateConversation(r.Context(), session.User.ID, request.Title, model.ID, effort)
	if err != nil {
		s.internalError(w, "create conversation", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": conversation})
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	conversation, err := s.store.ConversationByID(r.Context(), session.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get conversation", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversation})
}

type updateConversationRequest struct {
	Title           *string `json:"title"`
	Model           *string `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
	Archived        *bool   `json:"archived"`
}

func (s *Server) updateConversation(w http.ResponseWriter, r *http.Request) {
	var request updateConversationRequest
	if !readJSON(w, r, &request) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	current, err := s.store.OwnedConversationByID(r.Context(), session.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get conversation for update", err)
		return
	}
	if s.jobs.Busy(current.ID) &&
		(request.Model != nil || request.ReasoningEffort != nil || request.Archived != nil) {
		writeError(w, http.StatusConflict, "conversation_busy", "This conversation is currently generating a response.")
		return
	}
	if request.Archived != nil && request.Title == nil && request.Model == nil && request.ReasoningEffort == nil {
		conversation, archiveErr := s.store.SetConversationArchived(
			r.Context(), session.User.ID, current.ID, *request.Archived,
		)
		if archiveErr != nil {
			s.internalError(w, "set conversation archive state", archiveErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"conversation": conversation, "reasoningEffortReset": false,
		})
		return
	}

	title := current.Title
	if request.Title != nil {
		title = *request.Title
	}
	modelID := current.Model
	if request.Model != nil {
		modelID = strings.TrimSpace(*request.Model)
	}
	effort := current.ReasoningEffort
	if request.ReasoningEffort != nil {
		effort = normalizeRequestedEffort(*request.ReasoningEffort, "auto")
	}

	catalog, err := s.models.Models(r.Context())
	if err != nil {
		s.providerCatalogError(w, err)
		return
	}
	model, ok := s.models.FindSelectable(catalog, modelID)
	if !ok {
		writeError(w, http.StatusBadRequest, "provider_model_unavailable", "The selected model is not available.")
		return
	}
	reset := false
	if !provider.SupportsEffort(model, effort) {
		if request.ReasoningEffort != nil {
			writeError(w, http.StatusBadRequest, "reasoning_effort_unsupported", "The selected model does not support this reasoning effort.")
			return
		}
		effort = "auto"
		reset = true
	}
	conversation, err := s.store.UpdateConversation(r.Context(), session.User.ID, current.ID, title, model.ID, effort)
	if err != nil {
		s.internalError(w, "update conversation", err)
		return
	}
	if request.Archived != nil {
		conversation, err = s.store.SetConversationArchived(r.Context(), session.User.ID, current.ID, *request.Archived)
		if err != nil {
			s.internalError(w, "set conversation archive state", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation":         conversation,
		"reasoningEffortReset": reset,
	})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	conversationID := r.PathValue("id")
	if s.jobs.Busy(conversationID) {
		writeError(w, http.StatusConflict, "conversation_busy", "This conversation is currently generating a response.")
		return
	}
	paths, err := s.store.DeleteConversation(r.Context(), session.User.ID, conversationID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "delete conversation", err)
		return
	}
	for _, storagePath := range paths {
		fullPath := filepath.Clean(filepath.Join(s.cfg.DataDir, storagePath))
		dataRoot := filepath.Clean(s.cfg.DataDir) + string(os.PathSeparator)
		if !strings.HasPrefix(fullPath, dataRoot) {
			s.logger.Warn("skip invalid conversation attachment path")
			continue
		}
		if removeErr := os.Remove(fullPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			s.logger.Warn("delete conversation attachment failed", "error", removeErr)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func normalizeRequestedEffort(requested, fallback string) string {
	value := strings.ToLower(strings.TrimSpace(requested))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(fallback))
	}
	if value == "" {
		return "auto"
	}
	return value
}
