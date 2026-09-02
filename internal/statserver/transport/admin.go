package transport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
	"github.com/neverknowerdev/paylessforai/internal/statserver/services"
	"github.com/neverknowerdev/paylessforai/internal/statserver/views"
)

type AdminHandler struct {
	catalog  *services.CatalogService
	profiles *services.ProfileService
	auth     *services.AuthService
	views    *views.Renderer
}

func NewAdmin(catalog *services.CatalogService, profiles *services.ProfileService, auth *services.AuthService, renderer *views.Renderer) *AdminHandler {
	return &AdminHandler{catalog: catalog, profiles: profiles, auth: auth, views: renderer}
}

func (h *AdminHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", h.page)
	mux.HandleFunc("POST /admin/api/v1/session", h.session)
	mux.Handle("GET /admin/api/v1/capability-profiles", h.requireAdmin(http.HandlerFunc(h.profilesList)))
	mux.Handle("POST /admin/api/v1/capability-profiles/create", h.requireAdmin(http.HandlerFunc(h.profileCreate)))
	mux.Handle("POST /admin/api/v1/manual-signals", h.requireAdmin(http.HandlerFunc(h.signalCreate)))
	mux.Handle("GET /admin/api/v1/sources", h.requireAdmin(http.HandlerFunc(h.sources)))
	mux.Handle("GET /admin/api/v1/pricing", h.requireAdmin(http.HandlerFunc(h.pricingList)))
	mux.Handle("PUT /admin/api/v1/pricing/", h.requireAdmin(http.HandlerFunc(h.pricingUpdate)))
	mux.Handle("POST /admin/api/v1/profile-versions/", h.requireAdmin(http.HandlerFunc(h.profileVersion)))
	return Chain(mux, Recovery, SecurityHeaders, RequestLog)
}

func (h *AdminHandler) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !h.isAdmin(r) {
		_ = h.views.Render(w, "admin_login.html", nil)
		return
	}
	if err := h.views.Render(w, "admin.html", nil); err != nil {
		Error(w, 500, "render page")
	}
}
func (h *AdminHandler) session(w http.ResponseWriter, r *http.Request) {
	token, ok, err := h.auth.SignIn(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		Error(w, 500, "create session failed")
		return
	}
	if !ok {
		Error(w, 401, "invalid credentials")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "stat_admin", Value: token, HttpOnly: true, Secure: false, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 8 * 3600})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (h *AdminHandler) profilesList(w http.ResponseWriter, r *http.Request) {
	items, err := h.profiles.Admin(r.Context())
	if err != nil {
		Error(w, 500, "list profiles failed")
		return
	}
	JSON(w, 200, map[string]any{"data": items})
}
func (h *AdminHandler) profileCreate(w http.ResponseWriter, r *http.Request) {
	var input models.CreateProfile
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, 400, "invalid profile")
		return
	}
	profileID, versionID, err := h.profiles.Create(r.Context(), input)
	if err != nil {
		Error(w, 400, err.Error())
		return
	}
	JSON(w, 201, map[string]any{"profile_id": profileID, "version_id": versionID, "state": "draft"})
}
func (h *AdminHandler) signalCreate(w http.ResponseWriter, r *http.Request) {
	var input models.CreateSignal
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, 400, "invalid score signal")
		return
	}
	if err := h.profiles.CreateSignal(r.Context(), input); err != nil {
		Error(w, 400, err.Error())
		return
	}
	JSON(w, 201, map[string]bool{"created": true})
}
func (h *AdminHandler) sources(w http.ResponseWriter, r *http.Request) {
	items, err := h.catalog.Sources(r.Context())
	if err != nil {
		Error(w, 500, "list sources failed")
		return
	}
	JSON(w, 200, map[string]any{"data": items})
}
func (h *AdminHandler) pricingList(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	offset := parseOffset(r.URL.Query().Get("offset"))
	items, total, err := h.catalog.Pricing(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		Error(w, 500, "list pricing failed")
		return
	}
	JSON(w, 200, map[string]any{"data": items, "total": total, "limit": limit, "offset": offset})
}
func (h *AdminHandler) pricingUpdate(w http.ResponseWriter, r *http.Request) {
	offeringID, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/v1/pricing/"), "/"), 10, 64)
	if err != nil || offeringID < 1 {
		Error(w, 400, "invalid offering id")
		return
	}
	var input models.PriceOverride
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		Error(w, 400, "invalid price override")
		return
	}
	cookie, err := r.Cookie("stat_admin")
	if err != nil {
		Error(w, 403, "forbidden")
		return
	}
	userID, ok := h.auth.UserID(r.Context(), cookie.Value)
	if !ok {
		Error(w, 403, "forbidden")
		return
	}
	if err := h.catalog.OverridePricing(r.Context(), offeringID, userID, input); err != nil {
		Error(w, 400, err.Error())
		return
	}
	JSON(w, 200, map[string]any{"updated": true, "offering_id": offeringID})
}
func (h *AdminHandler) profileVersion(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/v1/profile-versions/"), "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	versionID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		Error(w, 400, "invalid profile version")
		return
	}
	switch parts[1] {
	case "components":
		var input models.CreateComponent
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			Error(w, 400, "invalid component")
			return
		}
		if err := h.profiles.AddComponent(r.Context(), versionID, input); err != nil {
			Error(w, 400, err.Error())
			return
		}
		JSON(w, 201, map[string]bool{"created": true})
	case "publish":
		if err := h.profiles.Publish(r.Context(), versionID); err != nil {
			Error(w, 400, err.Error())
			return
		}
		JSON(w, 200, map[string]any{"published": true, "version_id": versionID})
	default:
		http.NotFound(w, r)
	}
}
func (h *AdminHandler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.isAdmin(r) {
			Error(w, 403, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h *AdminHandler) isAdmin(r *http.Request) bool {
	cookie, err := r.Cookie("stat_admin")
	return err == nil && h.auth.IsAdmin(r.Context(), cookie.Value)
}
