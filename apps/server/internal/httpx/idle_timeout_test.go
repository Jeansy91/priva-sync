package httpx_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"privasync/server/internal/httpx/httpxtest"
)

const timeout = httpxtest.Timeout

// trickle writes chunks with gaps below the timeout that add up to well past
// it — a response that is slow, never stalled.
func trickle(w http.ResponseWriter, _ *http.Request) {
	for range 6 {
		_, _ = io.WriteString(w, "chunk\n")
		w.(http.Flusher).Flush()
		time.Sleep(timeout / 3)
	}
}

func get(srv interface{ Client() *http.Client }, url string) (string, error) {
	res, err := srv.Client().Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return string(body), err
}

func TestIdleTimeouts_SlowResponse(t *testing.T) {
	t.Parallel()
	want := strings.Repeat("chunk\n", 6)

	// Guards the test itself: without the middleware the same response is cut.
	bare := httpxtest.Bare(t, timeout, http.HandlerFunc(trickle))
	if body, err := get(bare, bare.URL); err == nil && body == want {
		t.Fatal("slow response arrived complete without the middleware — test proves nothing")
	}

	srv := httpxtest.Server(t, timeout, http.HandlerFunc(trickle))
	body, err := get(srv, srv.URL)
	if err != nil || body != want {
		t.Fatalf("slow response = %q, %v; want %q", body, err, want)
	}
}

// File downloads take the ReadFrom path (sendfile), chunked so the deadline is
// re-armed between chunks.
func TestIdleTimeouts_SlowFileDownload(t *testing.T) {
	t.Parallel()
	content := bytes.Repeat([]byte("0123456789abcdef"), 2<<20) // 32 MiB, several sendChunks
	file := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httpxtest.Server(t, timeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, file)
	}))

	res, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	got, err := httpxtest.ReadSlowly(res.Body)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("got %d of %d bytes, err %v", len(got), len(content), err)
	}

	// A range is served through the same path and must stay exact.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
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

// A client that stops reading must still release the handler: once the socket
// buffers fill, the next write blocks and the deadline set before it fires.
func TestIdleTimeouts_StalledClientIsCut(t *testing.T) {
	t.Parallel()
	writeErr := make(chan error, 1)
	srv := httpxtest.Server(t, timeout, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		buf := make([]byte, 64<<10)
		for range 4096 { // 256 MiB, far beyond any socket buffer
			if _, err := w.Write(buf); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}))

	res, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close() // read nothing: the client has stalled

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("handler wrote everything to a client that never read")
		}
	case <-time.After(20 * timeout):
		t.Fatal("handler still blocked on a stalled client — the idle timeout never fired")
	}
}

// A request body arriving slowly but steadily must be read completely, the
// request context must survive past the read timeout once it is in, and the
// bodyless answer — written by the server after the handler, long past the
// original write deadline — must still reach the client.
func TestIdleTimeouts_SlowRequestBody(t *testing.T) {
	t.Parallel()
	content := []byte(strings.Repeat("slow but complete ", 6))
	srv := httpxtest.Server(t, timeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		time.Sleep(2 * timeout)
		status := http.StatusNoContent
		if err != nil || !bytes.Equal(body, content) {
			status = http.StatusBadRequest
		} else if r.Context().Err() != nil {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
	}))

	req, _ := http.NewRequest(http.MethodPut, srv.URL, httpxtest.Trickle(content, 6, timeout/3))
	req.ContentLength = int64(len(content))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("slow upload lost its answer: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.StatusCode)
	}
}

// The ZIP downloads page rows with the request context while they stream. A
// read deadline left on a bodyless request cancels that context mid-body.
func TestIdleTimeouts_BodylessRequestKeepsContext(t *testing.T) {
	t.Parallel()
	srv := httpxtest.Server(t, timeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * timeout)
		if r.Context().Err() != nil {
			_, _ = io.WriteString(w, "cancelled")
			return
		}
		_, _ = io.WriteString(w, "alive")
	}))

	if body, err := get(srv, srv.URL); err != nil || body != "alive" {
		t.Fatalf("body = %q, %v; want the context to outlive the read timeout", body, err)
	}
}
