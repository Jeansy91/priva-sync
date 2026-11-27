package webdav_test

import (
	"bytes"
	"net/http"
	"testing"

	"privasync/server/internal/httpx/httpxtest"
)

// A WebDAV GET outlasting the server's WriteTimeout must arrive complete
// (ADR-0072).
func TestGet_OutlivesServerWriteTimeout(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	content := bytes.Repeat([]byte("0123456789abcdef"), 2<<20) // 32 MiB
	r.putFile(t, u, root, "/big.bin", content)

	srv := httpxtest.Server(t, httpxtest.Timeout, r.handler)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/Docs/big.bin", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.SetBasicAuth(u.Username, r.davSecret(t, u))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got, err := httpxtest.ReadSlowly(res.Body)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("slow GET: got %d of %d bytes, err %v", len(got), len(content), err)
	}
}
