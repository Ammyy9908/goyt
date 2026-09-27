package goyt

import (
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorInvalidURL     ErrorKind = "invalid_url"
	ErrorUnsupportedURL ErrorKind = "unsupported_url"
	ErrorExtraction     ErrorKind = "extraction_failed"
	ErrorInvalidResult  ErrorKind = "invalid_result"
)

// ErrDownloadStalled indicates network inactivity during a media transfer exceeded the stall timeout.
var ErrDownloadStalled = errors.New("goyt: download stalled")

// HTTPStatusError represents an unexpected HTTP response status from a media server or playlist.
type HTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("goyt: unexpected HTTP status %d", e.StatusCode)
}

// IsRefreshTrigger reports whether an error indicates a resource expiration or
// access failure (HTTP 403 or 410) that qualifies for a bounded URL refresh probe.
func IsRefreshTrigger(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrResourceExpired) {
		return true
	}
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 403 || httpErr.StatusCode == 410
	}
	return false
}

// ExtractionError preserves the failure category, adapter name,
// and underlying cause.
//
// The input URL is deliberately excluded because it may contain secrets.
// Underlying adapter errors should also avoid exposing credentials.
type ExtractionError struct {
	Kind      ErrorKind
	Extractor string
	Err       error
}

func (e *ExtractionError) Error() string {
	message := "goyt: " + string(e.Kind)

	if e.Extractor != "" {
		message += " (" + e.Extractor + ")"
	}

	if e.Err != nil {
		message += fmt.Sprintf(": %v", e.Err)
	}

	return message
}

// Unwrap allows errors.Is and errors.As to inspect the cause.
func (e *ExtractionError) Unwrap() error {
	return e.Err
}
