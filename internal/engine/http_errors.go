package engine

// HTTPResponseTransformError identifies a derived-view failure separately from
// network/status failures. The raw response remains available to the caller.
type HTTPResponseTransformError struct{ Err error }

func (e *HTTPResponseTransformError) Error() string { return e.Err.Error() }
func (e *HTTPResponseTransformError) Unwrap() error { return e.Err }
