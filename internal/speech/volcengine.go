package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

const volcengineStartupTimeout = 10 * time.Second

type VolcengineProvider struct {
	config config.VolcengineSpeech
	dialer *websocket.Dialer
}

func NewVolcengineProvider(cfg config.VolcengineSpeech) *VolcengineProvider {
	return &VolcengineProvider{
		config: cfg,
		dialer: &websocket.Dialer{
			HandshakeTimeout: volcengineStartupTimeout,
			Proxy:            http.ProxyFromEnvironment,
		},
	}
}

func (p *VolcengineProvider) ID() string {
	return "volcengine"
}

func (p *VolcengineProvider) Configured() bool {
	return p != nil && p.config.Endpoint != nil &&
		strings.TrimSpace(p.config.APIKey) != "" &&
		p.config.ResourceID == "seed-tts-2.0" &&
		p.config.Format == "pcm" &&
		p.config.SampleRate == 24000 &&
		len(p.config.Voices) > 0
}

func (p *VolcengineProvider) Voices() []Voice {
	if p == nil {
		return []Voice{}
	}
	result := make([]Voice, 0, len(p.config.Voices))
	for _, voice := range p.config.Voices {
		result = append(result, Voice{ID: voice.ID, Label: voice.Label})
	}
	return result
}

func (p *VolcengineProvider) Open(
	ctx context.Context,
	sessionConfig SessionConfig,
) (Session, error) {
	if !p.Configured() {
		return nil, ErrProviderUnavailable
	}
	if !voiceAllowed(sessionConfig.Voice, p.Voices()) {
		return nil, errors.New("speech voice is not allowed")
	}
	if sessionConfig.Speed < 0.5 || sessionConfig.Speed > 2 {
		return nil, errors.New("speech speed must be between 0.5 and 2.0")
	}
	connectID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("X-Api-Key", p.config.APIKey)
	headers.Set("X-Api-Resource-Id", p.config.ResourceID)
	headers.Set("X-Api-Connect-Id", connectID)
	headers.Set("X-Control-Require-Usage-Tokens-Return", "*")
	connection, response, err := p.dialer.DialContext(
		ctx, p.config.Endpoint.String(), headers,
	)
	logID := ""
	if response != nil {
		logID = strings.TrimSpace(response.Header.Get("X-Tt-Logid"))
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connect Volcengine speech service: %w", err)
	}
	connection.SetReadLimit(maxVolcProviderFrameBytes)
	sessionID, err := randomUUID()
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	session := &volcengineSession{
		connection: connection,
		sessionID:  sessionID,
		logID:      logID,
		voice:      sessionConfig.Voice,
		speedRate:  speedToVolcengineRate(sessionConfig.Speed),
		audio: AudioConfig{
			Format: p.config.Format, SampleRate: p.config.SampleRate,
			Channels: 1, BitDepth: 16,
		},
	}
	startupContext, cancel := context.WithTimeout(ctx, volcengineStartupTimeout)
	defer cancel()
	if err := session.writeEvent(
		startupContext, volcEventStartConnection, "", []byte("{}"),
	); err != nil {
		_ = session.Close()
		return nil, err
	}
	if _, err := session.waitForEvent(
		startupContext, volcEventConnectionStarted,
	); err != nil {
		_ = session.Close()
		return nil, err
	}
	startPayload, err := session.requestPayload(volcEventStartSession, "")
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.writeEvent(
		startupContext, volcEventStartSession, sessionID, startPayload,
	); err != nil {
		_ = session.Close()
		return nil, err
	}
	if _, err := session.waitForEvent(startupContext, volcEventSessionStarted); err != nil {
		_ = session.Close()
		return nil, err
	}
	_ = connection.SetReadDeadline(time.Time{})
	return session, nil
}

type volcengineSession struct {
	connection *websocket.Conn
	sessionID  string
	logID      string
	voice      string
	speedRate  int
	audio      AudioConfig

	writeMu             sync.Mutex
	stateMu             sync.Mutex
	finished            bool
	sessionCompleted    bool
	connectionCompleted bool
	closed              bool
}

type volcengineRequest struct {
	Event     int32                   `json:"event"`
	ReqParams volcengineRequestParams `json:"req_params"`
}

type volcengineRequestParams struct {
	Speaker     string                       `json:"speaker"`
	Text        string                       `json:"text,omitempty"`
	AudioParams volcengineAudioRequestParams `json:"audio_params"`
	Additions   string                       `json:"additions,omitempty"`
}

type volcengineAudioRequestParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
	SpeechRate int    `json:"speech_rate"`
}

func (s *volcengineSession) AudioConfig() AudioConfig {
	return s.audio
}

func (s *volcengineSession) SendText(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	s.stateMu.Lock()
	finished := s.finished
	s.stateMu.Unlock()
	if finished {
		return errors.New("speech session is already finishing")
	}
	payload, err := s.requestPayload(volcEventTaskRequest, text)
	if err != nil {
		return err
	}
	return s.writeEvent(ctx, volcEventTaskRequest, s.sessionID, payload)
}

func (s *volcengineSession) Finish(ctx context.Context) error {
	s.stateMu.Lock()
	if s.finished {
		s.stateMu.Unlock()
		return nil
	}
	s.finished = true
	s.stateMu.Unlock()
	return s.writeEvent(
		ctx, volcEventFinishSession, s.sessionID, []byte("{}"),
	)
}

