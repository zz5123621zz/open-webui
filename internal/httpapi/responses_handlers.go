package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/owui-personal-slim/owui-personal-slim/internal/activecontext"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/jobs"
	"github.com/owui-personal-slim/owui-personal-slim/internal/progressivesummary"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

const (
	maxProviderEventBytes = 50 * 1024 * 1024
	sseHeartbeatInterval  = 15 * time.Second
	responseInstructions  = "Reply in the language used by the latest user message. When the latest user message is primarily Chinese, write the final answer and every user-visible reasoning summary in Simplified Chinese. Make user-visible reasoning summaries clear and informative when the provider supports them, but never reveal private chain-of-thought; summarize only the approach, checks, and current progress."
)

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	conversation, err := s.readableConversation(
		r.Context(), session, r.PathValue("id"), true,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "authorize message list", err)
		return
	}
	messages, err := s.store.ListMessages(
		r.Context(), conversation.UserID, conversation.ID,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "list messages", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

type createResponseRequest struct {
	Text          string   `json:"text"`
	AttachmentIDs []string `json:"attachmentIds"`
	RequestID     string   `json:"requestId"`
	GenerateImage bool     `json:"generateImage"`
}

type regenerateResponseRequest struct {
	RequestID string `json:"requestId"`
}

func (s *Server) createResponse(w http.ResponseWriter, r *http.Request) {
	var request createResponseRequest
	if !readJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Text) == "" && len(request.AttachmentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "message_required", "Message text or an image is required.")
		return
	}
	if len(request.Text) > 2*1024*1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "message_too_large", "Message text is too large.")
		return
	}
	if len(request.AttachmentIDs) > 4 {
		writeError(w, http.StatusBadRequest, "too_many_images", "A message can contain at most four images.")
		return
	}
	if hasDuplicateStrings(request.AttachmentIDs) {
		writeError(w, http.StatusBadRequest, "duplicate_attachment", "An attachment can only appear once in a message.")
		return
	}
	if request.RequestID == "" {
		generated, err := ids.New()
		if err != nil {
			s.internalError(w, "generate request id", err)
			return
		}
		request.RequestID = generated
	}
	if len(request.RequestID) > 128 || !utf8.ValidString(request.RequestID) {
		writeError(w, http.StatusBadRequest, "invalid_request_id", "Request ID is invalid.")
		return
	}

	session, _ := sessionFromContext(r.Context())
	conversationID := r.PathValue("id")
	conversation, err := s.store.ConversationByID(r.Context(), session.User.ID, conversationID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get response conversation", err)
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
	if len(request.AttachmentIDs) > 0 && model.CapabilitiesComplete && !containsString(model.InputModalities, "image") {
		writeError(w, http.StatusBadRequest, "model_image_input_unsupported", "The selected model does not support image input.")
		return
	}
	if request.GenerateImage {
		if !s.cfg.Tools.ImageGenerationEnabled || model.ImageGenerationMode == "" {
			writeError(w, http.StatusBadRequest, "model_image_generation_unsupported", "The selected model does not support image generation.")
			return
		}
		if strings.TrimSpace(request.Text) == "" {
			writeError(w, http.StatusBadRequest, "image_prompt_required", "Image generation requires a text prompt.")
			return
		}
		if len(request.AttachmentIDs) > 0 {
			writeError(w, http.StatusBadRequest, "image_generation_attachments_unsupported", "Image generation cannot include uploaded images yet.")
			return
		}
	}
	sentEffort := conversation.ReasoningEffort
	if sentEffort == "auto" {
		sentEffort = ""
	}

	lease, queuedStream, ok := s.acquireResponseJob(w, r, session.User.ID, conversationID)
	if !ok {
		return
	}
	defer lease.Release()
	summaryMode := s.progressiveSummaryMode(r.Context())

	userMessage, assistantMessage, err := s.store.BeginResponse(
		r.Context(), session.User.ID, conversationID, request.RequestID,
		request.Text, conversation.Model, conversation.ReasoningEffort, sentEffort,
		request.AttachmentIDs,
	)
	if errors.Is(err, store.ErrDuplicateRequest) {
		respondBeforeStreamStart(
			queuedStream, w, http.StatusConflict, "duplicate_request",
			"This request has already been submitted.",
		)
		return
	}
	if err != nil && (errors.Is(err, store.ErrNotFound) || strings.Contains(err.Error(), "attachment")) {
		respondBeforeStreamStart(
			queuedStream, w, http.StatusBadRequest, "attachment_unavailable",
			"One or more attachments are unavailable.",
		)
		return
	}
	if err != nil {
		s.logger.Error("begin response failed", "error", err)
		respondBeforeStreamStart(
			queuedStream, w, http.StatusInternalServerError, "internal_error",
			"An internal error occurred.",
		)
		return
	}

	s.streamAssistantResponse(
		w, r, session.User.ID, request.RequestID, conversation, model, sentEffort,
		summaryMode, assistantMessage, &userMessage, nil, queuedStream,
		request.GenerateImage, request.Text,
	)
}

