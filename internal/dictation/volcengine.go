package dictation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

const volcengineStartupTimeout = 10 * time.Second

type VolcengineProvider struct {
	config config.VolcengineDictation
	dialer *websocket.Dialer
}

func NewVolcengineProvider(
	cfg config.VolcengineDictation,
) *VolcengineProvider {
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
		p.config.ResourceID == "volc.seedasr.sauc.duration" &&
		p.config.Format == "pcm" &&
		p.config.SampleRate == 16000 &&
		p.config.Bits == 16 &&
		p.config.Channels == 1
}

func (p *VolcengineProvider) Open(
	ctx context.Context,
	sessionConfig SessionConfig,
) (Session, error) {
	if !p.Configured() {
		return nil, ErrProviderUnavailable
	}
	requestID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("X-Api-Key", p.config.APIKey)
	headers.Set("X-Api-Resource-Id", p.config.ResourceID)
	headers.Set("X-Api-Request-Id", requestID)
	headers.Set("X-Api-Sequence", "-1")
	connection, response, err := p.dialer.DialContext(
		ctx, p.config.Endpoint.String(), headers,
	)
	logID := ""
	statusCode := 0
	handshakeDetail := ""
	if response != nil {
		statusCode = response.StatusCode
		logID = strings.TrimSpace(response.Header.Get("X-Tt-Logid"))
		if response.Body != nil {
			if err != nil {
				body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
				handshakeDetail = volcengineHandshakeDetail(body)
			}
			_ = response.Body.Close()
		}
	}
	if err != nil {
		return nil, classifyVolcengineHandshake(
			err, statusCode, handshakeDetail, logID,
		)
	}
	connection.SetReadLimit(maxVolcDictationFrameBytes)
	session := &volcengineSession{
		connection: connection,
		logID:      logID,
		sequence:   2,
	}
	payload, err := p.requestPayload(sessionConfig)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	frame, err := marshalVolcFullRequest(payload)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	startupContext, cancel := context.WithTimeout(
		ctx, volcengineStartupTimeout,
	)
	defer cancel()
	if err := session.writeFrame(startupContext, frame); err != nil {
		_ = session.Close()
		return nil, err
	}
	message, err := session.readMessage(startupContext)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if message.MessageType == volcMsgError {
		_ = session.Close()
		return nil, session.providerError("startup", message)
	}
	if message.MessageType != volcMsgFullServerResponse ||
		message.Flag&volcFlagPositiveSequence == 0 ||
		message.Sequence != 1 {
		_ = session.Close()
		return nil, errors.New(
			"Volcengine dictation returned an invalid startup acknowledgement",
		)
	}
	if message.Last {
		_ = session.Close()
		return nil, errors.New(
			"Volcengine dictation ended during startup",
		)
	}
	_ = connection.SetReadDeadline(time.Time{})
	return session, nil
}

type volcengineRequest struct {
	User    volcengineUser    `json:"user"`
	Audio   volcengineAudio   `json:"audio"`
	Request volcengineOptions `json:"request"`
}

type volcengineUser struct {
	UID string `json:"uid"`
}

type volcengineAudio struct {
	Format   string `json:"format"`
	Codec    string `json:"codec"`
	Rate     int    `json:"rate"`
	Bits     int    `json:"bits"`
	Channels int    `json:"channel"`
}

type volcengineOptions struct {
	ModelName         string            `json:"model_name"`
	EnableITN         bool              `json:"enable_itn"`
	EnablePunctuation bool              `json:"enable_punc"`
	EnableDDC         bool              `json:"enable_ddc"`
	ShowUtterances    bool              `json:"show_utterances"`
	EnableNonstream   bool              `json:"enable_nonstream"`
	EnableLID         bool              `json:"enable_lid"`
	SSDVersion        string            `json:"ssd_version"`
	ResultType        string            `json:"result_type"`
	Corpus            *volcengineCorpus `json:"corpus,omitempty"`
}

type volcengineCorpus struct {
	Context string `json:"context"`
}

type volcengineDialogContext struct {
	Type string                   `json:"context_type"`
	Data []volcengineContextEntry `json:"context_data"`
}

