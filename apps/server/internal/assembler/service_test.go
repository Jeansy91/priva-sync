package assembler_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/assembler"
	"privasync/server/internal/delta"
	"privasync/server/internal/errcode"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

type fakeStore struct {
	sess      *model.UploadSession
	claimErr  error
	completed *model.UploadSessionResult
	failedAs  string
	requeued  string
}

func (f *fakeStore) ClaimSession(context.Context, uuid.UUID) (*model.UploadSession, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	claimed := *f.sess
	claimed.Status = model.UploadSessionRunning
	claimed.Attempts++
	f.sess = &claimed
	return &claimed, nil
}

func (f *fakeStore) MarkCompleted(_ context.Context, _ uuid.UUID, res model.UploadSessionResult, _ time.Duration) error {
	f.completed = &res
	return nil
}

func (f *fakeStore) MarkFailed(_ context.Context, _ uuid.UUID, code string, _ time.Duration) error {
	f.failedAs = code
	return nil
}

func (f *fakeStore) MarkRequeued(_ context.Context, _ uuid.UUID, code string) error {
	f.requeued = code
	return nil
}

type fakeStorage struct {
	written      int64
	hash         []byte
	content      []byte // mirrored into tee, as the real finalise pass does
	assembleErr  error
	deletedFiles []string
	chunksGone   bool
}

func (f *fakeStorage) FinalizeChunks(_ fmt.Stringer, _ uuid.UUID, _, _ string, tee io.Writer) (int64, []byte, error) {
	if f.assembleErr != nil {
		return 0, nil, f.assembleErr
	}
	if tee != nil {
		if _, err := tee.Write(f.content); err != nil {
			return 0, nil, err
		}
	}
	return f.written, f.hash, nil
}

func (f *fakeStorage) DeleteFile(_ uuid.UUID, _, path string) error {
	f.deletedFiles = append(f.deletedFiles, path)
	return nil
}

func (f *fakeStorage) DeleteChunkDir(fmt.Stringer) error {
	f.chunksGone = true
	return nil
}

type fakeCommitter struct {
	cmd    syncpkg.UploadCmd
	result syncpkg.UploadResult
	err    error
}

func (f *fakeCommitter) UploadFile(_ context.Context, cmd syncpkg.UploadCmd) (syncpkg.UploadResult, error) {
	f.cmd = cmd
	return f.result, f.err
}

func openSession() *model.UploadSession {
	return &model.UploadSession{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		SyncRootID: uuid.New(),
		Path:       "games/data.pak",
		TotalSize:  3000,
		Status:     model.UploadSessionOpen,
	}
}

func okResult() syncpkg.UploadResult {
	return syncpkg.UploadResult{File: &model.File{
		ID: uuid.New(), Path: "games/data.pak", ServerVersion: 4,
		ContentHash: []byte{0xAA},
	}}
}

func newService(t *testing.T, store *fakeStore, stor *fakeStorage, commit *fakeCommitter) *assembler.Service {
	t.Helper()
	return assembler.NewService(store, stor, commit, assembler.Options{
		MaxAttempts: 3, Retain: time.Hour, Timeout: time.Minute,
	})
}

// The whole point of the worker: chunks become a committed file, and the
// session row ends up carrying the verdict the client will read.
func TestAssemble_CommitsAndRecordsTheResult(t *testing.T) {
	sess := openSession()
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{written: sess.TotalSize, hash: []byte{0xAA}}
	commit := &fakeCommitter{result: okResult()}

	if again := newService(t, store, stor, commit).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); again {
		t.Fatal("a successful assembly asked for another attempt")
	}

	if store.completed == nil {
		t.Fatal("session was not marked completed")
	}
	if store.completed.Kind != model.UploadResultOK {
		t.Errorf("Kind = %q, want %q", store.completed.Kind, model.UploadResultOK)
	}
	if !stor.chunksGone {
		t.Error("chunk directory was left behind after a successful commit")
	}
	if commit.cmd.Size != sess.TotalSize {
		t.Errorf("committed size = %d, want %d", commit.cmd.Size, sess.TotalSize)
	}
	if commit.cmd.StagingPath == sess.Path {
		t.Error("committed the target path directly instead of a staging copy")
	}
}

// A hash mismatch is the server telling the client the bytes it received are
// not the bytes it promised. Retrying rereads the same chunks.
func TestAssemble_FailsForGoodOnAHashMismatch(t *testing.T) {
	sess := openSession()
	sess.ContentHash = []byte{0xAA}
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{written: sess.TotalSize, hash: []byte{0xBB}}
	commit := &fakeCommitter{result: okResult()}

	if again := newService(t, store, stor, commit).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); again {
		t.Fatal("a hash mismatch asked for another attempt")
	}
	if store.failedAs != errcode.ContentHashMismatch {
		t.Errorf("failed as %q, want %q", store.failedAs, errcode.ContentHashMismatch)
	}
	if len(stor.deletedFiles) != 1 {
		t.Errorf("staging files deleted = %v, want exactly one", stor.deletedFiles)
	}
	if store.completed != nil {
		t.Error("a mismatching upload was committed anyway")
	}
}

// A chunk file that is gone cannot come back — the session is finished, not
// unlucky.
func TestAssemble_FailsForGoodWhenAChunkIsGone(t *testing.T) {
	sess := openSession()
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{assembleErr: fmt.Errorf("opening chunk 2: %w", fs.ErrNotExist)}

	if again := newService(t, store, stor, &fakeCommitter{}).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); again {
		t.Fatal("a missing chunk asked for another attempt")
	}
	if store.failedAs != errcode.ChunksMissing {
		t.Errorf("failed as %q, want %q", store.failedAs, errcode.ChunksMissing)
	}
}

