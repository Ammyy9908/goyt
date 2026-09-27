package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ammyy9908/goyt"
	"github.com/ammyy9908/goyt/extractor/youtube"
)

func TestRootHelpAndVersion(t *testing.T) {
	versionFlags := []string{"-version", "--version", "version"}
	for _, arg := range versionFlags {
		t.Run("version_"+arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{arg}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.TrimSpace(stdout.String()); got != goyt.Version {
				t.Fatalf("version = %q; want %q", got, goyt.Version)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got: %q", stderr.String())
			}
		})
	}

	helpFlags := []string{"-help", "--help", "-h", "help"}
	for _, arg := range helpFlags {
		t.Run("help_"+arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{arg}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("expected usage in stdout, got: %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "download") ||
				!strings.Contains(stdout.String(), "inspect") ||
				!strings.Contains(stdout.String(), "hls") {
				t.Fatalf("expected subcommands in help output, got: %q", stdout.String())
			}
		})
	}

	t.Run("empty args", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on empty args, got nil")
		}
		if !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("expected usage in stderr, got: %q", stderr.String())
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"foobar"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on unknown command, got nil")
		}
		if !strings.Contains(err.Error(), "unknown command \"foobar\"") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if !strings.Contains(stderr.String(), "unknown command") {
			t.Fatalf("expected unknown command in stderr, got: %q", stderr.String())
		}
	})
}

func TestSubcommandHelp(t *testing.T) {
	subcommands := []struct {
		name         string
		expectedText string
	}{
		{"download", "Usage: goyt download -url URL"},
		{"inspect", "Usage: goyt inspect -url YOUTUBE_URL"},
		{"hls", "Usage: goyt hls -url PLAYLIST_URL"},
	}

	for _, sc := range subcommands {
		t.Run(sc.name+"_help", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{sc.name, "-help"}, &stdout, &stderr)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("expected ErrHelp for %s -help, got: %v", sc.name, err)
			}
			if !strings.Contains(stderr.String(), sc.expectedText) {
				t.Fatalf("expected %q in stderr, got: %q", sc.expectedText, stderr.String())
			}
			if sc.name == "inspect" && !strings.Contains(stderr.String(), "-json") {
				t.Fatalf("expected '-json' in inspect help, got: %q", stderr.String())
			}
		})
	}
}

func TestDownloadFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty download args",
			args:    []string{"download"},
			wantErr: "usage:",
		},
		{
			name:    "missing url",
			args:    []string{"download", "-transport", "http"},
			wantErr: "usage:",
		},
		{
			name:    "unexpected positional arg",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "extra"},
			wantErr: "usage:",
		},
		{
			name:    "invalid height",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-height", "0"},
			wantErr: "height must be positive",
		},
		{
			name:    "negative height",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-height", "-10"},
			wantErr: "height must be positive",
		},
		{
			name:    "invalid transport",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "ftp"},
			wantErr: "transport must be http or hls",
		},
		{
			name:    "audio language with http transport",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "http", "-audio-language", "en"},
			wantErr: "-audio-language currently requires -transport hls",
		},
		{
			name:    "non mp4 output extension",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-out", "video.mkv"},
			wantErr: "this command requires an .mp4 output",
		},
		{
			name:    "unsupported youtube url",
			args:    []string{"download", "-url", "https://example.com/video"},
			wantErr: "unsupported YouTube URL",
		},
		{
			name:    "unsupported music playlist url",
			args:    []string{"download", "-url", "https://music.youtube.com/playlist?list=PL1234567890"},
			wantErr: "unsupported YouTube URL",
		},
		{
			name:    "unsupported lookalike music host",
			args:    []string{"download", "-url", "https://music.youtube.com.example.org/watch?v=12345678901"},
			wantErr: "unsupported YouTube URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestInspectFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty inspect args",
			args:    []string{"inspect"},
			wantErr: "usage:",
		},
		{
			name:    "missing url",
			args:    []string{"inspect", "-client", "web"},
			wantErr: "usage:",
		},
		{
			name:    "invalid client",
			args:    []string{"inspect", "-url", "https://youtube.com/watch?v=123", "-client", "android"},
			wantErr: "client must be web, visionos, or all",
		},
		{
			name:    "unsupported youtube url",
			args:    []string{"inspect", "-url", "https://example.com/video"},
			wantErr: "unsupported YouTube video URL",
		},
		{
			name:    "unsupported music playlist url in inspect",
			args:    []string{"inspect", "-url", "https://music.youtube.com/playlist?list=PL1234567890"},
			wantErr: "unsupported YouTube video URL",
		},
		{
			name:    "unsupported host with json flag",
			args:    []string{"inspect", "-url", "https://example.com/video", "-json"},
			wantErr: "unsupported YouTube video URL",
		},
		{
			name:    "invalid client with json flag",
			args:    []string{"inspect", "-url", "https://youtube.com/watch?v=12345678901", "-client", "ios", "-json"},
			wantErr: "client must be web, visionos, or all",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestHLSFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty hls args",
			args:    []string{"hls"},
			wantErr: "usage:",
		},
		{
			name:    "invalid height",
			args:    []string{"hls", "-url", "https://example.com/live.m3u8", "-height", "0"},
			wantErr: "usage:",
		},
		{
			name:    "non mp4 output extension",
			args:    []string{"hls", "-url", "https://example.com/live.m3u8", "-out", "video.ts"},
			wantErr: "this command requires an .mp4 output",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLegacyShorthandRouting(t *testing.T) {
	t.Run("legacy flag routes to download", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// Passing -url directly to root without "download" subcommand
		err := Run(context.Background(), []string{"-url", "https://example.com/video"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Should fail with download's validation error (unsupported YouTube URL)
		if !strings.Contains(err.Error(), "unsupported YouTube URL") {
			t.Fatalf("expected unsupported YouTube URL error, got: %v", err)
		}
	})

	t.Run("legacy invalid flags", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"-url", "https://youtube.com/watch?v=123", "-transport", "bad"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "transport must be http or hls") {
			t.Fatalf("expected transport error, got: %v", err)
		}
	})

	t.Run("legacy shorthand with audio-only", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"-url", "https://example.com/video", "-audio-only"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Should reach extractor validation for download command
		if !strings.Contains(err.Error(), "unsupported YouTube URL") {
			t.Fatalf("expected unsupported YouTube URL error, got: %v", err)
		}
	})
}

func TestAudioOnlyCLIFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "audio-format without audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-format", "mp3"},
			wantErr: "-audio-format, -audio-quality, and -audio-bitrate require -audio-only",
		},
		{
			name:    "audio-quality without audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-quality", "2"},
			wantErr: "-audio-format, -audio-quality, and -audio-bitrate require -audio-only",
		},
		{
			name:    "audio-bitrate without audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-bitrate", "128k"},
			wantErr: "-audio-format, -audio-quality, and -audio-bitrate require -audio-only",
		},
		{
			name:    "quality and bitrate mutually exclusive",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-quality", "2", "-audio-bitrate", "128k"},
			wantErr: "explicit -audio-quality and -audio-bitrate are mutually exclusive",
		},
		{
			name:    "explicit height with audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-height", "720"},
			wantErr: "-height is not supported in -audio-only mode",
		},
		{
			name:    "unsupported audio format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "wma"},
			wantErr: "unsupported audio format",
		},
		{
			name:    "audio quality for non-mp3 format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "aac", "-audio-quality", "2"},
			wantErr: "-audio-quality is only supported for mp3 format",
		},
		{
			name:    "audio bitrate for flac format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "flac", "-audio-bitrate", "128k"},
			wantErr: "-audio-bitrate is not supported for flac format",
		},
		{
			name:    "audio bitrate for wav format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "wav", "-audio-bitrate", "128k"},
			wantErr: "-audio-bitrate is not supported for wav format",
		},
		{
			name:    "audio bitrate for alac format",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "alac", "-audio-bitrate", "128k"},
			wantErr: "-audio-bitrate is not supported for alac format",
		},
		{
			name:    "negative audio quality",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "mp3", "-audio-quality", "-1"},
			wantErr: "audio quality must be between 0 and 9",
		},
		{
			name:    "too high audio quality",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "mp3", "-audio-quality", "10"},
			wantErr: "audio quality must be between 0 and 9",
		},
		{
			name:    "invalid audio bitrate",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "mp3", "-audio-bitrate", "invalid"},
			wantErr: "invalid audio bitrate",
		},
		{
			name:    "non-flac output for flac mode",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "flac", "-out", "song.mp3"},
			wantErr: "audio-only flac mode requires a .flac output",
		},
		{
			name:    "non-m4a output for alac mode",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "alac", "-out", "song.alac"},
			wantErr: "audio-only alac mode requires a .m4a output",
		},
		{
			name:    "non-ogg output for vorbis mode",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-audio-format", "vorbis", "-out", "song.mp3"},
			wantErr: "audio-only vorbis mode requires a .ogg output",
		},
		{
			name:    "default height with audio-only is accepted up to URL validation",
			args:    []string{"download", "-url", "https://example.com/video", "-audio-only"},
			wantErr: "unsupported YouTube URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

var transportMutex sync.Mutex

func withMockTransport(t *testing.T, fn mockRoundTripper, testBody func()) {
	t.Helper()
	transportMutex.Lock()
	defer transportMutex.Unlock()

	orig := http.DefaultTransport
	http.DefaultTransport = fn
	defer func() {
		http.DefaultTransport = orig
	}()

	testBody()
}

type failWriter struct {
	err error
}

func (fw *failWriter) Write(p []byte) (n int, err error) {
	return 0, fw.err
}

const mockWatchPageHTML = `<html><head><script>
var ytInitialPlayerResponse = {
	"videoDetails": {
		"videoId": "abcdefghijk",
		"title": "Mock YouTube Video",
		"lengthSeconds": "120"
	},
	"playabilityStatus": {
		"status": "OK"
	},
	"streamingData": {
		"formats": [
			{
				"itag": 18,
				"mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"",
				"qualityLabel": "360p",
				"height": 360,
				"width": 640,
				"bitrate": 600000,
				"url": "https://media.test/video.mp4"
			}
		],
		"adaptiveFormats": [
			{
				"itag": 137,
				"mimeType": "video/mp4; codecs=\"avc1.640028\"",
				"qualityLabel": "1080p",
				"height": 1080,
				"width": 1920,
				"bitrate": 4500000,
				"signatureCipher": "url=https%3A%2F%2Fmedia.test%2F1080.mp4&s=mockSig"
			}
		]
	}
};
ytcfg = {set: function(obj){}};
ytcfg.set({"VISITOR_DATA": "mock-visitor-123"});
</script></head><body></body></html>`

const mockInnertubePlayerJSON = `{
	"videoDetails": {
		"videoId": "abcdefghijk",
		"title": "Mock YouTube Video",
		"lengthSeconds": "120"
	},
	"playabilityStatus": {
		"status": "OK"
	},
	"streamingData": {
		"hlsManifestUrl": "https://manifest.test/master.m3u8",
		"formats": [
			{
				"itag": 18,
				"mimeType": "video/mp4",
				"qualityLabel": "360p",
				"height": 360,
				"url": "https://media.test/direct.mp4"
			}
		]
	}
}`

func mockInspectTransport(t *testing.T, visionosFails bool, restricted bool) mockRoundTripper {
	return func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			html := mockWatchPageHTML
			if restricted {
				html = `<html><script>var ytInitialPlayerResponse = {"playabilityStatus": {"status": "LOGIN_REQUIRED", "reason": "Sign in required"}};</script></html>`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/html; charset=utf-8"},
				},
				Body:    io.NopCloser(strings.NewReader(html)),
				Request: req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			if visionosFails {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header: http.Header{
						"Content-Type": []string{"application/json"},
					},
					Body:    io.NopCloser(strings.NewReader(`{"error":{"code":500,"status":"INTERNAL","message":"Internal error"}}`)),
					Request: req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body:    io.NopCloser(strings.NewReader(mockInnertubePlayerJSON)),
				Request: req,
			}, nil
		}

		if req.Method == http.MethodGet && req.URL.Host == "manifest.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/vnd.apple.mpegurl"},
				},
				Body:    io.NopCloser(strings.NewReader("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360\n360p.m3u8\n")),
				Request: req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	}
}

