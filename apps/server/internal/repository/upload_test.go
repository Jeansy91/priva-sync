package repository_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

func newTestUploadSession(t *testing.T) (*repository.UploadRepository, *model.UploadSession) {
	t.Helper()
	pool := getPool(t)
	uploads := repository.NewUploadRepository(pool)

	user := createTestUser(t)
	root := createTestSyncRoot(t, user.ID)

	created, err := uploads.CreateSession(context.Background(), &model.UploadSession{
		UserID:     user.ID,
		SyncRootID: root.ID,
		Path:       "foo/bar.txt",
		TotalSize:  1024,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() {
		_ = uploads.DeleteSession(context.Background(), created.ID)
	})
	return uploads, created
}

func TestUploadRepository_CreateAndGetSession(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	if created.ID == uuid.Nil {
		t.Fatal("CreateSession did not populate ID")
	}
	if len(created.ReceivedRanges) != 0 {
		t.Errorf("ReceivedRanges = %v, want none for a freshly created session", created.ReceivedRanges)
	}

	got, err := uploads.GetSession(context.Background(), created.ID, created.UserID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Path != "foo/bar.txt" {
		t.Errorf("Path = %q, want foo/bar.txt", got.Path)
	}
}

func TestUploadRepository_GetSession_WrongUserNotFound(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	_, err := uploads.GetSession(context.Background(), created.ID, uuid.New())
	if err != repository.ErrNotFound {
		t.Fatalf("GetSession(wrong user) = %v, want ErrNotFound", err)
	}
}

func TestUploadRepository_GetSession_UnknownIDNotFound(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	_, err := uploads.GetSession(context.Background(), uuid.New(), created.UserID)
	if err != repository.ErrNotFound {
		t.Fatalf("GetSession(unknown id) = %v, want ErrNotFound", err)
	}
}

func TestUploadRepository_AddRange_RecordsAndMerges(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	updated, err := uploads.AddRange(context.Background(), created.ID, 0, 256)
	if err != nil {
		t.Fatalf("AddRange: %v", err)
	}
	want := []model.ByteRange{{Start: 0, End: 256}}
	if !reflect.DeepEqual(updated.ReceivedRanges, want) {
		t.Fatalf("ReceivedRanges = %v, want %v", updated.ReceivedRanges, want)
	}

	// Adjacent range merges into one, rather than sitting as a second entry.
	updated2, err := uploads.AddRange(context.Background(), created.ID, 256, 256)
	if err != nil {
		t.Fatalf("AddRange(adjacent): %v", err)
	}
	want2 := []model.ByteRange{{Start: 0, End: 512}}
	if !reflect.DeepEqual(updated2.ReceivedRanges, want2) {
		t.Fatalf("ReceivedRanges = %v, want %v", updated2.ReceivedRanges, want2)
	}
}

// A retried chunk resends the same range. Union is naturally idempotent —
// unlike the old index bitmap, this must not be treated as an error.
func TestUploadRepository_AddRange_RetryOfTheSameRangeIsHarmless(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	if _, err := uploads.AddRange(context.Background(), created.ID, 0, 256); err != nil {
		t.Fatalf("AddRange: %v", err)
	}
	updated, err := uploads.AddRange(context.Background(), created.ID, 0, 256)
	if err != nil {
		t.Fatalf("AddRange (retry): %v", err)
	}
	want := []model.ByteRange{{Start: 0, End: 256}}
	if !reflect.DeepEqual(updated.ReceivedRanges, want) {
		t.Fatalf("ReceivedRanges = %v, want %v (retry must not duplicate)", updated.ReceivedRanges, want)
	}
}

func TestUploadRepository_AddRange_UnknownSessionNotFound(t *testing.T) {
	_, err := repository.NewUploadRepository(getPool(t)).AddRange(context.Background(), uuid.New(), 0, 256)
	if err != repository.ErrNotFound {
		t.Fatalf("AddRange(unknown session) = %v, want ErrNotFound", err)
	}
}

func TestUploadRepository_DeleteSession_RemovesRecord(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	if err := uploads.DeleteSession(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := uploads.GetSession(context.Background(), created.ID, created.UserID); err != repository.ErrNotFound {
		t.Fatalf("GetSession after delete = %v, want ErrNotFound", err)
	}
}

func TestUploadRepository_ListExpiredSessions(t *testing.T) {
	uploads, created := newTestUploadSession(t)

	// Sessions default to a future expiry -- nothing expired yet relative to now.
	notYetExpired, err := uploads.ListExpiredSessions(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("ListExpiredSessions: %v", err)
	}
	for _, s := range notYetExpired {
		if s.ID == created.ID {
			t.Fatal("freshly created session should not be expired yet")
		}
	}

	// Querying with a far-future cutoff must include it.
	expired, err := uploads.ListExpiredSessions(context.Background(), time.Now().Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("ListExpiredSessions: %v", err)
	}
	found := false
	for _, s := range expired {
		if s.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("ListExpiredSessions(far future) did not include the test session")
	}
}

// The purge needs to know which chunk directories on disk still belong to a
// session, so every id on record must show up here.
func TestListSessionIDs_IncludesALiveSession(t *testing.T) {
	uploads, sess := newTestUploadSession(t)

	ids, err := uploads.ListSessionIDs(context.Background())
	if err != nil {
		t.Fatalf("ListSessionIDs: %v", err)
	}

	found := false
	for _, id := range ids {
		if id == sess.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("session %s missing from %d listed ids", sess.ID, len(ids))
	}
}

// Accepting a complete must be a state transition, not a flag set from a
// read-modify-write: two completes racing on the same session may not both
// hand the file to a worker.
func TestUploadRepository_MarkQueued_AcceptsOnceFromOpen(t *testing.T) {
	uploads, created := newTestUploadSession(t)
	ctx := context.Background()

	queued, err := uploads.MarkQueued(ctx, created.ID, nil)
	if err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}
	if queued.Status != model.UploadSessionQueued {
		t.Errorf("Status = %q, want %q", queued.Status, model.UploadSessionQueued)
	}

	if _, err := uploads.MarkQueued(ctx, created.ID, nil); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("second MarkQueued error = %v, want ErrConflict", err)
	}
}

// The If-Match a client states on the complete request has to reach the
// worker, which runs long after the request is gone. It travels on the row.
func TestUploadRepository_MarkQueued_StoresTheIfMatchOverride(t *testing.T) {
	uploads, created := newTestUploadSession(t)
	want := int64(7)

	queued, err := uploads.MarkQueued(context.Background(), created.ID, &want)
	if err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}
	if queued.IfMatch == nil || *queued.IfMatch != want {
		t.Fatalf("IfMatch = %v, want %d", queued.IfMatch, want)
	}
}

