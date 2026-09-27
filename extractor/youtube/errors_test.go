package youtube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIsBotCheckReason(t *testing.T) {
	botReasons := []string{
		"Sign in to confirm you’re not a bot",
		"Sign in to confirm you're not a bot",
		"Sign in to confirm you’re not a bot.",
		"Sign in to confirm you're not a bot.",
		"Sign in to confirm you’re not a bot. This helps protect our community.",
		"Sign in to confirm you're not a bot. This helps protect our community.",
		"Please sign in to confirm you’re not a bot",
		"Confirm you're not a bot",
		"CONFIRM YOU’RE NOT A BOT",
	}

	for _, r := range botReasons {
		if !IsBotCheckReason(r) {
			t.Errorf("IsBotCheckReason(%q) = false, want true", r)
		}
	}

	nonBotReasons := []string{
		"",
		"This video is private.",
		"This video is unavailable in your country.",
		"Sign in to confirm your age",
		"This video is available to this channel's members on level...",
		"Video unavailable",
		"LOGIN_REQUIRED",
		"The uploader has not made this video available in your country",
	}

	for _, r := range nonBotReasons {
		if IsBotCheckReason(r) {
			t.Errorf("IsBotCheckReason(%q) = true, want false", r)
		}
	}
}

func TestClassifyPlayabilityError(t *testing.T) {
	// Bot check reason -> bot_check_required
	err1 := classifyPlayabilityError("visionos", "LOGIN_REQUIRED", "Sign in to confirm you’re not a bot")
	if err1.Code != ErrCodeBotCheckRequired {
		t.Fatalf("expected code %s, got %s", ErrCodeBotCheckRequired, err1.Code)
	}
	if err1.Client != "visionos" {
		t.Fatalf("expected client visionos, got %s", err1.Client)
	}
	if err1.PlaybackStatus != "LOGIN_REQUIRED" {
		t.Fatalf("expected status LOGIN_REQUIRED, got %s", err1.PlaybackStatus)
	}
	if !strings.Contains(err1.Message, "bot-check response") {
		t.Fatalf("unexpected message: %s", err1.Message)
	}

	// Non-bot LOGIN_REQUIRED -> playback_restricted (NOT bot_check_required)
	err2 := classifyPlayabilityError("web", "LOGIN_REQUIRED", "Sign in to confirm your age")
	if err2.Code != ErrCodePlaybackRestricted {
		t.Fatalf("expected code %s, got %s", ErrCodePlaybackRestricted, err2.Code)
	}
	if err2.Client != "web" {
		t.Fatalf("expected client web, got %s", err2.Client)
	}
	if !strings.Contains(err2.Message, "Sign in to confirm your age") {
		t.Fatalf("expected message to retain safe reason, got %s", err2.Message)
	}

	// UNPLAYABLE status with localized reason -> playback_restricted
	err3 := classifyPlayabilityError("visionos", "UNPLAYABLE", "Video unavailable in this region")
	if err3.Code != ErrCodePlaybackRestricted {
		t.Fatalf("expected code %s, got %s", ErrCodePlaybackRestricted, err3.Code)
	}

	// Status only, empty reason -> playback_restricted with status
	err4 := classifyPlayabilityError("visionos", "ERROR", "")
	if err4.Code != ErrCodePlaybackRestricted {
		t.Fatalf("expected code %s, got %s", ErrCodePlaybackRestricted, err4.Code)
	}
}