func (s *Server) regenerateResponse(w http.ResponseWriter, r *http.Request) {
	var request regenerateResponseRequest
	if !readJSON(w, r, &request) {
		return
	}
	if request.RequestID == "" {
		generated, err := ids.New()
		if err != nil {
			s.internalError(w, "generate regeneration request id", err)
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
		s.internalError(w, "get response for regeneration", err)
		return
	}
	conversation, err := s.store.ConversationByID(r.Context(), session.User.ID, original.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		s.internalError(w, "get regeneration conversation", err)
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
	generateImage := messageRequestedImageGeneration(original)
	if generateImage && (!s.cfg.Tools.ImageGenerationEnabled || model.ImageGenerationMode == "") {
		writeError(w, http.StatusBadRequest, "model_image_generation_unsupported", "The selected model does not support image generation.")
		return
	}
	sentEffort := conversation.ReasoningEffort
	if sentEffort == "auto" {
		sentEffort = ""
	}
	lease, queuedStream, ok := s.acquireResponseJob(w, r, session.User.ID, conversation.ID)
	if !ok {
		return
	}
	defer lease.Release()
	summaryMode := s.progressiveSummaryMode(r.Context())

	assistant, history, err := s.store.BeginRegeneration(
		r.Context(), session.User.ID, original.ID, request.RequestID,
		conversation.Model, conversation.ReasoningEffort, sentEffort,
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
			queuedStream, w, http.StatusConflict, "not_latest_response",
			"Only the latest response can be regenerated.",
		)
		return
	case errors.Is(err, store.ErrNotFound):
		respondBeforeStreamStart(
			queuedStream, w, http.StatusNotFound, "not_found", "Message not found.",
		)
		return
	case err != nil:
		respondBeforeStreamStart(
			queuedStream, w, http.StatusBadRequest, "response_not_regenerable",
			"This response cannot be regenerated.",
		)
		return
	}
	s.streamAssistantResponse(
		w, r, session.User.ID, request.RequestID, conversation, model, sentEffort,
		summaryMode, assistant, nil, history, queuedStream, generateImage,
		latestUserText(history),
	)
}

func (s *Server) streamAssistantResponse(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
	requestID string,
	conversation store.Conversation,
	model provider.Model,
	sentEffort string,
	summaryMode string,
	assistantMessage store.Message,
	userMessage *store.Message,
	history []store.Message,
	stream *sseWriter,
	generateImage bool,
	imagePrompt string,
) {
	responseContext, cancelResponse := context.WithCancel(r.Context())
	r = r.WithContext(responseContext)
	s.registerResponse(assistantMessage.ID, userID, cancelResponse)
	defer func() {
		s.unregisterResponse(assistantMessage.ID)
		cancelResponse()
	}()

	if stream == nil {
		stream = newSSEWriter(w)
		stream.start()
	}
	started := map[string]any{
		"requestId": requestID, "assistantMessage": assistantMessage,
	}
	if userMessage != nil {
		started["userMessage"] = *userMessage
	} else {
		started["regenerated"] = true
	}
	if err := stream.send("response.started", started); err != nil {
		_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "client_disconnected", nil)
		return
	}
	initialStage := "preparing_context"
	if generateImage && model.ImageGenerationMode == "dedicated" {
		initialStage = "generating_image"
	}
	if err := stream.send("response.stage", map[string]string{"stage": initialStage}); err != nil {
		_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "client_disconnected", nil)
		return
	}
	stopHeartbeat := stream.startHeartbeat(r.Context(), sseHeartbeatInterval, cancelResponse)
	defer stopHeartbeat()

	if generateImage && model.ImageGenerationMode == "dedicated" {
		s.streamDedicatedImage(
			r.Context(), stream, userID, conversation.ID, assistantMessage.ID,
			model.DedicatedImageModel, imagePrompt, requestID,
		)
		return
	}

	var err error
	if history == nil {
		history, err = s.store.ListMessages(r.Context(), userID, conversation.ID)
		if err != nil {
			_, _ = s.failAssistant(r.Context(), userID, assistantMessage.ID, "history_unavailable", nil)
			s.logger.Error("load response history failed", "error", err)
			_ = stream.send("response.error", map[string]string{"code": "history_unavailable", "message": "Conversation history could not be loaded."})
			return
		}
	}
	active, err := s.contexts.Prepare(
		r.Context(), userID, conversation, model, sentEffort, history, assistantMessage.ID,
		func(status string, data map[string]any) error {
			data["status"] = status
			return stream.send("response.context", data)
		},
	)
	if err != nil {
		code := "context_compaction_failed"
		if errors.Is(err, activecontext.ErrContextTooLarge) {
			code = "context_too_large"
		}
		_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, code, nil)
		_ = stream.send("response.error", map[string]string{"code": code, "message": "The conversation context could not be prepared safely."})
		return
	}
	if active.CompactionWarning != nil {
		s.logger.Warn("context compaction failed below hard threshold", "error", active.CompactionWarning, "conversation_id", conversation.ID)
	}
	providerRequest, err := s.buildResponsesRequest(r.Context(), userID, conversation, model, sentEffort, active.Checkpoint, active.Messages)
	if err != nil {
		_, _ = s.failAssistant(r.Context(), userID, assistantMessage.ID, "attachment_unavailable", nil)
		s.logger.Error("compile provider request failed", "error", err)
		_ = stream.send("response.error", map[string]string{"code": "attachment_unavailable", "message": "An image could not be prepared."})
		return
	}
	configureImageGenerationRequest(&providerRequest, generateImage)
	summaryDecision := progressivesummary.Decision{}
	if s.progressiveSummaryEligible(summaryMode, model) {
		summaryDecision = s.summaries.Decide(
			s.cfg.Provider.BaseURL.String(), model.ID,
		)
		if summaryDecision.Requested {
			enableProgressiveSummary(&providerRequest)
		}
	}
	if err := stream.send("response.stage", map[string]string{"stage": "waiting_for_model"}); err != nil {
		s.summaries.MarkInconclusive(summaryDecision)
		_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "client_disconnected", nil)
		return
	}
	newAccumulator := func() responseAccumulator {
		return responseAccumulator{
			saveImage: func(encoded string) (generatedImage, error) {
				return s.saveGeneratedImage(context.WithoutCancel(r.Context()), userID, conversation.ID, assistantMessage.ID, encoded)
			},
		}
	}
	runProvider := func(request provider.ResponsesRequest, accumulator *responseAccumulator) (error, error) {
		upstream, startErr := s.models.StartResponse(r.Context(), request)
		if startErr != nil {
			return startErr, nil
		}
		if request.StreamOptions != nil {
			s.summaries.MarkAccepted(summaryDecision)
		}
		consumeErr := consumeProviderSSE(upstream.Body, func(event providerStreamEvent) error {
			return accumulator.handle(stream, event)
		})
		closeErr := upstream.Body.Close()
		if consumeErr == nil {
			consumeErr = closeErr
		}
		return nil, consumeErr
	}

	var accumulator responseAccumulator
	var startErr, consumeErr error
	compatibilityFallbackUsed := false
	contextRetryUsed := false
	for {
		accumulator = newAccumulator()
		startErr, consumeErr = runProvider(providerRequest, &accumulator)
		if startErr == nil {
			break
		}
		if providerRequest.StreamOptions != nil &&
			!compatibilityFallbackUsed &&
			isProgressiveSummaryUnsupported(startErr) {
			s.summaries.MarkUnsupported(summaryDecision)
			compatibilityFallbackUsed = true
			providerRequest.StreamOptions = nil
			_ = stream.send("response.stage", map[string]string{"stage": "waiting_for_model"})
			continue
		}
		if isContextWindowError(startErr) && !contextRetryUsed {
			contextRetryUsed = true
			_ = stream.send("response.stage", map[string]string{"stage": "preparing_context"})
			forced, forceErr := s.contexts.ForcePrepare(
				r.Context(), userID, conversation, model, sentEffort, history, assistantMessage.ID,
				func(status string, data map[string]any) error {
					data["status"] = status
					data["retry"] = true
					return stream.send("response.context", data)
				},
			)
			if forceErr != nil {
				s.summaries.MarkInconclusive(summaryDecision)
				_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "context_compaction_failed", nil)
				_ = stream.send("response.error", map[string]string{
					"code": "context_compaction_failed", "message": "The conversation is too large and could not be compacted safely.",
				})
				return
			}
			providerRequest, err = s.buildResponsesRequest(
				r.Context(), userID, conversation, model, sentEffort,
				forced.Checkpoint, forced.Messages,
			)
			if err != nil {
				s.summaries.MarkInconclusive(summaryDecision)
				_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "attachment_unavailable", nil)
				_ = stream.send("response.error", map[string]string{"code": "attachment_unavailable", "message": "An image could not be prepared."})
				return
			}
			configureImageGenerationRequest(&providerRequest, generateImage)
			if summaryDecision.Requested && !compatibilityFallbackUsed {
				enableProgressiveSummary(&providerRequest)
			}
			_ = stream.send("response.stage", map[string]string{"stage": "waiting_for_model"})
			continue
		}
		if providerRequest.StreamOptions != nil {
			s.summaries.MarkInconclusive(summaryDecision)
		}
		break
	}
	if startErr != nil {
		code, _, message := providerStartError(startErr)
		_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, code, nil)
		_ = stream.send("response.error", map[string]string{"code": code, "message": message})
		return
	}

	status := "completed"
	errorCode := ""
	if consumeErr != nil {
		if errors.Is(consumeErr, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
			status = "interrupted"
			errorCode = "client_disconnected"
		} else if errors.Is(consumeErr, store.ErrStorageQuota) {
			status = "error"
			errorCode = "storage_quota_exceeded"
		} else {
			status = "error"
			errorCode = "provider_stream_error"
			s.logger.Error("provider stream failed", "error", consumeErr, "conversation_id", conversation.ID)
		}
	}
	if accumulator.failureCode != "" {
		status = "error"
		errorCode = accumulator.failureCode
	}
	if !accumulator.completed && status == "completed" {
		status = "interrupted"
		errorCode = "provider_stream_incomplete"
	}
	if generateImage {
		accumulator.markExplicitImageGeneration()
	}

	parts := accumulator.parts()
	result := store.AssistantResult{
		ProviderResponseID: accumulator.responseID,
		Status:             status,
		ErrorCode:          errorCode,
		InputTokens:        accumulator.inputTokens,
		OutputTokens:       accumulator.outputTokens,
		ReasoningTokens:    accumulator.reasoningTokens,
		Parts:              parts,
		ProviderItems:      accumulator.providerItems,
	}
	finalContext := context.WithoutCancel(r.Context())
	finalMessage, storeErr := s.store.CompleteAssistant(finalContext, userID, assistantMessage.ID, result)
	if storeErr != nil {
		s.logger.Error("store assistant result failed", "error", storeErr, "message_id", assistantMessage.ID)
		_ = stream.send("response.error", map[string]string{"code": "persistence_failed", "message": "The response could not be saved."})
		return
	}
	s.logger.Info("provider response finished",
		"request_id", requestID,
		"user_id_hash", hashIdentifier(userID),
		"provider_request_id", accumulator.responseID,
		"status", status,
		"error_code", errorCode,
	)
	if status == "completed" {
		_ = stream.send("response.completed", map[string]any{"message": finalMessage})
	} else {
		_ = stream.send("response.error", map[string]any{
			"code": errorCode, "message": "The response ended before completion.", "messageRecord": finalMessage,
		})
	}
}

