package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
			if (sc.name == "download" || sc.name == "hls") &&
				(!strings.Contains(stderr.String(), "-timeout") || !strings.Contains(stderr.String(), "-stall-timeout")) {
				t.Fatalf("expected '-timeout' and '-stall-timeout' in %s help, got: %q", sc.name, stderr.String())
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
			name:    "invalid output extension",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-out", "video.avi"},
			wantErr: "this command requires an .mp4, .webm, or .mkv output",
		},
		{
			name:    "unsupported video codec",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-video-codec", "prores"},
			wantErr: "unsupported video codec \"prores\"",
		},
		{
			name:    "unsupported container",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-container", "avi"},
			wantErr: "unsupported container \"avi\"",
		},
		{
			name:    "explicit container and output extension mismatch",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-container", "webm", "-out", "video.mp4"},
			wantErr: "output extension \".mp4\" does not match explicit container \"webm\"",
		},
		{
			name:    "incompatible video codec and container combination",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-video-codec", "vp9", "-container", "mp4"},
			wantErr: "incompatible video codec \"vp9\" for container \"mp4\"",
		},
		{
			name:    "hls rejects vp9 video codec",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "hls", "-video-codec", "vp9"},
			wantErr: "HLS video download currently only supports H.264 video",
		},
		{
			name:    "hls rejects av1 video codec",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "hls", "-video-codec", "av1"},
			wantErr: "HLS video download currently only supports H.264 video",
		},
		{
			name:    "hls rejects webm container",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-transport", "hls", "-container", "webm"},
			wantErr: "HLS video download does not support WebM container",
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
		{
			name:    "negative timeout in download",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=12345678901", "-timeout", "-5m"},
			wantErr: "timeout cannot be negative",
		},
		{
			name:    "negative stall-timeout in download",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=12345678901", "-stall-timeout", "-10s"},
			wantErr: "stall-timeout cannot be negative",
		},
		{
			name:    "negative url-refreshes in download",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=12345678901", "-url-refreshes", "-1"},
			wantErr: "url-refreshes cannot be negative",
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
		{
			name:    "negative timeout in hls",
			args:    []string{"hls", "-url", "https://example.com/live.m3u8", "-timeout", "-1m"},
			wantErr: "timeout cannot be negative",
		},
		{
			name:    "negative stall-timeout in hls",
			args:    []string{"hls", "-url", "https://example.com/live.m3u8", "-stall-timeout", "-30s"},
			wantErr: "stall-timeout cannot be negative",
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
			name:    "video codec with audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-video-codec", "vp9"},
			wantErr: "-video-codec and -container are not supported in -audio-only mode",
		},
		{
			name:    "container with audio-only",
			args:    []string{"download", "-url", "https://youtube.com/watch?v=123", "-audio-only", "-container", "mkv"},
			wantErr: "-video-codec and -container are not supported in -audio-only mode",
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

func makeMockWatchPageWithDurationHTML(videoID, itag18URL, durationSeconds string) string {
	return fmt.Sprintf(`<html><head><script>
var ytInitialPlayerResponse = {
	"videoDetails": {
		"videoId": %q,
		"title": "Mock YouTube Video",
		"lengthSeconds": %q
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
				"url": %q
			}
		]
	}
};
ytcfg = {set: function(obj){}};
ytcfg.set({"VISITOR_DATA": "mock-visitor-123"});
</script></head><body></body></html>`, videoID, durationSeconds, itag18URL)
}

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
		if resp.Results[1].Error.Code != youtube.ErrCodeExtractionRequestFailed {
			t.Fatalf("expected code %s, got %q", youtube.ErrCodeExtractionRequestFailed, resp.Results[1].Error.Code)
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

func makeMockPlayerJSON(videoID, itag18URL string) string {
	return makeMockPlayerWithDurationJSON(videoID, itag18URL, "120")
}

func makeMockPlayerWithDurationJSON(videoID, itag18URL, durationSeconds string) string {
	return fmt.Sprintf(`{
	"videoDetails": {
		"videoId": %q,
		"title": "Mock YouTube Video",
		"lengthSeconds": %q
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
				"url": %q
			}
		]
	}
}`, videoID, durationSeconds, itag18URL)
}

func TestDownloadCLI_403Refresh_Recovery(t *testing.T) {
	var extractionCount atomic.Int32
	var mediaOldCount atomic.Int32
	var mediaNewCount atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			count := extractionCount.Add(1)
			url := "https://media.test/old_url.mp4"
			if count > 1 {
				url = "https://media.test/new_url.mp4"
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", url))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://media.test/old_url.mp4" {
			mediaOldCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://media.test/new_url.mp4" {
			mediaNewCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden, // 403 on replacement stops because budget=1
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "http",
			"-url-refreshes", "1",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if err == nil {
			t.Fatal("expected error on final 403, got nil")
		}

		if extractionCount.Load() != 2 {
			t.Fatalf("expected 2 extractions (initial + 1 refresh), got %d", extractionCount.Load())
		}
		if mediaOldCount.Load() != 1 {
			t.Fatalf("expected 1 request to old URL, got %d", mediaOldCount.Load())
		}
		if mediaNewCount.Load() != 1 {
			t.Fatalf("expected 1 request to new refreshed URL, got %d", mediaNewCount.Load())
		}

		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "Refreshing playback URLs after media access failure (1/1).") {
			t.Fatalf("expected refresh notice in stderr, got: %s", stderrStr)
		}
		if !strings.Contains(stderrStr, "Restarting the affected transfer.") {
			t.Fatalf("expected restart notice in stderr, got: %s", stderrStr)
		}
	})
}

func TestDownloadCLI_RefreshDisabled_NoReExtraction(t *testing.T) {
	var extractionCount atomic.Int32
	var mediaCount atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			extractionCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", "https://media.test/video.mp4"))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://media.test/video.mp4" {
			mediaCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "http",
			"-url-refreshes", "0",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if err == nil {
			t.Fatal("expected error on 403 when refresh is disabled, got nil")
		}

		if extractionCount.Load() != 1 {
			t.Fatalf("expected exactly 1 extraction when refresh is disabled, got %d", extractionCount.Load())
		}
		if mediaCount.Load() != 1 {
			t.Fatalf("expected exactly 1 media request, got %d", mediaCount.Load())
		}
		if strings.Contains(stderr.String(), "Refreshing playback URLs") {
			t.Fatalf("unexpected refresh notice in stderr when refresh is disabled: %s", stderr.String())
		}
	})
}

