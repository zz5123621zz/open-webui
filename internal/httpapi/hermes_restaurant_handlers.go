package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/speech"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

const (
	hermesRestaurantAudioTTL      = 24 * time.Hour
	maxHermesTurnTextBytes        = 64 * 1024
	maxHermesRestaurantAudioFiles = 32
	maxHermesRestaurantAudioBytes = 25 * 1024 * 1024
	maxHermesRestaurantAudioTotal = 100 * 1024 * 1024
)

const hermesRestaurantCredentialContextKey contextKey = "hermes-restaurant-credential"

type hermesRestaurantTurnRequest struct {
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

type hermesRestaurantTurnResponse struct {
	RequestID string                        `json:"requestId"`
	Kind      string                        `json:"kind"`
	Text      string                        `json:"text"`
	Audio     hermesRestaurantAudioResponse `json:"audio"`
}

type hermesRestaurantAudioResponse struct {
	Status string                      `json:"status"`
	Code   string                      `json:"code"`
	Files  []hermesRestaurantAudioFile `json:"files"`
}

type hermesRestaurantAudioFile struct {
	ID           string `json:"id"`
	FileName     string `json:"fileName"`
	ContentType  string `json:"contentType"`
	ByteSize     int64  `json:"byteSize"`
	DownloadPath string `json:"downloadPath"`
}

func (s *Server) hermesRestaurantAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerValues := r.Header.Values("Authorization")
		if len(headerValues) != 1 ||
			!strings.HasPrefix(headerValues[0], "Bearer ") {
			writeError(
				w,
				http.StatusUnauthorized,
				"integration_authentication_required",
				"A valid integration credential is required.",
			)
			return
		}
		rawToken := strings.TrimPrefix(headerValues[0], "Bearer ")
		if strings.TrimSpace(rawToken) != rawToken ||
			strings.ContainsAny(rawToken, " \t\r\n") {
			writeError(
				w,
				http.StatusUnauthorized,
				"integration_authentication_required",
				"A valid integration credential is required.",
			)
			return
		}
		credential, err := s.store.AuthenticateHermesRestaurantCredential(
			r.Context(),
			rawToken,
		)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.logger.Error(
					"hermes restaurant credential lookup failed",
					"error",
					err,
				)
			}
			writeError(
				w,
				http.StatusUnauthorized,
				"integration_authentication_required",
				"A valid integration credential is required.",
			)
			return
		}
		if metadata, ok := requestMetadataFromContext(r.Context()); ok {
			metadata.userIDHash = hashIdentifier(credential.UserID)
		}
		ctx := context.WithValue(
			r.Context(),
			hermesRestaurantCredentialContextKey,
			credential,
		)
		ctx = withSession(ctx, store.Session{User: credential.User})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func hermesRestaurantCredentialFromContext(
	ctx context.Context,
) (store.HermesRestaurantCredential, bool) {
	credential, ok := ctx.Value(
		hermesRestaurantCredentialContextKey,
	).(store.HermesRestaurantCredential)
	return credential, ok
}

