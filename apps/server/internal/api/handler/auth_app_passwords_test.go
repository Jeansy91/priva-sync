package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"privasync/server/internal/audit"
	"privasync/server/internal/config"
	"privasync/server/internal/repository"
)

func newAppPasswordHandler(t *testing.T) (*AuthHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	h := NewAuthHandler(env.users, audit.NewRepository(env.pool), env.storage, nil,
		config.AuthConfig{JWTSecret: "test-secret", AccessTokenTTL: 5 * 60 * 1e9},
		"http://localhost", "PrivaSync").
		WithAppPasswords(repository.NewAppPasswordRepository(env.pool))
	return h, env
}

// The plaintext travels to the client exactly once, at creation. Anything
// else would mean the server could hand it out again later, which is the
// property that lets the DB store only a hash.
func TestCreateAppPassword_ReturnsSecretOnceAndNeverAgain(t *testing.T) {
	h, env := newAppPasswordHandler(t)
	claims := createTestUser(t, env)

	w := httptest.NewRecorder()
	h.CreateAppPassword(w, reqWithClaims(http.MethodPost, "/auth/app-passwords",
		strings.NewReader(`{"name":"Laptop"}`), claims))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var created struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Secret == "" {
		t.Fatal("create response carried no secret")
	}
	if created.Name != "Laptop" {
		t.Errorf("name = %q, want Laptop", created.Name)
	}

	wl := httptest.NewRecorder()
	h.ListAppPasswords(wl, reqWithClaims(http.MethodGet, "/auth/app-passwords", nil, claims))
	if wl.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", wl.Code)
	}
	if strings.Contains(wl.Body.String(), created.Secret) {
		t.Fatal("the listing handed out the secret again")
	}
}

func TestCreateAppPassword_RejectsEmptyName(t *testing.T) {
	h, env := newAppPasswordHandler(t)
	claims := createTestUser(t, env)

	for _, body := range []string{`{"name":""}`, `{"name":"   "}`, `{}`} {
		w := httptest.NewRecorder()
		h.CreateAppPassword(w, reqWithClaims(http.MethodPost, "/auth/app-passwords",
			strings.NewReader(body), claims))
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, w.Code)
		}
	}
}

// A name is a label the owner reads later; it must not be able to carry
// enough text to bloat the row or the UI.
func TestCreateAppPassword_RejectsOverlongName(t *testing.T) {
	h, env := newAppPasswordHandler(t)
	claims := createTestUser(t, env)

	w := httptest.NewRecorder()
	h.CreateAppPassword(w, reqWithClaims(http.MethodPost, "/auth/app-passwords",
		strings.NewReader(`{"name":"`+strings.Repeat("a", 200)+`"}`), claims))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDeleteAppPassword_ScopedToCaller(t *testing.T) {
	h, env := newAppPasswordHandler(t)
	owner := createTestUser(t, env)
	stranger := createTestUser(t, env)

	w := httptest.NewRecorder()
	h.CreateAppPassword(w, reqWithClaims(http.MethodPost, "/auth/app-passwords",
		strings.NewReader(`{"name":"Laptop"}`), owner))
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A stranger holding the id must not be able to revoke it.
	ws := httptest.NewRecorder()
	rs := reqWithClaims(http.MethodDelete, "/auth/app-passwords/"+created.ID, nil, stranger)
	h.DeleteAppPassword(ws, withURLParams(rs, "appPasswordID", created.ID))
	if ws.Code != http.StatusNotFound {
		t.Fatalf("stranger delete status = %d, want 404", ws.Code)
	}

	wo := httptest.NewRecorder()
	ro := reqWithClaims(http.MethodDelete, "/auth/app-passwords/"+created.ID, nil, owner)
	h.DeleteAppPassword(wo, withURLParams(ro, "appPasswordID", created.ID))
	if wo.Code != http.StatusNoContent {
		t.Fatalf("owner delete status = %d, want 204; body=%s", wo.Code, wo.Body.String())
	}
}

func TestListAppPasswords_OnlyOwn(t *testing.T) {
	h, env := newAppPasswordHandler(t)
	alice := createTestUser(t, env)
	bob := createTestUser(t, env)

	w := httptest.NewRecorder()
	h.CreateAppPassword(w, reqWithClaims(http.MethodPost, "/auth/app-passwords",
		strings.NewReader(`{"name":"Alice-Laptop"}`), alice))

	wb := httptest.NewRecorder()
	h.ListAppPasswords(wb, reqWithClaims(http.MethodGet, "/auth/app-passwords", nil, bob))
	if wb.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", wb.Code)
	}
	if strings.Contains(wb.Body.String(), "Alice-Laptop") {
		t.Fatal("the listing leaked another user's app password")
	}
}
