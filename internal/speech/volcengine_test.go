package speech

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

func TestVolcengineProviderUsesV3BidirectionalProtocol(t *testing.T) {
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	speechServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "new-console-api-key" {
			t.Errorf("X-Api-Key = %q", r.Header.Get("X-Api-Key"))
		}
		if r.Header.Get("X-Api-Resource-Id") != "seed-tts-2.0" {
			t.Errorf("X-Api-Resource-Id = %q", r.Header.Get("X-Api-Resource-Id"))
		}
		if r.Header.Get("X-Api-Connect-Id") == "" {
			t.Error("X-Api-Connect-Id is empty")
		}
		if r.Header.Get("X-Control-Require-Usage-Tokens-Return") != "*" {
			t.Errorf(
				"X-Control-Require-Usage-Tokens-Return = %q",
				r.Header.Get("X-Control-Require-Usage-Tokens-Return"),
			)
		}
		for _, legacy := range []string{
			"Authorization", "X-Api-App-Key", "X-Api-Access-Key",
		} {
			if r.Header.Get(legacy) != "" {
				t.Errorf("legacy header %s must not be sent", legacy)
			}
		}
		connection, err := upgrader.Upgrade(w, r, http.Header{
			"X-Tt-Logid": []string{"test-log-id"},
		})
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()

		startConnection, err := readVolcTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if startConnection.Event != volcEventStartConnection ||
			string(startConnection.Payload) != "{}" {
			t.Errorf("start connection = %#v", startConnection)
		}
		if err := writeVolcTestMessage(connection, volcMessage{
			MessageType: volcMsgFullServerResponse,
			Flag:        volcFlagWithEvent, Serialization: volcSerializationJSON,
			Event: volcEventConnectionStarted, ConnectID: "provider-connect-id",
			Payload: []byte("{}"),
		}); err != nil {
			serverErrors <- err
			return
		}

		startSession, err := readVolcTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if startSession.Event != volcEventStartSession || startSession.SessionID == "" {
			t.Errorf("start session = %#v", startSession)
		}
		assertVolcRequest(
			t, startSession.Payload, volcEventStartSession, "", 25,
		)
		if err := writeVolcTestMessage(connection, volcMessage{
			MessageType: volcMsgFullServerResponse,
			Flag:        volcFlagWithEvent, Serialization: volcSerializationJSON,
			Event: volcEventSessionStarted, SessionID: startSession.SessionID,
			Payload: []byte("{}"),
		}); err != nil {
			serverErrors <- err
			return
		}

		task, err := readVolcTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if task.Event != volcEventTaskRequest ||
			task.SessionID != startSession.SessionID {
			t.Errorf("task request = %#v", task)
		}
		assertVolcRequest(
			t, task.Payload, volcEventTaskRequest, "你好，世界。", 25,
		)
		if err := writeVolcTestMessage(connection, volcMessage{
			MessageType: volcMsgAudioOnlyServer,
			Flag:        volcFlagWithEvent, Serialization: volcSerializationRaw,
			Event: volcEventTTSResponse, SessionID: startSession.SessionID,
			Payload: []byte{1, 2, 3, 4},
		}); err != nil {
			serverErrors <- err
			return
		}

		finishSession, err := readVolcTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if finishSession.Event != volcEventFinishSession ||
			finishSession.SessionID != startSession.SessionID {
			t.Errorf("finish session = %#v", finishSession)
		}
		if err := writeVolcTestMessage(connection, volcMessage{
			MessageType: volcMsgFullServerResponse,
			Flag:        volcFlagWithEvent, Serialization: volcSerializationJSON,
			Event: volcEventSessionFinished, SessionID: startSession.SessionID,
			Payload: []byte(`{"usage":{"text_words":6}}`),
		}); err != nil {
			serverErrors <- err
			return
		}

		finishConnection, err := readVolcTestMessage(connection)
		if err != nil {
			serverErrors <- err
			return
		}
		if finishConnection.Event != volcEventFinishConnection {
			t.Errorf("finish connection = %#v", finishConnection)
		}
		if err := writeVolcTestMessage(connection, volcMessage{
			MessageType: volcMsgFullServerResponse,
			Flag:        volcFlagWithEvent, Serialization: volcSerializationJSON,
			Event: volcEventConnectionFinished, ConnectID: "provider-connect-id",
			Payload: []byte("{}"),
		}); err != nil {
			serverErrors <- err
		}
	}))
	defer speechServer.Close()

	speechEndpoint, _ := url.Parse("ws" + speechServer.URL[len("http"):])
	provider := NewVolcengineProvider(config.VolcengineSpeech{
		Endpoint: speechEndpoint, APIKey: "new-console-api-key",
		ResourceID: "seed-tts-2.0",
		Voices: []config.SpeechVoice{{
			ID: "zh_female_tianmeitaozi_mars_bigtts", Label: "甜美桃子",
		}},
		Format: "pcm", SampleRate: 24000,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := provider.Open(ctx, SessionConfig{
		Voice: "zh_female_tianmeitaozi_mars_bigtts", Speed: 1.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SendText(ctx, "你好，世界。"); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	audio, err := session.ReadEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if audio.Type != EventAudio || len(audio.Audio) != 4 {
		t.Fatalf("audio event = %#v", audio)
	}
	completed, err := session.ReadEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Type != EventCompleted {
		t.Fatalf("completed event = %#v", completed)
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
}

func TestVolcengineProviderRejectsUnknownVoiceBeforeNetwork(t *testing.T) {
	endpoint, _ := url.Parse("wss://speech.invalid/api/v3/tts/bidirection")
	provider := NewVolcengineProvider(config.VolcengineSpeech{
		Endpoint: endpoint, APIKey: "api-key", ResourceID: "seed-tts-2.0",
		Voices: []config.SpeechVoice{{ID: "allowed", Label: "Allowed"}},
		Format: "pcm", SampleRate: 24000,
	})
	if _, err := provider.Open(
		context.Background(), SessionConfig{Voice: "unknown", Speed: 1},
	); err == nil {
		t.Fatal("Open() error = nil, want unknown voice error")
	}
}

func TestVolcProtocolRejectsTruncatedPayload(t *testing.T) {
	frame, err := marshalVolcClientEvent(
		volcEventTaskRequest, "session", []byte(`{"event":200}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unmarshalVolcMessage(frame[:len(frame)-1]); err == nil {
		t.Fatal("unmarshal truncated frame error = nil")
	}
}

func assertVolcRequest(
	t *testing.T,
	raw []byte,
	event int32,
	text string,
	speechRate int,
) {
	t.Helper()
	var request volcengineRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Event != event ||
		request.ReqParams.Speaker != "zh_female_tianmeitaozi_mars_bigtts" ||
		request.ReqParams.Text != text ||
		request.ReqParams.AudioParams.Format != "pcm" ||
		request.ReqParams.AudioParams.SampleRate != 24000 ||
		request.ReqParams.AudioParams.SpeechRate != speechRate {
		t.Errorf("request = %#v", request)
	}
	var additions map[string]bool
	if err := json.Unmarshal([]byte(request.ReqParams.Additions), &additions); err != nil {
		t.Fatal(err)
	}
	if !additions["disable_markdown_filter"] ||
		!additions["disable_emoji_filter"] {
		t.Errorf("additions = %#v", additions)
	}
}

func readVolcTestMessage(connection *websocket.Conn) (volcMessage, error) {
	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		return volcMessage{}, err
	}
	if messageType != websocket.BinaryMessage {
		return volcMessage{}, context.Canceled
	}
	return unmarshalVolcMessage(payload)
}

func writeVolcTestMessage(connection *websocket.Conn, message volcMessage) error {
	frame, err := marshalVolcMessage(message)
	if err != nil {
		return err
	}
	return connection.WriteMessage(websocket.BinaryMessage, frame)
}
