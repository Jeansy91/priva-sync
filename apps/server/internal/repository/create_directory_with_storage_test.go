package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

type fakeDirCreator struct {
	dir *model.File
	err error
}

func (f fakeDirCreator) CreateDirectory(context.Context, uuid.UUID, uuid.UUID, string, time.Time) (*model.File, error) {
	return f.dir, f.err
}

type fakeDirDisk struct {
	err error
}

func (f fakeDirDisk) MakeDir(uuid.UUID, string, string) error {
	return f.err
}

// A DB error must short-circuit before the disk step is ever attempted —
// no orphaned directory on disk for a row that was never created.
func TestCreateDirectoryWithStorage_DBErrorSkipsDisk(t *testing.T) {
	dbErr := errors.New("db boom")
	diskCalled := false
	creator := fakeDirCreator{err: dbErr}
	disk := fakeDirDiskFunc(func() error {
		diskCalled = true
		return nil
	})

	dir, mkdirErr, err := repository.CreateDirectoryWithStorage(
		context.Background(), creator, disk, uuid.New(), uuid.New(), "/a", time.Now(),
	)

	if !errors.Is(err, dbErr) {
		t.Fatalf("err = %v, want %v", err, dbErr)
	}
	if dir != nil {
		t.Errorf("dir = %+v, want nil on DB error", dir)
	}
	if mkdirErr != nil {
		t.Errorf("mkdirErr = %v, want nil (never reached)", mkdirErr)
	}
	if diskCalled {
		t.Error("disk.MakeDir must not run when the DB step failed")
	}
}

// A successful DB row plus a failing disk mirror must still return the row
// — the caller (REST vs WebDAV) decides what to do with mkdirErr, this
// function must not turn a disk failure into a DB-level error.
func TestCreateDirectoryWithStorage_ReturnsRowAndMkdirErrSeparately(t *testing.T) {
	want := &model.File{Path: "/a"}
	mkdirBoom := errors.New("disk boom")
	creator := fakeDirCreator{dir: want}
	disk := fakeDirDisk{err: mkdirBoom}

	dir, mkdirErr, err := repository.CreateDirectoryWithStorage(
		context.Background(), creator, disk, uuid.New(), uuid.New(), "/a", time.Now(),
	)

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if dir != want {
		t.Errorf("dir = %+v, want %+v", dir, want)
	}
	if !errors.Is(mkdirErr, mkdirBoom) {
		t.Errorf("mkdirErr = %v, want %v", mkdirErr, mkdirBoom)
	}
}

// The success path with no disk failure returns both a row and a nil
// mkdirErr.
func TestCreateDirectoryWithStorage_Succeeds(t *testing.T) {
	want := &model.File{Path: "/a"}
	creator := fakeDirCreator{dir: want}
	disk := fakeDirDisk{}

	dir, mkdirErr, err := repository.CreateDirectoryWithStorage(
		context.Background(), creator, disk, uuid.New(), uuid.New(), "/a", time.Now(),
	)

	if err != nil || mkdirErr != nil {
		t.Fatalf("err = %v, mkdirErr = %v, want both nil", err, mkdirErr)
	}
	if dir != want {
		t.Errorf("dir = %+v, want %+v", dir, want)
	}
}

type fakeDirDiskFunc func() error

func (f fakeDirDiskFunc) MakeDir(uuid.UUID, string, string) error { return f() }
