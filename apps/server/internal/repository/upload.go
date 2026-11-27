package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

type UploadRepository struct {
	db *pgxpool.Pool
}

func NewUploadRepository(db *pgxpool.Pool) *UploadRepository {
	return &UploadRepository{db: db}
}

const sessionColumns = `id, user_id, sync_root_id, path, total_size, content_hash,
	received_ranges, mtime, if_match, created_at, expires_at,
	status, result_kind, result_file_id, result_server_version, result_content_hash,
	result_conflict_path, error_code, completed_at, attempts, last_error_code`

// int8multirangeGo is the Go representation pgx's default type map scans an
// int8multirange column into.
type int8multirangeGo = pgtype.Multirange[pgtype.Range[pgtype.Int8]]

func scanSession(row interface{ Scan(...any) error }, s *model.UploadSession) error {
	var ranges int8multirangeGo
	err := row.Scan(
		&s.ID, &s.UserID, &s.SyncRootID, &s.Path,
		&s.TotalSize, &s.ContentHash, &ranges,
		&s.MTime, &s.IfMatch, &s.CreatedAt, &s.ExpiresAt,
		&s.Status, &s.ResultKind, &s.ResultFileID, &s.ResultServerVersion,
		&s.ResultContentHash, &s.ResultConflictPath, &s.ErrorCode, &s.CompletedAt,
		&s.Attempts, &s.LastErrorCode,
	)
	if err != nil {
		return err
	}
	s.ReceivedRanges = byteRangesFromMultirange(ranges)
	return nil
}

// byteRangesFromMultirange reads the canonical [) bounds Postgres always
// normalises a discrete range to — int8range has no other form — into the
// domain type. An unbounded edge cannot occur here: every range this column
// ever holds comes from AddRange, which always supplies both bounds.
func byteRangesFromMultirange(mr int8multirangeGo) []model.ByteRange {
	out := make([]model.ByteRange, 0, len(mr))
	for _, r := range mr {
		out = append(out, model.ByteRange{Start: r.Lower.Int64, End: r.Upper.Int64})
	}
	return out
}

// CreateSession inserts a new upload session and returns the stored record.
func (r *UploadRepository) CreateSession(ctx context.Context, s *model.UploadSession) (*model.UploadSession, error) {
	var out model.UploadSession
	err := scanSession(r.db.QueryRow(ctx, `
		INSERT INTO upload_sessions
			(user_id, sync_root_id, path, total_size, content_hash, mtime, if_match)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING `+sessionColumns,
		s.UserID, s.SyncRootID, s.Path, s.TotalSize, s.ContentHash, s.MTime, s.IfMatch,
	), &out)
	if err != nil {
		return nil, fmt.Errorf("creating upload session: %w", err)
	}
	return &out, nil
}

// GetSession returns a session by ID, scoped to a user.
func (r *UploadRepository) GetSession(ctx context.Context, sessionID, userID uuid.UUID) (*model.UploadSession, error) {
	var out model.UploadSession
	err := scanSession(r.db.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM upload_sessions WHERE id = $1 AND user_id = $2`,
		sessionID, userID,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &out, err
}

// AddRange records [offset, offset+length) as received and returns the
// updated session. Idempotent by construction: int8multirange union merges a
// range that overlaps or repeats what is already there instead of erroring,
// so a retried chunk needs no special case the way a duplicate index once did.
func (r *UploadRepository) AddRange(ctx context.Context, sessionID uuid.UUID, offset, length int64) (*model.UploadSession, error) {
	var out model.UploadSession
	err := scanSession(r.db.QueryRow(ctx, `
		UPDATE upload_sessions
		SET received_ranges = received_ranges + int8multirange(int8range($2::bigint, $3::bigint))
		WHERE id = $1
		RETURNING `+sessionColumns,
		sessionID, offset, offset+length,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &out, err
}

// DeleteSession removes a session record (chunks must be cleaned up separately).
func (r *UploadRepository) DeleteSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM upload_sessions WHERE id = $1`, sessionID)
	return err
}