func TestDownloadCLI_RefreshedVideoIDMismatch(t *testing.T) {
	var extractionCount atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			count := extractionCount.Add(1)
			if count == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", "https://media.test/video.mp4"))),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("different123", "https://media.test/video2.mp4"))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://media.test/video.mp4" {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "http",
			"-url-refreshes", "1",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if err == nil {
			t.Fatal("expected error on video ID mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "different video ID") && !strings.Contains(err.Error(), "does not match original") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestDownloadCLI_PreflightExpiredResourceRefreshes(t *testing.T) {
	var extractionCount atomic.Int32
	var mediaCount atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			count := extractionCount.Add(1)
			if count == 1 {
				// Expired timestamp (year 1970)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", "https://media.test/video.mp4?expire=1000"))),
					Request:    req,
				}, nil
			}
			// Fresh timestamp (year 2033)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", "https://media.test/video_fresh.mp4?expire=2000000000"))),
				Request:    req,
			}, nil
		}

		if req.URL.Path == "/video_fresh.mp4" {
			mediaCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden, // stops after 1 refresh because budget=1
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "http",
			"-url-refreshes", "1",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if err == nil {
			t.Fatal("expected error on final 403, got nil")
		}

		if extractionCount.Load() != 2 {
			t.Fatalf("expected 2 extractions (initial expired + 1 preflight refresh), got %d", extractionCount.Load())
		}
		if mediaCount.Load() != 1 {
			t.Fatalf("expected 1 media request to refreshed fresh URL, got %d", mediaCount.Load())
		}

		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "Refreshing playback URLs after media access failure (1/1).") {
			t.Fatalf("expected refresh notice in stderr, got: %s", stderrStr)
		}
	})
}

func TestDownloadCLI_Repeated403_ExhaustsBudget(t *testing.T) {
	var extractionCount atomic.Int32
	var mediaRequests atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			count := extractionCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", fmt.Sprintf("https://media.test/video_%d.mp4", count)))),
				Request:    req,
			}, nil
		}

		if strings.HasPrefix(req.URL.String(), "https://media.test/video_") {
			mediaRequests.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "http",
			"-url-refreshes", "2",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if err == nil {
			t.Fatal("expected error when budget exhausted, got nil")
		}

		// Initial + 2 refreshes = 3 extractions and 3 media requests
		if extractionCount.Load() != 3 {
			t.Fatalf("expected 3 extractions, got %d", extractionCount.Load())
		}
		if mediaRequests.Load() != 3 {
			t.Fatalf("expected 3 media requests, got %d", mediaRequests.Load())
		}

		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "Refreshing playback URLs after media access failure (1/2).") {
			t.Fatalf("expected (1/2) notice, got: %s", stderrStr)
		}
		if !strings.Contains(stderrStr, "Refreshing playback URLs after media access failure (2/2).") {
			t.Fatalf("expected (2/2) notice, got: %s", stderrStr)
		}
	})
}

