package auth

import (
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// WebAuthnUser is a thin adapter over model.User + already-loaded
// credentials, implementing the go-webauthn library's User interface. Its
// getters have no branches — this just proves the wiring is correct.
func TestWebAuthnUser_ImplementsLibraryInterface(t *testing.T) {
	u := &model.User{ID: uuid.New(), Username: "webauthn_test_user"}
	creds := []webauthn.Credential{{ID: []byte("cred-1")}}

	wu := NewWebAuthnUser(u, creds)

	if got := wu.WebAuthnID(); string(got) != string(u.ID[:]) {
		t.Errorf("WebAuthnID = %x, want %x", got, u.ID[:])
	}
	if got := wu.WebAuthnName(); got != u.Username {
		t.Errorf("WebAuthnName = %q, want %q", got, u.Username)
	}
	if got := wu.WebAuthnDisplayName(); got != u.Username {
		t.Errorf("WebAuthnDisplayName = %q, want %q", got, u.Username)
	}
	got := wu.WebAuthnCredentials()
	if len(got) != 1 || string(got[0].ID) != "cred-1" {
		t.Errorf("WebAuthnCredentials = %+v, want the seeded credential", got)
	}
}

func TestNewWebAuthn_BuildsRelyingPartyFromBaseURL(t *testing.T) {
	w, err := NewWebAuthn("https://sync.example.com", "PrivaSync")
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}
	if w.Config.RPID != "sync.example.com" {
		t.Errorf("RPID = %q, want sync.example.com", w.Config.RPID)
	}
	if len(w.Config.RPOrigins) != 1 || w.Config.RPOrigins[0] != "https://sync.example.com" {
		t.Errorf("RPOrigins = %v, want [https://sync.example.com]", w.Config.RPOrigins)
	}
}

func TestNewWebAuthn_RejectsInvalidBaseURL(t *testing.T) {
	if _, err := NewWebAuthn("://not-a-url", "PrivaSync"); err == nil {
		t.Error("expected an error for a malformed base URL")
	}
}

// Login always goes through BeginDiscoverableLogin (usernameless, no
// allowCredentials) — a registration that doesn't force a resident key can
// create a credential that verifies fine but is invisible to that flow.
func TestNewWebAuthn_RequiresResidentKey(t *testing.T) {
	w, err := NewWebAuthn("https://sync.example.com", "PrivaSync")
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}
	if got := w.Config.AuthenticatorSelection.ResidentKey; got != protocol.ResidentKeyRequirementRequired {
		t.Errorf("AuthenticatorSelection.ResidentKey = %q, want %q", got, protocol.ResidentKeyRequirementRequired)
	}
}
