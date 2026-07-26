package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/activecontext"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/jobs"
	"github.com/owui-personal-slim/owui-personal-slim/internal/progressivesummary"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/speech"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	cfg             config.Config
	store           *store.Store
	models          *provider.Client
	jobs            *jobs.Scheduler
	contexts        *activecontext.Manager
	summaries       *progressivesummary.Manager
	speechProviders *speech.Registry
	speechGate      *speech.Gate
	logins          *loginLimiter
	actions         *actionLimiter
	uploads         *keyedGate
	activeMu        sync.Mutex
	active          map[string]activeResponse
	responseMu      sync.Mutex
	responseContext context.Context
	responseCancel  context.CancelCauseFunc
	responseWG      sync.WaitGroup
	shuttingDown    bool
	logger          *slog.Logger
	mux             *http.ServeMux
}

type activeResponse struct {
	userID string
	cancel context.CancelCauseFunc
}

func New(cfg config.Config, dataStore *store.Store, modelClient *provider.Client, logger *slog.Logger) *Server {
	cfg.Lifecycle = cfg.Lifecycle.Normalized()
	cfg.Speech = cfg.Speech.Normalized()
	responseContext, responseCancel := context.WithCancelCause(context.Background())
	server := &Server{
		cfg: cfg, store: dataStore, models: modelClient,
		jobs: jobs.NewScheduler(
			cfg.Jobs.MaxConcurrentGlobal,
			cfg.Jobs.MaxConcurrentPerUser,
			cfg.Jobs.MaxQueuedPerUser,
			cfg.Jobs.QueueTimeout,
		),
		logger:          logger,
		mux:             http.NewServeMux(),
		logins:          newLoginLimiter(),
		actions:         newActionLimiter(),
		uploads:         newKeyedGate(),
		active:          make(map[string]activeResponse),
		responseContext: responseContext,
		responseCancel:  responseCancel,
		summaries:       progressivesummary.New(30 * time.Minute),
		speechProviders: speech.NewRegistry(
			speech.NewAliyunProvider(cfg.Speech.Alibaba),
			speech.NewVolcengineProvider(cfg.Speech.Volcengine),
		),
		speechGate: speech.NewGate(
			cfg.Speech.MaxConcurrentGlobal,
			cfg.Speech.MaxConcurrentPerUser,
		),
	}
	server.contexts = activecontext.New(dataStore, modelClient, server.providerSafetyIdentifier)
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestLog(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("HEAD /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /api/v1/config/public", s.publicConfig)

	s.mux.Handle("POST /api/v1/auth/login", s.origin(http.HandlerFunc(s.login)))
	s.mux.Handle("POST /api/v1/auth/logout", s.auth(s.origin(s.csrf(http.HandlerFunc(s.logout)))))
	s.mux.Handle("POST /api/v1/auth/logout-all", s.auth(s.origin(s.csrf(http.HandlerFunc(s.logoutAll)))))
	s.mux.Handle("GET /api/v1/me", s.auth(http.HandlerFunc(s.me)))
	s.mux.Handle("PUT /api/v1/me/password", s.auth(s.origin(s.csrf(http.HandlerFunc(s.changePassword)))))
	s.mux.Handle("GET /api/v1/me/storage", s.auth(http.HandlerFunc(s.storageStatus)))
	s.mux.Handle("GET /api/v1/me/speech", s.auth(http.HandlerFunc(s.getMySpeechPreference)))
	s.mux.Handle(
		"PUT /api/v1/me/speech",
		s.auth(s.limitAction(
			"speech_preference", 30, time.Minute,
			s.origin(s.csrf(http.HandlerFunc(s.updateMySpeechPreference))),
		)),
	)
	s.mux.Handle("GET /api/v1/models", s.auth(http.HandlerFunc(s.listModels)))
	s.mux.Handle(
		"GET /api/v1/speech/sessions",
		s.auth(s.origin(http.HandlerFunc(s.speechSession))),
	)
	s.mux.Handle(
		"GET /api/v1/admin/speech",
		s.auth(s.administrator(http.HandlerFunc(s.getSpeechServiceSetting))),
	)
	s.mux.Handle(
		"PUT /api/v1/admin/speech",
		s.auth(s.administrator(s.limitAction(
			"service_setting", 30, time.Minute,
			s.origin(s.csrf(http.HandlerFunc(s.updateSpeechServiceSetting))),
		))),
	)
	s.mux.Handle(
		"GET /api/v1/admin/progressive-summaries",
		s.auth(s.administrator(http.HandlerFunc(s.getProgressiveSummarySetting))),
	)
	s.mux.Handle(
		"PUT /api/v1/admin/progressive-summaries",
		s.auth(s.administrator(s.limitAction(
			"service_setting", 30, time.Minute,
			s.origin(s.csrf(http.HandlerFunc(s.updateProgressiveSummarySetting))),
		))),
	)
	s.mux.Handle(
		"POST /api/v1/admin/progressive-summaries/recheck",
		s.auth(s.administrator(s.limitAction(
			"service_setting", 30, time.Minute,
			s.origin(s.csrf(http.HandlerFunc(s.recheckProgressiveSummaryCompatibility))),
		))),
	)

	s.mux.Handle("GET /api/v1/conversations", s.auth(http.HandlerFunc(s.listConversations)))
	s.mux.Handle("POST /api/v1/conversations", s.auth(s.limitAction("conversation_write", 120, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.createConversation))))))
	s.mux.Handle("GET /api/v1/conversations/{id}", s.auth(http.HandlerFunc(s.getConversation)))
	s.mux.Handle("PATCH /api/v1/conversations/{id}", s.auth(s.limitAction("conversation_write", 120, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.updateConversation))))))
	s.mux.Handle("DELETE /api/v1/conversations/{id}", s.auth(s.limitAction("delete", 30, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.deleteConversation))))))
	s.mux.Handle("GET /api/v1/conversations/{id}/messages", s.auth(http.HandlerFunc(s.listMessages)))
	s.mux.Handle("POST /api/v1/conversations/{id}/responses", s.auth(s.limitAction("response", 60, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.createResponse))))))
	s.mux.Handle("POST /api/v1/messages/{id}/regenerate", s.auth(s.limitAction("response", 60, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.regenerateResponse))))))
	s.mux.Handle("GET /api/v1/responses/{id}", s.auth(http.HandlerFunc(s.getResponse)))
	s.mux.Handle("POST /api/v1/responses/{id}/cancel", s.auth(s.limitAction("cancel", 120, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.cancelResponse))))))
	s.mux.Handle("GET /api/v1/conversations/{id}/context-checkpoints", s.auth(http.HandlerFunc(s.listContextCheckpoints)))

	s.mux.Handle("POST /api/v1/attachments", s.auth(s.limitAction("upload", 30, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.uploadAttachment))))))
	s.mux.Handle("GET /api/v1/attachments/{id}/content", s.auth(http.HandlerFunc(s.attachmentContent)))
	s.mux.Handle("DELETE /api/v1/attachments/{id}", s.auth(s.limitAction("delete", 30, time.Minute, s.origin(s.csrf(http.HandlerFunc(s.deleteAttachment))))))

	s.mux.HandleFunc("/", s.serveFrontend)
}