// A worker claiming a session is the same kind of transition, and it counts:
// a session that strands or fails transiently must not be retried forever.
func TestUploadRepository_ClaimSession_TakesItRunningAndCounts(t *testing.T) {
	uploads, created := newTestUploadSession(t)
	ctx := context.Background()

	if _, err := uploads.MarkQueued(ctx, created.ID, nil); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}
	claimed, err := uploads.ClaimSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}
	if claimed.Status != model.UploadSessionRunning {
		t.Errorf("Status = %q, want %q", claimed.Status, model.UploadSessionRunning)
	}
	if claimed.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", claimed.Attempts)
	}

	if _, err := uploads.ClaimSession(ctx, created.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("second ClaimSession error = %v, want ErrConflict", err)
	}
}

// A transient failure — the volume hiccuped mid-read — puts the session back
// in the queue with the reason attached, rather than answering the client with
// a verdict the next attempt might well contradict.
func TestUploadRepository_MarkRequeued_ReturnsItToTheQueueWithTheReason(t *testing.T) {
	uploads, created := newTestUploadSession(t)
	ctx := context.Background()

	if _, err := uploads.MarkQueued(ctx, created.ID, nil); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}
	if _, err := uploads.ClaimSession(ctx, created.ID); err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}
	if err := uploads.MarkRequeued(ctx, created.ID, "ASSEMBLY_FAILED"); err != nil {
		t.Fatalf("MarkRequeued: %v", err)
	}

	got, err := uploads.GetSession(ctx, created.ID, created.UserID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != model.UploadSessionQueued {
		t.Errorf("Status = %q, want %q", got.Status, model.UploadSessionQueued)
	}
	if got.LastErrorCode == nil || *got.LastErrorCode != "ASSEMBLY_FAILED" {
		t.Errorf("LastErrorCode = %v, want ASSEMBLY_FAILED", got.LastErrorCode)
	}
	if got.ErrorCode != nil {
		t.Errorf("ErrorCode = %v, want nil — the session has not failed", got.ErrorCode)
	}
}

// After a restart there is no worker left holding anything (exactly one server
// instance — ADR-0039), so every session still marked queued or running is
// stranded and has to be picked up again.
func TestUploadRepository_RequeueInFlight_PicksUpStrandedSessions(t *testing.T) {
	uploads, created := newTestUploadSession(t)
	ctx := context.Background()

	if _, err := uploads.MarkQueued(ctx, created.ID, nil); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}
	if _, err := uploads.ClaimSession(ctx, created.ID); err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}

	stranded, err := uploads.RequeueInFlight(ctx)
	if err != nil {
		t.Fatalf("RequeueInFlight: %v", err)
	}
	found := false
	for _, s := range stranded {
		if s.ID == created.ID {
			found = true
			if s.Status != model.UploadSessionQueued {
				t.Errorf("Status = %q, want %q", s.Status, model.UploadSessionQueued)
			}
		}
	}
	if !found {
		t.Fatalf("stranded sessions %v do not include %s", stranded, created.ID)
	}
}
