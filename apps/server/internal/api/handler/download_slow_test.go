package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/httpx/httpxtest"
)

// A download outlasting the server's WriteTimeout must arrive complete, and a
// range must still be answered exactly (ADR-0072).
func TestDownloadFile_OutlivesServerWriteTimeout(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	content := bytes.Repeat([]byte("0123456789abcdef"), 2<<20) // 32 MiB
	uploadFileAs(t, env, claims, root.ID, "/big.bin", content)

	srv := httpxtest.Server(t, httpxtest.Timeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyClaims, claims))
		r = withURLParams(r, "rootID", root.ID.String())
		env.handler.DownloadFile(w, r)
	}))

	res, err := srv.Client().Get(srv.URL + "/?path=/big.bin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	got, err := httpxtest.ReadSlowly(res.Body)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("slow download: got %d of %d bytes, err %v", len(got), len(content), err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/?path=/big.bin", nil)
	req.Header.Set("Range", "bytes=2-5")
	res, err = srv.Client().Do(req)
	if err != nil {
		t.Fatalf("range get: %v", err)
	}
	defer res.Body.Close()
	if body, _ := io.ReadAll(res.Body); res.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("range = %d %q, want 206 %q", res.StatusCode, body, "2345")
	}
}
