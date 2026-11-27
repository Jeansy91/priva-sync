package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

func newUserHandlerTestEnv(t *testing.T) (*UserHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	h := NewUserHandler(env.users)
	return h, env
}

func uniqueUsername(prefix string) string {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	return prefix + "_" + suffix
}

func TestSearchUsers_PrefixMatch(t *testing.T) {
	h, env := newUserHandlerTestEnv(t)
	caller := createTestUser(t, env)

	name := uniqueUsername("alice")
	seedUser(t, env, name, name+"@t.local")

	// A proper prefix that still carries most of the unique suffix: "alice"
	// alone matches every alice_* user other tests left in the shared DB,
	// and the result limit would push this one out.
	r := reqWithClaims("GET", "/users/search?q="+name[:len(name)-2], nil, caller)
	w := httptest.NewRecorder()
	h.Search(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []struct {
		ID       uuid.UUID `json:"id"`
		Username string    `json:"username"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, u := range got {
		if u.Username == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("user %q not in results: %+v", name, got)
	}
}

func TestSearchUsers_ExcludesSelf(t *testing.T) {
	h, env := newUserHandlerTestEnv(t)

	// Caller mit eindeutigem Username erstellen, der dem Prefix matcht.
	name := uniqueUsername("zoe")
	u := seedUser(t, env, name, name+"@t.local")
	caller := &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess}

	r := reqWithClaims("GET", "/users/search?q="+name[:5], nil, caller)
	w := httptest.NewRecorder()
	h.Search(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []userSummary
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	for _, x := range got {
		if x.ID == u.ID {
			t.Fatalf("caller %s should not appear in results: %+v", u.ID, got)
		}
	}
}

func TestSearchUsers_ExcludesInactive(t *testing.T) {
	h, env := newUserHandlerTestEnv(t)
	caller := createTestUser(t, env)

	name := uniqueUsername("dora")
	u := seedUser(t, env, name, name+"@t.local")
	if err := env.users.SetActive(context.Background(), u.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	r := reqWithClaims("GET", "/users/search?q="+name[:5], nil, caller)
	w := httptest.NewRecorder()
	h.Search(w, r)

	var got []userSummary
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	for _, x := range got {
		if x.ID == u.ID {
			t.Fatalf("inactive user must not be returned: %+v", got)
		}
	}
}

func TestSearchUsers_EmptyQueryReturnsUsers(t *testing.T) {
	h, env := newUserHandlerTestEnv(t)
	caller := createTestUser(t, env)

	// Seed mindestens ein anderer aktiver User, damit das Resultat nicht
	// nur am Self-Filter haengt.
	other := createTestUser(t, env)
	_ = other

	r := reqWithClaims("GET", "/users/search?q=", nil, caller)
	w := httptest.NewRecorder()
	h.Search(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []userSummary
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least one user for empty query")
	}
	for _, u := range got {
		if u.ID == caller.UserID {
			t.Fatalf("caller %s leaked in empty-query result", caller.UserID)
		}
	}
}

func TestSearchUsers_CappedAtLimit(t *testing.T) {
	h, env := newUserHandlerTestEnv(t)
	caller := createTestUser(t, env)

	prefix := strings.ReplaceAll(uuid.NewString(), "-", "")[:6] + "_"
	for i := 0; i < userSearchLimit+3; i++ {
		name := prefix + uniqueUsername("u")
		seedUser(t, env, name, name+"@t.local")
	}

	r := reqWithClaims("GET", "/users/search?q="+prefix, nil, caller)
	w := httptest.NewRecorder()
	h.Search(w, r)

	var got []userSummary
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != userSearchLimit {
		t.Fatalf("expected %d results (cap), got %d", userSearchLimit, len(got))
	}
}
