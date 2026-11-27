package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

type searchResponse struct {
	Files []struct {
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	} `json:"files"`
	NextCursor string `json:"next_cursor"`
}

func seedSearchFile(t *testing.T, env *testEnv, ctx context.Context, userID, rootID uuid.UUID, path string, isDir bool) *model.File {
	t.Helper()
	f, err := env.files.Upsert(ctx, &model.File{
		UserID:     userID,
		SyncRootID: rootID,
		Path:       path,
		Size:       0,
		MTime:      time.Now(),
		IsDir:      isDir,
	})
	if err != nil {
		t.Fatalf("upsert %s: %v", path, err)
	}
	return f
}

func doSearch(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, q string, extra url.Values) searchResponse {
	t.Helper()
	qs := url.Values{}
	qs.Set("q", q)
	for k, vs := range extra {
		for _, v := range vs {
			qs.Add(k, v)
		}
	}
	target := "/api/v1/sync-roots/" + rootID.String() + "/files/search?" + qs.Encode()
	r := reqWithClaims(http.MethodGet, target, nil, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.SearchFiles(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search status %d body=%s", w.Code, w.Body.String())
	}
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestSearchFiles_FindsByBasenameAndDirSegment(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/Bericht.pdf", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/foto.jpg", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/Fotos/2026/urlaub.jpg", false)

	resp := doSearch(t, env, claims, root.ID, "bericht", nil)
	if len(resp.Files) != 1 || resp.Files[0].Path != "/Bericht.pdf" {
		t.Fatalf("basename match: %+v", resp.Files)
	}

	// A directory segment matches the directory itself as well as what is
	// inside it -- directories are rows since ADR-0041, so searching for a
	// folder name finds the folder.
	resp = doSearch(t, env, claims, root.ID, "2026", nil)
	if len(resp.Files) != 2 ||
		resp.Files[0].Path != "/Fotos/2026" || !resp.Files[0].IsDir ||
		resp.Files[1].Path != "/Fotos/2026/urlaub.jpg" {
		t.Fatalf("dir segment match: %+v", resp.Files)
	}
}

func TestSearchFiles_ExcludesDeleted(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	f := seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/loeschkandidat.txt", false)
	if _, err := env.trash.SoftDelete(ctx, root.ID, f.Path, "trash/loeschkandidat.txt", claims.UserID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	resp := doSearch(t, env, claims, root.ID, "loesch", nil)
	if len(resp.Files) != 0 {
		t.Fatalf("deleted should be excluded: %+v", resp.Files)
	}
}

func TestSearchFiles_RespectsPathScope(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/outside/treffer.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/scope/inside/treffer.txt", false)

	resp := doSearch(t, env, claims, root.ID, "treffer", url.Values{"path": []string{"/scope"}})
	if len(resp.Files) != 1 || resp.Files[0].Path != "/scope/inside/treffer.txt" {
		t.Fatalf("scope: %+v", resp.Files)
	}
}

func TestSearchFiles_WildcardStar(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/foo-2026-bericht.pdf", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/foo-bericht.pdf", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/anders.txt", false)

	resp := doSearch(t, env, claims, root.ID, "foo*bericht", nil)
	if len(resp.Files) != 2 {
		t.Fatalf("wildcard: expected 2, got %+v", resp.Files)
	}
}

func TestSearchFiles_SqlMetaCharsLiteral(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/100_percent.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/100xpercent.txt", false)

	resp := doSearch(t, env, claims, root.ID, "100_percent", nil)
	paths := make([]string, 0, len(resp.Files))
	for _, f := range resp.Files {
		paths = append(paths, f.Path)
	}
	if len(paths) != 1 || paths[0] != "/100_percent.txt" {
		t.Fatalf("underscore must be literal: %+v", paths)
	}
}

func TestSearchFiles_CursorPagination(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	for _, name := range []string{"/seite-a.txt", "/seite-b.txt", "/seite-c.txt"} {
		seedSearchFile(t, env, ctx, claims.UserID, root.ID, name, false)
	}

	page1 := doSearch(t, env, claims, root.ID, "seite", url.Values{"limit": []string{"2"}})
	if len(page1.Files) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1: %+v cursor=%q", page1.Files, page1.NextCursor)
	}

	page2 := doSearch(t, env, claims, root.ID, "seite",
		url.Values{"limit": []string{"2"}, "cursor": []string{page1.NextCursor}})
	if len(page2.Files) != 1 || page2.NextCursor != "" {
		t.Fatalf("page2: %+v cursor=%q", page2.Files, page2.NextCursor)
	}
}

type globalSearchResponse struct {
	Groups []struct {
		SyncRootID   string `json:"sync_root_id"`
		SyncRootName string `json:"sync_root_name"`
		Files        []struct {
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"files"`
		NextCursor string `json:"next_cursor"`
	} `json:"groups"`
}

func doGlobalSearch(t *testing.T, env *testEnv, claims *auth.Claims, q string, extra url.Values) globalSearchResponse {
	t.Helper()
	qs := url.Values{}
	qs.Set("q", q)
	for k, vs := range extra {
		for _, v := range vs {
			qs.Add(k, v)
		}
	}
	r := reqWithClaims(http.MethodGet, "/api/v1/files/search?"+qs.Encode(), nil, claims)
	w := httptest.NewRecorder()
	env.handler.SearchFilesGlobal(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("global search status %d body=%s", w.Code, w.Body.String())
	}
	var resp globalSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestSearchFilesGlobal_GroupsHitsByRoot(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	r1 := createTestSyncRoot(t, env, claims.UserID, "alpha_"+uniqueSuffix())
	r2 := createTestSyncRoot(t, env, claims.UserID, "beta_"+uniqueSuffix())

	seedSearchFile(t, env, ctx, claims.UserID, r1.ID, "/bericht.pdf", false)
	seedSearchFile(t, env, ctx, claims.UserID, r2.ID, "/sub/bericht-notiz.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, r2.ID, "/anders.txt", false)

	resp := doGlobalSearch(t, env, claims, "bericht", nil)
	if len(resp.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", resp.Groups)
	}
	byRoot := map[string][]string{}
	for _, g := range resp.Groups {
		for _, f := range g.Files {
			byRoot[g.SyncRootID] = append(byRoot[g.SyncRootID], f.Path)
		}
	}
	if got := byRoot[r1.ID.String()]; len(got) != 1 || got[0] != "/bericht.pdf" {
		t.Fatalf("r1 hits: %+v", got)
	}
	if got := byRoot[r2.ID.String()]; len(got) != 1 || got[0] != "/sub/bericht-notiz.txt" {
		t.Fatalf("r2 hits: %+v", got)
	}
}

func TestSearchFilesGlobal_OmitsOtherUsersRoots(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	me := createTestUser(t, env)
	mine := createTestSyncRoot(t, env, me.UserID, "mine_"+uniqueSuffix())
	seedSearchFile(t, env, ctx, me.UserID, mine.ID, "/treffer.txt", false)

	other := createTestUser(t, env)
	theirs := createTestSyncRoot(t, env, other.UserID, "theirs_"+uniqueSuffix())
	seedSearchFile(t, env, ctx, other.UserID, theirs.ID, "/treffer.txt", false)

	resp := doGlobalSearch(t, env, me, "treffer", nil)
	if len(resp.Groups) != 1 {
		t.Fatalf("expected only own root, got %+v", resp.Groups)
	}
	if resp.Groups[0].SyncRootID != mine.ID.String() {
		t.Fatalf("wrong group: %+v", resp.Groups[0])
	}
}

func TestSearchFilesGlobal_OmitsRootsWithoutHits(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	r1 := createTestSyncRoot(t, env, claims.UserID, "hit_"+uniqueSuffix())
	_ = createTestSyncRoot(t, env, claims.UserID, "miss_"+uniqueSuffix())
	seedSearchFile(t, env, ctx, claims.UserID, r1.ID, "/treffer.txt", false)

	resp := doGlobalSearch(t, env, claims, "treffer", nil)
	if len(resp.Groups) != 1 || resp.Groups[0].SyncRootID != r1.ID.String() {
		t.Fatalf("only hit root expected: %+v", resp.Groups)
	}
}

func TestSearchFilesGlobal_PerGroupCursor(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	r := createTestSyncRoot(t, env, claims.UserID, "page_"+uniqueSuffix())
	for _, n := range []string{"/seite-a.txt", "/seite-b.txt", "/seite-c.txt"} {
		seedSearchFile(t, env, ctx, claims.UserID, r.ID, n, false)
	}

	resp := doGlobalSearch(t, env, claims, "seite", url.Values{"limit": []string{"2"}})
	if len(resp.Groups) != 1 {
		t.Fatalf("groups: %+v", resp.Groups)
	}
	g := resp.Groups[0]
	if len(g.Files) != 2 || g.NextCursor == "" {
		t.Fatalf("expected 2 files + cursor, got %+v cursor=%q", g.Files, g.NextCursor)
	}
}

func TestSearchFilesGlobal_EmptyQueryRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodGet, "/api/v1/files/search?q=", nil, claims)
	w := httptest.NewRecorder()
	env.handler.SearchFilesGlobal(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSearchFiles_EmptyQueryRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "sroot_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/search?q=", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.SearchFiles(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
