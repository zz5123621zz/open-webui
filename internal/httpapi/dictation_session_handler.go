package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/dictation"
	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
)

const (
	maxDictationControlBytes = 8 * 1024
	maxDictationDraftBytes   = 4 * 1024
	maxDictationAudioFrame   = 64 * 1024
)

var dictationUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   16 * 1024,
	WriteBufferSize:  16 * 1024,
	CheckOrigin: func(_ *http.Request) bool {
		// The strict application origin middleware runs before the upgrade.
		return true
	},
}

type dictationClientMessage struct {
	Type  string `json:"type"`
	Draft string `json:"draft,omitempty"`
}

type dictationClientRead struct {
	messageType int
	payload     []byte
	err         error
}

type dictationProviderRead struct {
	event dictation.Event
	err   error
}

func (s *Server) dictationSession(w http.ResponseWriter, r *http.Request) {
	connection, err := dictationUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() {
		_ = connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(2*time.Second),
		)
		_ = connection.Close()
	}()
	connection.SetReadLimit(maxDictationAudioFrame)
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	if !writeDictationJSON(connection, map[string]any{
		"type": "dictation.ready",
	}) {
		return
	}
	messageType, rawStart, err := connection.ReadMessage()
	if err != nil {
		return
	}
	if messageType != websocket.TextMessage ||
		len(rawStart) > maxDictationControlBytes {
		writeDictationError(
			connection, "dictation_start_invalid",
			"The dictation start request is invalid.",
		)
		return
	}
	var start dictationClientMessage
	if json.Unmarshal(rawStart, &start) != nil ||
		start.Type != "dictation.start" {
		writeDictationError(
			connection, "dictation_start_invalid",
			"The dictation start request is invalid.",
		)
		return
	}
	start.Draft = truncateDictationText(
		strings.TrimSpace(start.Draft), maxDictationDraftBytes,
	)
	setting, err := s.store.DictationServiceSetting(r.Context())
	if err != nil {
		s.logger.Error("read dictation setting for session", "error", err)
		writeDictationError(
			connection, "dictation_state_unavailable",
			"Dictation is temporarily unavailable.",
		)
		return
	}
	if !setting.Enabled {
		writeDictationError(
			connection, "dictation_disabled",
			"Dictation is disabled.",
		)
		return
	}
	if !s.dictationProvider.Configured() {
		writeDictationError(
			connection, "dictation_provider_unavailable",
			"Dictation is temporarily unavailable.",
		)
		return
	}
	session, _ := sessionFromContext(r.Context())
	release, err := s.dictationGate.Acquire(session.User.ID)
	if errors.Is(err, dictation.ErrBusy) {
		writeDictationError(
			connection, "dictation_session_limit",
			"A dictation session is already active or the service is busy.",
		)
		return
	}
	if err != nil {
		s.logger.Error("acquire dictation session", "error", err)
		writeDictationError(
			connection, "dictation_state_unavailable",
			"Dictation is temporarily unavailable.",
		)
		return
	}
	defer release()

	contextItems, err := s.dictationContext(
		r.Context(), session.User.ID, start.Draft,
	)
	if err != nil {
		s.logger.Error("prepare dictation context", "error", err)
		writeDictationError(
			connection, "dictation_state_unavailable",
			"Dictation is temporarily unavailable.",
		)
		return
	}
	dictationContext, cancel := context.WithTimeout(
		r.Context(), s.cfg.Dictation.SessionTTL,
	)
	defer cancel()
	if !writeDictationJSON(connection, map[string]any{
		"type": "dictation.connecting",
	}) {
		return
	}
	providerSession, err := s.dictationProvider.Open(
		dictationContext,
		dictation.SessionConfig{
			UserID:  s.providerSafetyIdentifier(session.User.ID),
			Context: contextItems,
		},
	)
	if err != nil {
		s.logDictationProviderError("open", err)
		writeMappedDictationProviderError(connection, err)
		return
	}
	defer providerSession.Close()
	_ = connection.SetReadDeadline(time.Time{})
	if !writeDictationJSON(connection, map[string]any{
		"type": "dictation.started",
		"audio": map[string]any{
			"format": "pcm", "sampleRate": 16000,
			"channels": 1, "bitDepth": 16,
		},
		"maxDurationSeconds": int(s.cfg.Dictation.MaxDuration.Seconds()),
	}) {
		return
	}

	clientReads := make(chan dictationClientRead, 1)
	providerReads := make(chan dictationProviderRead, 1)
	go readDictationClient(connection, clientReads)
	go readDictationProvider(
		dictationContext, providerSession, providerReads,
	)
	durationTimer := time.NewTimer(s.cfg.Dictation.MaxDuration)
	defer durationTimer.Stop()

	maxAudioBytes := int64(
		s.cfg.Dictation.MaxDuration.Seconds()*
			float64(s.cfg.Dictation.Volcengine.SampleRate)*
			float64(s.cfg.Dictation.Volcengine.Bits/8),
	) + maxDictationAudioFrame
	var pendingAudio []byte
	var totalAudioBytes int64
	finishing := false
	for {
		select {
		case <-dictationContext.Done():
			writeDictationError(
				connection, "dictation_session_expired",
				"The dictation session expired.",
			)
			return
		case <-durationTimer.C:
			if finishing {
				continue
			}
			if len(pendingAudio) == 0 {
				writeDictationError(
					connection, "dictation_audio_empty",
					"No microphone audio was received.",
				)
				return
			}
			if !writeDictationJSON(connection, map[string]any{
				"type":   "dictation.stopping",
				"reason": "duration_limit",
			}) {
				return
			}
			if err := providerSession.Finish(
				dictationContext, pendingAudio,
			); err != nil {
				s.logDictationProviderError("finish at duration limit", err)
				writeMappedDictationProviderError(connection, err)
				return
			}
			pendingAudio = nil
			finishing = true
		case read := <-clientReads:
			if read.err != nil {
				return
			}
			switch read.messageType {
			case websocket.BinaryMessage:
				if finishing {
					go readDictationClient(connection, clientReads)
					continue
				}
				if len(read.payload) == 0 ||
					len(read.payload) > maxDictationAudioFrame ||
					len(read.payload)%2 != 0 {
					writeDictationError(
						connection, "dictation_audio_invalid",
						"The microphone audio frame is invalid.",
					)
					return
				}
				totalAudioBytes += int64(len(read.payload))
				if totalAudioBytes > maxAudioBytes {
					writeDictationError(
						connection, "dictation_audio_limit",
						"The two-minute dictation limit was reached.",
					)
					return
				}
				if len(pendingAudio) > 0 {
					if err := providerSession.SendAudio(
						dictationContext, pendingAudio,
					); err != nil {
						s.logDictationProviderError("send audio", err)
						writeMappedDictationProviderError(connection, err)
						return
					}
				}
				pendingAudio = append(pendingAudio[:0], read.payload...)
			case websocket.TextMessage:
				if len(read.payload) > maxDictationControlBytes {
					writeDictationError(
						connection, "dictation_message_invalid",
						"The dictation control message is invalid.",
					)
					return
				}
				var message dictationClientMessage
				if json.Unmarshal(read.payload, &message) != nil {
					writeDictationError(
						connection, "dictation_message_invalid",
						"The dictation control message is invalid.",
					)
					return
				}
				switch message.Type {
				case "dictation.finish":
					if !finishing {
						if len(pendingAudio) == 0 {
							writeDictationError(
								connection, "dictation_audio_empty",
								"No microphone audio was received.",
							)
							return
						}
						if err := providerSession.Finish(
							dictationContext, pendingAudio,
						); err != nil {
							s.logDictationProviderError("finish", err)
							writeMappedDictationProviderError(connection, err)
							return
						}
						pendingAudio = nil
						finishing = true
					}
				case "dictation.cancel":
					_ = writeDictationJSON(connection, map[string]any{
						"type": "dictation.cancelled",
					})
					return
				case "dictation.ping":
					if !writeDictationJSON(connection, map[string]any{
						"type": "dictation.pong",
					}) {
						return
					}
				default:
					writeDictationError(
						connection, "dictation_message_invalid",
						"Unknown dictation control message.",
					)
					return
				}
			default:
				writeDictationError(
					connection, "dictation_message_invalid",
					"Unsupported dictation message type.",
				)
				return
			}
			go readDictationClient(connection, clientReads)
		case read := <-providerReads:
			if read.err != nil {
				s.logDictationProviderError("read response", read.err)
				writeMappedDictationProviderError(connection, read.err)
				return
			}
			switch read.event.Type {
			case dictation.EventTranscript:
				if !writeDictationJSON(connection, map[string]any{
					"type":     "dictation.transcript",
					"text":     read.event.Text,
					"definite": read.event.Definite,
				}) {
					return
				}
			case dictation.EventCompleted:
				if strings.TrimSpace(read.event.Text) == "" {
					writeDictationError(
						connection,
						"dictation_no_speech",
						"No recognizable speech was found.",
					)
					return
				}
				_ = writeDictationJSON(connection, map[string]any{
					"type": "dictation.completed",
					"text": read.event.Text,
				})
				return
			}
			go readDictationProvider(
				dictationContext, providerSession, providerReads,
			)
		}
	}
}

