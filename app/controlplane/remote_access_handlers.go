package controlplane

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/remoteaccess"
)

func (s *Server) registerRemoteAccessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/remote-access", s.handleRemoteAccess)
	mux.HandleFunc("/api/remote-access/retry", s.handleRemoteAccessRetry)
	mux.HandleFunc("/api/remote-access/identity", s.handleRemoteAccessIdentity)
}

func (s *Server) handleRemoteAccess(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeError(w, http.StatusServiceUnavailable, "remote_access_unavailable", "remote access is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.remote.Status(r.Context()))
	case http.MethodPut:
		var config remoteaccess.Config
		if err := decodeRemoteJSON(w, r, &config); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid remote-access settings")
			return
		}
		if err := s.remote.Configure(r.Context(), config); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, s.remote.Status(r.Context()))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "remote-access endpoint only accepts GET and PUT")
	}
}

func (s *Server) handleRemoteAccessRetry(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeError(w, http.StatusServiceUnavailable, "remote_access_unavailable", "remote access is unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "remote-access retry only accepts POST")
		return
	}
	if err := s.remote.Retry(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "retry_failed", "remote access could not be retried")
		return
	}
	writeJSON(w, http.StatusAccepted, s.remote.Status(r.Context()))
}

func (s *Server) handleRemoteAccessIdentity(w http.ResponseWriter, r *http.Request) {
	if s.remote == nil {
		writeError(w, http.StatusServiceUnavailable, "remote_access_unavailable", "remote access is unavailable")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Tailscale identity removal only accepts DELETE")
		return
	}
	if err := s.remote.ForgetIdentity(r.Context()); err != nil {
		if errors.Is(err, remoteaccess.ErrActive) {
			writeError(w, http.StatusConflict, "remote_access_active", "stop sharing before forgetting the Tailscale identity")
			return
		}
		writeError(w, http.StatusInternalServerError, "identity_forget_failed", "Tailscale identity could not be forgotten")
		return
	}
	writeJSON(w, http.StatusOK, s.remote.Status(r.Context()))
}

func decodeRemoteJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}
