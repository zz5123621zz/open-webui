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
	var conversations []store.Conversation
	var err error
	if session.User.Role == "admin" {
		conversations, err = s.store.ListAllConversationsByArchive(r.Context(), 200, archived)
	} else {
		conversations, err = s.store.ListConversationsByArchive(
			r.Context(), session.User.ID, 100, archived,
		)
	}
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
		if strings.TrimSpace(request.ReasoningEffort) == "" {
			effort = fallbackReasoningEffort(model, s.cfg.Provider.DefaultReasoningEffort)
		}
	}
	if !provider.SupportsEffort(model, effort) {
		writeError(w, http.StatusBadRequest, "reasoning_effort_unsupported", "The selected model does not support this reasoning effort.")
		return
	}
	conversation, err := s.store.CreateConversationWithLimit(
		r.Context(),
		session.User.ID,
		request.Title,
		model.ID,
		effort,
		s.cfg.Lifecycle.MaxActiveConversations,
	)
	if errors.Is(err, store.ErrConversationLimit) {
		writeError(
			w,
			http.StatusConflict,
			"conversation_limit_reached",
			"All active conversations are protected. Unpin or retain one before creating another.",
		)
		return
	}
	if err != nil {
		s.internalError(w, "create conversation", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": conversation})
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	conversation, err := s.readableConversation(
		r.Context(), session, r.PathValue("id"), false,
	)
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
	Pinned          *bool   `json:"pinned"`
}

func (s *Server) updateConversation(w http.ResponseWriter, r *http.Request) {
	var request updateConversationRequest
	if !readJSON(w, r, &request) {
		return
	}
	if request.Pinned != nil &&
		(request.Title != nil || request.Model != nil ||
			request.ReasoningEffort != nil || request.Archived != nil) {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid_conversation_update",
			"Pinning must be updated separately from other conversation fields.",
		)
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
	if request.Pinned != nil &&
		request.Title == nil &&
		request.Model == nil &&
		request.ReasoningEffort == nil &&
		request.Archived == nil {
		conversation, pinErr := s.store.SetConversationPinned(
			r.Context(),
			session.User.ID,
			current.ID,
			*request.Pinned,
			s.cfg.Lifecycle.MaxPinnedConversations,
		)
		if errors.Is(pinErr, store.ErrPinLimit) {
			writeError(
				w,
				http.StatusConflict,
				"pin_limit_reached",
				"Each user can pin at most ten conversations.",
			)
			return
		}
		if pinErr != nil {
			s.internalError(w, "set conversation pin state", pinErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"conversation": conversation, "reasoningEffortReset": false,
		})
		return
	}
	if request.Archived != nil &&
		request.Title == nil &&
		request.Model == nil &&
		request.ReasoningEffort == nil &&
		request.Pinned == nil {
		conversation, archiveErr := s.store.SetConversationArchivedWithPolicy(
			r.Context(),
			session.User.ID,
			current.ID,
			*request.Archived,
			s.cfg.Lifecycle.MaxActiveConversations,
			s.cfg.Lifecycle.MaxStorageBytes,
		)
		if errors.Is(archiveErr, store.ErrConversationLimit) {
			writeError(
				w,
				http.StatusConflict,
				"conversation_limit_reached",
				"Retain another active conversation before restoring this one.",
			)
			return
		}
		if errors.Is(archiveErr, store.ErrStorageQuota) {
			writeError(
				w,
				http.StatusInsufficientStorage,
				"storage_quota_exceeded",
				"Restoring this conversation would exceed your active storage allowance.",
			)
			return
		}
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
		effort = fallbackReasoningEffort(model, s.cfg.Provider.DefaultReasoningEffort)
		reset = true
	}
	conversation, err := s.store.UpdateConversation(r.Context(), session.User.ID, current.ID, title, model.ID, effort)
	if err != nil {
		s.internalError(w, "update conversation", err)
		return
	}
	if request.Archived != nil {
		conversation, err = s.store.SetConversationArchivedWithPolicy(
			r.Context(),
			session.User.ID,
			current.ID,
			*request.Archived,
			s.cfg.Lifecycle.MaxActiveConversations,
			s.cfg.Lifecycle.MaxStorageBytes,
		)
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

func fallbackReasoningEffort(model provider.Model, configured string) string {
	candidates := []string{
		strings.ToLower(strings.TrimSpace(configured)),
		strings.ToLower(strings.TrimSpace(model.DefaultReasoningEffort)),
		"high",
		"medium",
		"max",
		"auto",
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if provider.SupportsEffort(model, candidate) {
			return candidate
		}
	}
	return "auto"
}