func TestInspectCLI_SingleClientJSON(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, false, false), func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://www.youtube.com/watch?v=abcdefghijk", "-client", "web", "-json"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected empty stderr, got: %q", stderr.String())
		}

		outStr := stdout.String()
		if !strings.HasSuffix(outStr, "\n") {
			t.Fatal("expected stdout to end with newline")
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON stdout: %v\nOutput: %s", err, outStr)
		}

		if resp.SchemaVersion != 1 {
			t.Fatalf("schema_version = %d; want 1", resp.SchemaVersion)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("results count = %d; want 1", len(resp.Results))
		}

		res := resp.Results[0]
		if res.Client != "web" || res.Status != "ok" || res.Error != nil {
			t.Fatalf("unexpected result: %+v", res)
		}
		if res.Media == nil || res.Media.ID != "abcdefghijk" || res.Media.Title != "Mock YouTube Video" {
			t.Fatalf("unexpected media: %+v", res.Media)
		}
		if res.Media.DurationSeconds == nil || *res.Media.DurationSeconds != 120.0 {
			t.Fatalf("unexpected duration: %v", res.Media.DurationSeconds)
		}
		if len(res.AvailableVideoHeights) != 2 || res.AvailableVideoHeights[0] != 360 || res.AvailableVideoHeights[1] != 1080 {
			t.Fatalf("unexpected heights: %v", res.AvailableVideoHeights)
		}
	})
}

func TestInspectCLI_YouTubeMusicJSON(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, false, false), func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://music.youtube.com/watch?v=abcdefghijk&si=track_123", "-client", "web", "-json"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected empty stderr, got: %q", stderr.String())
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON: %v", err)
		}
		if len(resp.Results) != 1 || resp.Results[0].Media.ID != "abcdefghijk" {
			t.Fatalf("unexpected result: %+v", resp)
		}
	})
}

func TestInspectCLI_AllClients_SuccessJSON(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, false, false), func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://youtu.be/abcdefghijk", "-client", "all", "-json"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected empty stderr, got: %q", stderr.String())
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON: %v", err)
		}
		if len(resp.Results) != 2 {
			t.Fatalf("expected 2 client results, got %d", len(resp.Results))
		}
		if resp.Results[0].Client != "web" || resp.Results[0].Status != "ok" {
			t.Fatalf("unexpected web result: %+v", resp.Results[0])
		}
		if resp.Results[1].Client != "visionos" || resp.Results[1].Status != "ok" {
			t.Fatalf("unexpected visionos result: %+v", resp.Results[1])
		}
	})
}

