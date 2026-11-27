package httpx

import (
	"bufio"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

// minSendRate is the slowest client a single sendfile chunk still reaches
// within the write timeout.
const minSendRate = 32 << 10 // bytes per second

// IdleTimeouts turns http.Server's absolute ReadTimeout and WriteTimeout into
// idle timeouts: every body read and every write re-arms its deadline, so only
// a stalled peer is cut off, never a long transfer. Once the body is read, or
// if there is none, the read deadline is lifted — otherwise the server's
// background read cancels the request context when it elapses. See ADR-0072.
//
// Must wrap the outermost handler, so every route gets it.
func IdleTimeouts(read, write time.Duration, next http.Handler) http.Handler {
	// One sendfile call re-arms the deadline once per write timeout even for a
	// client at minSendRate: 3.75 MiB at 120s.
	chunk := max(64<<10, int64(write.Seconds()*minSendRate))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A writer without deadline support (test recorders) keeps the server
		// default, so every deadline error in this file is ignored.
		rc := http.NewResponseController(w)
		if r.Body == http.NoBody {
			_ = rc.SetReadDeadline(time.Time{})
		} else {
			r.Body = &idleBody{ReadCloser: r.Body, rc: rc, idle: read}
		}
		iw := &idleWriter{ResponseWriter: w, rc: rc, idle: write, chunk: chunk}
		next.ServeHTTP(iw, r)
		// The server flushes what is still buffered once the handler returns —
		// at least the status line of a bodyless response, after a long upload
		// say. That write needs a fresh deadline too.
		iw.arm()
	})
}

type idleBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	_ = b.rc.SetReadDeadline(time.Now().Add(b.idle))
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

type idleWriter struct {
	http.ResponseWriter
	rc    *http.ResponseController
	idle  time.Duration
	chunk int64
}

func (w *idleWriter) arm() {
	_ = w.rc.SetWriteDeadline(time.Now().Add(w.idle))
}

func (w *idleWriter) Write(p []byte) (int, error) {
	w.arm()
	return w.ResponseWriter.Write(p)
}

// ReadFrom keeps sendfile for file downloads, cut into chunks so the deadline
// is re-armed between them. A caller's LimitedReader is unwrapped first:
// sendfile sees through one level only.
func (w *idleWriter) ReadFrom(src io.Reader) (int64, error) {
	rf, ok := w.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(struct{ io.Writer }{w}, src)
	}
	limit := int64(math.MaxInt64)
	outer, _ := src.(*io.LimitedReader)
	if outer != nil {
		src, limit = outer.R, outer.N
		defer func() { outer.N = limit }()
	}
	var total int64
	for limit > 0 {
		lr := &io.LimitedReader{R: src, N: min(w.chunk, limit)}
		w.arm()
		n, err := rf.ReadFrom(lr)
		total += n
		limit -= n
		if err != nil || lr.N > 0 { // failed, or src ran dry
			return total, err
		}
	}
	return total, nil
}

// Flush and Hijack keep the interfaces visible that callers type-assert for:
// the SSE hub and the ZIP streams want a Flusher, and chi's logger forwards
// ReadFrom only when the writer it wraps is also a Hijacker.
func (w *idleWriter) Flush() {
	w.arm()
	_ = w.rc.Flush()
}

func (w *idleWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.rc.Hijack()
}

// Unwrap lets http.ResponseController reach the connection underneath.
func (w *idleWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
