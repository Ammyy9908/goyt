package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fakeSolver is a deterministic challenge solver used exclusively for testing orchestration.
// It does NOT implement real YouTube signature or n-parameter deciphering algorithms.
type fakeSolver struct {
	solveFunc func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error)
}

func (f *fakeSolver) SolveChallenges(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
	if f.solveFunc != nil {
		return f.solveFunc(ctx, script, batch)
	}

	res := NewChallengeBatchResult()
	for _, sig := range batch.Signatures {
		// Deterministic fake deciphering: reverse the string
		runes := []rune(sig.CipherString)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		res.Signatures[sig.ID] = SignatureResult{
			ID:         sig.ID,
			Deciphered: "deciphered_" + string(runes),
		}
	}

	for _, n := range batch.NParams {
		res.NParams[n.ID] = NResult{
			ID:          n.ID,
			Transformed: "transformed_" + n.RawValue,
		}
	}

	return res, nil
}

func makeWatchAndPlayerTransport(playerJSON string, scriptJS string) http.RoundTripper {
	return playerTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/watch" {
			html := fmt.Sprintf(`<html><head><script src="/s/player/testver/base.js"></script></head><body><script>ytInitialPlayerResponse = %s;</script></body></html>`, playerJSON)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(html)),
				Request:    req,
			}, nil
		}

		if req.URL.Path == "/s/player/testver/base.js" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/javascript"}},
				Body:       io.NopCloser(strings.NewReader(scriptJS)),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})
}

func TestExtractDownloadable_DirectFormatsNeedNoSolver(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Direct Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 18,
					"mimeType": "video/mp4; codecs=\"avc1.64001F, mp4a.40.2\"",
					"url": "https://media.test/video.mp4?expire=12345678"
				}
			]
		}
	}`

	scriptFetched := false
	transport := playerTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/s/player/testver/base.js" {
			scriptFetched = true
		}
		return makeWatchAndPlayerTransport(playerJSON, "var script = 1;").RoundTrip(req)
	})

	ext := New(&http.Client{Transport: transport})
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(media.Formats) != 1 {
		t.Fatalf("expected 1 format, got %d", len(media.Formats))
	}
	if scriptFetched {
		t.Fatalf("direct format should not cause player script fetch")
	}
}

func TestExtractDownloadable_SignatureChallengeWithSolver(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Cipher Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=ABCD1234&sp=sig&url=https%3A%2F%2Fmedia.test%2Fvideoplayback%3Fexpire%3D12345678"
				},
				{
					"itag": 140,
					"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
					"signatureCipher": "s=EFGH5678&url=https%3A%2F%2Fmedia.test%2Faudioplayback%3Fexpire%3D12345678"
				}
			]
		}
	}`

	transport := makeWatchAndPlayerTransport(playerJSON, "var playerScript = 123;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(&fakeSolver{}))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(media.Formats) != 2 {
		t.Fatalf("expected 2 formats after solving, got %d", len(media.Formats))
	}

	// Verify format 137 got deciphered signature under "sig" (custom sp)
	f0 := media.Formats[0]
	parsed0, _ := url.Parse(f0.Resource.URL)
	if parsed0.Query().Get("sig") != "deciphered_4321DCBA" {
		t.Fatalf("expected signature query param sig=deciphered_4321DCBA, got: %s", f0.Resource.URL)
	}

	// Verify format 140 used fallback sp ("sig")
	f1 := media.Formats[1]
	parsed1, _ := url.Parse(f1.Resource.URL)
	if parsed1.Query().Get("sig") != "deciphered_8765HGFE" {
		t.Fatalf("expected signature fallback query param sig=deciphered_8765HGFE, got: %s", f1.Resource.URL)
	}
}

