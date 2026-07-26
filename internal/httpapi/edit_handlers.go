package httpapi

import (
	"errors"
	"net/http"
	"strings"

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
	requestID, ok := s.ensureStreamRequestID(w, request.RequestID)
	if !ok {
		return
	}
	request.RequestID = requestID

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

	model, ok := s.resolveConversationModel(w, r, conversation)
	if !ok {
		return
	}

	// An edited turn keeps its original mode: if the previous answer to this
	// message was an explicit image generation, the resend generates an image.
	generateImage := false
	previousAnswer, err := s.store.LatestAssistantChild(r.Context(), session.User.ID, original.ID)
	if err == nil {
		generateImage = messageRequestedImageGeneration(previousAnswer)
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "load previous answer for edit", err)
		return
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
		// Covers user-caused rejections and unexpected store failures alike;
		// the log line keeps infrastructure faults diagnosable.
		s.logger.Warn("begin edit rejected", "error", err)
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
		summaryMode, "edit", assistant, &updatedUser, history, queuedStream, generateImage,
		latestUserText(history),
	)
}