func TestInspectCLI_PartialFailure_JSON(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, true, false), func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://youtu.be/abcdefghijk", "-client", "all", "-json"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on partial failure, got nil")
		}

		outStr := stdout.String()
		if !strings.HasSuffix(outStr, "\n") {
			t.Fatal("expected stdout to end with newline")
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON stdout: %v\nOutput: %s", err, outStr)
		}

		if len(resp.Results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(resp.Results))
		}

		// web succeeded
		if resp.Results[0].Client != "web" || resp.Results[0].Status != "ok" {
			t.Fatalf("expected web ok, got: %+v", resp.Results[0])
		}

		// visionos failed
		if resp.Results[1].Client != "visionos" || resp.Results[1].Status != "error" || resp.Results[1].Error == nil {
			t.Fatalf("expected visionos error, got: %+v", resp.Results[1])
		}
		if resp.Results[1].Error.Code != "extraction_failed" {
			t.Fatalf("expected code extraction_failed, got %q", resp.Results[1].Error.Code)
		}
	})
}

func TestInspectCLI_TotalFailure_JSON(t *testing.T) {
	withMockTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader("bad gateway")),
			Request:    req,
		}, nil
	}, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://youtu.be/abcdefghijk", "-client", "web", "-json"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on total failure, got nil")
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON stdout: %v", err)
		}
		if len(resp.Results) != 1 || resp.Results[0].Status != "error" {
			t.Fatalf("expected error result: %+v", resp)
		}
	})
}

func TestInspectCLI_RestrictedPlayback_JSON(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, false, true), func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://youtu.be/abcdefghijk", "-client", "web", "-json"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error on restricted playback inspection: %v", err)
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON stdout: %v", err)
		}
		if len(resp.Results) != 1 || resp.Results[0].Status != "ok" {
			t.Fatalf("expected status 'ok', got %+v", resp)
		}
		if resp.Results[0].Playback == nil || resp.Results[0].Playback.Status != "LOGIN_REQUIRED" {
			t.Fatalf("expected LOGIN_REQUIRED, got %+v", resp.Results[0].Playback)
		}
	})
}

func TestInspectCLI_Cancellation_JSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	withMockTransport(t, mockInspectTransport(t, false, false), func() {
		var stdout, stderr bytes.Buffer
		err := Run(ctx, []string{"inspect", "-url", "https://youtu.be/abcdefghijk", "-client", "web", "-json"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on canceled context, got nil")
		}

		var resp youtube.InspectResponse
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON: %v\nStdout: %s", err, stdout.String())
		}
		if len(resp.Results) != 1 || resp.Results[0].Status != "error" {
			t.Fatalf("expected error result, got %+v", resp)
		}
		if resp.Results[0].Error == nil || resp.Results[0].Error.Code != "context_canceled" {
			t.Fatalf("expected code context_canceled, got %+v", resp.Results[0].Error)
		}
	})
}

func TestInspectCLI_WriterFailure(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, false, false), func() {
		var stderr bytes.Buffer
		fw := &failWriter{err: errors.New("disk full")}
		err := Run(context.Background(), []string{"inspect", "-url", "https://youtu.be/abcdefghijk", "-client", "web", "-json"}, fw, &stderr)
		if err == nil {
			t.Fatal("expected error on writer failure, got nil")
		}
		if !strings.Contains(err.Error(), "write inspect JSON") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestInspectCLI_TextOutputPreserved(t *testing.T) {
	withMockTransport(t, mockInspectTransport(t, false, false), func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"inspect", "-url", "https://www.youtube.com/watch?v=abcdefghijk", "-client", "web"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		outStr := stdout.String()
		if !strings.Contains(outStr, "=== web ===") {
			t.Fatalf("expected '=== web ===' in text output, got: %s", outStr)
		}
		if !strings.Contains(outStr, "ID: abcdefghijk") {
			t.Fatalf("expected 'ID: abcdefghijk' in text output, got: %s", outStr)
		}
		if !strings.Contains(outStr, "Playback status: OK") {
			t.Fatalf("expected 'Playback status: OK' in text output, got: %s", outStr)
		}
		if !strings.Contains(outStr, "ID") || !strings.Contains(outStr, "QUALITY") || !strings.Contains(outStr, "DIRECT URL") {
			t.Fatalf("expected table headers in text output, got: %s", outStr)
		}
	})
}
