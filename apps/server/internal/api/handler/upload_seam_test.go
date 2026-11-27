package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// fakeUploadStore is a hand-written uploadStore (ADR-0029) that returns
// configured errors, so the handler's 500 paths — otherwise only reachable on
// a real DB failure — can be exercised deterministically.
type fakeUploadStore struct {
	createErr error
	getErr    error
	addErr    error
	markErr   error
	session   *model.UploadSession
}

func (f fakeUploadStore) CreateSession(_ context.Context, s *model.UploadSession) (*model.UploadSession, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return s, nil
}

func (f fakeUploadStore) GetSession(_ context.Context, _, _ uuid.UUID) (*model.UploadSession, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.session, nil
}

func (f fakeUploadStore) AddRange(_ context.Context, _ uuid.UUID, _, _ int64) (*model.UploadSession, error) {
	if f.addErr != nil {
		return nil, f.addErr
	}
	return f.session, nil
}

func (f fakeUploadStore) MarkQueued(_ context.Context, _ uuid.UUID, _ *int64) (*model.UploadSession, error) {
	if f.markErr != nil {
		return nil, f.markErr
	}
	queued := *f.session
	queued.Status = model.UploadSessionQueued
	return &queued, nil
}

func TestInitUpload_InternalErrorWhenCreateSessionFails(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	env.uploadH.uploads = fakeUploadStore{createErr: errors.New("db down")}

	body := bytes.NewBufferString(`{"path":"/x.txt","total_size":5}`)
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/uploads", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInternal)
}

func TestUploadChunk_InternalErrorWhenGetSessionFails(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	// A non-ErrNotFound failure must map to 500, not 404.
	env.uploadH.uploads = fakeUploadStore{getErr: errors.New("db down")}

	uploadID := uuid.New()
	r := reqWithClaims(http.MethodPut, "/uploads/"+uploadID.String()+"/chunks/0", bytes.NewBufferString("chunk"), claims)
	r = withURLParams(r, "uploadID", uploadID.String(), "offset", "0")
	w := httptest.NewRecorder()
	env.uploadH.UploadChunk(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInternal)
}

func TestCompleteUpload_InternalErrorWhenGetSessionFails(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	env.uploadH.uploads = fakeUploadStore{getErr: errors.New("db down")}

	uploadID := uuid.New()
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/complete", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String())
	w := httptest.NewRecorder()
	env.uploadH.CompleteUpload(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInternal)
}