func (s *Server) progressiveSummaryMode(ctx context.Context) string {
	setting, err := s.store.ProgressiveSummarySetting(ctx)
	if err != nil {
		s.logger.Error("read progressive summary mode failed", "error", err)
		return store.ProgressiveSummaryModeOff
	}
	return setting.Value
}

func (s *Server) progressiveSummaryEligible(mode string, model provider.Model) bool {
	if mode != store.ProgressiveSummaryModeAuto ||
		s.cfg.Provider.ProgressiveSummaryHardDisabled {
		return false
	}
	return !model.CapabilitiesComplete || len(model.ReasoningEfforts) > 0
}

func enableProgressiveSummary(request *provider.ResponsesRequest) {
	request.StreamOptions = &provider.StreamOptions{
		ReasoningSummaryDelivery: provider.ReasoningSummaryDeliverySequentialCutoff,
	}
}

func configureImageGenerationRequest(request *provider.ResponsesRequest, generateImage bool) {
	if !generateImage {
		return
	}
	request.Tools = []map[string]any{{"type": "image_generation"}}
	request.ToolChoice = "required"
}

func (s *Server) streamDedicatedImage(
	ctx context.Context,
	stream *sseWriter,
	userID string,
	conversationID string,
	messageID string,
	imageModel string,
	prompt string,
	requestID string,
) {
	callID, err := ids.New()
	if err != nil {
		_, _ = s.failAssistant(context.WithoutCancel(ctx), userID, messageID, "internal_error", nil)
		_ = stream.send("response.error", map[string]string{
			"code": "internal_error", "message": "The image request could not be initialized.",
		})
		return
	}
	startedAt := time.Now()
	running := toolSnapshot{
		CallID: callID, Type: "image_generation", Status: "in_progress",
		Data: json.RawMessage(`{"explicit":true}`),
	}
	if err := stream.send("response.tool", running); err != nil {
		_, _ = s.completeDedicatedImageFailure(
			context.WithoutCancel(ctx), userID, messageID, running, "client_disconnected", "interrupted",
		)
		return
	}

	result, generationErr := s.models.GenerateImage(ctx, provider.ImageGenerationRequest{
		Model: imageModel, Prompt: strings.TrimSpace(prompt),
	})
	if generationErr != nil {
		code, _, message := providerStartError(generationErr)
		status := "error"
		if ctx.Err() != nil {
			code = "client_disconnected"
			message = "The image request ended before completion."
			status = "interrupted"
		} else if errors.Is(generationErr, provider.ErrBadResponse) {
			code = "provider_invalid_response"
			message = "The image provider returned an invalid response."
		}
		failed := running
		failed.Status = "failed"
		failed.DurationMS = time.Since(startedAt).Milliseconds()
		failed.ErrorCode = code
		finalMessage, _ := s.completeDedicatedImageFailure(
			context.WithoutCancel(ctx), userID, messageID, failed, code, status,
		)
		_ = stream.send("response.tool", failed)
		_ = stream.send("response.error", map[string]any{
			"code": code, "message": message, "messageRecord": finalMessage,
		})
		s.logger.Info("provider response finished",
			"request_id", requestID,
			"user_id_hash", hashIdentifier(userID),
			"provider_request_id", result.ResponseID,
			"status", status,
			"error_code", code,
		)
		return
	}

	generated, err := s.saveGeneratedImage(
		context.WithoutCancel(ctx), userID, conversationID, messageID, result.Base64,
	)
	if err != nil {
		failed := running
		failed.Status = "failed"
		failed.DurationMS = time.Since(startedAt).Milliseconds()
		errorCode := "generated_image_invalid"
		message := "The generated image could not be saved safely."
		if errors.Is(err, store.ErrStorageQuota) {
			errorCode = "storage_quota_exceeded"
			message = "Your active workspace has reached its storage allowance."
		}
		failed.ErrorCode = errorCode
		finalMessage, _ := s.completeDedicatedImageFailure(
			context.WithoutCancel(ctx), userID, messageID, failed,
			errorCode, "error",
		)
		_ = stream.send("response.tool", failed)
		_ = stream.send("response.error", map[string]any{
			"code":          errorCode,
			"message":       message,
			"messageRecord": finalMessage,
		})
		return
	}

	completed := running
	completed.Status = "completed"
	completed.DurationMS = time.Since(startedAt).Milliseconds()
	rawTool, _ := json.Marshal(completed)
	finalMessage, storeErr := s.store.CompleteAssistant(
		context.WithoutCancel(ctx), userID, messageID, store.AssistantResult{
			ProviderResponseID: result.ResponseID,
			Status:             "completed",
			Parts: []store.NewMessagePart{
				{Type: "tool", JSONContent: rawTool},
				{Type: "image", AttachmentID: generated.AttachmentID},
			},
		},
	)
	if storeErr != nil {
		s.logger.Error("store generated image response failed", "error", storeErr, "message_id", messageID)
		_ = stream.send("response.error", map[string]string{
			"code": "persistence_failed", "message": "The generated image could not be saved.",
		})
		return
	}
	_ = stream.send("response.tool", completed)
	_ = stream.send("response.image", generated)
	_ = stream.send("response.completed", map[string]any{"message": finalMessage})
	s.logger.Info("provider response finished",
		"request_id", requestID,
		"user_id_hash", hashIdentifier(userID),
		"provider_request_id", result.ResponseID,
		"status", "completed",
		"error_code", "",
	)
}

