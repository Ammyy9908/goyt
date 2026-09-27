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

func TestConvertDirectVideo(t *testing.T) {
	format, ok := convertDirectFormat(playerFormat{
		Itag:          137,
		MIMEType:      `video/mp4; codecs="avc1.640028"`,
		URL:           "https://media.test/video?expire=2000000000",
		Height:        1080,
		Width:         1920,
		Bitrate:       2_000_000,
		ContentLength: "12345",
	}, visionOSProfile())

	if !ok {
		t.Fatal("valid direct video was rejected")
	}

	if format.ID != "137" ||
		format.VideoCodec != "avc1.640028" ||
		format.AudioCodec != "none" {
		t.Fatalf("incorrect format: %+v", format)
	}

	if format.Height == nil || *format.Height != 1080 {
		t.Fatal("missing height")
	}

	if format.SizeBytes == nil || *format.SizeBytes != 12345 {
		t.Fatal("missing size")
	}

	if format.Resource.ExpiresAt == nil {
		t.Fatal("missing expiry")
	}

	if format.Resource.Headers.Get("User-Agent") == "" {
		t.Fatal("missing client user agent")
	}
}

func TestConvertDirectAudio(t *testing.T) {
	format, ok := convertDirectFormat(playerFormat{
		Itag:          140,
		MIMEType:      `audio/mp4; codecs="mp4a.40.2"`,
		URL:           "https://media.test/audio",
		AudioChannels: 2,
	}, visionOSProfile())

	if !ok ||
		format.VideoCodec != "none" ||
		format.AudioCodec != "mp4a.40.2" {
		t.Fatalf("incorrect audio conversion: %+v", format)
	}
}

func TestConvertCombinedFormat(t *testing.T) {
	format, ok := convertDirectFormat(playerFormat{
		Itag:     18,
		MIMEType: `video/mp4; codecs="avc1.42001E, mp4a.40.2"`,
		URL:      "https://media.test/combined",
	}, visionOSProfile())

	if !ok ||
		format.VideoCodec == "none" ||
		format.AudioCodec == "none" {
		t.Fatal("combined stream was misclassified")
	}
}

func TestRejectUnsupportedDirectFormats(t *testing.T) {
	cases := []playerFormat{
		{
			MIMEType: `video/mp4; codecs="avc1.640028"`,
		},
		{
			MIMEType: `video/mp4; codecs="avc1.640028"`,
			URL:      "https://media.test/video?n=challenge",
		},
		{
			MIMEType:        `video/mp4; codecs="avc1.640028"`,
			URL:             "https://media.test/video",
			SignatureCipher: "s=challenge",
		},
		{
			MIMEType:    `video/mp4; codecs="avc1.640028"`,
			URL:         "https://media.test/video",
			DRMFamilies: []string{"reported-drm"},
		},
		{
			MIMEType: `video/mp4; codecs="unknown"`,
			URL:      "https://media.test/video",
		},
		{
			MIMEType:      `video/mp4; codecs="avc1.640028"`,
			URL:           "https://media.test/video",
			AudioChannels: 2,
		},
	}

	for i, raw := range cases {
		if _, ok := convertDirectFormat(raw, visionOSProfile()); ok {
			t.Fatalf("case %d should be rejected", i)
		}
	}
}

func TestExtractDownloadableWithClient_WebDirectURLs(t *testing.T) {
	pageHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "testvideo12", "title": "Web Test Video", "lengthSeconds": "120"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"formats": [{
				"itag": 18,
				"mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"",
				"url": "https://media.test/combined?expire=2000000000",
				"width": 640,
				"height": 360
			}],
			"adaptiveFormats": [{
				"itag": 137,
				"mimeType": "video/mp4; codecs=\"avc1.640028\"",
				"url": "https://media.test/v1080?expire=2000000000",
				"width": 1920,
				"height": 1080
			}, {
				"itag": 140,
				"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
				"url": "https://media.test/a140?expire=2000000000",
				"audioChannels": 2
			}]
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
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideo12")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("ExtractDownloadableWithClient(web) unexpected error: %v", err)
	}

	if media.ID != "testvideo12" || media.Title != "Web Test Video" {
		t.Fatalf("media metadata mismatch: %+v", media)
	}
	if len(media.Formats) != 3 {
		t.Fatalf("expected 3 usable formats, got %d", len(media.Formats))
	}
}