func TestDownloadCLI_Cancellation_InterruptsRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var extractionCount atomic.Int32
	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			extractionCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerJSON("abcdefghijk", "https://media.test/video.mp4"))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://media.test/video.mp4" {
			// Cancel context on first media request
			cancel()
			return nil, context.Canceled
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(ctx, []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "http",
			"-url-refreshes", "5",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if extractionCount.Load() != 1 {
			t.Fatalf("expected 1 extraction before cancellation, got %d", extractionCount.Load())
		}
	})
}

func makeMockPlayerHLSJSON(videoID, hlsManifestURL string) string {
	return fmt.Sprintf(`{
	"videoDetails": {
		"videoId": %q,
		"title": "Mock YouTube Video",
		"lengthSeconds": "120"
	},
	"playabilityStatus": {
		"status": "OK"
	},
	"streamingData": {
		"hlsManifestUrl": %q
	}
}`, videoID, hlsManifestURL)
}

func TestDownloadCLI_HLSPlaylist403_Recovery(t *testing.T) {
	var extractionCount atomic.Int32
	var manifest1Requests atomic.Int32
	var manifest2Requests atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			count := extractionCount.Add(1)
			manifestURL := "https://manifest.test/master_1.m3u8"
			if count > 1 {
				manifestURL = "https://manifest.test/master_2.m3u8"
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerHLSJSON("abcdefghijk", manifestURL))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://manifest.test/master_1.m3u8" {
			manifest1Requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://manifest.test/master_2.m3u8" {
			manifest2Requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusForbidden, // stops after 1 refresh probe
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "hls",
			"-url-refreshes", "1",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if err == nil {
			t.Fatal("expected error on final 403, got nil")
		}

		if extractionCount.Load() != 2 {
			t.Fatalf("expected 2 extractions (initial + 1 refresh), got %d", extractionCount.Load())
		}
		if manifest1Requests.Load() != 1 {
			t.Fatalf("expected 1 request to manifest 1, got %d", manifest1Requests.Load())
		}
		if manifest2Requests.Load() != 1 {
			t.Fatalf("expected 1 request to manifest 2, got %d", manifest2Requests.Load())
		}

		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "Refreshing playback URLs after media access failure (1/1).") {
			t.Fatalf("expected refresh notice in stderr, got: %s", stderrStr)
		}
		if !strings.Contains(stderrStr, "Restarting the affected transfer.") {
			t.Fatalf("expected restart notice in stderr, got: %s", stderrStr)
		}
	})
}

func TestDownloadCLI_HLSCancellation_NoReExtraction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var extractionCount atomic.Int32
	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			extractionCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerHLSJSON("abcdefghijk", "https://manifest.test/master.m3u8"))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://manifest.test/master.m3u8" {
			// Cancel context during manifest fetch
			cancel()
			return nil, context.Canceled
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(ctx, []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-transport", "hls",
			"-url-refreshes", "5",
			"-out", filepath.Join(t.TempDir(), "output.mp4"),
		}, &stdout, &stderr)

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if extractionCount.Load() != 1 {
			t.Fatalf("expected 1 extraction before cancellation, got %d", extractionCount.Load())
		}
	})
}

