package goyt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

type registeredExtractor struct {
	name    string
	adapter Extractor
}

// Client holds an immutable, ordered extractor registry.
//
// The zero value is usable but has no registered extractors.
// Concurrent use requires concurrency-safe adapters.
type Client struct {
	extractors []registeredExtractor
}

// New registers extractors in priority order.
//
// Nil adapters, blank names, and duplicate names are rejected.
func New(extractors ...Extractor) (*Client, error) {
	client := &Client{}
	names := make(map[string]bool)

	for _, adapter := range extractors {
		if isNilExtractor(adapter) {
			return nil, errors.New("goyt: nil extractor")
		}

		name := strings.TrimSpace(adapter.Name())
		if name == "" {
			return nil, errors.New("goyt: extractor name is empty")
		}

		if names[name] {
			return nil, fmt.Errorf("goyt: duplicate extractor %q", name)
		}

		names[name] = true
		client.extractors = append(
			client.extractors,
			registeredExtractor{
				name:    name,
				adapter: adapter,
			},
		)
	}

	return client, nil
}

// An interface can contain a typed nil pointer while not being nil itself.
func isNilExtractor(adapter Extractor) bool {
	if adapter == nil {
		return true
	}

	value := reflect.ValueOf(adapter)

	switch value.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return value.IsNil()
	}

	return false
}

// Extract resolves one absolute HTTP(S) URL into metadata.
//
// No media files are downloaded.
//
// Cancellation is cooperative: adapters must honor ctx.
// The supplied context must be non-nil.
func (c *Client) Extract(
	ctx context.Context,
	rawURL string,
) (*Media, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	u, err := url.Parse(rawURL)
	if err != nil ||
		u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil {
		return nil, &ExtractionError{
			Kind: ErrorInvalidURL,
		}
	}

	for _, registered := range c.extractors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if !registered.adapter.Match(u) {
			continue
		}

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		media, err := registered.adapter.Extract(ctx, u)

		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, cancelErr
		}

		if err != nil {
			return nil, &ExtractionError{
				Kind:      ErrorExtraction,
				Extractor: registered.name,
				Err:       err,
			}
		}

		if media == nil || strings.TrimSpace(media.ID) == "" {
			return nil, &ExtractionError{
				Kind:      ErrorInvalidResult,
				Extractor: registered.name,
				Err: errors.New(
					"extractor must return media with a non-empty ID",
				),
			}
		}

		if media.SourceURL == "" {
			media.SourceURL = u.String()
		}

		return media, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return nil, &ExtractionError{
		Kind: ErrorUnsupportedURL,
	}
}
