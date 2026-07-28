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
	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/jobs"
	"github.com/owui-personal-slim/owui-personal-slim/internal/progressivesummary"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

const (
	maxProviderEventBytes            = 50 * 1024 * 1024
	sseHeartbeatInterval             = 15 * time.Second
	providerContinuationInstructions = `

The immediately preceding assistant draft was interrupted by a transient upstream server failure after some text had already been delivered. Continue only the missing remainder from the exact cutoff. Do not repeat the introduction, headings, completed rows, searches, or any other material already present. Do not call tools. Finish the requested deliverable succinctly and in the same language and format as the draft.`
	responseInstructions = `Reply in the language used by the latest user message. When the latest user message is primarily Chinese, write the final answer and every user-visible reasoning summary in Simplified Chinese. Make user-visible reasoning summaries clear and informative when the provider supports them, but never reveal private chain-of-thought; summarize only the approach, checks, and current progress.

Use a China-first web-search strategy when the subject is in mainland China or the user asks about a Chinese local place, business, person, event, policy, or service. Start with Simplified Chinese queries and include any known city, province, or district. Prioritize current mainland first-party or official sources, government websites, and official accounts or pages. For local businesses, Chinese map and local platforms such as 高德地图、百度地图、大众点评、美团和小红书 may be used for discovery, but distinguish user-generated content from verified facts and cross-check addresses, opening hours, and other practical details against an official source or at least two independent recent local sources when possible. Do not substitute a same-named foreign entity or search primarily non-Chinese sites when relevant Chinese sources exist. Ask for the city or location when the entity is ambiguous. For genuinely global topics, or when credible Chinese sources are unavailable, use the best international sources and state that limitation. When web search is used, cite the sources actually relied on and distinguish official facts from reviews or other user-generated claims.`
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
	Text               string                       `json:"text"`
	AttachmentIDs      []string                     `json:"attachmentIds"`
	RequestID          string                       `json:"requestId"`
	GenerateImage      bool                         `json:"generateImage"`
	GuidanceSubmission *guidance.GuidanceSubmission `json:"guidanceSubmission"`
}

type regenerateResponseRequest struct {
	RequestID      string `json:"requestId"`
	BypassGuidance bool   `json:"bypassGuidance"`
}

// ensureStreamRequestID fills in a generated request id when absent and
// validates it, writing the error response itself on failure.
func (s *Server) ensureStreamRequestID(w http.ResponseWriter, requestID string) (string, bool) {
	if requestID == "" {
		generated, err := ids.New()
		if err != nil {
			s.internalError(w, "generate request id", err)
			return "", false
		}
		return generated, true
	}
	if len(requestID) > 128 || !utf8.ValidString(requestID) {
		writeError(w, http.StatusBadRequest, "invalid_request_id", "Request ID is invalid.")
		return "", false
	}
	return requestID, true
}

// resolveConversationModel loads the catalog and validates the conversation's
// model and reasoning effort, writing the error response on failure.
func (s *Server) resolveConversationModel(
	w http.ResponseWriter,
	r *http.Request,
	conversation store.Conversation,
) (provider.Model, bool) {
	catalog, err := s.models.Models(r.Context())
	if err != nil {
		s.providerCatalogError(w, err)
		return provider.Model{}, false
	}
	model, ok := s.models.FindSelectable(catalog, conversation.Model)
	if !ok {
		writeError(w, http.StatusBadRequest, "provider_model_unavailable", "The conversation model is no longer available.")
		return provider.Model{}, false
	}
	if !provider.SupportsEffort(model, conversation.ReasoningEffort) {
		writeError(w, http.StatusBadRequest, "reasoning_effort_unsupported", "The conversation reasoning effort is no longer supported.")
		return provider.Model{}, false
	}
	return model, true
}

