package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
)

// A share whose path is "/" is a root-share -- the UI has a dedicated case
// for it (shared_mounts_listing.go). Activating such a mount pulls its delta
// feed through ListChangedFilesSinceUnderPrefix, so "/" has to mean
// "everything".
func TestListChangedFilesSinceUnderPrefix_RootPrefixCoversTheWholeTree(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/a.txt", "/dir/b.txt", "/dir/sub/c.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	tests := []struct {
		name   string
		prefix string
		want   int
	}{
		{name: "root prefix", prefix: "/", want: 5},
		{name: "sub directory", prefix: "/dir", want: 4},
		{name: "deeper directory", prefix: "/dir/sub", want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := files.ListChangedFilesSinceUnderPrefix(ctx, root.ID, tt.prefix, 0, 100)
			if err != nil {
				t.Fatalf("ListChangedFilesSinceUnderPrefix(%q): %v", tt.prefix, err)
			}
			if len(got) != tt.want {
				t.Fatalf("ListChangedFilesSinceUnderPrefix(%q) returned %d rows, want %d",
					tt.prefix, len(got), tt.want)
			}
		})
	}
}

// The subtree walk backs cascade-delete and the public-share ZIP; it is
// scoped the same way and therefore raises the same question at the root.
func TestSubtreeWalk_RootPrefixReturnsTheWholeTree(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/a.txt", "/dir/b.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	wantPaths(t, `EachInSubtree("/")`, pathsOf(collectSubtree(t, files, root.ID, "/")),
		[]string{"/a.txt", "/dir", "/dir/b.txt"})
}