func TestDownloadCLI_JobDir_FlagsValidation(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("job-dir and resume-job mutually exclusive", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-job-dir", filepath.Join(tempDir, "job1"),
			"-resume-job", filepath.Join(tempDir, "job2"),
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on mutually exclusive flags, got nil")
		}
		if !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("job-dir fails if directory already exists", func(t *testing.T) {
		existingDir := filepath.Join(tempDir, "existing-job")
		if err := os.MkdirAll(existingDir, 0700); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-job-dir", existingDir,
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error when job-dir already exists, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("resume-job fails if directory not found", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-resume-job", filepath.Join(tempDir, "nonexistent-job"),
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error when resume-job not found, got nil")
		}
		if !errors.Is(err, goyt.ErrJobNotFound) && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("resume-job rejects source and selection overrides", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "sample-job")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}

		tests := []struct {
			flagName string
			args     []string
		}{
			{"url", []string{"-url", "https://youtube.com/watch?v=123"}},
			{"out", []string{"-out", "other.mp4"}},
			{"height", []string{"-height", "720"}},
			{"video-codec", []string{"-video-codec", "vp9"}},
			{"container", []string{"-container", "webm"}},
			{"transport", []string{"-transport", "http"}},
			{"decode-check", []string{"-decode-check"}},
			{"audio-only", []string{"-audio-only"}},
			{"audio-format", []string{"-audio-format", "mp3"}},
			{"audio-quality", []string{"-audio-quality", "2"}},
			{"audio-bitrate", []string{"-audio-bitrate", "128k"}},
			{"audio-language", []string{"-audio-language", "en"}},
		}

		for _, tc := range tests {
			t.Run(tc.flagName, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				args := append([]string{"download", "-resume-job", jobDir}, tc.args...)
				err := Run(context.Background(), args, &stdout, &stderr)
				if err == nil {
					t.Fatalf("expected error overriding flag %s on resume-job, got nil", tc.flagName)
				}
				if !strings.Contains(err.Error(), "cannot be specified with -resume-job") {
					t.Fatalf("unexpected error message: %v", err)
				}
			})
		}
	})

	t.Run("goyt hls rejects job-dir and resume-job", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"hls",
			"-url", "https://manifest.test/playlist.m3u8",
			"-job-dir", filepath.Join(tempDir, "hls-job-dir"),
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for job-dir in goyt hls, got nil")
		}
		if !strings.Contains(err.Error(), "not supported in goyt hls") {
			t.Fatalf("unexpected error message: %v", err)
		}

		err = Run(context.Background(), []string{
			"hls",
			"-url", "https://manifest.test/playlist.m3u8",
			"-resume-job", filepath.Join(tempDir, "hls-resume-job"),
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for resume-job in goyt hls, got nil")
		}
		if !strings.Contains(err.Error(), "not supported in goyt hls") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestDownloadCLI_PersistentJob_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	jobDir := filepath.Join(tempDir, "cli-persistent-job")
	outFile := filepath.Join(tempDir, "cli-output.mp4")
	sourceMP4 := filepath.Join(tempDir, "source.mp4")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=c=blue:s=640x360:r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", sourceMP4)
	if err := cmd.Run(); err != nil {
		t.Skip("ffmpeg not available for CLI test:", err)
	}
	mockMediaData, err := os.ReadFile(sourceMP4)
	if err != nil {
		t.Fatal(err)
	}

	var mediaRequests atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerWithDurationJSON("abcdefghijk", "https://media.test/video.mp4", "1"))),
				Request:    req,
			}, nil
		}

		if req.URL.String() == "https://media.test/video.mp4" {
			count := mediaRequests.Add(1)
			if count <= 3 {
				// Abort on initial run requests (attempt 0, 1, 2)
				return &http.Response{
					StatusCode: http.StatusGatewayTimeout,
					Status:     "504 Gateway Timeout",
					Body:       io.NopCloser(strings.NewReader("timeout")),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(mockMediaData)),
				Header: http.Header{
					"Content-Type": []string{"video/mp4"},
				},
				Body:    io.NopCloser(bytes.NewReader(mockMediaData)),
				Request: req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		// Run 1: Initial creation fails on media request
		var stdout1, stderr1 bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-job-dir", jobDir,
			"-out", outFile,
		}, &stdout1, &stderr1)
		if err == nil {
			t.Fatal("expected error on run 1, got nil")
		}

		stderr1Str := stderr1.String()
		if !strings.Contains(stderr1Str, "To resume this job, run:") || !strings.Contains(stderr1Str, "download -resume-job") {
			t.Fatalf("expected resume instructions in stderr, got: %s", stderr1Str)
		}

		// Verify job manifest was created
		manifest, err := goyt.LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load saved job manifest: %v", err)
		}
		if manifest.SchemaVersion != 1 {
			t.Fatalf("unexpected schema version: %d", manifest.SchemaVersion)
		}
		if manifest.VideoID != "abcdefghijk" {
			t.Fatalf("unexpected video ID: %s", manifest.VideoID)
		}

		// Run 2: Resume with -resume-job succeeds
		var stdout2, stderr2 bytes.Buffer
		err = Run(context.Background(), []string{
			"download",
			"-resume-job", jobDir,
		}, &stdout2, &stderr2)
		if err != nil {
			t.Fatalf("unexpected error resuming job: %v", err)
		}

		stdout2Str := stdout2.String()
		if !strings.Contains(stdout2Str, "Saved and verified:") {
			t.Fatalf("expected success message in stdout, got: %s", stdout2Str)
		}

		// Verify output file exists
		data, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, mockMediaData) {
			t.Fatal("saved output data does not match source media data")
		}

		// Run 3: Idempotent resume of completed job
		initialReqs := mediaRequests.Load()
		var stdout3, stderr3 bytes.Buffer
		err = Run(context.Background(), []string{
			"download",
			"-resume-job", jobDir,
		}, &stdout3, &stderr3)
		if err != nil {
			t.Fatalf("unexpected error on repeated resume: %v", err)
		}
		if mediaRequests.Load() != initialReqs {
			t.Fatalf("expected 0 additional requests on completed job resume, got %d", mediaRequests.Load()-initialReqs)
		}
		if !strings.Contains(stdout3.String(), "Job already completed.") {
			t.Fatalf("expected 'Job already completed.' notice, got: %s", stdout3.String())
		}
	})
}

