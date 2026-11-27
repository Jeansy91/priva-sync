package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestSetLanguage_UpdatesPreference(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"language":"en"}`)
	r := reqWithClaims("PUT", "/auth/language", body, claims)
	w := httptest.NewRecorder()
	h.SetLanguage(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if u.PreferredLanguage != "en" {
		t.Errorf("PreferredLanguage = %q, want en", u.PreferredLanguage)
	}
}

func TestSetLanguage_RejectsUnsupported(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"language":"fr"}`)
	r := reqWithClaims("PUT", "/auth/language", body, claims)
	w := httptest.NewRecorder()
	h.SetLanguage(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrUnsupportedOp {
		t.Errorf("code = %v, want %q", resp["code"], ErrUnsupportedOp)
	}
}

func TestSetShowFileTree_TogglesPreference(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"show":true}`)
	r := reqWithClaims("PUT", "/auth/preferences/file-tree", body, claims)
	w := httptest.NewRecorder()
	h.SetShowFileTree(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if !u.ShowFileTree {
		t.Error("ShowFileTree = false after SetShowFileTree(true)")
	}
}

func TestSetShowFileCheckboxes_TogglesPreference(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"show":true}`)
	r := reqWithClaims("PUT", "/auth/preferences/file-checkboxes", body, claims)
	w := httptest.NewRecorder()
	h.SetShowFileCheckboxes(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if !u.ShowFileCheckboxes {
		t.Error("ShowFileCheckboxes = false after SetShowFileCheckboxes(true)")
	}
}

func TestSetTrashView_UpdatesPreference(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"view":"tree"}`)
	r := reqWithClaims("PUT", "/auth/preferences/trash-view", body, claims)
	w := httptest.NewRecorder()
	h.SetTrashView(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if u.TrashView != "tree" {
		t.Errorf("TrashView = %q, want tree", u.TrashView)
	}
}

func TestSetTrashView_RejectsUnsupported(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"view":"grid"}`)
	r := reqWithClaims("PUT", "/auth/preferences/trash-view", body, claims)
	w := httptest.NewRecorder()
	h.SetTrashView(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