// A read error on the volume may well not repeat, so the session goes back to
// the queue with the reason rather than being answered with a verdict.
func TestAssemble_RetriesATransientAssemblyFailure(t *testing.T) {
	sess := openSession()
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{assembleErr: errors.New("input/output error")}

	if again := newService(t, store, stor, &fakeCommitter{}).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); !again {
		t.Fatal("a transient failure did not ask for another attempt")
	}
	if store.requeued != errcode.AssemblyFailed {
		t.Errorf("requeued with %q, want %q", store.requeued, errcode.AssemblyFailed)
	}
	if store.failedAs != "" {
		t.Errorf("session was failed as %q while a retry was still owed", store.failedAs)
	}
}

// Retrying is not free — the client is waiting and every attempt rereads the
// whole file. The last allowed attempt answers instead of looping.
func TestAssemble_StopsRetryingOnTheLastAttempt(t *testing.T) {
	sess := openSession()
	sess.Attempts = 2 // the claim makes this the third and last attempt
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{assembleErr: errors.New("input/output error")}

	if again := newService(t, store, stor, &fakeCommitter{}).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); again {
		t.Fatal("the last allowed attempt asked for one more")
	}
	if store.failedAs != errcode.AssemblyFailed {
		t.Errorf("failed as %q, want %q", store.failedAs, errcode.AssemblyFailed)
	}
}

// A full quota at commit time is a decision, not an accident.
func TestAssemble_FailsForGoodWhenTheQuotaIsFull(t *testing.T) {
	sess := openSession()
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{written: sess.TotalSize, hash: []byte{0xAA}}
	commit := &fakeCommitter{err: quota.ErrExceeded}

	if again := newService(t, store, stor, commit).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); again {
		t.Fatal("an exceeded quota asked for another attempt")
	}
	if store.failedAs != errcode.QuotaExceeded {
		t.Errorf("failed as %q, want %q", store.failedAs, errcode.QuotaExceeded)
	}
}

// A real conflict is a successful upload with an unhappy ending: the loser copy
// is materialised from the staging file, so that file must survive.
func TestAssemble_KeepsTheStagingFileForARealConflict(t *testing.T) {
	sess := openSession()
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{written: sess.TotalSize, hash: []byte{0xAA}}
	result := okResult()
	result.Conflict = syncpkg.ConflictReal
	result.ConflictPath = "games/data.pak.conflict-1"
	commit := &fakeCommitter{result: result}

	newService(t, store, stor, commit).Assemble(assembler.Job{SessionID: sess.ID, UserID: sess.UserID})

	if len(stor.deletedFiles) != 0 {
		t.Errorf("deleted %v — the loser copy is made from the staging file", stor.deletedFiles)
	}
	if store.completed == nil || store.completed.Kind != model.UploadResultConflict {
		t.Fatalf("recorded result = %+v, want a conflict", store.completed)
	}
	if store.completed.ConflictPath == nil || *store.completed.ConflictPath != result.ConflictPath {
		t.Errorf("ConflictPath = %v, want %q", store.completed.ConflictPath, result.ConflictPath)
	}
}

// A session someone else already holds, or that already has an answer, is not
// assembled a second time.
func TestAssemble_LeavesASessionItCannotClaimAlone(t *testing.T) {
	sess := openSession()
	store := &fakeStore{sess: sess, claimErr: repository.ErrConflict}
	stor := &fakeStorage{}

	if again := newService(t, store, stor, &fakeCommitter{}).Assemble(
		assembler.Job{SessionID: sess.ID, UserID: sess.UserID},
	); again {
		t.Fatal("an unclaimable session asked for another attempt")
	}
	if store.completed != nil || store.failedAs != "" || store.requeued != "" {
		t.Error("an unclaimable session was written to anyway")
	}
}

// The delta cache used to cost a second full read of the finished file. The
// assembler now tees the finalise pass through a signature writer, so the
// signatures must arrive at the coordinator ready-made and be byte-identical
// to what a separate BlockSignatures read would have produced.
func TestAssemble_CarriesBlockSignaturesFromTheFinalisePass(t *testing.T) {
	sess := openSession()
	content := bytes.Repeat([]byte("delta"), 700_000) // > MinDeltaSize
	sess.TotalSize = int64(len(content))
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{written: sess.TotalSize, hash: []byte{0x01}, content: content}
	commit := &fakeCommitter{result: okResult()}

	newService(t, store, stor, commit).Assemble(assembler.Job{SessionID: sess.ID})

	want, err := delta.BlockSignatures(bytes.NewReader(content), delta.BlockSize)
	if err != nil {
		t.Fatalf("BlockSignatures: %v", err)
	}
	if !reflect.DeepEqual(commit.cmd.BlockSignatures, want) {
		t.Fatalf("committed signatures differ from a separate read: got %d sigs, want %d sigs",
			len(commit.cmd.BlockSignatures), len(want))
	}
}

// Below the delta threshold no client ever gets a delta offer, so hashing
// blocks would only cost CPU and a cache column nobody reads.
func TestAssemble_SkipsBlockSignaturesBelowTheDeltaThreshold(t *testing.T) {
	sess := openSession()
	sess.TotalSize = delta.MinDeltaSize
	store := &fakeStore{sess: sess}
	stor := &fakeStorage{written: sess.TotalSize, hash: []byte{0x01}, content: []byte("small")}
	commit := &fakeCommitter{result: okResult()}

	newService(t, store, stor, commit).Assemble(assembler.Job{SessionID: sess.ID})

	if commit.cmd.BlockSignatures != nil {
		t.Fatalf("want no signatures below the threshold, got %d", len(commit.cmd.BlockSignatures))
	}
}