func (s *Server) completeDedicatedImageFailure(
	ctx context.Context,
	userID string,
	messageID string,
	tool toolSnapshot,
	code string,
	status string,
) (store.Message, error) {
	rawTool, _ := json.Marshal(tool)
	return s.store.CompleteAssistant(ctx, userID, messageID, store.AssistantResult{
		Status: status, ErrorCode: code,
		Parts: []store.NewMessagePart{{Type: "tool", JSONContent: rawTool}},
	})
}

func messageRequestedImageGeneration(message store.Message) bool {
	for _, part := range message.Parts {
		if part.Type != "tool" {
			continue
		}
		var snapshot struct {
			Type string `json:"type"`
			Data struct {
				Explicit bool `json:"explicit"`
			} `json:"data"`
		}
		if json.Unmarshal(part.JSONContent, &snapshot) == nil &&
			snapshot.Type == "image_generation" && snapshot.Data.Explicit {
			return true
		}
	}
	return false
}

func latestUserText(messages []store.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "user" {
			continue
		}
		for _, part := range messages[index].Parts {
			if part.Type == "text" && strings.TrimSpace(part.TextContent) != "" {
				return part.TextContent
			}
		}
	}
	return ""
}

func (s *Server) failAssistant(ctx context.Context, userID, messageID, code string, parts []store.NewMessagePart) (store.Message, error) {
	return s.store.CompleteAssistant(ctx, userID, messageID, store.AssistantResult{
		Status: "error", ErrorCode: code, Parts: parts,
	})
}

func (s *Server) acquireResponseJob(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
	conversationID string,
) (*jobs.Lease, *sseWriter, bool) {
	var stream *sseWriter
	lease, err := s.jobs.AcquireWithQueueCallback(
		r.Context(), userID, conversationID,
		func(position int) error {
			stream = newSSEWriter(w)
			stream.start()
			return stream.send("response.queued", map[string]any{
				"position":       position,
				"timeoutSeconds": int(s.cfg.Jobs.QueueTimeout.Seconds()),
			})
		},
	)
	if err == nil {
		return lease, stream, true
	}
	status, code, message := jobErrorDetails(err)
	if stream != nil {
		_ = stream.send("response.error", map[string]any{
			"code": code, "message": message, "retryable": true,
		})
	} else {
		writeError(w, status, code, message)
	}
	return nil, stream, false
}

func respondBeforeStreamStart(
	stream *sseWriter,
	w http.ResponseWriter,
	status int,
	code string,
	message string,
) {
	if stream != nil {
		_ = stream.send("response.error", map[string]any{
			"code": code, "message": message, "retryable": false,
		})
		return
	}
	writeError(w, status, code, message)
}

func (s *Server) writeJobError(w http.ResponseWriter, err error) {
	status, code, message := jobErrorDetails(err)
	writeError(w, status, code, message)
}

func jobErrorDetails(err error) (int, string, string) {
	switch {
	case errors.Is(err, jobs.ErrConversationBusy):
		return http.StatusConflict, "conversation_busy", "This conversation is already generating a response."
	case errors.Is(err, jobs.ErrQueueFull):
		return http.StatusTooManyRequests, "too_many_requests", "Too many requests are queued for this user."
	case errors.Is(err, jobs.ErrQueueTimeout):
		return http.StatusServiceUnavailable, "provider_queue_timeout", "The request waited too long for an available slot."
	default:
		return http.StatusRequestTimeout, "request_cancelled", "The request was cancelled."
	}
}

func providerStartError(err error) (string, int, string) {
	if errors.Is(err, provider.ErrImagePromptTooLong) {
		return "image_prompt_too_long", http.StatusRequestEntityTooLarge,
			"The image prompt exceeds the provider's 8000-byte limit."
	}
	if isContextWindowError(err) {
		return "context_too_large", http.StatusRequestEntityTooLarge, "The conversation is still too large after compaction."
	}
	var upstream *provider.HTTPError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == http.StatusBadRequest &&
			strings.Contains(strings.ToLower(upstream.Message), "prompt length exceeds") {
			return "image_prompt_too_long", http.StatusRequestEntityTooLarge,
				"The image prompt exceeds the provider's 8000-byte limit."
		}
		switch upstream.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "provider_authentication_failed", http.StatusBadGateway, "The provider credential was rejected."
		case http.StatusTooManyRequests:
			return "provider_rate_limited", http.StatusTooManyRequests, "The provider is rate limited. Try again shortly."
		case http.StatusRequestEntityTooLarge:
			return "provider_request_too_large", http.StatusRequestEntityTooLarge, "The compiled conversation is too large."
		default:
			if upstream.StatusCode >= 500 {
				return "provider_unavailable", http.StatusBadGateway, "The provider is temporarily unavailable."
			}
			return "provider_request_rejected", http.StatusBadGateway, "The provider rejected the request."
		}
	}
	if strings.Contains(err.Error(), "exceeds") {
		return "provider_request_too_large", http.StatusRequestEntityTooLarge, "The compiled conversation is too large."
	}
	return "provider_unavailable", http.StatusBadGateway, "The provider is temporarily unavailable."
}

