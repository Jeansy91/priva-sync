package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
)

// GET /api/v1/admin/audit-log?user=&event=&from=&to=&limit=&offset=
//
// Lists audit events, newest first. All filters optional. `from`/`to` are
// RFC3339; `limit` defaults to 100 and caps at 500. Parse errors -> 400.
func (h *AdminHandler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := audit.ListFilter{}

	if s := q.Get("user"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
			return
		}
		f.UserID = &id
	}
	if s := q.Get("event"); s != "" {
		f.EventType = s
	}
	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid from (RFC3339 required)")
			return
		}
		f.From = &t
	}
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid to (RFC3339 required)")
			return
		}
		f.To = &t
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid limit")
			return
		}
		if n > 500 {
			n = 500
		}
		f.Limit = n
	}
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid offset")
			return
		}
		f.Offset = n
	}

	events, err := h.audits.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
	})
}
