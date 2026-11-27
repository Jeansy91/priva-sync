package bus

import "github.com/google/uuid"

// Event names used by the coordinator and the quota service. Kept as
// constants so subscribers and publishers can't drift on spelling.
const (
	EventFileUploaded          = "file.uploaded"
	EventFileConflictKept      = "file.conflict_kept"
	EventFileConflictDiscarded = "file.conflict_discarded"
	EventFileTrashed           = "file.trashed"
	EventFileRestored          = "file.restored"
	EventFileMoved             = "file.moved"
	EventSyncRootDeleted       = "syncroot.deleted"
	EventFilePurgedByCron      = "file.purged_by_cron"
	EventAdminQuotaAdjusted    = "quota.admin_adjusted"
	EventQuotaThresholdCrossed = "quota.threshold_crossed"
	EventConflictCreated       = "conflict.created"
)

// FileMoved fires after a successful rename. Quota-neutral — published so
// audit/mail subscribers can observe the path change.
type FileMoved struct {
	UserID uuid.UUID
	RootID uuid.UUID
	FileID uuid.UUID
	From   string
	To     string
}

func (FileMoved) EventName() string { return EventFileMoved }

// FileUploaded fires after a successful upsert. Bytes is the new size,
// PrevBytes the previous size (0 for new files). Delta = Bytes - PrevBytes.
type FileUploaded struct {
	UserID    uuid.UUID
	RootID    uuid.UUID
	FileID    uuid.UUID
	Bytes     int64
	PrevBytes int64
}

func (FileUploaded) EventName() string { return EventFileUploaded }

// FileConflictKept fires when a loser-path bytes copy lands alongside the
// existing file. Bytes is the size of the loser copy.
type FileConflictKept struct {
	UserID uuid.UUID
	RootID uuid.UUID
	FileID uuid.UUID
	Bytes  int64
}

func (FileConflictKept) EventName() string { return EventFileConflictKept }

// FileConflictDiscarded fires when a user resolves a conflict by discarding
// the loser-path copy. Bytes is the size of the file that was removed from
// storage, freed from the quota's used counter.
type FileConflictDiscarded struct {
	UserID     uuid.UUID
	RootID     uuid.UUID
	FileID     uuid.UUID
	ConflictID uuid.UUID
	Bytes      int64
}

func (FileConflictDiscarded) EventName() string { return EventFileConflictDiscarded }

// FileTrashed fires when a file is soft-deleted: bytes move from used into
// trash. Total stays.
type FileTrashed struct {
	UserID uuid.UUID
	RootID uuid.UUID
	FileID uuid.UUID
	Bytes  int64
}

func (FileTrashed) EventName() string { return EventFileTrashed }

// FileRestored fires when a trashed file returns: bytes move from trash back
// into used.
type FileRestored struct {
	UserID uuid.UUID
	RootID uuid.UUID
	FileID uuid.UUID
	Bytes  int64
}

func (FileRestored) EventName() string { return EventFileRestored }

// SyncRootDeleted fires when a whole sync root is removed; both used and
// trash counters drop in one go.
type SyncRootDeleted struct {
	UserID     uuid.UUID
	RootID     uuid.UUID
	UsedBytes  int64
	TrashBytes int64
}

func (SyncRootDeleted) EventName() string { return EventSyncRootDeleted }

// FilePurgedByCron fires when the 30-day trash retention sweep hard-deletes
// a file. Bytes is the size of the purged trash entry and must be released
// from the user's trash counter in the same TX as the row delete.
type FilePurgedByCron struct {
	UserID uuid.UUID
	RootID uuid.UUID
	FileID uuid.UUID
	Bytes  int64
}

func (FilePurgedByCron) EventName() string { return EventFilePurgedByCron }

// AdminQuotaAdjusted fires from the admin recount endpoint with the freshly
// summed counters. Subscriber overwrites used_bytes/trash_bytes absolutely
// (not delta) so drift between DB and `files`-derived truth self-heals.
type AdminQuotaAdjusted struct {
	UserID   uuid.UUID
	NewUsed  int64
	NewTrash int64
}

func (AdminQuotaAdjusted) EventName() string { return EventAdminQuotaAdjusted }

// QuotaThresholdCrossed fires only on escalations into warning80/critical95
//. De-escalations stay internal to the quota service — the mail
// subscriber treats every received event as a candidate for a warning mail.
type QuotaThresholdCrossed struct {
	UserID   uuid.UUID
	NewState string
	UsedPct  int
}

func (QuotaThresholdCrossed) EventName() string { return EventQuotaThresholdCrossed }

// ConflictCreated fires after a conflict row is committed. The mail
// subscriber turns it into a durable KindMailSyncConflict outbox row so the
// conflict notification survives crashes between bus dispatch and SMTP delivery.
type ConflictCreated struct {
	UserID     uuid.UUID
	ConflictID uuid.UUID
	FilePath   string
}

func (ConflictCreated) EventName() string { return EventConflictCreated }