func (s *Server) createResponse(w http.ResponseWriter, r *http.Request) {
	var request createResponseRequest
	if !readJSON(w, r, &request) {
		return
	}
	structured := request.GuidanceSubmission != nil
	if !structured && strings.TrimSpace(request.Text) == "" && len(request.AttachmentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "message_required", "Message text or an image is required.")
		return
	}
	if structured &&
		(strings.TrimSpace(request.Text) != "" ||
			len(request.AttachmentIDs) != 0 ||
			request.GenerateImage) {
		writeError(
			w, http.StatusBadRequest, "invalid_guidance_submission",
			"A guidance submission cannot include message text, attachments, or image generation.",
		)
		return
	}
	if structured && !s.cfg.Tools.RestaurantGuidanceEnabled {
		writeError(
			w, http.StatusConflict, "guidance_disabled",
			"Restaurant guidance is currently disabled.",
		)
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
	requestID, ok := s.ensureStreamRequestID(w, request.RequestID)
	if !ok {
		return
	}
	request.RequestID = requestID

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

	model, ok := s.resolveConversationModel(w, r, conversation)
	if !ok {
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

	lease, queuedStream, ok := s.acquireResponseJob(w, r, session.User.ID, conversationID)
	if !ok {
		return
	}
	defer lease.Release()
	summaryMode := s.progressiveSummaryMode(r.Context())

	var userMessage, assistantMessage store.Message
	if structured {
		userMessage, assistantMessage, err = s.store.BeginGuidanceResponse(
			r.Context(), session.User.ID, conversationID, request.RequestID,
			*request.GuidanceSubmission, conversation.Model,
			conversation.ReasoningEffort, sentEffort,
		)
	} else {
		userMessage, assistantMessage, err = s.store.BeginResponse(
			r.Context(), session.User.ID, conversationID, request.RequestID,
			request.Text, conversation.Model, conversation.ReasoningEffort, sentEffort,
			request.AttachmentIDs,
		)
	}
	if errors.Is(err, store.ErrDuplicateRequest) {
		respondBeforeStreamStart(
			queuedStream, w, http.StatusConflict, "duplicate_request",
			"This request has already been submitted.",
		)
		return
	}
	if errors.Is(err, store.ErrStaleGuidance) {
		respondBeforeStreamStart(
			queuedStream, w, http.StatusConflict, "stale_guidance",
			"This guidance card is no longer actionable.",
		)
		return
	}
	if structured && errors.Is(err, store.ErrNotFound) {
		respondBeforeStreamStart(
			queuedStream, w, http.StatusNotFound, "not_found",
			"The guidance card was not found.",
		)
		return
	}
	if structured && err != nil {
		s.logger.Warn("guidance submission rejected", "error", err)
		respondBeforeStreamStart(
			queuedStream, w, http.StatusBadRequest, "invalid_guidance_submission",
			"The selected guidance answers could not be accepted.",
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
		w, r, clientContext, cancelResponse,
		session.User.ID, request.RequestID, conversation, model, sentEffort,
		summaryMode, map[bool]string{true: "guidance", false: "create"}[structured],
		assistantMessage, &userMessage, nil, queuedStream,
		request.GenerateImage, request.Text, false,
	)
}

func (s *Server) regenerateResponse(w http.ResponseWriter, r *http.Request) {
	var request regenerateResponseRequest
	if !readJSON(w, r, &request) {
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
		s.internalError(w, "get response for regeneration", err)
		return
	}
	if request.BypassGuidance && original.ErrorCode != "invalid_guidance_output" {
		writeError(
			w, http.StatusBadRequest, "guidance_bypass_not_allowed",
			"Guidance can only be bypassed after an invalid guidance output.",
		)
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

	model, ok := s.resolveConversationModel(w, r, conversation)
	if !ok {
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
		s.logger.Warn("begin regeneration rejected", "error", err)
		respondBeforeStreamStart(
			queuedStream, w, http.StatusBadRequest, "response_not_regenerable",
			"This response cannot be regenerated.",
		)
		return
	}
	s.streamAssistantResponse(
		w, r, clientContext, cancelResponse,
		session.User.ID, request.RequestID, conversation, model, sentEffort,
		summaryMode, "regenerate", assistant, nil, history, queuedStream, generateImage,
		latestUserText(history), request.BypassGuidance,
	)
}

func (s *Server) streamAssistantResponse(
	w http.ResponseWriter,
	r *http.Request,
	clientContext context.Context,
	cancelResponse context.CancelCauseFunc,
	userID string,
	requestID string,
	conversation store.Conversation,
	model provider.Model,
	sentEffort string,
	summaryMode string,
	mode string,
	assistantMessage store.Message,
	userMessage *store.Message,
	history []store.Message,
	stream *sseWriter,
	generateImage bool,
	imagePrompt string,
	forceGuidanceFinal bool,
) {
	s.registerResponse(assistantMessage.ID, userID, cancelResponse)
	defer s.unregisterResponse(assistantMessage.ID)

	if stream == nil {
		stream = newResponseSSEWriter(w)
		stream.start()
	}
	// mode names the flow explicitly ("create", "regenerate", "edit") instead
	// of clients inferring it from which fields happen to be present.
	started := map[string]any{
		"requestId": requestID, "assistantMessage": assistantMessage, "mode": mode,
	}
	if userMessage != nil {
		started["userMessage"] = *userMessage
	} else {
		started["regenerated"] = true
	}
	_ = stream.send("response.started", started)
	initialStage := "preparing_context"
	if generateImage && model.ImageGenerationMode == "dedicated" {
		initialStage = "generating_image"
	}
	_ = stream.send("response.stage", map[string]string{"stage": initialStage})
	stopHeartbeat := stream.startHeartbeat(clientContext, sseHeartbeatInterval, func() {})
	defer stopHeartbeat()
	completeInterruption := func(parts []store.NewMessagePart) {
		code := responseInterruptionCode(r.Context())
		finalContext, cancelFinal := context.WithTimeout(
			context.WithoutCancel(r.Context()), 5*time.Second,
		)
		finalMessage, err := s.store.CompleteAssistant(
			finalContext, userID, assistantMessage.ID, store.AssistantResult{
				Status: "interrupted", ErrorCode: code, Parts: parts,
			},
		)
		cancelFinal()
		if err != nil {
			s.logger.Error(
				"store interrupted assistant failed",
				"error", err,
				"message_id", assistantMessage.ID,
			)
		}
		payload := map[string]any{
			"code": code, "message": "The response ended before completion.",
		}
		if err == nil {
			payload["messageRecord"] = finalMessage
		}
		_ = stream.send("response.error", payload)
	}

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
			if r.Context().Err() != nil {
				completeInterruption(nil)
				return
			}
			_, _ = s.failAssistant(r.Context(), userID, assistantMessage.ID, "history_unavailable", nil)
			s.logger.Error("load response history failed", "error", err)
			_ = stream.send("response.error", map[string]string{"code": "history_unavailable", "message": "Conversation history could not be loaded."})
			return
		}
	}
	guidanceState, err := s.guidanceRuntime(
		r.Context(), userID, history, forceGuidanceFinal, generateImage,
	)
	if err != nil {
		if r.Context().Err() != nil {
			completeInterruption(nil)
			return
		}
		_, _ = s.failAssistant(
			context.WithoutCancel(r.Context()), userID, assistantMessage.ID,
			"guidance_state_unavailable", nil,
		)
		s.logger.Error("prepare guidance state failed", "error", err)
		_ = stream.send("response.error", map[string]string{
			"code":    "guidance_state_unavailable",
			"message": "Restaurant guidance state could not be prepared.",
		})
		return
	}
	active, err := s.contexts.Prepare(
		r.Context(), userID, conversation, model, sentEffort, history, assistantMessage.ID,
		func(status string, data map[string]any) error {
			data["status"] = status
			return stream.send("response.context", data)
		},
	)
	if err != nil {
		if r.Context().Err() != nil {
			completeInterruption(nil)
			return
		}
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
	providerRequest, err := s.buildResponsesRequest(
		r.Context(), userID, conversation, model, sentEffort,
		active.Checkpoint, active.Messages, guidanceState,
	)
	if err != nil {
		if r.Context().Err() != nil {
			completeInterruption(nil)
			return
		}
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
	_ = stream.send("response.stage", map[string]string{"stage": "waiting_for_model"})
	newAccumulator := func() responseAccumulator {
		return responseAccumulator{
			guidanceRuntime:    guidanceState,
			guidanceInstanceID: assistantMessage.ID,
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
		lastProgressSave := time.Time{}
		consumeErr := consumeProviderSSE(upstream.Body, func(event providerStreamEvent) error {
			if err := accumulator.handle(stream, event); err != nil {
				return err
			}
			if !lastProgressSave.IsZero() &&
				time.Since(lastProgressSave) < time.Second {
				return nil
			}
			progressParts := accumulator.progressParts()
			if len(progressParts) == 0 && len(accumulator.providerItems) == 0 {
				return nil
			}
			progressContext, cancelProgress := context.WithTimeout(
				context.WithoutCancel(r.Context()), 5*time.Second,
			)
			_, progressErr := s.store.SaveAssistantProgress(
				progressContext, userID, assistantMessage.ID, store.AssistantResult{
					ProviderResponseID: accumulator.responseID,
					InputTokens:        accumulator.inputTokens,
					OutputTokens:       accumulator.outputTokens,
					ReasoningTokens:    accumulator.reasoningTokens,
					Parts:              progressParts,
					ProviderItems:      accumulator.providerItems,
				},
			)
			cancelProgress()
			if progressErr != nil {
				accumulator.failureCode = "persistence_failed"
				return progressErr
			}
			lastProgressSave = time.Now()
			return nil
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
				if r.Context().Err() != nil {
					completeInterruption(nil)
					return
				}
				_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "context_compaction_failed", nil)
				_ = stream.send("response.error", map[string]string{
					"code": "context_compaction_failed", "message": "The conversation is too large and could not be compacted safely.",
				})
				return
			}
			providerRequest, err = s.buildResponsesRequest(
				r.Context(), userID, conversation, model, sentEffort,
				forced.Checkpoint, forced.Messages, guidanceState,
			)
			if err != nil {
				s.summaries.MarkInconclusive(summaryDecision)
				if r.Context().Err() != nil {
					completeInterruption(nil)
					return
				}
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
	continuedAfterTransientFailure := false
	if startErr == nil &&
		consumeErr == nil &&
		canSafelyContinueProviderResponse(
			&accumulator,
			guidanceState,
			generateImage,
		) {
		firstInputTokens := accumulator.inputTokens
		firstOutputTokens := accumulator.outputTokens
		firstReasoningTokens := accumulator.reasoningTokens
		originalFailureCode := accumulator.failureCode
		continuationRequest := providerContinuationRequest(
			providerRequest,
			accumulator.text.String(),
		)
		accumulator.failureCode = ""
		accumulator.completed = false
		// The continuation belongs to a new provider response lineage. Clear
		// replay items before it starts so an in-progress save can never mix
		// items from two responses. The combined visible text remains the
		// canonical history if the continuation is interrupted again.
		accumulator.responseTextStart = accumulator.text.Len()
		accumulator.suppressProviderItems = true
		accumulator.providerItems = nil
		_ = stream.send(
			"response.stage",
			map[string]string{"stage": "continuing_answer"},
		)
		s.logger.Warn(
			"continuing partial response after transient provider failure",
			"failure_code", originalFailureCode,
			"conversation_id", conversation.ID,
		)
		continuationStartErr, continuationConsumeErr := runProvider(
			continuationRequest,
			&accumulator,
		)
		if continuationStartErr != nil {
			accumulator.failureCode = originalFailureCode
			s.logger.Warn(
				"provider continuation could not start",
				"error", continuationStartErr,
				"conversation_id", conversation.ID,
			)
		} else {
			continuedAfterTransientFailure = true
			consumeErr = continuationConsumeErr
			accumulator.inputTokens += firstInputTokens
			accumulator.outputTokens += firstOutputTokens
			accumulator.reasoningTokens += firstReasoningTokens
		}
	}
	accumulator.finalizeGuidance()
	if startErr != nil {
		if r.Context().Err() != nil {
			completeInterruption(nil)
			return
		}
		code, _, message := providerStartError(startErr)
		_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, code, nil)
		_ = stream.send("response.error", map[string]string{"code": code, "message": message})
		return
	}

	status := "completed"
	errorCode := ""
	if consumeErr != nil {
		if r.Context().Err() != nil {
			status = "interrupted"
			errorCode = responseInterruptionCode(r.Context())
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
		"continued_after_transient_failure", continuedAfterTransientFailure,
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
	_ = stream.send("response.tool", running)
	rawRunning, _ := json.Marshal(running)
	progressContext, cancelProgress := context.WithTimeout(
		context.WithoutCancel(ctx), 5*time.Second,
	)
	_, progressErr := s.store.SaveAssistantProgress(
		progressContext, userID, messageID, store.AssistantResult{
			Parts: []store.NewMessagePart{{
				Type: "tool", JSONContent: rawRunning,
			}},
		},
	)
	cancelProgress()
	if progressErr != nil {
		_, _ = s.failAssistant(
			context.WithoutCancel(ctx), userID, messageID,
			"persistence_failed", []store.NewMessagePart{{
				Type: "tool", JSONContent: rawRunning,
			}},
		)
		_ = stream.send("response.error", map[string]string{
			"code":    "persistence_failed",
			"message": "The image response could not be tracked safely.",
		})
		return
	}

	result, generationErr := s.models.GenerateImage(ctx, provider.ImageGenerationRequest{
		Model: imageModel, Prompt: strings.TrimSpace(prompt),
	})
	if generationErr != nil {
		code, _, message := providerStartError(generationErr)
		status := "error"
		if ctx.Err() != nil {
			code = responseInterruptionCode(ctx)
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
	if ctx.Err() != nil {
		code := responseInterruptionCode(ctx)
		failed := running
		failed.Status = "failed"
		failed.DurationMS = time.Since(startedAt).Milliseconds()
		failed.ErrorCode = code
		finalMessage, _ := s.completeDedicatedImageFailure(
			context.WithoutCancel(ctx), userID, messageID, failed, code, "interrupted",
		)
		_ = stream.send("response.tool", failed)
		_ = stream.send("response.error", map[string]any{
			"code": code, "message": "The image request ended before completion.",
			"messageRecord": finalMessage,
		})
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
			stream = newResponseSSEWriter(w)
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

func (s *Server) buildResponsesRequest(
	ctx context.Context,
	userID string,
	conversation store.Conversation,
	model provider.Model,
	effort string,
	checkpoint *store.ContextCheckpoint,
	messages []store.Message,
	guidanceState guidance.Runtime,
) (provider.ResponsesRequest, error) {
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
				switch {
				case part.Type == "image" && part.AttachmentID != "":
					generatedNotes = append(generatedNotes, "[Generated image attachment "+part.AttachmentID+"]")
				case part.Type == guidance.PartClarification ||
					part.Type == guidance.PartTaskBrief ||
					part.Type == guidance.PartGuidanceError:
					if strings.TrimSpace(part.TextContent) != "" {
						generatedNotes = append(generatedNotes, part.TextContent)
					}
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
			case "text", guidance.PartClarification,
				guidance.PartClarificationSubmission, guidance.PartTaskBrief,
				guidance.PartGuidanceError:
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
	toolChoice := "auto"
	if guidanceState.Enabled &&
		!guidanceState.FinalAnswer &&
		(guidanceState.AllowClarification || guidanceState.AllowTaskBrief) {
		guidanceTools := guidance.ToolDefinitions(guidanceState)
		if guidanceState.RequireClarification || guidanceState.RequireTaskBrief {
			// A user-selected guidance transition must be deterministic. Keep
			// only the one allowed control tool so tool_choice=required cannot
			// select web search, image generation, or the wrong control.
			tools = guidanceTools
			toolChoice = "required"
		} else {
			tools = append(tools, guidanceTools...)
		}
	}
	return provider.ResponsesRequest{
		Model:            conversation.Model,
		Instructions:     responseInstructions + guidance.CompileInstructions(guidanceState),
		SafetyIdentifier: s.providerSafetyIdentifier(userID),
		Input:            input, Stream: true, Store: false,
		Reasoning: provider.ReasoningOptions{Effort: effort, Summary: "auto"},
		Tools:     tools, ToolChoice: toolChoice,
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
	bestEffort bool
	failed     bool
}

func newSSEWriter(w http.ResponseWriter) *sseWriter {
	return &sseWriter{writer: w, controller: http.NewResponseController(w)}
}

func newResponseSSEWriter(w http.ResponseWriter) *sseWriter {
	stream := newSSEWriter(w)
	stream.bestEffort = true
	return stream
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
	if s.failed && s.bestEffort {
		return nil
	}
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, raw); err != nil {
		s.failed = true
		if s.bestEffort {
			return nil
		}
		return err
	}
	if err := s.controller.Flush(); err != nil {
		s.failed = true
		if s.bestEffort {
			return nil
		}
		return err
	}
	return nil
}

func (s *sseWriter) comment(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed && s.bestEffort {
		return io.ErrClosedPipe
	}
	if _, err := fmt.Fprintf(s.writer, ": %s\n\n", value); err != nil {
		s.failed = true
		return err
	}
	if err := s.controller.Flush(); err != nil {
		s.failed = true
		return err
	}
	return nil
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
	Code           string          `json:"code"`
	Message        string          `json:"message"`
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
	responseID            string
	text                  strings.Builder
	reasoning             []*reasoningSection
	reasoningByKey        map[string]*reasoningSection
	citations             []citation
	tools                 []toolSnapshot
	toolStarted           map[string]time.Time
	images                []generatedImage
	partOrder             []accumulatorPart
	inputTokens           int64
	outputTokens          int64
	reasoningTokens       int64
	completed             bool
	failureCode           string
	saveImage             func(string) (generatedImage, error)
	providerItems         []store.NewProviderItem
	guidanceRuntime       guidance.Runtime
	guidanceInstanceID    string
	guidanceCalls         []guidanceCall
	guidanceControlPart   *guidance.ControlPart
	responseTextStart     int
	suppressProviderItems bool
}

type guidanceCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
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
	case "response.failed":
		a.finishOpenReasoning()
		a.readFailure(event.Response, event.Error, event.Code)
	case "response.incomplete":
		a.finishOpenReasoning()
		a.readIncomplete(event.Response, event.Code)
	case "error":
		a.finishOpenReasoning()
		a.readFailure(nil, event.Error, event.Code)
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
	if a.suppressProviderItems || len(raw) == 0 {
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
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Status    string          `json:"status"`
		Result    string          `json:"result"`
		Action    any             `json:"action"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Error     struct {
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
	case "function_call":
		if fallbackStatus == "completed" && a.guidanceRuntime.Enabled {
			arguments, ok := functionCallArguments(item.Arguments)
			if !ok {
				arguments = nil
			}
			call := guidanceCall{
				ID: item.ID, Name: item.Name, Arguments: arguments,
			}
			for index := range a.guidanceCalls {
				if call.ID != "" && a.guidanceCalls[index].ID == call.ID {
					a.guidanceCalls[index] = call
					return nil
				}
			}
			a.guidanceCalls = append(a.guidanceCalls, call)
		}
		return nil
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
		if a.text.Len() == a.responseTextStart {
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

func functionCallArguments(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, false
	}
	arguments := json.RawMessage(encoded)
	return arguments, json.Valid(arguments)
}

func (a *responseAccumulator) finalizeGuidance() {
	if !a.guidanceRuntime.Enabled {
		return
	}
	controlRequired := a.guidanceRuntime.RequireClarification ||
		a.guidanceRuntime.RequireTaskBrief
	if len(a.guidanceCalls) == 0 && !controlRequired {
		return
	}
	invalid := len(a.guidanceCalls) != 1 || strings.TrimSpace(a.text.String()) != ""
	var control guidance.ControlPart
	if !invalid {
		call := a.guidanceCalls[0]
		switch call.Name {
		case guidance.ToolShowClarificationCards:
			invalid = !a.guidanceRuntime.AllowClarification
		case guidance.ToolShowTaskBrief:
			invalid = !a.guidanceRuntime.AllowTaskBrief
		default:
			invalid = true
		}
		if len(call.Arguments) == 0 {
			invalid = true
		}
		if !invalid {
			var err error
			control, err = guidance.ParseControlCall(
				call.Name,
				call.Arguments,
				a.guidanceInstanceID,
				a.guidanceRuntime,
			)
			invalid = err != nil
		}
	}
	if invalid {
		fallback := guidance.GuidanceErrorPart("invalid_guidance_output")
		a.guidanceControlPart = &fallback
		a.failureCode = "invalid_guidance_output"
		a.providerItems = nil
		return
	}
	if strings.TrimSpace(a.text.String()) == "" {
		a.text.Reset()
	}
	a.guidanceControlPart = &control
	a.ensurePart(control.Type, "")
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

func (a *responseAccumulator) readFailure(
	responseRaw,
	errorRaw json.RawMessage,
	topLevelCode string,
) {
	a.readResponseMetadata(responseRaw)
	a.failureCode = "provider_response_failed"
	a.applyFailureCode(topLevelCode)
	if len(responseRaw) > 0 {
		var response struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(responseRaw, &response) == nil {
			a.applyFailureCode(response.Error.Type)
			a.applyFailureCode(response.Error.Code)
		}
	}
	if len(errorRaw) > 0 {
		var upstream struct {
			Code string `json:"code"`
			Type string `json:"type"`
		}
		if json.Unmarshal(errorRaw, &upstream) == nil {
			a.applyFailureCode(upstream.Type)
			a.applyFailureCode(upstream.Code)
		}
	}
}

func (a *responseAccumulator) readIncomplete(
	responseRaw json.RawMessage,
	topLevelCode string,
) {
	a.readResponseMetadata(responseRaw)
	a.failureCode = "provider_response_incomplete"
	a.applyFailureCode(topLevelCode)
	if len(responseRaw) == 0 {
		return
	}
	var response struct {
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if json.Unmarshal(responseRaw, &response) == nil {
		a.applyFailureCode(response.IncompleteDetails.Reason)
	}
}

func (a *responseAccumulator) applyFailureCode(value string) {
	if strings.TrimSpace(value) != "" {
		a.failureCode = sanitizeCode(value)
	}
}

func canSafelyContinueProviderResponse(
	accumulator *responseAccumulator,
	guidanceState guidance.Runtime,
	generateImage bool,
) bool {
	if accumulator == nil ||
		!guidanceState.FinalAnswer ||
		generateImage ||
		accumulator.completed ||
		!isTransientProviderFailure(accumulator.failureCode) ||
		strings.TrimSpace(accumulator.text.String()) == "" ||
		len(accumulator.images) > 0 ||
		len(accumulator.guidanceCalls) > 0 ||
		accumulator.guidanceControlPart != nil {
		return false
	}
	for _, tool := range accumulator.tools {
		if tool.Type != "web_search" {
			return false
		}
	}
	return true
}

func isTransientProviderFailure(code string) bool {
	switch sanitizeCode(code) {
	case "server_error", "internal_server_error", "bad_gateway":
		return true
	default:
		return false
	}
}

func providerContinuationRequest(
	request provider.ResponsesRequest,
	partialText string,
) provider.ResponsesRequest {
	continuation := request
	continuation.Instructions += providerContinuationInstructions
	continuation.Input = append(
		append([]provider.ResponseInput(nil), request.Input...),
		provider.ResponseInput{
			Role: "assistant",
			Content: []provider.ResponseContent{{
				Type: "output_text",
				Text: partialText,
			}},
		},
		provider.ResponseInput{
			Role:    "developer",
			Content: "Continue the interrupted assistant draft according to the continuation instructions.",
		},
	)
	continuation.Tools = nil
	continuation.ToolChoice = ""
	return continuation
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
	return a.buildParts()
}

func (a *responseAccumulator) progressParts() []store.NewMessagePart {
	return a.buildParts()
}

func (a *responseAccumulator) buildParts() []store.NewMessagePart {
	if a.failureCode == "invalid_guidance_output" && a.guidanceControlPart != nil {
		return []store.NewMessagePart{{
			Type:        a.guidanceControlPart.Type,
			TextContent: a.guidanceControlPart.Text,
			JSONContent: a.guidanceControlPart.Data,
		}}
	}
	parts := make([]store.NewMessagePart, 0, len(a.partOrder)+4)
	emitted := make(map[string]bool, len(a.partOrder)+4)
	appendPart := func(partType, key string) {
		identity := partType + "\x00" + key
		if emitted[identity] {
			return
		}
		emitted[identity] = true
		switch partType {
		case guidance.PartClarification, guidance.PartTaskBrief:
			if a.guidanceControlPart == nil ||
				a.guidanceControlPart.Type != partType {
				return
			}
			parts = append(parts, store.NewMessagePart{
				Type:        a.guidanceControlPart.Type,
				TextContent: a.guidanceControlPart.Text,
				JSONContent: a.guidanceControlPart.Data,
			})
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
	if a.guidanceControlPart != nil {
		appendPart(a.guidanceControlPart.Type, "")
	}
	return parts
}