func (s *Server) hermesRestaurantTurn(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Tools.RestaurantGuidanceEnabled {
		writeError(
			w,
			http.StatusConflict,
			"guidance_disabled",
			"Restaurant guidance is currently disabled.",
		)
		return
	}
	contentType, _, contentTypeErr := mime.ParseMediaType(
		r.Header.Get("Content-Type"),
	)
	if contentTypeErr != nil || contentType != "application/json" {
		writeError(
			w,
			http.StatusUnsupportedMediaType,
			"unsupported_media_type",
			"Content-Type must be application/json.",
		)
		return
	}
	var request hermesRestaurantTurnRequest
	if !readJSON(w, r, &request) {
		return
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Text = strings.TrimSpace(request.Text)
	if !validHermesBridgeIdentifier(request.RequestID) {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid_request_id",
			"Request ID is invalid.",
		)
		return
	}
	if !validHermesBridgeIdentifier(request.SessionID) {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid_session_id",
			"Session ID is invalid.",
		)
		return
	}
	if request.Text == "" ||
		!utf8.ValidString(request.Text) ||
		len(request.Text) > maxHermesTurnTextBytes {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid_message",
			"Message text is empty or too large.",
		)
		return
	}
	credential, _ := hermesRestaurantCredentialFromContext(r.Context())
	if !s.hermesTurns.acquire(credential.ID) {
		w.Header().Set("Retry-After", "2")
		writeError(
			w,
			http.StatusConflict,
			"turn_in_progress",
			"An earlier turn is still being processed.",
		)
		return
	}
	defer s.hermesTurns.release(credential.ID)

	requestPrefix := hermesRestaurantRequestPrefix(
		credential.ID,
		request.RequestID,
	)
	requestKey := hermesRestaurantRequestKey(
		requestPrefix,
		request.SessionID,
		request.Text,
	)
	if existing, err := s.store.HermesResponseByClientRequestID(
		r.Context(),
		credential.UserID,
		requestKey,
	); err == nil {
		s.writeExistingHermesRestaurantTurn(
			w,
			r,
			credential,
			request.RequestID,
			requestKey,
			existing,
		)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "recover hermes restaurant request", err)
		return
	}
	reused, err := s.store.HermesClientRequestPrefixExists(
		r.Context(),
		credential.UserID,
		requestPrefix,
	)
	if err != nil {
		s.internalError(w, "inspect hermes restaurant request reuse", err)
		return
	}
	if reused {
		writeError(
			w,
			http.StatusConflict,
			"request_id_reused",
			"Request ID was already used for different input.",
		)
		return
	}

	conversation, err := s.store.HermesRestaurantConversation(
		r.Context(),
		credential,
		request.SessionID,
		hermesRestaurantConversationTitle(request.Text),
		s.cfg.Lifecycle.MaxActiveConversations,
	)
	if errors.Is(err, store.ErrConversationLimit) {
		writeError(
			w,
			http.StatusConflict,
			"conversation_limit_reached",
			"No conversation slot is currently available.",
		)
		return
	}
	if err != nil {
		s.internalError(w, "prepare hermes restaurant conversation", err)
		return
	}
	model, ok := s.resolveConversationModel(w, r, conversation)
	if !ok {
		return
	}
	history, err := s.store.ListMessages(
		r.Context(),
		credential.UserID,
		conversation.ID,
	)
	if err != nil {
		s.internalError(w, "load hermes restaurant history", err)
		return
	}
	submission, forceFinal, err := hermesRestaurantInput(
		history,
		request.Text,
	)
	if err != nil {
		s.logger.Warn(
			"stored hermes restaurant guidance is invalid",
			"error",
			err,
			"conversation_id",
			conversation.ID,
		)
		writeError(
			w,
			http.StatusConflict,
			"guidance_state_invalid",
			"The current clarification state is unavailable. Start a new session.",
		)
		return
	}

	responseContext, cancelResponse, finishResponse, err := s.beginResponseJob(
		r.Context(),
	)
	if err != nil {
		writeError(
			w,
			http.StatusServiceUnavailable,
			"service_stopping",
			"The service is stopping.",
		)
		return
	}
	defer finishResponse()
	jobRequest := r.WithContext(responseContext)
	s.registerResponse(requestKey, credential.UserID, cancelResponse)
	defer s.unregisterResponse(requestKey)
	lease, err := s.jobs.Acquire(
		responseContext,
		credential.UserID,
		conversation.ID,
	)
	if err != nil {
		status, code, message := jobErrorDetails(err)
		writeError(w, status, code, message)
		return
	}
	defer lease.Release()

	sentEffort := conversation.ReasoningEffort
	if sentEffort == "auto" {
		sentEffort = ""
	}
	var userMessage, assistantMessage store.Message
	if submission != nil {
		userMessage, assistantMessage, err = s.store.BeginGuidanceResponse(
			responseContext,
			credential.UserID,
			conversation.ID,
			requestKey,
			*submission,
			conversation.Model,
			conversation.ReasoningEffort,
			sentEffort,
		)
	} else {
		userMessage, assistantMessage, err = s.store.BeginResponse(
			responseContext,
			credential.UserID,
			conversation.ID,
			requestKey,
			request.Text,
			conversation.Model,
			conversation.ReasoningEffort,
			sentEffort,
			nil,
		)
	}
	if err != nil {
		s.writeHermesRestaurantBeginError(w, err, submission != nil)
		return
	}
	discard := newDiscardResponseWriter()
	s.streamAssistantResponse(
		discard,
		jobRequest,
		responseContext,
		cancelResponse,
		credential.UserID,
		requestKey,
		conversation,
		model,
		sentEffort,
		s.progressiveSummaryMode(responseContext),
		"hermes",
		assistantMessage,
		&userMessage,
		nil,
		nil,
		false,
		request.Text,
		forceFinal,
		guidance.WeChatQuestionsPerRound,
	)
	finalMessage, err := s.store.MessageByID(
		context.WithoutCancel(responseContext),
		credential.UserID,
		assistantMessage.ID,
	)
	if err != nil {
		s.internalError(w, "load completed hermes restaurant response", err)
		return
	}
	if finalMessage.Status != "completed" {
		writeError(
			w,
			http.StatusBadGateway,
			hermesResponseErrorCode(finalMessage),
			"The answer could not be completed.",
		)
		return
	}
	response, err := s.hermesRestaurantResponse(
		responseContext,
		credential,
		request.RequestID,
		requestKey,
		finalMessage,
	)
	if err != nil {
		s.internalError(w, "render hermes restaurant response", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) writeExistingHermesRestaurantTurn(
	w http.ResponseWriter,
	r *http.Request,
	credential store.HermesRestaurantCredential,
	requestID string,
	requestKey string,
	message store.Message,
) {
	switch message.Status {
	case "pending", "streaming":
		w.Header().Set("Retry-After", "2")
		writeError(
			w,
			http.StatusConflict,
			"turn_in_progress",
			"An earlier turn is still being processed.",
		)
		return
	case "completed":
		response, err := s.hermesRestaurantResponse(
			r.Context(),
			credential,
			requestID,
			requestKey,
			message,
		)
		if err != nil {
			s.internalError(w, "render recovered hermes restaurant response", err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		writeError(
			w,
			http.StatusBadGateway,
			hermesResponseErrorCode(message),
			"The earlier request did not complete.",
		)
	}
}

func (s *Server) hermesRestaurantResponse(
	ctx context.Context,
	credential store.HermesRestaurantCredential,
	requestID string,
	requestKey string,
	message store.Message,
) (hermesRestaurantTurnResponse, error) {
	response := hermesRestaurantTurnResponse{
		RequestID: requestID,
		Audio: hermesRestaurantAudioResponse{
			Status: "not_applicable",
			Files:  []hermesRestaurantAudioFile{},
		},
	}
	for _, part := range message.Parts {
		switch part.Type {
		case guidance.PartClarification:
			cards, err := guidance.DecodeClarificationCards(part.JSONContent)
			if err != nil {
				return hermesRestaurantTurnResponse{}, err
			}
			text, err := guidance.RenderWeChatClarification(cards)
			if err != nil {
				return hermesRestaurantTurnResponse{}, err
			}
			response.Kind = "clarification"
			response.Text = text
			return response, nil
		case guidance.PartTaskBrief:
			brief, err := guidance.DecodeTaskBrief(part.JSONContent)
			if err != nil {
				return hermesRestaurantTurnResponse{}, err
			}
			text, err := guidance.RenderWeChatTaskBrief(brief)
			if err != nil {
				return hermesRestaurantTurnResponse{}, err
			}
			response.Kind = "task_brief"
			response.Text = text
			return response, nil
		}
	}
	answer := hermesAnswerText(message)
	if answer == "" {
		return hermesRestaurantTurnResponse{}, errors.New(
			"completed hermes response contains no answer text",
		)
	}
	response.Kind = "answer"
	response.Text = answer
	response.Audio = s.hermesRestaurantSpeech(
		ctx,
		credential,
		requestKey,
		answer,
	)
	return response, nil
}

func (s *Server) hermesRestaurantSpeech(
	ctx context.Context,
	credential store.HermesRestaurantCredential,
	requestKey string,
	answer string,
) hermesRestaurantAudioResponse {
	cached, err := s.store.HermesRestaurantAudioForRequest(
		ctx,
		credential.ID,
		credential.UserID,
		requestKey,
		time.Now().UnixMilli(),
	)
	if err == nil && len(cached) > 0 && s.hermesAudioFilesExist(cached) {
		return hermesAudioResponseFromRecords(cached)
	}
	if err != nil {
		s.logger.Error(
			"load cached hermes restaurant audio failed",
			"error",
			err,
		)
		return unavailableHermesAudio("speech_persistence_failed")
	}
	if len(cached) > 0 {
		idsToDelete := make([]string, 0, len(cached))
		for _, audio := range cached {
			idsToDelete = append(idsToDelete, audio.ID)
		}
		paths, deleteErr := s.store.DeleteHermesRestaurantAudio(ctx, idsToDelete)
		if deleteErr != nil {
			s.logger.Error(
				"delete stale hermes restaurant audio failed",
				"error",
				deleteErr,
			)
			return unavailableHermesAudio("speech_persistence_failed")
		}
		s.removeHermesAudioPaths(paths)
	}

	setting, err := s.store.SpeechServiceSetting(ctx)
	if err != nil {
		s.logger.Error("read hermes speech setting failed", "error", err)
		return unavailableHermesAudio("speech_settings_unavailable")
	}
	if !setting.Enabled {
		return unavailableHermesAudio("speech_disabled")
	}
	provider, exists := s.speechProviders.Provider(setting.Provider)
	if !exists || !provider.Configured() {
		return unavailableHermesAudio("speech_provider_unavailable")
	}
	preference, err := s.store.UserSpeechPreference(ctx, credential.UserID)
	if err != nil {
		s.logger.Error("read hermes speech preference failed", "error", err)
		return unavailableHermesAudio("speech_settings_unavailable")
	}
	voice := effectiveSpeechVoice(
		preference.Voice,
		setting.DefaultVoice,
		provider.Voices(),
	)
	if voice == "" {
		return unavailableHermesAudio("speech_voice_unavailable")
	}
	release, err := s.speechGate.Acquire(credential.UserID)
	if err != nil {
		return unavailableHermesAudio("speech_busy")
	}
	defer release()

	spokenText := speech.NormalizeAnswerText(answer)
	chunks := speech.SplitAnswerText(
		spokenText,
		speech.DefaultFileChunkRunes,
	)
	if len(chunks) == 0 {
		return unavailableHermesAudio("speech_text_empty")
	}
	if len(chunks) > maxHermesRestaurantAudioFiles {
		return unavailableHermesAudio("speech_too_large")
	}
	speechContext, cancel := context.WithTimeout(
		ctx,
		s.cfg.Speech.SessionTTL,
	)
	defer cancel()
	now := time.Now().UnixMilli()
	records := make([]store.HermesRestaurantAudio, 0, len(chunks))
	createdPaths := make([]string, 0, len(chunks))
	totalAudioBytes := 0
	cleanup := func() {
		for _, path := range createdPaths {
			_ = os.Remove(path)
		}
	}
	for index, chunk := range chunks {
		pcm, audioConfig, synthErr := speech.SynthesizePCM(
			speechContext,
			provider,
			speech.SessionConfig{
				Voice: voice,
				Speed: preference.Speed,
			},
			chunk,
		)
		if synthErr != nil {
			cleanup()
			code := hermesSpeechErrorCode(synthErr)
			s.logger.Warn(
				"hermes restaurant speech synthesis failed",
				"provider",
				setting.Provider,
				"code",
				code,
				"error",
				synthErr,
			)
			return unavailableHermesAudio(code)
		}
		wav, wavErr := speech.PCMToWAV(pcm, audioConfig)
		if wavErr != nil {
			cleanup()
			s.logger.Error(
				"hermes restaurant wav encoding failed",
				"error",
				wavErr,
			)
			return unavailableHermesAudio("speech_format_unsupported")
		}
		if len(wav) > maxHermesRestaurantAudioBytes ||
			totalAudioBytes > maxHermesRestaurantAudioTotal-len(wav) {
			cleanup()
			return unavailableHermesAudio("speech_too_large")
		}
		totalAudioBytes += len(wav)
		id, idErr := ids.New()
		if idErr != nil {
			cleanup()
			return unavailableHermesAudio("speech_file_failed")
		}
		storagePath := filepath.Join(
			"hermes-restaurant-audio",
			id+".wav",
		)
		fullPath := filepath.Join(s.cfg.DataDir, storagePath)
		if writeErr := speech.WriteFileAtomic(fullPath, wav, 0o600); writeErr != nil {
			cleanup()
			s.logger.Error(
				"write hermes restaurant audio failed",
				"error",
				writeErr,
			)
			return unavailableHermesAudio("speech_file_failed")
		}
		createdPaths = append(createdPaths, fullPath)
		digest := sha256.Sum256(wav)
		records = append(records, store.HermesRestaurantAudio{
			ID: id, CredentialID: credential.ID,
			UserID: credential.UserID, RequestKey: requestKey,
			PartIndex: index,
			FileName: fmt.Sprintf(
				"answer-%02d-of-%02d.wav",
				index+1,
				len(chunks),
			),
			StoragePath: storagePath,
			ByteSize:    int64(len(wav)),
			SHA256:      hex.EncodeToString(digest[:]),
			CreatedAt:   now,
			ExpiresAt:   now + hermesRestaurantAudioTTL.Milliseconds(),
		})
	}
	if err := s.store.CreateHermesRestaurantAudioBatch(
		speechContext,
		records,
	); err != nil {
		cleanup()
		s.logger.Error(
			"persist hermes restaurant audio failed",
			"error",
			err,
		)
		return unavailableHermesAudio("speech_persistence_failed")
	}
	return hermesAudioResponseFromRecords(records)
}

func (s *Server) hermesRestaurantAudio(w http.ResponseWriter, r *http.Request) {
	credential, _ := hermesRestaurantCredentialFromContext(r.Context())
	audio, err := s.store.HermesRestaurantAudioByID(
		r.Context(),
		credential.ID,
		credential.UserID,
		r.PathValue("id"),
		time.Now().UnixMilli(),
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Audio not found.")
		return
	}
	if err != nil {
		s.internalError(w, "load hermes restaurant audio", err)
		return
	}
	fullPath, ok := s.hermesAudioPath(audio.StoragePath)
	if !ok {
		s.logger.Error(
			"invalid stored hermes restaurant audio path",
			"audio_id",
			audio.ID,
		)
		writeError(w, http.StatusNotFound, "not_found", "Audio not found.")
		return
	}
	file, info, err := openVerifiedHermesAudio(fullPath, audio)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "not_found", "Audio not found.")
		return
	}
	if err != nil {
		s.logger.Warn(
			"Hermes restaurant audio integrity check failed",
			"audio_id",
			audio.ID,
			"error",
			err,
		)
		writeError(w, http.StatusNotFound, "not_found", "Audio not found.")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set(
		"Content-Disposition",
		`attachment; filename="`+audio.FileName+`"`,
	)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(audio.ByteSize, 10))
	http.ServeContent(w, r, audio.FileName, info.ModTime(), file)
}

func hermesRestaurantInput(
	history []store.Message,
	text string,
) (*guidance.GuidanceSubmission, bool, error) {
	forceFinal := guidance.WeChatDirectGenerationRequested(text)
	if len(history) == 0 {
		return nil, forceFinal, nil
	}
	latest := history[len(history)-1]
	if latest.Role != "assistant" || latest.Status != "completed" {
		return nil, forceFinal, nil
	}
	for _, part := range latest.Parts {
		switch part.Type {
		case guidance.PartClarification:
			cards, err := guidance.DecodeClarificationCards(part.JSONContent)
			if err != nil {
				return nil, false, err
			}
			reply, err := guidance.ParseWeChatClarificationReply(
				cards,
				latest.ID,
				part.ID,
				text,
			)
			if err != nil {
				return nil, false, err
			}
			return reply.Submission, reply.ForceFinal, nil
		case guidance.PartTaskBrief:
			brief, err := guidance.DecodeTaskBrief(part.JSONContent)
			if err != nil {
				return nil, false, err
			}
			submission, matched, err := guidance.ParseWeChatTaskBriefReply(
				brief,
				latest.ID,
				part.ID,
				text,
			)
			if err != nil {
				return nil, false, err
			}
			if matched {
				return submission, submission.Intent == guidance.IntentConfirmBrief, nil
			}
		}
	}
	return nil, forceFinal, nil
}

func (s *Server) writeHermesRestaurantBeginError(
	w http.ResponseWriter,
	err error,
	structured bool,
) {
	switch {
	case errors.Is(err, store.ErrDuplicateRequest):
		w.Header().Set("Retry-After", "2")
		writeError(
			w,
			http.StatusConflict,
			"turn_in_progress",
			"An earlier turn is still being processed.",
		)
	case errors.Is(err, store.ErrStaleGuidance):
		writeError(
			w,
			http.StatusConflict,
			"stale_guidance",
			"The clarification is no longer current.",
		)
	case structured && errors.Is(err, store.ErrNotFound):
		writeError(
			w,
			http.StatusNotFound,
			"not_found",
			"The clarification was not found.",
		)
	case structured:
		s.logger.Warn(
			"hermes restaurant guidance submission rejected",
			"error",
			err,
		)
		writeError(
			w,
			http.StatusBadRequest,
			"invalid_guidance_submission",
			"The clarification answer could not be accepted.",
		)
	default:
		s.logger.Error("begin hermes restaurant response failed", "error", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"internal_error",
			"An internal error occurred.",
		)
	}
}

func validHermesBridgeIdentifier(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if unicodeControl(current) {
			return false
		}
	}
	return true
}