func (s *volcengineSession) ReadEvent(ctx context.Context) (Event, error) {
	for {
		message, err := s.readMessage(ctx)
		if err != nil {
			return Event{}, err
		}
		if message.SessionID != "" && message.SessionID != s.sessionID {
			return Event{}, errors.New("Volcengine speech response has an unexpected session ID")
		}
		switch message.MessageType {
		case volcMsgError:
			return Event{}, s.providerError("request", message)
		case volcMsgAudioOnlyServer:
			if message.Event == volcEventTTSResponse && len(message.Payload) > 0 {
				return Event{Type: EventAudio, Audio: message.Payload}, nil
			}
		case volcMsgFullServerResponse:
			switch message.Event {
			case volcEventConnectionFailed:
				return Event{}, s.providerError("connection", message)
			case volcEventSessionFailed:
				return Event{}, s.providerError("session", message)
			case volcEventSessionCanceled:
				return Event{}, errors.New("Volcengine speech session was canceled")
			case volcEventSessionFinished:
				s.stateMu.Lock()
				s.sessionCompleted = true
				s.stateMu.Unlock()
				if err := s.finishConnection(ctx); err != nil {
					return Event{}, err
				}
			case volcEventConnectionFinished:
				s.stateMu.Lock()
				s.connectionCompleted = true
				sessionCompleted := s.sessionCompleted
				s.stateMu.Unlock()
				if !sessionCompleted {
					return Event{}, errors.New(
						"Volcengine speech connection ended before the session",
					)
				}
				return Event{Type: EventCompleted}, nil
			}
		default:
			return Event{}, fmt.Errorf(
				"unexpected Volcengine speech message type %d",
				message.MessageType,
			)
		}
	}
}

func (s *volcengineSession) Close() error {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return nil
	}
	s.closed = true
	finished := s.finished
	sessionCompleted := s.sessionCompleted
	connectionCompleted := s.connectionCompleted
	s.stateMu.Unlock()

	closeContext, cancel := context.WithTimeout(
		context.Background(), time.Second,
	)
	defer cancel()
	if !finished && !sessionCompleted {
		_ = s.writeEvent(
			closeContext, volcEventCancelSession, s.sessionID, []byte("{}"),
		)
	}
	if !connectionCompleted {
		_ = s.writeEvent(
			closeContext, volcEventFinishConnection, "", []byte("{}"),
		)
	}
	return s.connection.Close()
}

func (s *volcengineSession) requestPayload(event int32, text string) ([]byte, error) {
	additions, err := json.Marshal(map[string]bool{
		"disable_markdown_filter": true,
		"disable_emoji_filter":    true,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(volcengineRequest{
		Event: event,
		ReqParams: volcengineRequestParams{
			Speaker: s.voice,
			Text:    text,
			AudioParams: volcengineAudioRequestParams{
				Format: s.audio.Format, SampleRate: s.audio.SampleRate,
				SpeechRate: s.speedRate,
			},
			Additions: string(additions),
		},
	})
}

func (s *volcengineSession) waitForEvent(
	ctx context.Context,
	expected int32,
) (volcMessage, error) {
	for {
		message, err := s.readMessage(ctx)
		if err != nil {
			return volcMessage{}, err
		}
		if message.MessageType == volcMsgError ||
			message.Event == volcEventConnectionFailed ||
			message.Event == volcEventSessionFailed {
			return volcMessage{}, s.providerError("startup", message)
		}
		if message.MessageType != volcMsgFullServerResponse ||
			message.Event != expected {
			return volcMessage{}, fmt.Errorf(
				"unexpected Volcengine speech startup event %d", message.Event,
			)
		}
		return message, nil
	}
}

func (s *volcengineSession) readMessage(ctx context.Context) (volcMessage, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.connection.SetReadDeadline(deadline)
	} else {
		_ = s.connection.SetReadDeadline(time.Time{})
	}
	messageType, payload, err := s.connection.ReadMessage()
	if err != nil {
		return volcMessage{}, fmt.Errorf("read Volcengine speech response: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		return volcMessage{}, errors.New("Volcengine speech returned a non-binary frame")
	}
	message, err := unmarshalVolcMessage(payload)
	if err != nil {
		return volcMessage{}, err
	}
	return message, nil
}

func (s *volcengineSession) writeEvent(
	ctx context.Context,
	event int32,
	sessionID string,
	payload []byte,
) error {
	frame, err := marshalVolcClientEvent(event, sessionID, payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.connection.SetWriteDeadline(deadline)
	} else {
		_ = s.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	if err := s.connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fmt.Errorf("send Volcengine speech event %d: %w", event, err)
	}
	return nil
}

func (s *volcengineSession) finishConnection(ctx context.Context) error {
	s.stateMu.Lock()
	connectionCompleted := s.connectionCompleted
	s.stateMu.Unlock()
	if connectionCompleted {
		return nil
	}
	return s.writeEvent(
		ctx, volcEventFinishConnection, "", []byte("{}"),
	)
}

func (s *volcengineSession) providerError(scope string, message volcMessage) error {
	detail := strings.TrimSpace(string(message.Payload))
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(message.Payload, &payload) == nil &&
		strings.TrimSpace(payload.Message) != "" {
		detail = strings.TrimSpace(payload.Message)
	}
	if len(detail) > 500 {
		detail = detail[:500]
	}
	if detail == "" {
		detail = "provider rejected the request"
	}
	logSuffix := ""
	if s.logID != "" {
		logSuffix = "; log ID " + s.logID
	}
	if message.ErrorCode != 0 {
		return fmt.Errorf(
			"Volcengine speech %s failed (%d): %s%s",
			scope, message.ErrorCode, detail, logSuffix,
		)
	}
	return fmt.Errorf(
		"Volcengine speech %s failed at event %d: %s%s",
		scope, message.Event, detail, logSuffix,
	)
}

func speedToVolcengineRate(speed float64) int {
	return int(math.Round((speed - 1) * 100))
}
