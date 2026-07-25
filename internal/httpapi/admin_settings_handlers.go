package httpapi

import (
	"errors"
	"net/http"

	"github.com/owui-personal-slim/owui-personal-slim/internal/progressivesummary"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type progressiveSummarySettingResponse struct {
	Mode           string                           `json:"mode"`
	HardDisabled   bool                             `json:"hardDisabled"`
	EffectiveState string                           `json:"effectiveState"`
	Models         []progressivesummary.ModelStatus `json:"models"`
	UpdatedAt      int64                            `json:"updatedAt"`
}

type updateProgressiveSummarySettingRequest struct {
	Mode string `json:"mode"`
}

func (s *Server) getProgressiveSummarySetting(w http.ResponseWriter, r *http.Request) {
	setting, err := s.store.ProgressiveSummarySetting(r.Context())
	if err != nil {
		s.internalError(w, "read progressive summary setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"progressiveSummary": s.progressiveSummarySettingPayload(setting),
	})
}

func (s *Server) updateProgressiveSummarySetting(w http.ResponseWriter, r *http.Request) {
	var request updateProgressiveSummarySettingRequest
	if !readJSON(w, r, &request) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	setting, err := s.store.SetProgressiveSummaryMode(
		r.Context(), session.User.ID, request.Mode,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		if request.Mode != store.ProgressiveSummaryModeAuto &&
			request.Mode != store.ProgressiveSummaryModeOff {
			writeError(
				w, http.StatusBadRequest, "invalid_summary_mode",
				"Progressive summary mode must be auto or off.",
			)
			return
		}
		s.internalError(w, "update progressive summary setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"progressiveSummary": s.progressiveSummarySettingPayload(setting),
	})
}

func (s *Server) recheckProgressiveSummaryCompatibility(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RecordProgressiveSummaryRecheck(
		r.Context(), session.User.ID,
	); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		s.internalError(w, "audit progressive summary recheck", err)
		return
	}
	s.summaries.Reset()
	setting, err := s.store.ProgressiveSummarySetting(r.Context())
	if err != nil {
		s.internalError(w, "read progressive summary setting after recheck", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"progressiveSummary": s.progressiveSummarySettingPayload(setting),
	})
}

func (s *Server) progressiveSummarySettingPayload(
	setting store.ServiceSetting,
) progressiveSummarySettingResponse {
	statuses := s.summaries.Snapshot(s.cfg.Provider.BaseURL.String())
	effective := aggregateProgressiveSummaryState(statuses)
	if s.cfg.Provider.ProgressiveSummaryHardDisabled ||
		setting.Value == store.ProgressiveSummaryModeOff {
		effective = string(progressivesummary.StateDisabled)
	}
	return progressiveSummarySettingResponse{
		Mode: setting.Value, HardDisabled: s.cfg.Provider.ProgressiveSummaryHardDisabled,
		EffectiveState: effective, Models: statuses, UpdatedAt: setting.UpdatedAt,
	}
}

func aggregateProgressiveSummaryState(
	statuses []progressivesummary.ModelStatus,
) string {
	if len(statuses) == 0 {
		return string(progressivesummary.StateUnknown)
	}
	counts := make(map[progressivesummary.State]int)
	for _, status := range statuses {
		counts[status.State]++
	}
	if counts[progressivesummary.StateProbing] > 0 {
		return string(progressivesummary.StateProbing)
	}
	if len(counts) == 1 {
		return string(statuses[0].State)
	}
	return "mixed"
}