type volcengineContextEntry struct {
	Text string `json:"text"`
}

func (p *VolcengineProvider) requestPayload(
	sessionConfig SessionConfig,
) ([]byte, error) {
	request := volcengineRequest{
		User: volcengineUser{UID: strings.TrimSpace(sessionConfig.UserID)},
		Audio: volcengineAudio{
			Format: p.config.Format, Codec: "raw",
			Rate: p.config.SampleRate, Bits: p.config.Bits,
			Channels: p.config.Channels,
		},
		Request: volcengineOptions{
			ModelName: "bigmodel", EnableITN: true,
			EnablePunctuation: true, EnableDDC: false,
			ShowUtterances: true, EnableNonstream: true,
			EnableLID: true, SSDVersion: "200", ResultType: "full",
		},
	}
	contextEntries := make([]volcengineContextEntry, 0, len(sessionConfig.Context))
	for _, value := range sessionConfig.Context {
		value = strings.TrimSpace(value)
		if value == "" || !utf8.ValidString(value) {
			continue
		}
		if len(value) > 2000 {
			value = value[:2000]
			for !utf8.ValidString(value) {
				value = value[:len(value)-1]
			}
		}
		contextEntries = append(contextEntries, volcengineContextEntry{Text: value})
		if len(contextEntries) == 20 {
			break
		}
	}
	if len(contextEntries) > 0 {
		contextJSON, err := json.Marshal(volcengineDialogContext{
			Type: "dialog_ctx", Data: contextEntries,
		})
		if err != nil {
			return nil, err
		}
		request.Request.Corpus = &volcengineCorpus{Context: string(contextJSON)}
	}
	return json.Marshal(request)
}

type volcengineSession struct {
	connection *websocket.Conn
	logID      string

	writeMu    sync.Mutex
	stateMu    sync.Mutex
	sequence   int32
	finished   bool
	closed     bool
	latestText string
}

func (s *volcengineSession) SendAudio(
	ctx context.Context,
	audio []byte,
) error {
	if len(audio) == 0 || len(audio) > maxVolcAudioFrameBytes ||
		len(audio)%2 != 0 {
		return errors.New("dictation audio frame is invalid")
	}
	s.stateMu.Lock()
	if s.finished {
		s.stateMu.Unlock()
		return errors.New("dictation session is already finishing")
	}
	sequence := s.sequence
	s.sequence++
	s.stateMu.Unlock()
	frame, err := marshalVolcAudio(sequence, audio, false)
	if err != nil {
		return err
	}
	return s.writeFrame(ctx, frame)
}

func (s *volcengineSession) Finish(
	ctx context.Context,
	finalAudio []byte,
) error {
	if len(finalAudio) == 0 || len(finalAudio) > maxVolcAudioFrameBytes ||
		len(finalAudio)%2 != 0 {
		return errors.New("final dictation audio frame is invalid")
	}
	s.stateMu.Lock()
	if s.finished {
		s.stateMu.Unlock()
		return nil
	}
	s.finished = true
	sequence := s.sequence
	s.sequence++
	s.stateMu.Unlock()
	frame, err := marshalVolcAudio(sequence, finalAudio, true)
	if err != nil {
		return err
	}
	return s.writeFrame(ctx, frame)
}

func (s *volcengineSession) ReadEvent(
	ctx context.Context,
) (Event, error) {
	for {
		message, err := s.readMessage(ctx)
		if err != nil {
			return Event{}, err
		}
		if message.MessageType == volcMsgError {
			return Event{}, s.providerError("recognition", message)
		}
		var payload volcengineResponse
		if len(message.Payload) > 0 {
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return Event{}, fmt.Errorf(
					"decode Volcengine dictation response: %w", err,
				)
			}
			if strings.TrimSpace(payload.Error) != "" {
				return Event{}, s.providerPayloadError(payload.Error)
			}
		}
		text := strings.TrimSpace(payload.Result.Text)
		if text != "" {
			s.latestText = text
		}
		if message.Last {
			return Event{
				Type: EventCompleted, Text: s.latestText, Definite: true,
			}, nil
		}
		if text == "" {
			continue
		}
		definite := len(payload.Result.Utterances) > 0
		for _, utterance := range payload.Result.Utterances {
			if !utterance.Definite {
				definite = false
				break
			}
		}
		return Event{
			Type: EventTranscript, Text: text, Definite: definite,
		}, nil
	}
}

