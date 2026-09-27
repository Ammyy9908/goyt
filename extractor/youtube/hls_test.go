package youtube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestYouTubeManifestResource(t *testing.T) {
	resource, err := youtubeManifestResource(
		"https://manifest.test/api/expire/2000000000/index.m3u8",
		visionOSProfile(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if resource.ExpiresAt == nil ||
		resource.ExpiresAt.Unix() != 2000000000 {
		t.Fatal("manifest expiry was not parsed")
	}

	if resource.Headers.Get("User-Agent") == "" {
		t.Fatal("missing client user agent")
	}
}

func TestYouTubeManifestQueryExpiry(t *testing.T) {
	resource, err := youtubeManifestResource(
		"https://manifest.test/index.m3u8?expire=2000000000",
		visionOSProfile(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if resource.ExpiresAt == nil ||
		resource.ExpiresAt.Unix() != 2000000000 {
		t.Fatal("query expiry was not parsed")
	}
}

func TestYouTubeManifestRejectsUnsupportedURLs(t *testing.T) {
	for _, input := range []string{
		"",
		"/relative.m3u8",
		"http://manifest.test/index.m3u8",
		"https://user:password@manifest.test/index.m3u8",
		"https://manifest.test/index.m3u8?n=challenge",
		"https://manifest.test/api/n/challenge/index.m3u8",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := youtubeManifestResource(
				input,
				visionOSProfile(),
			); err == nil {
				t.Fatal("unsupported URL was accepted")
			}
		})
	}
}

func TestExtractHLSWithClient_Present(t *testing.T) {
	pageHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "hlsvideo123", "title": "HLS Video", "lengthSeconds": "120"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"hlsManifestUrl": "https://manifest.test/index.m3u8?expire=2000000000"
		}
	};</script></html>`

	client := &http.Client{
		Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(pageHTML)),
				Request:    req,
			}, nil
		}),
	}

	ext := New(client)
	u, _ := url.Parse("https://www.youtube.com/watch?v=hlsvideo123")

	res, err := ext.ExtractHLSWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("ExtractHLSWithClient(web) unexpected error: %v", err)
	}

	if res.Media.ID != "hlsvideo123" || res.Media.Title != "HLS Video" {
		t.Fatalf("unexpected media: %+v", res.Media)
	}
	if res.Manifest.URL != "https://manifest.test/index.m3u8?expire=2000000000" {
		t.Fatalf("unexpected manifest URL: %s", res.Manifest.URL)
	}
}

func TestExtractHLSWithClient_Absent(t *testing.T) {
	pageHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "hlsvideo123", "title": "No HLS Video"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {}
	};</script></html>`

	client := &http.Client{
		Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(pageHTML)),
				Request:    req,
			}, nil
		}),
	}

	ext := New(client)
	u, _ := url.Parse("https://www.youtube.com/watch?v=hlsvideo123")

	_, err := ext.ExtractHLSWithClient(context.Background(), u, ClientWeb)
	if err == nil {
		t.Fatal("expected manifest_unavailable error, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected *ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeManifestUnavailable {
		t.Fatalf("expected code %s, got %s", ErrCodeManifestUnavailable, extErr.Code)
	}
}

func TestExtractHLSWithClient_NChallenge(t *testing.T) {
	pageHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "hlsvideo123", "title": "Challenged HLS"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"hlsManifestUrl": "https://manifest.test/index.m3u8?n=challenge_param"
		}
	};</script></html>`

	client := &http.Client{
		Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(pageHTML)),
				Request:    req,
			}, nil
		}),
	}

	ext := New(client)
	u, _ := url.Parse("https://www.youtube.com/watch?v=hlsvideo123")

	_, err := ext.ExtractHLSWithClient(context.Background(), u, ClientWeb)
	if err == nil {
		t.Fatal("expected error on n-challenged HLS URL, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected *ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeNChallengeRequired {
		t.Fatalf("expected code %s, got %s", ErrCodeNChallengeRequired, extErr.Code)
	}
}

func TestExtractHLSWithClient_OracleBotCheck(t *testing.T) {
	oracleResponseBody := `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in to confirm you’re not a bot"}}`
	oracleWatchPageHTML := `<html><script>ytcfg.set({"VISITOR_DATA":"mock-visitor"});</script></html>`

	client := &http.Client{
		Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader(oracleWatchPageHTML)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(oracleResponseBody)),
				Request:    req,
			}, nil
		}),
	}

	ext := New(client)
	u, _ := url.Parse("https://www.youtube.com/watch?v=oracle12345")

	_, err := ext.ExtractHLSWithClient(context.Background(), u, ClientVisionOS)
	if err == nil {
		t.Fatal("expected bot check error, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected *ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeBotCheckRequired {
		t.Fatalf("expected code %s, got %s", ErrCodeBotCheckRequired, extErr.Code)
	}
}
