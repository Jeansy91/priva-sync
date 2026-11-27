package events_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/sse/events"
)

func TestFileUploadedEnvelopeJSON(t *testing.T) {
	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fileID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	updatedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	mtime := time.Date(2026, 5, 17, 11, 30, 0, 0, time.UTC)

	payload := events.FileUploaded{
		RootID: rootID,
		Path:   "/docs/spec.md",
		File: events.File{
			ID:            fileID,
			Path:          "/docs/spec.md",
			Size:          4096,
			MTime:         mtime,
			ContentHash:   "deadbeef",
			IsDeleted:     false,
			IsDir:         false,
			ServerVersion: 7,
			UpdatedAt:     updatedAt,
		},
	}

	b, err := json.Marshal(events.NewEnvelope(payload))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var got struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Data    struct {
			RootID string `json:"root_id"`
			Path   string `json:"path"`
			File   struct {
				ID            string `json:"id"`
				Size          int64  `json:"size"`
				ContentHash   string `json:"content_hash"`
				IsDir         bool   `json:"is_dir"`
				ServerVersion int64  `json:"server_version"`
				UpdatedAt     string `json:"updated_at"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if got.Type != "file_uploaded" {
		t.Errorf("type = %q, want file_uploaded", got.Type)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	if got.Data.RootID != rootID.String() {
		t.Errorf("root_id = %q", got.Data.RootID)
	}
	if got.Data.Path != "/docs/spec.md" {
		t.Errorf("path = %q", got.Data.Path)
	}
	if got.Data.File.ID != fileID.String() {
		t.Errorf("file.id = %q", got.Data.File.ID)
	}
	if got.Data.File.Size != 4096 {
		t.Errorf("file.size = %d", got.Data.File.Size)
	}
	if got.Data.File.ContentHash != "deadbeef" {
		t.Errorf("file.content_hash = %q", got.Data.File.ContentHash)
	}
	if got.Data.File.ServerVersion != 7 {
		t.Errorf("file.server_version = %d", got.Data.File.ServerVersion)
	}
	if got.Data.File.UpdatedAt != "2026-05-17T12:00:00Z" {
		t.Errorf("updated_at = %q", got.Data.File.UpdatedAt)
	}
}

func TestEnvelopeTypeMatchesPayloadEventName(t *testing.T) {
	for _, p := range []events.Payload{
		events.FileUploaded{},
		events.FileMoved{},
		events.FileDeleted{},
		events.FileRestored{},
		events.FileAdded{},
		events.ConflictAdded{},
		events.ConflictResolved{},
		events.QuotaUpdated{},
		events.QuotaThresholdCrossed{},
		events.TrashAdded{},
		events.TrashEmptied{},
		events.TrashPurged{},
		events.SyncRootAdded{},
		events.SyncRootDeleted{},
		events.SyncRootRenamed{},
		events.ShareAdded{},
		events.ShareRevoked{},
		events.ShareUpdated{},
	} {
		env := events.NewEnvelope(p)
		if env.Type != p.EventName() {
			t.Errorf("%T: envelope type %q drifts from EventName() %q", p, env.Type, p.EventName())
		}
	}
}

func TestShareUpdatedPayload(t *testing.T) {
	shareID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	b, _ := json.Marshal(events.NewEnvelope(events.ShareUpdated{
		Share: events.ShareRef{ID: shareID, Path: "/2024", ShareType: "user", CanWrite: false},
	}))
	var got struct {
		Type string `json:"type"`
		Data struct {
			Share struct {
				ID       string `json:"id"`
				CanWrite bool   `json:"can_write"`
			} `json:"share"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "share_updated" {
		t.Errorf("type = %q, want share_updated", got.Type)
	}
	if got.Data.Share.ID != shareID.String() {
		t.Errorf("share.id = %q", got.Data.Share.ID)
	}
	if got.Data.Share.CanWrite != false {
		t.Errorf("share.can_write = %v, want false", got.Data.Share.CanWrite)
	}
}

func TestQuotaUpdatedWireName(t *testing.T) {
	b, _ := json.Marshal(events.NewEnvelope(events.QuotaUpdated{}))
	if !bytes.Contains(b, []byte(`"type":"quota_updated"`)) {
		t.Errorf("missing wire name: %s", b)
	}
}

func TestQuotaThresholdCrossedPayload(t *testing.T) {
	b, _ := json.Marshal(events.NewEnvelope(events.QuotaThresholdCrossed{
		NewState: "warning80", UsedPct: 82,
	}))
	var got struct {
		Type string `json:"type"`
		Data struct {
			NewState string `json:"new_state"`
			UsedPct  int    `json:"used_pct"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "quota_threshold_crossed" || got.Data.NewState != "warning80" || got.Data.UsedPct != 82 {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestTrashAddedPayload(t *testing.T) {
	root := uuid.New()
	fid := uuid.New()
	b, _ := json.Marshal(events.NewEnvelope(events.TrashAdded{
		RootID: root, FileID: fid, Path: "/x.txt", Size: 42, TrashPath: "/.trash/x.txt",
	}))
	if !bytes.Contains(b, []byte(`"trash_path":"/.trash/x.txt"`)) {
		t.Errorf("trash_path missing: %s", b)
	}
}

func TestFileAddedPayload(t *testing.T) {
	root := uuid.New()
	fid := uuid.New()
	b, _ := json.Marshal(events.NewEnvelope(events.FileAdded{
		RootID: root, Path: "/dir",
		File: events.File{ID: fid, Path: "/dir", IsDir: true},
	}))
	if !bytes.Contains(b, []byte(`"type":"file_added"`)) {
		t.Errorf("type drift: %s", b)
	}
	if !bytes.Contains(b, []byte(`"is_dir":true`)) {
		t.Errorf("is_dir missing: %s", b)
	}
}

func TestConflictAddedEnvelopeJSON(t *testing.T) {
	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fileID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	confID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	created := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	b, err := json.Marshal(events.NewEnvelope(events.ConflictAdded{
		ConflictID:   confID,
		FileID:       fileID,
		RootID:       rootID,
		ConflictPath: "/x.conflict-20260517.txt",
		OriginalPath: "/x.txt",
		CurrentPath:  "/y.txt",
		CreatedAt:    created,
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Type string `json:"type"`
		Data struct {
			ConflictID   string `json:"conflict_id"`
			RootID       string `json:"sync_root_id"`
			ConflictPath string `json:"conflict_path"`
			OriginalPath string `json:"original_path"`
			CurrentPath  string `json:"current_path,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "conflict_added" {
		t.Errorf("type = %q", got.Type)
	}
	if got.Data.ConflictID != confID.String() {
		t.Errorf("conflict_id drift")
	}
	if got.Data.CurrentPath != "/y.txt" {
		t.Errorf("current_path = %q", got.Data.CurrentPath)
	}
}

func TestConflictAddedOmitsEmptyCurrentPath(t *testing.T) {
	b, err := json.Marshal(events.NewEnvelope(events.ConflictAdded{
		ConflictID:   uuid.New(),
		FileID:       uuid.New(),
		RootID:       uuid.New(),
		ConflictPath: "/x.conflict.txt",
		OriginalPath: "/x.txt",
		CreatedAt:    time.Now(),
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte(`"current_path"`)) {
		t.Errorf("current_path leaked when empty: %s", b)
	}
}

func TestFileRestoredEnvelopeJSON(t *testing.T) {
	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fileID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	b, err := json.Marshal(events.NewEnvelope(events.FileRestored{
		RootID: rootID, FileID: fileID, Path: "/x.txt",
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b, &got)
	if got.Type != "file_restored" {
		t.Errorf("type = %q, want file_restored", got.Type)
	}
}

func TestFileDeletedEnvelopeJSON(t *testing.T) {
	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fileID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	b, err := json.Marshal(events.NewEnvelope(events.FileDeleted{
		RootID: rootID, FileID: fileID, Path: "/x.txt",
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Type string `json:"type"`
		Data struct {
			RootID string `json:"sync_root_id"`
			FileID string `json:"file_id"`
			Path   string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "file_deleted" {
		t.Errorf("type = %q", got.Type)
	}
	if got.Data.RootID != rootID.String() || got.Data.FileID != fileID.String() || got.Data.Path != "/x.txt" {
		t.Errorf("payload drift: %+v", got.Data)
	}
}

func TestFileMovedEnvelopeJSON(t *testing.T) {
	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fileID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	mtime := time.Date(2026, 5, 17, 11, 30, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	payload := events.FileMoved{
		RootID: rootID,
		FileID: fileID,
		From:   "/old/path.md",
		To:     "/new/path.md",
		File: events.File{
			ID: fileID, Path: "/new/path.md", Size: 1024, MTime: mtime,
			ContentHash: "cafebabe", ServerVersion: 9, UpdatedAt: updatedAt,
		},
	}
	b, err := json.Marshal(events.NewEnvelope(payload))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Type string `json:"type"`
		Data struct {
			RootID string `json:"sync_root_id"`
			FileID string `json:"file_id"`
			From   string `json:"from"`
			To     string `json:"to"`
			File   struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "file_moved" {
		t.Errorf("type = %q, want file_moved", got.Type)
	}
	if got.Data.RootID != rootID.String() {
		t.Errorf("sync_root_id = %q", got.Data.RootID)
	}
	if got.Data.From != "/old/path.md" || got.Data.To != "/new/path.md" {
		t.Errorf("from/to drift: %q → %q", got.Data.From, got.Data.To)
	}
	if got.Data.File.Path != "/new/path.md" {
		t.Errorf("file.path = %q", got.Data.File.Path)
	}
}
