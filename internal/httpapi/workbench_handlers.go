package httpapi

import (
	"net/http"
	"strings"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) getMyWorkbench(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	setting, err := s.store.WorkbenchSetting(r.Context(), session.User.ID)
	if err != nil {
		s.internalError(w, "read workbench preference", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workbench":       setting,
		"guidanceEnabled": s.cfg.Tools.RestaurantGuidanceEnabled,
	})
}

type updateWorkbenchRequest struct {
	Workbench string `json:"workbench"`
}

func (s *Server) updateMyWorkbench(w http.ResponseWriter, r *http.Request) {
	var request updateWorkbenchRequest
	if !readJSON(w, r, &request) {
		return
	}
	request.Workbench = strings.ToLower(strings.TrimSpace(request.Workbench))
	session, _ := sessionFromContext(r.Context())
	current, err := s.store.WorkbenchSetting(r.Context(), session.User.ID)
	if err != nil {
		s.internalError(w, "read workbench preference", err)
		return
	}
	if request.Workbench == "restaurant" &&
		current.Initial != "restaurant" {
		writeError(
			w, http.StatusForbidden, "workbench_not_assigned",
			"The restaurant workbench has not been assigned to this account.",
		)
		return
	}
	setting, err := s.store.SetWorkbenchPreference(
		r.Context(), session.User.ID, request.Workbench,
	)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "not_found", "User not found.")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_workbench", "Workbench is invalid.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workbench":       setting,
		"guidanceEnabled": s.cfg.Tools.RestaurantGuidanceEnabled,
	})
}

func (s *Server) getMyRestaurantProfile(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	facts, err := s.store.RestaurantProfile(r.Context(), session.User.ID)
	if err != nil {
		s.internalError(w, "read restaurant profile", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"facts": facts})
}
