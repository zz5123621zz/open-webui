package httpapi

import (
	"errors"
	"net/http"

	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
)

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.models.Models(r.Context())
	if err != nil {
		s.providerCatalogError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, catalog)
}

func (s *Server) providerCatalogError(w http.ResponseWriter, err error) {
	s.logger.Error("provider model catalog failed", "error", err)
	if errors.Is(err, provider.ErrBadResponse) {
		writeError(w, http.StatusBadGateway, "provider_invalid_response", "The model catalog returned an invalid response.")
		return
	}
	writeError(w, http.StatusBadGateway, "provider_unavailable", "The model catalog is temporarily unavailable.")
}
