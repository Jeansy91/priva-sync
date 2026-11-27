package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

// eventDataMap extracts the inner payload map from a parsed SSE event. Every
// publish path now carries the full envelope {type,version,data}, so the
// helper unwraps `.data` regardless of which payload struct produced it.
func eventDataMap(t *testing.T, ev parsedEvent) map[string]any {
	t.Helper()
	env, ok := ev.Data.(map[string]any)
	if !ok {
		t.Fatalf("eventDataMap: envelope is not an object: %T", ev.Data)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("eventDataMap: envelope.data missing or wrong shape: %v", env)
	}
	return data
}

// uploadHelper is a helper for tests: creates a file via the handler and
// drains the resulting file_uploaded event from the subscriber so subsequent
// assertions only see the event of interest.
func uploadHelper(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path string, content string, events <-chan parsedEvent) {
	t.Helper()
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files?path="+path, bytes.NewReader([]byte(content)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload helper status: %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
	if events != nil {
		waitForEvent(t, events, "file_uploaded", 2*time.Second)
	}
}

func TestUploadFilePublishesFileUploaded(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	body := bytes.NewReader([]byte("hello world"))
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files?path=/hello.txt", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()

	env.handler.UploadFile(w, r)

	if w.Code != 200 {
		t.Fatalf("upload status: got %d, body=%s", w.Code, w.Body.String())
	}

	env.drainOutbox(t)

	ev := waitForEvent(t, events, "file_uploaded", 2*time.Second)
	// Data is the full envelope as json.RawMessage so the wire bytes the
	// consumer sees match what NewEnvelope produced — re-parse it here.
	raw, ok := ev.Data.(json.RawMessage)
	if !ok {
		// Fallback: chunk parser may have unmarshalled into a generic any.
		buf, err := json.Marshal(ev.Data)
		if err != nil {
			t.Fatalf("re-marshal envelope: %v", err)
		}
		raw = buf
	}
	var env2 struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Data    struct {
			RootID string `json:"root_id"`
			Path   string `json:"path"`
			File   struct {
				ID   string `json:"id"`
				Path string `json:"path"`
				Size int64  `json:"size"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env2); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env2.Type != "file_uploaded" || env2.Version != 2 {
		t.Errorf("envelope header = %+v", env2)
	}
	if env2.Data.RootID != root.ID.String() {
		t.Errorf("root_id: got %s, want %s", env2.Data.RootID, root.ID)
	}
	if env2.Data.Path != "/hello.txt" || env2.Data.File.Path != "/hello.txt" {
		t.Errorf("paths: %+v", env2.Data)
	}
	if env2.Data.File.Size != int64(len("hello world")) {
		t.Errorf("size: got %d", env2.Data.File.Size)
	}
	if env2.Data.File.ID == "" {
		t.Errorf("file.id empty")
	}
}

func TestDeleteFilePublishesFileDeletedAndTrashAdded(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootDel")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	uploadHelper(t, env, claims, root, "/bye.txt", "bye", events)

	r := reqWithClaims("DELETE", "/sync-roots/"+root.ID.String()+"/files?path=/bye.txt", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()

	env.handler.DeleteFile(w, r)

	if w.Code != 204 {
		t.Fatalf("delete status: got %d, body=%s", w.Code, w.Body.String())
	}

	env.drainOutbox(t)

	// Beide Events koennen in beliebiger Reihenfolge eintreffen.
	gotDeleted := false
	gotTrashAdded := false
	deadline := time.After(2 * time.Second)
	for !(gotDeleted && gotTrashAdded) {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed prematurely")
			}
			m := eventDataMap(t, ev)
			switch ev.Type {
			case "file_deleted":
				gotDeleted = true
				if m["sync_root_id"] != root.ID.String() || m["path"] != "/bye.txt" {
					t.Errorf("file_deleted payload: %v", m)
				}
			case "trash_added":
				gotTrashAdded = true
				if m["sync_root_id"] != root.ID.String() {
					t.Errorf("trash_added sync_root_id: %v", m["sync_root_id"])
				}
				if m["path"] != "/bye.txt" {
					t.Errorf("trash_added path: %v", m["path"])
				}
			}
		case <-deadline:
			t.Fatalf("timeout: deleted=%v trash=%v", gotDeleted, gotTrashAdded)
		}
	}
}

func TestMoveFilePublishesFileMoved(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootMv")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	uploadHelper(t, env, claims, root, "/a.txt", "data", events)

	body, _ := json.Marshal(map[string]string{"op": "move", "from": "/a.txt", "to": "/b.txt"})
	r := reqWithClaims("PATCH", "/sync-roots/"+root.ID.String()+"/files", strings.NewReader(string(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()

	env.handler.MoveFile(w, r)

	if w.Code != 200 {
		t.Fatalf("move status: got %d, body=%s", w.Code, w.Body.String())
	}

	env.drainOutbox(t)

	ev := waitForEvent(t, events, "file_moved", 2*time.Second)
	m := eventDataMap(t, ev)
	if m["sync_root_id"] != root.ID.String() {
		t.Errorf("sync_root_id: %v", m["sync_root_id"])
	}
	if m["from"] != "/a.txt" || m["to"] != "/b.txt" {
		t.Errorf("from/to: %v -> %v", m["from"], m["to"])
	}
	if m["file_id"] == nil {
		t.Errorf("file_id missing")
	}
}

func TestCreateSyncRootPublishesSyncRootAdded(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{"name": "Photos"})
	r := reqWithClaims("POST", "/sync-roots", strings.NewReader(string(body)), claims)
	w := httptest.NewRecorder()

	env.handler.CreateSyncRoot(w, r)

	if w.Code != 201 {
		t.Fatalf("create root status: got %d, body=%s", w.Code, w.Body.String())
	}

	ev := waitForEvent(t, events, "sync_root_added", 2*time.Second)
	m := eventDataMap(t, ev)
	root, ok := m["root"].(map[string]any)
	if !ok {
		t.Fatalf("root payload missing: %v", m)
	}
	if root["name"] != "Photos" {
		t.Errorf("name: %v", root["name"])
	}
}

// deleteHelper triggert DeleteFile + drainiert beide Events.
func deleteHelper(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path string, events <-chan parsedEvent) {
	t.Helper()
	r := reqWithClaims("DELETE", "/sync-roots/"+root.ID.String()+"/files?path="+path, nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != 204 {
		t.Fatalf("delete helper status: %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
	// drain file_deleted + trash_added
	gotDel, gotTrash := false, false
	deadline := time.After(2 * time.Second)
	for !(gotDel && gotTrash) {
		select {
		case ev := <-events:
			if ev.Type == "file_deleted" {
				gotDel = true
			} else if ev.Type == "trash_added" {
				gotTrash = true
			}
		case <-deadline:
			t.Fatalf("delete helper: drain timeout del=%v trash=%v", gotDel, gotTrash)
		}
	}
}

func TestRestoreTrashPublishesFileRestoredAndFileAdded(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootR")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	uploadHelper(t, env, claims, root, "/r.txt", "data", events)
	deleteHelper(t, env, claims, root, "/r.txt", events)

	// finde trashed-File-ID
	trashed, err := env.trash.ListTrash(context.Background(), root.ID)
	if err != nil || len(trashed) != 1 {
		t.Fatalf("expected 1 trashed file, got %d err=%v", len(trashed), err)
	}
	fileID := trashed[0].ID

	r := reqWithClaims("POST",
		"/sync-roots/"+root.ID.String()+"/trash/"+fileID.String()+"/restore",
		nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", fileID.String())
	w := httptest.NewRecorder()

	env.handler.RestoreTrash(w, r)

	if w.Code != 200 {
		t.Fatalf("restore status: got %d, body=%s", w.Code, w.Body.String())
	}

	env.drainOutbox(t)

	gotRestored, gotAdded := false, false
	deadline := time.After(2 * time.Second)
	for !(gotRestored && gotAdded) {
		select {
		case ev := <-events:
			switch ev.Type {
			case "file_restored":
				gotRestored = true
				m := eventDataMap(t, ev)
				if m["path"] != "/r.txt" {
					t.Errorf("restored path: %v", m["path"])
				}
			case "file_added":
				gotAdded = true
			}
		case <-deadline:
			t.Fatalf("restore drain timeout: restored=%v added=%v", gotRestored, gotAdded)
		}
	}
}

func TestPurgeTrashPublishesTrashPurged(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootP")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	uploadHelper(t, env, claims, root, "/p.txt", "data", events)
	deleteHelper(t, env, claims, root, "/p.txt", events)

	trashed, err := env.trash.ListTrash(context.Background(), root.ID)
	if err != nil || len(trashed) != 1 {
		t.Fatalf("trash setup: %d entries err=%v", len(trashed), err)
	}
	fileID := trashed[0].ID

	r := reqWithClaims("DELETE",
		"/sync-roots/"+root.ID.String()+"/trash/"+fileID.String(),
		nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", fileID.String())
	w := httptest.NewRecorder()

	env.handler.PurgeTrash(w, r)

	if w.Code != 204 {
		t.Fatalf("purge status: got %d, body=%s", w.Code, w.Body.String())
	}

	ev := waitForEvent(t, events, "trash_purged", 2*time.Second)
	m := eventDataMap(t, ev)
	if m["file_id"] != fileID.String() {
		t.Errorf("file_id: %v want %s", m["file_id"], fileID)
	}
}

func TestEmptyTrashPublishesTrashEmptied(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootE")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	uploadHelper(t, env, claims, root, "/x.txt", "data1", events)
	uploadHelper(t, env, claims, root, "/y.txt", "data2", events)
	deleteHelper(t, env, claims, root, "/x.txt", events)
	deleteHelper(t, env, claims, root, "/y.txt", events)

	r := reqWithClaims("DELETE", "/trash", nil, claims)
	w := httptest.NewRecorder()
	env.handler.EmptyTrash(w, r)
	if w.Code != 204 {
		t.Fatalf("empty status: got %d, body=%s", w.Code, w.Body.String())
	}

	env.drainOutbox(t)

	_ = waitForEvent(t, events, "trash_emptied", 2*time.Second)

	var rows int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE user_id = $1 AND is_deleted = TRUE`,
		claims.UserID).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("expected 0 trashed rows post-empty, got %d", rows)
	}
}