func TestExtractDownloadable_NChallengeWithSolver(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "N-Challenge Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"url": "https://media.test/videoplayback?expire=12345678&n=challengeToken"
				}
			]
		}
	}`

	transport := makeWatchAndPlayerTransport(playerJSON, "var playerScript = 123;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(&fakeSolver{}))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(media.Formats) != 1 {
		t.Fatalf("expected 1 format after solving, got %d", len(media.Formats))
	}

	parsed, _ := url.Parse(media.Formats[0].Resource.URL)
	if parsed.Query().Get("n") != "transformed_challengeToken" {
		t.Fatalf("expected transformed n parameter, got %s", media.Formats[0].Resource.URL)
	}
	if parsed.Query().Get("expire") != "12345678" {
		t.Fatalf("expected unrelated query parameter expire to be preserved")
	}
}

func TestExtractDownloadable_CombinedSignatureAndNChallenge(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Combined Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=SECRET123&sp=sig&url=https%3A%2F%2Fmedia.test%2Fvideoplayback%3Fexpire%3D12345678%26n%3DNTOKEN"
				}
			]
		}
	}`

	transport := makeWatchAndPlayerTransport(playerJSON, "var playerScript = 123;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(&fakeSolver{}))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(media.Formats) != 1 {
		t.Fatalf("expected 1 format, got %d", len(media.Formats))
	}

	parsed, _ := url.Parse(media.Formats[0].Resource.URL)
	if parsed.Query().Get("sig") != "deciphered_321TERCES" {
		t.Fatalf("expected signature param sig, got %s", media.Formats[0].Resource.URL)
	}
	if parsed.Query().Get("n") != "transformed_NTOKEN" {
		t.Fatalf("expected transformed n param, got %s", media.Formats[0].Resource.URL)
	}
}

func TestExtractDownloadable_PartialFailureRejectsIncompleteFormat(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Partial Failure Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=SECRET123&sp=sig&url=https%3A%2F%2Fmedia.test%2Fvideoplayback%3Fexpire%3D12345678%26n%3DNTOKEN"
				},
				{
					"itag": 140,
					"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
					"url": "https://media.test/audioplayback?expire=12345678"
				}
			]
		}
	}`

	// Solver fails on N parameter but succeeds on signature
	partialSolver := &fakeSolver{
		solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
			res := NewChallengeBatchResult()
			for _, sig := range batch.Signatures {
				res.Signatures[sig.ID] = SignatureResult{ID: sig.ID, Deciphered: "deciphered"}
			}
			for _, n := range batch.NParams {
				res.NParams[n.ID] = NResult{ID: n.ID, Error: errors.New("n decipher failed")}
			}
			return res, nil
		},
	}

	transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(partialSolver))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only format 140 (direct, unchallenged) should survive
	if len(media.Formats) != 1 {
		t.Fatalf("expected 1 format (the direct audio format), got %d", len(media.Formats))
	}
	if media.Formats[0].ID != "140" {
		t.Fatalf("expected format 140, got %s", media.Formats[0].ID)
	}
}

func TestExtractDownloadable_Deduplication(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Deduplication Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=SHARED_SIG&sp=sig&url=https%3A%2F%2Fmedia.test%2F1%3Fn%3DSHARED_N"
				},
				{
					"itag": 136,
					"mimeType": "video/mp4; codecs=\"avc1.64001F\"",
					"signatureCipher": "s=SHARED_SIG&sp=sig&url=https%3A%2F%2Fmedia.test%2F2%3Fn%3DSHARED_N"
				}
			]
		}
	}`

	var batchReceived ChallengeBatch
	solver := &fakeSolver{
		solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
			batchReceived = batch
			res := NewChallengeBatchResult()
			for _, s := range batch.Signatures {
				res.Signatures[s.ID] = SignatureResult{ID: s.ID, Deciphered: "solved_sig"}
			}
			for _, n := range batch.NParams {
				res.NParams[n.ID] = NResult{ID: n.ID, Transformed: "solved_n"}
			}
			return res, nil
		},
	}

	transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(solver))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(batchReceived.Signatures) != 1 {
		t.Fatalf("expected 1 deduplicated signature in batch, got %d", len(batchReceived.Signatures))
	}
	if len(batchReceived.NParams) != 1 {
		t.Fatalf("expected 1 deduplicated n param in batch, got %d", len(batchReceived.NParams))
	}
	if len(media.Formats) != 2 {
		t.Fatalf("expected 2 formats mapped from deduplicated result, got %d", len(media.Formats))
	}
}

func TestExtractDownloadable_SolverError_FailsDescriptively(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Solver Error Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=SHARED_SIG&sp=sig&url=https%3A%2F%2Fmedia.test%2F1"
				}
			]
		}
	}`

	failingSolver := &fakeSolver{
		solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
			return ChallengeBatchResult{}, errors.New("vm execution crashed")
		},
	}

	transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(failingSolver))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	_, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err == nil {
		t.Fatal("expected error from failing solver, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected *ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeChallengeSolverFailed {
		t.Fatalf("expected code %s, got %s", ErrCodeChallengeSolverFailed, extErr.Code)
	}
}

func TestExtractDownloadable_MalformedCipherPayload(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Malformed Cipher"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=SECRET&url=relative-url-not-http"
				}
			]
		}
	}`

	transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(&fakeSolver{}))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	_, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err == nil {
		t.Fatal("expected error for malformed cipher URL, got nil")
	}
}

