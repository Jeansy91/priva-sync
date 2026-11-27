package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/repository"
)

// ADR-0041: a directory is a row. Every write path that can introduce a new
// path has to materialise the directories above it, or the file lands
// somewhere the navigation cannot reach -- present, syncing, invisible.

// liveDirs returns the live directory rows in a root, so a test can name the
// missing one instead of counting.
func liveDirs(t *testing.T, rootID uuid.UUID) []string {
	t.Helper()
	rows, err := getPool(t).Query(context.Background(),
		`SELECT path FROM files
		 WHERE sync_root_id = $1 AND is_deleted = FALSE AND is_dir = TRUE
		 ORDER BY path`, rootID)
	if err != nil {
		t.Fatalf("read live dirs: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan dir path: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func TestDirectoryRows_UpsertMaterialisesEveryAncestor(t *testing.T) {
	pool := getPool(t)
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.Upsert(context.Background(), fileAt(u.ID, root.ID, "/a/b/c/deep.txt")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	wantPaths(t, "directories after upload", liveDirs(t, root.ID),
		[]string{"/a", "/a/b", "/a/b/c"})
}

// The directory has to reach the client before the file inside it, or a
// cursor-paged sync sees a file whose parent it has never heard of.
func TestDirectoryRows_AncestorsCarryTheSmallerSeq(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	file, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/box/inside.txt"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	dir, err := files.FindByPath(ctx, root.ID, "/box")
	if err != nil {
		t.Fatalf("find /box: %v", err)
	}
	if dir.Seq >= file.Seq {
		t.Fatalf("directory seq %d is not before its file's %d", dir.Seq, file.Seq)
	}
}

// Uploading a second file into the same directory must not fail on the row
// that is already there, nor duplicate it.
func TestDirectoryRows_MaterialisationIsIdempotent(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/shared/one.txt", "/shared/two.txt", "/shared/deep/three.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	wantPaths(t, "directories", liveDirs(t, root.ID), []string{"/shared", "/shared/deep"})
}

func TestDirectoryRows_CreateDirectoryMaterialisesItsAncestors(t *testing.T) {
	pool := getPool(t)
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.CreateDirectory(context.Background(), u.ID, root.ID, "/x/y/z", time.Now()); err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}

	wantPaths(t, "directories", liveDirs(t, root.ID), []string{"/x", "/x/y", "/x/y/z"})
}

// Every destination-taking mutation needs the same guarantee, so they are
// checked together: the destination's ancestors exist afterwards.
func TestDirectoryRows_DestinationAncestorsExistAfterMutation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		seed  string
		apply func(*repository.FileRepository, uuid.UUID, uuid.UUID) error
		want  []string
	}{
		{
			name: "MoveFile",
			seed: "/src.txt",
			apply: func(f *repository.FileRepository, rootID, actor uuid.UUID) error {
				_, err := f.MoveFile(ctx, rootID, "/src.txt", "/into/a/moved.txt", actor)
				return err
			},
			want: []string{"/into", "/into/a"},
		},
		{
			name: "CopyFile",
			seed: "/src.txt",
			apply: func(f *repository.FileRepository, rootID, actor uuid.UUID) error {
				_, err := f.CopyFile(ctx, rootID, "/src.txt", "/into/a/copy.txt", actor)
				return err
			},
			want: []string{"/into", "/into/a"},
		},
		{
			name: "MoveDirectory",
			seed: "/dir/child.txt",
			apply: func(f *repository.FileRepository, rootID, actor uuid.UUID) error {
				_, err := f.MoveDirectory(ctx, rootID, "/dir", "/into/a/dir", actor)
				return err
			},
			want: []string{"/into", "/into/a", "/into/a/dir"},
		},
		{
			name: "CopyDirectory",
			seed: "/dir/child.txt",
			apply: func(f *repository.FileRepository, rootID, actor uuid.UUID) error {
				_, err := f.CopyDirectory(ctx, rootID, "/dir", "/into/a/dir", actor)
				return err
			},
			want: []string{"/dir", "/into", "/into/a", "/into/a/dir"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := getPool(t)
			u := createTestUser(t)
			root := createTestSyncRoot(t, u.ID)
			files := repository.NewFileRepository(pool)

			if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, tt.seed)); err != nil {
				t.Fatalf("seed %s: %v", tt.seed, err)
			}
			if err := tt.apply(files, root.ID, u.ID); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}

			wantPaths(t, tt.name+" directories", liveDirs(t, root.ID), tt.want)
		})
	}
}

// A restore lands in a directory that may have been purged while the file sat
// in the trash. The row has to come back with it.
func TestDirectoryRows_RestoreMaterialisesItsAncestors(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)
	trash := repository.NewTrashRepository(pool)

	f, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/gone/file.txt"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := trash.SoftDelete(ctx, root.ID, f.Path, "trash/file.txt", u.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// The directory is purged behind its back, as the trash sweep would.
	if _, err := pool.Exec(ctx,
		`DELETE FROM files WHERE sync_root_id = $1 AND path = '/gone'`, root.ID); err != nil {
		t.Fatalf("purge directory: %v", err)
	}

	restored, err := trash.RestoreFromTrash(ctx, f.ID, u.ID)
	if err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if restored.Path != "/gone/file.txt" {
		t.Fatalf("restored to %q", restored.Path)
	}
	wantPaths(t, "directories after restore", liveDirs(t, root.ID), []string{"/gone"})
}

// parent_path is derived by the database, so a bulk path rewrite cannot leave
// it stale -- that is the whole reason it is a trigger and not a column the
// repository writes.
func TestDirectoryRows_ParentPathFollowsABulkMove(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/old/deep/file.txt")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := files.MoveDirectory(ctx, root.ID, "/old", "/new", u.ID); err != nil {
		t.Fatalf("MoveDirectory: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT path, parent_path FROM files
		 WHERE sync_root_id = $1 AND is_deleted = FALSE ORDER BY path`, root.ID)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p, parent string
		if err := rows.Scan(&p, &parent); err != nil {
			t.Fatalf("scan: %v", err)
		}
		want := p[:len(p)-len(baseName(p))]
		if parent != want {
			t.Errorf("row %q has parent_path %q, want %q", p, parent, want)
		}
	}

	// And the listing follows: the level under /new/deep holds the file.
	assertShapes(t, listLevel(t, files, root.ID, "/new/deep", "", 100),
		[]entryShape{{Name: "file.txt"}})
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// A file already sitting where a directory would have to go is a state that
// predates ADR-0041 and cannot be created through it. Materialisation must
// leave it alone rather than overwrite a file with a directory.
func TestDirectoryRows_DoesNotOverwriteAFileStandingInTheWay(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	blocker, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/thing"))
	if err != nil {
		t.Fatalf("upsert blocker: %v", err)
	}
	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/thing/inside.txt")); err != nil {
		t.Fatalf("upsert under blocker: %v", err)
	}

	still, err := files.FindByPath(ctx, root.ID, "/thing")
	if err != nil {
		t.Fatalf("find blocker: %v", err)
	}
	if still.ID != blocker.ID || still.IsDir {
		t.Fatalf("/thing = %+v, want the original file row untouched", still)
	}
}
