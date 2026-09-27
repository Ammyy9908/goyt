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