func TestParseCandidateFormat_AmbiguousQueryFields(t *testing.T) {
	tests := []struct {
		name string
		raw  playerFormat
		want bool
	}{
		{
			name: "valid signatureCipher",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET123&sp=sig&url=https%3A%2F%2Fmedia.test%2Fvideoplayback%3Fexpire%3D12345678",
			},
			want: true,
		},
		{
			name: "valid direct URL",
			raw: playerFormat{
				Itag:     137,
				MIMEType: `video/mp4; codecs="avc1.640028"`,
				URL:      "https://media.test/videoplayback?expire=12345678&n=token",
			},
			want: true,
		},
		{
			name: "reject conflicting simultaneous signatureCipher and cipher",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET1&url=https%3A%2F%2Fmedia.test%2F1",
				Cipher:          "s=SECRET2&url=https%3A%2F%2Fmedia.test%2F2",
			},
			want: false,
		},
		{
			name: "reject duplicate url parameter in cipher with same value",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET&url=https%3A%2F%2Fmedia.test%2F1&url=https%3A%2F%2Fmedia.test%2F1",
			},
			want: false,
		},
		{
			name: "reject duplicate url parameter in cipher with different value",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET&url=https%3A%2F%2Fmedia.test%2F1&url=https%3A%2F%2Fmedia.test%2F2",
			},
			want: false,
		},
		{
			name: "reject duplicate s parameter in cipher with same value",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET&s=SECRET&url=https%3A%2F%2Fmedia.test%2F1",
			},
			want: false,
		},
		{
			name: "reject duplicate s parameter in cipher with different value",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET1&s=SECRET2&url=https%3A%2F%2Fmedia.test%2F1",
			},
			want: false,
		},
		{
			name: "reject duplicate sp parameter in cipher with same value",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET&sp=sig&sp=sig&url=https%3A%2F%2Fmedia.test%2F1",
			},
			want: false,
		},
		{
			name: "reject duplicate sp parameter in cipher with different value",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET&sp=sig&sp=signature&url=https%3A%2F%2Fmedia.test%2F1",
			},
			want: false,
		},
		{
			name: "reject duplicate n parameter in media URL inside cipher",
			raw: playerFormat{
				Itag:            137,
				MIMEType:        `video/mp4; codecs="avc1.640028"`,
				SignatureCipher: "s=SECRET&sp=sig&url=https%3A%2F%2Fmedia.test%2F1%3Fn%3Dtoken1%26n%3Dtoken2",
			},
			want: false,
		},
		{
			name: "reject duplicate n parameter in direct URL format",
			raw: playerFormat{
				Itag:     137,
				MIMEType: `video/mp4; codecs="avc1.640028"`,
				URL:      "https://media.test/1?n=token1&n=token2",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := parseCandidateFormat(tt.raw)
			if ok != tt.want {
				t.Errorf("parseCandidateFormat() ok = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestExtractDownloadable_AdversarialSolverResults(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Adversarial Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=SECRET123&sp=sig&url=https%3A%2F%2Fmedia.test%2F1"
				},
				{
					"itag": 140,
					"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
					"url": "https://media.test/2?n=NTOKEN"
				},
				{
					"itag": 299,
					"mimeType": "video/mp4; codecs=\"avc1.64002a\"",
					"signatureCipher": "s=DUALSECRET&sp=sig&url=https%3A%2F%2Fmedia.test%2F3%3Fn%3DDUALN"
				}
			]
		}
	}`

	t.Run("rejects unknown result ID in solver response", func(t *testing.T) {
		unknownSolver := &fakeSolver{
			solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
				res := NewChallengeBatchResult()
				// Return valid result for sig-0
				res.Signatures["sig-0"] = SignatureResult{ID: "sig-0", Deciphered: "valid_sig"}
				// Inject unknown result IDs
				res.Signatures["sig-unknown-999"] = SignatureResult{ID: "sig-unknown-999", Deciphered: "evil_sig"}
				res.NParams["n-unknown-888"] = NResult{ID: "n-unknown-888", Transformed: "evil_n"}
				return res, nil
			},
		}

		transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
		ext := New(&http.Client{Transport: transport}, WithChallengeSolver(unknownSolver))
		u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

		media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Format 137 (sig-0) succeeded; formats 140 and 299 were not solved.
		if len(media.Formats) != 1 {
			t.Fatalf("expected 1 format (format 137), got %d", len(media.Formats))
		}
		if media.Formats[0].ID != "137" {
			t.Fatalf("expected format 137, got %s", media.Formats[0].ID)
		}
	})

	t.Run("rejects mismatched result ID where map key differs from result struct ID", func(t *testing.T) {
		mismatchedSolver := &fakeSolver{
			solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
				res := NewChallengeBatchResult()
				// Map key is "sig-0" but struct ID is "sig-other"
				res.Signatures["sig-0"] = SignatureResult{ID: "sig-other", Deciphered: "mismatched_sig"}
				return res, nil
			},
		}

		transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
		ext := New(&http.Client{Transport: transport}, WithChallengeSolver(mismatchedSolver))
		u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

		_, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
		if err == nil {
			t.Fatal("expected extraction failure when all challenged formats fail solver validation, got nil")
		}
	})

	t.Run("rejects oversized deciphered value exceeding MaxTransformedValueLength", func(t *testing.T) {
		oversizedSolver := &fakeSolver{
			solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
				res := NewChallengeBatchResult()
				res.Signatures["sig-0"] = SignatureResult{
					ID:         "sig-0",
					Deciphered: strings.Repeat("a", MaxTransformedValueLength+10),
				}
				return res, nil
			},
		}

		transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
		ext := New(&http.Client{Transport: transport}, WithChallengeSolver(oversizedSolver))
		u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

		_, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
		if err == nil {
			t.Fatal("expected extraction failure when solver returns oversized value, got nil")
		}
	})

	t.Run("format requiring both signature and n remains unusable if either operation fails", func(t *testing.T) {
		// Dual format itag 299 requires both signature and n.
		// Solver 1: sig succeeds, n fails
		solverSigOnly := &fakeSolver{
			solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
				res := NewChallengeBatchResult()
				for _, s := range batch.Signatures {
					res.Signatures[s.ID] = SignatureResult{ID: s.ID, Deciphered: "solved_sig"}
				}
				for _, n := range batch.NParams {
					res.NParams[n.ID] = NResult{ID: n.ID, Error: errors.New("n failed")}
				}
				return res, nil
			},
		}

		transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
		ext := New(&http.Client{Transport: transport}, WithChallengeSolver(solverSigOnly))
		u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

		media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Only format 137 (sig-only) succeeds; 299 (dual) must be unusable and rejected
		for _, f := range media.Formats {
			if f.ID == "299" {
				t.Fatalf("format 299 requiring both sig and n must not be present when n fails")
			}
		}

		// Solver 2: n succeeds, sig fails
		solverNOnly := &fakeSolver{
			solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
				res := NewChallengeBatchResult()
				for _, s := range batch.Signatures {
					res.Signatures[s.ID] = SignatureResult{ID: s.ID, Error: errors.New("sig failed")}
				}
				for _, n := range batch.NParams {
					res.NParams[n.ID] = NResult{ID: n.ID, Transformed: "solved_n"}
				}
				return res, nil
			},
		}

		ext2 := New(&http.Client{Transport: transport}, WithChallengeSolver(solverNOnly))
		media2, err2 := ext2.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
		if err2 != nil {
			t.Fatalf("unexpected error: %v", err2)
		}

		// Only format 140 (n-only) succeeds; 299 (dual) must be unusable and rejected
		for _, f := range media2.Formats {
			if f.ID == "299" {
				t.Fatalf("format 299 requiring both sig and n must not be present when sig fails")
			}
		}
	})
}

func TestExtractDownloadable_BatchSizeLimit(t *testing.T) {
	// Build a player response with 120 unique challenged formats (exceeding MaxChallengeBatchSize of 100)
	var formats []string
	for i := 0; i < 120; i++ {
		formats = append(formats, fmt.Sprintf(`{
			"itag": %d,
			"mimeType": "video/mp4; codecs=\"avc1.640028\"",
			"signatureCipher": "s=SIG%d&sp=sig&url=https%%3A%%2F%%2Fmedia.test%%2F%d"
		}`, 1000+i, i, i))
	}

	playerJSON := fmt.Sprintf(`{
		"videoDetails": {"videoId": "testvideoid", "title": "Large Batch Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [%s]
		}
	}`, strings.Join(formats, ","))

	var receivedBatch ChallengeBatch
	solver := &fakeSolver{
		solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
			receivedBatch = batch
			res := NewChallengeBatchResult()
			for _, s := range batch.Signatures {
				res.Signatures[s.ID] = SignatureResult{ID: s.ID, Deciphered: "solved_" + s.ID}
			}
			return res, nil
		},
	}

	transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(solver))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	totalChallenges := len(receivedBatch.Signatures) + len(receivedBatch.NParams)
	if totalChallenges > MaxChallengeBatchSize {
		t.Fatalf("batch size %d exceeded MaxChallengeBatchSize %d", totalChallenges, MaxChallengeBatchSize)
	}
	if len(media.Formats) != MaxChallengeBatchSize {
		t.Fatalf("expected %d formats extracted (capped at batch limit), got %d", MaxChallengeBatchSize, len(media.Formats))
	}
}

func TestExtractDownloadable_ConcurrencyAndSnapshot(t *testing.T) {
	playerJSON := `{
		"videoDetails": {"videoId": "testvideoid", "title": "Concurrent Stream"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 137,
					"mimeType": "video/mp4; codecs=\"avc1.640028\"",
					"signatureCipher": "s=CONCURRENT_SIG&sp=sig&url=https%3A%2F%2Fmedia.test%2F1"
				}
			]
		}
	}`

	solver1 := &fakeSolver{
		solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
			res := NewChallengeBatchResult()
			for _, s := range batch.Signatures {
				res.Signatures[s.ID] = SignatureResult{ID: s.ID, Deciphered: "solver1_sig"}
			}
			return res, nil
		},
	}

	solver2 := &fakeSolver{
		solveFunc: func(ctx context.Context, script PlayerScript, batch ChallengeBatch) (ChallengeBatchResult, error) {
			res := NewChallengeBatchResult()
			for _, s := range batch.Signatures {
				res.Signatures[s.ID] = SignatureResult{ID: s.ID, Deciphered: "solver2_sig"}
			}
			return res, nil
		},
	}

	transport := makeWatchAndPlayerTransport(playerJSON, "var script = 1;")
	ext := New(&http.Client{Transport: transport}, WithChallengeSolver(solver1))
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideoid")

	const goroutines = 20
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			// Alternate SetSolver concurrently
			if idx%2 == 0 {
				ext.SetSolver(solver2)
			} else {
				ext.SetSolver(solver1)
			}

			// Perform extraction
			media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d failed: %w", idx, err)
				return
			}
			if len(media.Formats) != 1 {
				errCh <- fmt.Errorf("goroutine %d expected 1 format, got %d", idx, len(media.Formats))
				return
			}

			// Ensure the result is from either solver1 or solver2 cleanly (no corruption)
			parsed, _ := url.Parse(media.Formats[0].Resource.URL)
			sigVal := parsed.Query().Get("sig")
			if sigVal != "solver1_sig" && sigVal != "solver2_sig" {
				errCh <- fmt.Errorf("goroutine %d got unexpected signature: %s", idx, sigVal)
				return
			}

			errCh <- nil
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlayerScript_IdentityAndIsolation(t *testing.T) {
	scriptURL := "https://www.youtube.com/s/player/v12345/base.js"
	versionID := "v12345"

	source1 := []byte("var a = 1;")
	source2 := []byte("var a = 2; // different implementation")

	ps1 := NewPlayerScript(scriptURL, versionID, source1)
	ps2 := NewPlayerScript(scriptURL, versionID, source2)

	if ps1.ContentSHA256 == ps2.ContentSHA256 {
		t.Fatalf("expected different ContentSHA256 for different sources, got identical %s", ps1.ContentSHA256)
	}

	if ps1.Identity() == ps2.Identity() {
		t.Fatalf("expected different Identity() for different script contents, got identical %s", ps1.Identity())
	}

	// Verify that Identity contains both URL and SHA256
	if !strings.Contains(ps1.Identity(), scriptURL) || !strings.Contains(ps1.Identity(), ps1.ContentSHA256) {
		t.Fatalf("Identity %q must include URL and ContentSHA256", ps1.Identity())
	}

	// Simulate solver cache keyed by PlayerScript.Identity()
	transformationCache := make(map[string]string)
	transformationCache[ps1.Identity()] = "transform_result_for_script_1"

	if result, cached := transformationCache[ps2.Identity()]; cached {
		t.Fatalf("script 2 should not have a cache hit under script 1's identity; got %q", result)
	}
}
