package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/routing"
)

func (s *Server) registerGroupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/groups", s.handleGroups)
	mux.HandleFunc("/api/groups/", s.handleGroupPath)
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.db.ListGroups(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "group_list_failed", "could not list groups")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		s.saveGroup(w, r, groups.Definition{}, nil)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "groups endpoint only accepts GET and POST")
	}
}

func (s *Server) handleGroupPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/groups/")
	if path == "preview" && r.Method == http.MethodPost {
		s.previewGroup(w, r, nil)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "group ID is required")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "preview" && r.Method == http.MethodPost {
		s.previewGroup(w, r, &id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.db.GetGroup(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group_not_found", "group was not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "group_get_failed", "could not load group")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": item})
	case http.MethodPut:
		revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "group_revision_required", "revision query parameter is required")
			return
		}
		s.saveGroup(w, r, groups.Definition{ID: id}, &revision)
	case http.MethodDelete:
		revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "group_revision_required", "revision query parameter is required")
			return
		}
		if err := s.db.DeleteGroup(r.Context(), id, revision); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				writeError(w, http.StatusConflict, "group_in_use", "group is referenced by another group")
			} else if strings.Contains(err.Error(), "group_revision_conflict") {
				writeError(w, http.StatusConflict, "group_revision_conflict", "group was changed; reload before deleting")
			} else {
				writeError(w, http.StatusInternalServerError, "group_delete_failed", "could not delete group")
			}
			return
		}
		if s.groups != nil {
			_ = s.groups.Reload(r.Context())
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported group method")
	}
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request, base groups.Definition, expected *int64) {
	var input groups.Definition
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_group_json", "group definition must be valid JSON")
		return
	}
	input.ID = base.ID
	input.Slug = groups.NormalizeSlug(input.Slug)
	items, err := s.db.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "group_list_failed", "could not validate groups")
		return
	}
	all := map[string]groups.Definition{}
	for _, item := range items {
		all[item.ID] = item
	}
	if input.ID == "" {
		input.Revision = 1
		input.ID = ""
	}
	all[input.ID] = input
	issues := groups.ValidateDefinition(input, bySlug(items))
	if s.catalog != nil {
		for _, model := range s.catalog.Snapshot().Models {
			if strings.EqualFold(model.ID, input.Slug) {
				issues = append(issues, groups.ValidationIssue{Path: "slug", Code: "group_model_slug_conflict", Message: "slug is already a discovered model ID", Level: "error"})
			}
		}
	}
	compiled := groups.Compile(input, all, groups.DefaultCompileLimits())
	issues = append(issues, compiled.Issues...)
	if hasGroupErrors(issues) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "invalid_group", "message": "group definition is invalid", "issues": issues}})
		return
	}
	saved, err := s.db.SaveGroup(r.Context(), input, expected)
	if err != nil {
		if strings.Contains(err.Error(), "revision_conflict") {
			writeError(w, http.StatusConflict, "group_revision_conflict", "group was changed; reload before saving")
		} else if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			writeError(w, http.StatusConflict, "group_slug_conflict", "another group already uses this slug")
		} else {
			writeError(w, http.StatusInternalServerError, "group_save_failed", "could not save group")
		}
		return
	}
	if s.groups != nil {
		if err := s.groups.Reload(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "group_reload_failed", "group was saved but could not be reloaded")
			return
		}
	}
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"data": saved, "issues": issues})
}

func (s *Server) previewGroup(w http.ResponseWriter, r *http.Request, id *string) {
	var definition groups.Definition
	if id != nil {
		loaded, err := s.db.GetGroup(r.Context(), *id)
		if err != nil {
			writeError(w, http.StatusNotFound, "group_not_found", "group was not found")
			return
		}
		definition = loaded
	} else if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&definition); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_group_json", "group definition must be valid JSON")
		return
	}
	items, err := s.db.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "group_list_failed", "could not load groups")
		return
	}
	all := map[string]groups.Definition{}
	for _, item := range items {
		all[item.ID] = item
	}
	all[definition.ID] = definition
	issues := groups.ValidateDefinition(definition, bySlug(items))
	compiled := groups.Compile(definition, all, groups.DefaultCompileLimits())
	issues = append(issues, compiled.Issues...)
	preview := map[string]any{"definition": definition, "issues": issues, "stages": compiled.Stages}
	if s.catalog != nil && !hasGroupErrors(issues) {
		protocol := matcher.ProtocolChatCompletions
		if raw := r.URL.Query().Get("protocol"); raw != "" {
			protocol = matcher.Protocol(raw)
		}
		inputTokens, _ := strconv.ParseInt(r.URL.Query().Get("input_tokens"), 10, 64)
		outputTokens, _ := strconv.ParseInt(r.URL.Query().Get("output_tokens"), 10, 64)
		if inputTokens == 0 {
			inputTokens = 1000
		}
		if outputTokens == 0 {
			outputTokens = 1000
		}
		plan := routing.BuildGroup(matcher.MatchRequest{Protocol: protocol, LogicalModel: definition.Slug, InputTokens: inputTokens, ExpectedOutput: outputTokens}, definition, all, s.catalog.Snapshot().Routes, time.Now().UTC(), routing.DefaultLimits())
		preview["plan"] = plan
	}
	writeJSON(w, http.StatusOK, preview)
}

func bySlug(items []groups.Definition) map[string]groups.Definition {
	result := map[string]groups.Definition{}
	for _, item := range items {
		result[groups.NormalizeSlug(item.Slug)] = item
	}
	return result
}
func hasGroupErrors(issues []groups.ValidationIssue) bool {
	for _, item := range issues {
		if item.Level == "error" {
			return true
		}
	}
	return false
}
