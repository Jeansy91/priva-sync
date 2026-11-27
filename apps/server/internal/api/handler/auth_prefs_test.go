package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/auth"
)

// putPref issues a preference PUT carrying the given claims and body.
func putPref(h func(http.ResponseWriter, *http.Request), claims *auth.Claims, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPut, "/auth/pref", bytes.NewBufferString(body), claims)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestSetLanguage_RejectsMalformedBody(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetLanguage, claims, "{not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestSetLanguage_RejectsUnsupportedLanguage(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetLanguage, claims, `{"language":"fr"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUnsupportedOp)
}

func TestSetLanguage_Succeeds(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetLanguage, claims, `{"language":"de"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if string(u.PreferredLanguage) != "de" {
		t.Errorf("preferred language = %q, want de", u.PreferredLanguage)
	}
}

func TestSetShowFileTree_RejectsMalformedBody(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetShowFileTree, claims, "{bad")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestSetShowFileTree_Succeeds(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetShowFileTree, claims, `{"show":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestSetShowFileCheckboxes_RejectsMalformedBody(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetShowFileCheckboxes, claims, "{bad")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestSetShowFileCheckboxes_Succeeds(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetShowFileCheckboxes, claims, `{"show":false}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestSetTrashView_RejectsMalformedBody(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetTrashView, claims, "{bad")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestSetTrashView_RejectsUnsupportedView(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetTrashView, claims, `{"view":"grid"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUnsupportedOp)
}

func TestSetTrashView_Succeeds(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	w := putPref(h.SetTrashView, claims, `{"view":"tree"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}