func TestDownloadCLI_ClientFlagValidation(t *testing.T) {
	tempDir := t.TempDir()
	outFile := filepath.Join(tempDir, "out.mp4")

	t.Run("rejects client all on download", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-client", "all",
			"-out", outFile,
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for -client all on download, got nil")
		}
		if !strings.Contains(err.Error(), "unsupported client") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects unknown client on download", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-client", "android",
			"-out", outFile,
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for -client android on download, got nil")
		}
		if !strings.Contains(err.Error(), "unsupported client") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects -client on -resume-job", func(t *testing.T) {
		jobDir := filepath.Join(tempDir, "dummy-job")
		if err := os.MkdirAll(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-resume-job", jobDir,
			"-client", "web",
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error specifying -client with -resume-job, got nil")
		}
		if !strings.Contains(err.Error(), "flag -client cannot be specified with -resume-job") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDownloadCLI_ClientRouting(t *testing.T) {
	tempDir := t.TempDir()
	sourceMP4 := filepath.Join(tempDir, "source.mp4")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=c=blue:s=640x360:r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", sourceMP4)
	if err := cmd.Run(); err != nil {
		t.Skip("ffmpeg not available for CLI test:", err)
	}
	mockMediaData, err := os.ReadFile(sourceMP4)
	if err != nil {
		t.Fatal(err)
	}

	var visionOSCalls atomic.Int32
	var webCalls atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		// Web watch-page GET
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			webCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(makeMockWatchPageWithDurationHTML("abcdefghijk", "https://media.test/video.mp4", "1"))),
				Request:    req,
			}, nil
		}

		// VisionOS Innertube POST
		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			visionOSCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerWithDurationJSON("abcdefghijk", "https://media.test/video.mp4", "1"))),
				Request:    req,
			}, nil
		}

		// Media request
		if req.URL.Host == "media.test" {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Length": []string{fmt.Sprintf("%d", len(mockMediaData))}},
				Body:          io.NopCloser(bytes.NewReader(mockMediaData)),
				ContentLength: int64(len(mockMediaData)),
				Request:       req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	t.Run("default download client is visionos", func(t *testing.T) {
		visionOSCalls.Store(0)
		webCalls.Store(0)

		withMockTransport(t, transport, func() {
			outFile := filepath.Join(tempDir, "default-visionos.mp4")
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{
				"download",
				"-url", "https://www.youtube.com/watch?v=abcdefghijk",
				"-out", outFile,
			}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if visionOSCalls.Load() == 0 {
				t.Fatal("expected visionos client to be called by default")
			}
		})
	})

	t.Run("explicit web download client routes to web", func(t *testing.T) {
		visionOSCalls.Store(0)
		webCalls.Store(0)

		withMockTransport(t, transport, func() {
			outFile := filepath.Join(tempDir, "explicit-web.mp4")
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{
				"download",
				"-url", "https://www.youtube.com/watch?v=abcdefghijk",
				"-client", "web",
				"-out", outFile,
			}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if webCalls.Load() == 0 {
				t.Fatal("expected web client to be called with -client web")
			}
			if visionOSCalls.Load() != 0 {
				t.Fatalf("expected visionos not to be called with -client web, got %d calls", visionOSCalls.Load())
			}
		})
	})
}