func TestClassifyExtractionFailure(t *testing.T) {
	// Context cancellation
	canceledErr := classifyExtractionFailure("visionos", context.Canceled)
	var extCanceled *ExtractionError
	if !errors.As(canceledErr, &extCanceled) {
		t.Fatalf("expected *ExtractionError, got %T: %v", canceledErr, canceledErr)
	}
	if extCanceled.Code != ErrCodeContextCanceled {
		t.Fatalf("expected code %s, got %s", ErrCodeContextCanceled, extCanceled.Code)
	}
	if !errors.Is(canceledErr, context.Canceled) {
		t.Fatalf("expected errors.Is(canceledErr, context.Canceled) to be true")
	}

	// Timeout
	timeoutErr := classifyExtractionFailure("web", context.DeadlineExceeded)
	var extTimeout *ExtractionError
	if !errors.As(timeoutErr, &extTimeout) {
		t.Fatalf("expected *ExtractionError, got %T: %v", timeoutErr, timeoutErr)
	}
	if extTimeout.Code != ErrCodeTimeout {
		t.Fatalf("expected code %s, got %s", ErrCodeTimeout, extTimeout.Code)
	}
	if !errors.Is(timeoutErr, context.DeadlineExceeded) {
		t.Fatalf("expected errors.Is(timeoutErr, context.DeadlineExceeded) to be true")
	}

	// HTTP 403 error is classified as extraction_request_failed, NOT bot check or expired
	http403 := fmt.Errorf("youtube: HTTP 403 Forbidden")
	err403 := classifyExtractionFailure("visionos", http403)
	var ext403 *ExtractionError
	if !errors.As(err403, &ext403) {
		t.Fatalf("expected *ExtractionError, got %T: %v", err403, err403)
	}
	if ext403.Code != ErrCodeExtractionRequestFailed {
		t.Fatalf("expected code %s, got %s", ErrCodeExtractionRequestFailed, ext403.Code)
	}
	if strings.Contains(ext403.Message, "bot") {
		t.Fatalf("HTTP 403 must not be classified as bot check: %s", ext403.Message)
	}

	// Generic network error
	netErr := fmt.Errorf("dial tcp: connection refused")
	errNet := classifyExtractionFailure("web", netErr)
	var extNet *ExtractionError
	if !errors.As(errNet, &extNet) {
		t.Fatalf("expected *ExtractionError, got %T: %v", errNet, errNet)
	}
	if extNet.Code != ErrCodeExtractionRequestFailed {
		t.Fatalf("expected code %s, got %s", ErrCodeExtractionRequestFailed, extNet.Code)
	}
	if !errors.Is(errNet, netErr) {
		t.Fatalf("expected wrapped error to be matchable via errors.Is")
	}
}

func TestClassifyNoSupportedFormats(t *testing.T) {
	// Only signature challenged formats
	pSig := &playerResponse{}
	pSig.StreamingData.AdaptiveFormats = []playerFormat{
		{Itag: 137, SignatureCipher: "s=secret&url=https://media.test/1"},
		{Itag: 140, SignatureCipher: "s=secret2&url=https://media.test/2"},
	}
	errSig := classifyNoSupportedFormats("web", pSig)
	if errSig.Code != ErrCodeSignatureChallengeRequired {
		t.Fatalf("expected code %s, got %s", ErrCodeSignatureChallengeRequired, errSig.Code)
	}

	// Only n-challenged formats
	pN := &playerResponse{}
	pN.StreamingData.AdaptiveFormats = []playerFormat{
		{Itag: 137, URL: "https://media.test/1?n=challenge1"},
		{Itag: 140, URL: "https://media.test/2?n=challenge2"},
	}
	errN := classifyNoSupportedFormats("web", pN)
	if errN.Code != ErrCodeNChallengeRequired {
		t.Fatalf("expected code %s, got %s", ErrCodeNChallengeRequired, errN.Code)
	}

	// Mixed challenges / SABR
	pMixed := &playerResponse{}
	pMixed.StreamingData.Formats = []playerFormat{
		{Itag: 18, DRMFamilies: []string{"widevine"}},
	}
	pMixed.StreamingData.AdaptiveFormats = []playerFormat{
		{Itag: 137, SignatureCipher: "s=secret&url=https://media.test/1"},
		{Itag: 140, URL: "https://media.test/2?n=challenge2"},
	}
	pMixed.StreamingData.ServerABRStreamingURL = "https://sabr.test/endpoint"

	errMixed := classifyNoSupportedFormats("web", pMixed)
	if errMixed.Code != ErrCodeNoSupportedFormats {
		t.Fatalf("expected code %s, got %s", ErrCodeNoSupportedFormats, errMixed.Code)
	}
	if len(errMixed.Challenges) < 3 {
		t.Fatalf("expected at least 3 challenges preserved, got %d", len(errMixed.Challenges))
	}

	// Empty formats list
	pEmpty := &playerResponse{}
	errEmpty := classifyNoSupportedFormats("visionos", pEmpty)
	if errEmpty.Code != ErrCodeNoSupportedFormats {
		t.Fatalf("expected code %s, got %s", ErrCodeNoSupportedFormats, errEmpty.Code)
	}
}