// MarkQueued accepts a complete for assembly: open -> queued, in one
// statement so two racing completes cannot both hand the same session to a
// worker. ErrConflict means the session is no longer open — already queued,
// already running, or already answered; the caller re-reads to find out which.
// A non-nil ifMatch overrides what init recorded; the assembly runs long after
// the request that carried the header is gone, so the value travels on the row.
func (r *UploadRepository) MarkQueued(ctx context.Context, sessionID uuid.UUID, ifMatch *int64) (*model.UploadSession, error) {
	var out model.UploadSession
	err := scanSession(r.db.QueryRow(ctx, `
		UPDATE upload_sessions
		   SET status   = $2,
		       if_match = COALESCE($3::bigint, if_match)
		 WHERE id = $1 AND status = $4
		RETURNING `+sessionColumns,
		sessionID, model.UploadSessionQueued, ifMatch, model.UploadSessionOpen,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	return &out, err
}

// ClaimSession is a worker taking ownership: queued -> running, one claim per
// worker. The attempt count rises with every claim, including one cut short by
// a restart — a session that strands repeatedly is a session nobody should
// keep re-reading a hundred gigabytes for.
func (r *UploadRepository) ClaimSession(ctx context.Context, sessionID uuid.UUID) (*model.UploadSession, error) {
	var out model.UploadSession
	err := scanSession(r.db.QueryRow(ctx, `
		UPDATE upload_sessions
		   SET status   = $2,
		       attempts = attempts + 1
		 WHERE id = $1 AND status = $3
		RETURNING `+sessionColumns,
		sessionID, model.UploadSessionRunning, model.UploadSessionQueued,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	return &out, err
}

// MarkRequeued hands a session back to the queue after an attempt failed for
// a reason the next one may not hit — a read error on the volume, a database
// blip. The reason is kept in last_error_code; error_code stays clear, because
// the session has not failed yet.
func (r *UploadRepository) MarkRequeued(ctx context.Context, sessionID uuid.UUID, code string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE upload_sessions
		   SET status          = $2,
		       last_error_code = $3
		 WHERE id = $1 AND status = $4`,
		sessionID, model.UploadSessionQueued, code, model.UploadSessionRunning,
	)
	return err
}

// MarkCompleted records the terminal answer a complete arrived at, so a
// repeated call replays it instead of assembling the file again. The expiry is
// pushed out from this moment: the row is only useful while the client might
// still retry, and its original 24h window may be nearly spent by the time the
// upload finishes.
func (r *UploadRepository) MarkCompleted(ctx context.Context, sessionID uuid.UUID, res model.UploadSessionResult, retain time.Duration) error {
	_, err := r.db.Exec(ctx, `
		UPDATE upload_sessions
		   SET status                = $2,
		       result_kind           = $3,
		       result_file_id        = $4,
		       result_server_version = $5,
		       result_content_hash   = $6,
		       result_conflict_path  = $7,
		       error_code            = NULL,
		       completed_at          = NOW(),
		       expires_at            = NOW() + $8::interval
		 WHERE id = $1`,
		sessionID, model.UploadSessionCompleted, res.Kind, res.FileID,
		res.ServerVersion, res.ContentHash, res.ConflictPath, retain.String(),
	)
	return err
}

// MarkFailed records a failure the session cannot recover from — the bytes on
// the server are what they are, so retrying only repeats the work and the
// answer.
func (r *UploadRepository) MarkFailed(ctx context.Context, sessionID uuid.UUID, code string, retain time.Duration) error {
	_, err := r.db.Exec(ctx, `
		UPDATE upload_sessions
		   SET status       = $2,
		       error_code   = $3,
		       completed_at = NOW(),
		       expires_at   = NOW() + $4::interval
		 WHERE id = $1`,
		sessionID, model.UploadSessionFailed, code, retain.String(),
	)
	return err
}

// RequeueInFlight resets every session the assembly worker was holding back to
// queued and returns them, so the boot sweep can submit them again. Exactly
// one server instance runs (ADR-0039), so after a restart nothing is genuinely
// running any more — a row still saying so is a worker that died with it.
func (r *UploadRepository) RequeueInFlight(ctx context.Context) ([]model.UploadSession, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE upload_sessions
		   SET status = $1
		 WHERE status IN ($1, $2)
		RETURNING `+sessionColumns,
		model.UploadSessionQueued, model.UploadSessionRunning,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []model.UploadSession
	for rows.Next() {
		var s model.UploadSession
		if err := scanSession(rows, &s); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// ListSessionIDs returns the id of every session on record. The purge uses it
// to decide which chunk directories on disk still belong to something.
func (r *UploadRepository) ListSessionIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `SELECT id FROM upload_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListExpiredSessions returns sessions whose expires_at is before the given
// time. Used by the background purge goroutine. Sessions the assembly worker
// is holding are left alone however old they are — a slow upload can outlive
// its own window, and deleting the row mid-assembly would throw away the
// record of a file that is about to be committed.
func (r *UploadRepository) ListExpiredSessions(ctx context.Context, before time.Time) ([]model.UploadSession, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+sessionColumns+` FROM upload_sessions
		  WHERE expires_at < $1 AND status NOT IN ($2, $3)`,
		before, model.UploadSessionQueued, model.UploadSessionRunning,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []model.UploadSession
	for rows.Next() {
		var s model.UploadSession
		if err := scanSession(rows, &s); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
