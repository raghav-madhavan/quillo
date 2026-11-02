package addons

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// Handler serves the admin addon API under /api/quillo/admin. The router
// registers each method behind Silo's login and acting-admin checks, so every
// request here is from an administrator. It manages the server-wide addon set.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// addonView is the wire form of an addon.
type addonView struct {
	ID            string          `json:"id"`
	ManifestURL   string          `json:"manifest_url"`
	ManifestHost  string          `json:"manifest_host"`
	AddonID       string          `json:"addon_id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Description   string          `json:"description"`
	Logo          string          `json:"logo,omitempty"`
	Enabled       bool            `json:"enabled"`
	Position      int             `json:"position"`
	Summary       ManifestSummary `json:"summary"`
	LastCheckedAt time.Time       `json:"last_checked_at"`
	LastError     *string         `json:"last_error"`
}

func viewOf(a *Addon) addonView {
	host := ""
	if u, err := url.Parse(a.ManifestURL); err == nil {
		host = u.Host
	}
	return addonView{
		ID: strconv.FormatInt(a.ID, 10), ManifestURL: a.ManifestURL, ManifestHost: host,
		AddonID: a.AddonID, Name: a.Name, Version: a.Manifest.Version,
		Description: a.Manifest.Description, Logo: a.Manifest.Logo,
		Enabled: a.Enabled, Position: a.Position, Summary: a.Manifest.Summary(),
		LastCheckedAt: a.LastCheckedAt, LastError: a.LastError,
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	addons, err := h.svc.store.List(r.Context(), nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]addonView, 0, len(addons))
	for _, a := range addons {
		out = append(out, viewOf(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

type urlBody struct {
	URL string `json:"url"`
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	var body urlBody
	if !readJSON(w, r, &body) {
		return
	}
	u, m, err := h.svc.Preview(r.Context(), body.URL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manifest_url": u, "addon_id": m.ID, "name": m.Name, "version": m.Version,
		"description": m.Description, "logo": m.Logo, "summary": m.Summary(),
	})
}

func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
	var body urlBody
	if !readJSON(w, r, &body) {
		return
	}
	a, err := h.svc.Install(r.Context(), nil, body.URL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewOf(a))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, r, userErrorf("Nothing to change."))
		return
	}
	a, err := h.svc.store.SetEnabled(r.Context(), id, nil, *body.Enabled)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(a))
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.Refresh(r.Context(), id, nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(a))
}

func (h *Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ids := make([]int64, 0, len(body.IDs))
	for _, s := range body.IDs {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeError(w, r, userErrorf("The addon list changed while you were reordering. Reload the page and try again."))
			return
		}
		ids = append(ids, id)
	}
	if err := h.svc.store.Reorder(r.Context(), nil, ids); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.store.Delete(r.Context(), id, nil); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Presets(w http.ResponseWriter, r *http.Request) {
	installed, err := h.svc.store.List(r.Context(), nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	type presetView struct {
		Preset
		Installed bool `json:"installed"`
	}
	out := make([]presetView, 0, len(Presets))
	for _, p := range Presets {
		v := presetView{Preset: p}
		for _, a := range installed {
			if a.ManifestURL == p.ManifestURL {
				v.Installed = true
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, problem{Error: "not_found", Message: "That addon isn't installed."})
		return 0, false
	}
	return id, true
}

const maxBodyBytes = 64 << 10

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, problem{Error: "bad_request", Message: "The request couldn't be read."})
		return false
	}
	return true
}

// problem matches Silo's error envelope: a stable code and a sentence.
type problem struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ue *UserError
	switch {
	case errors.As(err, &ue):
		writeJSON(w, http.StatusUnprocessableEntity, problem{Error: "addon_rejected", Message: ue.Message})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, problem{Error: "not_found", Message: "That addon isn't installed."})
	default:
		slog.ErrorContext(r.Context(), "quillo addons request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, problem{Error: "internal_error", Message: "Something went wrong on the server. Try again."})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
