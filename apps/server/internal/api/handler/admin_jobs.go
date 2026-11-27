package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"privasync/server/internal/jobs"
)

// AdminJobsHandler exposes the registry to the admin UI: list status of all
// background jobs and trigger a manual run.
type AdminJobsHandler struct {
	reg *jobs.Registry
}

func NewAdminJobsHandler(reg *jobs.Registry) *AdminJobsHandler {
	return &AdminJobsHandler{reg: reg}
}

type jobResp struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Status       string  `json:"status"`
	Message      string  `json:"message"`
	Items        int     `json:"items"`
	StartedAt    *string `json:"started_at"`
	FinishedAt   *string `json:"finished_at"`
	Running      bool    `json:"running"`
	Schedule     string  `json:"schedule"`
	NextRunAt    *string `json:"next_run_at"`
}

// GET /api/v1/admin/jobs
func (h *AdminJobsHandler) List(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.reg.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	out := make([]jobResp, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, jobResp{
			ID:         s.Name,
			Label:      s.Label,
			Status:     s.Status,
			Message:    s.Message,
			Items:      s.Items,
			StartedAt:  fmtTimePtr(s.StartedAt),
			FinishedAt: fmtTimePtr(s.FinishedAt),
			Running:    s.Running,
			Schedule:   s.ScheduleDescription,
			NextRunAt:  fmtTimePtr(s.NextRunAt),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jobs": out})
}

// POST /api/v1/admin/jobs/{jobID}/run
func (h *AdminJobsHandler) Trigger(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "jobID")

	// Probe synchronously: pre-check that the job exists and is not already
	// running. RunOnce reports both error sentinels but we need them BEFORE
	// detaching into a goroutine, otherwise the HTTP response is already 202.
	if !h.reg.Has(id) {
		writeError(w, http.StatusNotFound, "JOB_NOT_FOUND", "unknown job")
		return
	}
	if h.reg.IsRunning(id) {
		writeError(w, http.StatusConflict, "JOB_RUNNING", "job already running")
		return
	}

	go func() {
		ctx := context.Background()
		if _, err := h.reg.RunOnce(ctx, id); err != nil && !errors.Is(err, jobs.ErrAlreadyRunning) {
			slog.Warn("admin job: run failed", "job", id, "error", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
