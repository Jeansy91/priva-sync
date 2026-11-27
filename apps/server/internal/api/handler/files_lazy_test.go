package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type listResponse struct {
	Entries []struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	} `json:"entries"`
	NextCursor string `json:"next_cursor"`
}

func TestListFiles_LazyOneLevelOnly(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "lazy_"+uniqueSuffix())

	// Eine Datei direkt unter / und eine zwei Ebenen tief.
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/top.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/deep/sub/leaf.txt", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp listResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	gotNames := map[string]bool{}
	for _, e := range resp.Entries {
		gotNames[e.Name] = true
	}
	if !gotNames["top.txt"] {
		t.Fatalf("missing top.txt: %+v", resp.Entries)
	}
	if !gotNames["deep"] {
		t.Fatalf("missing implicit dir 'deep': %+v", resp.Entries)
	}
	if gotNames["leaf.txt"] || gotNames["sub"] {
		t.Fatalf("leaked deeper entries: %+v", resp.Entries)
	}
}

func TestListFiles_ImplicitDirHasIsDir(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "lazy_"+uniqueSuffix())

	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/fotos/a.jpg", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp listResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "fotos" || !resp.Entries[0].IsDir {
		t.Fatalf("implicit dir not marked is_dir: %+v", resp.Entries)
	}
}

func TestListFiles_CursorPagination(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "lazy_"+uniqueSuffix())

	for _, n := range []string{"/a.txt", "/b.txt", "/c.txt"} {
		seedSearchFile(t, env, ctx, claims.UserID, root.ID, n, false)
	}

	req := func(qs string) listResponse {
		r := reqWithClaims(http.MethodGet,
			"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/&"+qs, nil, claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.ListFiles(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", w.Code, w.Body.String())
		}
		var resp listResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}

	p1 := req("limit=2")
	if len(p1.Entries) != 2 || p1.NextCursor == "" {
		t.Fatalf("p1 wrong: %+v cursor=%q", p1.Entries, p1.NextCursor)
	}
	p2 := req("limit=2&cursor=" + url.QueryEscape(p1.NextCursor))
	if len(p2.Entries) != 1 || p2.NextCursor != "" {
		t.Fatalf("p2 wrong: %+v cursor=%q", p2.Entries, p2.NextCursor)
	}
}

func TestDirectoryStats_RecursiveAggregate(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "lazy_"+uniqueSuffix())

	// 2 Dateien in /scope, 1 Datei in /scope/sub. /other ist ausserhalb.
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/scope/a.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/scope/b.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/scope/sub/c.txt", false)
	seedSearchFile(t, env, ctx, claims.UserID, root.ID, "/other/x.txt", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/directories/stats?path=/scope", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DirectoryStats(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var stats struct {
		ItemCount int   `json:"item_count"`
		FileCount int   `json:"file_count"`
		TotalSize int64 `json:"total_size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// direkte Kinder: a.txt, b.txt, sub  => 3
	if stats.ItemCount != 3 {
		t.Fatalf("item_count: want 3 got %d", stats.ItemCount)
	}
	if stats.FileCount != 3 {
		t.Fatalf("file_count: want 3 got %d", stats.FileCount)
	}
}
