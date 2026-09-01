package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/network"
)

func (s *Server) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/settings/network", s.handleNetworkSettings)
}

func (s *Server) handleNetworkSettings(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "network settings are unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		state, err := s.network.State(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "settings_read_failed", "could not read network settings")
			return
		}
		writeJSON(w, http.StatusOK, networkSettingsResponse(state))
	case http.MethodPut:
		var input struct {
			Port int `json:"port"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "port must be a JSON number")
			return
		}
		state, err := s.network.SetPort(r.Context(), input.Port)
		if err != nil {
			status, code := http.StatusInternalServerError, "settings_write_failed"
			switch {
			case errors.Is(err, network.ErrInvalidPort):
				status, code = http.StatusBadRequest, "invalid_port"
			case errors.Is(err, network.ErrPortInUse):
				status, code = http.StatusConflict, "port_unavailable"
			}
			writeError(w, status, code, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, networkSettingsResponse(state))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "network settings only accept GET and PUT")
	}
}

func networkSettingsResponse(state network.State) map[string]any {
	response := map[string]any{
		"configured":       map[string]any{"port": state.ConfiguredPort, "set": state.HasConfigured},
		"restart_required": state.RestartRequired,
		"override_active":  state.OverrideActive,
	}
	if state.ActivePort > 0 {
		response["active"] = map[string]any{"host": state.Host, "port": state.ActivePort, "address": state.ActiveAddress(), "base_url": state.BaseURL()}
	} else {
		response["active"] = nil
	}
	return response
}
