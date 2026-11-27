package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminCreateUser_RejectsMalformedBody(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	r := reqWithClaims(http.MethodPost, "/admin/users", bytes.NewBufferString("{not json"), admin)
	w := httptest.NewRecorder()
	adminH.CreateUser(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestAdminCreateUser_RejectsMissingFields(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	r := reqWithClaims(http.MethodPost, "/admin/users", bytes.NewBufferString(`{"username":"","email":""}`), admin)
	w := httptest.NewRecorder()
	adminH.CreateUser(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrRegistrationFields)
}

func TestAdminCreateUser_RejectsDuplicateUsername(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	existing := seedUser(t, env, "dup_"+uniqueSuffix(), "dup_"+uniqueSuffix()+"@t.local")

	body := `{"username":"` + existing.Username + `","email":"fresh_` + uniqueSuffix() + `@t.local"}`
	r := reqWithClaims(http.MethodPost, "/admin/users", bytes.NewBufferString(body), admin)
	w := httptest.NewRecorder()
	adminH.CreateUser(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUsernameTaken)
}

// The admin UI prepends the created row straight into its live table, so the
// create response must carry the same shape as the listing — a missing
// counter renders as NaN and a missing is_active reads as "locked".
func TestAdminCreateUser_ReturnsFullUserShape(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	name := "fresh_" + uniqueSuffix()
	body := `{"username":"` + name + `","email":"` + name + `@t.local"}`
	r := reqWithClaims(http.MethodPost, "/admin/users", bytes.NewBufferString(body), admin)
	w := httptest.NewRecorder()
	adminH.CreateUser(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	for _, k := range []string{
		"id", "username", "email", "role", "quota_bytes", "used_bytes",
		"trash_bytes", "totp_enabled", "is_active", "is_locked", "avatar_url",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("response misses %q; body=%s", k, w.Body.String())
		}
	}
	if got["is_active"] != true {
		t.Errorf("is_active = %v, want true", got["is_active"])
	}
	if got["is_locked"] != false {
		t.Errorf("is_locked = %v, want false", got["is_locked"])
	}
	if got["used_bytes"] != float64(0) {
		t.Errorf("used_bytes = %v, want 0", got["used_bytes"])
	}
}
