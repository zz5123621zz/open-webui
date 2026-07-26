package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/owui-personal-slim/owui-personal-slim/internal/speech"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type updateSpeechServiceSettingRequest struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`
	DefaultVoice string `json:"defaultVoice"`
}

type updateMySpeechPreferenceRequest struct {
	Mode  string  `json:"mode"`
	Speed float64 `json:"speed"`
	Voice string  `json:"voice"`
}

func (s *Server) getSpeechServiceSetting(w http.ResponseWriter, r *http.Request) {
	setting, err := s.store.SpeechServiceSetting(r.Context())
	if err != nil {
		s.internalError(w, "read speech service setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"speech": s.speechServiceSettingPayload(setting),
	})
}

func (s *Server) updateSpeechServiceSetting(w http.ResponseWriter, r *http.Request) {
	var request updateSpeechServiceSettingRequest
	if !readJSON(w, r, &request) {
		return
	}
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.DefaultVoice = strings.TrimSpace(request.DefaultVoice)
	provider, exists := s.speechProviders.Provider(request.Provider)
	if !exists {
		writeError(w, http.StatusBadRequest, "unknown_speech_provider", "Unknown speech provider.")
		return
	}
	if !speechVoiceAllowed(request.DefaultVoice, provider.Voices()) {
		writeError(w, http.StatusBadRequest, "invalid_speech_voice", "The selected speech voice is not available.")
		return
	}
	if request.Enabled && !provider.Configured() {
		writeError(
			w, http.StatusConflict, "speech_provider_not_configured",
			"Configure the speech provider credentials before enabling speech.",
		)
		return
	}
	session, _ := sessionFromContext(r.Context())
	setting, err := s.store.SetSpeechServiceSetting(
		r.Context(),
		session.User.ID,
		store.SpeechServiceSetting{
			Enabled: request.Enabled, Provider: request.Provider,
			DefaultVoice: request.DefaultVoice,
		},
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		s.internalError(w, "update speech service setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"speech": s.speechServiceSettingPayload(setting),
	})
}

func (s *Server) getMySpeechPreference(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	preference, err := s.store.UserSpeechPreference(r.Context(), session.User.ID)
	if err != nil {
		s.internalError(w, "read user speech preference", err)
		return
	}
	setting, err := s.store.SpeechServiceSetting(r.Context())
	if err != nil {
		s.internalError(w, "read speech service setting for user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"speech": s.userSpeechPreferencePayload(preference, setting),
	})
}

func (s *Server) updateMySpeechPreference(w http.ResponseWriter, r *http.Request) {
	var request updateMySpeechPreferenceRequest
	if !readJSON(w, r, &request) {
		return
	}
	setting, err := s.store.SpeechServiceSetting(r.Context())
	if err != nil {
		s.internalError(w, "read speech service setting for preference", err)
		return
	}
	provider, exists := s.speechProviders.Provider(setting.Provider)
	if !exists {
		s.internalError(w, "resolve speech provider", speech.ErrProviderUnavailable)
		return
	}
	request.Voice = strings.TrimSpace(request.Voice)
	if request.Voice != "" && !speechVoiceAllowed(request.Voice, provider.Voices()) {
		writeError(w, http.StatusBadRequest, "invalid_speech_voice", "The selected speech voice is not available.")
		return
	}
	if request.Mode != store.SpeechModeManual && request.Mode != store.SpeechModeAuto {
		writeError(w, http.StatusBadRequest, "invalid_speech_mode", "Speech mode must be manual or auto.")
		return
	}
	if request.Speed < 0.5 || request.Speed > 2 {
		writeError(w, http.StatusBadRequest, "invalid_speech_speed", "Speech speed must be between 0.5 and 2.0.")
		return
	}
	session, _ := sessionFromContext(r.Context())
	preference, err := s.store.SetUserSpeechPreference(
		r.Context(),
		session.User.ID,
		store.UserSpeechPreference{
			Mode: request.Mode, Speed: request.Speed, Voice: request.Voice,
		},
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		s.internalError(w, "update user speech preference", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"speech": s.userSpeechPreferencePayload(preference, setting),
	})
}

func (s *Server) speechServiceSettingPayload(setting store.SpeechServiceSetting) map[string]any {
	return map[string]any{
		"enabled": setting.Enabled, "provider": setting.Provider,
		"defaultVoice": setting.DefaultVoice, "updatedAt": setting.UpdatedAt,
		"providers": s.speechProviders.Descriptors(),
		"concurrency": map[string]int{
			"perUser": s.cfg.Speech.MaxConcurrentPerUser,
			"global":  s.cfg.Speech.MaxConcurrentGlobal,
		},
	}
}

func (s *Server) userSpeechPreferencePayload(
	preference store.UserSpeechPreference,
	setting store.SpeechServiceSetting,
) map[string]any {
	provider, _ := s.speechProviders.Provider(setting.Provider)
	voices := []speech.Voice{}
	configured := false
	if provider != nil {
		voices = provider.Voices()
		configured = provider.Configured()
	}
	voice := effectiveSpeechVoice(preference.Voice, setting.DefaultVoice, voices)
	return map[string]any{
		"mode": preference.Mode, "autoRead": preference.Mode == store.SpeechModeAuto,
		"speed": preference.Speed, "voice": preference.Voice,
		"effectiveVoice": voice, "updatedAt": preference.UpdatedAt,
		"serviceEnabled": setting.Enabled, "provider": setting.Provider,
		"providerConfigured": configured, "voices": voices,
		"audioAuthorization": "required_on_each_device",
	}
}

func speechVoiceAllowed(id string, voices []speech.Voice) bool {
	for _, voice := range voices {
		if voice.ID == id {
			return true
		}
	}
	return false
}