func TestDownloadCLI_PersistentJob_ClientPreservation(t *testing.T) {
	tempDir := t.TempDir()
	jobDir := filepath.Join(tempDir, "job-web-persist")
	outFile := filepath.Join(tempDir, "web-persist.mp4")
	sourceMP4 := filepath.Join(tempDir, "source-persist.mp4")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=c=blue:s=640x360:r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", sourceMP4)
	if err := cmd.Run(); err != nil {
		t.Skip("ffmpeg not available for CLI test:", err)
	}
	mockMediaData, err := os.ReadFile(sourceMP4)
	if err != nil {
		t.Fatal(err)
	}

	var visionOSCalls atomic.Int32
	var webCalls atomic.Int32
	var mediaCalls atomic.Int32

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			webCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(makeMockWatchPageWithDurationHTML("abcdefghijk", "https://media.test/video.mp4", "1"))),
				Request:    req,
			}, nil
		}

		if req.Method == http.MethodPost && req.URL.String() == "https://www.youtube.com/youtubei/v1/player?prettyPrint=false" {
			visionOSCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(makeMockPlayerWithDurationJSON("abcdefghijk", "https://media.test/video.mp4", "1"))),
				Request:    req,
			}, nil
		}

		if req.URL.Host == "media.test" {
			count := mediaCalls.Add(1)
			if count == 1 {
				// Simulate failure on initial attempt to force pause or resume
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("gateway error")),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Length": []string{fmt.Sprintf("%d", len(mockMediaData))}},
				Body:          io.NopCloser(bytes.NewReader(mockMediaData)),
				ContentLength: int64(len(mockMediaData)),
				Request:       req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		// Run 1: Create persistent job with -client web
		var stdout1, stderr1 bytes.Buffer
		_ = Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=abcdefghijk",
			"-client", "web",
			"-job-dir", jobDir,
			"-out", outFile,
		}, &stdout1, &stderr1)

		manifest, err := goyt.LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}
		if manifest.Client != "web" {
			t.Fatalf("expected saved manifest client 'web', got %q", manifest.Client)
		}

		// Reset counters before resume
		visionOSCalls.Store(0)
		webCalls.Store(0)

		// Run 2: Resume job (no -client specified)
		var stdout2, stderr2 bytes.Buffer
		err = Run(context.Background(), []string{
			"download",
			"-resume-job", jobDir,
		}, &stdout2, &stderr2)
		if err != nil {
			t.Fatalf("unexpected error resuming job: %v", err)
		}

		if visionOSCalls.Load() != 0 {
			t.Fatalf("resume must not switch client to visionos, got %d calls", visionOSCalls.Load())
		}
	})
}

func TestDownloadCLI_OracleBotCheck_SafeDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	outFile := filepath.Join(tempDir, "oracle.mp4")

	oracleResponseBody := `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in to confirm you’re not a bot","messages":["Sign in to confirm you’re not a bot. This helps protect our community."]}}`

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchPageHTML)),
				Request:    req,
			}, nil
		}
		if req.Method == http.MethodPost {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(oracleResponseBody)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=oracle12345",
			"-out", outFile,
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error on Oracle bot check response, got nil")
		}

		errStr := err.Error()
		if !strings.Contains(errStr, "bot-check response") {
			t.Fatalf("expected bot-check explanation in error, got: %s", errStr)
		}
		if !strings.Contains(errStr, "visionos") {
			t.Fatalf("expected client name in error, got: %s", errStr)
		}

		// Ensure no secrets or raw JSON leaked
		if strings.Contains(errStr, "protected our community") || strings.Contains(errStr, `"playabilityStatus"`) {
			t.Fatalf("error exposed raw response body: %s", errStr)
		}
	})
}

func TestJSRuntimeFlagValidation(t *testing.T) {
	t.Run("download rejects nonexistent runtime", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			"-js-runtime", "nonexistent_js_binary_xyz",
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for nonexistent runtime, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent_js_binary_xyz") && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected actionable error message, got: %v", err)
		}
	})

	t.Run("inspect rejects nonexistent runtime", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"inspect",
			"-url", "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			"-js-runtime", "nonexistent_js_binary_xyz",
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for nonexistent runtime, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent_js_binary_xyz") && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected actionable error message, got: %v", err)
		}
	})

	t.Run("download help includes js-runtime", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		_ = Run(context.Background(), []string{"download", "-help"}, &stdout, &stderr)
		out := stdout.String() + stderr.String()
		if !strings.Contains(out, "-js-runtime") {
			t.Fatalf("expected -js-runtime in download help, got: %s", out)
		}
	})

	t.Run("inspect help includes js-runtime", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		_ = Run(context.Background(), []string{"inspect", "-help"}, &stdout, &stderr)
		out := stdout.String() + stderr.String()
		if !strings.Contains(out, "-js-runtime") {
			t.Fatalf("expected -js-runtime in inspect help, got: %s", out)
		}
	})
}