func TestExtractDownloadableWithClient_WebOnlyChallenges(t *testing.T) {
	pageHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "testvideo12", "title": "Challenged Video"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [{
				"itag": 137,
				"mimeType": "video/mp4; codecs=\"avc1.640028\"",
				"signatureCipher": "s=sigsecret&url=https%3A%2F%2Fmedia.test%2Fv1080"
			}, {
				"itag": 140,
				"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
				"url": "https://media.test/a140?n=nchallenge",
				"audioChannels": 2
			}]
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
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideo12")

	_, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err == nil {
		t.Fatal("expected error on web challenged formats, got nil")
	}

	var extErr *ExtractionError
	if !errors.As(err, &extErr) {
		t.Fatalf("expected *ExtractionError, got %T: %v", err, err)
	}
	if extErr.Code != ErrCodeNoSupportedFormats {
		t.Fatalf("expected code %s, got %s", ErrCodeNoSupportedFormats, extErr.Code)
	}
	if len(extErr.Challenges) != 2 {
		t.Fatalf("expected 2 challenges preserved, got %d", len(extErr.Challenges))
	}
}

func TestExtractDownloadableWithClient_PlayableSurvivesChallenged(t *testing.T) {
	// One playable audio format (140) alongside a signature-challenged video (137) and n-challenged (18)
	pageHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "testvideo12", "title": "Mixed Formats Video"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"formats": [{
				"itag": 18,
				"mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"",
				"url": "https://media.test/combined?n=nchallenge"
			}],
			"adaptiveFormats": [{
				"itag": 137,
				"mimeType": "video/mp4; codecs=\"avc1.640028\"",
				"signatureCipher": "s=sigsecret&url=https%3A%2F%2Fmedia.test%2Fv1080"
			}, {
				"itag": 140,
				"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
				"url": "https://media.test/a140?expire=2000000000",
				"audioChannels": 2
			}]
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
	u, _ := url.Parse("https://www.youtube.com/watch?v=testvideo12")

	media, err := ext.ExtractDownloadableWithClient(context.Background(), u, ClientWeb)
	if err != nil {
		t.Fatalf("playable format should survive, got error: %v", err)
	}

	if len(media.Formats) != 1 || media.Formats[0].ID != "140" {
		t.Fatalf("expected 1 surviving format (140), got %+v", media.Formats)
	}
}

func TestExtractDownloadableWithClient_OracleBotCheck(t *testing.T) {
	// Oracle Ubuntu fixture: both web and visionos return LOGIN_REQUIRED with "Sign in to confirm you’re not a bot"
	oracleResponseBody := `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in to confirm you’re not a bot","messages":["Sign in to confirm you’re not a bot. This helps protect our community."]}}`
	oracleWatchPageHTML := fmt.Sprintf(`<html><script>var ytInitialPlayerResponse = %s;</script><script>ytcfg.set({"VISITOR_DATA":"mock-visitor"});</script></html>`, oracleResponseBody)

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

	for _, clientName := range []string{ClientVisionOS, ClientWeb} {
		t.Run(clientName, func(t *testing.T) {
			_, err := ext.ExtractDownloadableWithClient(context.Background(), u, clientName)
			if err == nil {
				t.Fatalf("expected bot check error for client %s, got nil", clientName)
			}

			var extErr *ExtractionError
			if !errors.As(err, &extErr) {
				t.Fatalf("expected *ExtractionError, got %T: %v", err, err)
			}

			if extErr.Code != ErrCodeBotCheckRequired {
				t.Fatalf("expected code %s, got %s", ErrCodeBotCheckRequired, extErr.Code)
			}
			if extErr.Client != clientName {
				t.Fatalf("expected client %s, got %s", clientName, extErr.Client)
			}
			if extErr.PlaybackStatus != "LOGIN_REQUIRED" {
				t.Fatalf("expected status LOGIN_REQUIRED, got %s", extErr.PlaybackStatus)
			}
			if !strings.Contains(extErr.Message, "bot-check response") {
				t.Fatalf("expected message to mention bot check: %s", extErr.Message)
			}
		})
	}
}
