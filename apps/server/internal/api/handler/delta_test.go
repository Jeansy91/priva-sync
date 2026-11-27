package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"lukechampine.com/blake3"

	"privasync/server/internal/auth"
	"privasync/server/internal/delta"
	"privasync/server/internal/model"
)

// Slice 7 — delta-request on an existing file larger than the threshold returns
// the block signatures so the client can diff locally.
func TestDeltaRequest_LargeFileReturnsSignatures(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	// Just over one block so we get two signatures.
	content := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(content), nil)

	r := reqWithClaims("GET", "/sync-roots/"+root.ID.String()+"/files/delta?path=/big.bin", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.deltaH.DeltaRequest(w, r)

	if w.Code != 200 {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		DeltaAvailable bool              `json:"delta_available"`
		BlockSize      int64             `json:"block_size"`
		Signatures     []delta.Signature `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.DeltaAvailable {
		t.Fatal("want delta_available=true")
	}
	if resp.BlockSize != delta.BlockSize {
		t.Fatalf("block_size: got %d want %d", resp.BlockSize, delta.BlockSize)
	}
	if len(resp.Signatures) != 2 {
		t.Fatalf("want 2 signatures, got %d", len(resp.Signatures))
	}
	if resp.Signatures[1].Size != 512 {
		t.Fatalf("trailing block size: got %d want 512", resp.Signatures[1].Size)
	}
}

// decodeCachedSigs unmarshals a file row's block_hashes JSONB into signatures.
func decodeCachedSigs(t *testing.T, raw []byte) []delta.Signature {
	t.Helper()
	if raw == nil {
		t.Fatal("block_hashes is NULL, want cached signatures")
	}
	var sigs []delta.Signature
	if err := json.Unmarshal(raw, &sigs); err != nil {
		t.Fatalf("decode block_hashes: %v", err)
	}
	return sigs
}

// onTheFlySigs computes the signatures the server would produce by hashing
// content directly — the oracle the cache must match.
func onTheFlySigs(t *testing.T, content []byte) []delta.Signature {
	t.Helper()
	sigs, err := delta.BlockSignatures(bytes.NewReader(content), delta.BlockSize)
	if err != nil {
		t.Fatalf("on-the-fly sigs: %v", err)
	}
	return sigs
}

// assertSigsEqual fails unless got holds exactly the same block signatures as
// want (offset, size, weak and strong hash per block).
func assertSigsEqual(t *testing.T, got, want []delta.Signature) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("signature count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Weak != want[i].Weak || !bytes.Equal(got[i].Strong, want[i].Strong) ||
			got[i].Offset != want[i].Offset || got[i].Size != want[i].Size {
			t.Fatalf("sig %d mismatch: got %+v want %+v", i, got[i], want[i])
		}
	}
}

// Uploading a file above the delta threshold persists
// its block signatures into files.block_hashes, matching on-the-fly hashing.
func TestUpload_PopulatesBlockHashesCache(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	content := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(content), nil)

	f, err := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	assertSigsEqual(t, decodeCachedSigs(t, f.BlockHashes), onTheFlySigs(t, content))
}

// requestSigs runs delta-request and returns the served signatures.
func requestSigs(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path string) []delta.Signature {
	t.Helper()
	r := reqWithClaims("GET", "/sync-roots/"+root.ID.String()+"/files/delta?path="+path, nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.deltaH.DeltaRequest(w, r)
	if w.Code != 200 {
		t.Fatalf("delta-request status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		DeltaAvailable bool              `json:"delta_available"`
		Signatures     []delta.Signature `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.DeltaAvailable {
		t.Fatal("want delta_available=true")
	}
	return resp.Signatures
}

// Delta-request serves signatures straight from the cache: even
// with the on-disk bytes gone, the cached row answers without re-hashing.
func TestDeltaRequest_ServesFromCacheWithoutReadingFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	content := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(content), nil)

	// Remove the on-disk file: a cache hit must not need it.
	if err := env.storage.DeleteFile(claims.UserID, root.ID.String(), "/big.bin"); err != nil {
		t.Fatalf("delete on-disk file: %v", err)
	}

	assertSigsEqual(t, requestSigs(t, env, claims, root, "/big.bin"), onTheFlySigs(t, content))
}

