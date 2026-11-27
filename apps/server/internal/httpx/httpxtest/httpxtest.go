// Package httpxtest builds test servers that time out like production, for
// tests that prove a transfer outlives the server's timeouts.
package httpxtest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"privasync/server/internal/httpx"
)

// Timeout is the server timeout slow-transfer tests run against. Absolute
// stalls of the test process — scheduler and GC pauses on a loaded CI runner —
// must stay well below it, or the idle timeout fires on the test itself.
const Timeout = time.Second

// Server serves h with both server timeouts set to timeout and wrapped in
// httpx.IdleTimeouts, the way cmd/server wires it.
func Server(t *testing.T, timeout time.Duration, h http.Handler) *httptest.Server {
	t.Helper()
	return Bare(t, timeout, httpx.IdleTimeouts(timeout, timeout, h))
}

// Bare is Server without the middleware — the absolute timeouts alone.
func Bare(t *testing.T, timeout time.Duration, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = timeout
	srv.Config.WriteTimeout = timeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// ReadSlowly drains body at a steady pace: never stalled, but far slower than
// loopback. A 32 MiB body takes about three times Timeout to read.
func ReadSlowly(body io.Reader) ([]byte, error) {
	var out bytes.Buffer
	buf := make([]byte, 256<<10)
	for {
		n, err := body.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return out.Bytes(), err
		}
		time.Sleep(Timeout / 40)
	}
}

// Trickle returns a body that delivers data in pieces with gaps of gap — a
// slow sender that never stalls for long.
func Trickle(data []byte, pieces int, gap time.Duration) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		size := (len(data) + pieces - 1) / pieces
		for len(data) > 0 {
			n := min(size, len(data))
			_, _ = pw.Write(data[:n])
			data = data[n:]
			time.Sleep(gap)
		}
		_ = pw.Close()
	}()
	return pr
}
