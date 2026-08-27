package transport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
	"github.com/neverknowerdev/paylessforai/internal/statserver/services"
	"github.com/neverknowerdev/paylessforai/internal/statserver/views"
)

type PublicHandler struct {
	catalog   *services.CatalogService
	telemetry *services.TelemetryService
	profiles  *services.ProfileService
	views     *views.Renderer
}

func NewPublic(catalog *services.CatalogService, telemetry *services.TelemetryService, profiles *services.ProfileService, renderer *views.Renderer) *PublicHandler {
	return &PublicHandler{catalog: catalog, telemetry: telemetry, profiles: profiles, views: renderer}
}

func (h *PublicHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", h.ready)
	mux.HandleFunc("GET /v1/models/search", h.search)
	mux.HandleFunc("GET /v1/models/resolve", h.resolve)
	mux.HandleFunc("GET /v1/models/", h.modelDetail)
	mux.HandleFunc("GET /v1/models", h.models)
	mux.HandleFunc("GET /v1/sources/status", h.sources)
	mux.HandleFunc("GET /v1/statistics", h.statistics)
	mux.HandleFunc("GET /v1/capability-profiles", h.capabilityProfiles)
	mux.HandleFunc("POST /v1/telemetry", h.telemetryIngest)
	mux.HandleFunc("GET /", h.index)
	return Chain(mux, Recovery, SecurityHeaders, RequestLog)
}

func (h *PublicHandler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := h.views.Render(w, "public.html", nil); err != nil {
		Error(w, 500, "render page")
	}
}
func (h *PublicHandler) ready(w http.ResponseWriter, r *http.Request) {
	ready, err := h.catalog.Ready(r.Context())
	if err != nil {
		Error(w, 500, "readiness check failed")
		return
	}
	if !ready {
		Error(w, 503, "catalog unavailable")
		return
	}
	_, _ = w.Write([]byte("ready"))
}
func (h *PublicHandler) models(w http.ResponseWriter, r *http.Request) {
	items, total, err := h.catalog.List(r.Context(), parseLimit(r.URL.Query().Get("limit")))
	if err != nil {
		Error(w, 500, "list models failed")
		return
	}
	JSON(w, 200, map[string]any{"object": "list", "data": items, "total": total})
}
func (h *PublicHandler) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if strings.TrimSpace(query) == "" {
		h.models(w, r)
		return
	}
	results, err := h.catalog.Search(r.Context(), query)
	if err != nil {
		Error(w, 500, "search models failed")
		return
	}
	JSON(w, 200, map[string]any{"results": results, "total": len(results)})
}
func (h *PublicHandler) resolve(w http.ResponseWriter, r *http.Request) {
	result, err := h.catalog.Resolve(r.Context(), r.URL.Query().Get("name"))
	if errors.Is(err, sql.ErrNoRows) {
		Error(w, 404, "model not found")
		return
	}
	if err != nil {
		Error(w, 500, "resolve model failed")
		return
	}
	JSON(w, 200, map[string]any{"id": result.ID, "canonical_slug": result.CanonicalSlug, "display_name": result.DisplayName, "creator": result.Creator, "resolved_from": r.URL.Query().Get("name")})
}
func (h *PublicHandler) sources(w http.ResponseWriter, r *http.Request) {
	items, err := h.catalog.Sources(r.Context())
	if err != nil {
		Error(w, 500, "list sources failed")
		return
	}
	JSON(w, 200, map[string]any{"data": items})
}
func (h *PublicHandler) statistics(w http.ResponseWriter, r *http.Request) {
	stats, err := h.telemetry.Statistics(r.Context(), r.URL.Query().Get("model"), r.URL.Query().Get("provider"))
	if err != nil {
		Error(w, 500, "calculate statistics failed")
		return
	}
	JSON(w, 200, stats)
}
func (h *PublicHandler) capabilityProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.profiles.Public(r.Context())
	if err != nil {
		Error(w, 500, "list profiles failed")
		return
	}
	JSON(w, 200, map[string]any{"data": profiles})
}
func (h *PublicHandler) telemetryIngest(w http.ResponseWriter, r *http.Request) {
	batch, err := decodeTelemetry(r)
	if err != nil {
		Error(w, 400, "invalid JSON")
		return
	}
	credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	accepted, duplicate, err := h.telemetry.Ingest(r.Context(), credential, batch)
	if errors.Is(err, services.ErrInvalidCredential) {
		Error(w, 401, err.Error())
		return
	}
	if errors.Is(err, services.ErrInvalidBatch) {
		Error(w, 400, err.Error())
		return
	}
	if err != nil {
		Error(w, 500, "persist telemetry failed")
		return
	}
	JSON(w, 200, map[string]any{"accepted": accepted, "duplicate": duplicate, "batch_id": batch.BatchID})
}
func (h *PublicHandler) modelDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/v1/models/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "statistics" {
		h.modelStatistics(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "scores" {
		h.modelScores(w, r, parts[0])
		return
	}
	detail, err := h.catalog.Detail(r.Context(), path)
	if errors.Is(err, sql.ErrNoRows) {
		Error(w, 404, "model not found")
		return
	}
	if err != nil {
		Error(w, 500, "get model failed")
		return
	}
	JSON(w, 200, detail)
}
func (h *PublicHandler) modelStatistics(w http.ResponseWriter, r *http.Request, slug string) {
	stats, err := h.telemetry.Statistics(r.Context(), slug, r.URL.Query().Get("provider"))
	if err != nil {
		Error(w, 500, "calculate statistics failed")
		return
	}
	JSON(w, 200, stats)
}
func (h *PublicHandler) modelScores(w http.ResponseWriter, r *http.Request, slug string) {
	scores, err := h.profiles.Scores(r.Context(), slug)
	if errors.Is(err, sql.ErrNoRows) {
		Error(w, 404, "model not found")
		return
	}
	if err != nil {
		Error(w, 500, "get model scores failed")
		return
	}
	JSON(w, 200, map[string]any{"data": scores})
}
func parseLimit(value string) int {
	var limit int
	_, _ = fmt.Sscanf(value, "%d", &limit)
	if limit < 1 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}
func decodeTelemetry(r *http.Request) (models.TelemetryBatch, error) {
	var batch models.TelemetryBatch
	err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&batch)
	return batch, err
}
