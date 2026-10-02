package webhost

import (
	"io"
	"net/http"
	"time"
)

const (
	maxRejectedBodyDrain = 64 << 10
	rejectedBodyTimeout  = 250 * time.Millisecond
)

// Track reads from the start of dispatch, including partial reads and errors
// hidden by a later EOF. In particular, net/http's body can return EOF after
// UnexpectedEOF; that must never make an incomplete request reusable.
type requestBody struct {
	io.ReadCloser
	length   int64
	consumed int64
	eof      bool
	readErr  error
	disposed bool
}

func (b *requestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.consumed += int64(n)
	if err == io.EOF {
		b.eof = true
	} else if err != nil && b.readErr == nil {
		b.readErr = err
	}
	return n, err
}

func trackRequestBody(r *http.Request) *requestBody {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	if b, ok := r.Body.(*requestBody); ok {
		return b
	}
	b := &requestBody{ReadCloser: r.Body, length: r.ContentLength}
	r.Body = b
	return b
}

// disposeRejectedBody never parses or acts on discarded bytes. Small known
// bodies are consumed before the error is sent, so closing an HTTP/1 connection
// cannot reset unread bytes over the response (notably on Windows). The bound
// applies to the original Content-Length, not just a partially read remainder.
// Unknown, chunked, Expect/continue, cancelled and stalled bodies are closed
// without an unbounded read. An embedding writer must support read deadlines.
func disposeRejectedBody(w http.ResponseWriter, r *http.Request) {
	b := trackRequestBody(r)
	if b == nil || b.disposed {
		return
	}
	b.disposed = true
	controller := http.NewResponseController(w)
	closeConnection := func() {
		w.Header().Set("Connection", "close")
		_ = controller.SetReadDeadline(time.Now())
	}
	if b.readErr != nil || b.eof && b.length >= 0 && b.consumed < b.length {
		closeConnection()
		return
	}
	if b.eof || b.length >= 0 && b.consumed >= b.length {
		return
	}
	if b.length <= 0 || b.length > maxRejectedBodyDrain || len(r.TransferEncoding) != 0 || r.Header.Get("Expect") != "" || r.Context().Err() != nil {
		closeConnection()
		return
	}
	deadline := time.Now().Add(rejectedBodyTimeout)
	if limit, ok := r.Context().Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := controller.SetReadDeadline(deadline); err != nil {
		closeConnection()
		return
	}
	_, err := io.CopyN(io.Discard, contextReader{r.Context(), b}, b.length-b.consumed)
	if b.readErr != nil || r.Context().Err() != nil || err != nil {
		closeConnection()
		return
	}
	_ = controller.SetReadDeadline(time.Time{})
}

func failRequest(w http.ResponseWriter, r *http.Request, status int, message string) {
	disposeRejectedBody(w, r)
	fail(w, status, message)
}

func rejectRequest(w http.ResponseWriter, r *http.Request, message string) {
	failRequest(w, r, http.StatusForbidden, message)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	disposeRejectedBody(w, r)
	http.NotFound(w, r)
}

// ServeContent chooses its own error status (for example 412 or 416). Intercept
// those errors locally, without pre-reading successful asset/download requests
// or changing ServeContent's content headers and streaming behavior.
type contentResponseWriter struct {
	http.ResponseWriter
	request     *http.Request
	wroteHeader bool
}

func (w *contentResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *contentResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	if status >= 400 {
		disposeRejectedBody(w, w.request)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *contentResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *contentResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(r)
	}
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, r)
}
