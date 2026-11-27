package model

import (
	"time"

	"github.com/google/uuid"
)

type SyncRoot struct {
	ID        uuid.UUID `db:"id"`
	UserID    uuid.UUID `db:"user_id"`
	Name      string    `db:"name"`
	Path      string    `db:"path"`
	CreatedAt time.Time `db:"created_at"`
}

// SharedMount is the recipient-side projection of a user-share. Carries
// the underlying share id, the giver's profile and a `is_file` flag so
// the client can distinguish file-shares from directory-shares without
// a second lookup. See ADR-0010.
type SharedMount struct {
	ShareID         uuid.UUID
	SyncRootID      uuid.UUID
	SyncRootName    string
	Path            string
	IsFile          bool
	CanWrite        bool
	ExpiresAt       *time.Time
	OwnerID         uuid.UUID
	OwnerUsername   string
	OwnerAvatarPath *string
}

// SyncRootWithOwner enriches SyncRoot with the owning user's display
// fields so the detail panel can render the owner without an extra
// round-trip. AvatarPath is nil when the user has no avatar uploaded.
// See ADR-0009.
type SyncRootWithOwner struct {
	SyncRoot
	OwnerUsername   string  `db:"owner_username"`
	OwnerAvatarPath *string `db:"owner_avatar_path"`
}

type File struct {
	ID            uuid.UUID  `db:"id"`
	UserID        uuid.UUID  `db:"user_id"`
	SyncRootID    uuid.UUID  `db:"sync_root_id"`
	Path          string     `db:"path"`
	Size          int64      `db:"size"`
	MTime         time.Time  `db:"mtime"`
	ContentHash   []byte     `db:"content_hash"`
	IsDeleted     bool       `db:"is_deleted"`
	IsDir         bool       `db:"is_dir"`
	UpdatedAt     time.Time  `db:"updated_at"`
	ServerVersion int64      `db:"server_version"`
	// Monotonic change-sequence (ADR-0036) -- the changes-feed cursor.
	// Independent of updated_at: a clock correction cannot make it go backwards.
	Seq int64 `db:"seq"`
	TrashPath     *string    `db:"trash_path"`
	DeletedAt     *time.Time `db:"deleted_at"`
	// Actual user who last mutated this row — diverges from UserID when a
	// recipient writes through a writable Shared Mount (ADR-0013). NULL
	// for rows created before the migration.
	LastModifierUserID *uuid.UUID `db:"last_modifier_user_id"`
	// Cached delta-sync block signatures as raw JSON (a []delta.Signature
	// array), persisted at upload time so delta-request need not re-hash the
	// file. NULL means "unknown" — pre-migration rows or sub-threshold files —
	// and the read path falls back to on-the-fly hashing (ADR-0002). Kept
	// as raw bytes so model carries no dependency on the delta package.
	BlockHashes []byte `db:"block_hashes"`
}

// MoveEvent is a single changes-feed entry standing in for an entire
// directory-move subtree (ADR-0065): the client recomputes the prefix
// change on its own rows instead of seeing one entry per descendant. Seq
// is drawn from the same sequence as File.Seq so both sources share one
// total order in the feed merge.
type MoveEvent struct {
	SyncRootID uuid.UUID `db:"sync_root_id"`
	OldPrefix  string    `db:"old_prefix"`
	NewPrefix  string    `db:"new_prefix"`
	Seq        int64     `db:"seq"`
}

type Conflict struct {
	ID           uuid.UUID  `db:"id"            json:"id"`
	FileID       uuid.UUID  `db:"file_id"       json:"file_id"`
	ConflictPath string     `db:"conflict_path" json:"conflict_path"`
	ClientID     *uuid.UUID `db:"client_id"     json:"client_id"`
	Resolved     bool       `db:"resolved"      json:"resolved"`
	CreatedAt    time.Time  `db:"created_at"    json:"created_at"`
	ResolvedAt   *time.Time `db:"resolved_at"   json:"resolved_at"`
}

type ShareType string

const (
	ShareTypePublic ShareType = "public"
	ShareTypeUser   ShareType = "user"
)

type Share struct {
	ID           uuid.UUID  `db:"id"`
	UserID       uuid.UUID  `db:"user_id"`
	SyncRootID   *uuid.UUID `db:"sync_root_id"`
	TargetUserID *uuid.UUID `db:"target_user_id"`
	Path         string     `db:"path"`
	ShareType    ShareType  `db:"share_type"`
	Token        *string    `db:"token"`
	ReadToken    *string    `db:"read_token"`
	WriteToken   *string    `db:"write_token"`
	PasswordHash *string    `db:"password_hash"`
	ExpiresAt    *time.Time `db:"expires_at"`
	// Per-link expiry for public dual-links — read and write link carry
	// independent limits, set at link creation (ADR-0015).
	ReadExpiresAt  *time.Time `db:"read_expires_at"`
	WriteExpiresAt *time.Time `db:"write_expires_at"`
	CanWrite       bool       `db:"can_write"`
	CreatedAt      time.Time  `db:"created_at"`
}

// ShareGrant is one recipient of a user-share aggregate: per-recipient write
// permission and expiry. The recipient's mount anchor (ADR-0010/0011) is the
// grant id, so each recipient owns an independent row under the shared path.
// See ADR-0015.
type ShareGrant struct {
	ID           uuid.UUID  `db:"id"`
	ShareID      uuid.UUID  `db:"share_id"`
	TargetUserID uuid.UUID  `db:"target_user_id"`
	CanWrite     bool       `db:"can_write"`
	ExpiresAt    *time.Time `db:"expires_at"`
	CreatedAt    time.Time  `db:"created_at"`
}
