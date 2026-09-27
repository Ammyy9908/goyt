package goyt

import (
	"context"
	"net/url"
)

// Extractor converts a supported page URL into media metadata.
//
// Name must be stable and non-empty.
//
// Match must be fast, perform no network requests,
// and leave the supplied URL unchanged.
//
// Extract must honor context cancellation and return
// fresh results owned by the caller.
//
// Implementations must be concurrency-safe if the Client is shared.
type Extractor interface {
	Name() string
	Match(*url.URL) bool
	Extract(context.Context, *url.URL) (*Media, error)
}
