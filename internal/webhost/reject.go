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

func rejectRequest(w http.ResponseWriter, r *http.Request, message string) {
	rejectUnreadRequest(w, r, http.StatusForbidden, message)
}

// rejectUnreadRequest is only for failures before any request body is read.
// It never decodes or acts on the body. For small, already-declared bodies,
// discard the bytes before sending the error so closing an HTTP/1 connection
// cannot reset the unread body over that response (notably on Windows clients
// using Connection: close).
// Large, chunked, Expect/continue and stalled bodies are closed without waiting
// beyond the fixed deadline. An unsupported embedding writer is not read from.
func rejectUnreadRequest(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
		controller := http.NewResponseController(w)
		bounded := r.ContentLength > 0 && r.ContentLength <= maxRejectedBodyDrain && len(r.TransferEncoding) == 0 && r.Header.Get("Expect") == "" && r.Context().Err() == nil
		if bounded {
			deadline := time.Now().Add(rejectedBodyTimeout)
			if limit, ok := r.Context().Deadline(); ok && limit.Before(deadline) {
				deadline = limit
			}
			if err := controller.SetReadDeadline(deadline); err == nil {
				if _, err = io.CopyN(io.Discard, r.Body, r.ContentLength); err == nil {
					_ = controller.SetReadDeadline(time.Time{})
				} else {
					w.Header().Set("Connection", "close")
					_ = controller.SetReadDeadline(time.Now())
				}
			} else {
				w.Header().Set("Connection", "close")
			}
		} else {
			w.Header().Set("Connection", "close")
			_ = controller.SetReadDeadline(time.Now())
		}
	}
	fail(w, status, message)
}