func TestDiagnosticSafety_NoSecretLeaks(t *testing.T) {
	sensitiveURL := "https://rr1---sn-abc.googlevideo.com/videoplayback?expire=12345678&sig=SIG_SECRET&n=N_TOKEN"
	underlying := fmt.Errorf("failed request to %s with header Authorization: Bearer SECRET_TOKEN and Cookie: VISITOR_INFO1_LIVE=VISITOR_SECRET", sensitiveURL)

	err := classifyExtractionFailure("visionos", underlying)

	msg := err.Error()
	forbiddenStrings := []string{
		"SIG_SECRET",
		"N_TOKEN",
		"SECRET_TOKEN",
		"VISITOR_SECRET",
		"rr1---sn-abc",
	}

	for _, s := range forbiddenStrings {
		if strings.Contains(msg, s) {
			t.Fatalf("error message contains sensitive information %q: %s", s, msg)
		}
	}

	// Underlying error must remain unwrappable via errors.Is/errors.As
	if !errors.Is(err, underlying) {
		t.Fatalf("expected underlying error to be unwrappable via errors.Is")
	}
}

func TestDiagnosticSafety_PlaybackReasonSanitization(t *testing.T) {
	dirtyReason := "Video unavailable: check https://rr1---sn-abc.googlevideo.com/videoplayback?sig=SENSITIVE_SIG with Authorization: Bearer SECRET"
	err := classifyPlayabilityError("visionos", "UNPLAYABLE", dirtyReason)

	msg := err.Error()
	if strings.Contains(msg, "SENSITIVE_SIG") {
		t.Fatalf("error message leaked signature from playback reason: %s", msg)
	}
	if strings.Contains(msg, "SECRET") {
		t.Fatalf("error message leaked bearer secret from playback reason: %s", msg)
	}
	if err.PlaybackReason == "" {
		t.Fatalf("expected sanitized playback reason to be retained, got empty")
	}
}

func TestClassifyNoSupportedFormats_MixedChallenges(t *testing.T) {
	player := &playerResponse{}
	player.StreamingData.Formats = []playerFormat{
		{Itag: 18, DRMFamilies: []string{"widevine"}},
	}
	player.StreamingData.AdaptiveFormats = []playerFormat{
		{Itag: 137, SignatureCipher: "s=secretSig&url=https://media.test/1"},
		{Itag: 140, URL: "https://media.test/2?n=challengeToken"},
	}
	player.StreamingData.ServerABRStreamingURL = "https://sabr.test/endpoint"

	err := classifyNoSupportedFormats("web", player)
	if err.Code != ErrCodeNoSupportedFormats {
		t.Fatalf("expected code %s, got %s", ErrCodeNoSupportedFormats, err.Code)
	}

	expectedChallenges := []string{"signature_challenge", "n_challenge", "sabr_endpoint", "drm_reported"}
	if len(err.Challenges) != len(expectedChallenges) {
		t.Fatalf("expected %d challenges, got %d (%v)", len(expectedChallenges), len(err.Challenges), err.Challenges)
	}
	for _, exp := range expectedChallenges {
		found := false
		for _, ch := range err.Challenges {
			if ch == exp {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected challenge %q to be present in %v", exp, err.Challenges)
		}
	}

	msg := err.Error()
	if !strings.Contains(msg, "unhandled challenges present") {
		t.Fatalf("expected message to mention unhandled challenges, got: %s", msg)
	}
}