func TestDownloadCLI_SelectedFormatDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	outFile := filepath.Join(tempDir, "diag-test.mp4")
	sourceMP4 := filepath.Join(tempDir, "source-diag.mp4")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=c=blue:s=640x360:r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", sourceMP4)
	if err := cmd.Run(); err != nil {
		t.Skip("ffmpeg not available for CLI test:", err)
	}
	mockMediaData, err := os.ReadFile(sourceMP4)
	if err != nil {
		t.Fatal(err)
	}

	watchHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "diagtest123", "title": "Diagnostic Test Video", "lengthSeconds": "1"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"formats": [{
				"itag": 18,
				"mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"",
				"url": "https://media.test/video.mp4?expire=2000000000",
				"width": 640,
				"height": 360
			}]
		}
	};</script></html>`

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
				Request:    req,
			}, nil
		}
		if req.URL.Host == "media.test" {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"video/mp4"}},
				Body:          io.NopCloser(bytes.NewReader(mockMediaData)),
				ContentLength: int64(len(mockMediaData)),
				Request:       req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=diagtest123",
			"-client", "web",
			"-height", "360",
			"-out", outFile,
		}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected download error: %v", err)
		}

		stderrStr := stderr.String()
		stdoutStr := stdout.String()

		// Verify stderr contains sanitized format diagnostics
		expectedDiag := "Format 18: signature=not_required, n=not_required"
		if !strings.Contains(stderrStr, expectedDiag) {
			t.Fatalf("expected stderr to contain %q, got:\n%s", expectedDiag, stderrStr)
		}

		// Verify stdout does NOT contain solver diagnostics
		if strings.Contains(stdoutStr, "signature=") || strings.Contains(stdoutStr, "Format 18: signature=") {
			t.Fatalf("stdout polluted with format diagnostics:\n%s", stdoutStr)
		}

		// Verify no sensitive tokens leak in stdout or stderr
		for _, out := range []string{stdoutStr, stderrStr} {
			if strings.Contains(out, "ytInitialPlayerResponse") || strings.Contains(out, "media.test") {
				t.Fatalf("output leaked sensitive info:\n%s", out)
			}
		}
	})
}

func TestDownloadCLI_MultiTrackAudioDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	outFile := filepath.Join(tempDir, "audio-multi.m4a")
	jobDir := filepath.Join(tempDir, "audio-multi-job")
	sourceM4A := filepath.Join(tempDir, "source-multi.m4a")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:a", "aac", sourceM4A)
	if err := cmd.Run(); err != nil {
		t.Skip("ffmpeg not available for CLI test:", err)
	}
	mockMediaData, err := os.ReadFile(sourceM4A)
	if err != nil {
		t.Fatal(err)
	}

	watchHTML := `<html><script>var ytInitialPlayerResponse = {
		"videoDetails": {"videoId": "multitrack1", "title": "Multi-Track Audio Video", "lengthSeconds": "1"},
		"playabilityStatus": {"status": "OK"},
		"streamingData": {
			"adaptiveFormats": [
				{
					"itag": 140,
					"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
					"url": "https://media.test/a_en.m4a?expire=2000000000",
					"audioChannels": 2,
					"audioTrack": {
						"id": "en.4",
						"displayName": "English (original)",
						"audioIsDefault": true
					}
				},
				{
					"itag": 140,
					"mimeType": "audio/mp4; codecs=\"mp4a.40.2\"",
					"url": "https://media.test/a_es.m4a?expire=2000000000",
					"audioChannels": 2,
					"audioTrack": {
						"id": "es.4",
						"displayName": "Spanish",
						"audioIsDefault": false
					}
				}
			]
		}
	};</script></html>`

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Host == "www.youtube.com" && req.URL.Path == "/watch" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(watchHTML)),
				Request:    req,
			}, nil
		}
		if req.URL.Host == "media.test" {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"audio/mp4"}},
				Body:          io.NopCloser(bytes.NewReader(mockMediaData)),
				ContentLength: int64(len(mockMediaData)),
				Request:       req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	})

	withMockTransport(t, transport, func() {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{
			"download",
			"-url", "https://www.youtube.com/watch?v=multitrack1",
			"-audio-only",
			"-audio-format", "best",
			"-client", "web",
			"-job-dir", jobDir,
			"-out", outFile,
		}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected download error: %v", err)
		}

		stderrStr := stderr.String()

		// Verify stderr contains track-specific diagnostic attribution
		expectedDiag := "Format 140 (en.4): signature=not_required, n=not_required"
		if !strings.Contains(stderrStr, expectedDiag) {
			t.Fatalf("expected stderr to contain %q, got:\n%s", expectedDiag, stderrStr)
		}

		// Verify manifest audio track information
		loadedManifest, err := goyt.LoadJobManifest(jobDir)
		if err != nil {
			t.Fatalf("failed to load manifest: %v", err)
		}
		if len(loadedManifest.Streams) == 0 || loadedManifest.Streams[0].Format.AudioTrackID != "en.4" || loadedManifest.Streams[0].Format.AudioTrackName != "English (original)" {
			t.Fatalf("expected manifest audio track en.4 English (original), got streams: %+v", loadedManifest.Streams)
		}

		// Verify manifest does NOT persist transient diagnostics
		manifestBytes, err := os.ReadFile(filepath.Join(jobDir, "job.json"))
		if err != nil {
			t.Fatalf("failed to read manifest: %v", err)
		}
		manifestStr := string(manifestBytes)
		for _, transientTerm := range []string{"signature=", "not_required", "resolved(", "unresolved"} {
			if strings.Contains(manifestStr, transientTerm) {
				t.Fatalf("job manifest persisted transient diagnostic term %q:\n%s", transientTerm, manifestStr)
			}
		}
	})
}

func TestInspect_LimitationsOutput_WebAndVisionOS(t *testing.T) {
	staleLimitation := "No JavaScript challenge solver or PO-token provider is implemented."
	expectedStatements := []string{
		"Inspection reports detected JavaScript challenges without executing solving.",
		"Downloads can optionally solve supported JavaScript challenges using -js-runtime.",
		"No PO-token provider is implemented.",
	}

	mockPlayerResponse := `{
		"videoDetails": {
			"videoId": "dQw4w9WgXcQ",
			"title": "Rick Astley - Never Gonna Give You Up",
			"lengthSeconds": "212"
		},
		"playabilityStatus": {
			"status": "OK"
		},
		"streamingData": {
			"formats": [
				{
					"itag": 18,
					"mimeType": "video/mp4; codecs=\"avc1.42001E, mp4a.40.2\"",
					"width": 640,
					"height": 360,
					"url": "https://media.test/video.mp4"
				}
			]
		}
	}`

	mockWatchHTML := fmt.Sprintf(`<!DOCTYPE html><html><head>
		<script>var ytInitialPlayerResponse = %s;</script>
		<script>ytcfg.set({"VISITOR_DATA":"test-visitor-token"});</script>
	</head><body></body></html>`, mockPlayerResponse)

	transport := mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/watch") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(mockWatchHTML)),
				Request:    req,
			}, nil
		}
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/youtubei/v1/player") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(mockPlayerResponse)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("404")),
			Request:    req,
		}, nil
	})

	testURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

	// 1. Text table inspection for web client
	t.Run("web_text", func(t *testing.T) {
		withMockTransport(t, transport, func() {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{"inspect", "-url", testURL, "-client", "web"}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected inspect error: %v", err)
			}
			out := stdout.String()
			if strings.Contains(out, staleLimitation) {
				t.Errorf("web text inspect output contains stale limitation: %q", staleLimitation)
			}
			for _, stmt := range expectedStatements {
				if !strings.Contains(out, stmt) {
					t.Errorf("web text inspect output missing expected statement: %q", stmt)
				}
			}
		})
	})

	// 2. Text table inspection for visionos client
	t.Run("visionos_text", func(t *testing.T) {
		withMockTransport(t, transport, func() {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{"inspect", "-url", testURL, "-client", "visionos"}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected inspect error: %v", err)
			}
			out := stdout.String()
			if strings.Contains(out, staleLimitation) {
				t.Errorf("visionos text inspect output contains stale limitation: %q", staleLimitation)
			}
			for _, stmt := range expectedStatements {
				if !strings.Contains(out, stmt) {
					t.Errorf("visionos text inspect output missing expected statement: %q", stmt)
				}
			}
		})
	})

	// 3. JSON inspection for all clients
	t.Run("all_json", func(t *testing.T) {
		withMockTransport(t, transport, func() {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), []string{"inspect", "-url", testURL, "-client", "all", "-json"}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected inspect json error: %v", err)
			}
			var parsed struct {
				SchemaVersion int `json:"schema_version"`
				Results       []struct {
					Client      string   `json:"client"`
					Status      string   `json:"status"`
					Limitations []string `json:"limitations"`
				} `json:"results"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
				t.Fatalf("failed to parse inspect JSON output: %v\nOutput was:\n%s", err, stdout.String())
			}
			if parsed.SchemaVersion != 1 {
				t.Fatalf("expected schema_version 1, got %d", parsed.SchemaVersion)
			}
			if len(parsed.Results) != 2 {
				t.Fatalf("expected 2 results (visionos and web), got %d", len(parsed.Results))
			}
			for _, res := range parsed.Results {
				for _, lim := range res.Limitations {
					if lim == staleLimitation {
						t.Errorf("json result for %s contains stale limitation: %q", res.Client, lim)
					}
				}
				for _, stmt := range expectedStatements {
					found := false
					for _, lim := range res.Limitations {
						if lim == stmt {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("json result for %s missing expected statement: %q", res.Client, stmt)
					}
				}
			}
		})
	})
}
