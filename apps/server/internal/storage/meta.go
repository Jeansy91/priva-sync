package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// ─── Path helpers ─────────────────────────────────────────────────────────────

// Sidecar directory names under dataPath. Centralised so prune/recovery jobs
// agree with the writers on the on-disk layout (see ADR-0001).
const (
	DirUsers     = "users"
	DirSyncRoots = "sync_roots"
	DirShares    = "shares"
	DirAvatars   = "avatars"
)

func (s *Service) userMetaPath(userID uuid.UUID) string {
	return filepath.Join(s.dataPath, DirUsers, userID.String()+".json")
}

func (s *Service) rootMetaPath(rootID uuid.UUID) string {
	return filepath.Join(s.dataPath, DirSyncRoots, rootID.String()+".json")
}

func (s *Service) shareMetaPath(shareID uuid.UUID) string {
	return filepath.Join(s.dataPath, DirShares, shareID.String()+".json")
}

// writeJSONFile atomically writes v as indented JSON to path.
func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(v); encErr != nil {
		f.Close()
		os.Remove(tmp)
		return encErr
	}
	if syncErr := f.Sync(); syncErr != nil {
		f.Close()
		os.Remove(tmp)
		return syncErr
	}
	f.Close()
	return os.Rename(tmp, path)
}

// ─── User ─────────────────────────────────────────────────────────────────────

// UserMeta is the on-disk JSON representation of a user.
// Contains all fields needed to reconstruct the users table row.
// Volatile counters (used_bytes/trash_bytes/quota_state) are excluded —
// they are derived from the files table by RecomputeQuota post-recovery.
type UserMeta struct {
	ID                string  `json:"id"`
	Username          string  `json:"username"`
	Email             string  `json:"email"`
	PasswordHash      string  `json:"password_hash"`
	TOTPSecret        *string `json:"totp_secret,omitempty"`
	TOTPEnabled       bool    `json:"totp_enabled"`
	Role              string  `json:"role"`
	QuotaBytes        int64   `json:"quota_bytes"`
	IsActive          bool    `json:"is_active"`
	EmailNotify       bool    `json:"email_notify"`
	AvatarPath        *string `json:"avatar_path,omitempty"`
	PreferredLanguage string  `json:"preferred_language"`
	ShowFileTree       bool    `json:"show_file_tree"`
	ShowFileCheckboxes bool    `json:"show_file_checkboxes"`
	TrashView          string  `json:"trash_view"`
	CreatedAt         string  `json:"created_at"`
}

// WriteUserMeta persists user credentials and settings to /data/users/{id}.json.
// Called after every user create or update so the file stays in sync with the DB.
func (s *Service) WriteUserMeta(u *model.User) error {
	return writeJSONFile(s.userMetaPath(u.ID), UserMeta{
		ID:                u.ID.String(),
		Username:          u.Username,
		Email:             u.Email,
		PasswordHash:      u.PasswordHash,
		TOTPSecret:        u.TOTPSecret,
		TOTPEnabled:       u.TOTPEnabled,
		Role:              string(u.Role),
		QuotaBytes:        u.QuotaBytes,
		IsActive:          u.IsActive,
		EmailNotify:       u.EmailNotify,
		AvatarPath:        u.AvatarPath,
		PreferredLanguage: u.PreferredLanguage,
		ShowFileTree:       u.ShowFileTree,
		ShowFileCheckboxes: u.ShowFileCheckboxes,
		TrashView:          u.TrashView,
		CreatedAt:         u.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Service) DeleteUserMeta(userID uuid.UUID) error {
	err := os.Remove(s.userMetaPath(userID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ─── Sync Root ────────────────────────────────────────────────────────────────

// RootMeta is the on-disk JSON representation of a sync root.
type RootMeta struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	CreatedAt string `json:"created_at"`
}

// WriteRootMeta persists sync root metadata to /data/sync_roots/{id}.json.
func (s *Service) WriteRootMeta(r *model.SyncRoot) error {
	return writeJSONFile(s.rootMetaPath(r.ID), RootMeta{
		ID:        r.ID.String(),
		UserID:    r.UserID.String(),
		Name:      r.Name,
		Path:      r.Path,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Service) DeleteRootMeta(rootID uuid.UUID) error {
	err := os.Remove(s.rootMetaPath(rootID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ─── Share ────────────────────────────────────────────────────────────────────

// ShareMeta is the on-disk JSON representation of a share.
type ShareMeta struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id"`
	SyncRootID   *string `json:"sync_root_id,omitempty"`
	TargetUserID *string `json:"target_user_id,omitempty"`
	Path         string  `json:"path"`
	ShareType    string  `json:"share_type"`
	Token        *string `json:"token,omitempty"`
	PasswordHash *string `json:"password_hash,omitempty"`
	ExpiresAt    *string `json:"expires_at,omitempty"`
	CanWrite     bool    `json:"can_write"`
	CreatedAt    string  `json:"created_at"`
}

// WriteShareMeta persists share configuration to /data/shares/{id}.json.
func (s *Service) WriteShareMeta(sh *model.Share) error {
	meta := ShareMeta{
		ID:           sh.ID.String(),
		UserID:       sh.UserID.String(),
		Path:         sh.Path,
		ShareType:    string(sh.ShareType),
		Token:        sh.Token,
		PasswordHash: sh.PasswordHash,
		CanWrite:     sh.CanWrite,
		CreatedAt:    sh.CreatedAt.UTC().Format(time.RFC3339),
	}
	if sh.SyncRootID != nil {
		s := sh.SyncRootID.String()
		meta.SyncRootID = &s
	}
	if sh.TargetUserID != nil {
		s := sh.TargetUserID.String()
		meta.TargetUserID = &s
	}
	if sh.ExpiresAt != nil {
		t := sh.ExpiresAt.UTC().Format(time.RFC3339)
		meta.ExpiresAt = &t
	}
	return writeJSONFile(s.shareMetaPath(sh.ID), meta)
}

func (s *Service) DeleteShareMeta(shareID uuid.UUID) error {
	err := os.Remove(s.shareMetaPath(shareID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// DeleteShareMetas removes all named share snapshots in one call.  Used by
// cascading deletes (sync-root removal) so callers do not have to loop.
// Missing files are not an error; any other failure aborts and is returned.
func (s *Service) DeleteShareMetas(shareIDs []uuid.UUID) error {
	for _, id := range shareIDs {
		if err := os.Remove(s.shareMetaPath(id)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

