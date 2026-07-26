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

func TestAliyunProviderStreamsCommandsAndAudio(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		for _, key := range []string{
			"AccessKeyId", "Action", "Format", "RegionId", "Signature",
			"SignatureMethod", "SignatureNonce", "SignatureVersion", "Timestamp", "Version",
		} {
			if query.Get(key) == "" {
				t.Errorf("token query is missing %s", key)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Token": map[string]any{
				"Id": "temporary-token", "ExpireTime": time.Now().Add(time.Hour).Unix(),
			},
		})
	}))
	defer tokenServer.Close()

	commands := make(chan string, 3)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	speechServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-NLS-Token") != "temporary-token" {
			t.Errorf("X-NLS-Token = %q", r.Header.Get("X-NLS-Token"))
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		for {
			var command aliyunCommand
			if err := connection.ReadJSON(&command); err != nil {
				return
			}
			commands <- command.Header.Name
			switch command.Header.Name {
			case "StartSynthesis":
				_ = connection.WriteJSON(aliyunCommand{Header: aliyunHeader{
					TaskID: command.Header.TaskID, Namespace: aliyunNamespace,
					Name: "SynthesisStarted", Status: 20000000,
				}})
			case "RunSynthesis":
				_ = connection.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4})
			case "StopSynthesis":
				_ = connection.WriteJSON(aliyunCommand{Header: aliyunHeader{
					TaskID: command.Header.TaskID, Namespace: aliyunNamespace,
					Name: "SynthesisCompleted", Status: 20000000,
				}})
				return
			}
		}
	}))
	defer speechServer.Close()

	tokenEndpoint, _ := url.Parse(tokenServer.URL)
	speechEndpoint, _ := url.Parse("ws" + speechServer.URL[len("http"):])
	provider := NewAliyunProvider(config.AlibabaSpeech{
		Endpoint: speechEndpoint, TokenEndpoint: tokenEndpoint,
		AppKey: "app-key", AccessKeyID: "access-key",
		AccessKeySecret: "access-secret",
		Voices:          []config.SpeechVoice{{ID: "longxiaochun", Label: "龙小淳"}},
		Format:          "pcm", SampleRate: 24000,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := provider.Open(ctx, SessionConfig{Voice: "longxiaochun", Speed: 1})
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
	for _, expected := range []string{"StartSynthesis", "RunSynthesis", "StopSynthesis"} {
		select {
		case actual := <-commands:
			if actual != expected {
				t.Fatalf("command = %q, want %q", actual, expected)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for command")
		}
	}
}

func TestAliyunProviderRejectsUnknownVoiceBeforeNetwork(t *testing.T) {
	endpoint, _ := url.Parse("wss://speech.invalid/ws/v1")
	tokenEndpoint, _ := url.Parse("https://token.invalid/")
	provider := NewAliyunProvider(config.AlibabaSpeech{
		Endpoint: endpoint, TokenEndpoint: tokenEndpoint,
		AppKey: "app", AccessKeyID: "id", AccessKeySecret: "secret",
		Voices: []config.SpeechVoice{{ID: "allowed", Label: "Allowed"}},
	})
	if _, err := provider.Open(
		context.Background(), SessionConfig{Voice: "unknown", Speed: 1},
	); err == nil {
		t.Fatal("Open() error = nil, want unknown voice error")
	}
}
