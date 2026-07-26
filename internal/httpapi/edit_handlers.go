package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type editResponseRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"requestId"`
}

// editResponse rewrites the latest user message and streams a fresh assistant
// answer for it, mirroring the regeneration flow.
func (s *Server) editResponse(w http.ResponseWriter, r *http.Request) {
	var request editResponseRequest
	if !readJSON(w, r, &request) {
		return
	}
	if len(request.Text) > 2*1024*1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "message_too_large", "Message text is too large.")
		return
	}
	if request.RequestID == "" {
		generated, err := ids.New()
		if err != nil {
			s.internalError(w, "generate edit request id", err)
			return
		}
		request.RequestID = generated
	}
	if len(request.RequestID) > 128 || !utf8.ValidString(request.RequestID) {
		writeError(w, http.StatusBadRequest, "invalid_request_id", "Request ID is invalid.")
		return
	}

	session, _ := sessionFromContext(r.Context())
	original, err := s.store.MessageByID(r.Context(), session.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Message not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get message for edit", err)
		return
	}
	if original.Role != "user" {
		writeError(w, http.StatusBadRequest, "message_not_editable", "Only a user message can be edited.")
		return
	}
	conversation, err := s.store.ConversationByID(r.Context(), session.User.ID, original.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get edit conversation", err)
		return
	}

	catalog, err := s.models.Models(r.Context())
	if err != nil {
		s.providerCatalogError(w, err)
		return
	}
	model, ok := s.models.FindSelectable(catalog, conversation.Model)
	if !ok {
		writeError(w, http.StatusBadRequest, "provider_model_unavailable", "The conversation model is no longer available.")
		return
	}
	if !provider.SupportsEffort(model, conversation.ReasoningEffort) {
		writeError(w, http.StatusBadRequest, "reasoning_effort_unsupported", "The conversation reasoning effort is no longer supported.")
		return
	}

	// An edited turn keeps its original mode: if the previous answer to this
	// message was an explicit image generation, the resend generates an image.
	generateImage := false
	priorMessages, err := s.store.ListMessages(r.Context(), session.User.ID, conversation.ID)
	if err != nil {
		s.internalError(w, "load edit history", err)
		return
	}
	for index := len(priorMessages) - 1; index >= 0; index-- {
		message := priorMessages[index]
		if message.Role == "assistant" && message.ParentMessageID == original.ID {
			generateImage = messageRequestedImageGeneration(message)
			break
		}
	}
	if generateImage {
		if !s.cfg.Tools.ImageGenerationEnabled || model.ImageGenerationMode == "" {
			writeError(w, http.StatusBadRequest, "model_image_generation_unsupported", "The selected model does not support image generation.")
			return
		}
		if strings.TrimSpace(request.Text) == "" {
			writeError(w, http.StatusBadRequest, "image_prompt_required", "Image generation requires a text prompt.")
			return
		}
	}
	sentEffort := conversation.ReasoningEffort
	if sentEffort == "auto" {
		sentEffort = ""
	}

	clientContext := r.Context()
	responseContext, cancelResponse, finishResponse, err := s.beginResponseJob(clientContext)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_stopping", "The service is stopping.")
		return
	}
	defer finishResponse()
	r = r.WithContext(responseContext)
	s.registerResponse(request.RequestID, session.User.ID, cancelResponse)
	defer s.unregisterResponse(request.RequestID)

	lease, queuedStream, ok := s.acquireResponseJob(w, r, session.User.ID, conversation.ID)
	if !ok {
		return
	}
	defer lease.Release()
	summaryMode := s.progressiveSummaryMode(r.Context())

	assistant, history, err := s.store.BeginEdit(
		r.Context(), session.User.ID, original.ID, request.RequestID,
		request.Text, conversation.Model, conversation.ReasoningEffort, sentEffort,
	)
	switch {
	case errors.Is(err, store.ErrDuplicateRequest):
		respondBeforeStreamStart(
			queuedStream, w, http.StatusConflict, "duplicate_request",
			"This request has already been submitted.",
		)
		return
	case errors.Is(err, store.ErrNotLatestMessage):
		respondBeforeStreamStart(
			queuedStream, w, http.StatusConflict, "not_latest_message",
			"Only the latest message can be edited.",
		)
		return
	case errors.Is(err, store.ErrNotFound):
		respondBeforeStreamStart(
			queuedStream, w, http.StatusNotFound, "not_found", "Message not found.",
		)
		return
	case err != nil:
		respondBeforeStreamStart(
			queuedStream, w, http.StatusBadRequest, "message_not_editable",
			"This message cannot be edited right now.",
		)
		return
	}
	updatedUser := history[len(history)-1]
	s.streamAssistantResponse(
		w, r, clientContext, cancelResponse,
		session.User.ID, request.RequestID, conversation, model, sentEffort,
		summaryMode, assistant, &updatedUser, history, queuedStream, generateImage,
		latestUserText(history),
	)
}
