package storage

import (
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestWriteAvatar_ThenReadBackMatches(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}

	if err := s.WriteAvatar(userID, png); err != nil {
		t.Fatalf("WriteAvatar: %v", err)
	}

	got, err := os.ReadFile(s.AvatarPath(userID))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(png) {
		t.Fatalf("avatar content = %v, want %v", got, png)
	}
}

func TestWriteAvatar_OverwritesExisting(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()

	if err := s.WriteAvatar(userID, []byte("first")); err != nil {
		t.Fatalf("WriteAvatar (first): %v", err)
	}
	if err := s.WriteAvatar(userID, []byte("second")); err != nil {
		t.Fatalf("WriteAvatar (second): %v", err)
	}

	got, err := os.ReadFile(s.AvatarPath(userID))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("avatar content = %q, want %q (overwrite)", got, "second")
	}
}

func TestDeleteAvatar_RemovesFile(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()

	if err := s.WriteAvatar(userID, []byte("data")); err != nil {
		t.Fatalf("WriteAvatar: %v", err)
	}
	if err := s.DeleteAvatar(userID); err != nil {
		t.Fatalf("DeleteAvatar: %v", err)
	}
	if _, err := os.Stat(s.AvatarPath(userID)); !os.IsNotExist(err) {
		t.Fatalf("avatar file still exists after DeleteAvatar, stat err = %v", err)
	}
}

func TestDeleteAvatar_NonexistentIsNotAnError(t *testing.T) {
	s := NewService(t.TempDir())
	if err := s.DeleteAvatar(uuid.New()); err != nil {
		t.Fatalf("DeleteAvatar (nonexistent) = %v, want nil", err)
	}
}

func TestAvatarPath_IsDeterministicPerUser(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	if s.AvatarPath(userID) != s.AvatarPath(userID) {
		t.Fatal("AvatarPath must be deterministic for the same userID")
	}
	if s.AvatarPath(userID) == s.AvatarPath(uuid.New()) {
		t.Fatal("AvatarPath must differ between distinct users")
	}
}
