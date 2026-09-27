package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Machine-readable diagnostic error codes.
const (
	ErrCodePlaybackRestricted         = "playback_restricted"
	ErrCodeBotCheckRequired           = "bot_check_required"
	ErrCodeNoSupportedFormats         = "no_supported_formats"
	ErrCodeSignatureChallengeRequired = "signature_challenge_required"
	ErrCodeSignatureChallengeReq      = ErrCodeSignatureChallengeRequired
	ErrCodeNChallengeRequired         = "n_challenge_required"
	ErrCodeNChallengeReq              = ErrCodeNChallengeRequired
	ErrCodeManifestUnavailable        = "manifest_unavailable"
	ErrCodeInvalidPlayerResponse      = "invalid_player_response"
	ErrCodeExtractionRequestFailed    = "extraction_request_failed"
	ErrCodeContextCanceled            = "context_canceled"
	ErrCodeTimeout                    = "timeout"
)

// ExtractionError represents a structured, diagnostic YouTube extraction error.
type ExtractionError struct {
	Code           string   // Stable machine-readable code
	Client         string   // Client profile name (e.g. "visionos", "web")
	PlaybackStatus string   // e.g. "LOGIN_REQUIRED", "UNPLAYABLE", "ERROR"
	PlaybackReason string   // Safe playback reason from YouTube
	Message        string   // Sanitized, safe human-readable message
	Err            error    // Underlying wrapped error if any
	Challenges     []string // Detected format obstacles (e.g. signature_challenge, n_challenge, sabr)
}

func (e *ExtractionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return fmt.Sprintf("youtube extraction failed: %s", e.Code)
	}
	return "youtube extraction failed"
}

func (e *ExtractionError) Unwrap() error {
	return e.Err
}

// IsBotCheckReason detects if a playability reason represents a bot-detection gate.
func IsBotCheckReason(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	if r == "" {
		return false
	}
	return strings.Contains(r, "not a bot") ||
		strings.Contains(r, "confirm you're not a bot") ||
		strings.Contains(r, "confirm you’re not a bot") ||
		strings.Contains(r, "confirm you are not a bot") ||
		strings.Contains(r, "automated queries")
}

// sanitizePlaybackReason cleans playback reasons from YouTube while preserving useful safe text.
func sanitizePlaybackReason(reason string) string {
	cleaned := sanitizeErrorMessage(reason)
	cleaned = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, cleaned)
	return strings.TrimSpace(cleaned)
}

// classifyPlayabilityError converts a non-OK playability status into a structured ExtractionError.
func classifyPlayabilityError(client string, status, reason string) *ExtractionError {
	status = strings.TrimSpace(status)
	safeReason := sanitizePlaybackReason(reason)

	if IsBotCheckReason(safeReason) {
		msg := fmt.Sprintf(
			"youtube: %s client received a YouTube bot-check response (%q); no usable media was returned",
			client,
			safeReason,
		)
		return &ExtractionError{
			Code:           ErrCodeBotCheckRequired,
			Client:         client,
			PlaybackStatus: status,
			PlaybackReason: safeReason,
			Message:        msg,
		}
	}

	var msg string
	if safeReason != "" {
		msg = fmt.Sprintf("youtube: %s playback is restricted (%s: %s)", client, status, safeReason)
	} else {
		msg = fmt.Sprintf("youtube: %s playback is restricted (%s)", client, status)
	}

	return &ExtractionError{
		Code:           ErrCodePlaybackRestricted,
		Client:         client,
		PlaybackStatus: status,
		PlaybackReason: safeReason,
		Message:        msg,
	}
}

var (
	sensitiveURLRegex   = regexp.MustCompile(`https?://[^\s"'<>]+`)
	authHeaderRegex     = regexp.MustCompile(`(?i)(authorization:\s*)(bearer\s+)?[^\s,;]+`)
	cookieHeaderRegex   = regexp.MustCompile(`(?i)(cookie:\s*)[^\r\n]+`)
	visitorPatternRegex = regexp.MustCompile(`(?i)(visitor(?:_data|_info\w*)?[=:][\s"']?)[^\s"',;&]+`)
	httpStatusRegex     = regexp.MustCompile(`\bHTTP\s+(\d{3})\b`)
)

func sanitizeErrorMessage(raw string) string {
	msg := authHeaderRegex.ReplaceAllString(raw, "$1[REDACTED]")
	msg = cookieHeaderRegex.ReplaceAllString(msg, "$1[REDACTED]")
	msg = visitorPatternRegex.ReplaceAllString(msg, "$1[REDACTED]")

	msg = sensitiveURLRegex.ReplaceAllStringFunc(msg, func(uStr string) string {
		parsed, err := url.Parse(uStr)
		if err != nil {
			return "[URL]"
		}
		if strings.Contains(parsed.Host, "googlevideo.com") {
			return "[media-url]"
		}
		if parsed.RawQuery != "" {
			return parsed.Scheme + "://" + parsed.Host + parsed.Path
		}
		return uStr
	})

	return msg
}

