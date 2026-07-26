package speech

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

const aliyunNamespace = "FlowingSpeechSynthesizer"

type AliyunProvider struct {
	config config.AlibabaSpeech
	token  *aliyunTokenSource
	dialer *websocket.Dialer
}

func NewAliyunProvider(cfg config.AlibabaSpeech) *AliyunProvider {
	client := &http.Client{Timeout: 10 * time.Second}
	return &AliyunProvider{
		config: cfg,
		token: &aliyunTokenSource{
			endpoint: cfg.TokenEndpoint, accessKeyID: cfg.AccessKeyID,
			accessKeySecret: cfg.AccessKeySecret, client: client,
			now: time.Now, nonce: randomUUID,
		},
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			Proxy:            http.ProxyFromEnvironment,
		},
	}
}

func (p *AliyunProvider) ID() string {
	return "aliyun"
}

func (p *AliyunProvider) Configured() bool {
	return p != nil && p.config.Endpoint != nil && p.config.TokenEndpoint != nil &&
		strings.TrimSpace(p.config.AppKey) != "" &&
		strings.TrimSpace(p.config.AccessKeyID) != "" &&
		strings.TrimSpace(p.config.AccessKeySecret) != ""
}

func (p *AliyunProvider) Voices() []Voice {
	if p == nil {
		return []Voice{}
	}
	result := make([]Voice, 0, len(p.config.Voices))
	for _, voice := range p.config.Voices {
		result = append(result, Voice{ID: voice.ID, Label: voice.Label})
	}
	return result
}

func (p *AliyunProvider) Open(
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
	token, err := p.token.Token(ctx)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("X-NLS-Token", token)
	connection, response, err := p.dialer.DialContext(ctx, p.config.Endpoint.String(), headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("connect Aliyun speech service: %w", err)
	}
	taskID, err := randomHex(16)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	session := &aliyunSession{
		connection: connection,
		appKey:     p.config.AppKey,
		taskID:     taskID,
		audio: AudioConfig{
			Format: p.config.Format, SampleRate: p.config.SampleRate,
			Channels: 1, BitDepth: 16,
		},
	}
	start := aliyunCommand{
		Header: aliyunHeader{
			AppKey: p.config.AppKey, TaskID: taskID,
			Namespace: aliyunNamespace, Name: "StartSynthesis",
		},
		Payload: map[string]any{
			"voice": sessionConfig.Voice, "format": p.config.Format,
			"sample_rate": p.config.SampleRate, "volume": 50,
			"speech_rate": speedToAliyunRate(sessionConfig.Speed),
			"pitch_rate": 0,
		},
	}
	if err := session.writeCommand(ctx, start); err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.waitForStarted(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

type aliyunSession struct {
	connection *websocket.Conn
	appKey     string
	taskID     string
	audio      AudioConfig
	writeMu    sync.Mutex
	finished   bool
}

type aliyunCommand struct {
	Header  aliyunHeader   `json:"header"`
	Payload map[string]any `json:"payload,omitempty"`
}

type aliyunHeader struct {
	AppKey        string `json:"appkey,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	TaskID        string `json:"task_id"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	Status        int    `json:"status,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
}

func (s *aliyunSession) AudioConfig() AudioConfig {
	return s.audio
}

func (s *aliyunSession) SendText(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if s.finished {
		return errors.New("speech session is already finishing")
	}
	return s.writeCommand(ctx, aliyunCommand{
		Header: aliyunHeader{
			AppKey: s.appKey, TaskID: s.taskID,
			Namespace: aliyunNamespace, Name: "RunSynthesis",
		},
		Payload: map[string]any{"text": text},
	})
}

func (s *aliyunSession) Finish(ctx context.Context) error {
	if s.finished {
		return nil
	}
	s.finished = true
	return s.writeCommand(ctx, aliyunCommand{
		Header: aliyunHeader{
			AppKey: s.appKey, TaskID: s.taskID,
			Namespace: aliyunNamespace, Name: "StopSynthesis",
		},
	})
}

func (s *aliyunSession) ReadEvent(ctx context.Context) (Event, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.connection.SetReadDeadline(deadline)
	} else {
		_ = s.connection.SetReadDeadline(time.Time{})
	}
	messageType, payload, err := s.connection.ReadMessage()
	if err != nil {
		return Event{}, err
	}
	if messageType == websocket.BinaryMessage {
		return Event{Type: EventAudio, Audio: payload}, nil
	}
	if messageType != websocket.TextMessage {
		return Event{}, nil
	}
	var message aliyunCommand
	if err := json.Unmarshal(payload, &message); err != nil {
		return Event{}, fmt.Errorf("decode Aliyun speech event: %w", err)
	}
	if message.Header.Status != 0 && message.Header.Status != 20000000 {
		return Event{}, fmt.Errorf(
			"Aliyun speech request failed (%d): %s",
			message.Header.Status, message.Header.StatusMessage,
		)
	}
	if message.Header.Name == "SynthesisCompleted" {
		return Event{Type: EventCompleted}, nil
	}
	return Event{}, nil
}

func (s *aliyunSession) Close() error {
	return s.connection.Close()
}

func (s *aliyunSession) waitForStarted(ctx context.Context) error {
	for {
		if deadline, ok := ctx.Deadline(); ok {
			_ = s.connection.SetReadDeadline(deadline)
		}
		messageType, payload, err := s.connection.ReadMessage()
		if err != nil {
			return fmt.Errorf("wait for Aliyun speech start: %w", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message aliyunCommand
		if err := json.Unmarshal(payload, &message); err != nil {
			return fmt.Errorf("decode Aliyun speech start: %w", err)
		}
		if message.Header.Status != 0 && message.Header.Status != 20000000 {
			return fmt.Errorf(
				"Aliyun speech start failed (%d): %s",
				message.Header.Status, message.Header.StatusMessage,
			)
		}
		if message.Header.Name == "SynthesisStarted" {
			_ = s.connection.SetReadDeadline(time.Time{})
			return nil
		}
	}
}

func (s *aliyunSession) writeCommand(ctx context.Context, command aliyunCommand) error {
	messageID, err := randomHex(16)
	if err != nil {
		return err
	}
	command.Header.MessageID = messageID
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.connection.SetWriteDeadline(deadline)
	} else {
		_ = s.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	if err := s.connection.WriteJSON(command); err != nil {
		return fmt.Errorf("send Aliyun speech command: %w", err)
	}
	return nil
}

func speedToAliyunRate(speed float64) int {
	if speed <= 1 {
		return int((speed - 1) * 1000)
	}
	return int((speed - 1) * 500)
}

func voiceAllowed(id string, voices []Voice) bool {
	for _, voice := range voices {
		if voice.ID == id {
			return true
		}
	}
	return false
}

func randomHex(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16],
	), nil
}