func (s *Server) dictationContext(
	ctx context.Context,
	userID string,
	draft string,
) ([]string, error) {
	workbench, err := s.store.WorkbenchSetting(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !s.cfg.Tools.RestaurantGuidanceEnabled ||
		workbench.Effective != guidance.WorkbenchRestaurant {
		return nil, nil
	}
	items := make([]string, 0, 2)
	if draft != "" {
		items = append(items, "录音前输入框草稿："+draft)
	}
	facts, err := s.store.RestaurantProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(facts) > 0 {
		var profile strings.Builder
		profile.WriteString("当前餐厅档案：")
		for index, fact := range facts {
			if index > 0 {
				profile.WriteString("；")
			}
			profile.WriteString(fact.Field)
			profile.WriteString("：")
			profile.WriteString(fact.Value)
			if profile.Len() >= maxDictationDraftBytes {
				break
			}
		}
		items = append(items, truncateDictationText(
			profile.String(), maxDictationDraftBytes,
		))
	}
	return items, nil
}

func readDictationClient(
	connection *websocket.Conn,
	output chan<- dictationClientRead,
) {
	messageType, payload, err := connection.ReadMessage()
	output <- dictationClientRead{
		messageType: messageType, payload: payload, err: err,
	}
}

func readDictationProvider(
	ctx context.Context,
	session dictation.Session,
	output chan<- dictationProviderRead,
) {
	event, err := session.ReadEvent(ctx)
	output <- dictationProviderRead{event: event, err: err}
}

func writeDictationJSON(connection *websocket.Conn, value any) bool {
	_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return connection.WriteJSON(value) == nil
}

func writeDictationError(
	connection *websocket.Conn,
	code string,
	message string,
) {
	_ = writeDictationJSON(connection, map[string]any{
		"type": "dictation.error", "code": code, "message": message,
	})
}

func writeMappedDictationProviderError(
	connection *websocket.Conn,
	err error,
) {
	switch {
	case errors.Is(err, dictation.ErrProviderNotGranted):
		writeDictationError(
			connection,
			"dictation_provider_not_granted",
			"The speech recognition resource is not enabled for this provider project.",
		)
	case errors.Is(err, dictation.ErrProviderAuth):
		writeDictationError(
			connection,
			"dictation_provider_auth_failed",
			"The speech recognition provider rejected its credentials.",
		)
	case errors.Is(err, dictation.ErrProviderBusy):
		writeDictationError(
			connection,
			"dictation_provider_busy",
			"The speech recognition provider is temporarily busy.",
		)
	case errors.Is(err, dictation.ErrNoSpeech):
		writeDictationError(
			connection,
			"dictation_no_speech",
			"No recognizable speech was found.",
		)
	default:
		writeDictationError(
			connection,
			"dictation_provider_failed",
			"The speech recognition provider connection failed.",
		)
	}
}

func (s *Server) logDictationProviderError(scope string, err error) {
	s.logger.Error(
		"dictation provider failed",
		"provider", s.dictationProvider.ID(),
		"scope", scope,
		"error", err,
	)
}

func truncateDictationText(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}