func isContextWindowError(err error) bool {
	if err == nil {
		return false
	}
	var upstream *provider.HTTPError
	if !errors.As(err, &upstream) {
		return false
	}
	return isContextWindowCode(upstream.Code) || isContextWindowCode(upstream.Message)
}

func isProgressiveSummaryUnsupported(err error) bool {
	var upstream *provider.HTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusBadRequest {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(upstream.Code + " " + upstream.Message))
	if !strings.Contains(value, "reasoning_summary_delivery") &&
		!strings.Contains(value, "stream_options") {
		return false
	}
	for _, marker := range []string{
		"unsupported",
		"not supported",
		"unknown",
		"unrecognized",
		"unexpected",
		"not allowed",
		"not permitted",
		"extra input",
		"invalid parameter",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func isContextWindowCode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "context_length") ||
		strings.Contains(value, "context_window") ||
		strings.Contains(value, "prompt_too_long") ||
		strings.Contains(value, "too_many_tokens")
}

func (s *Server) buildResponsesRequest(ctx context.Context, userID string, conversation store.Conversation, model provider.Model, effort string, checkpoint *store.ContextCheckpoint, messages []store.Message) (provider.ResponsesRequest, error) {
	input := make([]provider.ResponseInput, 0, len(messages)+1)
	if checkpoint != nil {
		input = append(input, provider.ResponseInput{
			Role:    "developer",
			Content: "Earlier conversation checkpoint (preserve as context, do not repeat unless relevant):\n\n" + checkpoint.SummaryText,
		})
	}
	for _, message := range messages {
		if message.Status == "streaming" || message.Status == "pending" {
			continue
		}
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Role == "assistant" && len(message.ProviderItems) > 0 {
			for _, item := range message.ProviderItems {
				input = append(input, provider.ResponseInput{Raw: item.ReplayJSON})
			}
			var generatedNotes []string
			for _, part := range message.Parts {
				if part.Type == "image" && part.AttachmentID != "" {
					generatedNotes = append(generatedNotes, "[Generated image attachment "+part.AttachmentID+"]")
				}
			}
			if len(generatedNotes) > 0 {
				input = append(input, provider.ResponseInput{
					Role: "assistant", Content: []provider.ResponseContent{{
						Type: "output_text", Text: strings.Join(generatedNotes, "\n"),
					}},
				})
			}
			continue
		}
		content := make([]provider.ResponseContent, 0, len(message.Parts))
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				contentType := "input_text"
				if message.Role == "assistant" {
					contentType = "output_text"
				}
				content = append(content, provider.ResponseContent{Type: contentType, Text: part.TextContent})
			case "image":
				if message.Role != "user" {
					continue
				}
				attachment, err := s.store.AttachmentByID(ctx, userID, part.AttachmentID)
				if err != nil {
					return provider.ResponsesRequest{}, err
				}
				content = append(content, provider.ResponseContent{
					Type: "input_image", ImagePath: filepath.Join(s.cfg.DataDir, attachment.StoragePath),
					MediaType: attachment.MediaType, ByteSize: attachment.ByteSize,
				})
			}
		}
		if len(content) == 0 {
			continue
		}
		input = append(input, provider.ResponseInput{Role: message.Role, Content: content})
	}
	tools := make([]map[string]any, 0, 2)
	if s.cfg.Tools.WebSearchEnabled && model.SupportsWebSearch {
		tools = append(tools, map[string]any{"type": "web_search"})
	}
	if s.cfg.Tools.ImageGenerationEnabled && model.ImageGenerationMode == "responses_tool" {
		tools = append(tools, map[string]any{"type": "image_generation"})
	}
	return provider.ResponsesRequest{
		Model: conversation.Model, Instructions: responseInstructions,
		SafetyIdentifier: s.providerSafetyIdentifier(userID),
		Input:            input, Stream: true, Store: false,
		Reasoning: provider.ReasoningOptions{Effort: effort, Summary: "auto"},
		Tools:     tools, ToolChoice: "auto",
	}, nil
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type sseWriter struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	mu         sync.Mutex
}

func newSSEWriter(w http.ResponseWriter) *sseWriter {
	return &sseWriter{writer: w, controller: http.NewResponseController(w)}
}

func (s *sseWriter) start() {
	s.writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	s.writer.Header().Set("Cache-Control", "no-cache, no-transform")
	s.writer.Header().Set("Connection", "keep-alive")
	s.writer.Header().Set("X-Accel-Buffering", "no")
	s.writer.WriteHeader(http.StatusOK)
	_ = s.controller.Flush()
}

func (s *sseWriter) send(event string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, raw); err != nil {
		return err
	}
	return s.controller.Flush()
}

func (s *sseWriter) comment(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.writer, ": %s\n\n", value); err != nil {
		return err
	}
	return s.controller.Flush()
}

func (s *sseWriter) startHeartbeat(
	ctx context.Context,
	interval time.Duration,
	onFailure context.CancelFunc,
) func() {
	if interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				if err := s.comment("keepalive"); err != nil {
					onFailure()
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

type providerStreamEvent struct {
	Type           string          `json:"type"`
	Delta          string          `json:"delta"`
	Text           string          `json:"text"`
	ItemID         string          `json:"item_id"`
	OutputIndex    int             `json:"output_index"`
	SummaryIndex   int             `json:"summary_index"`
	SequenceNumber int             `json:"sequence_number"`
	Item           json.RawMessage `json:"item"`
	Response       json.RawMessage `json:"response"`
	Error          json.RawMessage `json:"error"`
}

func consumeProviderSSE(body io.Reader, handle func(providerStreamEvent) error) error {
	reader := bufio.NewReaderSize(body, 64*1024)
	var dataLines [][]byte
	dataBytes := 0
	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		var payload []byte
		if len(dataLines) == 1 {
			payload = dataLines[0]
		} else {
			var joined bytes.Buffer
			joined.Grow(dataBytes)
			for index, line := range dataLines {
				if index > 0 {
					joined.WriteByte('\n')
				}
				joined.Write(line)
			}
			payload = joined.Bytes()
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			return io.EOF
		}
		var event providerStreamEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return fmt.Errorf("decode provider event: %w", err)
		}
		return handle(event)
	}
	for {
		line, err := readLimitedLine(reader, maxProviderEventBytes)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) == 0 {
			if len(dataLines) > 0 {
				if dispatchErr := dispatch(); errors.Is(dispatchErr, io.EOF) {
					return nil
				} else if dispatchErr != nil {
					return dispatchErr
				}
				dataLines = nil
				dataBytes = 0
			}
		} else if bytes.HasPrefix(trimmed, []byte("data:")) {
			value := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
			if dataBytes+len(value)+len(dataLines) > maxProviderEventBytes {
				return errors.New("provider event exceeds 50 MiB")
			}
			dataLines = append(dataLines, value)
			dataBytes += len(value)
		}
		if errors.Is(err, io.EOF) {
			if len(dataLines) > 0 {
				dispatchErr := dispatch()
				if errors.Is(dispatchErr, io.EOF) {
					return nil
				}
				return dispatchErr
			}
			return nil
		}
	}
}

