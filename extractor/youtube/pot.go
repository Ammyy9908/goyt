package youtube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Safety bounds for Proof-of-Origin (PO) tokens.
const (
	// MaxPOTokenLength limits the acceptable length of an opaque PO-token.
	MaxPOTokenLength = 4096
)

// POTokenContext identifies the YouTube request context requiring a PO-token.
type POTokenContext string

const (
	// POTokenContextPlayer represents an Innertube v1/player API request context.
	// Placement: serviceIntegrityDimensions.poToken at the root of the JSON request body.
	POTokenContextPlayer POTokenContext = "player"

	// POTokenContextGVS represents a Google Video Server (GVS) media stream request context.
	// Placement: pot query parameter on direct googlevideo.com media URLs.
	POTokenContextGVS POTokenContext = "gvs"

	// POTokenContextSubs represents a subtitle request context.
	// Placement: pot query parameter on subtitle request URLs.
	// Note: Subtitle downloading is out of scope for goyt; this context is defined for protocol completeness.
	POTokenContextSubs POTokenContext = "subs"
)

// POTokenRequest describes the scope for which a Proof-of-Origin token is requested.
type POTokenRequest struct {
	Client      string         // Client identifier (e.g. "web", "visionos")
	Context     POTokenContext // Token context: player, gvs, subs
	VideoID     string         // YouTube 11-character video ID (when applicable)
	VisitorData string         // Session/visitor identifier (when available/bound)
}

// Validate checks that the request contains required scope fields.
func (r POTokenRequest) Validate() error {
	if strings.TrimSpace(r.Client) == "" {
		return errors.New("youtube: PO-token request missing client")
	}
	switch r.Context {
	case POTokenContextPlayer, POTokenContextGVS, POTokenContextSubs:
	default:
		return fmt.Errorf("youtube: unsupported PO-token context %q", r.Context)
	}
	if r.Context == POTokenContextGVS && strings.TrimSpace(r.VideoID) == "" {
		return errors.New("youtube: video ID is required for GVS PO-token request")
	}
	if r.VideoID != "" && !videoIDPattern.MatchString(r.VideoID) {
		return errors.New("youtube: invalid video ID in PO-token request")
	}
	return nil
}

// ScopeKey generates a canonical composite key representing the exact scope of this token request.
func (r POTokenRequest) ScopeKey() string {
	return fmt.Sprintf("%s|%s|%s|%s", r.Client, r.Context, r.VideoID, r.VisitorData)
}

// POTokenResult contains the Proof-of-Origin token and associated scope metadata.
type POTokenResult struct {
	Token       string         // Opaque token value
	Client      string         // Bound client identifier
	Context     POTokenContext // Bound token context
	VideoID     string         // Bound video ID (if applicable)
	VisitorData string         // Bound visitor data (if applicable)
	ExpiresAt   *time.Time     // Optional known expiration time; nil if unknown
}

// ValidateFor verifies that the result matches the requested scope, contains a valid bounded token,
// and has not expired.
func (res POTokenResult) ValidateFor(req POTokenRequest, now time.Time) error {
	if strings.TrimSpace(res.Token) == "" {
		return ErrPOTokenInvalid
	}
	if len(res.Token) > MaxPOTokenLength {
		return ErrPOTokenInvalid
	}
	for _, r := range res.Token {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ErrPOTokenInvalid
		}
	}
	if res.Client != "" && res.Client != req.Client {
		return ErrPOTokenScopeMismatch
	}
	if res.Context != "" && res.Context != req.Context {
		return ErrPOTokenScopeMismatch
	}
	if req.VideoID != "" && res.VideoID != "" && res.VideoID != req.VideoID {
		return ErrPOTokenScopeMismatch
	}
	if req.VisitorData != "" && res.VisitorData != "" && res.VisitorData != req.VisitorData {
		return ErrPOTokenScopeMismatch
	}
	if res.ExpiresAt != nil && !res.ExpiresAt.IsZero() {
		if !now.Before(*res.ExpiresAt) {
			return ErrPOTokenExpired
		}
	}
	return nil
}

// POTokenProvider represents a pluggable provider for YouTube Proof-of-Origin (PO) tokens.
//
// Implementations MUST be safe for concurrent use across multiple goroutines.
type POTokenProvider interface {
	// GetPOToken requests a PO-token for the given scope.
	//
	// It must respect context cancellation and return ErrPOTokenUnavailable if no token
	// is available for the requested scope, or ErrPOTokenProviderFailed / underlying error on failure.
	GetPOToken(ctx context.Context, req POTokenRequest) (POTokenResult, error)
}

// Errors associated with PO-token operations.
var (
	// ErrNoPOTokenProviderConfigured indicates that a PO-token operation was requested without a configured provider.
	ErrNoPOTokenProviderConfigured = errors.New("youtube: no PO-token provider configured")

	// ErrPOTokenUnavailable indicates that the provider has no token for the requested scope.
	ErrPOTokenUnavailable = errors.New("youtube: PO-token unavailable for requested scope")

	// ErrPOTokenScopeMismatch indicates that the returned token was bound to a different scope than requested.
	ErrPOTokenScopeMismatch = errors.New("youtube: PO-token scope mismatch")

	// ErrPOTokenExpired indicates that the returned token has already expired.
	ErrPOTokenExpired = errors.New("youtube: PO-token expired")

	// ErrPOTokenInvalid indicates that the token value is empty, oversized, or contains control characters.
	ErrPOTokenInvalid = errors.New("youtube: PO-token invalid")

	// ErrPOTokenProviderFailed indicates that the provider failed to generate or retrieve a token.
	ErrPOTokenProviderFailed = errors.New("youtube: PO-token provider failed")
)

// ClientSupportsPOTokenContext reports whether the named client and token context
// have an established, supported request placement in goyt.
func ClientSupportsPOTokenContext(clientName string, ctx POTokenContext) bool {
	switch strings.ToLower(strings.TrimSpace(clientName)) {
	case ClientWeb:
		return ctx == POTokenContextPlayer || ctx == POTokenContextGVS
	default:
		return false
	}
}