func unicodeControl(value rune) bool {
	return value < 0x20 || value == 0x7f
}

func hermesRestaurantRequestPrefix(
	credentialID string,
	requestID string,
) string {
	digest := sha256.Sum256([]byte(requestID))
	return "hbr:" + credentialID + ":" + hex.EncodeToString(digest[:])
}

func hermesRestaurantRequestKey(
	requestPrefix string,
	sessionID string,
	text string,
) string {
	digest := sha256.Sum256(
		[]byte(sessionID + "\x00" + strings.TrimSpace(text)),
	)
	return requestPrefix + ":" + hex.EncodeToString(digest[:])
}

func hermesRestaurantConversationTitle(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 24 {
		runes = append(runes[:24], '…')
	}
	return "微信餐饮 · " + string(runes)
}

func hermesAnswerText(message store.Message) string {
	parts := make([]string, 0)
	for _, part := range message.Parts {
		if part.Type == "text" && strings.TrimSpace(part.TextContent) != "" {
			parts = append(parts, strings.TrimSpace(part.TextContent))
		}
	}
	return strings.Join(parts, "\n\n")
}

func hermesResponseErrorCode(message store.Message) string {
	if code := strings.TrimSpace(message.ErrorCode); code != "" {
		return code
	}
	return "answer_incomplete"
}

