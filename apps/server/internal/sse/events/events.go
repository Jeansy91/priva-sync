// Package events holds the typed payload structs that travel through SSE.
// Each payload satisfies the Payload interface; NewEnvelope wraps it into the
// versioned wire format {type, version, data} consumed by web and desktop.
//
//go:generate tygo generate
package events

import (
	"time"

	"github.com/google/uuid"
)

// EnvelopeVersion is the wire-format version. Bumped only on breaking changes
// to the envelope itself (not on payload field additions).
const EnvelopeVersion = 2

// Payload is the constraint every SSE event struct must satisfy.
type Payload interface {
	EventName() string
}

// Envelope is the JSON shape on the wire: a discriminated union keyed by Type.
type Envelope[T Payload] struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	Data    T      `json:"data"`
}

// NewEnvelope wraps payload p into the versioned envelope. Type is taken from
// p.EventName() so the wire tag can never drift from the Go type.
func NewEnvelope[T Payload](p T) Envelope[T] {
	return Envelope[T]{
		Type:    p.EventName(),
		Version: EnvelopeVersion,
		Data:    p,
	}
}

// File mirrors the model.File row fields needed by web/desktop list views.
// Kept as a flat struct (not the DB model) so the wire format is decoupled
// from server-internal representations.
type File struct {
	ID            uuid.UUID `json:"id"`
	Path          string    `json:"path"`
	Size          int64     `json:"size"`
	MTime         time.Time `json:"mtime"`
	ContentHash   string    `json:"content_hash"`
	IsDeleted     bool      `json:"is_deleted"`
	IsDir         bool      `json:"is_dir"`
	ServerVersion int64     `json:"server_version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// FileUploaded is the SSE payload announcing a successful upload. RootID is
// flat so consumers can dispatch without nesting; the full File row is nested
// so list views can patch in place without a re-fetch.
type FileUploaded struct {
	RootID uuid.UUID `json:"root_id"`
	Path   string    `json:"path"`
	File   File      `json:"file"`
}

// EventName implements Payload.
func (FileUploaded) EventName() string { return "file_uploaded" }

// FileMoved announces a server-side rename of a single file. From/To are full
// paths inside the sync root; File carries the post-move row so list views can
// patch in place without a re-fetch.
type FileMoved struct {
	RootID uuid.UUID `json:"sync_root_id"`
	FileID uuid.UUID `json:"file_id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	File   File      `json:"file"`
}

// EventName implements Payload.
func (FileMoved) EventName() string { return "file_moved" }

// FileDeleted announces a soft-delete: the row left the live view but its
// bytes still live in trash. Trash-side UI gets a separate TrashAdded.
type FileDeleted struct {
	RootID uuid.UUID `json:"sync_root_id"`
	FileID uuid.UUID `json:"file_id"`
	Path   string    `json:"path"`
}

// EventName implements Payload.
func (FileDeleted) EventName() string { return "file_deleted" }

// FileRestored announces a row leaving the trash back to live state. The full
// re-appeared row travels via the separate FileAdded event (live-list patch),
// so this payload stays minimal — UI uses it only to clear the trash entry.
type FileRestored struct {
	RootID uuid.UUID `json:"sync_root_id"`
	FileID uuid.UUID `json:"file_id"`
	Path   string    `json:"path"`
}

// EventName implements Payload. Wire name renamed from trash_restored — the
// trash side of the action is implicit in the verb "restored".
func (FileRestored) EventName() string { return "file_restored" }

// ConflictAdded announces a new conflict row. CurrentPath is the live row's
// path when the conflict is a move-conflict (source kept, loser-path created);
// empty for upload/restore conflicts where the original_path is authoritative.
type ConflictAdded struct {
	ConflictID   uuid.UUID `json:"conflict_id"`
	FileID       uuid.UUID `json:"file_id"`
	RootID       uuid.UUID `json:"sync_root_id"`
	ConflictPath string    `json:"conflict_path"`
	OriginalPath string    `json:"original_path"`
	CurrentPath  string    `json:"current_path,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// EventName implements Payload.
func (ConflictAdded) EventName() string { return "conflict_added" }

// QuotaUpdated is a content-free ping: bytes/used/limit changed somewhere,
// the client should re-query the quota endpoint. Coalescing on the UI side
// keeps the wire chatty-but-cheap (empty body).
type QuotaUpdated struct{}

// EventName implements Payload.
func (QuotaUpdated) EventName() string { return "quota_updated" }

// QuotaThresholdCrossed fires when the user's usage crosses into a higher
// quota band (warning80 → critical95). De-escalations stay server-internal.
// NewState mirrors bus.QuotaThresholdCrossed so the SSE consumer can render
// the matching banner without re-deriving the band from UsedPct.
type QuotaThresholdCrossed struct {
	NewState string `json:"new_state"`
	UsedPct  int    `json:"used_pct"`
}

// EventName implements Payload.
func (QuotaThresholdCrossed) EventName() string { return "quota_threshold_crossed" }

// TrashAdded announces a row entering trash. Sibling to FileDeleted (live
// list) — emitted in the same TX so both panels patch in lock-step.
type TrashAdded struct {
	RootID    uuid.UUID `json:"sync_root_id"`
	FileID    uuid.UUID `json:"file_id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	TrashPath string    `json:"trash_path"`
}

