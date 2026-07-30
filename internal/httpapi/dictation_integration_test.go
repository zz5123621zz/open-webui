package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/dictation"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestDictationSettingsAreScopedConfiguredAndAudited(t *testing.T) {
	fixture := startDictationIntegrationApp(t, true)
	memberCookie, _ := loginTestUser(
		t,
		fixture.server.URL,
		"dictation-user",
		"test-password",
	)
	adminCookie, adminCSRF := loginTestUser(
		t,
		fixture.server.URL,
		"dictation-admin",
		"test-password",
	)

	member := authenticatedRequest(
		t,
		http.MethodGet,
		fixture.server.URL+"/api/v1/me/dictation",
		memberCookie,
		"",
		"",
	)
	defer member.Body.Close()
	if member.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(member.Body)
		t.Fatalf("member setting status=%d body=%s", member.StatusCode, body)
	}
	var memberPayload struct {
		Dictation map[string]any `json:"dictation"`
	}
	if err := json.NewDecoder(member.Body).Decode(&memberPayload); err != nil {
		t.Fatal(err)
	}
	if memberPayload.Dictation["enabled"] != true ||
		memberPayload.Dictation["configured"] != true ||
		memberPayload.Dictation["provider"] != "volcengine" ||
		memberPayload.Dictation["audioStored"] != false ||
		memberPayload.Dictation["maxDurationSeconds"] != float64(120) {
		t.Fatalf("member dictation payload = %#v", memberPayload.Dictation)
	}
	if _, exists := memberPayload.Dictation["resourceId"]; exists {
		t.Fatalf(
			"member payload exposed resource ID: %#v",
			memberPayload.Dictation,
		)
	}
	if _, exists := memberPayload.Dictation["concurrency"]; exists {
		t.Fatalf(
			"member payload exposed concurrency: %#v",
			memberPayload.Dictation,
		)
	}

	forbidden := authenticatedRequest(
		t,
		http.MethodGet,
		fixture.server.URL+"/api/v1/admin/dictation",
		memberCookie,
		"",
		"",
	)
	forbidden.Body.Close()
	if forbidden.StatusCode != http.StatusNotFound {
		t.Fatalf("member admin setting status = %d", forbidden.StatusCode)
	}

	admin := authenticatedRequest(
		t,
		http.MethodGet,
		fixture.server.URL+"/api/v1/admin/dictation",
		adminCookie,
		"",
		"",
	)
	defer admin.Body.Close()
	if admin.StatusCode != http.StatusOK {
		t.Fatalf("admin setting status = %d", admin.StatusCode)
	}
	var adminPayload struct {
		Dictation struct {
			ResourceID  string         `json:"resourceId"`
			Concurrency map[string]int `json:"concurrency"`
		} `json:"dictation"`
	}
	if err := json.NewDecoder(admin.Body).Decode(&adminPayload); err != nil {
		t.Fatal(err)
	}
	if adminPayload.Dictation.ResourceID !=
		"volc.seedasr.sauc.duration" ||
		adminPayload.Dictation.Concurrency["perUser"] != 1 ||
		adminPayload.Dictation.Concurrency["global"] != 2 {
		t.Fatalf("admin dictation payload = %#v", adminPayload.Dictation)
	}

	disabled := authenticatedRequest(
		t,
		http.MethodPut,
		fixture.server.URL+"/api/v1/admin/dictation",
		adminCookie,
		adminCSRF,
		`{"enabled":false}`,
	)
	defer disabled.Body.Close()
	if disabled.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(disabled.Body)
		t.Fatalf("disable status=%d body=%s", disabled.StatusCode, body)
	}
	fixture.provider.setConfigured(false)
	rejected := authenticatedRequest(
		t,
		http.MethodPut,
		fixture.server.URL+"/api/v1/admin/dictation",
		adminCookie,
		adminCSRF,
		`{"enabled":true}`,
	)
	defer rejected.Body.Close()
	rejectedBody, _ := io.ReadAll(rejected.Body)
	if rejected.StatusCode != http.StatusConflict ||
		!strings.Contains(
			string(rejectedBody),
			"dictation_provider_not_configured",
		) {
		t.Fatalf(
			"unconfigured enable status=%d body=%s",
			rejected.StatusCode,
			rejectedBody,
		)
	}
	setting, err := fixture.dataStore.DictationServiceSetting(
		context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Enabled {
		t.Fatalf("rejected enable changed setting = %#v", setting)
	}
}