func readLimitedLine(reader *bufio.Reader, maximum int) ([]byte, error) {
	result := make([]byte, 0, 4096)
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(result)+len(chunk) > maximum {
			return nil, errors.New("provider SSE line exceeds 50 MiB")
		}
		result = append(result, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return result, err
		}
	}
}

type responseAccumulator struct {
	responseID      string
	text            strings.Builder
	reasoning       []*reasoningSection
	reasoningByKey  map[string]*reasoningSection
	citations       []citation
	tools           []toolSnapshot
	toolStarted     map[string]time.Time
	images          []generatedImage
	partOrder       []accumulatorPart
	inputTokens     int64
	outputTokens    int64
	reasoningTokens int64
	completed       bool
	failureCode     string
	saveImage       func(string) (generatedImage, error)
	providerItems   []store.NewProviderItem
}

func (a *responseAccumulator) markExplicitImageGeneration() {
	for index := range a.tools {
		if a.tools[index].Type == "image_generation" {
			a.tools[index].Data = json.RawMessage(`{"explicit":true}`)
		}
	}
}

type accumulatorPart struct {
	Type string
	Key  string
}

type reasoningSection struct {
	key          string
	itemID       string
	summaryIndex int
	outputIndex  int
	text         strings.Builder
	startedAt    time.Time
	durationMS   int64
	sawDelta     bool
	completed    bool
}

type citation struct {
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	StartIndex int    `json:"startIndex,omitempty"`
	EndIndex   int    `json:"endIndex,omitempty"`
}

