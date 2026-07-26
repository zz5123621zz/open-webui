package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/speech"
)

const (
	maxSpeechTextFrameBytes = 8 * 1024
	maxSpeechSessionBytes   = 200 * 1024
)

var speechUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   4096,
	WriteBufferSize:  16 * 1024,
	CheckOrigin: func(_ *http.Request) bool {
		// The strict application origin middleware runs before the upgrade.
		return true
	},
}

type speechClientMessage struct {
	Type     string `json:"type"`
	Sequence uint64 `json:"sequence,omitempty"`
	Text     string `json:"text,omitempty"`
}

type speechClientRead struct {
	message speechClientMessage
	err     error
}

type speechProviderRead struct {
	event speech.Event
	err   error
}

func (s *Server) speechSession(w http.ResponseWriter, r *http.Request) {
	setting, err := s.store.SpeechServiceSetting(r.Context())
	if err != nil {
		s.internalError(w, "read speech setting for session", err)
		return
	}
	if !setting.Enabled {
		writeError(w, http.StatusServiceUnavailable, "speech_disabled", "Speech is disabled.")
		return
	}
	provider, exists := s.speechProviders.Provider(setting.Provider)
	if !exists || !provider.Configured() {
		writeError(
			w, http.StatusServiceUnavailable, "speech_provider_unavailable",
			"Speech is temporarily unavailable.",
		)
		return
	}
	session, _ := sessionFromContext(r.Context())
	preference, err := s.store.UserSpeechPreference(r.Context(), session.User.ID)
	if err != nil {
		s.internalError(w, "read speech preference for session", err)
		return
	}
	voice := effectiveSpeechVoice(preference.Voice, setting.DefaultVoice, provider.Voices())
	if !speechVoiceAllowed(voice, provider.Voices()) {
		writeError(
			w, http.StatusConflict, "speech_voice_unavailable",
			"The selected speech voice is no longer available.",
		)
		return
	}
	release, err := s.speechGate.Acquire(session.User.ID)
	if errors.Is(err, speech.ErrBusy) {
		writeError(
			w, http.StatusTooManyRequests, "speech_session_limit",
			"A speech session is already active or the service is busy.",
		)
		return
	}
	if err != nil {
		s.internalError(w, "acquire speech session", err)
		return
	}
	defer release()

	connection, err := speechUpgrader.Upgrade(w, r, nil)
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
	connection.SetReadLimit(16 * 1024)
	_ = connection.SetReadDeadline(time.Now().Add(s.cfg.Speech.SessionTTL))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(s.cfg.Speech.SessionTTL))
	})
	if !writeSpeechJSON(connection, map[string]any{
		"type": "speech.connecting", "provider": setting.Provider,
	}) {
		return
	}

	speechContext, cancel := context.WithTimeout(r.Context(), s.cfg.Speech.SessionTTL)
	defer cancel()
	providerSession, err := provider.Open(speechContext, speech.SessionConfig{
		Voice: voice, Speed: preference.Speed,
	})
	if err != nil {
		s.logger.Error("open speech provider session", "provider", setting.Provider, "error", err)
		switch {
		case errors.Is(err, speech.ErrProviderNotGranted):
			writeSpeechError(
				connection,
				"speech_provider_not_granted",
				"The speech resource is not enabled for the provider project.",
			)
		case errors.Is(err, speech.ErrProviderAuth):
			writeSpeechError(
				connection,
				"speech_provider_auth_failed",
				"The speech provider rejected its API credentials.",
			)
		default:
			writeSpeechError(
				connection,
				"speech_provider_failed",
				"The speech provider could not start.",
			)
		}
		return
	}
	defer providerSession.Close()
	audio := providerSession.AudioConfig()
	if !writeSpeechJSON(connection, map[string]any{
		"type": "speech.started", "provider": setting.Provider,
		"voice": voice, "speed": preference.Speed, "audio": audio,
	}) {
		return
	}

	clientReads := make(chan speechClientRead, 1)
	providerReads := make(chan speechProviderRead, 1)
	go readSpeechClient(connection, clientReads)
	go readSpeechProvider(speechContext, providerSession, providerReads)

	var lastSequence uint64
	totalBytes := 0
	finishing := false
	for {
		select {
		case <-speechContext.Done():
			writeSpeechError(connection, "speech_session_expired", "The speech session expired.")
			return
		case read := <-clientReads:
			if read.err != nil {
				return
			}
			message := read.message
			switch message.Type {
			case "speech.text":
				if finishing {
					writeSpeechError(connection, "speech_already_finishing", "The speech session is already finishing.")
					return
				}
				if message.Sequence != lastSequence+1 {
					writeSpeechError(connection, "speech_sequence_invalid", "Speech text is out of sequence.")
					return
				}
				message.Text = strings.TrimSpace(message.Text)
				if message.Text == "" || !utf8.ValidString(message.Text) ||
					len(message.Text) > maxSpeechTextFrameBytes {
					writeSpeechError(connection, "speech_text_invalid", "Speech text is empty or too large.")
					return
				}
				totalBytes += len(message.Text)
				if totalBytes > maxSpeechSessionBytes {
					writeSpeechError(connection, "speech_text_limit", "The speech session text limit was reached.")
					return
				}
				if err := providerSession.SendText(speechContext, message.Text); err != nil {
					s.logger.Error("send speech provider text", "provider", setting.Provider, "error", err)
					writeSpeechError(connection, "speech_provider_failed", "The speech provider rejected the text.")
					return
				}
				lastSequence = message.Sequence
			case "speech.finish":
				if !finishing {
					finishing = true
					if err := providerSession.Finish(speechContext); err != nil {
						s.logger.Error("finish speech provider session", "provider", setting.Provider, "error", err)
						writeSpeechError(connection, "speech_provider_failed", "The speech provider could not finish.")
						return
					}
				}
			case "speech.cancel":
				_ = writeSpeechJSON(connection, map[string]any{"type": "speech.cancelled"})
				return
			case "speech.ping":
				if !writeSpeechJSON(connection, map[string]any{"type": "speech.pong"}) {
					return
				}
			default:
				writeSpeechError(connection, "speech_message_invalid", "Unknown speech session message.")
				return
			}
			go readSpeechClient(connection, clientReads)
		case read := <-providerReads:
			if read.err != nil {
				s.logger.Error("read speech provider event", "provider", setting.Provider, "error", read.err)
				if errors.Is(read.err, speech.ErrProviderVoiceModel) {
					writeSpeechError(
						connection,
						"speech_voice_model_mismatch",
						"The selected voice does not support the configured speech model.",
					)
				} else {
					writeSpeechError(
						connection,
						"speech_provider_failed",
						"The speech provider connection ended.",
					)
				}
				return
			}
			switch read.event.Type {
			case speech.EventAudio:
				if len(read.event.Audio) > 0 {
					_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := connection.WriteMessage(websocket.BinaryMessage, read.event.Audio); err != nil {
						return
					}
				}
			case speech.EventCompleted:
				_ = writeSpeechJSON(connection, map[string]any{
					"type": "speech.completed", "textBytes": totalBytes,
				})
				return
			}
			go readSpeechProvider(speechContext, providerSession, providerReads)
		}
	}
}

func effectiveSpeechVoice(preferred, fallback string, voices []speech.Voice) string {
	if speechVoiceAllowed(preferred, voices) {
		return preferred
	}
	return fallback
}

func readSpeechClient(connection *websocket.Conn, output chan<- speechClientRead) {
	var message speechClientMessage
	err := connection.ReadJSON(&message)
	output <- speechClientRead{message: message, err: err}
}

func readSpeechProvider(
	ctx context.Context,
	session speech.Session,
	output chan<- speechProviderRead,
) {
	event, err := session.ReadEvent(ctx)
	output <- speechProviderRead{event: event, err: err}
}

func writeSpeechJSON(connection *websocket.Conn, value any) bool {
	_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return connection.WriteJSON(value) == nil
}

func writeSpeechError(connection *websocket.Conn, code, message string) {
	_ = writeSpeechJSON(connection, map[string]any{
		"type": "speech.error", "code": code, "message": message,
	})
}
