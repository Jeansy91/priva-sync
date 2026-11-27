package repository_test

import (
	"context"
	"errors"
	"testing"

	"privasync/server/internal/repository"
)

func newAppPasswordRepo(t *testing.T) *repository.AppPasswordRepository {
	t.Helper()
	return repository.NewAppPasswordRepository(getPool(t))
}

// The secret is generated server-side and shown once; only its hash is kept,
// so a database dump cannot be replayed against WebDAV.
func TestAppPassword_CreateReturnsSecretOnceAndStoresOnlyItsHash(t *testing.T) {
	repo := newAppPasswordRepo(t)
	u := createTestUser(t)
	ctx := context.Background()

	created, secret, err := repo.Create(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if secret == "" {
		t.Fatal("Create returned an empty secret")
	}
	if len(secret) < 24 {
		t.Errorf("secret is only %d chars — too little entropy to skip a slow KDF", len(secret))
	}
	if created.Name != "Laptop" {
		t.Errorf("Name = %q, want Laptop", created.Name)
	}

	listed, err := repo.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d app passwords, want 1", len(listed))
	}
	if listed[0].SecretHash == secret {
		t.Error("the plaintext secret was stored instead of its hash")
	}
	if listed[0].LastUsedAt != nil {
		t.Error("a fresh app password must not claim to have been used")
	}
}

func TestAppPassword_FindBySecret(t *testing.T) {
	repo := newAppPasswordRepo(t)
	u := createTestUser(t)
	ctx := context.Background()

	_, secret, err := repo.Create(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{name: "the issued secret", secret: secret},
		{name: "a wrong secret", secret: "not-the-secret", wantErr: true},
		{name: "an empty secret", secret: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.FindBySecret(ctx, tt.secret)
			if tt.wantErr {
				if !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("FindBySecret = %v, want ErrNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindBySecret: %v", err)
			}
			if got.UserID != u.ID {
				t.Errorf("app password belongs to %v, want %v", got.UserID, u.ID)
			}
		})
	}
}

// Revoking one credential must not disturb the others — that is the whole
// point of issuing them per device.
func TestAppPassword_DeleteRevokesOnlyItsOwn(t *testing.T) {
	repo := newAppPasswordRepo(t)
	u := createTestUser(t)
	ctx := context.Background()

	laptop, laptopSecret, err := repo.Create(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, phoneSecret, err := repo.Create(ctx, u.ID, "Phone")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, laptop.ID, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.FindBySecret(ctx, laptopSecret); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("revoked secret still resolves: %v", err)
	}
	if _, err := repo.FindBySecret(ctx, phoneSecret); err != nil {
		t.Fatalf("the other app password stopped working: %v", err)
	}
}

// A user must not be able to revoke someone else's credential by guessing an id.
func TestAppPassword_DeleteIsScopedToTheOwner(t *testing.T) {
	repo := newAppPasswordRepo(t)
	owner := createTestUser(t)
	stranger := createTestUser(t)
	ctx := context.Background()

	ap, secret, err := repo.Create(ctx, owner.ID, "Laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, ap.ID, stranger.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Delete by a stranger = %v, want ErrNotFound", err)
	}
	if _, err := repo.FindBySecret(ctx, secret); err != nil {
		t.Fatalf("the owner's app password was revoked by a stranger: %v", err)
	}
}

func TestAppPassword_TouchLastUsed(t *testing.T) {
	repo := newAppPasswordRepo(t)
	u := createTestUser(t)
	ctx := context.Background()

	ap, _, err := repo.Create(ctx, u.ID, "Laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.TouchLastUsed(ctx, ap.ID); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}

	listed, err := repo.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if listed[0].LastUsedAt == nil {
		t.Fatal("last_used_at was not recorded")
	}
}
