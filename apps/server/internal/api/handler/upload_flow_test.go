package handler

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"lukechampine.com/blake3"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/auth"
	"privasync/server/internal/httpx/httpxtest"
	"privasync/server/internal/model"
)

func initUpload(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string, totalSize int) uuid.UUID {
	t.Helper()
	body := bytes.NewBufferString(`{"path":"` + path + `","total_size":` + strconv.Itoa(totalSize) + `}`)
	r := reqWithClaims("POST", "/sync-roots/"+rootID.String()+"/uploads", body, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)
	if w.Code != 201 {
		t.Fatalf("InitUpload status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		UploadID uuid.UUID `json:"upload_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp.UploadID
}

func TestUploadFlow_InitChunkStatusComplete(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("hello upload world")
	uploadID := initUpload(t, env, claims, root.ID, "/greet.txt", len(content))

	chunkReq := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/chunks/0",
		bytes.NewReader(content), claims)
	chunkReq = withURLParams(chunkReq, "rootID", root.ID.String(), "uploadID", uploadID.String(), "offset", "0")
	chunkW := httptest.NewRecorder()
	env.uploadH.UploadChunk(chunkW, chunkReq)
	if chunkW.Code != 200 {
		t.Fatalf("UploadChunk status = %d, want 200; body=%s", chunkW.Code, chunkW.Body.String())
	}

	statusReq := reqWithClaims("GET", "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/status", nil, claims)
	statusReq = withURLParams(statusReq, "uploadID", uploadID.String())
	statusW := httptest.NewRecorder()
	env.uploadH.UploadStatus(statusW, statusReq)
	if statusW.Code != 200 {
		t.Fatalf("UploadStatus status = %d, want 200; body=%s", statusW.Code, statusW.Body.String())
	}
	var status struct {
		MissingRanges [][2]int64 `json:"missing_ranges"`
	}
	_ = json.Unmarshal(statusW.Body.Bytes(), &status)
	if len(status.MissingRanges) != 0 {
		t.Fatalf("missing_ranges = %v, want none after uploading the only chunk", status.MissingRanges)
	}

	completeW := completeUpload(env, claims, root, uploadID, "")
	if completeW.Code != 200 && completeW.Code != 201 {
		t.Fatalf("CompleteUpload status = %d, want 200/201; body=%s", completeW.Code, completeW.Body.String())
	}

	f, err := env.files.FindByPath(context.Background(), root.ID, "/greet.txt")
	if err != nil {
		t.Fatalf("FindByPath: %v", err)
	}
	if f.Size != int64(len(content)) {
		t.Fatalf("stored file size = %d, want %d", f.Size, len(content))
	}
}

func TestUploadChunk_OffsetExceedingTotalSizeRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadID := initUpload(t, env, claims, root.ID, "/f.txt", 5)

	r := reqWithClaims("PUT", "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/chunks/99",
		bytes.NewReader([]byte("x")), claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String(), "offset", "99")
	w := httptest.NewRecorder()
	env.uploadH.UploadChunk(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestCompleteUpload_MissingChunksRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadID := initUpload(t, env, claims, root.ID, "/f.txt", 5)
	// No chunk uploaded at all.

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/complete", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String())
	w := httptest.NewRecorder()
	env.uploadH.CompleteUpload(w, r)

	if w.Code != 409 {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestInitUpload_InvalidBodyRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/uploads", bytes.NewBufferString(`{}`), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestUploadStatus_UnknownSessionNotFound(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims("GET", "/sync-roots/x/uploads/"+uuid.NewString()+"/status", nil, claims)
	r = withURLParams(r, "uploadID", uuid.NewString())
	w := httptest.NewRecorder()
	env.uploadH.UploadStatus(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// A chunk arriving slowly but steadily must outlive the server's ReadTimeout —
// a 5 MiB chunk against an absolute 30s deadline demands roughly 1.4 Mbit/s
// just to arrive (ADR-0072).
func TestUploadChunk_OutlivesServerReadTimeout(t *testing.T) {
	const readTimeout = 300 * time.Millisecond

	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("slow but complete")
	uploadID := initUpload(t, env, claims, root.ID, "/slow.bin", len(content))

	srv := httpxtest.Server(t, readTimeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyClaims, claims))
		r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String(), "offset", "0")
		env.uploadH.UploadChunk(w, r)
	}))

	// Gaps below the timeout that add up to well past it.
	req, err := http.NewRequest(http.MethodPut, srv.URL, httpxtest.Trickle(content, 6, readTimeout/3))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	// A real chunk upload always declares its length upfront (the client already
	// has the bytes in hand before it starts sending) — set it explicitly here
	// since http.NewRequest cannot infer one from an io.Pipe reader, and without
	// it Go sends the request chunked-transfer-encoded with no Content-Length at all.
	req.ContentLength = int64(len(content))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("slow chunk upload failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, want 200; body=%s", res.StatusCode, body)
	}
}