// A NULL cache (pre-migration row) makes delta-request fall
// back to on-the-fly hashing of the still-present file, returning correct sigs.
func TestDeltaRequest_FallsBackWhenCacheNull(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	content := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(content), nil)

	// Simulate a pre-cache row: clear the cache, keep the on-disk bytes.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE files SET block_hashes = NULL WHERE sync_root_id = $1 AND path = $2`,
		root.ID, "/big.bin"); err != nil {
		t.Fatalf("null cache: %v", err)
	}

	assertSigsEqual(t, requestSigs(t, env, claims, root, "/big.bin"), onTheFlySigs(t, content))
}

func deltaAvailable(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	var resp struct {
		DeltaAvailable bool `json:"delta_available"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return resp.DeltaAvailable
}

// Slice 8 — a file at/below the threshold is cheaper to send whole, so the
// server signals a full upload.
func TestDeltaRequest_SmallFileSignalsFullUpload(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	uploadHelper(t, env, claims, root, "/small.txt", "tiny", nil)

	r := reqWithClaims("GET", "/sync-roots/"+root.ID.String()+"/files/delta?path=/small.txt", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.deltaH.DeltaRequest(w, r)

	if w.Code != 200 {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	if deltaAvailable(t, w) {
		t.Fatal("want delta_available=false for small file")
	}
}

// Slice 9 — no server version for the path: full upload.
func TestDeltaRequest_MissingFileSignalsFullUpload(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	r := reqWithClaims("GET", "/sync-roots/"+root.ID.String()+"/files/delta?path=/nope.bin", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.deltaH.DeltaRequest(w, r)

	if w.Code != 200 {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	if deltaAvailable(t, w) {
		t.Fatal("want delta_available=false for missing file")
	}
}

// Slice 10 — a root the caller does not own is indistinguishable from a
// missing root: 404, never another user's signatures.
func TestDeltaRequest_ForeignRootIs404(t *testing.T) {
	env := newTestEnv(t)
	owner := createTestUser(t, env)
	root := createTestSyncRoot(t, env, owner.UserID, "root1")
	uploadHelper(t, env, owner, root, "/big.bin", "whatever", nil)

	intruder := createTestUser(t, env)
	r := reqWithClaims("GET", "/sync-roots/"+root.ID.String()+"/files/delta?path=/big.bin", nil, intruder)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.deltaH.DeltaRequest(w, r)

	if w.Code != 404 {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

// applyBody builds a delta-apply request body. ops is raw JSON for the ops array.
func applyBody(path, expectedHash, opsJSON string) string {
	return `{"path":"` + path + `","expected_hash":"` + expectedHash + `","ops":` + opsJSON + `}`
}

// Slice 11 — apply reconstructs the file from copy+data ops, replacing the
// server version and bumping server_version.
func TestDeltaApply_ReconstructsAndBumpsVersion(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	old := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(old), nil)

	base, err := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if err != nil {
		t.Fatalf("find base: %v", err)
	}

	// New file = whole old file + appended literal bytes.
	appended := []byte("APPENDED")
	newContent := append(append([]byte(nil), old...), appended...)
	wantHash := blake3.Sum256(newContent)

	opsJSON := `[{"copy":{"offset":0,"length":` + strconv.Itoa(len(old)) + `}},` +
		`{"data":"` + base64.StdEncoding.EncodeToString(appended) + `"}]`
	body := applyBody("/big.bin", hex.EncodeToString(wantHash[:]), opsJSON)

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files/delta/apply", bytes.NewReader([]byte(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	r.Header.Set("If-Match", strconv.FormatInt(base.ServerVersion, 10))
	w := httptest.NewRecorder()
	env.deltaH.DeltaApply(w, r)

	if w.Code != 200 {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	got, err := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if err != nil {
		t.Fatalf("find after: %v", err)
	}
	if got.ServerVersion <= base.ServerVersion {
		t.Fatalf("server_version not bumped: %d -> %d", base.ServerVersion, got.ServerVersion)
	}
	if got.Size != int64(len(newContent)) {
		t.Fatalf("size: got %d want %d", got.Size, len(newContent))
	}
	if !bytes.Equal(got.ContentHash, wantHash[:]) {
		t.Fatalf("content hash mismatch")
	}
}

// Delta-apply commits the reconstructed file through the
// coordinator, so block_hashes is refreshed to the new content's signatures and
// never keeps the pre-apply (now stale) cache.
func TestDeltaApply_RefreshesBlockHashesCache(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	old := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(old), nil)
	base, err := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if err != nil {
		t.Fatalf("find base: %v", err)
	}
	staleCache := decodeCachedSigs(t, base.BlockHashes) // cache of the old content

	appended := []byte("APPENDED")
	newContent := append(append([]byte(nil), old...), appended...)
	wantHash := blake3.Sum256(newContent)
	opsJSON := `[{"copy":{"offset":0,"length":` + strconv.Itoa(len(old)) + `}},` +
		`{"data":"` + base64.StdEncoding.EncodeToString(appended) + `"}]`
	body := applyBody("/big.bin", hex.EncodeToString(wantHash[:]), opsJSON)

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files/delta/apply", bytes.NewReader([]byte(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	r.Header.Set("If-Match", strconv.FormatInt(base.ServerVersion, 10))
	w := httptest.NewRecorder()
	env.deltaH.DeltaApply(w, r)
	if w.Code != 200 {
		t.Fatalf("apply status: got %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	after, err := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if err != nil {
		t.Fatalf("find after: %v", err)
	}
	got := decodeCachedSigs(t, after.BlockHashes)
	assertSigsEqual(t, got, onTheFlySigs(t, newContent))
	// The trailing block grew by the appended bytes — the cache must reflect it,
	// not the stale pre-apply signature.
	if got[len(got)-1].Size == staleCache[len(staleCache)-1].Size {
		t.Fatal("cache still holds stale trailing-block size")
	}
}

// Copying a file carries its block_hashes cache verbatim: the
// bytes are identical, so the copy's signatures must already be valid.
func TestCopyFile_PreservesBlockHashesCache(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	content := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, "/big.bin", string(content), nil)
	src, err := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if err != nil {
		t.Fatalf("find src: %v", err)
	}

	copied, err := env.files.CopyFile(context.Background(), root.ID, "/big.bin", "/copy.bin", claims.UserID)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if !bytes.Equal(copied.BlockHashes, src.BlockHashes) {
		t.Fatal("copy did not carry block_hashes cache")
	}
	if len(decodeCachedSigs(t, copied.BlockHashes)) == 0 {
		t.Fatal("copied cache is empty")
	}
}

// A file at/below the delta threshold never gets a cache: it is
// never offered a delta, so hashing it would be wasted work.
func TestUpload_SmallFileLeavesCacheNull(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	uploadHelper(t, env, claims, root, "/tiny.txt", "tiny", nil)
	f, err := env.files.FindByPath(context.Background(), root.ID, "/tiny.txt")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if f.BlockHashes != nil {
		t.Fatalf("small file got a cache: %s", string(f.BlockHashes))
	}
}

// seedBigFile uploads a >threshold file and returns its base row.
func seedBigFile(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path string) ([]byte, *model.File) {
	t.Helper()
	old := bytes.Repeat([]byte("x"), int(delta.BlockSize)+512)
	uploadHelper(t, env, claims, root, path, string(old), nil)
	base, err := env.files.FindByPath(context.Background(), root.ID, path)
	if err != nil {
		t.Fatalf("find base: %v", err)
	}
	return old, base
}

// Slice 12 — a plan whose reconstructed bytes do not hash to expected_hash is
// rejected and never replaces the server file.
func TestDeltaApply_HashMismatchRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	old, base := seedBigFile(t, env, claims, root, "/big.bin")

	opsJSON := `[{"copy":{"offset":0,"length":` + strconv.Itoa(len(old)) + `}}]`
	bogus := blake3.Sum256([]byte("not the real content"))
	body := applyBody("/big.bin", hex.EncodeToString(bogus[:]), opsJSON)

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files/delta/apply", bytes.NewReader([]byte(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	r.Header.Set("If-Match", strconv.FormatInt(base.ServerVersion, 10))
	w := httptest.NewRecorder()
	env.deltaH.DeltaApply(w, r)

	if w.Code != 422 {
		t.Fatalf("status: got %d want 422, body=%s", w.Code, w.Body.String())
	}
	after, _ := env.files.FindByPath(context.Background(), root.ID, "/big.bin")
	if after.ServerVersion != base.ServerVersion {
		t.Fatalf("server file changed despite bad hash")
	}
}

// Slice 13 — an If-Match that disagrees with the current server_version loses
// the optimistic-lock race: 409, routed through the coordinator's
// version-conflict path.
func TestDeltaApply_StaleIfMatchConflicts(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	old, base := seedBigFile(t, env, claims, root, "/big.bin")

	appended := []byte("APPENDED")
	newContent := append(append([]byte(nil), old...), appended...)
	wantHash := blake3.Sum256(newContent)
	opsJSON := `[{"copy":{"offset":0,"length":` + strconv.Itoa(len(old)) + `}},` +
		`{"data":"` + base64.StdEncoding.EncodeToString(appended) + `"}]`
	body := applyBody("/big.bin", hex.EncodeToString(wantHash[:]), opsJSON)

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files/delta/apply", bytes.NewReader([]byte(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	r.Header.Set("If-Match", strconv.FormatInt(base.ServerVersion+1, 10)) // disagrees with current
	w := httptest.NewRecorder()
	env.deltaH.DeltaApply(w, r)

	if w.Code != 409 {
		t.Fatalf("status: got %d want 409, body=%s", w.Code, w.Body.String())
	}
}

// Slice 14 — a copy op that runs past the end of the server file fails
// reconstruction with 422, not a truncated commit.
func TestDeltaApply_CopyOutOfBoundsRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	old, base := seedBigFile(t, env, claims, root, "/big.bin")

	// Copy more bytes than the file holds.
	opsJSON := `[{"copy":{"offset":0,"length":` + strconv.Itoa(len(old)+10_000) + `}}]`
	wantHash := blake3.Sum256(old) // irrelevant; reconstruction fails first
	body := applyBody("/big.bin", hex.EncodeToString(wantHash[:]), opsJSON)

	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files/delta/apply", bytes.NewReader([]byte(body)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	r.Header.Set("If-Match", strconv.FormatInt(base.ServerVersion, 10))
	w := httptest.NewRecorder()
	env.deltaH.DeltaApply(w, r)

	if w.Code != 422 {
		t.Fatalf("status: got %d want 422, body=%s", w.Code, w.Body.String())
	}
}

// The chunked path now derives the block signatures from the
// same read that hashes the file, and its chunks land at their offsets in one
// staging file. Uploading in reverse order and comparing against a separate
// on-the-fly hashing run pins both: same bytes, same signatures.
func TestChunkedUpload_PopulatesBlockHashesCacheInReverseOrder(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	// Just over one 5 MiB chunk: enough for the delta threshold, without
	// paying for bytes the assertion does not need.
	content := bytes.Repeat([]byte("delta-sync"), (5<<20)/10+1)
	uploadID := initUpload(t, env, claims, root.ID, "/big.pak", len(content))

	// The test picks its own chunk size (ADR-0066: the client owns this, the
	// server has no session-wide constant any more) — small enough to force
	// several chunks so the reverse-order upload actually exercises out-of-order arrival.
	const testChunkSize = 2 << 20 // 2 MiB
	totalSize := int64(len(content))
	totalChunks := int((totalSize + testChunkSize - 1) / testChunkSize)
	if totalChunks < 2 {
		t.Fatalf("totalChunks = %d, want a multi-chunk upload", totalChunks)
	}

	for i := totalChunks - 1; i >= 0; i-- {
		start := int64(i) * testChunkSize
		end := min(start+testChunkSize, totalSize)
		r := reqWithClaims("PUT", "/chunks", bytes.NewReader(content[start:end]), claims)
		r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String(),
			"offset", strconv.FormatInt(start, 10))
		w := httptest.NewRecorder()
		env.uploadH.UploadChunk(w, r)
		if w.Code != 200 {
			t.Fatalf("UploadChunk(offset=%d) status = %d; body=%s", start, w.Code, w.Body.String())
		}
	}

	if w := completeUpload(env, claims, root, uploadID, ""); w.Code != 200 && w.Code != 201 {
		t.Fatalf("CompleteUpload status = %d; body=%s", w.Code, w.Body.String())
	}

	f, err := env.files.FindByPath(context.Background(), root.ID, "/big.pak")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if f.Size != int64(len(content)) {
		t.Fatalf("stored size = %d, want %d", f.Size, len(content))
	}
	assertSigsEqual(t, decodeCachedSigs(t, f.BlockHashes), onTheFlySigs(t, content))
}