func unavailableHermesAudio(code string) hermesRestaurantAudioResponse {
	return hermesRestaurantAudioResponse{
		Status: "unavailable",
		Code:   code,
		Files:  []hermesRestaurantAudioFile{},
	}
}

func hermesAudioResponseFromRecords(
	records []store.HermesRestaurantAudio,
) hermesRestaurantAudioResponse {
	files := make([]hermesRestaurantAudioFile, 0, len(records))
	for _, audio := range records {
		files = append(files, hermesRestaurantAudioFile{
			ID: audio.ID, FileName: audio.FileName,
			ContentType: "audio/wav", ByteSize: audio.ByteSize,
			DownloadPath: "/api/v1/integrations/hermes/restaurant/audio/" +
				audio.ID,
		})
	}
	return hermesRestaurantAudioResponse{
		Status: "ready",
		Files:  files,
	}
}

func (s *Server) hermesAudioFilesExist(
	records []store.HermesRestaurantAudio,
) bool {
	for _, audio := range records {
		path, ok := s.hermesAudioPath(audio.StoragePath)
		if !ok {
			return false
		}
		file, _, err := openVerifiedHermesAudio(path, audio)
		if err != nil {
			return false
		}
		_ = file.Close()
	}
	return true
}

func openVerifiedHermesAudio(
	path string,
	audio store.HermesRestaurantAudio,
) (*os.File, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != audio.ByteSize {
		return nil, nil, errors.New("audio file metadata does not match")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	closeWithError := func(err error) (*os.File, os.FileInfo, error) {
		_ = file.Close()
		return nil, nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return closeWithError(err)
	}
	if !openedInfo.Mode().IsRegular() ||
		openedInfo.Size() != audio.ByteSize ||
		!os.SameFile(info, openedInfo) {
		return closeWithError(errors.New("audio file changed during verification"))
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return closeWithError(err)
	}
	if !strings.EqualFold(
		hex.EncodeToString(digest.Sum(nil)),
		strings.TrimSpace(audio.SHA256),
	) {
		return closeWithError(errors.New("audio file digest does not match"))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return closeWithError(err)
	}
	return file, openedInfo, nil
}

func (s *Server) hermesAudioPath(storagePath string) (string, bool) {
	root := filepath.Join(s.cfg.DataDir, "hermes-restaurant-audio")
	fullPath := filepath.Clean(filepath.Join(s.cfg.DataDir, storagePath))
	relative, err := filepath.Rel(root, fullPath)
	if err != nil ||
		relative == "." ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return fullPath, true
}

func (s *Server) removeHermesAudioPaths(paths []string) {
	for _, storagePath := range paths {
		fullPath, ok := s.hermesAudioPath(storagePath)
		if !ok {
			s.logger.Error(
				"refused invalid hermes audio cleanup path",
				"storage_path",
				storagePath,
			)
			continue
		}
		if err := os.Remove(fullPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			s.logger.Warn(
				"remove hermes restaurant audio failed",
				"error",
				err,
			)
		}
	}
}

func hermesSpeechErrorCode(err error) string {
	switch {
	case errors.Is(err, speech.ErrBusy):
		return "speech_busy"
	case errors.Is(err, speech.ErrProviderUnavailable):
		return "speech_provider_unavailable"
	case errors.Is(err, speech.ErrProviderAuth):
		return "speech_provider_auth_failed"
	case errors.Is(err, speech.ErrProviderNotGranted):
		return "speech_provider_not_granted"
	case errors.Is(err, speech.ErrProviderVoiceModel):
		return "speech_voice_model_mismatch"
	case errors.Is(err, context.DeadlineExceeded):
		return "speech_timeout"
	default:
		return "speech_provider_failed"
	}
}

type discardResponseWriter struct {
	header http.Header
}

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}

func (w *discardResponseWriter) Header() http.Header {
	return w.header
}

func (w *discardResponseWriter) WriteHeader(_ int) {}

func (w *discardResponseWriter) Write(value []byte) (int, error) {
	return io.Discard.Write(value)
}

func (w *discardResponseWriter) Flush() {}

var _ http.ResponseWriter = (*discardResponseWriter)(nil)
var _ http.Flusher = (*discardResponseWriter)(nil)