func TestDictationWebSocketStreamsAudioAndKeepsContextNarrow(t *testing.T) {
	fixture := startDictationIntegrationApp(t, true)
	if _, err := fixture.dataStore.SetInitialWorkbenchByUsername(
		context.Background(),
		"dictation-user",
		"restaurant",
		"",
	); err != nil {
		t.Fatal(err)
	}
	profileDB, err := sql.Open(
		"sqlite",
		"file:"+url.PathEscape(fixture.databasePath),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profileDB.ExecContext(context.Background(), `
		INSERT INTO restaurant_profile_facts(
			user_id, field_key, value, source_message_id, created_at, updated_at
		)
		VALUES(?, '主要客群', '附近家庭聚餐顾客', NULL, 1, 1)
	`, fixture.user.ID); err != nil {
		profileDB.Close()
		t.Fatal(err)
	}
	if err := profileDB.Close(); err != nil {
		t.Fatal(err)
	}
	cookie, _ := loginTestUser(
		t,
		fixture.server.URL,
		"dictation-user",
		"test-password",
	)
	connection, response, err := dialDictationTestSocket(
		fixture.server.URL,
		cookie,
	)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		t.Fatal(err)
	}
	defer connection.Close()

	ready := readDictationTestJSON(t, connection)
	if ready["type"] != "dictation.ready" {
		t.Fatalf("ready event = %#v", ready)
	}
	if err := connection.WriteJSON(map[string]any{
		"type":  "dictation.start",
		"draft": "帮我设计餐厅会员体系",
	}); err != nil {
		t.Fatal(err)
	}
	connecting := readDictationTestJSON(t, connection)
	if connecting["type"] != "dictation.connecting" {
		t.Fatalf("connecting event = %#v", connecting)
	}
	select {
	case opened := <-fixture.provider.opened:
		if opened.UserID == fixture.user.ID ||
			len(opened.UserID) < 24 ||
			strings.Contains(opened.UserID, fixture.user.Username) {
			t.Fatalf("provider user ID = %q", opened.UserID)
		}
		if len(opened.Context) != 2 ||
			opened.Context[0] !=
				"录音前输入框草稿：帮我设计餐厅会员体系" ||
			opened.Context[1] !=
				"当前餐厅档案：主要客群：附近家庭聚餐顾客" {
			t.Fatalf("provider context = %#v", opened.Context)
		}
	case <-time.After(time.Second):
		t.Fatal("provider Open was not called")
	}
	started := readDictationTestJSON(t, connection)
	if started["type"] != "dictation.started" ||
		started["maxDurationSeconds"] != float64(120) {
		t.Fatalf("started event = %#v", started)
	}
	audio, _ := started["audio"].(map[string]any)
	if audio["format"] != "pcm" ||
		audio["sampleRate"] != float64(16000) ||
		audio["channels"] != float64(1) ||
		audio["bitDepth"] != float64(16) {
		t.Fatalf("started audio = %#v", audio)
	}

	if err := connection.WriteMessage(
		websocket.BinaryMessage,
		[]byte{1, 2, 3, 4},
	); err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(
		websocket.BinaryMessage,
		[]byte{5, 6, 7, 8},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case sent := <-fixture.session.sentAudio:
		if string(sent) != "\x01\x02\x03\x04" {
			t.Fatalf("provider SendAudio = %v", sent)
		}
	case <-time.After(time.Second):
		t.Fatal("provider SendAudio was not called")
	}
	if err := connection.WriteJSON(map[string]string{
		"type": "dictation.finish",
	}); err != nil {
		t.Fatal(err)
	}
	// A late AudioWorklet message may already be in flight when the user stops.
	// The server must ignore it after entering the finishing state.
	if err := connection.WriteMessage(
		websocket.BinaryMessage,
		[]byte{9, 10, 11, 12},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case final := <-fixture.session.finishedAudio:
		if string(final) != "\x05\x06\x07\x08" {
			t.Fatalf("provider Finish = %v", final)
		}
	case <-time.After(time.Second):
		t.Fatal("provider Finish was not called")
	}

	fixture.session.events <- dictation.Event{
		Type: dictation.EventTranscript,
		Text: "帮我设计会员体系",
	}
	partial := readDictationTestJSON(t, connection)
	if partial["type"] != "dictation.transcript" ||
		partial["text"] != "帮我设计会员体系" ||
		partial["definite"] != false {
		t.Fatalf("partial transcript = %#v", partial)
	}
	fixture.session.events <- dictation.Event{
		Type:     dictation.EventCompleted,
		Text:     "帮我设计一套餐厅会员体系。",
		Definite: true,
	}
	completed := readDictationTestJSON(t, connection)
	if completed["type"] != "dictation.completed" ||
		completed["text"] != "帮我设计一套餐厅会员体系。" {
		t.Fatalf("completed transcript = %#v", completed)
	}
	select {
	case extra := <-fixture.session.sentAudio:
		t.Fatalf("late audio reached provider: %v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDictationWebSocketRejectsUnauthorizedDisabledAndEmptyAudio(
	t *testing.T,
) {
	fixture := startDictationIntegrationApp(t, true)
	_, unauthorizedResponse, err := dialDictationTestSocket(
		fixture.server.URL,
		nil,
	)
	if err == nil {
		t.Fatal("unauthorized WebSocket dial error = nil")
	}
	if unauthorizedResponse == nil ||
		unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"unauthorized WebSocket response = %#v",
			unauthorizedResponse,
		)
	}
	unauthorizedResponse.Body.Close()

	cookie, _ := loginTestUser(
		t,
		fixture.server.URL,
		"dictation-user",
		"test-password",
	)
	wrongOriginHeaders := http.Header{}
	wrongOriginHeaders.Set("Origin", "https://evil.example")
	wrongOriginHeaders.Set("Cookie", cookie.String())
	endpoint := "ws" + fixture.server.URL[len("http"):] +
		"/api/v1/dictation/sessions"
	_, wrongOriginResponse, err := websocket.DefaultDialer.Dial(
		endpoint,
		wrongOriginHeaders,
	)
	if err == nil {
		t.Fatal("cross-origin WebSocket dial error = nil")
	}
	if wrongOriginResponse == nil ||
		wrongOriginResponse.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"cross-origin WebSocket response = %#v",
			wrongOriginResponse,
		)
	}
	wrongOriginResponse.Body.Close()

	connection, _, err := dialDictationTestSocket(
		fixture.server.URL,
		cookie,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event := readDictationTestJSON(t, connection); event["type"] !=
		"dictation.ready" {
		t.Fatalf("ready event = %#v", event)
	}
	if err := connection.WriteJSON(map[string]string{
		"type": "dictation.start",
	}); err != nil {
		t.Fatal(err)
	}
	if event := readDictationTestJSON(t, connection); event["type"] !=
		"dictation.connecting" {
		t.Fatalf("connecting event = %#v", event)
	}
	if event := readDictationTestJSON(t, connection); event["type"] !=
		"dictation.started" {
		t.Fatalf("started event = %#v", event)
	}
	if err := connection.WriteJSON(map[string]string{
		"type": "dictation.finish",
	}); err != nil {
		t.Fatal(err)
	}
	empty := readDictationTestJSON(t, connection)
	if empty["type"] != "dictation.error" ||
		empty["code"] != "dictation_audio_empty" {
		t.Fatalf("empty-audio event = %#v", empty)
	}
	connection.Close()

	if _, err := fixture.dataStore.SetDictationServiceSetting(
		context.Background(),
		fixture.admin.ID,
		false,
	); err != nil {
		t.Fatal(err)
	}
	disabledConnection, _, err := dialDictationTestSocket(
		fixture.server.URL,
		cookie,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer disabledConnection.Close()
	if event := readDictationTestJSON(
		t,
		disabledConnection,
	); event["type"] != "dictation.ready" {
		t.Fatalf("disabled ready event = %#v", event)
	}
	if err := disabledConnection.WriteJSON(map[string]string{
		"type": "dictation.start",
	}); err != nil {
		t.Fatal(err)
	}
	disabled := readDictationTestJSON(t, disabledConnection)
	if disabled["type"] != "dictation.error" ||
		disabled["code"] != "dictation_disabled" {
		t.Fatalf("disabled event = %#v", disabled)
	}
}

func TestDictationWebSocketStopsAtConfiguredDuration(t *testing.T) {
	fixture := startDictationIntegrationApp(t, true)
	fixture.app.cfg.Dictation.MaxDuration = 250 * time.Millisecond
	fixture.app.cfg.Dictation.SessionTTL = time.Second
	cookie, _ := loginTestUser(
		t,
		fixture.server.URL,
		"dictation-user",
		"test-password",
	)
	connection, _, err := dialDictationTestSocket(
		fixture.server.URL,
		cookie,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if event := readDictationTestJSON(t, connection); event["type"] !=
		"dictation.ready" {
		t.Fatalf("ready event = %#v", event)
	}
	if err := connection.WriteJSON(map[string]string{
		"type": "dictation.start",
	}); err != nil {
		t.Fatal(err)
	}
	if event := readDictationTestJSON(t, connection); event["type"] !=
		"dictation.connecting" {
		t.Fatalf("connecting event = %#v", event)
	}
	if event := readDictationTestJSON(t, connection); event["type"] !=
		"dictation.started" {
		t.Fatalf("started event = %#v", event)
	}
	if err := connection.WriteMessage(
		websocket.BinaryMessage,
		[]byte{1, 2, 3, 4},
	); err != nil {
		t.Fatal(err)
	}
	stopping := readDictationTestJSON(t, connection)
	if stopping["type"] != "dictation.stopping" ||
		stopping["reason"] != "duration_limit" {
		t.Fatalf("duration stopping event = %#v", stopping)
	}
	select {
	case final := <-fixture.session.finishedAudio:
		if string(final) != "\x01\x02\x03\x04" {
			t.Fatalf("duration-limit final audio = %v", final)
		}
	case <-time.After(time.Second):
		t.Fatal("duration-limit Finish was not called")
	}
	fixture.session.events <- dictation.Event{
		Type:     dictation.EventCompleted,
		Text:     "达到时限后的最终文字",
		Definite: true,
	}
	completed := readDictationTestJSON(t, connection)
	if completed["type"] != "dictation.completed" {
		t.Fatalf("duration completion event = %#v", completed)
	}
}

func TestMappedDictationErrorsDoNotExposeProviderDetails(t *testing.T) {
	for _, testCase := range []struct {
		err  error
		code string
	}{
		{dictation.ErrProviderNotGranted, "dictation_provider_not_granted"},
		{dictation.ErrProviderAuth, "dictation_provider_auth_failed"},
		{dictation.ErrProviderBusy, "dictation_provider_busy"},
		{dictation.ErrNoSpeech, "dictation_no_speech"},
		{errors.New("secret provider detail"), "dictation_provider_failed"},
	} {
		connection, read, closeServer := dictationErrorTestSocket(t)
		writeMappedDictationProviderError(connection, testCase.err)
		payload := <-read
		closeServer()
		if payload["code"] != testCase.code {
			t.Fatalf("mapped error %v = %#v", testCase.err, payload)
		}
		raw, _ := json.Marshal(payload)
		if strings.Contains(string(raw), "secret provider detail") {
			t.Fatalf("mapped error leaked detail: %s", raw)
		}
	}
}

type dictationIntegrationFixture struct {
	app          *Server
	server       *httptest.Server
	dataStore    *store.Store
	databasePath string
	user         store.User
	admin        store.User
	provider     *fakeDictationProvider
	session      *fakeDictationSession
}

func startDictationIntegrationApp(
	t *testing.T,
	configured bool,
) dictationIntegrationFixture {
	t.Helper()
	dataDir := t.TempDir()
	databasePath := filepath.Join(dataDir, "app.db")
	dataStore, err := store.Open(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	passwordHash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := dataStore.CreateUser(
		context.Background(),
		"dictation-user",
		"Dictation User",
		passwordHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dataStore.CreateUserWithRole(
		context.Background(),
		"dictation-admin",
		"Dictation Admin",
		passwordHash,
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, _ := url.Parse("http://chat.test")
	providerURL, _ := url.Parse("http://127.0.0.1:9/v1")
	asrURL, _ := url.Parse(
		"wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async",
	)
	cfg := config.Config{
		Environment:       "test",
		BaseURL:           baseURL,
		DataDir:           dataDir,
		DatabasePath:      databasePath,
		AppSecret:         []byte("01234567890123456789012345678901"),
		SessionTTL:        time.Hour,
		SessionCookieName: "owui_session",
		Provider: config.Provider{
			Kind: "cpa", BaseURL: providerURL, APIKey: "provider-test-key",
			DefaultModel: "gpt-test", ModelsTimeout: time.Second,
			DefaultReasoningEffort:    "high",
			UnknownModelContextTokens: 128000,
			RequestBodyMaxBytes:       50 << 20,
		},
		Dictation: config.Dictation{
			MaxConcurrentGlobal:  2,
			MaxConcurrentPerUser: 1,
			MaxDuration:          2 * time.Minute,
			SessionTTL:           135 * time.Second,
			Volcengine: config.VolcengineDictation{
				Endpoint:   asrURL,
				APIKey:     "integration-asr-key",
				ResourceID: "volc.seedasr.sauc.duration",
				Format:     "pcm",
				SampleRate: 16000,
				Bits:       16,
				Channels:   1,
			},
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal:  2,
			MaxConcurrentPerUser: 1,
			MaxQueuedPerUser:     1,
			QueueTimeout:         time.Second,
		},
		Tools: config.Tools{RestaurantGuidanceEnabled: true},
	}
	modelClient := provider.NewClient(cfg.Provider, "test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(cfg, dataStore, modelClient, logger)
	fakeSession := newFakeDictationSession()
	fakeProvider := &fakeDictationProvider{
		configured: configured,
		session:    fakeSession,
		opened:     make(chan dictation.SessionConfig, 4),
	}
	app.dictationProvider = fakeProvider
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	return dictationIntegrationFixture{
		app:          app,
		server:       server,
		dataStore:    dataStore,
		databasePath: databasePath,
		user:         user,
		admin:        admin,
		provider:     fakeProvider,
		session:      fakeSession,
	}
}

type fakeDictationProvider struct {
	mu         sync.Mutex
	configured bool
	session    *fakeDictationSession
	opened     chan dictation.SessionConfig
}

func (p *fakeDictationProvider) ID() string {
	return "volcengine"
}

func (p *fakeDictationProvider) Configured() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.configured
}

func (p *fakeDictationProvider) setConfigured(value bool) {
	p.mu.Lock()
	p.configured = value
	p.mu.Unlock()
}

func (p *fakeDictationProvider) Open(
	_ context.Context,
	cfg dictation.SessionConfig,
) (dictation.Session, error) {
	p.opened <- cfg
	return p.session, nil
}

type fakeDictationSession struct {
	sentAudio     chan []byte
	finishedAudio chan []byte
	events        chan dictation.Event
	closeOnce     sync.Once
}

func newFakeDictationSession() *fakeDictationSession {
	return &fakeDictationSession{
		sentAudio:     make(chan []byte, 8),
		finishedAudio: make(chan []byte, 2),
		events:        make(chan dictation.Event, 8),
	}
}

func (s *fakeDictationSession) SendAudio(
	_ context.Context,
	audio []byte,
) error {
	s.sentAudio <- append([]byte(nil), audio...)
	return nil
}

func (s *fakeDictationSession) Finish(
	_ context.Context,
	audio []byte,
) error {
	s.finishedAudio <- append([]byte(nil), audio...)
	return nil
}

func (s *fakeDictationSession) ReadEvent(
	ctx context.Context,
) (dictation.Event, error) {
	select {
	case event := <-s.events:
		return event, nil
	case <-ctx.Done():
		return dictation.Event{}, ctx.Err()
	}
}

func (s *fakeDictationSession) Close() error {
	s.closeOnce.Do(func() {})
	return nil
}

func dialDictationTestSocket(
	baseURL string,
	cookie *http.Cookie,
) (*websocket.Conn, *http.Response, error) {
	endpoint := "ws" + baseURL[len("http"):] +
		"/api/v1/dictation/sessions"
	headers := http.Header{}
	headers.Set("Origin", "http://chat.test")
	if cookie != nil {
		headers.Set("Cookie", cookie.String())
	}
	return websocket.DefaultDialer.Dial(endpoint, headers)
}

func readDictationTestJSON(
	t *testing.T,
	connection *websocket.Conn,
) map[string]any {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	var payload map[string]any
	if err := connection.ReadJSON(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func dictationErrorTestSocket(
	t *testing.T,
) (*websocket.Conn, <-chan map[string]any, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	serverConnection := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnection <- connection
	}))
	endpoint := "ws" + server.URL[len("http"):]
	client, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	serverSide := <-serverConnection
	read := make(chan map[string]any, 1)
	go func() {
		var payload map[string]any
		_ = client.ReadJSON(&payload)
		read <- payload
	}()
	return serverSide, read, func() {
		_ = serverSide.Close()
		_ = client.Close()
		server.Close()
	}
}
