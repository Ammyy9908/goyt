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

var (
	// ErrJobLocked indicates that another process holds an exclusive lock on the job directory.
	ErrJobLocked = errors.New("goyt: job directory is locked by another process")

	// ErrJobExists indicates that the specified job directory already exists when creating a new job.
	ErrJobExists = errors.New("goyt: job directory already exists")

	// ErrJobNotFound indicates that the specified job directory does not exist or has no manifest.
	ErrJobNotFound = errors.New("goyt: job directory not found")

	// ErrInvalidManifest indicates that a job manifest could not be parsed or contains invalid fields.
	ErrInvalidManifest = errors.New("goyt: invalid or corrupted job manifest")

	// ErrUnsupportedManifestVersion indicates that the manifest schema version is not supported.
	ErrUnsupportedManifestVersion = errors.New("goyt: unsupported job manifest version")

	// ErrSelectionUnavailable indicates that the originally pinned format or audio track is missing upon re-extraction.
	ErrSelectionUnavailable = errors.New("goyt: original format or audio track is no longer available")

	// ErrInputIntegrityMismatch indicates that a saved input stream failed its size or SHA-256 integrity check.
	ErrInputIntegrityMismatch = errors.New("goyt: downloaded input stream failed integrity check")

	// ErrCompletedOutputMismatch indicates that a completed job's output file has been removed or modified.
	ErrCompletedOutputMismatch = errors.New("goyt: completed output file is missing or modified")

	// ErrInvalidJobPath indicates that a file path in the job manifest escapes the job directory or is unsafe.
	ErrInvalidJobPath = errors.New("goyt: job file path is invalid or attempts directory traversal")
)

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
