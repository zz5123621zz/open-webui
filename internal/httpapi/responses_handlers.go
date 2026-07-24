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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/owui-personal-slim/owui-personal-slim/internal/activecontext"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/jobs"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

const maxProviderEventBytes = 50 * 1024 * 1024

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	messages, err := s.store.ListMessages(r.Context(), session.User.ID, r.PathValue("id"))
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
	sentEffort := conversation.ReasoningEffort
	if sentEffort == "auto" {
		sentEffort = ""
	}

	lease, queuedStream, ok := s.acquireResponseJob(w, r, session.User.ID, conversationID)
	if !ok {
		return
	}
	defer lease.Release()

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
		assistantMessage, &userMessage, nil, queuedStream,
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
	sentEffort := conversation.ReasoningEffort
	if sentEffort == "auto" {
		sentEffort = ""
	}
	lease, queuedStream, ok := s.acquireResponseJob(w, r, session.User.ID, conversation.ID)
	if !ok {
		return
	}
	defer lease.Release()

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
		assistant, nil, history, queuedStream,
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
	assistantMessage store.Message,
	userMessage *store.Message,
	history []store.Message,
	stream *sseWriter,
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
	newAccumulator := func() responseAccumulator {
		return responseAccumulator{
			saveImage: func(encoded string) (generatedImage, error) {
				return s.saveGeneratedImage(context.WithoutCancel(r.Context()), userID, conversation.ID, assistantMessage.ID, encoded)
			},
		}
	}
	runProvider := func(request provider.ResponsesRequest, accumulator *responseAccumulator) (error, error) {
		upstream, startErr := s.startResponseWithNetworkRetry(r.Context(), request)
		if startErr != nil {
			return startErr, nil
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

	accumulator := newAccumulator()
	startErr, consumeErr := runProvider(providerRequest, &accumulator)
	contextRetry := isContextWindowError(startErr) ||
		(startErr == nil && isContextWindowCode(accumulator.failureCode) && !accumulator.hasVisibleOutput())
	if contextRetry {
		forced, forceErr := s.contexts.ForcePrepare(
			r.Context(), userID, conversation, model, sentEffort, history, assistantMessage.ID,
			func(status string, data map[string]any) error {
				data["status"] = status
				data["retry"] = true
				return stream.send("response.context", data)
			},
		)
		if forceErr != nil {
			_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "context_compaction_failed", nil)
			_ = stream.send("response.error", map[string]string{
				"code": "context_compaction_failed", "message": "The conversation is too large and could not be compacted safely.",
			})
			return
		}
		providerRequest, err = s.buildResponsesRequest(
			r.Context(), userID, conversation, model, sentEffort, forced.Checkpoint, forced.Messages,
		)
		if err != nil {
			_, _ = s.failAssistant(context.WithoutCancel(r.Context()), userID, assistantMessage.ID, "attachment_unavailable", nil)
			_ = stream.send("response.error", map[string]string{"code": "attachment_unavailable", "message": "An image could not be prepared."})
			return
		}
		accumulator = newAccumulator()
		startErr, consumeErr = runProvider(providerRequest, &accumulator)
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

func (s *Server) startResponseWithNetworkRetry(ctx context.Context, request provider.ResponsesRequest) (*http.Response, error) {
	response, err := s.models.StartResponse(ctx, request)
	if err == nil || !errors.Is(err, provider.ErrUnavailable) || ctx.Err() != nil {
		return response, err
	}
	return s.models.StartResponse(ctx, request)
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
	if isContextWindowError(err) {
		return "context_too_large", http.StatusRequestEntityTooLarge, "The conversation is still too large after compaction."
	}
	var upstream *provider.HTTPError
	if errors.As(err, &upstream) {
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
	if s.cfg.Tools.ImageGenerationEnabled {
		tools = append(tools, map[string]any{"type": "image_generation"})
	}
	return provider.ResponsesRequest{
		Model: conversation.Model, SafetyIdentifier: s.providerSafetyIdentifier(userID),
		Input: input, Stream: true, Store: false,
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
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, raw); err != nil {
		return err
	}
	return s.controller.Flush()
}

type providerStreamEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	ItemID   string          `json:"item_id"`
	Item     json.RawMessage `json:"item"`
	Response json.RawMessage `json:"response"`
	Error    json.RawMessage `json:"error"`
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
	responseID          string
	text                strings.Builder
	reasoning           strings.Builder
	reasoningStarted    time.Time
	reasoningDurationMS int64
	citations           []citation
	tools               []toolSnapshot
	toolStarted         map[string]time.Time
	images              []generatedImage
	partOrder           []accumulatorPart
	inputTokens         int64
	outputTokens        int64
	reasoningTokens     int64
	completed           bool
	failureCode         string
	saveImage           func(string) (generatedImage, error)
	providerItems       []store.NewProviderItem
}

type accumulatorPart struct {
	Type string
	Key  string
}

func (a *responseAccumulator) hasVisibleOutput() bool {
	return a.text.Len() > 0 || a.reasoning.Len() > 0 || len(a.tools) > 0 ||
		len(a.toolStarted) > 0 || len(a.images) > 0
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
		a.finishReasoning()
		a.ensurePart("text", "")
		a.text.WriteString(event.Delta)
		return stream.send("response.text.delta", map[string]string{"delta": event.Delta})
	case "response.reasoning_summary_text.delta":
		if a.reasoningStarted.IsZero() {
			a.reasoningStarted = time.Now()
		}
		a.ensurePart("reasoning", "")
		a.reasoning.WriteString(event.Delta)
		return stream.send("response.reasoning.delta", map[string]string{"delta": event.Delta})
	case "response.output_item.added":
		return a.handleItem(stream, event.Item, "in_progress")
	case "response.output_item.done":
		a.captureProviderItem(event.Item)
		return a.handleItem(stream, event.Item, "completed")
	case "response.completed":
		a.finishReasoning()
		a.completed = true
		a.readResponseMetadata(event.Response)
	case "response.failed", "response.incomplete":
		a.finishReasoning()
		a.readFailure(event.Response, event.Error)
	case "error":
		a.finishReasoning()
		a.readFailure(nil, event.Error)
	default:
		if strings.Contains(event.Type, "web_search_call") {
			a.finishReasoning()
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
	case "message", "reasoning", "web_search_call":
		a.providerItems = append(a.providerItems, store.NewProviderItem{
			ItemType: header.Type, ReplayJSON: append(json.RawMessage(nil), raw...),
		})
	}
}

func (a *responseAccumulator) handleItem(stream *sseWriter, raw json.RawMessage, fallbackStatus string) error {
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
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil
	}
	status := item.Status
	if status == "" {
		status = fallbackStatus
	}
	if a.toolStarted == nil {
		a.toolStarted = make(map[string]time.Time)
	}
	if status == "in_progress" && item.ID != "" {
		if _, exists := a.toolStarted[item.ID]; !exists {
			a.toolStarted[item.ID] = time.Now()
		}
	}
	var durationMS int64
	if started, exists := a.toolStarted[item.ID]; exists && status != "in_progress" {
		durationMS = time.Since(started).Milliseconds()
		delete(a.toolStarted, item.ID)
	}
	switch item.Type {
	case "web_search_call":
		a.finishReasoning()
		safeData := safeWebAction(item.Action)
		snapshot := toolSnapshot{
			CallID: item.ID, Type: "web_search", Status: status, Data: safeData,
			DurationMS: durationMS, ErrorCode: sanitizeOptionalCode(item.Error.Code),
		}
		a.recordTool(snapshot)
		return stream.send("response.tool", snapshot)
	case "image_generation_call":
		a.finishReasoning()
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
					a.finishReasoning()
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

func (a *responseAccumulator) finishReasoning() {
	if a.reasoningStarted.IsZero() || a.reasoningDurationMS > 0 {
		return
	}
	a.reasoningDurationMS = time.Since(a.reasoningStarted).Milliseconds()
	if a.reasoningDurationMS < 1 {
		a.reasoningDurationMS = 1
	}
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

func sanitizeCitationURL(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 2048 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	parsed.User = nil
	return parsed.String()
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
	a.finishReasoning()
	parts := make([]store.NewMessagePart, 0, len(a.partOrder)+4)
	emitted := make(map[string]bool, len(a.partOrder)+4)
	appendPart := func(partType, key string) {
		identity := partType + "\x00" + key
		if emitted[identity] {
			return
		}
		switch partType {
		case "reasoning":
			if a.reasoning.Len() == 0 {
				return
			}
			raw, _ := json.Marshal(map[string]int64{"durationMs": a.reasoningDurationMS})
			parts = append(parts, store.NewMessagePart{
				Type: "reasoning", TextContent: a.reasoning.String(), JSONContent: raw,
			})
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
	appendPart("reasoning", "")
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
