package goyt

import (
	"net/http"
	"time"
)

// Media describes one extracted media item.
type Media struct {
	ID        string
	SourceURL string
	Title     string
	Duration  *time.Duration
	Formats   []Format
}

// Protocol identifies how a resource is delivered.
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
	ProtocolHLS  Protocol = "hls"
	ProtocolDASH Protocol = "dash"
)

// Format describes one available representation.
//
// Dimensions are pixels, Bitrate is bits per second,
// and SizeBytes is bytes.
//
// Empty codec names mean unknown; "none" means absent.
type Format struct {
	ID         string
	Protocol   Protocol
	Container  string
	VideoCodec string
	AudioCodec string
	Width      *int
	Height     *int
	Bitrate    *int64
	SizeBytes  *int64
	Language   string
	Resource   Resource
}

// Resource describes a potentially temporary media URL.
//
// Headers apply to this resource only. Future downloaders must
// avoid forwarding credentials across unrelated redirects.
type Resource struct {
	URL       string
	Headers   http.Header `json:"-"`
	ExpiresAt *time.Time
}