// initUploadIfMatch starts a chunked session that opts into the optimistic
// version check. The complete call carries no body, so init is the only place
// a chunked client can state the version it believes it is overwriting.
func initUploadIfMatch(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string, totalSize int, ifMatch int64) uuid.UUID {
	t.Helper()
	body := bytes.NewBufferString(`{"path":"` + path + `","total_size":` + strconv.Itoa(totalSize) +
		`,"if_match":` + strconv.FormatInt(ifMatch, 10) + `}`)
	r := reqWithClaims("POST", "/sync-roots/"+rootID.String()+"/uploads", body, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)
	if w.Code != 201 {
		t.Fatalf("InitUpload status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		UploadID uuid.UUID `json:"upload_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp.UploadID
}

// putChunkAndComplete uploads a single-chunk body and finishes the session
// without an If-Match header — exactly what the desktop client does.
func putChunkAndComplete(t *testing.T, env *testEnv, claims *auth.Claims, rootID, uploadID uuid.UUID, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	chunkReq := reqWithClaims("PUT", "/chunks/0", bytes.NewReader(content), claims)
	chunkReq = withURLParams(chunkReq, "rootID", rootID.String(), "uploadID", uploadID.String(), "offset", "0")
	chunkW := httptest.NewRecorder()
	env.uploadH.UploadChunk(chunkW, chunkReq)
	if chunkW.Code != 200 {
		t.Fatalf("UploadChunk status = %d; body=%s", chunkW.Code, chunkW.Body.String())
	}

	// Complete only accepts the work. The test environment assembles
	// inline, so a second call returns the verdict the first one set in motion.
	completeW := postComplete(env, claims, &model.SyncRoot{ID: rootID}, uploadID, "")
	if completeW.Code == http.StatusAccepted {
		completeW = postComplete(env, claims, &model.SyncRoot{ID: rootID}, uploadID, "")
	}
	return completeW
}

// Updating an existing file through the chunked path must not be treated as a
// blind write. Without the session carrying if_match, every such update takes
// the real-conflict path: a full loser copy on disk plus a 409 that sends the
// client off to download what it just uploaded — ruinous for the multi-GB
// files that reach the chunked path in the first place.
func TestChunkedUpload_IfMatchFromInitAvoidsConflict(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadHelper(t, env, claims, root, "/mod.pak", "version one", nil)
	base, err := env.files.FindByPath(context.Background(), root.ID, "/mod.pak")
	if err != nil {
		t.Fatalf("find base: %v", err)
	}

	content := []byte("version two")
	uploadID := initUploadIfMatch(t, env, claims, root.ID, "/mod.pak", len(content), base.ServerVersion)
	w := putChunkAndComplete(t, env, claims, root.ID, uploadID, content)

	if w.Code != 200 {
		t.Fatalf("CompleteUpload status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	got, err := env.files.FindByPath(context.Background(), root.ID, "/mod.pak")
	if err != nil {
		t.Fatalf("find after: %v", err)
	}
	if got.ServerVersion <= base.ServerVersion {
		t.Errorf("server_version not bumped: %d -> %d", base.ServerVersion, got.ServerVersion)
	}
	if got.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", got.Size, len(content))
	}
}

// The optimistic check must still bite: an if_match that disagrees with the
// current server_version is a genuine conflict and stays one.
func TestChunkedUpload_StaleIfMatchStillConflicts(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadHelper(t, env, claims, root, "/mod.pak", "version one", nil)
	base, err := env.files.FindByPath(context.Background(), root.ID, "/mod.pak")
	if err != nil {
		t.Fatalf("find base: %v", err)
	}

	content := []byte("version two")
	uploadID := initUploadIfMatch(t, env, claims, root.ID, "/mod.pak", len(content), base.ServerVersion+1)
	w := putChunkAndComplete(t, env, claims, root.ID, uploadID, content)

	if w.Code != 409 {
		t.Fatalf("CompleteUpload status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	// A version mismatch is the client losing a race it opted into: the
	// staging file is dropped and no loser copy is materialised. A blind
	// write would instead answer with conflict_path — which is exactly how
	// this path used to be misclassified.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["conflict_path"]; ok {
		t.Errorf("stale If-Match produced a loser copy: %v", resp)
	}
}

// A resuming client reads missing_ranges to know what is still left to send.
// Two chunks of different sizes leave a gap in the middle — the two received
// edges must not appear as missing, and the interior gap must appear as
// exactly one byte range (ADR-0066).
func TestUploadStatus_ReportsMissingRangesAfterAGapInTheMiddle(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	const totalSize = 100
	uploadID := initUpload(t, env, claims, root.ID, "/uneven.bin", totalSize)

	// [0,10) and [40,100) received — variable chunk sizes, unlike each other.
	if w := putChunk(env, claims, root, uploadID, "0", bytes.Repeat([]byte("a"), 10)); w.Code != http.StatusOK {
		t.Fatalf("first chunk status = %d; body=%s", w.Code, w.Body.String())
	}
	if w := putChunk(env, claims, root, uploadID, "40", bytes.Repeat([]byte("b"), 60)); w.Code != http.StatusOK {
		t.Fatalf("second chunk status = %d; body=%s", w.Code, w.Body.String())
	}

	r := reqWithClaims("GET", "/status", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String())
	w := httptest.NewRecorder()
	env.uploadH.UploadStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		MissingRanges [][2]int64 `json:"missing_ranges"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := [][2]int64{{10, 40}}
	if !reflect.DeepEqual(resp.MissingRanges, want) {
		t.Errorf("missing_ranges = %v, want %v", resp.MissingRanges, want)
	}
}

// A chunk that arrives shorter than its declared Content-Length means the
// transfer was cut short. Accepting it stores a hole that only surfaces as a
// silently truncated file — one the server then hashes itself, so the stored
// hash agrees with the corruption and nothing downstream notices.
func TestUploadChunk_RejectsBodyShorterThanDeclaredContentLength(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadID := initUpload(t, env, claims, root.ID, "/two-chunk.bin", 1000)

	short := bytes.Repeat([]byte("x"), 100)
	r := reqWithClaims("PUT", "/chunks/0", bytes.NewReader(short), claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String(), "offset", "0")
	r.ContentLength = 500 // declares more than the 100 bytes the body actually carries
	w := httptest.NewRecorder()
	env.uploadH.UploadChunk(w, r)

	if w.Code == 200 {
		t.Fatalf("a 100-byte body was accepted where 500 bytes were declared")
	}
	if w.Code != 400 {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrChunkLengthMismatch)
}

// Last line of defence: every chunk arrived at the right size, but one was
// damaged on disk before assembly. The server hashes whatever it assembled,
// so without a size check the truncation would be committed together with a
// hash that agrees with it — corruption that looks healthy forever after.
func TestCompleteUpload_RejectsAssemblyShorterThanTheSession(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := bytes.Repeat([]byte("a"), 100)
	uploadID := initUpload(t, env, claims, root.ID, "/damaged.bin", len(content))

	chunkReq := reqWithClaims("PUT", "/chunks/0", bytes.NewReader(content), claims)
	chunkReq = withURLParams(chunkReq, "rootID", root.ID.String(), "uploadID", uploadID.String(), "offset", "0")
	chunkW := httptest.NewRecorder()
	env.uploadH.UploadChunk(chunkW, chunkReq)
	if chunkW.Code != 200 {
		t.Fatalf("UploadChunk status = %d; body=%s", chunkW.Code, chunkW.Body.String())
	}

	// Damage the staged bytes behind the API's back.
	stagingPath := filepath.Join(env.storDir, ".chunks", uploadID.String(), "data")
	if err := os.Truncate(stagingPath, 10); err != nil {
		t.Fatalf("truncate staging file: %v", err)
	}

	completeReq := reqWithClaims("POST", "/complete", nil, claims)
	completeReq = withURLParams(completeReq, "rootID", root.ID.String(), "uploadID", uploadID.String())
	w := httptest.NewRecorder()
	env.uploadH.CompleteUpload(w, completeReq)

	if w.Code == 200 {
		t.Fatalf("a truncated assembly was committed; body=%s", w.Body.String())
	}
	if _, err := env.files.FindByPath(context.Background(), root.ID, "/damaged.bin"); err == nil {
		t.Error("the truncated file was committed anyway")
	}
}

// A session is opened for a size the user cannot possibly store. Letting it
// through means the client pushes the whole file across the wire and onto the
// disk before the commit-time quota check finally rejects it — and until the
// session expires a day later, those chunks occupy space the user does not have.
func TestInitUpload_RejectsWhatDoesNotFitTheQuota(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	if err := env.users.SetQuota(context.Background(), claims.UserID, 1024); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	body := bytes.NewBufferString(`{"path":"/huge.bin","total_size":1048576}`)
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/uploads", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)

	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrQuotaExceeded)
}

// Quota is an accounting limit; the disk is a physical one. A server whose
// volume cannot hold the announced size should say so at init rather than
// fill up and fail somewhere in the middle.
func TestInitUpload_RejectsWhatDoesNotFitTheDisk(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	env.uploadH.DiskUsage = func(string) (free, total int64, err error) { return 500, 1000, nil }

	body := bytes.NewBufferString(`{"path":"/huge.bin","total_size":100000}`)
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/uploads", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)

	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrDiskFull)
}

// The everyday case must stay unaffected: enough quota, enough disk.
func TestInitUpload_AcceptsWhatFits(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	if err := env.users.SetQuota(context.Background(), claims.UserID, 10*1024*1024); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	env.uploadH.DiskUsage = func(string) (free, total int64, err error) { return 1 << 40, 1 << 41, nil }

	uploadID := initUpload(t, env, claims, root.ID, "/fits.bin", 1024)
	if uploadID == (uuid.UUID{}) {
		t.Error("no session was opened for a file that fits")
	}
}

// A repeated complete must answer with what the first one decided instead of
// assembling the file again. The client retries for up to twelve hours, and
// every retry of a multi-gigabyte session re-reads and re-hashes the whole
// thing — when the chunks are even still there, which after the first
// successful complete they are not.
func TestCompleteUpload_ReplaysTheStoredResultOnRetry(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("finished once")
	uploadID := initUpload(t, env, claims, root.ID, "/once.bin", len(content))
	first := putChunkAndComplete(t, env, claims, root.ID, uploadID, content)
	if first.Code != 200 {
		t.Fatalf("first complete = %d; body=%s", first.Code, first.Body.String())
	}
	var firstBody struct {
		ServerVersion int64  `json:"server_version"`
		ContentHash   string `json:"content_hash"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	second := postComplete(env, claims, root, uploadID, "")

	if second.Code != 200 {
		t.Fatalf("second complete = %d, want 200; body=%s", second.Code, second.Body.String())
	}
	var secondBody struct {
		ServerVersion int64  `json:"server_version"`
		ContentHash   string `json:"content_hash"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if secondBody.ServerVersion != firstBody.ServerVersion {
		t.Errorf("server_version = %d, want the first answer's %d", secondBody.ServerVersion, firstBody.ServerVersion)
	}
	if secondBody.ContentHash != firstBody.ContentHash {
		t.Errorf("content_hash = %q, want %q", secondBody.ContentHash, firstBody.ContentHash)
	}
}

// A terminal failure is a decision too, and repeating it must not repeat the
// work. The chunks that produced the wrong hash are still the chunks that
// produce it.
func TestCompleteUpload_ReplaysAStoredFailure(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("real content")
	wrong := blake3.Sum256([]byte("not the content"))
	body := `{"path":"/mismatch.bin","total_size":` + strconv.Itoa(len(content)) +
		`,"content_hash":"` + hex.EncodeToString(wrong[:]) + `"}`
	uploadID := initUploadRaw(t, env, claims, root, body)

	if w := putChunk(env, claims, root, uploadID, "0", content); w.Code != http.StatusOK {
		t.Fatalf("chunk status = %d; body=%s", w.Code, w.Body.String())
	}
	first := completeUpload(env, claims, root, uploadID, "")
	if first.Code != http.StatusUnprocessableEntity {
		t.Fatalf("first complete = %d, want 422; body=%s", first.Code, first.Body.String())
	}

	second := completeUpload(env, claims, root, uploadID, "")
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second complete = %d, want 422; body=%s", second.Code, second.Body.String())
	}
	assertCode(t, second, ErrContentHashMismatch)
}
