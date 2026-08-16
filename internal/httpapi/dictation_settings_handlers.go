package httpapi

import (
	"errors"
	"net/http"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type updateDictationServiceSettingRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) getMyDictationSetting(
	w http.ResponseWriter,
	r *http.Request,
) {
	setting, err := s.store.DictationServiceSetting(r.Context())
	if err != nil {
		s.internalError(w, "read dictation setting for user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dictation": s.dictationServiceSettingPayload(setting, false),
	})
}

func (s *Server) getDictationServiceSetting(
	w http.ResponseWriter,
	r *http.Request,
) {
	setting, err := s.store.DictationServiceSetting(r.Context())
	if err != nil {
		s.internalError(w, "read dictation service setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dictation": s.dictationServiceSettingPayload(setting, true),
	})
}

func (s *Server) updateDictationServiceSetting(
	w http.ResponseWriter,
	r *http.Request,
) {
	var request updateDictationServiceSettingRequest
	if !readJSON(w, r, &request) {
		return
	}
	if request.Enabled && !s.dictationProvider.Configured() {
		writeError(
			w,
			http.StatusConflict,
			"dictation_provider_not_configured",
			"Configure the dictation provider credentials before enabling dictation.",
		)
		return
	}
	session, _ := sessionFromContext(r.Context())
	setting, err := s.store.SetDictationServiceSetting(
		r.Context(), session.User.ID, request.Enabled,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		s.internalError(w, "update dictation service setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dictation": s.dictationServiceSettingPayload(setting, true),
	})
}

func (s *Server) dictationServiceSettingPayload(
	setting store.DictationServiceSetting,
	administrator bool,
) map[string]any {
	payload := map[string]any{
		"enabled":            setting.Enabled,
		"configured":         s.dictationProvider.Configured(),
		"provider":           s.dictationProvider.ID(),
		"maxDurationSeconds": int(s.cfg.Dictation.MaxDuration.Seconds()),
		"audioStored":        false,
		"updatedAt":          setting.UpdatedAt,
	}
	if administrator {
		payload["resourceId"] = s.cfg.Dictation.Volcengine.ResourceID
		payload["concurrency"] = map[string]int{
			"perUser": s.cfg.Dictation.MaxConcurrentPerUser,
			"global":  s.cfg.Dictation.MaxConcurrentGlobal,
		}
	}
	return payload
}
