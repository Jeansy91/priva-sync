package model

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// ByteRange is a half-open interval [Start, End) of bytes within the file
// being assembled. Chunks address themselves this way (ADR-0066) instead of
// by index, because their size is no longer constant across a session.
type ByteRange struct {
	Start int64
	End   int64
}

// MissingRanges is the complement of received within [0, totalSize) — what a
// resuming client still has to send. received may be unsorted and may
// overlap (a retried chunk can arrive again before the gap it left is
// noticed); both are normalised away by sorting and merging before the walk.
func MissingRanges(totalSize int64, received []ByteRange) []ByteRange {
	sorted := make([]ByteRange, len(received))
	copy(sorted, received)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	missing := make([]ByteRange, 0)
	var cursor int64
	for _, r := range sorted {
		if r.Start > cursor {
			missing = append(missing, ByteRange{Start: cursor, End: r.Start})
		}
		if r.End > cursor {
			cursor = r.End
		}
	}
	if cursor < totalSize {
		missing = append(missing, ByteRange{Start: cursor, End: totalSize})
	}
	return missing
}

type UploadSession struct {
	ID             uuid.UUID   `db:"id"`
	UserID         uuid.UUID   `db:"user_id"`
	SyncRootID     uuid.UUID   `db:"sync_root_id"`
	Path           string      `db:"path"`
	TotalSize      int64       `db:"total_size"`
	ContentHash    []byte      `db:"content_hash"` // pre-computed BLAKE3, may be nil
	ReceivedRanges []ByteRange `db:"received_ranges"`
	MTime          *time.Time  `db:"mtime"`
	IfMatch        *int64      `db:"if_match"`
	CreatedAt      time.Time   `db:"created_at"`
	ExpiresAt      time.Time   `db:"expires_at"`

	// Terminal state of the session. A completed or failed session answers a
	// repeated complete from these fields instead of assembling again — the
	// client retries for hours, and by then the chunks are gone.
	Status              string     `db:"status"`
	ResultKind          *string    `db:"result_kind"`
	ResultFileID        *uuid.UUID `db:"result_file_id"`
	ResultServerVersion *int64     `db:"result_server_version"`
	ResultContentHash   []byte     `db:"result_content_hash"`
	ResultConflictPath  *string    `db:"result_conflict_path"`
	ErrorCode           *string    `db:"error_code"`
	CompletedAt         *time.Time `db:"completed_at"`

	// Assembly bookkeeping. Attempts counts claims by the assembly worker, so
	// a session that fails deterministically stops being retried; the last
	// code survives between attempts, while ErrorCode is only set on the
	// terminal failure.
	Attempts      int     `db:"attempts"`
	LastErrorCode *string `db:"last_error_code"`
}

// Session states. A session stays open until a complete is accepted for it,
// then walks queued -> running -> completed | failed. A transient assembly
// failure puts it back to queued.
const (
	UploadSessionOpen      = "open"
	UploadSessionQueued    = "queued"
	UploadSessionRunning   = "running"
	UploadSessionCompleted = "completed"
	UploadSessionFailed    = "failed"
)

// UploadSessionInFlight reports whether the session is waiting for or being
// worked on by the assembly worker — the states a status long-poll waits on
// and the boot sweep requeues.
func UploadSessionInFlight(status string) bool {
	return status == UploadSessionQueued || status == UploadSessionRunning
}

// Result kinds, mirroring the Sync Coordinator's three outcomes.
const (
	UploadResultOK              = "ok"
	UploadResultVersionMismatch = "version_mismatch"
	UploadResultConflict        = "conflict"
)

// UploadSessionResult is the terminal answer a complete arrived at, in the
// shape the session row stores it.
type UploadSessionResult struct {
	Kind          string
	FileID        *uuid.UUID
	ServerVersion *int64
	ContentHash   []byte
	ConflictPath  *string
}