// EventName implements Payload.
func (TrashAdded) EventName() string { return "trash_added" }

// TrashEmptied is a content-free signal: every trashed row of the user just
// went away. The client drops its trash view wholesale.
type TrashEmptied struct{}

// EventName implements Payload.
func (TrashEmptied) EventName() string { return "trash_emptied" }

// FileAdded announces a row appearing in the live list. Used by directory
// mkdir on the handler side and by restore-from-trash in the coordinator. The
// full row travels nested so the consumer can patch without a re-fetch.
type FileAdded struct {
	RootID uuid.UUID `json:"sync_root_id"`
	Path   string    `json:"path"`
	File   File      `json:"file"`
}

// EventName implements Payload.
func (FileAdded) EventName() string { return "file_added" }

// ConflictResolved announces that a conflict row has been settled. Desktop
// triggers an immediate sync-cycle on this so the canonical state lands on
// disk; web reloads the conflicts list.
type ConflictResolved struct {
	ConflictID uuid.UUID `json:"conflict_id"`
	FileID     uuid.UUID `json:"file_id"`
	RootID     uuid.UUID `json:"sync_root_id"`
	FilePath   string    `json:"file_path"`
}

// EventName implements Payload.
func (ConflictResolved) EventName() string { return "conflict_resolved" }

// SyncRootRef is the minimal sync-root descriptor pushed via SSE. Kept flat so
// list views can append the row without a re-fetch.
type SyncRootRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

// SyncRootAdded announces a new sync root for the user.
type SyncRootAdded struct {
	Root SyncRootRef `json:"root"`
}

// EventName implements Payload.
func (SyncRootAdded) EventName() string { return "sync_root_added" }

// SyncRootDeleted announces removal of a sync root for the user.
type SyncRootDeleted struct {
	RootID uuid.UUID `json:"sync_root_id"`
}

// EventName implements Payload.
func (SyncRootDeleted) EventName() string { return "sync_root_deleted" }

// SyncRootRenamed announces a display-name change of a sync root. Only the
// name changes; id and storage path are stable, so other tabs/clients just
// patch the label (and fix the URL if they are viewing the renamed root).
type SyncRootRenamed struct {
	RootID uuid.UUID `json:"sync_root_id"`
	Name   string    `json:"name"`
}

// EventName implements Payload.
func (SyncRootRenamed) EventName() string { return "sync_root_renamed" }

// TrashPurged announces that a single trash row has been permanently removed.
type TrashPurged struct {
	RootID uuid.UUID `json:"sync_root_id"`
	FileID uuid.UUID `json:"file_id"`
}

// EventName implements Payload.
func (TrashPurged) EventName() string { return "trash_purged" }

// ShareRef mirrors the api shareResponse shape needed by list views. Kept flat
// inside ShareAdded so the consumer can patch without a re-fetch. Optional
// fields use pointers/omitempty to match the REST response exactly.
type ShareRef struct {
	ID           uuid.UUID  `json:"id"`
	SyncRootID   *uuid.UUID `json:"sync_root_id,omitempty"`
	TargetUserID *uuid.UUID `json:"target_user_id,omitempty"`
	Path         string     `json:"path"`
	ShareType    string     `json:"share_type"`
	Token        *string    `json:"token,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CanWrite     bool       `json:"can_write"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ShareAdded announces a new share. Emitted to both the owner and (if set) the
// target user so both sides patch their share list in lock-step.
type ShareAdded struct {
	Share ShareRef `json:"share"`
}

// EventName implements Payload.
func (ShareAdded) EventName() string { return "share_added" }

// ShareUpdated announces a mutable field of an existing share changed in place
// (currently only can_write). Carries the full post-update ShareRef so both the
// owner and the target can patch their share list without a re-fetch — same
// shape as ShareAdded. The desktop treats it as latency-only and re-pulls on
// receipt; the pull stays authoritative (ADR-0014).
type ShareUpdated struct {
	Share ShareRef `json:"share"`
}

// EventName implements Payload.
func (ShareUpdated) EventName() string { return "share_updated" }

// ShareRevoked announces a share has been deleted by the owner.
type ShareRevoked struct {
	ShareID uuid.UUID `json:"share_id"`
}

// EventName implements Payload.
func (ShareRevoked) EventName() string { return "share_revoked" }