func TestCreateSharePublishesShareAdded(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootShare")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{
		"path":       "/",
		"share_type": "public",
	})
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", strings.NewReader(string(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()

	env.shareH.CreateShare(w, r)

	if w.Code != 201 {
		t.Fatalf("create share status: got %d, body=%s", w.Code, w.Body.String())
	}

	ev := waitForEvent(t, events, "share_added", 2*time.Second)
	m := eventDataMap(t, ev)
	share, ok := m["share"].(map[string]any)
	if !ok {
		t.Fatalf("share payload missing: %v", m)
	}
	if share["share_type"] != "public" {
		t.Errorf("share_type: %v", share["share_type"])
	}
}

func TestDeleteSharePublishesShareRevoked(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootShareDel")

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	// Share erst anlegen
	body, _ := json.Marshal(map[string]any{"path": "/", "share_type": "public"})
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", strings.NewReader(string(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != 201 {
		t.Fatalf("setup share: %d", w.Code)
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	shareID := created["id"].(string)
	waitForEvent(t, events, "share_added", 2*time.Second)

	r2 := reqWithClaims("DELETE", "/shares/"+shareID, nil, claims)
	r2 = withURLParams(r2, "shareID", shareID)
	w2 := httptest.NewRecorder()
	env.shareH.DeleteShare(w2, r2)

	if w2.Code != 204 {
		t.Fatalf("delete share status: %d body=%s", w2.Code, w2.Body.String())
	}

	ev := waitForEvent(t, events, "share_revoked", 2*time.Second)
	m := eventDataMap(t, ev)
	if m["share_id"] != shareID {
		t.Errorf("share_id: %v want %s", m["share_id"], shareID)
	}
}

func TestDeleteSyncRootPublishesSyncRootDeleted(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootRm")
	// Storage-Root anlegen, damit DeleteSyncRoot ihn entfernen kann.
	_ = env.storage.CreateRootDir(claims.UserID, root.ID.String())

	events, cleanup := captureEvents(t, env.hub, claims.UserID)
	defer cleanup()

	r := reqWithClaims("DELETE", "/sync-roots/"+root.ID.String(), nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()

	env.handler.DeleteSyncRoot(w, r)

	if w.Code != 204 {
		t.Fatalf("delete root status: got %d, body=%s", w.Code, w.Body.String())
	}

	ev := waitForEvent(t, events, "sync_root_deleted", 2*time.Second)
	m := eventDataMap(t, ev)
	if m["sync_root_id"] != root.ID.String() {
		t.Errorf("sync_root_id: %v", m["sync_root_id"])
	}
}
