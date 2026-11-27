package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// AvatarsDirName is the central directory for all profile pictures. Lives
// alongside user sync roots so avatars are never accidentally synced.
const AvatarsDirName = "avatars"

// AvatarPath liefert den absoluten Pfad zur Avatar-Datei eines Users.
// Format: {dataPath}/avatars/{userID}.png
func (s *Service) AvatarPath(userID uuid.UUID) string {
	return filepath.Join(s.dataPath, AvatarsDirName, userID.String()+".png")
}

// WriteAvatar writes PNG bytes to {dataPath}/avatars/{userID}.png. Atomic
// via tmp+rename so an aborted upload never leaves half a file behind.
func (s *Service) WriteAvatar(userID uuid.UUID, png []byte) error {
	dir := filepath.Join(s.dataPath, AvatarsDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating avatar dir: %w", err)
	}
	target := s.AvatarPath(userID)
	tmp, err := os.CreateTemp(dir, ".tmp-avatar-*")
	if err != nil {
		return fmt.Errorf("creating temp avatar: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(png); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing avatar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing avatar: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming avatar: %w", err)
	}
	return nil
}

// DeleteAvatar entfernt die Avatar-Datei eines Users. Nicht-existent ist OK.
func (s *Service) DeleteAvatar(userID uuid.UUID) error {
	if err := os.Remove(s.AvatarPath(userID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