type toolSnapshot struct {
	CallID     string          `json:"callId,omitempty"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Data       json.RawMessage `json:"data,omitempty"`
	DurationMS int64           `json:"durationMs,omitempty"`
	ErrorCode  string          `json:"errorCode,omitempty"`
}

func (a *responseAccumulator) handle(stream *sseWriter, event providerStreamEvent) error {
	switch event.Type {
	case "response.created", "response.in_progress":
		a.readResponseMetadata(event.Response)
	case "response.output_text.delta":
		a.finishOpenReasoning()
		a.ensurePart("text", "")
		a.text.WriteString(event.Delta)
		return stream.send("response.text.delta", map[string]string{"delta": event.Delta})
	case "response.reasoning_summary_text.delta":
		if event.Delta == "" {
			return nil
		}
		section := a.reasoningSection(event)
		if section.completed {
			return nil
		}
		section.sawDelta = true
		section.text.WriteString(event.Delta)
		return stream.send("response.reasoning.delta", reasoningEventPayload(
			section, event.Delta, "",
		))
	case "response.reasoning_summary_text.done":
		section := a.reasoningSection(event)
		previousText := section.text.String()
		wasCompleted := section.completed
		if event.Text != "" {
			section.text.Reset()
			section.text.WriteString(event.Text)
		}
		if section.text.Len() == 0 ||
			(wasCompleted && previousText == section.text.String()) {
			return nil
		}
		a.finishReasoningSection(section)
		return stream.send("response.reasoning.done", reasoningEventPayload(
			section, "", section.text.String(),
		))
	case "response.output_item.added":
		return a.handleItem(stream, event, "in_progress")
	case "response.output_item.done":
		a.captureProviderItem(event.Item)
		return a.handleItem(stream, event, "completed")
	case "response.completed":
		a.finishOpenReasoning()
		a.completed = true
		a.readResponseMetadata(event.Response)
	case "response.failed", "response.incomplete":
		a.finishOpenReasoning()
		a.readFailure(event.Response, event.Error)
	case "error":
		a.finishOpenReasoning()
		a.readFailure(nil, event.Error)
	default:
		if strings.Contains(event.Type, "web_search_call") {
			a.finishOpenReasoning()
			status := "in_progress"
			if strings.HasSuffix(event.Type, ".completed") {
				status = "completed"
			} else if strings.HasSuffix(event.Type, ".failed") {
				status = "failed"
			}
			if a.toolStarted == nil {
				a.toolStarted = make(map[string]time.Time)
			}
			if event.ItemID != "" && status == "in_progress" {
				if _, exists := a.toolStarted[event.ItemID]; !exists {
					a.toolStarted[event.ItemID] = time.Now()
				}
			}
			var durationMS int64
			if started, exists := a.toolStarted[event.ItemID]; exists && status != "in_progress" {
				durationMS = time.Since(started).Milliseconds()
				delete(a.toolStarted, event.ItemID)
			}
			snapshot := toolSnapshot{
				CallID: event.ItemID, Type: "web_search", Status: status,
				DurationMS: durationMS,
			}
			a.recordTool(snapshot)
			return stream.send("response.tool", snapshot)
		}
	}
	return nil
}

func (a *responseAccumulator) captureProviderItem(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var header struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return
	}
	switch header.Type {
	case "message", "web_search_call":
		safe, ok := sanitizeProviderReplayItem(raw)
		if !ok {
			return
		}
		a.providerItems = append(a.providerItems, store.NewProviderItem{
			ItemType: header.Type, ReplayJSON: safe,
		})
	}
}

func (a *responseAccumulator) handleItem(
	stream *sseWriter,
	event providerStreamEvent,
	fallbackStatus string,
) error {
	raw := event.Item
	if len(raw) == 0 {
		return nil
	}
	var item struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Result string `json:"result"`
		Action any    `json:"action"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
		Content []struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Annotations []struct {
				Type       string `json:"type"`
				URL        string `json:"url"`
				Title      string `json:"title"`
				StartIndex int    `json:"start_index"`
				EndIndex   int    `json:"end_index"`
			} `json:"annotations"`
		} `json:"content"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil
	}
	status := normalizeOutputItemStatus(item.Status, fallbackStatus)
	var durationMS int64
	if item.Type == "web_search_call" || item.Type == "image_generation_call" {
		if a.toolStarted == nil {
			a.toolStarted = make(map[string]time.Time)
		}
		if status == "in_progress" && item.ID != "" {
			if _, exists := a.toolStarted[item.ID]; !exists {
				a.toolStarted[item.ID] = time.Now()
			}
		}
		if started, exists := a.toolStarted[item.ID]; exists && status != "in_progress" {
			durationMS = time.Since(started).Milliseconds()
			delete(a.toolStarted, item.ID)
		}
	}
	switch item.Type {
	case "reasoning":
		if status != "completed" {
			return nil
		}
		for summaryIndex, summary := range item.Summary {
			if summary.Type != "summary_text" || summary.Text == "" {
				continue
			}
			summaryEvent := providerStreamEvent{
				ItemID:       item.ID,
				OutputIndex:  event.OutputIndex,
				SummaryIndex: summaryIndex,
			}
			section := a.reasoningSection(summaryEvent)
			previousText := section.text.String()
			wasCompleted := section.completed
			section.text.Reset()
			section.text.WriteString(summary.Text)
			a.finishReasoningSection(section)
			if wasCompleted && previousText == summary.Text {
				continue
			}
			if err := stream.send("response.reasoning.done", reasoningEventPayload(
				section, "", section.text.String(),
			)); err != nil {
				return err
			}
		}
		return nil
	case "web_search_call":
		a.finishOpenReasoning()
		safeData := safeWebAction(item.Action)
		snapshot := toolSnapshot{
			CallID: item.ID, Type: "web_search", Status: status, Data: safeData,
			DurationMS: durationMS, ErrorCode: sanitizeOptionalCode(item.Error.Code),
		}
		a.recordTool(snapshot)
		return stream.send("response.tool", snapshot)
	case "image_generation_call":
		a.finishOpenReasoning()
		snapshot := toolSnapshot{
			CallID: item.ID, Type: "image_generation", Status: status,
			DurationMS: durationMS, ErrorCode: sanitizeOptionalCode(item.Error.Code),
		}
		a.recordTool(snapshot)
		if status == "completed" {
			if item.Result != "" && a.saveImage != nil {
				if len(a.images) >= 1 {
					return errors.New("provider returned more than one generated image")
				}
				generated, err := a.saveImage(item.Result)
				if err != nil {
					return err
				}
				a.images = append(a.images, generated)
				a.ensurePart("image", generated.AttachmentID)
				if err := stream.send("response.image", generated); err != nil {
					return err
				}
			}
		}
		return stream.send("response.tool", snapshot)
	case "message":
		if a.text.Len() == 0 {
			for _, content := range item.Content {
				if content.Type == "output_text" {
					a.finishOpenReasoning()
					a.ensurePart("text", "")
					a.text.WriteString(content.Text)
				}
			}
		}
		for _, content := range item.Content {
			for _, annotation := range content.Annotations {
				if annotation.Type != "url_citation" || annotation.URL == "" {
					continue
				}
				safeURL := sanitizeCitationURL(annotation.URL)
				if safeURL == "" {
					continue
				}
				a.ensurePart("citations", "")
				a.citations = append(a.citations, citation{
					URL: safeURL, Title: truncateRunes(annotation.Title, 300),
					StartIndex: annotation.StartIndex, EndIndex: annotation.EndIndex,
				})
			}
		}
	}
	return nil
}

func normalizeOutputItemStatus(status, eventStatus string) string {
	status = strings.TrimSpace(status)
	eventStatus = strings.TrimSpace(eventStatus)
	switch eventStatus {
	case "in_progress":
		if status == "" || status == "queued" || status == "generating" {
			return "in_progress"
		}
	case "completed":
		// CPA can emit response.output_item.done for an image_generation_call
		// while the embedded item still carries the transitional "generating"
		// status. The event lifecycle is authoritative in that case and the
		// same item contains the final Base64 result.
		if status == "" || status == "queued" || status == "in_progress" || status == "generating" {
			return "completed"
		}
	}
	return status
}

func (a *responseAccumulator) recordTool(snapshot toolSnapshot) {
	a.ensurePart("tool", toolKey(snapshot))
	for index := range a.tools {
		if a.tools[index].CallID == snapshot.CallID && a.tools[index].Type == snapshot.Type {
			if len(snapshot.Data) == 0 {
				snapshot.Data = a.tools[index].Data
			}
			if snapshot.DurationMS == 0 {
				snapshot.DurationMS = a.tools[index].DurationMS
			}
			a.tools[index] = snapshot
			return
		}
	}
	a.tools = append(a.tools, snapshot)
}

func toolKey(snapshot toolSnapshot) string {
	return snapshot.Type + ":" + snapshot.CallID
}

func (a *responseAccumulator) ensurePart(partType, key string) {
	for _, part := range a.partOrder {
		if part.Type == partType && part.Key == key {
			return
		}
	}
	a.partOrder = append(a.partOrder, accumulatorPart{Type: partType, Key: key})
}

func reasoningSectionKey(itemID string, outputIndex, summaryIndex int) string {
	if itemID != "" {
		return itemID + "\x1f" + strconv.Itoa(summaryIndex)
	}
	return "output:" + strconv.Itoa(outputIndex) + "\x1f" + strconv.Itoa(summaryIndex)
}

func (a *responseAccumulator) reasoningSection(event providerStreamEvent) *reasoningSection {
	if a.reasoningByKey == nil {
		a.reasoningByKey = make(map[string]*reasoningSection)
	}
	key := reasoningSectionKey(event.ItemID, event.OutputIndex, event.SummaryIndex)
	if section, exists := a.reasoningByKey[key]; exists {
		return section
	}
	section := &reasoningSection{
		key:          key,
		itemID:       event.ItemID,
		summaryIndex: event.SummaryIndex,
		outputIndex:  event.OutputIndex,
		startedAt:    time.Now(),
	}
	a.reasoningByKey[key] = section
	a.reasoning = append(a.reasoning, section)
	a.ensurePart("reasoning", key)
	return section
}

func (a *responseAccumulator) finishReasoningSection(section *reasoningSection) {
	if section == nil || section.completed {
		return
	}
	if section.sawDelta {
		section.durationMS = time.Since(section.startedAt).Milliseconds()
		if section.durationMS < 1 {
			section.durationMS = 1
		}
	}
	section.completed = true
}

func (a *responseAccumulator) finishOpenReasoning() {
	for _, section := range a.reasoning {
		a.finishReasoningSection(section)
	}
}

func reasoningEventPayload(
	section *reasoningSection,
	delta string,
	text string,
) map[string]any {
	payload := map[string]any{
		"summaryIndex": section.summaryIndex,
		"outputIndex":  section.outputIndex,
		"completed":    section.completed,
	}
	if section.itemID != "" {
		payload["itemId"] = section.itemID
	}
	if delta != "" {
		payload["delta"] = delta
	}
	if text != "" {
		payload["text"] = text
	}
	if section.durationMS > 0 {
		payload["durationMs"] = section.durationMS
	}
	return payload
}

func safeWebAction(value any) json.RawMessage {
	input, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	output := make(map[string]string, 4)
	for _, key := range []string{"type", "query", "url", "pattern"} {
		raw, ok := input[key].(string)
		if !ok {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		limit := 500
		if key == "url" {
			raw = sanitizeCitationURL(raw)
			limit = 2048
		}
		if raw != "" {
			output[key] = truncateRunes(raw, limit)
		}
	}
	if len(output) == 0 {
		return nil
	}
	raw, _ := json.Marshal(output)
	return raw
}

func sanitizeProviderReplayItem(raw json.RawMessage) (json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	safe := sanitizeProviderReplayValue(value)
	encoded, err := json.Marshal(safe)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func sanitizeProviderReplayValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		safe := make(map[string]any, len(typed))
		for key, nested := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if sensitiveProviderReplayField(normalized) {
				continue
			}
			if normalized == "url" {
				rawURL, ok := nested.(string)
				if !ok {
					continue
				}
				cleanURL := sanitizeCitationURL(rawURL)
				if cleanURL != "" {
					safe[key] = cleanURL
				}
				continue
			}
			safe[key] = sanitizeProviderReplayValue(nested)
		}
		return safe
	case []any:
		safe := make([]any, len(typed))
		for index, nested := range typed {
			safe[index] = sanitizeProviderReplayValue(nested)
		}
		return safe
	default:
		return typed
	}
}

func sensitiveProviderReplayField(key string) bool {
	switch key {
	case "encrypted_content", "authorization", "cookie", "cookies", "headers",
		"request_headers", "response_headers":
		return true
	default:
		return unsafeEvidenceQueryParameter(key)
	}
}

func sanitizeCitationURL(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 2048 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.Host == "" || (scheme != "https" && scheme != "http") {
		return ""
	}
	parsed.Scheme = scheme
	parsed.User = nil
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		if unsafeEvidenceQueryParameter(key) {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.ForceQuery = false
	return parsed.String()
}

func unsafeEvidenceQueryParameter(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(key, "utm_") ||
		strings.HasPrefix(key, "x-amz-") ||
		strings.HasPrefix(key, "x-goog-") {
		return true
	}
	switch key {
	case "fbclid", "gclid", "dclid", "msclkid", "mc_cid", "mc_eid",
		"token", "access_token", "refresh_token", "id_token",
		"api_key", "apikey", "api-key", "client_secret", "secret",
		"signature", "sig", "session", "sessionid", "session_id",
		"auth", "authorization", "password", "passwd", "jwt",
		"credential", "security_token":
		return true
	default:
		return false
	}
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maximum {
		runes = runes[:maximum]
	}
	return string(runes)
}

func (a *responseAccumulator) readResponseMetadata(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var response struct {
		ID    string `json:"id"`
		Usage struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			OutputTokenDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	if response.ID != "" {
		a.responseID = response.ID
	}
	a.inputTokens = response.Usage.InputTokens
	a.outputTokens = response.Usage.OutputTokens
	a.reasoningTokens = response.Usage.OutputTokenDetails.ReasoningTokens
}

func (a *responseAccumulator) readFailure(responseRaw, errorRaw json.RawMessage) {
	a.failureCode = "provider_response_failed"
	if len(responseRaw) > 0 {
		var response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(responseRaw, &response) == nil && response.Error.Code != "" {
			a.failureCode = sanitizeCode(response.Error.Code)
		}
	}
	if len(errorRaw) > 0 {
		var upstream struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(errorRaw, &upstream) == nil && upstream.Code != "" {
			a.failureCode = sanitizeCode(upstream.Code)
		}
	}
}

func sanitizeCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' || current == '_' || current == '-' {
			result.WriteRune(current)
		}
		if result.Len() == 64 {
			break
		}
	}
	if result.Len() == 0 {
		return "provider_error"
	}
	return result.String()
}

func sanitizeOptionalCode(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return sanitizeCode(value)
}

func (a *responseAccumulator) parts() []store.NewMessagePart {
	a.finishOpenReasoning()
	parts := make([]store.NewMessagePart, 0, len(a.partOrder)+4)
	emitted := make(map[string]bool, len(a.partOrder)+4)
	appendPart := func(partType, key string) {
		identity := partType + "\x00" + key
		if emitted[identity] {
			return
		}
		switch partType {
		case "reasoning":
			for _, section := range a.reasoning {
				if section.key != key {
					continue
				}
				if section.text.Len() == 0 {
					return
				}
				raw, _ := json.Marshal(map[string]any{
					"durationMs":     section.durationMS,
					"providerItemId": section.itemID,
					"summaryIndex":   section.summaryIndex,
					"outputIndex":    section.outputIndex,
					"completed":      section.completed,
				})
				parts = append(parts, store.NewMessagePart{
					Type: "reasoning", TextContent: section.text.String(), JSONContent: raw,
				})
				return
			}
		case "text":
			if a.text.Len() == 0 {
				return
			}
			parts = append(parts, store.NewMessagePart{Type: "text", TextContent: a.text.String()})
		case "citations":
			if len(a.citations) == 0 {
				return
			}
			raw, _ := json.Marshal(map[string]any{"citations": a.citations})
			parts = append(parts, store.NewMessagePart{Type: "citations", JSONContent: raw})
		case "tool":
			for _, tool := range a.tools {
				if toolKey(tool) != key {
					continue
				}
				raw, _ := json.Marshal(tool)
				parts = append(parts, store.NewMessagePart{Type: "tool", JSONContent: raw})
				break
			}
		case "image":
			for _, image := range a.images {
				if image.AttachmentID == key {
					parts = append(parts, store.NewMessagePart{
						Type: "image", AttachmentID: image.AttachmentID,
					})
					break
				}
			}
		}
		emitted[identity] = true
	}
	for _, part := range a.partOrder {
		appendPart(part.Type, part.Key)
	}
	for _, section := range a.reasoning {
		appendPart("reasoning", section.key)
	}
	appendPart("text", "")
	appendPart("citations", "")
	for _, tool := range a.tools {
		appendPart("tool", toolKey(tool))
	}
	for _, image := range a.images {
		appendPart("image", image.AttachmentID)
	}
	return parts
}