type volcengineResponse struct {
	Result struct {
		Text       string `json:"text"`
		Utterances []struct {
			Definite bool   `json:"definite"`
			Text     string `json:"text"`
		} `json:"utterances,omitempty"`
	} `json:"result"`
	Error string `json:"error,omitempty"`
}

func (s *volcengineSession) Close() error {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return nil
	}
	s.closed = true
	s.stateMu.Unlock()
	return s.connection.Close()
}

func (s *volcengineSession) readMessage(
	ctx context.Context,
) (volcMessage, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.connection.SetReadDeadline(deadline)
	} else {
		_ = s.connection.SetReadDeadline(time.Time{})
	}
	messageType, payload, err := s.connection.ReadMessage()
	if err != nil {
		return volcMessage{}, fmt.Errorf(
			"read Volcengine dictation response: %w", err,
		)
	}
	if messageType != websocket.BinaryMessage {
		return volcMessage{}, errors.New(
			"Volcengine dictation returned a non-binary frame",
		)
	}
	return unmarshalVolcResponse(payload)
}

func (s *volcengineSession) writeFrame(
	ctx context.Context,
	frame []byte,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.connection.SetWriteDeadline(deadline)
	} else {
		_ = s.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	if err := s.connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fmt.Errorf("send Volcengine dictation frame: %w", err)
	}
	return nil
}

func (s *volcengineSession) providerError(
	scope string,
	message volcMessage,
) error {
	detail := strings.TrimSpace(string(message.Payload))
	if detail == "" {
		detail = "provider rejected the request"
	}
	if len(detail) > 500 {
		detail = detail[:500]
	}
	err := fmt.Errorf(
		"Volcengine dictation %s failed (%d): %s%s",
		scope,
		message.ErrorCode,
		detail,
		logIDSuffix(s.logID),
	)
	if message.ErrorCode == 55000031 {
		return fmt.Errorf("%w: %v", ErrProviderBusy, err)
	}
	if message.ErrorCode == 45000002 {
		return fmt.Errorf("%w: %v", ErrNoSpeech, err)
	}
	return err
}

func (s *volcengineSession) providerPayloadError(detail string) error {
	detail = strings.TrimSpace(detail)
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return fmt.Errorf(
		"Volcengine dictation failed: %s%s",
		detail,
		logIDSuffix(s.logID),
	)
}

func classifyVolcengineHandshake(
	dialErr error,
	statusCode int,
	detail string,
	logID string,
) error {
	detail = strings.TrimSpace(detail)
	lowerDetail := strings.ToLower(detail)
	suffix := ""
	if statusCode > 0 {
		suffix = fmt.Sprintf(" (HTTP %d", statusCode)
		if detail != "" {
			suffix += ": " + detail
		}
		if logID != "" {
			suffix += "; log ID " + logID
		}
		suffix += ")"
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return fmt.Errorf(
			"%w: connect Volcengine dictation%s",
			ErrProviderBusy, suffix,
		)
	case statusCode == http.StatusForbidden:
		return fmt.Errorf(
			"%w: connect Volcengine dictation%s",
			ErrProviderNotGranted, suffix,
		)
	case statusCode == http.StatusUnauthorized ||
		strings.Contains(lowerDetail, "api key") ||
		strings.Contains(lowerDetail, "authentication"):
		return fmt.Errorf(
			"%w: connect Volcengine dictation%s",
			ErrProviderAuth, suffix,
		)
	default:
		return fmt.Errorf(
			"connect Volcengine dictation: %w%s", dialErr, suffix,
		)
	}
}

func volcengineHandshakeDetail(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	detail := strings.TrimSpace(payload.Error)
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return detail
}

func logIDSuffix(logID string) string {
	if strings.TrimSpace(logID) == "" {
		return ""
	}
	return "; log ID " + strings.TrimSpace(logID)
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4], value[4:6], value[6:8],
		value[8:10], value[10:16],
	), nil
}
