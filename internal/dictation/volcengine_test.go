package dictation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

func TestVolcengineProviderStreamsTwoPassRecognition(t *testing.T) {
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	asrServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.URL.Path != "/api/v3/sauc/bigmodel_async" {
			serverErrors <- fmt.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != "new-console-api-key" {
			serverErrors <- fmt.Errorf(
				"X-Api-Key = %q",
				r.Header.Get("X-Api-Key"),
			)
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Api-Resource-Id") !=
			"volc.seedasr.sauc.duration" {
			serverErrors <- fmt.Errorf(
				"X-Api-Resource-Id = %q",
				r.Header.Get("X-Api-Resource-Id"),
			)
			http.Error(w, "bad resource", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Api-Request-Id") == "" ||
			r.Header.Get("X-Api-Sequence") != "-1" {
			serverErrors <- fmt.Errorf(
				"request ID/sequence = %q/%q",
				r.Header.Get("X-Api-Request-Id"),
				r.Header.Get("X-Api-Sequence"),
			)
			http.Error(w, "bad headers", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "" {
			serverErrors <- errors.New("legacy Authorization header was sent")
			http.Error(w, "legacy header", http.StatusBadRequest)
			return
		}
		connection, err := upgrader.Upgrade(w, r, http.Header{
			"X-Tt-Logid": []string{"asr-test-log-id"},
		})
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()

		start, err := readVolcDictationClientTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if start.MessageType != volcMsgFullClientRequest ||
			start.Sequence != 1 {
			serverErrors <- fmt.Errorf("start request = %#v", start)
			return
		}
		var request volcengineRequest
		if err := json.Unmarshal(start.Payload, &request); err != nil {
			serverErrors <- err
			return
		}
		if request.User.UID != "safe-user-id" ||
			request.Audio.Format != "pcm" ||
			request.Audio.Codec != "raw" ||
			request.Audio.Rate != 16000 ||
			request.Audio.Bits != 16 ||
			request.Audio.Channels != 1 ||
			request.Request.ModelName != "bigmodel" ||
			!request.Request.EnableITN ||
			!request.Request.EnablePunctuation ||
			!request.Request.ShowUtterances ||
			!request.Request.EnableNonstream ||
			!request.Request.EnableLID ||
			request.Request.SSDVersion != "200" ||
			request.Request.ResultType != "full" {
			serverErrors <- fmt.Errorf("start payload = %#v", request)
			return
		}
		if request.Request.Corpus == nil {
			serverErrors <- errors.New("dialog context is missing")
			return
		}
		var dialogContext volcengineDialogContext
		if err := json.Unmarshal(
			[]byte(request.Request.Corpus.Context),
			&dialogContext,
		); err != nil {
			serverErrors <- err
			return
		}
		if dialogContext.Type != "dialog_ctx" ||
			len(dialogContext.Data) != 2 ||
			dialogContext.Data[0].Text != "录音前输入框草稿：设计会员体系" ||
			dialogContext.Data[1].Text != "当前餐厅档案：主要客群：家庭聚餐" {
			serverErrors <- fmt.Errorf(
				"dialog context = %#v",
				dialogContext,
			)
			return
		}
		if err := connection.WriteMessage(
			websocket.BinaryMessage,
			marshalVolcServerTestFrame(
				t,
				volcMsgFullServerResponse,
				volcFlagPositiveSequence,
				1,
				0,
				[]byte(`{}`),
				true,
			),
		); err != nil {
			serverErrors <- err
			return
		}

		firstAudio, err := readVolcDictationClientTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		finalAudio, err := readVolcDictationClientTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if firstAudio.MessageType != volcMsgAudioOnlyClient ||
			firstAudio.Sequence != 2 ||
			firstAudio.Flag != volcFlagPositiveSequence ||
			string(firstAudio.Payload) != "\x01\x02\x03\x04" {
			serverErrors <- fmt.Errorf("first audio = %#v", firstAudio)
			return
		}
		if finalAudio.Sequence != -3 ||
			finalAudio.Flag != volcFlagLastSequence ||
			string(finalAudio.Payload) != "\x05\x06\x07\x08" {
			serverErrors <- fmt.Errorf("final audio = %#v", finalAudio)
			return
		}
		if err := connection.WriteMessage(
			websocket.BinaryMessage,
			marshalVolcServerTestFrame(
				t,
				volcMsgFullServerResponse,
				volcFlagPositiveSequence,
				2,
				0,
				[]byte(`{
					"result":{
						"text":"侬好，今朝生意哪能？",
						"utterances":[{"text":"侬好，今朝生意哪能？","definite":false}]
					}
				}`),
				true,
			),
		); err != nil {
			serverErrors <- err
			return
		}
		if err := connection.WriteMessage(
			websocket.BinaryMessage,
			marshalVolcServerTestFrame(
				t,
				volcMsgFullServerResponse,
				volcFlagLastSequence,
				-3,
				0,
				[]byte(`{
					"result":{
						"text":"侬好，今朝生意怎么样？",
						"utterances":[{"text":"侬好，今朝生意怎么样？","definite":true}]
					}
				}`),
				true,
			),
		); err != nil {
			serverErrors <- err
		}
	}))
	defer asrServer.Close()

	endpoint, _ := url.Parse(
		"ws" + asrServer.URL[len("http"):] +
			"/api/v3/sauc/bigmodel_async",
	)
	provider := NewVolcengineProvider(config.VolcengineDictation{
		Endpoint: endpoint, APIKey: "new-console-api-key",
		ResourceID: "volc.seedasr.sauc.duration",
		Format:     "pcm", SampleRate: 16000, Bits: 16, Channels: 1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := provider.Open(ctx, SessionConfig{
		UserID: "safe-user-id",
		Context: []string{
			"录音前输入框草稿：设计会员体系",
			"当前餐厅档案：主要客群：家庭聚餐",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SendAudio(ctx, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(ctx, []byte{5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}

	partial, err := session.ReadEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Type != EventTranscript ||
		partial.Text != "侬好，今朝生意哪能？" ||
		partial.Definite {
		t.Fatalf("partial event = %#v", partial)
	}
	completed, err := session.ReadEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Type != EventCompleted ||
		completed.Text != "侬好，今朝生意怎么样？" ||
		!completed.Definite {
		t.Fatalf("completed event = %#v", completed)
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
}

func TestVolcengineProviderRejectsInvalidStartupAcknowledgement(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	asrServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readVolcDictationClientTestMessage(connection); err != nil {
			return
		}
		_ = connection.WriteMessage(
			websocket.BinaryMessage,
			marshalVolcServerTestFrame(
				t,
				volcMsgFullServerResponse,
				volcFlagPositiveSequence,
				2,
				0,
				[]byte(`{}`),
				false,
			),
		)
	}))
	defer asrServer.Close()

	provider := newVolcengineTestProvider(t, asrServer.URL)
	_, err := provider.Open(
		context.Background(),
		SessionConfig{UserID: "safe-user-id"},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "invalid startup acknowledgement") {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestVolcengineProviderAcceptsSequenceLessStartupAcknowledgement(
	t *testing.T,
) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	asrServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readVolcDictationClientTestMessage(connection); err != nil {
			return
		}
		_ = connection.WriteMessage(
			websocket.BinaryMessage,
			marshalVolcServerTestFrame(
				t,
				volcMsgFullServerResponse,
				volcFlagNoSequence,
				0,
				0,
				[]byte(`{"result":{"text":"","utterances":[]}}`),
				false,
			),
		)
	}))
	defer asrServer.Close()

	provider := newVolcengineTestProvider(t, asrServer.URL)
	session, err := provider.Open(
		context.Background(),
		SessionConfig{UserID: "safe-user-id"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVolcengineProviderClassifiesHandshakeFailures(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		body       string
		want       error
		wantDetail string
	}{
		{
			name: "resource not granted", status: http.StatusForbidden,
			body: `{"error":"requested resource not granted"}`,
			want: ErrProviderNotGranted, wantDetail: "HTTP 403",
		},
		{
			name: "authentication", status: http.StatusUnauthorized,
			body: `{"error":"invalid API Key"}`,
			want: ErrProviderAuth, wantDetail: "HTTP 401",
		},
		{
			name: "provider busy", status: http.StatusTooManyRequests,
			body: `{"error":"too many requests"}`,
			want: ErrProviderBusy, wantDetail: "HTTP 429",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			asrServer := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Tt-Logid", "handshake-log-id")
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer asrServer.Close()
			provider := newVolcengineTestProvider(t, asrServer.URL)
			_, err := provider.Open(
				context.Background(),
				SessionConfig{UserID: "safe-user-id"},
			)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Open() error = %v, want %v", err, testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.wantDetail) ||
				!strings.Contains(err.Error(), "handshake-log-id") ||
				strings.Contains(err.Error(), "new-console-api-key") {
				t.Fatalf("Open() error = %q", err)
			}
		})
	}
}

func TestVolcengineProviderMapsBusyAndNoSpeechErrors(t *testing.T) {
	session := &volcengineSession{logID: "provider-error-log-id"}
	busy := session.providerError("recognition", volcMessage{
		ErrorCode: 55000031,
		Payload:   []byte(`{"error":"quota exceeded"}`),
	})
	if !errors.Is(busy, ErrProviderBusy) ||
		!strings.Contains(busy.Error(), "provider-error-log-id") {
		t.Fatalf("busy error = %v", busy)
	}
	noSpeech := session.providerError("recognition", volcMessage{
		ErrorCode: 45000002,
		Payload:   []byte(`{"error":"no speech"}`),
	})
	if !errors.Is(noSpeech, ErrNoSpeech) {
		t.Fatalf("no-speech error = %v", noSpeech)
	}
}

func TestVolcengineProviderRequiresExactRuntimeConfiguration(t *testing.T) {
	endpoint, _ := url.Parse(
		"wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async",
	)
	valid := config.VolcengineDictation{
		Endpoint: endpoint, APIKey: "api-key",
		ResourceID: "volc.seedasr.sauc.duration",
		Format:     "pcm", SampleRate: 16000, Bits: 16, Channels: 1,
	}
	if !NewVolcengineProvider(valid).Configured() {
		t.Fatal("valid provider is not configured")
	}
	for name, mutate := range map[string]func(*config.VolcengineDictation){
		"missing key": func(cfg *config.VolcengineDictation) {
			cfg.APIKey = ""
		},
		"wrong resource": func(cfg *config.VolcengineDictation) {
			cfg.ResourceID = "other"
		},
		"wrong format": func(cfg *config.VolcengineDictation) {
			cfg.Format = "wav"
		},
		"wrong rate": func(cfg *config.VolcengineDictation) {
			cfg.SampleRate = 8000
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if NewVolcengineProvider(cfg).Configured() {
				t.Fatal("invalid provider is configured")
			}
		})
	}
}

func readVolcDictationClientTestMessage(
	connection *websocket.Conn,
) (volcMessage, error) {
	messageType, frame, err := connection.ReadMessage()
	if err != nil {
		return volcMessage{}, err
	}
	if messageType != websocket.BinaryMessage {
		return volcMessage{}, errors.New("client frame is not binary")
	}
	return decodeVolcClientTestFrame(frame)
}

func newVolcengineTestProvider(
	t *testing.T,
	serverURL string,
) *VolcengineProvider {
	t.Helper()
	endpoint, err := url.Parse("ws" + serverURL[len("http"):])
	if err != nil {
		t.Fatal(err)
	}
	return NewVolcengineProvider(config.VolcengineDictation{
		Endpoint: endpoint, APIKey: "new-console-api-key",
		ResourceID: "volc.seedasr.sauc.duration",
		Format:     "pcm", SampleRate: 16000, Bits: 16, Channels: 1,
	})
}