// classifyExtractionFailure classifies an unexpected request/parsing error into an ExtractionError.
func classifyExtractionFailure(client string, err error) error {
	if err == nil {
		return nil
	}
	var existing *ExtractionError
	if errors.As(err, &existing) {
		return existing
	}

	if errors.Is(err, context.Canceled) {
		return &ExtractionError{
			Code:    ErrCodeContextCanceled,
			Client:  client,
			Message: fmt.Sprintf("youtube: %s extraction canceled", client),
			Err:     err,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ExtractionError{
			Code:    ErrCodeTimeout,
			Client:  client,
			Message: fmt.Sprintf("youtube: %s extraction timed out", client),
			Err:     err,
		}
	}
	if errors.Is(err, ErrPlayerResponseMissing) {
		return &ExtractionError{
			Code:    ErrCodeInvalidPlayerResponse,
			Client:  client,
			Message: fmt.Sprintf("youtube: %s embedded player response not found on watch page", client),
			Err:     err,
		}
	}

	errStr := err.Error()
	var publicMsg string

	if match := httpStatusRegex.FindStringSubmatch(errStr); len(match) == 2 {
		publicMsg = fmt.Sprintf("youtube: %s player API returned HTTP %s", client, match[1])
	} else if strings.Contains(errStr, "different video ID") {
		publicMsg = fmt.Sprintf("youtube: %s player API returned a different video ID", client)
	} else if strings.Contains(errStr, "missing the requested video ID") {
		publicMsg = fmt.Sprintf("youtube: %s playable response is missing the requested video ID", client)
	} else if strings.Contains(errStr, "missing playback status") {
		publicMsg = fmt.Sprintf("youtube: %s player API response is missing playback status", client)
	} else if strings.Contains(errStr, "invalid JSON") || strings.Contains(errStr, "invalid character") {
		publicMsg = fmt.Sprintf("youtube: %s player API returned invalid JSON", client)
	} else if strings.Contains(errStr, "size limit") {
		publicMsg = fmt.Sprintf("youtube: %s player response exceeds size limit", client)
	} else if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no such host") || strings.Contains(errStr, "i/o timeout") || strings.Contains(errStr, "EOF") {
		publicMsg = fmt.Sprintf("youtube: %s extraction request failed (network error)", client)
	} else {
		publicMsg = fmt.Sprintf("youtube: %s extraction request failed", client)
	}

	return &ExtractionError{
		Code:    ErrCodeExtractionRequestFailed,
		Client:  client,
		Message: publicMsg,
		Err:     err,
	}
}

// classifyNoSupportedFormats inspects the rejected formats from a playable response
// to produce an accurate diagnostic error with identified obstacles.
func classifyNoSupportedFormats(client string, player *playerResponse) *ExtractionError {
	var challenges []string
	hasSig := false
	hasN := false
	hasDRM := false
	hasSABR := player.StreamingData.ServerABRStreamingURL != ""

	allRaw := append([]playerFormat{}, player.StreamingData.Formats...)
	allRaw = append(allRaw, player.StreamingData.AdaptiveFormats...)

	for _, f := range allRaw {
		cipher := f.SignatureCipher
		if cipher == "" {
			cipher = f.Cipher
		}
		if cipher != "" {
			hasSig = true
			if q, err := url.ParseQuery(cipher); err == nil && q.Get("s") != "" {
				hasSig = true
			}
		}
		if f.URL != "" {
			if u, err := url.Parse(f.URL); err == nil && u.Query().Get("n") != "" {
				hasN = true
			}
		}
		if len(f.DRMFamilies) > 0 {
			hasDRM = true
		}
	}

	if hasSig {
		challenges = append(challenges, "signature_challenge")
	}
	if hasN {
		challenges = append(challenges, "n_challenge")
	}
	if hasSABR {
		challenges = append(challenges, "sabr_endpoint")
	}
	if hasDRM {
		challenges = append(challenges, "drm_reported")
	}

	code := ErrCodeNoSupportedFormats
	if hasSig && !hasN && !hasDRM && !hasSABR {
		code = ErrCodeSignatureChallengeReq
	} else if hasN && !hasSig && !hasDRM && !hasSABR {
		code = ErrCodeNChallengeReq
	}

	var msg string
	if len(challenges) > 0 {
		msg = fmt.Sprintf(
			"youtube: %s client found no supported direct formats; unhandled challenges present: %s",
			client,
			strings.Join(challenges, ", "),
		)
	} else if len(allRaw) == 0 {
		msg = fmt.Sprintf("youtube: %s client response exposed no stream formats", client)
	} else {
		msg = fmt.Sprintf("youtube: %s client found no supported direct formats (unsupported container or codec composition)", client)
	}

	return &ExtractionError{
		Code:       code,
		Client:     client,
		Message:    msg,
		Challenges: challenges,
	}
}
