package repository_test

import (
	"context"
	"encoding/json"
	"testing"

	"privasync/server/internal/repository"
)

// credentialBlob is a stand-in for the JSON-marshaled webauthn.Credential
// production code stores here — the column is JSONB, so arbitrary bytes are
// rejected by Postgres.
func credentialBlob(tag string) []byte {
	return []byte(`{"tag":"` + tag + `"}`)
}

func TestPasskeyRepository_FindByCredentialID(t *testing.T) {
	pool := getPool(t)
	repo := repository.NewPasskeyRepository(pool)
	u := createTestUser(t)

	credID := []byte("cred-" + uniqueSuffix())
	if _, err := repo.Create(context.Background(), u.ID, credID, credentialBlob("v1"), "my key"); err != nil {
		t.Fatalf("create: %v", err)
	}

	gotUserID, err := repo.FindByCredentialID(context.Background(), credID)
	if err != nil {
		t.Fatalf("FindByCredentialID: %v", err)
	}
	if gotUserID != u.ID {
		t.Errorf("owner = %s, want %s", gotUserID, u.ID)
	}
}

func TestPasskeyRepository_FindByCredentialID_NotFound(t *testing.T) {
	pool := getPool(t)
	repo := repository.NewPasskeyRepository(pool)

	_, err := repo.FindByCredentialID(context.Background(), []byte("does-not-exist-"+uniqueSuffix()))
	if err != repository.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPasskeyRepository_UpdateSignCount(t *testing.T) {
	pool := getPool(t)
	repo := repository.NewPasskeyRepository(pool)
	u := createTestUser(t)

	credID := []byte("cred-" + uniqueSuffix())
	if _, err := repo.Create(context.Background(), u.ID, credID, credentialBlob("v1"), "my key"); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.UpdateSignCount(context.Background(), credID, credentialBlob("v2")); err != nil {
		t.Fatalf("UpdateSignCount: %v", err)
	}

	creds, err := repo.ListByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
	// Column is JSONB -- Postgres re-serializes on read (e.g. adds a space
	// after ":"), so compare parsed values rather than raw bytes.
	var got, want map[string]string
	if err := json.Unmarshal(creds[0].Credential, &got); err != nil {
		t.Fatalf("unmarshal stored credential: %v", err)
	}
	if err := json.Unmarshal(credentialBlob("v2"), &want); err != nil {
		t.Fatalf("unmarshal expected credential: %v", err)
	}
	if got["tag"] != want["tag"] {
		t.Errorf("credential tag = %q, want %q (post-update blob)", got["tag"], want["tag"])
	}
	if creds[0].LastUsedAt == nil {
		t.Error("last_used_at should be set after UpdateSignCount")
	}
}

func TestPasskeyRepository_UpdateSignCount_NotFound(t *testing.T) {
	pool := getPool(t)
	repo := repository.NewPasskeyRepository(pool)

	err := repo.UpdateSignCount(context.Background(), []byte("no-such-cred-"+uniqueSuffix()), credentialBlob("x"))
	if err != repository.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPasskeyRepository_Delete(t *testing.T) {
	pool := getPool(t)
	repo := repository.NewPasskeyRepository(pool)
	u := createTestUser(t)

	credID := []byte("cred-" + uniqueSuffix())
	created, err := repo.Create(context.Background(), u.ID, credID, credentialBlob("v1"), "my key")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Delete(context.Background(), created.ID, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	creds, err := repo.ListByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("expected 0 credentials after delete, got %d", len(creds))
	}
}

// Deleting another user's credential must fail (scoped by owner id).
func TestPasskeyRepository_Delete_WrongOwnerFails(t *testing.T) {
	pool := getPool(t)
	repo := repository.NewPasskeyRepository(pool)
	owner := createTestUser(t)
	other := createTestUser(t)

	created, err := repo.Create(context.Background(), owner.ID, []byte("cred-"+uniqueSuffix()), credentialBlob("v1"), "my key")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = repo.Delete(context.Background(), created.ID, other.ID)
	if err != repository.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}

	creds, err := repo.ListByUser(context.Background(), owner.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(creds) != 1 {
		t.Errorf("credential should still exist, got %d", len(creds))
	}
}