func (s *Server) publicConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"registrationEnabled":     false,
		"provider":                s.cfg.Provider.Kind,
		"maxUploadBytes":          maxUploadBytes,
		"maxImagesPerMessage":     4,
		"maxImageBytesPerMessage": 30 * 1024 * 1024,
		"webSearchEnabled":        s.cfg.Tools.WebSearchEnabled,
		"imageGenerationEnabled":  s.cfg.Tools.ImageGenerationEnabled,
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Service is not ready.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		http.Error(w, "static assets unavailable", http.StatusInternalServerError)
		return
	}

	requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requestPath == "." || requestPath == "" {
		requestPath = "index.html"
	}
	if info, statErr := fs.Stat(sub, requestPath); statErr != nil || info.IsDir() {
		requestPath = "index.html"
	}
	if requestPath == "index.html" {
		// The HTML shell points at content-hashed immutable assets. Never retain
		// the shell itself, otherwise a long-lived browser cache can keep loading
		// an older asset graph after a deployment.
		w.Header().Set("Cache-Control", "no-store, max-age=0")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, sub, requestPath)
}

func (s *Server) csrfToken(rawSessionToken string) string {
	mac := hmac.New(sha256.New, s.cfg.AppSecret)
	_, _ = mac.Write([]byte("csrf\x00"))
	_, _ = mac.Write([]byte(rawSessionToken))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) providerSafetyIdentifier(userID string) string {
	mac := hmac.New(sha256.New, s.cfg.AppSecret)
	_, _ = mac.Write([]byte("provider-safety-identifier\x00"))
	_, _ = mac.Write([]byte(userID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
